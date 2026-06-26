package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

func mergeTestStore(t *testing.T) *customnodetype.Store {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skip("set LLM_AGENT_STUDIO_PG_URL")
	}
	st, err := storage.Open(context.Background(), storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return customnodetype.New(st.GORM())
}

func TestResolveMergeNonDangerousOverride(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "fetch", Kind: "http", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	nodes := []planner.WorkflowNode{{
		ID: "n1", Type: "custom:fetch", TypeId: ct.ID, TypeVersion: 1,
		Parameters: json.RawMessage(`{"outputFormat":"json","url":"http://attacker/x"}`),
	}}
	res, err := resolveCustomTypes(context.Background(), store, org, nodes)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(res["n1"].Params, &got)
	if got["outputFormat"] != "json" {
		t.Errorf("non-dangerous override not applied: %v", got["outputFormat"])
	}
	if got["url"] != "https://api.example.com" {
		t.Errorf("RegistryOnly url override not denied: %v", got["url"])
	}
}

func TestResolveMergeUnknownTypeVersionFailsClosed(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"systemPrompt": "s", "userPrompt": "{{x}}", "outputFormat": "text"})
	ct, _ := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "llm", Kind: "llm", Params: base})
	nodes := []planner.WorkflowNode{{ID: "n1", Type: "custom:llm", TypeId: ct.ID, TypeVersion: 2}}
	if _, err := resolveCustomTypes(context.Background(), store, org, nodes); err == nil {
		t.Fatal("unknown typeVersion must fail closed, got nil error")
	}
}

func TestResolveMergeNoOverlayUnchanged(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"systemPrompt": "s", "userPrompt": "{{x}}", "outputFormat": "text"})
	ct, _ := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "llm", Kind: "llm", Params: base})
	nodes := []planner.WorkflowNode{{ID: "n1", Type: "custom:llm", TypeId: ct.ID}} // no Parameters/TypeVersion
	res, err := resolveCustomTypes(context.Background(), store, org, nodes)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(res["n1"].Params, &got)
	if got["systemPrompt"] != "s" || got["outputFormat"] != "text" {
		t.Errorf("old node (no overlay) regressed: %s", res["n1"].Params)
	}
}

// TestResolveMergeCrossTenantDenied (security, P-write-4) [DB]: org A references
// org B's typeId. The org-scoped registry lookup must fail closed (not found) —
// no cross-tenant type leakage, no params from B reachable under A.
func TestResolveMergeCrossTenantDenied(t *testing.T) {
	store := mergeTestStore(t)
	orgA, orgB := "org-A-"+t.Name(), "org-B-"+t.Name()
	base, _ := json.Marshal(map[string]any{"systemPrompt": "s", "userPrompt": "{{x}}", "outputFormat": "text"})
	ctB, err := store.Create(context.Background(), orgB, customnodetype.UpsertInput{Label: "llm", Kind: "llm", Params: base})
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}
	// org A editor references org B's typeId → resolve under orgA must fail closed.
	nodes := []planner.WorkflowNode{{ID: "n1", Type: "custom:llm", TypeId: ctB.ID, TypeVersion: 1,
		Parameters: json.RawMessage(`{"outputFormat":"json"}`)}}
	res, err := resolveCustomTypes(context.Background(), store, orgA, nodes)
	if err == nil {
		t.Fatal("cross-tenant typeId reference must fail closed")
	}
	if res != nil {
		t.Fatalf("cross-tenant resolve must return nil map, got %v", res)
	}
	// Zero leakage: org B's params (systemPrompt "s") must NOT appear in the error.
	if strings.Contains(err.Error(), "systemPrompt") || strings.Contains(err.Error(), `"s"`) {
		t.Fatalf("error leaked cross-tenant params: %v", err)
	}
}

// TestSaveCrossTenantOverlayDenied (security, P-write-4) [DB]: the SAVE-time
// overlay validator (validateNodeParameterOverlays, W1/W2) must also fail closed
// on a cross-tenant typeId — opaque error, zero leakage.
func TestSaveCrossTenantOverlayDenied(t *testing.T) {
	store := mergeTestStore(t)
	orgA, orgB := "org-A-"+t.Name(), "org-B-"+t.Name()
	base, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://secret-internal.example.com", "outputFormat": "text"})
	ctB, err := store.Create(context.Background(), orgB, customnodetype.UpsertInput{Label: "fetch", Kind: "http", Params: base})
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}
	nodes := []planner.WorkflowNode{{ID: "n1", Type: "custom:fetch", TypeId: ctB.ID, TypeVersion: 1,
		Parameters: json.RawMessage(`{"outputFormat":"json"}`)}}
	err = validateNodeParameterOverlays(context.Background(), store, orgA, nodes)
	if err == nil {
		t.Fatal("save-time cross-tenant typeId must fail closed")
	}
	// Opaque: org B's internal URL must NOT leak into the surfaced error.
	if strings.Contains(err.Error(), "secret-internal") {
		t.Fatalf("save-time error leaked cross-tenant url: %v", err)
	}
}

func TestResolveMergeRejectsIllegalMergedValue(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	ct, _ := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "fetch", Kind: "http", Params: base})
	// outputFormat is description-known + non-RegistryOnly, so it merges in — but
	// "xml" is not a legal enum value. The full validator must reject it.
	nodes := []planner.WorkflowNode{{
		ID: "n1", Type: "custom:fetch", TypeId: ct.ID, TypeVersion: 1,
		Parameters: json.RawMessage(`{"outputFormat":"xml"}`),
	}}
	if _, err := resolveCustomTypes(context.Background(), store, org, nodes); err == nil {
		t.Fatal("illegal merged value (outputFormat=xml) must be rejected at run-time resolve")
	}
}
