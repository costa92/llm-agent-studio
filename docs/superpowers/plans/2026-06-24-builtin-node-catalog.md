# Built-in Node Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** Make the 3 built-in workflow node types (script/storyboard/asset) data-driven from a backend catalog (single source), add a read-only built-in management page separate from the custom page, and drive the canvas palette/picker from the catalog (removing the hardcoded `PALETTE_TYPES`/`PICKER_TYPES`).

**Architecture:** New leaf pkg `internal/builtinnode` is the single source; planner's `whitelistedTypes` derives from it; a global `authOnly` endpoint serves it; the palette/picker + new page consume it via a react-query hook. Canvas node *rendering* stays synchronous on the existing `NODE_COLOR`/`TYPE_LABEL` (not invasive). Color stays frontend-only.

**Tech Stack:** Go (net/http mux), React/TS, TanStack Query + Router.

> All `go` commands `GOWORK=off`. DB-gated tests use a FRESH DB `-p 1`: `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_bnc_<rand>?sslmode=disable`.

---

### Task N0.1: `internal/builtinnode` leaf package

**Files:** Create `internal/builtinnode/catalog.go`, `internal/builtinnode/catalog_test.go`

- [ ] **Step 1: test first** — `catalog_test.go`: assert `len(Catalog())==3`; assert `Catalog()` types == `{script,storyboard,asset}`; assert `Types()` keys match; assert `Types()` returns a FRESH map (mutate the returned map, call `Types()` again, confirm the new one is unmutated). Run → FAIL (pkg missing).
- [ ] **Step 2: implement** `catalog.go`:

```go
// Package builtinnode is the single source of truth for the built-in workflow
// node types (script/storyboard/asset). Leaf package: imports nothing from the
// studio tree, so planner can import it without a cycle. Color is intentionally
// NOT modeled here — it is a frontend/theme concern (CSS vars --script/--board/
// --asset), kept single-sourced in the web layer.
package builtinnode

// BuiltinNodeType describes one built-in workflow node type.
type BuiltinNodeType struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

var catalog = []BuiltinNodeType{
	{Type: "script", Label: "剧本", Description: "根据项目简报生成剧本/脚本；工作流必须包含至少一个剧本节点。"},
	{Type: "storyboard", Label: "分镜", Description: "将剧本拆解为分镜镜头；完成后按镜头扇出生成资产节点。"},
	{Type: "asset", Label: "资产", Description: "生成单个图像/视频/音频资产（通常由分镜扇出，不直接编排）。"},
}

// Catalog returns a copy of the ordered built-in node catalog.
func Catalog() []BuiltinNodeType {
	out := make([]BuiltinNodeType, len(catalog))
	copy(out, catalog)
	return out
}

// Types returns a freshly-allocated set of built-in type names. Each call
// returns an independent map so callers (e.g. the planner whitelist) may mutate
// it without corrupting this package's shared state.
func Types() map[string]bool {
	m := make(map[string]bool, len(catalog))
	for _, b := range catalog {
		m[b.Type] = true
	}
	return m
}
```

- [ ] **Step 3:** `GOWORK=off go test ./internal/builtinnode/... -count=1` → PASS. Commit `feat(builtinnode): single-source catalog for built-in workflow node types`.

---

### Task N0.2: derive planner whitelist from the catalog

**Files:** Modify `internal/planner/graph.go`; add `internal/planner/graph_builtin_test.go`

- [ ] **Step 1: test** — assert `isTypeAllowed("script")` true, `isTypeAllowed("storyboard")`/`"asset"` true, `isTypeAllowed("nope")` false; then `RegisterType("translate")` and assert `isTypeAllowed("translate")` true (proves the derived map is still mutable). (`isTypeAllowed` is unexported — put the test in `package planner`.)
- [ ] **Step 2:** In `graph.go`, change the var init (line ~14-17):

```go
var (
	typesMu          sync.RWMutex
	whitelistedTypes = builtinnode.Types() // single source; RegisterType still appends (Types returns a fresh map)
)
```

Add import `"github.com/costa92/llm-agent-studio/internal/builtinnode"`. Leave `RegisterType`/`isTypeAllowed`/`isCustomType` unchanged.

- [ ] **Step 3:** `GOWORK=off go build ./... && GOWORK=off go test ./internal/planner/... -count=1` (this is DB-free for graph tests; if the package needs DB, use a fresh DB). Run the existing worker RegisterType path too. Commit `refactor(planner): derive whitelistedTypes from builtinnode catalog (single source)`.

---

### Task N0.3: `GET /api/node-types/builtin` endpoint

**Files:** Modify `internal/httpapi/m2handlers.go` (add handler near `promptStylesHandler`), `internal/httpapi/httpapi.go` (register), add a handler test (mirror `m2handlers_test.go`'s `promptStylesHandler` test).

- [ ] **Step 1: test** — in `m2handlers_test.go` style: call `builtinNodeTypesHandler()` against `GET /api/node-types/builtin`, assert 200 + body `{"items":[...]}` with 3 entries each having `type`+`label`+`description` (and NO `color` key).
- [ ] **Step 2: handler** (in m2handlers.go, mirror `promptStylesHandler`):

```go
// builtinNodeTypesHandler (GET /api/node-types/builtin): authenticated, global.
// Returns the static built-in workflow node catalog (single source: builtinnode).
func builtinNodeTypesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": builtinnode.Catalog()})
	}
}
```

Add the `builtinnode` import. (Confirm `writeJSON` is the helper used by neighboring handlers — it is.)

- [ ] **Step 3: register** in httpapi.go near the other static catalogs (`/api/prompt-styles`, `/api/model-catalog`):

```go
	mux.Handle("GET /api/node-types/builtin", authOnly(builtinNodeTypesHandler()))
```

NO new `Deps` field, NO `if d.X != nil` guard — the handler is static.

- [ ] **Step 4:** `GOWORK=off go test ./internal/httpapi/... -count=1` (fresh DB) → PASS. Build. Commit `feat(httpapi): GET /api/node-types/builtin (global authOnly static catalog)`.

---

### Task N1: frontend built-in catalog page (read-only)

**Files:** `web/src/lib/types.ts` (add `BuiltinNodeType`); Create `web/src/features/builtin-node-types/api.ts` + `BuiltinNodeTypeList.tsx` (+ `.test.tsx`); Create route `web/src/routes/_authed/orgs.$org.builtin-node-types.tsx`; Modify `web/src/app/nav.ts`.

- [ ] **Step 1:** `types.ts`: `export interface BuiltinNodeType { type: string; label: string; description: string }`.
- [ ] **Step 2:** `api.ts` — mirror `custom-node-types/api.ts` `useCustomNodeTypes`, but GLOBAL (query key WITHOUT org):

```ts
import { useQuery, type UseQueryResult } from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { BuiltinNodeType } from "@/lib/types"

// Global built-in node catalog: GET /api/node-types/builtin → {items}.
// Static (staleTime Infinity); query key intentionally has no org.
export function useBuiltinNodeTypes(): UseQueryResult<BuiltinNodeType[]> {
  return useQuery({
    queryKey: ["builtin-node-types"],
    queryFn: () =>
      apiJSON<{ items: BuiltinNodeType[] }>("/api/node-types/builtin").then((d) => d.items),
    staleTime: Infinity,
  })
}
```

- [ ] **Step 3:** `BuiltinNodeTypeList.tsx`: a read-only table — columns: 颜色点 (`NODE_COLOR[type]` from `workflow-canvas/nodeColor`), Label, type (mono slug), Description; each row a "内置 · 只读" badge; header note "内置节点由系统定义，不可增删改；在工作流画布中直接使用。" Mirror the page chrome of `CustomNodeTypeManager` (title/description block) but NO create/edit/delete. Use theme tokens (no hardcoded colors) per house rules.
- [ ] **Step 4:** route `orgs.$org.builtin-node-types.tsx` — mirror `orgs.$org.custom-node-types.tsx`, wrap `<AdminGate>`, render `<BuiltinNodeTypeList/>`. (`routeTree.gen.ts` is regenerated by `tsr generate` on dev/build — do not hand-edit.)
- [ ] **Step 5:** `nav.ts`: in the 配置 section, (a) change the existing custom-node-types item label `"节点类型"` → `"自定义节点"`; (b) add BEFORE it a built-in item: `{ to: "/orgs/$org/builtin-node-types", params: {}, icon: createElement(Cpu), label: "内置节点", adminOnly: true, orgScoped: true }` (reuse an existing imported icon, e.g. `Boxes`/`Cpu` — pick one already imported or add the import).
- [ ] **Step 6:** `BuiltinNodeTypeList.test.tsx`: mock `useBuiltinNodeTypes` to return 3 items, assert rows render with labels + "内置 · 只读" badge + no action buttons. `cd web && npx vitest run src/features/builtin-node-types && npx tsc --noEmit`. Commit `feat(web): built-in node types read-only page + nav (separate from custom)`.

---

### Task N2: data-drive the canvas palette/picker

**Files:** Modify `web/src/features/workflow-canvas/NodePalette.tsx`, `NodeTypePicker.tsx`; add a `TYPE_LABEL` parity test; do NOT touch `WorkflowNode.tsx`/`PropertiesPanel.tsx`/minimaps.

- [ ] **Step 1:** `NodePalette.tsx`: remove `const PALETTE_TYPES = [...]`; instead `const { data: builtins = [] } = useBuiltinNodeTypes()` and map over `builtins` (each `b.type`), rendering color via `NODE_COLOR[b.type]` and label via `b.label`. During load (`builtins` empty) the built-in section is briefly empty — acceptable (side panel; staleTime Infinity → one-time). Keep the custom section + divider exactly as-is.
- [ ] **Step 2:** `NodeTypePicker.tsx`: same swap (remove `PICKER_TYPES`, drive from `useBuiltinNodeTypes()`).
- [ ] **Step 3:** parity test `web/src/features/workflow-canvas/nodeColor.parity.test.ts`: assert the frontend `TYPE_LABEL` map keys+values equal the known built-in catalog `{script:"剧本",storyboard:"分镜",asset:"资产"}` (this guards the one residual duplication — `TYPE_LABEL` is the synchronous canvas-render source; the catalog is the API source). If they drift, this fails.
- [ ] **Step 4:** `cd web && npx vitest run && npx tsc --noEmit` → green (NodePalette/NodeTypePicker tests if any, canvas regression, parity). Commit `feat(web): data-drive canvas palette/picker built-ins from catalog hook`.

---

### Task N3: full sweep + smoke

- [ ] **Step 1:** `GOWORK=off go build ./... && GOWORK=off go vet ./...`.
- [ ] **Step 2:** Full Go sweep ONCE on a FRESH DB: `LLM_AGENT_STUDIO_PG_URL=...studio_bnc_<rand>... GOWORK=off go test ./... -count=1 -p 1` → all ok. (Never reuse the DB — dirty-data false failures.)
- [ ] **Step 3:** `cd web && npx vitest run && npx tsc --noEmit`.
- [ ] **Step 4:** Final whole-branch review. Then `superpowers:finishing-a-development-branch` (user opens the PR — house rule; do not push main / open PR).

## Self-review notes
- Spec coverage: N0(catalog+derive+endpoint)→后端; N1(page+nav)→两独立页; N2(palette/picker)→数据驱动列表; N3 sweep. ✓
- Import cycle avoided (builtinnode leaf); `Types()` fresh-map pinned + RegisterType test. ✓
- Canvas rendering NOT invaded (validation #3); color frontend-only, no value-dup in catalog (validation #4); TYPE_LABEL parity test guards the one residual. ✓
- Endpoint global authOnly no Deps (validation #2); nav adminOnly for section consistency (validation #5). ✓
