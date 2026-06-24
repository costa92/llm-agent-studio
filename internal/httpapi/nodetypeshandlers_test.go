package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/nodedesc"
)

// ntListStub implements CustomNodeTypeStore returning a fixed List result; the
// other methods are unused by nodeTypesHandler (it only calls List).
type ntListStub struct {
	rows []customnodetype.CustomNodeType
}

func (s ntListStub) List(_ context.Context, _ string) ([]customnodetype.CustomNodeType, error) {
	return s.rows, nil
}
func (ntListStub) Create(_ context.Context, _ string, _ customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{}, nil
}
func (ntListStub) Update(_ context.Context, _, _ string, _ customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{}, nil
}
func (ntListStub) Delete(_ context.Context, _, _ string) error { return nil }
func (ntListStub) Get(_ context.Context, _, _ string) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{}, nil
}

type nodeTypesResp struct {
	Version   int                            `json:"version"`
	NodeTypes []nodedesc.NodeTypeDescription `json:"nodeTypes"`
}

func getNodeTypes(t *testing.T, s CustomNodeTypeStore) nodeTypesResp {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/orgs/org1/node-types", nil)
	req.SetPathValue("org", "org1")
	rec := httptest.NewRecorder()
	nodeTypesHandler(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out nodeTypesResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body.String())
	}
	return out
}

func findType(types []nodedesc.NodeTypeDescription, typ string) (nodedesc.NodeTypeDescription, bool) {
	for _, d := range types {
		if d.Type == typ {
			return d, true
		}
	}
	return nodedesc.NodeTypeDescription{}, false
}

func TestNodeTypesEnvelopeAndBuiltins(t *testing.T) {
	out := getNodeTypes(t, ntListStub{})
	if out.Version != nodedesc.Version {
		t.Errorf("version = %d, want %d", out.Version, nodedesc.Version)
	}
	if len(out.NodeTypes) != len(nodedesc.Builtins()) {
		t.Errorf("nodeTypes len = %d, want %d (builtins only)", len(out.NodeTypes), len(nodedesc.Builtins()))
	}
}

func TestNodeTypesMergesCustomRowAsBaseKind(t *testing.T) {
	row := customnodetype.CustomNodeType{
		ID:     "c1",
		Slug:   "translate",
		Label:  "翻译",
		Kind:   "llm",
		Params: json.RawMessage(`{"userPrompt":"译: {{text}}","model":"gpt-4o"}`),
	}
	out := getNodeTypes(t, ntListStub{rows: []customnodetype.CustomNodeType{row}})

	if len(out.NodeTypes) != len(nodedesc.Builtins())+1 {
		t.Fatalf("nodeTypes len = %d, want %d", len(out.NodeTypes), len(nodedesc.Builtins())+1)
	}
	d, ok := findType(out.NodeTypes, "custom:translate")
	if !ok {
		t.Fatalf("custom:translate not present; got %v", out.NodeTypes)
	}
	if d.Label != "翻译" {
		t.Errorf("label = %q, want 翻译", d.Label)
	}
	// Carries the llm base properties.
	llmBase, _ := findType(nodedesc.Builtins(), "llm")
	if len(d.Properties) != len(llmBase.Properties) {
		t.Errorf("properties len = %d, want %d (llm base)", len(d.Properties), len(llmBase.Properties))
	}
	// userPrompt property's Default projected from the row param.
	var found bool
	for _, p := range d.Properties {
		if p.Name == "userPrompt" {
			found = true
			var got string
			if err := json.Unmarshal(p.Default, &got); err != nil {
				t.Fatalf("userPrompt default unmarshal: %v (raw %s)", err, p.Default)
			}
			if got != "译: {{text}}" {
				t.Errorf("userPrompt default = %q, want 译: {{text}}", got)
			}
		}
	}
	if !found {
		t.Error("userPrompt property not found in custom:translate")
	}

	// Built-in llm must NOT be corrupted by the projection (deep-copy guard).
	llmAfter, _ := findType(nodedesc.Builtins(), "llm")
	for _, p := range llmAfter.Properties {
		if p.Name == "userPrompt" && len(p.Default) != 0 {
			t.Errorf("built-in llm userPrompt default corrupted: %s", p.Default)
		}
	}
}

func TestNodeTypesBuiltinWinsAndRejectsReservedCustom(t *testing.T) {
	rows := []customnodetype.CustomNodeType{
		{ID: "a", Slug: "script", Label: "impostor", Kind: "script", Params: json.RawMessage(`{}`)},
		{ID: "b", Slug: "studio.script", Label: "impostor2", Kind: "script", Params: json.RawMessage(`{}`)},
	}
	out := getNodeTypes(t, ntListStub{rows: rows})

	if len(out.NodeTypes) != len(nodedesc.Builtins()) {
		t.Errorf("nodeTypes len = %d, want %d (reserved rows dropped)", len(out.NodeTypes), len(nodedesc.Builtins()))
	}
	if _, ok := findType(out.NodeTypes, "custom:script"); ok {
		t.Error("custom:script leaked (reserved namespace not dropped)")
	}
	if _, ok := findType(out.NodeTypes, "custom:studio.script"); ok {
		t.Error("custom:studio.script leaked (reserved namespace not dropped)")
	}
	// Built-in script keeps its real label, not the impostor's.
	d, ok := findType(out.NodeTypes, "script")
	if !ok {
		t.Fatal("built-in script missing")
	}
	if d.Label == "impostor" {
		t.Error("built-in script label was shadowed by custom row")
	}
}

func TestNodeTypesMergeOverRealStore(t *testing.T) {
	db := modelTestGorm(t) // skips when LLM_AGENT_STUDIO_PG_URL unset
	store := customnodetype.New(db)
	const org = "ntorg1"
	_, err := store.Create(context.Background(), org, customnodetype.UpsertInput{
		Label:  "翻译",
		Kind:   "llm",
		Params: json.RawMessage(`{"userPrompt":"译: {{text}}","model":"gpt-4o"}`),
	})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/orgs/"+org+"/node-types", nil)
	req.SetPathValue("org", org)
	rec := httptest.NewRecorder()
	nodeTypesHandler(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out nodeTypesResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.NodeTypes) != len(nodedesc.Builtins())+1 {
		t.Errorf("nodeTypes len = %d, want %d", len(out.NodeTypes), len(nodedesc.Builtins())+1)
	}
}
