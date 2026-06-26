# P-write (P4) Authoring Write-Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the canvas `<PropertiesForm>` actually persist per-node parameter overrides — written to the on-disk `WorkflowNode` envelope, read back at plan/run time, and gated by a registry-only danger filter validated at BOTH save and run.

**Architecture:** Backend lands first (D-2): `WorkflowNode` gains `Parameters`/`TypeVersion`; the merge + danger-filter lives at the single resolve choke point (`resolveCustomTypes`), keyed by resolved `kind` + `typeVersion`, allow-listing only description-known non-`RegistryOnly` keys; the full `validate*` validators (extracted from `customnodetype`) run on the merged blob at save (both editor write paths) and again at run (worker dispatch). Only then does the frontend round-trip (`toStudioNodes` preserve-unknown) and the editable `onChange` ship — in that order, because a stale FE bundle that strips `parameters` is active data corruption (M4).

**Tech Stack:** Go (stdlib `encoding/json`, GORM/pgx, Postgres JSONB), React + TypeScript (`@xyflow/react`), Vitest, `go test`.

---

## Source spec

`docs/superpowers/specs/2026-06-25-workflow-v2-p4-pwrite-authoring-design.md` (commit 127d2d8). This plan implements §3–§8 of that spec. Anchor references (D-1/D-2, S-1, M1–M4, B1, B-A1, B-A7) are the spec's finding numbers.

## Load-bearing ordering (spec §8)

1. **P-write-1** — backend envelope + resolve-layer read path. No frontend write yet (D-2: read path must land before any value is written, or written values strand in JSONB).
2. **P-write-2** — extract full `validate*` to call on an arbitrary param blob + worker run-time revalidation (S-1: run-time is the authoritative last line; W3 backfill structurally bypasses save).
3. **P-write-3** — frontend `toStudioNodes` round-trip + preserve-unknown. MUST ship before/with P-write-4 (M4: a stale FE bundle without this strips `parameters` on every save = active data corruption).
4. **P-write-4** — editable `onChange` → persist + save-time validation on BOTH editor write paths. Gated by an independent security review (§6.5 / §9 open-1).

---

## File Structure

| File | P-write step | Responsibility |
|---|---|---|
| `internal/planner/planner.go` | 1 | Add `Parameters json.RawMessage` + `TypeVersion int` to `WorkflowNode` (envelope, ~:147). PlanCustom stays store-thin (unchanged). |
| `internal/nodedesc/types.go` | 1 | Add `RegistryOnly bool` to `Constraints` (~:90). |
| `internal/nodedesc/builtin.go` | 1 | Set `RegistryOnly:true` on http `url`/`headers`/`bodyTemplate`/`allowResponseBody` + script `code` (~:107–120). |
| `internal/nodedesc/merge.go` (new) | 1 | `DescByKind(kind string, typeVersion int) (NodeTypeDescription, bool)` + `MergeOverlay(base, overlay json.RawMessage, desc NodeTypeDescription) (json.RawMessage, error)` — allow-list-by-description ∩ non-RegistryOnly, drop-unknown, default-deny RegistryOnly. Leaf pkg (stdlib only). |
| `internal/httpapi/handlers.go` | 1, 4 | `resolveCustomTypes` (~:100): inject `TypeVersion` description select + `MergeOverlay` + validator call. `createProject` (~:294, W2): save-time validation. |
| `internal/customnodetype/validate.go` (new) | 2 | Extract `ValidateParams(kind string, params json.RawMessage) error` callable on an arbitrary flat blob (today bound to `UpsertInput` in `store.go:80`). |
| `internal/customnodetype/store.go` | 2 | Re-point `validate()` at the extracted entry (no behavior change). |
| `internal/worker/worker.go` | 2 | Run-time revalidation entry `revalidateCustomParams` called from `runCustom` (~:1705) before dispatch; merge existing scatter checks (~:1908/1912/1969). |
| `internal/httpapi/workflowhandlers.go` | 4 | `createWorkflowHandler` (~:47) + `updateWorkflowHandler` (~:77) (W1): save-time validation. |
| `web/src/lib/types.ts` | 3 | Add `parameters?` + `typeVersion?` to `WorkflowNode` (~:106). |
| `web/src/features/workflow-canvas/canvasModel.ts` | 3 | `toStudioNodes` (~:85) full-node round-trip + preserve-unknown. |
| `web/src/features/workflow-canvas/canvasModel.parameters.test.ts` (new) | 3 | B-A1 parity test (Vitest). |
| `web/src/features/workflow-canvas/PropertiesPanel.tsx` | 4 | Flip `onChange={() => {}}` (~:284) to `onPatch({ parameters, typeVersion })`. |

---

## Test environment (DB-backed Go tests)

Tasks marked **[DB]** need a FRESH Postgres database (this repo's iron rule: dirty data collides with transient unique indexes + parallel-migrate races):

```bash
# host 172.17.0.3:5432, user postgres, pw `pw`
DB=pwrite_$(date +%s)
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "CREATE DATABASE $DB;"
export LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@172.17.0.3:5432/$DB?sslmode=disable"
# ... run tests with GOWORK=off ... -p 1 ...
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "DROP DATABASE $DB;"
```

All Go test commands use `GOWORK=off` and `-count=1`. The worker suite is slow (~120s) — budget for it. Frontend: `cd web && npm run test` (Vitest) + `npx tsc --noEmit`.

---

# P-write-1 — Backend envelope + resolve-layer read path

> No frontend writes these fields yet. Purely additive, zero regression. Verifies spec tests 2, 4, 6. **No data migration** (§3.3): `workflows.nodes` stores graph + authoring intent, not execution state — every run re-plans via PlanCustom, projecting nodes into `todos.input_json`. Old rows lacking `parameters` take the "no overlay" branch and produce byte-identical `{kind, params}` to today. New fields are pure additive authoring metadata.

### Task 1: Add `Parameters` + `TypeVersion` to `WorkflowNode`

**Files:**
- Modify: `internal/planner/planner.go:146-166`
- Test: `internal/planner/planner_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/planner/planner_test.go`:

```go
func TestWorkflowNodeParametersRoundTrip(t *testing.T) {
	in := WorkflowNode{
		ID:          "node-3",
		Type:        "custom:my-llm",
		TypeId:      "a1b2c3",
		DependsOn:   []string{"script-1"},
		TypeVersion: 1,
		Parameters:  json.RawMessage(`{"temperature":0.2,"outputFormat":"json"}`),
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out WorkflowNode
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.TypeVersion != 1 {
		t.Fatalf("typeVersion lost: %d", out.TypeVersion)
	}
	if string(out.Parameters) != `{"temperature":0.2,"outputFormat":"json"}` {
		t.Fatalf("parameters lost: %s", out.Parameters)
	}
	// omitempty: a node without the new fields must NOT emit the keys.
	bare, _ := json.Marshal(WorkflowNode{ID: "n", Type: "script", DependsOn: []string{}})
	if strings.Contains(string(bare), "typeVersion") || strings.Contains(string(bare), "parameters") {
		t.Fatalf("omitempty broken: %s", bare)
	}
}
```

Ensure `planner_test.go` imports `encoding/json` and `strings` (add if missing).

- [ ] **Step 2: Run test, see it fail**

Run: `GOWORK=off go test ./internal/planner/... -run TestWorkflowNodeParametersRoundTrip -count=1`
Expected: FAIL — `out.TypeVersion undefined` / `out.Parameters undefined` (compile error).

- [ ] **Step 3: Add the two fields**

In `internal/planner/planner.go`, inside `WorkflowNode` (after `VarBindings []CustomVariable` at :165), add:

```go
	// TypeVersion records the description.Version pinned at placement/save time.
	// The resolve layer selects the description by (resolved kind, TypeVersion);
	// an unknown TypeVersion fails closed (spec §4.3) — never a silent v1 fallback.
	TypeVersion int `json:"typeVersion,omitempty"`
	// Parameters is the serialized PropertiesForm value object: NON-dangerous
	// per-node overrides only. RegistryOnly/dangerous fields stay in the registry
	// (spec §6) and are filtered out at the resolve choke point.
	Parameters json.RawMessage `json:"parameters,omitempty"`
```

Confirm `encoding/json` is already imported in `planner.go` (it is — used by `ResolvedType`).

- [ ] **Step 4: Run test, see it pass**

Run: `GOWORK=off go test ./internal/planner/... -run TestWorkflowNodeParametersRoundTrip -count=1`
Expected: PASS (ok).

- [ ] **Step 5: Run the full planner suite (no regression)**

Run: `GOWORK=off go test ./internal/planner/... -count=1`
Expected: PASS — PlanCustom unchanged; old nodes unaffected.

- [ ] **Step 6: Commit**

```bash
git add internal/planner/planner.go internal/planner/planner_test.go
git commit -m "feat(planner): add Parameters + TypeVersion to WorkflowNode envelope (P-write-1)"
```

### Task 2: Add `RegistryOnly` to `nodedesc.Constraints` + mark dangerous builtins

**Files:**
- Modify: `internal/nodedesc/types.go:90-94`
- Modify: `internal/nodedesc/builtin.go:107-120`
- Test: `internal/nodedesc/builtin_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/nodedesc/builtin_test.go`:

```go
func TestRegistryOnlyMarkedOnDangerousFields(t *testing.T) {
	want := map[string]map[string]bool{
		"http":   {"url": true, "headers": true, "bodyTemplate": true, "allowResponseBody": true},
		"script": {"code": true},
	}
	for _, d := range Builtins() {
		exp, ok := want[d.Type]
		if !ok {
			continue
		}
		got := map[string]bool{}
		for _, p := range d.Properties {
			if p.Constraints != nil && p.Constraints.RegistryOnly {
				got[p.Name] = true
			}
		}
		for name := range exp {
			if !got[name] {
				t.Errorf("%s.%s must be RegistryOnly", d.Type, name)
			}
		}
		// allowResponseBody is a PLAIN bool with no other constraint — assert the
		// marker still lands (spec §6.3: the no-constraint exfil-launcher hole).
		if d.Type == "http" && !got["allowResponseBody"] {
			t.Error("http.allowResponseBody (no other constraint) must still be RegistryOnly")
		}
	}
}
```

- [ ] **Step 2: Run test, see it fail**

Run: `GOWORK=off go test ./internal/nodedesc/... -run TestRegistryOnlyMarkedOnDangerousFields -count=1`
Expected: FAIL — `p.Constraints.RegistryOnly undefined` (compile error).

- [ ] **Step 3: Add the field**

In `internal/nodedesc/types.go`, change `Constraints` (:90-94) to:

```go
type Constraints struct {
	NoTemplate      bool     `json:"noTemplate,omitempty"`
	NoSecret        bool     `json:"noSecret,omitempty"`
	SecretAllowedIn []string `json:"secretAllowedIn,omitempty"`
	// RegistryOnly marks a field that a per-node parameters overlay may NEVER set
	// (spec §6.3, M1). The single source of truth for "danger" — covers the
	// no-constraint exfil launcher (http.allowResponseBody) that no danger
	// Constraint alone would catch.
	RegistryOnly bool `json:"registryOnly,omitempty"`
}
```

- [ ] **Step 4: Mark the dangerous builtins**

In `internal/nodedesc/builtin.go`, the http node (:107-112) and script node (:120):

```go
{Name: "url", Label: "URL", Type: PropertyString, Required: true, Constraints: &Constraints{NoTemplate: true, RegistryOnly: true}},
{Name: "headers", Label: "请求头", Type: PropertyKeyValue, Constraints: &Constraints{SecretAllowedIn: []string{"headers"}, RegistryOnly: true}},
{Name: "bodyTemplate", Label: "请求体模板", Type: PropertyTextarea, Constraints: &Constraints{NoSecret: true, RegistryOnly: true}, TypeOptions: &TypeOptions{Rows: 3}},
```
```go
{Name: "allowResponseBody", Label: "允许显示响应体", Type: PropertyBoolean, Default: raw(`false`), Constraints: &Constraints{RegistryOnly: true}},
```
```go
{Name: "code", Label: "脚本代码", Type: PropertyCode, Required: true, Constraints: &Constraints{NoSecret: true, RegistryOnly: true}, TypeOptions: &TypeOptions{Editor: "starlark", Rows: 8}},
```

- [ ] **Step 5: Run test, see it pass**

Run: `GOWORK=off go test ./internal/nodedesc/... -run TestRegistryOnlyMarkedOnDangerousFields -count=1`
Expected: PASS.

- [ ] **Step 6: Run the nodedesc suite + the Go↔TS parity guard note**

Run: `GOWORK=off go test ./internal/nodedesc/... -count=1`
Expected: PASS. (The TS `Constraints.registryOnly` mirror is added in P-write-3 Task 8 — `nodeDesc.parity.test.ts` only asserts the keys it knows about, so it stays green here.)

- [ ] **Step 7: Commit**

```bash
git add internal/nodedesc/types.go internal/nodedesc/builtin.go internal/nodedesc/builtin_test.go
git commit -m "feat(nodedesc): RegistryOnly marker on dangerous builtin fields (P-write-1)"
```

### Task 3: `DescByKind` + `MergeOverlay` (allow-list-by-description ∩ non-RegistryOnly)

**Files:**
- Create: `internal/nodedesc/merge.go`
- Test: `internal/nodedesc/merge_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/nodedesc/merge_test.go`:

```go
package nodedesc

import (
	"encoding/json"
	"testing"
)

func descFor(t *testing.T, kind string) NodeTypeDescription {
	t.Helper()
	d, ok := DescByKind(kind, 1)
	if !ok {
		t.Fatalf("DescByKind(%q,1) not found", kind)
	}
	return d
}

func TestDescByKindUnknownVersionFailsClosed(t *testing.T) {
	if _, ok := DescByKind("http", 2); ok {
		t.Fatal("DescByKind must return ok=false for unknown typeVersion (fail-closed)")
	}
	if _, ok := DescByKind("http", 0); !ok {
		t.Fatal("typeVersion 0 (omitempty / old node) must default to v1")
	}
}

func TestMergeOverlayAllowListNonDangerous(t *testing.T) {
	desc := descFor(t, "llm")
	base := json.RawMessage(`{"systemPrompt":"sys","userPrompt":"{{x}}","outputFormat":"text"}`)
	overlay := json.RawMessage(`{"outputFormat":"json","temperature":0.2}`)
	merged, err := MergeOverlay(base, overlay, desc)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(merged, &got)
	if got["outputFormat"] != "json" {
		t.Errorf("non-dangerous override not applied: %v", got["outputFormat"])
	}
	if got["temperature"].(float64) != 0.2 {
		t.Errorf("known key temperature not applied: %v", got["temperature"])
	}
	if got["systemPrompt"] != "sys" {
		t.Errorf("base key lost: %v", got["systemPrompt"])
	}
}

func TestMergeOverlayDropsUnknownKey(t *testing.T) {
	desc := descFor(t, "llm")
	base := json.RawMessage(`{"outputFormat":"text"}`)
	overlay := json.RawMessage(`{"outputFormat":"json","bogusKey":"x"}`)
	merged, _ := MergeOverlay(base, overlay, desc)
	var got map[string]any
	_ = json.Unmarshal(merged, &got)
	if _, present := got["bogusKey"]; present {
		t.Error("unknown key must be dropped from runtime params (drop-unknown, M2)")
	}
}

func TestMergeOverlayDefaultDeniesRegistryOnly(t *testing.T) {
	desc := descFor(t, "http")
	base := json.RawMessage(`{"method":"GET","url":"https://api.example.com","allowResponseBody":false}`)
	overlay := json.RawMessage(`{"url":"http://attacker/collect","allowResponseBody":true}`)
	merged, _ := MergeOverlay(base, overlay, desc)
	var got map[string]any
	_ = json.Unmarshal(merged, &got)
	if got["url"] != "https://api.example.com" {
		t.Errorf("RegistryOnly url overlay not denied: %v", got["url"])
	}
	if got["allowResponseBody"] != false {
		t.Errorf("RegistryOnly allowResponseBody overlay not denied: %v", got["allowResponseBody"])
	}
}

func TestMergeOverlayEmptyOverlayByteIdentical(t *testing.T) {
	desc := descFor(t, "llm")
	base := json.RawMessage(`{"systemPrompt":"sys","outputFormat":"text"}`)
	merged, err := MergeOverlay(base, nil, desc)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	// No overlay → merged must round-trip to base map (no regression for old nodes).
	var a, b map[string]any
	_ = json.Unmarshal(base, &a)
	_ = json.Unmarshal(merged, &b)
	if len(a) != len(b) || b["systemPrompt"] != "sys" || b["outputFormat"] != "text" {
		t.Errorf("empty overlay changed base: %s", merged)
	}
}
```

- [ ] **Step 2: Run test, see it fail**

Run: `GOWORK=off go test ./internal/nodedesc/... -run TestMergeOverlay -count=1`
Expected: FAIL — `DescByKind`/`MergeOverlay` undefined.

- [ ] **Step 3: Implement `merge.go`**

Create `internal/nodedesc/merge.go`:

```go
package nodedesc

import (
	"encoding/json"
	"fmt"
)

// DescByKind selects a builtin description by its base kind (llm/http/script) and
// the node's pinned TypeVersion. typeVersion 0 (omitempty / old node) defaults to
// the current Version. A typeVersion that matches no known description version
// returns ok=false — callers MUST fail closed (spec §4.3 / D-1): never silently
// fall back, because that would select the wrong danger classification.
func DescByKind(kind string, typeVersion int) (NodeTypeDescription, bool) {
	if typeVersion == 0 {
		typeVersion = Version
	}
	if typeVersion != Version {
		return NodeTypeDescription{}, false
	}
	for _, d := range builtins {
		if d.Type == kind {
			return d, true
		}
	}
	return NodeTypeDescription{}, false
}

// MergeOverlay merges a per-node parameters overlay onto the registry base params,
// keyed by the description. inject_keys = {description-known property names} ∩
// {non-RegistryOnly}. Unknown keys are dropped (M2). RegistryOnly keys are
// default-denied — base wins (M1/M4). A nil/empty overlay returns base unchanged.
// Value legality (enums, cross-field) is NOT checked here — callers run the full
// validate* on the result (spec §4.2 / §6.3, m1).
func MergeOverlay(base, overlay json.RawMessage, desc NodeTypeDescription) (json.RawMessage, error) {
	var merged map[string]json.RawMessage
	if len(base) == 0 {
		merged = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(base, &merged); err != nil {
		return nil, fmt.Errorf("nodedesc: merge base: %w", err)
	}
	if len(overlay) == 0 {
		return base, nil
	}
	var ov map[string]json.RawMessage
	if err := json.Unmarshal(overlay, &ov); err != nil {
		return nil, fmt.Errorf("nodedesc: merge overlay: %w", err)
	}
	injectable := map[string]bool{}
	for _, p := range desc.Properties {
		if p.Constraints != nil && p.Constraints.RegistryOnly {
			continue // default-deny RegistryOnly
		}
		injectable[p.Name] = true // description-known AND non-RegistryOnly
	}
	for k, v := range ov {
		if injectable[k] {
			merged[k] = v
		}
		// else: unknown key (drop-unknown) OR RegistryOnly (default-deny) → ignored.
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("nodedesc: merge marshal: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test, see it pass**

Run: `GOWORK=off go test ./internal/nodedesc/... -run "TestDescByKind|TestMergeOverlay" -count=1`
Expected: PASS (all 5 sub-tests).

- [ ] **Step 5: Commit**

```bash
git add internal/nodedesc/merge.go internal/nodedesc/merge_test.go
git commit -m "feat(nodedesc): DescByKind + MergeOverlay allow-list merge with RegistryOnly default-deny (P-write-1)"
```

### Task 4: Wire merge into `resolveCustomTypes` [DB]

> `resolveCustomTypes` is the single choke point both run paths pass through (`runHandler` @ handlers.go:460, `runWorkflowHandler` @ workflowhandlers.go:186). It already holds `ct.Kind` + org + registry. PlanCustom stays byte-for-byte unchanged — it keeps consuming `rt.Params`, which is now the merged result. Value validation (the full `validate*`) is added in P-write-2; here we wire description-select + merge + the fail-closed typeVersion path. **Validator hook is a no-op stub here** (returns nil) and is filled in P-write-2 Task 6 — note this in the code comment so the executor wires the real call there.

**Files:**
- Modify: `internal/httpapi/handlers.go:96-116`
- Test: `internal/httpapi/nodetypeshandlers_test.go` (or a new `resolvemerge_test.go`)

- [ ] **Step 1: Write the failing test** (DB-backed via `customnodetype.Store`)

Create `internal/httpapi/resolvemerge_test.go`. Use the existing test-DB helper pattern (see `customnodetype/store_test.go:testGorm`); construct a `customnodetype.Store`, seed an http type, then call `resolveCustomTypes`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"os"
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
```

- [ ] **Step 2: Run test, see it fail** [DB]

```bash
DB=pwrite_$(date +%s); PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "CREATE DATABASE $DB;"
export LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@172.17.0.3:5432/$DB?sslmode=disable"
GOWORK=off go test ./internal/httpapi/... -run TestResolveMerge -count=1 -p 1
```
Expected: FAIL — `url`/`allowResponseBody` overlay applied (no merge yet) / typeVersion=2 does not error.

- [ ] **Step 3: Wire merge into `resolveCustomTypes`**

In `internal/httpapi/handlers.go`, add `nodedesc` to imports, then replace the loop body (:113):

```go
		ct, err := res.Get(ctx, n.TypeId, orgID)
		if err != nil {
			return nil, fmt.Errorf("custom node %q: resolve type %q: %w", n.ID, n.TypeId, err)
		}
		merged := ct.Params
		if len(n.Parameters) > 0 || n.TypeVersion != 0 {
			desc, ok := nodedesc.DescByKind(ct.Kind, n.TypeVersion)
			if !ok {
				// Fail closed (spec §4.3): unknown typeVersion would mis-select the
				// danger classification. Never silently fall back to v1.
				return nil, fmt.Errorf("custom node %q: typeVersion %d unsupported (max %d); please upgrade", n.ID, n.TypeVersion, nodedesc.Version)
			}
			m, mErr := nodedesc.MergeOverlay(ct.Params, n.Parameters, desc)
			if mErr != nil {
				return nil, fmt.Errorf("custom node %q: merge params: %w", n.ID, mErr)
			}
			// P-write-2 Task 6 inserts the full validator call HERE on `m`
			// (customnodetype.ValidateParams(ct.Kind, m)) before assigning.
			merged = m
		}
		resolved[n.ID] = planner.ResolvedType{Kind: ct.Kind, Params: merged}
```

- [ ] **Step 4: Run test, see it pass** [DB]

Run (same exported `LLM_AGENT_STUDIO_PG_URL`): `GOWORK=off go test ./internal/httpapi/... -run TestResolveMerge -count=1 -p 1`
Expected: PASS (3 tests).

- [ ] **Step 5: Run the httpapi suite (no regression), then drop the DB** [DB]

```bash
GOWORK=off go test ./internal/httpapi/... -count=1 -p 1
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "DROP DATABASE $DB;"
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi/handlers.go internal/httpapi/resolvemerge_test.go
git commit -m "feat(httpapi): merge per-node parameters at resolveCustomTypes choke point, fail-closed on unknown typeVersion (P-write-1)"
```

---

# P-write-2 — Extractable `validate*` + worker run-time revalidation

> Security work (S-1). The merge in P-write-1 filters WHICH keys may be overridden; the validators here check WHETHER the merged values are legal (enums, url-no-`{{`, no `{{secret:}}` in body/code). Run-time revalidation is the authoritative last line: W3 backfill (`storage.go:390-395`) and any future writer structurally bypass save-time validation.

### Task 5: Extract `ValidateParams(kind, params)` from `customnodetype`

**Files:**
- Create: `internal/customnodetype/validate.go`
- Modify: `internal/customnodetype/store.go:80-155` (re-point `validate`, move the two validators)
- Test: `internal/customnodetype/validate_test.go`

- [ ] **Step 1: Write the failing test** (no DB — pure functions)

Create `internal/customnodetype/validate_test.go`:

```go
package customnodetype

import (
	"encoding/json"
	"testing"
)

func TestValidateParamsHTTP(t *testing.T) {
	ok, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	if err := ValidateParams("http", ok); err != nil {
		t.Fatalf("valid http rejected: %v", err)
	}
	bad, _ := json.Marshal(map[string]any{"method": "GET", "url": "http://x/{{y}}"})
	if err := ValidateParams("http", bad); err == nil {
		t.Fatal("templated url must be rejected")
	}
	secretBody, _ := json.Marshal(map[string]any{"method": "POST", "url": "https://x", "bodyTemplate": "{{secret:K}}"})
	if err := ValidateParams("http", secretBody); err == nil {
		t.Fatal("{{secret:}} in body must be rejected")
	}
}

func TestValidateParamsScript(t *testing.T) {
	ok, _ := json.Marshal(map[string]any{"code": "print(1)", "outputFormat": "text"})
	if err := ValidateParams("script", ok); err != nil {
		t.Fatalf("valid script rejected: %v", err)
	}
	bad, _ := json.Marshal(map[string]any{"code": "x = {{secret:K}}"})
	if err := ValidateParams("script", bad); err == nil {
		t.Fatal("{{secret:}} in code must be rejected")
	}
}

func TestValidateParamsLLMNoChecks(t *testing.T) {
	// llm has no hardcoded validator today — accept any valid JSON.
	p, _ := json.Marshal(map[string]any{"outputFormat": "json"})
	if err := ValidateParams("llm", p); err != nil {
		t.Fatalf("llm params rejected: %v", err)
	}
}
```

- [ ] **Step 2: Run test, see it fail**

Run: `GOWORK=off go test ./internal/customnodetype/... -run TestValidateParams -count=1`
Expected: FAIL — `ValidateParams` undefined.

- [ ] **Step 3: Create `validate.go` with the extracted entry + move the two validators**

Create `internal/customnodetype/validate.go`. Move `validateHTTPParams` (store.go:102-132) and `validateScriptParams` (store.go:137-155) verbatim into it, and add the dispatch entry:

```go
package customnodetype

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateParams runs the kind's full save-time/runtime validator on an arbitrary
// flat param blob (NOT bound to UpsertInput). The single source of truth for value
// legality, reused at: registry save (store.go), per-node merge (httpapi resolve),
// and worker run-time revalidation. llm has no hardcoded checks (any valid JSON).
func ValidateParams(kind string, params json.RawMessage) error {
	if len(params) == 0 || !json.Valid(params) {
		return fmt.Errorf("customnodetype: params must be valid JSON")
	}
	switch kind {
	case "http":
		return validateHTTPParams(params)
	case "script":
		return validateScriptParams(params)
	default:
		return nil
	}
}

// validateHTTPParams ... (moved verbatim from store.go:99-132)
func validateHTTPParams(raw json.RawMessage) error {
	var p struct {
		Method       string            `json:"method"`
		URL          string            `json:"url"`
		Headers      map[string]string `json:"headers"`
		BodyTemplate string            `json:"bodyTemplate"`
		OutputFormat string            `json:"outputFormat"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("customnodetype: http params: %w", err)
	}
	if !httpMethods[p.Method] {
		return fmt.Errorf("customnodetype: http method %q invalid (GET|POST|PUT|PATCH|DELETE)", p.Method)
	}
	if strings.TrimSpace(p.URL) == "" {
		return fmt.Errorf("customnodetype: http url required")
	}
	if strings.Contains(p.URL, "{{") {
		return fmt.Errorf("customnodetype: http url must be a static literal (no {{...}} templates)")
	}
	if secretRefRe.MatchString(p.BodyTemplate) {
		return fmt.Errorf("customnodetype: {{secret:...}} not allowed in bodyTemplate (headers only)")
	}
	if p.OutputFormat != "" && p.OutputFormat != "text" && p.OutputFormat != "json" {
		return fmt.Errorf("customnodetype: http outputFormat %q invalid (text|json)", p.OutputFormat)
	}
	return nil
}

// validateScriptParams ... (moved verbatim from store.go:134-155)
func validateScriptParams(raw json.RawMessage) error {
	var p struct {
		Code         string `json:"code"`
		OutputFormat string `json:"outputFormat"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("customnodetype: script params: %w", err)
	}
	if strings.TrimSpace(p.Code) == "" {
		return fmt.Errorf("customnodetype: script code required")
	}
	if secretRefRe.MatchString(p.Code) {
		return fmt.Errorf("customnodetype: {{secret:...}} not allowed in script code")
	}
	if p.OutputFormat != "" && p.OutputFormat != "text" && p.OutputFormat != "json" {
		return fmt.Errorf("customnodetype: script outputFormat %q invalid (text|json)", p.OutputFormat)
	}
	return nil
}
```

Then in `internal/customnodetype/store.go`: DELETE the moved `validateHTTPParams` (:99-132) and `validateScriptParams` (:134-155), and change `validate` (:80-97) to delegate:

```go
func validate(in UpsertInput) error {
	if strings.TrimSpace(in.Label) == "" {
		return fmt.Errorf("customnodetype: label required")
	}
	if !validKinds[in.Kind] {
		return fmt.Errorf("customnodetype: invalid kind %q (want llm|http|script)", in.Kind)
	}
	return ValidateParams(in.Kind, in.Params)
}
```

- [ ] **Step 4: Run test, see it pass**

Run: `GOWORK=off go test ./internal/customnodetype/... -run TestValidateParams -count=1`
Expected: PASS.

- [ ] **Step 5: Run the customnodetype suite (no regression — store_test still green) [DB]**

```bash
DB=pwrite_$(date +%s); PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "CREATE DATABASE $DB;"
export LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@172.17.0.3:5432/$DB?sslmode=disable"
GOWORK=off go test ./internal/customnodetype/... -count=1 -p 1
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "DROP DATABASE $DB;"
```
Expected: PASS (existing `validate`-path tests unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/customnodetype/validate.go internal/customnodetype/store.go internal/customnodetype/validate_test.go
git commit -m "refactor(customnodetype): extract ValidateParams callable on arbitrary param blob (P-write-2)"
```

### Task 6: Run the full validator on the merged blob in `resolveCustomTypes` [DB]

> Fills the P-write-1 Task 4 Step 3 stub: after merge, run `ValidateParams(ct.Kind, m)`. This makes the resolve path (the run-time merge) reject illegal merged values, complementing the per-key RegistryOnly filter.

**Files:**
- Modify: `internal/httpapi/handlers.go` (the merge block from Task 4)
- Test: `internal/httpapi/resolvemerge_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/httpapi/resolvemerge_test.go`:

```go
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
```

- [ ] **Step 2: Run test, see it fail** [DB]

```bash
DB=pwrite_$(date +%s); PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "CREATE DATABASE $DB;"
export LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@172.17.0.3:5432/$DB?sslmode=disable"
GOWORK=off go test ./internal/httpapi/... -run TestResolveMergeRejectsIllegalMergedValue -count=1 -p 1
```
Expected: FAIL — no error returned (validator not wired yet).

- [ ] **Step 3: Wire the validator call**

In `internal/httpapi/handlers.go`, in the merge block, replace the stub comment with the real call (add `customnodetype` import if not present — it already is, used by the resolver interface):

```go
			m, mErr := nodedesc.MergeOverlay(ct.Params, n.Parameters, desc)
			if mErr != nil {
				return nil, fmt.Errorf("custom node %q: merge params: %w", n.ID, mErr)
			}
			if vErr := customnodetype.ValidateParams(ct.Kind, m); vErr != nil {
				return nil, fmt.Errorf("custom node %q: invalid merged params: %w", n.ID, vErr)
			}
			merged = m
```

- [ ] **Step 4: Run test, see it pass; run suite; drop DB** [DB]

```bash
GOWORK=off go test ./internal/httpapi/... -run TestResolveMerge -count=1 -p 1
GOWORK=off go test ./internal/httpapi/... -count=1 -p 1
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "DROP DATABASE $DB;"
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/handlers.go internal/httpapi/resolvemerge_test.go
git commit -m "feat(httpapi): run full ValidateParams on merged per-node params at resolve (P-write-2)"
```

### Task 7: Worker run-time revalidation (authoritative last line)

> W3 backfill copies dirty `projects.workflow_nodes` → `workflows.nodes` with ZERO save validation; a direct `PUT`/INSERT of dirty JSON also bypasses save. The worker is the only gate that holds for the params at the moment of execution. Consolidate the existing scatter checks (body `{{secret:}}` @ :1908, url `{{` @ :1912, code `{{secret:}}` @ :1969) into one explicit entry called before dispatch.

**Files:**
- Modify: `internal/worker/worker.go` (`runCustom` ~:1705, add `revalidateCustomParams`)
- Test: `internal/worker/worker_custom_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/worker_custom_test.go`:

```go
func TestRevalidateCustomParamsRejectsDirty(t *testing.T) {
	cases := []struct {
		name string
		env  customEnvelope
	}{
		{"http url template", customEnvelope{Kind: "http", Params: json.RawMessage(`{"method":"GET","url":"http://x/{{y}}"}`)}},
		{"http body secret", customEnvelope{Kind: "http", Params: json.RawMessage(`{"method":"POST","url":"https://x","bodyTemplate":"{{secret:K}}"}`)}},
		{"script code secret", customEnvelope{Kind: "script", Params: json.RawMessage(`{"code":"x={{secret:K}}"}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := revalidateCustomParams(tc.env); err == nil {
				t.Fatalf("dirty %s params must be rejected at run-time", tc.name)
			}
		})
	}
}

func TestRevalidateCustomParamsAcceptsClean(t *testing.T) {
	clean := customEnvelope{Kind: "http", Params: json.RawMessage(`{"method":"GET","url":"https://api.example.com","outputFormat":"text"}`)}
	if err := revalidateCustomParams(clean); err != nil {
		t.Fatalf("clean params rejected: %v", err)
	}
}
```

Ensure `worker_custom_test.go` imports `encoding/json`.

- [ ] **Step 2: Run test, see it fail**

Run: `GOWORK=off go test ./internal/worker/... -run TestRevalidateCustomParams -count=1`
Expected: FAIL — `revalidateCustomParams` undefined.

- [ ] **Step 3: Add `revalidateCustomParams` and call it in `runCustom`**

In `internal/worker/worker.go`, add a package-level function (mirrors the customnodetype validators but lives in worker to avoid a cross-package import of the registry into the worker hot path — keep the regex `secretRefRe` already defined at :1701):

```go
// revalidateCustomParams is the authoritative run-time last line (spec §6.3). It
// re-asserts the dangerous-field invariants on the params at the moment of
// execution — covering W3 backfill + direct dirty-JSON writes that bypass save.
// Opaque-by-design: callers map the error to the kind's opaque enum.
func revalidateCustomParams(env customEnvelope) error {
	switch env.Kind {
	case "http":
		var p httpParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return fmt.Errorf("worker: revalidate http params: %w", err)
		}
		if strings.Contains(p.URL, "{{") {
			return fmt.Errorf("worker: http url must be static literal")
		}
		if secretRefRe.MatchString(p.BodyTemplate) || strings.Contains(p.BodyTemplate, "{{secret:") {
			return fmt.Errorf("worker: {{secret:}} not allowed in http body")
		}
	case "script":
		var p scriptParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return fmt.Errorf("worker: revalidate script params: %w", err)
		}
		if secretRefRe.MatchString(p.Code) || strings.Contains(p.Code, "{{secret:") {
			return fmt.Errorf("worker: {{secret:}} not allowed in script code")
		}
	}
	return nil
}
```

Then in `runCustom` (:1705), call it right after unmarshalling the envelope:

```go
	var env customEnvelope
	if err := json.Unmarshal(c.input, &env); err != nil {
		return "", fmt.Errorf("worker: custom input unmarshal: %w", err)
	}
	if err := revalidateCustomParams(env); err != nil {
		// Opaque: dirty params (backfill / dirty JSON) → no outbound request, no leak.
		switch env.Kind {
		case "script":
			return "", errScriptFailed
		default:
			return "", errRequestFailed
		}
	}
	switch env.Kind {
```

The existing inline checks at :1908/:1912/:1969 STAY (defense in depth — they run on post-substitution values; `revalidateCustomParams` runs on the raw template). No deletion.

- [ ] **Step 4: Run test, see it pass**

Run: `GOWORK=off go test ./internal/worker/... -run TestRevalidateCustomParams -count=1`
Expected: PASS.

- [ ] **Step 5: Run the (slow ~120s) worker custom suite — no regression**

Run: `GOWORK=off go test ./internal/worker/... -run TestRunCustom -count=1` (then the full `./internal/worker/...` if time permits)
Expected: PASS — existing `{{secret:}}`/url regression behavior unchanged; clean params still execute.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/worker.go internal/worker/worker_custom_test.go
git commit -m "feat(worker): explicit run-time revalidation of custom params before dispatch (P-write-2)"
```

---

# P-write-3 — Frontend round-trip + preserve-unknown (M4 — MUST precede P-write-4)

> Today `toStudioNodes` rebuilds nodes field-by-field via a whitelist, stripping any non-whitelisted key — `parameters`/`typeVersion` would be silently dropped. Under new-BE/old-FE straddle, a stale FE bundle without this fix wipes other clients' just-written `parameters` on every save = active data corruption. This MUST ship before/with any UI that writes `parameters`. **Scope note (m3):** preserve-unknown is frontend/disk-only — `PlanCustom` re-marshals through the typed struct (`planner.go:305`) and drops unknown keys server-side. This is consistent: unknown keys live on disk (authoring forward-compat), runtime injection only takes description-known keys (P-write-1 drop-unknown).

### Task 8: TS mirror — add `parameters`/`typeVersion` to `WorkflowNode` + `registryOnly` to `Constraints`

**Files:**
- Modify: `web/src/lib/types.ts:106-123`
- Modify: `web/src/features/workflow-canvas/nodeDescTypes.ts:20-24`

- [ ] **Step 1: Add the fields (no test yet — types only; tsc is the check)**

In `web/src/lib/types.ts`, append to `WorkflowNode` (after `varBindings` :122):

```ts
  // typed 自定义节点的 schema 化参数覆盖（PropertiesForm value 对象）。非危险键 only；
  // 危险/RegistryOnly 字段留注册表（后端 resolve 层 default-deny）。preserve-unknown：
  // toStudioNodes 透传未知键（前端/disk 级前向兼容）。
  parameters?: Record<string, unknown>
  // 放置/保存时钉入的 description.version；后端按 (kind, typeVersion) 选描述。
  typeVersion?: number
```

In `web/src/features/workflow-canvas/nodeDescTypes.ts`, add to `Constraints` (:20-24):

```ts
export interface Constraints {
  noTemplate?: boolean
  noSecret?: boolean
  secretAllowedIn?: string[]
  registryOnly?: boolean
}
```

- [ ] **Step 2: Run tsc — see it pass (additive, optional fields)**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/types.ts web/src/features/workflow-canvas/nodeDescTypes.ts
git commit -m "feat(web): mirror parameters/typeVersion on WorkflowNode + registryOnly on Constraints (P-write-3)"
```

### Task 9: `toStudioNodes` full-node round-trip + preserve-unknown + parity test

**Files:**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts:85-115`
- Create: `web/src/features/workflow-canvas/canvasModel.parameters.test.ts`

- [ ] **Step 1: Write the failing test (B-A1 parity)**

Create `web/src/features/workflow-canvas/canvasModel.parameters.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { toReactFlow, toStudioNodes } from "./canvasModel"
import type { WorkflowNode } from "@/lib/types"

describe("toStudioNodes parameters round-trip (B-A1)", () => {
  it("preserves parameters + typeVersion through load→save→reload", () => {
    const nodes: (WorkflowNode & Record<string, unknown>)[] = [
      {
        id: "n1",
        type: "custom:my-llm",
        promptId: "",
        typeId: "abc",
        dependsOn: [],
        typeVersion: 1,
        parameters: { temperature: 0.2, outputFormat: "json" },
        // an UNKNOWN property a future client wrote that this bundle doesn't model:
        futureField: { nested: true },
      },
    ]
    const { nodes: rf, edges } = toReactFlow(nodes as WorkflowNode[])
    const out = toStudioNodes(rf, edges) as (WorkflowNode & Record<string, unknown>)[]
    expect(out).toHaveLength(1)
    expect(out[0].parameters).toEqual({ temperature: 0.2, outputFormat: "json" })
    expect(out[0].typeVersion).toBe(1)
    // preserve-unknown: the unmodeled key survives the round-trip.
    expect(out[0].futureField).toEqual({ nested: true })
  })

  it("still derives dependsOn from edges and id from RF (existing invariants)", () => {
    const nodes: WorkflowNode[] = [
      { id: "a", type: "script", promptId: "", dependsOn: [] },
      { id: "b", type: "storyboard", promptId: "", dependsOn: ["a"] },
    ]
    const { nodes: rf, edges } = toReactFlow(nodes)
    const out = toStudioNodes(rf, edges)
    expect(out.find((n) => n.id === "b")?.dependsOn).toEqual(["a"])
  })
})
```

- [ ] **Step 2: Run test, see it fail**

Run: `cd web && npx vitest run src/features/workflow-canvas/canvasModel.parameters.test.ts`
Expected: FAIL — `out[0].parameters`/`typeVersion`/`futureField` undefined (whitelist strips them).

- [ ] **Step 3: Rewrite `toStudioNodes` to round-trip the whole node**

In `web/src/features/workflow-canvas/canvasModel.ts`, replace the `toStudioNodes` body (:89-114):

```ts
  return rfNodes.map((rf) => {
    const n = rf.data.node
    const dependsOn = rfEdges
      .filter((e) => e.target === rf.id)
      .map((e) => e.source)
    // preserve-unknown: spread the whole source node first so未识别字段（含未来
    // Property）随 disk JSON 往返存活（B-A1）。再显式覆盖三条不变量：
    //   id 取 RF（重命名级联权威）、dependsOn 由边推导（单一真源）、position 取 live 坐标。
    return {
      ...n,
      id: rf.id,
      dependsOn,
      position: {
        x: Math.round(rf.position.x),
        y: Math.round(rf.position.y),
      },
    }
  })
```

(The old explicit `if (n.promptText)` / `n.label` / `n.color` / `n.typeId` / `n.varBindings` copies are removed — `...n` now carries them. Verify TS still types the return as `WorkflowNode[]`.)

- [ ] **Step 4: Run test, see it pass**

Run: `cd web && npx vitest run src/features/workflow-canvas/canvasModel.parameters.test.ts`
Expected: PASS.

- [ ] **Step 5: Run the full canvasModel suite + tsc (no regression — T1 typeId/varBindings tests must stay green)**

```bash
cd web && npx vitest run src/features/workflow-canvas/canvasModel.test.ts && npx tsc --noEmit
```
Expected: PASS — existing typeId/varBindings preservation tests still pass (now via `...n`).

- [ ] **Step 6: Commit**

```bash
git add web/src/features/workflow-canvas/canvasModel.ts web/src/features/workflow-canvas/canvasModel.parameters.test.ts
git commit -m "feat(web): toStudioNodes full-node round-trip + preserve-unknown (B-A1, P-write-3)"
```

---

# P-write-4 — Editable write path + save-time validation (security-gated)

> Flip the dead `onChange={() => {}}`. `PropertiesForm` is already a controlled component (`onChange`/`patch`/`patchOption` implemented). `onPatch` is `PropertiesPanel`'s existing flat-key patch channel — `parameters` and `typeVersion` are flat keys on `WorkflowNode`, so `onPatch({ parameters, typeVersion })` is compatible. Save-time validation lands on BOTH editor write paths (W1 + W2, B1 dual-writer). **This step is gated by an independent security review (§6.5) — see the final checklist task.**

### Task 10: Save-time validation on W1 (workflow create + update) [DB]

**Files:**
- Modify: `internal/httpapi/workflowhandlers.go:54-66, 84-96`
- Test: `internal/httpapi/workflowhandlers_test.go`

> The handlers already unmarshal `[]planner.WorkflowNode` and run `ValidateCustomGraph`. Add a per-typed-node parameters check: reject overlay with a RegistryOnly key, and validate the merged result. Because the handler does not hold a registry resolver, validate the OVERLAY's danger surface directly using `nodedesc.DescByKind` + a "no RegistryOnly key present" check, then `customnodetype.ValidateParams(kind, overlay-merged-onto-empty)` is not meaningful without base — so the save check asserts (a) overlay carries no RegistryOnly key, (b) overlay alone passes `ValidateParams` for known value-shape keys. The authoritative full-merge validation is the run-time gate (Task 6/7). Add a `validateNodeParameterOverlays(nodes, kindByTypeId)` helper. Since the handler lacks org/registry context for kind lookup at save, resolve kind via the workflow store is out of scope — instead reject any RegistryOnly key by description for the node's resolved kind obtained from the `custom:<slug>`→registry path the run handler uses.

**Decision (spec underspecified — see summary):** Save-time validation needs the node's `kind` to pick the description, but W1/W2 handlers don't currently resolve the registry. To avoid widening the handler's dependencies AND to keep the dual-writer requirement (B1), the save check operates on the **overlay alone**: it rejects any key whose description (looked up via the resolver the handler already wires for the run path) is `RegistryOnly`, and runs `ValidateParams(kind, overlay)` for value legality. The handlers must therefore receive the `CustomNodeTypeResolver` (already available in the router wiring for run handlers) to map `typeId → kind`. This is the minimal change that satisfies B1.

- [ ] **Step 1: Write the failing test** [DB]

Append to `internal/httpapi/workflowhandlers_test.go` (use the existing test harness in that file; seed an http custom type, POST/PUT a workflow whose node carries a dangerous overlay):

```go
func TestCreateWorkflowRejectsRegistryOnlyOverlay(t *testing.T) {
	h, store, org, projectID := newWorkflowTestHarness(t) // existing or add per file's pattern
	ct, _ := store.Create(context.Background(), org, customnodetype.UpsertInput{
		Label: "fetch", Kind: "http",
		Params: mustJSON(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"}),
	})
	body := mustJSON(map[string]any{
		"name": "wf1",
		"nodes": []map[string]any{{
			"id": "n1", "type": "custom:fetch", "typeId": ct.ID, "dependsOn": []string{}, "typeVersion": 1,
			"parameters": map[string]any{"url": "http://attacker/collect", "allowResponseBody": true},
		}},
	})
	rec := doRequest(t, h, "POST", "/api/projects/"+projectID+"/workflows", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("RegistryOnly overlay must be rejected at create, got %d", rec.Code)
	}
}
```

(If `newWorkflowTestHarness`/`doRequest`/`mustJSON` helpers don't exist in `workflowhandlers_test.go`, mirror the construction used by the existing tests in that file — read it first and reuse its router + DB setup.)

- [ ] **Step 2: Run test, see it fail** [DB]

```bash
DB=pwrite_$(date +%s); PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "CREATE DATABASE $DB;"
export LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@172.17.0.3:5432/$DB?sslmode=disable"
GOWORK=off go test ./internal/httpapi/... -run TestCreateWorkflowRejectsRegistryOnlyOverlay -count=1 -p 1
```
Expected: FAIL — create returns 200 (no parameters check yet).

- [ ] **Step 3: Add the save-time check helper + wire into create + update**

Add to `internal/httpapi/workflowhandlers.go` a helper that, given the resolver + org + nodes, rejects RegistryOnly overlays and validates overlay value-shape:

```go
// validateNodeParameterOverlays enforces, at SAVE time on an editor write path
// (W1 create/update, W2 create-project), the per-node parameters invariants:
// (a) no RegistryOnly key in the overlay (default-deny, fail-closed), and
// (b) the overlay's value shape is legal per the kind's validator. The run-time
// resolve+worker gates remain authoritative (W3 backfill / dirty JSON bypass save).
func validateNodeParameterOverlays(ctx context.Context, res CustomNodeTypeResolver, orgID string, nodes []planner.WorkflowNode) error {
	for _, n := range nodes {
		if n.TypeId == "" || len(n.Parameters) == 0 {
			continue
		}
		ct, err := res.Get(ctx, n.TypeId, orgID)
		if err != nil {
			return fmt.Errorf("node %q: resolve type: %w", n.ID, err)
		}
		desc, ok := nodedesc.DescByKind(ct.Kind, n.TypeVersion)
		if !ok {
			return fmt.Errorf("node %q: typeVersion %d unsupported", n.ID, n.TypeVersion)
		}
		var overlay map[string]json.RawMessage
		if err := json.Unmarshal(n.Parameters, &overlay); err != nil {
			return fmt.Errorf("node %q: invalid parameters: %w", n.ID, err)
		}
		registryOnly := map[string]bool{}
		for _, p := range desc.Properties {
			if p.Constraints != nil && p.Constraints.RegistryOnly {
				registryOnly[p.Name] = true
			}
		}
		for k := range overlay {
			if registryOnly[k] {
				return fmt.Errorf("node %q: parameter %q is registry-only and cannot be overridden", n.ID, k)
			}
		}
		// Merge onto base, then full validate (catches illegal non-dangerous values).
		merged, mErr := nodedesc.MergeOverlay(ct.Params, n.Parameters, desc)
		if mErr != nil {
			return fmt.Errorf("node %q: %w", n.ID, mErr)
		}
		if vErr := customnodetype.ValidateParams(ct.Kind, merged); vErr != nil {
			return fmt.Errorf("node %q: invalid params: %w", n.ID, vErr)
		}
	}
	return nil
}
```

Add `customnodetype`, `nodedesc` to imports. Thread `CustomNodeTypeResolver` into `createWorkflowHandler`/`updateWorkflowHandler` signatures + the router wiring (mirror how `runWorkflowHandler` already receives `customTypeResolver`). The handler must also resolve `orgID` from the project (load via `ProjectStore.Get` as `runWorkflowHandler` does, or pass org through). In both handlers, after the existing `ValidateCustomGraph` block, add:

```go
				if err := validateNodeParameterOverlays(r.Context(), customTypeResolver, orgID, nodes); err != nil {
					http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
					return
				}
```

- [ ] **Step 4: Run test, see it pass; run suite; drop DB** [DB]

```bash
GOWORK=off go test ./internal/httpapi/... -run TestCreateWorkflow -count=1 -p 1
GOWORK=off go test ./internal/httpapi/... -count=1 -p 1
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "DROP DATABASE $DB;"
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/workflowhandlers.go internal/httpapi/workflowhandlers_test.go
git commit -m "feat(httpapi): save-time parameters validation on workflow create+update (W1, P-write-4)"
```

### Task 11: Save-time validation on W2 (legacy createProject) [DB]

**Files:**
- Modify: `internal/httpapi/handlers.go:294-324` (createProject) — reuse `validateNodeParameterOverlays`
- Test: `internal/httpapi/m2handlers_test.go` (or the file covering createProject)

- [ ] **Step 1: Write the failing test** [DB]

Append a test that POSTs `/api/orgs/{org}/projects` with `customWorkflowEnabled:true` + `workflowNodes` carrying a node with a RegistryOnly overlay, asserting 400. Mirror the existing createProject test construction in the relevant `_test.go` (read it first for the harness):

```go
func TestCreateProjectRejectsRegistryOnlyOverlay(t *testing.T) {
	h, store, org := newProjectTestHarness(t) // per file's existing pattern
	ct, _ := store.Create(context.Background(), org, customnodetype.UpsertInput{
		Label: "code", Kind: "script",
		Params: mustJSON(map[string]any{"code": "print(1)", "outputFormat": "text"}),
	})
	nodes := mustJSON([]map[string]any{{
		"id": "n1", "type": "custom:code", "typeId": ct.ID, "dependsOn": []string{}, "typeVersion": 1,
		"parameters": map[string]any{"code": "x = {{secret:K}}"},
	}})
	body := mustJSON(map[string]any{"name": "p1", "customWorkflowEnabled": true, "workflowNodes": json.RawMessage(nodes)})
	rec := doRequest(t, h, "POST", "/api/orgs/"+org+"/projects", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("RegistryOnly overlay must be rejected at createProject, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test, see it fail** [DB]

```bash
DB=pwrite_$(date +%s); PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "CREATE DATABASE $DB;"
export LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@172.17.0.3:5432/$DB?sslmode=disable"
GOWORK=off go test ./internal/httpapi/... -run TestCreateProjectRejectsRegistryOnlyOverlay -count=1 -p 1
```
Expected: FAIL — createProject returns 200.

- [ ] **Step 3: Wire the check into createProject**

In `createProject` (handlers.go), `createProjectHandler` must receive `customTypeResolver` (thread it through the router wiring as `runHandler` does). After decoding `req` and before `ps.Create` (:302), when `req.CustomWorkflowEnabled && len(req.WorkflowNodes) > 0`:

```go
		if req.CustomWorkflowEnabled && len(req.WorkflowNodes) > 0 {
			var nodes []planner.WorkflowNode
			if err := json.Unmarshal(req.WorkflowNodes, &nodes); err != nil {
				http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := validateNodeParameterOverlays(r.Context(), customTypeResolver, r.PathValue("org"), nodes); err != nil {
				http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
```

- [ ] **Step 4: Run test, see it pass; run suite; drop DB** [DB]

```bash
GOWORK=off go test ./internal/httpapi/... -run TestCreateProject -count=1 -p 1
GOWORK=off go test ./internal/httpapi/... -count=1 -p 1
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "DROP DATABASE $DB;"
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/handlers.go internal/httpapi/*_test.go
git commit -m "feat(httpapi): save-time parameters validation on legacy createProject (W2, P-write-4)"
```

### Task 12: Flip `PropertiesPanel` onChange → persist `parameters` + `typeVersion`

**Files:**
- Modify: `web/src/features/workflow-canvas/PropertiesPanel.tsx:274-288`
- Test: `web/src/features/workflow-canvas/PropertiesPanel.test.tsx` (create if absent, or extend)

- [ ] **Step 1: Write the failing test**

Add a test that renders `PropertiesPanel` for a typed node with a `description`, simulates a `PropertiesForm` field change, and asserts `onPatch` received `{ parameters, typeVersion }`. Reuse `PropertiesForm.test.tsx` rendering patterns. If a `PropertiesPanel.test.tsx` does not exist, create one with a minimal typed-llm node + a one-property description and fire a change on the `outputFormat` select:

```tsx
import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { PropertiesPanel } from "./PropertiesPanel"
import type { NodeTypeDescription } from "./nodeDescTypes"
import type { WorkflowNode } from "@/lib/types"

const llmDesc: NodeTypeDescription = {
  type: "custom:my-llm", version: 1, label: "LLM", description: "", group: "transform",
  inputs: [], outputs: [],
  properties: [{ name: "outputFormat", label: "输出格式", type: "options",
    options: [{ value: "text", label: "文本" }, { value: "json", label: "JSON" }] }],
}

describe("PropertiesPanel typed node persists parameters", () => {
  it("onChange → onPatch({ parameters, typeVersion })", () => {
    const node: WorkflowNode = { id: "n1", type: "custom:my-llm", promptId: "", typeId: "abc", dependsOn: [] }
    const onPatch = vi.fn()
    render(
      <PropertiesPanel
        node={node} prompts={[]} basics={[]} org="o" otherIds={[]}
        onPatch={onPatch} onRename={() => {}} onDelete={() => {}}
        typedParams={{ systemPrompt: "", userPrompt: "", model: "", temperature: 0, outputFormat: "text" } as never}
        description={llmDesc}
      />,
    )
    // change outputFormat → json
    fireEvent.click(screen.getByText("JSON"))
    expect(onPatch).toHaveBeenCalled()
    const arg = onPatch.mock.calls.at(-1)![0]
    expect(arg).toHaveProperty("parameters")
    expect(arg.typeVersion).toBe(1)
  })
})
```

(If the options widget renders differently, adapt the interaction to the actual `PropertiesForm` widget — read `PropertiesForm.tsx` `renderWidget`. The assertion on `onPatch` shape is the load-bearing part.)

- [ ] **Step 2: Run test, see it fail**

Run: `cd web && npx vitest run src/features/workflow-canvas/PropertiesPanel.test.tsx`
Expected: FAIL — `onPatch` not called (current `onChange={() => {}}`).

- [ ] **Step 3: Flip the onChange**

In `web/src/features/workflow-canvas/PropertiesPanel.tsx`, change the typed-node `<PropertiesForm>` block (:274-287). The `value` should prefer the node's saved `parameters` over the registry typedParams fallback (mirrors the backend merge precedence §5.2):

```tsx
            {description ? (
              <PropertiesForm
                description={description}
                value={
                  ((node as WorkflowNode).parameters ??
                    (isTypedLlm
                      ? typedParams
                      : isTypedHttp
                        ? typedHttpParams
                        : typedScriptParams)) as Record<string, unknown>
                }
                onChange={(next) =>
                  onPatch({ parameters: next, typeVersion: description.version })
                }
                secretNames={[]}
                modelOptions={[]}
              />
            ) : (
```

Update the comment at :112-115 (it currently says onChange is a no-op / never wired to onPatch) to reflect that typed nodes now persist parameters. Keep it factual and minimal.

- [ ] **Step 4: Run test, see it pass + tsc**

```bash
cd web && npx vitest run src/features/workflow-canvas/PropertiesPanel.test.tsx && npx tsc --noEmit
```
Expected: PASS, no type errors.

- [ ] **Step 5: Run the full frontend suite (no regression)**

Run: `cd web && npm run test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/workflow-canvas/PropertiesPanel.tsx web/src/features/workflow-canvas/PropertiesPanel.test.tsx
git commit -m "feat(web): persist typed-node parameters via PropertiesForm onChange (P-write-4)"
```

### Task 13: Cross-tenant + run-time fail-closed security tests [DB]

**Files:**
- Modify: `internal/httpapi/resolvemerge_test.go`
- Modify: `internal/worker/worker_custom_test.go`

- [ ] **Step 1: Write the cross-tenant resolve test**

Append to `internal/httpapi/resolvemerge_test.go`:

```go
func TestResolveMergeCrossTenantDenied(t *testing.T) {
	store := mergeTestStore(t)
	orgA, orgB := "org-A-"+t.Name(), "org-B-"+t.Name()
	base, _ := json.Marshal(map[string]any{"systemPrompt": "s", "userPrompt": "{{x}}", "outputFormat": "text"})
	ctB, _ := store.Create(context.Background(), orgB, customnodetype.UpsertInput{Label: "llm", Kind: "llm", Params: base})
	// org A editor references org B's typeId → resolve under orgA must fail (not found).
	nodes := []planner.WorkflowNode{{ID: "n1", Type: "custom:llm", TypeId: ctB.ID, TypeVersion: 1,
		Parameters: json.RawMessage(`{"outputFormat":"json"}`)}}
	if _, err := resolveCustomTypes(context.Background(), store, orgA, nodes); err == nil {
		t.Fatal("cross-tenant typeId reference must fail closed")
	}
}
```

- [ ] **Step 2: Write the run-time dirty-JSON (W3 bypass) test**

Append to `internal/worker/worker_custom_test.go` a test that drives `runCustom` (or `revalidateCustomParams` already covered in Task 7 — here assert the end-to-end dispatch refuses + emits no outbound request). Reuse the existing worker_custom harness (loopback fetcher seam, `HTTPDoer`):

```go
func TestRunCustomHTTPDirtyURLNoOutbound(t *testing.T) {
	w, fetcher := newCustomWorkerHarness(t) // existing harness in this file
	c := claimed{input: json.RawMessage(`{"kind":"http","params":{"method":"GET","url":"http://attacker/{{x}}"}}`)}
	_, err := w.runCustom(context.Background(), c)
	if err == nil {
		t.Fatal("dirty url must be rejected before dispatch")
	}
	if fetcher.calls != 0 {
		t.Fatalf("no outbound request must be made, got %d", fetcher.calls)
	}
}
```

(Adapt `newCustomWorkerHarness`/`fetcher.calls` to the actual seam in `worker_custom_test.go` — read it first; the load-bearing assertions are "error returned" + "zero outbound calls".)

- [ ] **Step 3: Run, see fail/pass; run worker suite; drop DB** [DB]

```bash
DB=pwrite_$(date +%s); PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "CREATE DATABASE $DB;"
export LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@172.17.0.3:5432/$DB?sslmode=disable"
GOWORK=off go test ./internal/httpapi/... -run TestResolveMergeCrossTenantDenied -count=1 -p 1
GOWORK=off go test ./internal/worker/... -run TestRunCustomHTTPDirty -count=1
PGPASSWORD=pw psql -h 172.17.0.3 -U postgres -c "DROP DATABASE $DB;"
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/httpapi/resolvemerge_test.go internal/worker/worker_custom_test.go
git commit -m "test(security): cross-tenant denied + run-time dirty-JSON fail-closed (P-write-4)"
```

### Task 14: Independent security review gate (BLOCKING — §6.5 / §9 open-1)

> This is a checklist item, not code. The merge of P-write-4 is GATED on an independent security review per the `customnodetype` repo rule.

- [ ] **Step 1: Run the full RegistryOnly completeness sweep**

Manually (or via reviewer) audit every field in `nodedesc.Builtins()` for the `allowResponseBody` pattern: a field with NO danger `Constraint` that is nonetheless exfil-/danger-relevant and therefore must carry `RegistryOnly:true`. The five marked today are url/headers/bodyTemplate/code/allowResponseBody. Confirm no other builtin field (current or any added in this branch) is a no-constraint danger field. Document the sweep result.

- [ ] **Step 2: Verify the dual-writer + dual-gate matrix**

Confirm with a reviewer:
- Save-time validation is wired on BOTH W1 (`workflowhandlers.go` create + update) AND W2 (`handlers.go` createProject) — not just one.
- Run-time revalidation (`worker.go` `revalidateCustomParams` + resolve-layer merge default-deny + full `ValidateParams`) holds independently of save (covers W3 backfill + direct dirty `PUT`/INSERT).
- `allowResponseBody` overlay regression: `{"allowResponseBody":true}` is default-denied at merge (base `false` kept); a secret-bearing http node still stores only `{status}` (`worker.go:1934` guard not flipped by overlay). Add an explicit test if not already covered by Task-3 `TestMergeOverlayDefaultDeniesRegistryOnly` + the worker suite.
- Errors are opaque (no url/secret/header/body in surfaced error); cross-tenant `$node`-style references stay fail-closed.

- [ ] **Step 3: Obtain sign-off, then finish the branch**

Only after the security review signs off, proceed to PR. Per repo convention (studio changes go via branch→push→PR→rebase, no direct push to main).

```bash
GOWORK=off go build ./... && cd web && npx tsc --noEmit && npm run test
```
Expected: clean build, no type errors, all tests pass.

---

## Self-review (run before final commit of this plan)

**Spec coverage (§3–§8):**
- §3.1 `WorkflowNode` Parameters/TypeVersion → Task 1. §3.2 on-disk shape + parity → Task 9. §3.3 no migration → stated in P-write-1 preamble + Task 4 `TestResolveMergeNoOverlayUnchanged`.
- §4.1 merge not in PlanCustom → Task 4 (choke point). §4.2 allow-list ∩ non-RegistryOnly + drop-unknown → Task 3. §4.3 typeVersion fail-closed → Task 3 `TestDescByKindUnknownVersionFailsClosed` + Task 4 `TestResolveMergeUnknownTypeVersionFailsClosed`. §4.5 builtins unchanged → `resolveCustomTypes` skips `TypeId==""` (unchanged).
- §5.1 toStudioNodes round-trip → Task 9. §5.2 onChange→persist → Task 12. §5.3 query-key unchanged → no task (untouched).
- §6.3 RegistryOnly marker → Task 2; extracted validators → Task 5; save-time both writers → Tasks 10+11; run-time → Tasks 6+7. §6.4 secret/{status} guard unchanged → Task 14 verify. §6.5 review gate → Task 14.
- §7 tests: 1→Task 9; 2→Task 4; 3→Tasks 10/11/13; 4→Tasks 3/4; 5→Task 7; 6→Task 3 + Task 14. §8 ordering → 4 sub-steps in stated order.

**Placeholder scan:** Tasks 10/11/12/13 reference existing test harness helpers (`newWorkflowTestHarness`, `doRequest`, `mustJSON`, `newCustomWorkerHarness`) that the executor must confirm/mirror from the existing `_test.go` files — flagged inline as "read it first". This is intentional (the harness shape is file-local and must match), not a vague step; the load-bearing assertions are spelled out.

**Type consistency:** `RegistryOnly` (Go `Constraints` + TS `registryOnly`), `Parameters`/`TypeVersion` (Go) / `parameters`/`typeVersion` (TS), `DescByKind`, `MergeOverlay`, `ValidateParams`, `revalidateCustomParams`, `validateNodeParameterOverlays` — names consistent across all tasks.
