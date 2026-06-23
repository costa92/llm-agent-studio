# Custom Nodes (Frontend-First, Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users add, name, color, and manage **custom node types** (`type: custom:<slug>`) on the workflow canvas — saved/reloaded as self-describing nodes — while runs are cleanly refused when custom nodes are present.

**Architecture:** Self-describing nodes: a custom node carries extra persisted fields `label`+`color` that survive the backend's raw-JSONB passthrough. Backend changes are two validation-layer spots only (relax `ValidateCustomGraph` to accept `custom:*`; refuse runs containing custom nodes) — **worker/planner execution untouched**. Frontend derives the per-workflow custom-type registry from the canvas nodes themselves (no new storage).

**Tech Stack:** Go (planner + httpapi, run with `GOWORK=off`), React + TypeScript, `@xyflow/react` v12, vitest 4, pnpm. Backend under `internal/`, frontend under `web/src/`.

**Spec:** `docs/superpowers/specs/2026-06-23-custom-nodes-frontend-design.md`

**Working dir:** `llm-agent-studio`, branch `feat/custom-nodes` (created; spec committed). Go tests: `cd <repo> && GOWORK=off go test ./internal/planner/... ./internal/httpapi/... -count=1`. Frontend: `cd web && pnpm test` (full) or `pnpm exec vitest run <file>`.

**Invariants:** EDGES stay the sole source of `dependsOn`; edge id `${source}->${target}` + `type:"studio"`; `takeSnapshot()` before any `{nodes,edges}` mutation; amber tri-theme tokens for UI chrome (the only deliberate exception is a custom node's own user-chosen `color` hex).

---

## File Structure

**PR-1 — Backend gate (Go)**
- Modify: `internal/planner/planner.go` — relax `ValidateCustomGraph`; add `HasCustomNode`.
- Modify: `internal/planner/graph.go` — add `isCustomType` helper.
- Test: `internal/planner/planner_test.go` — new cases.
- Modify: `internal/httpapi/workflowhandlers.go` — run-guard 400 in `runWorkflowHandler`.
- Test: `internal/httpapi/workflowhandlers_test.go` — run-guard case.

**PR-2 — Frontend**
- Modify: `web/src/lib/types.ts` — `WorkflowNode` += `label?`/`color?`.
- Modify: `web/src/features/workflow-canvas/nodeColor.ts` — `isCustomType`, `CUSTOM_PALETTE`, `nodeDisplay`, `slugify`.
- Modify: `web/src/features/workflow-canvas/canvasModel.ts` — carry `label`/`color` in `toStudioNodes`; `display?` param on `addNodeAt`/`createNode`/`insertNodeOnEdge`; add `applyTypeDisplay`, `collectCustomTypes`, `hasCustomNode`.
- Modify: `web/src/features/workflow-canvas/WorkflowNode.tsx` — render via `nodeDisplay`.
- Create: `web/src/features/workflow-canvas/CustomTypeDialog.tsx` (+ test) — new/edit dialog.
- Modify: `web/src/features/workflow-canvas/NodeTypePicker.tsx` — list custom types.
- Modify: `web/src/features/workflow-canvas/NodePalette.tsx` — "+ 自定义类型" + used-types list.
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx` — registry, dialog wiring, cascade, disable-run.
- Modify: `web/src/features/workflow-canvas/PropertiesPanel.tsx` — custom-node display.
- Tests: `canvasModel.test.ts`, `nodeColor.test.ts` (new), `CustomTypeDialog.test.tsx` (new).

---

# PR-1 — Backend gate

### Task 1: Relax `ValidateCustomGraph` to accept `custom:*`

**Files:**
- Modify: `internal/planner/graph.go`
- Modify: `internal/planner/planner.go:175`
- Test: `internal/planner/planner_test.go`

- [ ] **Step 1: Write failing tests** — add cases to the `TestValidateCustomGraph` table in `planner_test.go` (inside the `cases := []struct{...}{...}` slice):

```go
		{
			name: "custom type accepted",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "script"},
				{ID: "node2", Type: "custom:translate", DependsOn: []string{"node1"}},
			},
			wantErr: "",
		},
		{
			name: "empty custom slug rejected",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "custom:"},
			},
			wantErr: "custom workflow: node \"node1\" has non-whitelisted type \"custom:\"",
		},
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio && GOWORK=off go test ./internal/planner/ -run TestValidateCustomGraph -count=1`
Expected: FAIL — "custom type accepted" errors (custom:translate non-whitelisted).

- [ ] **Step 3: Add `isCustomType` helper** in `graph.go`, right after `isTypeAllowed` (it already imports `strings`):

```go
// isCustomType reports whether typ is a user-defined custom node type
// (prefix "custom:" with a non-empty slug). Custom types are accepted by
// ValidateCustomGraph (save path) but refused at run time (no executor yet).
func isCustomType(typ string) bool {
	return strings.HasPrefix(typ, "custom:") && len(typ) > len("custom:")
}
```

- [ ] **Step 4: Relax the type check** in `planner.go` (the `ValidateCustomGraph` loop, currently line ~175):

Replace:
```go
		if !isTypeAllowed(n.Type) {
			return fmt.Errorf("custom workflow: node %q has non-whitelisted type %q", n.ID, n.Type)
		}
```
with:
```go
		if !isTypeAllowed(n.Type) && !isCustomType(n.Type) {
			return fmt.Errorf("custom workflow: node %q has non-whitelisted type %q", n.ID, n.Type)
		}
```

(Leave `Validate` in graph.go — the LLM-graph path — strict; only the custom-workflow path is relaxed.)

- [ ] **Step 5: Run to verify it passes**

Run: `cd <repo> && GOWORK=off go test ./internal/planner/ -count=1`
Expected: PASS (new cases + existing).

- [ ] **Step 6: Commit**

```bash
git add internal/planner/graph.go internal/planner/planner.go internal/planner/planner_test.go
git commit -m "feat(planner): ValidateCustomGraph accepts custom:* node types"
```

---

### Task 2: Refuse runs containing custom nodes

**Files:**
- Modify: `internal/planner/planner.go` — add exported `HasCustomNode`.
- Test: `internal/planner/planner_test.go`
- Modify: `internal/httpapi/workflowhandlers.go` — run-guard.
- Test: `internal/httpapi/workflowhandlers_test.go`

- [ ] **Step 1: Write failing test for `HasCustomNode`** — append to `planner_test.go`:

```go
func TestHasCustomNode(t *testing.T) {
	if HasCustomNode([]WorkflowNode{{ID: "a", Type: "script"}}) {
		t.Fatal("builtin-only graph should not report custom node")
	}
	if !HasCustomNode([]WorkflowNode{
		{ID: "a", Type: "script"},
		{ID: "b", Type: "custom:translate"},
	}) {
		t.Fatal("graph with custom: node should report custom node")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd <repo> && GOWORK=off go test ./internal/planner/ -run TestHasCustomNode -count=1`
Expected: FAIL — `HasCustomNode` undefined.

- [ ] **Step 3: Add `HasCustomNode`** in `planner.go`, right after `ValidateCustomGraph`:

```go
// HasCustomNode reports whether any node is a user-defined custom type
// (custom:* prefix). Run handlers use this to refuse running workflows whose
// custom nodes have no executor yet (Phase 1). Save handlers do NOT call this.
func HasCustomNode(nodes []WorkflowNode) bool {
	for _, n := range nodes {
		if isCustomType(n.Type) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd <repo> && GOWORK=off go test ./internal/planner/ -run TestHasCustomNode -count=1`
Expected: PASS.

- [ ] **Step 5: Add the run-guard** in `runWorkflowHandler` (`workflowhandlers.go`), immediately AFTER the existing `ValidateCustomGraph` block (the one near line 162) and BEFORE the `quotaExceeded` check:

```go
		if err := planner.ValidateCustomGraph(nodes); err != nil {
			http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
			return
		}
		if planner.HasCustomNode(nodes) {
			http.Error(w, "当前 Workflow 包含自定义节点，暂不支持运行", http.StatusBadRequest)
			return
		}
```

- [ ] **Step 6: Write a handler test** — open `internal/httpapi/workflowhandlers_test.go`, find the existing run-handler test (search for `runWorkflowHandler` or a test that POSTs `/run`), and add a sibling test that seeds a workflow containing a `custom:translate` node and asserts the run endpoint returns 400 with the refusal message. Match the existing test's harness (store fakes, `httptest`, request construction). Concretely, mirror the existing run test but set the workflow nodes to:

```go
[]planner.WorkflowNode{
	{ID: "s1", Type: "script"},
	{ID: "c1", Type: "custom:translate", DependsOn: []string{"s1"}},
}
```
and assert `rec.Code == http.StatusBadRequest` and the body contains `暂不支持运行`. (Read the neighbouring run test first to reuse its setup verbatim; do not invent a new harness.)

- [ ] **Step 7: Run to verify it passes**

Run: `cd <repo> && GOWORK=off go test ./internal/httpapi/ ./internal/planner/ -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/planner/planner.go internal/planner/planner_test.go internal/httpapi/workflowhandlers.go internal/httpapi/workflowhandlers_test.go
git commit -m "feat(httpapi): refuse running workflows that contain custom nodes"
```

> **PR-1 boundary:** push, open PR (base `main`), land before PR-2 (save needs the relaxed validation).

---

# PR-2 — Frontend

### Task 3: `WorkflowNode` type + `toStudioNodes` carry label/color

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/features/workflow-canvas/canvasModel.ts`
- Test: `web/src/features/workflow-canvas/canvasModel.test.ts`

- [ ] **Step 1: Write the failing test** — append to `canvasModel.test.ts` (`toStudioNodes` describe or a new one):

```ts
describe("custom node label/color round-trip", () => {
  it("toStudioNodes carries label+color for custom nodes, omits for builtin", () => {
    const nodes: RFNode[] = [
      {
        id: "c1", type: "studio", position: { x: 0, y: 0 },
        data: { node: { id: "c1", type: "custom:translate", promptId: "", dependsOn: [], label: "翻译", color: "#7c93ff" } },
      },
      {
        id: "s1", type: "studio", position: { x: 0, y: 0 },
        data: { node: { id: "s1", type: "script", promptId: "", dependsOn: [] } },
      },
    ]
    const out = toStudioNodes(nodes, [])
    const c = out.find((n) => n.id === "c1")!
    expect(c.label).toBe("翻译")
    expect(c.color).toBe("#7c93ff")
    const s = out.find((n) => n.id === "s1")!
    expect(s.label).toBeUndefined()
    expect(s.color).toBeUndefined()
  })

  it("toReactFlow preserves label/color into data.node", () => {
    const { nodes } = toReactFlow([
      { id: "c1", type: "custom:translate", promptId: "", dependsOn: [], label: "翻译", color: "#7c93ff" },
    ])
    expect(nodes[0].data.node.label).toBe("翻译")
    expect(nodes[0].data.node.color).toBe("#7c93ff")
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: FAIL — `label` missing on `out` / type error.

- [ ] **Step 3: Add fields to `WorkflowNode`** in `web/src/lib/types.ts` (the interface at ~line 106):

```ts
export interface WorkflowNode {
  id: string
  type: string
  promptId: string
  promptText?: string
  dependsOn: string[]
  position?: { x: number; y: number }
  // 自定义节点（type 形如 custom:<slug>）的显示名与颜色（hex）。内置节点不设。
  label?: string
  color?: string
}
```

- [ ] **Step 4: Carry label/color in `toStudioNodes`** (`canvasModel.ts`) — in the `.map`, after the `if (n.promptText) out.promptText = n.promptText` line, add:

```ts
    if (n.label) out.label = n.label
    if (n.color) out.color = n.color
```

(`toReactFlow` already spreads `data: { node: n }`, so it preserves label/color with no change.)

- [ ] **Step 5: Run to verify it passes**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/types.ts web/src/features/workflow-canvas/canvasModel.ts web/src/features/workflow-canvas/canvasModel.test.ts
git commit -m "feat(web): WorkflowNode carries optional label/color for custom nodes"
```

---

### Task 4: `nodeColor.ts` helpers (isCustomType / palette / nodeDisplay / slugify)

**Files:**
- Modify: `web/src/features/workflow-canvas/nodeColor.ts`
- Test: `web/src/features/workflow-canvas/nodeColor.test.ts` (new)

- [ ] **Step 1: Write the failing test** — create `nodeColor.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { isCustomType, nodeDisplay, slugify, CUSTOM_PALETTE } from "./nodeColor"

describe("isCustomType", () => {
  it("true only for custom: with non-empty slug", () => {
    expect(isCustomType("custom:translate")).toBe(true)
    expect(isCustomType("custom:")).toBe(false)
    expect(isCustomType("script")).toBe(false)
  })
})

describe("nodeDisplay", () => {
  it("builtin → table label/color", () => {
    expect(nodeDisplay({ type: "script" })).toEqual({ label: "剧本", color: "var(--script)" })
  })
  it("custom → own label/color, with fallbacks", () => {
    expect(nodeDisplay({ type: "custom:x", label: "翻译", color: "#7c93ff" })).toEqual({ label: "翻译", color: "#7c93ff" })
    const fb = nodeDisplay({ type: "custom:x" })
    expect(fb.label).toBe("自定义")
    expect(fb.color).toMatch(/^#/)
  })
})

describe("slugify", () => {
  it("normalizes to a non-empty slug", () => {
    expect(slugify("My Step")).toBe("my-step")
    expect(slugify("翻译")).toBe("翻译")
    expect(slugify("   ")).toBe("type")
  })
})

describe("CUSTOM_PALETTE", () => {
  it("is a non-empty list of hex colors", () => {
    expect(CUSTOM_PALETTE.length).toBeGreaterThan(0)
    for (const c of CUSTOM_PALETTE) expect(c).toMatch(/^#[0-9a-f]{6}$/i)
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/nodeColor.test.ts`
Expected: FAIL — exports missing.

- [ ] **Step 3: Add the helpers** — append to `nodeColor.ts`:

```ts
export const CUSTOM_PREFIX = "custom:"

// 自定义节点类型：custom: 前缀 + 非空 slug。
export function isCustomType(type: string): boolean {
  return type.startsWith(CUSTOM_PREFIX) && type.length > CUSTOM_PREFIX.length
}

// 预设调色板：中等饱和 hex，dark-studio/light/cinematic 三主题下都可读。
// 自定义节点颜色仅从这里单选（不开放自由 hex 输入）。
export const CUSTOM_PALETTE = [
  "#7c93ff", "#22b8a6", "#e0795b", "#c879e0",
  "#5b9be0", "#e0b84a", "#6bbf59", "#e05b8a",
] as const

const DEFAULT_CUSTOM_COLOR = "#8a8f98"

// 节点显示（标签 + 颜色）：内置查表；自定义读自带 label/color，缺则兜底。
export function nodeDisplay(node: {
  type: string
  label?: string
  color?: string
}): { label: string; color: string } {
  if (isCustomType(node.type)) {
    return {
      label: node.label || "自定义",
      color: node.color || DEFAULT_CUSTOM_COLOR,
    }
  }
  return {
    label: TYPE_LABEL[node.type] ?? node.type,
    color: NODE_COLOR[node.type] ?? "var(--line)",
  }
}

// 显示名 → custom slug：小写、空白转 -、去非法字符（保留中日韩）；空则 "type"。
export function slugify(label: string): string {
  const s = label
    .trim()
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9\-_一-龥]/g, "")
  return s || "type"
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/nodeColor.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/nodeColor.ts web/src/features/workflow-canvas/nodeColor.test.ts
git commit -m "feat(web): nodeColor custom-type helpers (isCustomType/nodeDisplay/slugify/palette)"
```

---

### Task 5: canvasModel — display threading + registry + cascade + hasCustomNode

**Files:**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts`
- Test: `web/src/features/workflow-canvas/canvasModel.test.ts`

- [ ] **Step 1: Write the failing tests** — append to `canvasModel.test.ts` (add `applyTypeDisplay, collectCustomTypes, hasCustomNode, createNode` to imports if not present):

```ts
describe("custom-type registry + cascade", () => {
  const mk = (id: string, type: string, label?: string, color?: string): RFNode => ({
    id, type: "studio", position: { x: 0, y: 0 },
    data: { node: { id, type, promptId: "", dependsOn: [], ...(label ? { label } : {}), ...(color ? { color } : {}) } },
  })

  it("collectCustomTypes dedupes custom nodes by type", () => {
    const nodes = [mk("a", "custom:t", "翻译", "#111111"), mk("b", "custom:t", "翻译", "#111111"), mk("s", "script")]
    const types = collectCustomTypes(nodes)
    expect(types).toHaveLength(1)
    expect(types[0]).toEqual({ type: "custom:t", label: "翻译", color: "#111111" })
  })

  it("applyTypeDisplay updates label/color on every same-type node", () => {
    const nodes = [mk("a", "custom:t", "old", "#111111"), mk("b", "custom:t", "old", "#111111"), mk("s", "script")]
    const next = applyTypeDisplay(nodes, "custom:t", "新名", "#222222")
    const changed = next.filter((n) => n.data.node.type === "custom:t")
    expect(changed.every((n) => n.data.node.label === "新名" && n.data.node.color === "#222222")).toBe(true)
    expect(next.find((n) => n.id === "s")!.data.node.label).toBeUndefined()
  })

  it("hasCustomNode detects a custom node", () => {
    expect(hasCustomNode([mk("s", "script")])).toBe(false)
    expect(hasCustomNode([mk("s", "script"), mk("c", "custom:t")])).toBe(true)
  })

  it("createNode threads display onto the new node", () => {
    const res = createNode([], [], "custom:t", { x: 0, y: 0 }, undefined, undefined, { label: "翻译", color: "#333333" })
    const n = res.nodes[0].data.node
    expect(n.label).toBe("翻译")
    expect(n.color).toBe("#333333")
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: FAIL — `collectCustomTypes`/`applyTypeDisplay`/`hasCustomNode` undefined; `createNode` 7th arg ignored.

- [ ] **Step 3: Add `display?` param to `addNodeAt`** — replace its body:

```ts
export function addNodeAt(
  rfNodes: RFNode[],
  type: string,
  pos: { x: number; y: number },
  prompts?: Prompt[],
  id?: string,
  display?: { label?: string; color?: string },
): RFNode[] {
  const nodeId = id ?? nextNodeId(rfNodes)
  const node: WorkflowNode = {
    id: nodeId,
    type,
    promptId: defaultPromptIdFor(prompts, type),
    promptText: "",
    dependsOn: [],
    position: pos,
    ...(display?.label ? { label: display.label } : {}),
    ...(display?.color ? { color: display.color } : {}),
  }
  return [
    ...rfNodes,
    { id: nodeId, type: "studio", position: pos, data: { node } },
  ]
}
```

- [ ] **Step 4: Thread `display?` through `createNode`** — update its signature + the `addNodeAt` call:

```ts
export function createNode(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
  type: string,
  pos: { x: number; y: number },
  prompts?: Prompt[],
  source?: string,
  display?: { label?: string; color?: string },
): { nodes: RFNode[]; edges: RFEdge[]; newId: string } {
  const newId = nextNodeId(rfNodes)
  const nodes = addNodeAt(rfNodes, type, pos, prompts, newId, display)
  const edges = source
    ? [
        ...rfEdges,
        { id: `${source}->${newId}`, source, target: newId, type: "studio" },
      ]
    : rfEdges
  return { nodes, edges, newId }
}
```

- [ ] **Step 5: Thread `display?` through `insertNodeOnEdge`** — add the param and apply to the inserted node. Update its signature to add `display?: { label?: string; color?: string }` after `prompts`, and set `label`/`color` on the `node` literal it builds:

```ts
  const node: WorkflowNode = {
    id: newId,
    type,
    promptId: defaultPromptIdFor(prompts, type),
    promptText: "",
    dependsOn: [],
    position: midPos,
    ...(display?.label ? { label: display.label } : {}),
    ...(display?.color ? { color: display.color } : {}),
  }
```

- [ ] **Step 6: Add `collectCustomTypes`, `applyTypeDisplay`, `hasCustomNode`** — append to `canvasModel.ts` and add `import { isCustomType, nodeDisplay } from "./nodeColor"` at the top of the file:

```ts
// 本工作流的自定义类型登记表：从画布上的 custom: 节点按 type 去重（label/color 取 nodeDisplay）。
export function collectCustomTypes(
  rfNodes: RFNode[],
): { type: string; label: string; color: string }[] {
  const seen = new Map<string, { type: string; label: string; color: string }>()
  for (const n of rfNodes) {
    const t = n.data.node.type
    if (isCustomType(t) && !seen.has(t)) {
      const d = nodeDisplay(n.data.node)
      seen.set(t, { type: t, label: d.label, color: d.color })
    }
  }
  return [...seen.values()]
}

// 改名/改色级联：把同 type 的所有节点的 label/color 批量更新（纯函数）。
export function applyTypeDisplay(
  rfNodes: RFNode[],
  type: string,
  label: string,
  color: string,
): RFNode[] {
  return rfNodes.map((n) =>
    n.data.node.type === type
      ? { ...n, data: { ...n.data, node: { ...n.data.node, label, color } } }
      : n,
  )
}

// 画布是否含自定义节点（用于禁运行）。
export function hasCustomNode(rfNodes: RFNode[]): boolean {
  return rfNodes.some((n) => isCustomType(n.data.node.type))
}
```

- [ ] **Step 7: Run to verify it passes**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/workflow-canvas/canvasModel.ts web/src/features/workflow-canvas/canvasModel.test.ts
git commit -m "feat(web): canvasModel custom-type registry, cascade, display threading"
```

---

### Task 6: Render custom nodes via `nodeDisplay`

**Files:**
- Modify: `web/src/features/workflow-canvas/WorkflowNode.tsx`

- [ ] **Step 1: Switch label/color to `nodeDisplay`** — in `WorkflowNode.tsx`, replace the two lines that compute `color`/`typeLabel`:

```tsx
  const color = NODE_COLOR[node.type] ?? "var(--line)"
  const typeLabel = TYPE_LABEL[node.type] ?? node.type
```
with:
```tsx
  const { label: typeLabel, color } = nodeDisplay(node)
```

And update the import line `import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"` to:
```tsx
import { nodeDisplay } from "./nodeColor"
```
(Remove `NODE_COLOR`/`TYPE_LABEL` from this file if no longer referenced — check the file; the run-dot already receives `color` via prop, so they should be gone.)

- [ ] **Step 2: Verify build + existing node tests**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/WorkflowNode.test.tsx`
Expected: PASS (built-in nodes still render 剧本/分镜/资产 with their colors).

- [ ] **Step 3: Commit**

```bash
git add web/src/features/workflow-canvas/WorkflowNode.tsx
git commit -m "feat(web): WorkflowNode renders label/color via nodeDisplay (custom-aware)"
```

---

### Task 7: `CustomTypeDialog` component

**Files:**
- Create: `web/src/features/workflow-canvas/CustomTypeDialog.tsx`
- Test: `web/src/features/workflow-canvas/CustomTypeDialog.test.tsx`

- [ ] **Step 1: Write the failing test** — create `CustomTypeDialog.test.tsx`:

```tsx
import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { CustomTypeDialog } from "./CustomTypeDialog"

describe("CustomTypeDialog", () => {
  it("submits name + first palette color for a new type", () => {
    const onSubmit = vi.fn()
    render(<CustomTypeDialog open mode="create" onSubmit={onSubmit} onCancel={() => {}} />)
    fireEvent.change(screen.getByLabelText("显示名"), { target: { value: "翻译" } })
    fireEvent.click(screen.getByRole("button", { name: "确认" }))
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ label: "翻译" }))
    expect(onSubmit.mock.calls[0][0].color).toMatch(/^#/)
  })

  it("disables 确认 when name is empty", () => {
    render(<CustomTypeDialog open mode="create" onSubmit={() => {}} onCancel={() => {}} />)
    expect(screen.getByRole("button", { name: "确认" })).toBeDisabled()
  })

  it("prefills name+color in edit mode", () => {
    render(
      <CustomTypeDialog open mode="edit" initial={{ label: "旧名", color: "#22b8a6" }} onSubmit={() => {}} onCancel={() => {}} />,
    )
    expect((screen.getByLabelText("显示名") as HTMLInputElement).value).toBe("旧名")
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/CustomTypeDialog.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the component** — create `CustomTypeDialog.tsx` (uses the shared dialog primitive, mirrors `features/common/crud/ConfirmDialog.tsx`):

```tsx
import { useState } from "react"
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"
import { CUSTOM_PALETTE } from "./nodeColor"

export interface CustomTypePayload {
  label: string
  color: string
}

export interface CustomTypeDialogProps {
  open: boolean
  mode: "create" | "edit"
  initial?: CustomTypePayload
  onSubmit: (payload: CustomTypePayload) => void
  onCancel: () => void
}

// 新建/编辑自定义类型：显示名 + 预设调色板单选。slug/类型由画布层根据 label 生成。
export function CustomTypeDialog({
  open, mode, initial, onSubmit, onCancel,
}: CustomTypeDialogProps) {
  const [label, setLabel] = useState(initial?.label ?? "")
  const [color, setColor] = useState(initial?.color ?? CUSTOM_PALETTE[0])
  const valid = label.trim().length > 0

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onCancel() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{mode === "create" ? "新建自定义类型" : "编辑自定义类型"}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3 py-2">
          <label className="flex flex-col gap-1 text-[12px] text-text-2">
            显示名
            <input
              aria-label="显示名"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="如：翻译 / 配音脚本"
              className="rounded-md border border-line bg-bg-base px-2 py-1.5 text-[13px] text-text-1 focus:border-amber focus:outline-none"
            />
          </label>
          <div className="flex flex-col gap-1.5 text-[12px] text-text-2">
            颜色
            <div className="flex flex-wrap gap-2">
              {CUSTOM_PALETTE.map((c) => (
                <button
                  key={c}
                  type="button"
                  aria-label={`颜色 ${c}`}
                  onClick={() => setColor(c)}
                  className={
                    "h-6 w-6 rounded-full border-2 " +
                    (color === c ? "border-text-1" : "border-transparent")
                  }
                  style={{ backgroundColor: c }}
                />
              ))}
            </div>
          </div>
        </div>
        <DialogFooter>
          <UiButton variant="outline" onClick={onCancel}>取消</UiButton>
          <UiButton
            disabled={!valid}
            onClick={() => onSubmit({ label: label.trim(), color })}
          >
            确认
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/CustomTypeDialog.test.tsx`
Expected: PASS. (If the shared `Dialog` requires a portal/root that breaks jsdom, check how `ConfirmDialog.test` renders — match it; do not stub the primitive.)

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/CustomTypeDialog.tsx web/src/features/workflow-canvas/CustomTypeDialog.test.tsx
git commit -m "feat(web): CustomTypeDialog for creating/editing custom node types"
```

---

### Task 8: NodeTypePicker lists custom types

**Files:**
- Modify: `web/src/features/workflow-canvas/NodeTypePicker.tsx`

- [ ] **Step 1: Extend props + render** — replace `NodeTypePicker.tsx` body so it accepts `customTypes` and a richer `onPick`:

```tsx
import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"

const PICKER_TYPES = ["script", "storyboard", "asset"] as const

export interface PickerCustomType {
  type: string
  label: string
  color: string
}

export interface NodeTypePickerProps {
  open: boolean
  screenX: number
  screenY: number
  customTypes?: PickerCustomType[]
  // 内置项：onPick(type)；自定义项：onPick(type, {label,color})。
  onPick: (type: string, display?: { label: string; color: string }) => void
  onClose: () => void
}

export function NodeTypePicker({
  open, screenX, screenY, customTypes = [], onPick, onClose,
}: NodeTypePickerProps) {
  if (!open) return null
  return (
    <>
      <div data-slot="picker-overlay" className="fixed inset-0 z-40" onClick={onClose} />
      <div
        data-slot="node-type-picker"
        role="menu"
        className="fixed z-50 rounded-md border border-line bg-bg-raised p-1 shadow-lg"
        style={{ left: screenX, top: screenY }}
      >
        {PICKER_TYPES.map((t) => (
          <button
            key={t}
            type="button"
            role="menuitem"
            onClick={() => onPick(t)}
            className="flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-[12px] text-text-1 hover:bg-bg-surface"
          >
            <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: NODE_COLOR[t] }} />
            {TYPE_LABEL[t]}
          </button>
        ))}
        {customTypes.length > 0 && <div className="my-1 border-t border-line" />}
        {customTypes.map((c) => (
          <button
            key={c.type}
            type="button"
            role="menuitem"
            onClick={() => onPick(c.type, { label: c.label, color: c.color })}
            className="flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-[12px] text-text-1 hover:bg-bg-surface"
          >
            <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: c.color }} />
            {c.label}
          </button>
        ))}
      </div>
    </>
  )
}
```

- [ ] **Step 2: Verify build** (consumer updates land in Task 9; type errors there are expected until then)

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/` — the picker file compiles; `WorkflowCanvas` may show a type error on `onPick` signature until Task 9. If so, proceed to Task 9 before running the full suite.

- [ ] **Step 3: Commit**

```bash
git add web/src/features/workflow-canvas/NodeTypePicker.tsx
git commit -m "feat(web): NodeTypePicker lists workflow custom types"
```

---

### Task 9: WorkflowCanvas wiring (registry, dialog, cascade, all entry points, disable-run) + NodePalette

**Files:**
- Modify: `web/src/features/workflow-canvas/NodePalette.tsx`
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`

- [ ] **Step 1: Extend NodePalette** — add custom-type props and rendering. Replace `NodePaletteProps` + the JSX so it accepts the registry and callbacks:

Add to props:
```tsx
export interface NodePaletteProps {
  onStandardPipeline: () => void
  onAutoTidy?: () => void
  // 本工作流自定义类型 + 管理回调（Phase 1 自定义节点）。
  customTypes?: { type: string; label: string; color: string }[]
  onAddCustomType?: () => void
  onEditCustomType?: (type: string) => void
}
```
Destructure them, and after the built-in `PALETTE_TYPES` chip block add a custom section:
```tsx
        {(customTypes ?? []).map((c) => (
          <div
            key={c.type}
            data-slot="palette-chip-custom"
            draggable
            onDragStart={(e) => {
              e.dataTransfer.setData(PALETTE_DND_TYPE, c.type)
              e.dataTransfer.effectAllowed = "move"
            }}
            className="group flex cursor-grab items-center gap-2 rounded-md border border-line bg-bg-base px-2.5 py-1.5 hover:border-text-3 active:cursor-grabbing"
            title="拖入画布添加"
          >
            <span aria-hidden className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: c.color }} />
            <span className="flex-1 text-[12px] text-text-1">{c.label}</span>
            {onEditCustomType && (
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); onEditCustomType(c.type) }}
                className="text-[11px] text-text-3 opacity-0 group-hover:opacity-100 hover:text-text-1"
              >
                编辑
              </button>
            )}
          </div>
        ))}
        {onAddCustomType && (
          <button
            type="button"
            onClick={onAddCustomType}
            className="rounded-md border border-dashed border-line px-2.5 py-1.5 text-left text-[12px] text-text-3 hover:border-text-3 hover:text-text-1"
          >
            + 自定义类型
          </button>
        )}
```

- [ ] **Step 2: Wire registry + dialog state in WorkflowCanvas** — add imports:
```tsx
import { CustomTypeDialog, type CustomTypePayload } from "./CustomTypeDialog"
```
add to the canvasModel import list: `applyTypeDisplay, collectCustomTypes, hasCustomNode, slugify` (slugify is in nodeColor — import `slugify`, `isCustomType` from `./nodeColor` where NODE_COLOR is already imported).

Add state + derived registry near the other `useState`/`useMemo`:
```tsx
  const customTypes = useMemo(() => collectCustomTypes(rfNodes as RFNode[]), [rfNodes])
  const [typeDialog, setTypeDialog] = useState<
    | { mode: "create" }
    | { mode: "edit"; type: string; initial: CustomTypePayload }
    | null
  >(null)
```

- [ ] **Step 3: Update `onPickType` to thread display** — change its signature and the create/insert calls:
```tsx
  const onPickType = useCallback(
    (type: string, display?: { label: string; color: string }) => {
      if (!picker) return
      if (picker.mode === "create") {
        const built = createNode(getNodes(), getEdges(), type, picker.flow, prompts, picker.source, display)
        // ...unchanged guard + takeSnapshot + setRfNodes/setRfEdges using built...
      } else {
        const candidate = insertNodeOnEdge(getNodes(), getEdges(), picker.edgeId, type, picker.flow, prompts, display)
        // ...unchanged...
      }
      setPicker(null)
    },
    [picker, getNodes, getEdges, prompts, setRfNodes, setRfEdges, takeSnapshot],
  )
```
(Keep the existing bodies; only add the `display` param and pass it as the new last arg to `createNode` and `insertNodeOnEdge`.)

- [ ] **Step 4: Custom-type dialog submit handler** — add:
```tsx
  // 新建自定义类型 → 生成唯一 custom:slug → 在画布中央建一个该类型节点。
  const onCreateCustomType = useCallback(
    (p: CustomTypePayload) => {
      const base = `custom:${slugify(p.label)}`
      const existing = new Set(collectCustomTypes(getNodes()).map((c) => c.type))
      let type = base
      let i = 2
      while (existing.has(type)) { type = `${base}-${i}`; i += 1 }
      takeSnapshot()
      const pos = screenToFlowPosition({ x: 300, y: 200 })
      setRfNodes((nds) => createNode(nds as RFNode[], [], type, pos, prompts, undefined, p).nodes)
      setTypeDialog(null)
    },
    [getNodes, prompts, screenToFlowPosition, setRfNodes, takeSnapshot],
  )

  // 编辑自定义类型 → 改名/改色级联同 type 全部节点。
  const onEditCustomType = useCallback(
    (p: CustomTypePayload) => {
      if (typeDialog?.mode !== "edit") return
      takeSnapshot()
      setRfNodes((nds) => applyTypeDisplay(nds as RFNode[], typeDialog.type, p.label, p.color))
      setTypeDialog(null)
    },
    [typeDialog, setRfNodes, takeSnapshot],
  )
```

- [ ] **Step 5: Update onDrop to carry custom display** — the palette drag of a custom type needs label/color. In `onDrop`, after resolving `type`:
```tsx
      const display = isCustomType(type)
        ? customTypes.find((c) => c.type === type)
        : undefined
      setRfNodes((nds) => addNodeAt(nds as RFNode[], type, pos, prompts, undefined, display))
```
(Add `customTypes`, `isCustomType` to the `onDrop` useCallback deps.)

- [ ] **Step 6: Pass props through** — update the JSX:
  - `<NodePalette ... customTypes={customTypes} onAddCustomType={() => setTypeDialog({ mode: "create" })} onEditCustomType={(type) => { const c = customTypes.find((x) => x.type === type); if (c) setTypeDialog({ mode: "edit", type, initial: { label: c.label, color: c.color } }) }} />`
  - `<NodeTypePicker ... customTypes={customTypes} onPick={onPickType} />`
  - Render the dialog after `<CanvasContextMenu .../>`:
```tsx
          <CustomTypeDialog
            open={!!typeDialog}
            mode={typeDialog?.mode ?? "create"}
            initial={typeDialog?.mode === "edit" ? typeDialog.initial : undefined}
            onSubmit={typeDialog?.mode === "edit" ? onEditCustomType : onCreateCustomType}
            onCancel={() => setTypeDialog(null)}
          />
```

- [ ] **Step 7: Disable run when custom nodes present** — compute `const runDisabled = useMemo(() => hasCustomNode(rfNodes as RFNode[]), [rfNodes])`. In the header where `<ModeToggle mode="edit" onChange={onModeChange} />` renders, gate it:
```tsx
          {!isCreate && onModeChange && (
            runDisabled ? (
              <span className="text-[12px] text-text-3" title="当前 Workflow 包含自定义节点，暂不支持运行">
                含自定义节点 · 暂不支持运行
              </span>
            ) : (
              <ModeToggle mode="edit" onChange={onModeChange} />
            )
          )}
```

- [ ] **Step 8: Run full suite + type-check**

Run: `cd web && pnpm test`
Expected: PASS, type-check clean. (If the known `workflow-route.test.tsx` flake fails under parallel load, re-run once.)

- [ ] **Step 9: Manual verify (note for reviewer)** — on `:5173`, switch a workflow to 编辑 mode: "+ 自定义类型" → name+color → node appears; drag it again from palette; rename/recolor cascades; right-click 添加节点 / 拖到空白 / 尾部"+" / 边插入 all list the custom type; 保存 then reload keeps custom nodes; the run toggle shows "含自定义节点 · 暂不支持运行".

- [ ] **Step 10: Commit**

```bash
git add web/src/features/workflow-canvas/NodePalette.tsx web/src/features/workflow-canvas/WorkflowCanvas.tsx
git commit -m "feat(web): custom node types — palette, dialog, all entry points, disable-run"
```

---

### Task 10: PropertiesPanel custom-node display

**Files:**
- Modify: `web/src/features/workflow-canvas/PropertiesPanel.tsx`

- [ ] **Step 1: Branch the type section for custom nodes** — read the panel; where it renders the task-type `<Select>` (the three built-in items) and the prompt selector, guard with `isCustomType(node.type)`:
  - For a custom node: hide the built-in type `<Select>` and the prompt selector; instead show a read-only row with the custom type's `nodeDisplay(node).label` + a swatch of `node.color`, and an "编辑类型" button that calls a new optional prop `onEditType?: () => void`.
  - For built-in nodes: unchanged.

Add the import `import { isCustomType, nodeDisplay } from "./nodeColor"` and the prop `onEditType?: () => void` to `PropertiesPanelProps`. Wire `onEditType` in `WorkflowCanvas` to open the edit dialog for `selected.type` (reuse the same `setTypeDialog({ mode: "edit", ... })` logic from Task 9, looked up via `customTypes`).

- [ ] **Step 2: Run panel tests + full suite**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/PropertiesPanel.test.tsx && pnpm test`
Expected: PASS (built-in node editing unchanged; custom node shows the read-only type row).

- [ ] **Step 3: Commit**

```bash
git add web/src/features/workflow-canvas/PropertiesPanel.tsx web/src/features/workflow-canvas/WorkflowCanvas.tsx
git commit -m "feat(web): PropertiesPanel shows custom node type (read-only + 编辑类型)"
```

> **PR-2 boundary:** push (base `main`), open PR, land.

---

## Self-Review notes

- **Spec coverage:** data model (label/color, no new table) → Tasks 3,5; backend relax + run-refuse → Tasks 1,2; `custom:` prefix + non-empty slug → Tasks 1,4; per-workflow registry → Task 5 (`collectCustomTypes`); rename/recolor cascade → Tasks 5,9 (`applyTypeDisplay`); all entry points (palette/picker/right-click/drag-empty/edge-insert/trailing+) → Tasks 8,9 (picker `customTypes` + `display` threading + `onDrop`); preset-palette-only color → Task 7; disable run → Tasks 5,9 (`hasCustomNode`); built-in↔custom conversion excluded → Task 10 (read-only type for custom).
- **Type consistency:** `display?: { label?: string; color?: string }` is the create-path shape (`addNodeAt`/`createNode`/`insertNodeOnEdge`); `nodeDisplay`/`collectCustomTypes` return `{ label: string; color: string }` (non-optional); picker `onPick(type, { label, color })` matches `onPickType`. `CustomTypePayload = { label, color }` is the dialog shape.
- **Decision locked:** color is hex from `CUSTOM_PALETTE` only; `type` slug immutable after creation (edit changes label/color only, via `applyTypeDisplay`).
