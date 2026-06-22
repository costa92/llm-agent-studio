# Canvas Interactions Phase D (beequant parity) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four missing beequant-class canvas interactions — left-drag pan + Shift box-select, edge reconnect, node trailing "+" quick-add, and right-click context menus — to the studio workflow canvas.

**Architecture:** Reuse-first (Approach A). Push new graph logic into pure `canvasModel.ts` helpers (`reconnectEdge`, `createNode`) tested with vitest; wire them in `WorkflowCanvas.tsx` reusing the existing `picker` selector, `findGraphError` cycle guard, `takeSnapshot` discipline, and `CanvasActionsContext` channel. Two PRs: D1 (drag model + reconnect), D2 (trailing "+" + context menus).

**Tech Stack:** React + TypeScript, `@xyflow/react` v12, vitest 4, pnpm. All work under `web/src/features/workflow-canvas/`. No backend changes.

**Invariants (do not break):** EDGES are the sole source of `dependsOn` (only mutate `rfEdges`, never `data.node.dependsOn`); every edge-creating/reconnecting path runs `findGraphError` on a candidate model and toasts+aborts on non-empty; edge id is always `` `${source}->${target}` ``; `takeSnapshot()` before any `{nodes,edges}` mutation, never on guard-rejected ops, never on pure selection change; amber tri-theme tokens only.

**Working dir:** `llm-agent-studio` repo, branch `feat/canvas-interactions-phase-d` (already created; spec committed). Run tests from `web/`: `pnpm test`. Run a single file: `pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`.

---

## File Structure

**PR-D1**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts` — add `reconnectEdge`.
- Test: `web/src/features/workflow-canvas/canvasModel.test.ts` — add `reconnectEdge` describe block.
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx` — add `onReconnect`; change drag/selection props; update empty-canvas hint.

**PR-D2**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts` — add `createNode` (source-optional).
- Test: `web/src/features/workflow-canvas/canvasModel.test.ts` — add `createNode` describe block.
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx` — refactor `onPickType` create branch to `createNode`; make `picker` create `source` optional; add `onQuickAddFrom`; extract `doPaste`/`selectAll`; add context-menu state + handlers + item builder.
- Modify: `web/src/features/workflow-canvas/CanvasActionsContext.tsx` — add `onQuickAddFrom`.
- Modify: `web/src/features/workflow-canvas/WorkflowNode.tsx` — add hover trailing "+" button.
- Create: `web/src/features/workflow-canvas/CanvasContextMenu.tsx` — generic floating menu.
- Test: `web/src/features/workflow-canvas/CanvasContextMenu.test.tsx` — render + dispatch.

---

# PR-D1 — Drag model + edge reconnect

### Task 1: `reconnectEdge` helper

**Files:**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts` (add near `insertNodeOnEdge`, end of file)
- Test: `web/src/features/workflow-canvas/canvasModel.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `canvasModel.test.ts`. Add `reconnectEdge` to the import list at the top (line 2-16 block) as well.

```ts
describe("reconnectEdge", () => {
  it("removes the old edge and adds a re-keyed edge for the new connection", () => {
    const { edges } = toReactFlow(chain) // script-1->storyboard-1, storyboard-1->asset-1
    const next = reconnectEdge(edges as RFEdge[], "storyboard-1->asset-1", {
      source: "script-1",
      target: "asset-1",
    })
    const ids = next.map((e) => e.id).sort()
    expect(ids).toEqual(["script-1->asset-1", "script-1->storyboard-1"])
    const re = next.find((e) => e.id === "script-1->asset-1")
    expect(re).toMatchObject({ source: "script-1", target: "asset-1", type: "studio" })
  })

  it("produces a candidate graph findGraphError can reject when reconnect would cycle", () => {
    const { nodes, edges } = toReactFlow(chain)
    // reconnect script-1->storyboard-1 into asset-1->storyboard-1: keeps storyboard-1->asset-1,
    // adds the back-edge → storyboard-1 ↔ asset-1 cycle.
    const candidateEdges = reconnectEdge(edges as RFEdge[], "script-1->storyboard-1", {
      source: "asset-1",
      target: "storyboard-1",
    })
    const err = findGraphError(toStudioNodes(nodes as RFNode[], candidateEdges))
    expect(err).toBeTruthy()
  })

  it("leaves other edges untouched and is a no-op id-wise when old id is absent", () => {
    const { edges } = toReactFlow(chain)
    const next = reconnectEdge(edges as RFEdge[], "missing->edge", {
      source: "script-1",
      target: "asset-1",
    })
    // old id absent → nothing filtered, new edge appended
    expect(next).toHaveLength(3)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: FAIL — `reconnectEdge is not a function` / import error.

- [ ] **Step 3: Write minimal implementation**

Append to `canvasModel.ts` (after `insertNodeOnEdge`):

```ts
// 连线重连（Phase D）：移除旧边、按新 source/target 追加重键后的边，其余边不动。
// 纯函数：环检测由调用方用 toStudioNodes(...)+findGraphError 在提交前做。
export function reconnectEdge(
  rfEdges: RFEdge[],
  oldEdgeId: string,
  conn: { source: string; target: string },
): RFEdge[] {
  return [
    ...rfEdges.filter((e) => e.id !== oldEdgeId),
    {
      id: `${conn.source}->${conn.target}`,
      source: conn.source,
      target: conn.target,
      type: "studio",
    },
  ]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: PASS (all 3 new cases + existing).

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/canvasModel.ts web/src/features/workflow-canvas/canvasModel.test.ts
git commit -m "feat(web): add reconnectEdge canvas helper"
```

---

### Task 2: Wire `onReconnect` in WorkflowCanvas

**Files:**
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`

- [ ] **Step 1: Import the helper and Edge type**

In the `canvasModel` import block (lines 34-48) add `reconnectEdge`. In the `@xyflow/react` import block (lines 2-18) add `type Edge`.

- [ ] **Step 2: Add the `onReconnect` callback**

Insert after `onConnect` (after line 213):

```tsx
  // ── 连线重连（Phase D）──────────────────────────────────────
  // 拖动已有边的端点到新节点：建候选图（去旧边+加新边）跑环守卫；通过则重键。
  // 拖到空白处时 ReactFlow 不触发 onReconnect → 边自动还原（不删，删除走 ×/Delete）。
  const onReconnect = useCallback(
    (oldEdge: Edge, conn: Connection) => {
      if (!conn.source || !conn.target) return
      const next = reconnectEdge(rfEdges, oldEdge.id, {
        source: conn.source,
        target: conn.target,
      })
      const err = findGraphError(toStudioNodes(rfNodes as RFNode[], next))
      if (err) {
        toast.error(err)
        return
      }
      takeSnapshot()
      setRfEdges(next)
    },
    [rfNodes, rfEdges, setRfEdges, takeSnapshot],
  )
```

- [ ] **Step 3: Pass `onReconnect` to `<ReactFlow>`**

Add the prop alongside `onConnect` (line 688):

```tsx
              onConnect={onConnect}
              onReconnect={onReconnect}
```

- [ ] **Step 4: Verify build + no regression**

Run: `cd web && pnpm test`
Expected: PASS (existing suite green; type-check via `tsr generate` clean).

- [ ] **Step 5: Manual verify (note for reviewer)**

On `:5173` workflow editor: drag an existing edge's target endpoint onto another node → edge re-keys; drag onto empty → edge snaps back unchanged; drag to form a cycle → toast error, edge reverts. Undo (⌘Z) reverts a successful reconnect.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/workflow-canvas/WorkflowCanvas.tsx
git commit -m "feat(web): edge reconnect on canvas (cycle-guarded, revert on empty)"
```

---

### Task 3: Drag/selection interaction model

**Files:**
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`

- [ ] **Step 1: Change the ReactFlow drag/selection props**

Replace lines 704-706:

```tsx
              selectionOnDrag
              panOnDrag={[1, 2]}
              selectionMode={SelectionMode.Partial}
```

with:

```tsx
              panOnDrag
              selectionKeyCode="Shift"
              selectionMode={SelectionMode.Partial}
```

(Left-drag now pans; hold Shift and drag to box-select. `selectionMode` and the existing `multiSelectionKeyCode` default are unchanged, so Shift+click add-to-selection still works.)

- [ ] **Step 2: Update the empty-canvas hint text**

Replace the hint span (lines 730-732):

```tsx
                <span className="text-[11px] text-text-3">
                  左键框选，中键/右键平移
                </span>
```

with:

```tsx
                <span className="text-[11px] text-text-3">
                  左键拖拽平移，Shift+拖拽框选
                </span>
```

> Note: the "右键打开菜单" mention is **deferred to PR-D2 Task 9** (don't promise the context menu before it ships). PR-D2 Task 9 should append it to this hint once menus exist.

- [ ] **Step 3: Verify build + no regression**

Run: `cd web && pnpm test`
Expected: PASS.

- [ ] **Step 4: Manual verify (note for reviewer)**

On `:5173`: left-drag empty canvas pans; Shift+drag draws a selection box; Shift+click adds nodes to selection; dragging a node still moves it (alignment guides intact).

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/WorkflowCanvas.tsx
git commit -m "feat(web): canvas pans on left-drag, Shift+drag box-selects (beequant model)"
```

> **PR-D1 boundary:** push branch, open PR, land before starting PR-D2.

---

# PR-D2 — Trailing "+" quick-add + right-click context menus

### Task 4: `createNode` helper (source-optional)

**Files:**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts`
- Test: `web/src/features/workflow-canvas/canvasModel.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `canvasModel.test.ts` (and add `createNode` to the import list):

```ts
describe("createNode", () => {
  it("with a source: adds a node and an edge source->newId", () => {
    const { nodes, edges } = toReactFlow(chain)
    const res = createNode(
      nodes as RFNode[],
      edges as RFEdge[],
      "asset",
      { x: 10, y: 20 },
      undefined,
      "asset-1",
    )
    expect(res.nodes).toHaveLength(4)
    const added = res.nodes.find((n) => n.id === res.newId)
    expect(added?.data.node.type).toBe("asset")
    expect(added?.position).toEqual({ x: 10, y: 20 })
    expect(res.edges.map((e) => e.id)).toContain(`asset-1->${res.newId}`)
  })

  it("without a source: adds only a node, no new edge", () => {
    const { nodes, edges } = toReactFlow(chain)
    const res = createNode(
      nodes as RFNode[],
      edges as RFEdge[],
      "script",
      { x: 0, y: 0 },
    )
    expect(res.nodes).toHaveLength(4)
    expect(res.edges).toHaveLength(edges.length) // unchanged
  })

  it("assigns a fresh non-colliding id", () => {
    const { nodes, edges } = toReactFlow(chain)
    const res = createNode(nodes as RFNode[], edges as RFEdge[], "script", { x: 0, y: 0 })
    expect(nodes.map((n) => n.id)).not.toContain(res.newId)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: FAIL — `createNode is not a function`.

- [ ] **Step 3: Write minimal implementation**

Append to `canvasModel.ts` (after `reconnectEdge`):

```ts
// 建节点（Phase D，泛化 onConnectEnd/边插入的「建点」语义）：
// 有 source → 同时连 source→新节点；无 source → 仅建点。复用 addNodeAt + nextNodeId。
export function createNode(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
  type: string,
  pos: { x: number; y: number },
  prompts?: Prompt[],
  source?: string,
): { nodes: RFNode[]; edges: RFEdge[]; newId: string } {
  const newId = nextNodeId(rfNodes)
  const nodes = addNodeAt(rfNodes, type, pos, prompts, newId)
  const edges = source
    ? [
        ...rfEdges,
        { id: `${source}->${newId}`, source, target: newId, type: "studio" },
      ]
    : rfEdges
  return { nodes, edges, newId }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/canvasModel.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/canvasModel.ts web/src/features/workflow-canvas/canvasModel.test.ts
git commit -m "feat(web): add source-optional createNode canvas helper"
```

---

### Task 5: Make `picker` create source-optional; route through `createNode`

**Files:**
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`

- [ ] **Step 1: Widen the `picker` create variant type**

Change the `picker` state type (lines 136-140) so `create` carries `source?`:

```tsx
  const [picker, setPicker] = useState<
    | { mode: "create"; screenX: number; screenY: number; flow: { x: number; y: number }; source?: string }
    | { mode: "insert"; screenX: number; screenY: number; flow: { x: number; y: number }; edgeId: string }
    | null
  >(null)
```

- [ ] **Step 2: Import `createNode`**

Add `createNode` to the `canvasModel` import block (lines 34-48).

- [ ] **Step 3: Refactor the `onPickType` create branch**

Replace the create branch body (lines 251-277) with:

```tsx
      if (picker.mode === "create") {
        const built = createNode(
          getNodes(),
          getEdges(),
          type,
          picker.flow,
          prompts,
          picker.source,
        )
        const err = findGraphError(toStudioNodes(built.nodes, built.edges))
        if (err) {
          toast.error(err)
          setPicker(null)
          return
        }
        takeSnapshot()
        setRfNodes(built.nodes)
        setRfEdges(built.edges)
      } else {
```

(The `nextNodeId`/`addNodeAt` inline calls and the manual edge push are now inside `createNode`. Leave the `insert` branch and trailing `setPicker(null)` unchanged.)

- [ ] **Step 4: Verify build + no regression**

Run: `cd web && pnpm test`
Expected: PASS. Existing "drag-from-handle-to-empty → picker → create+connect" flow still works (now `source` is set, same behavior).

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/WorkflowCanvas.tsx
git commit -m "refactor(web): route picker create through source-optional createNode"
```

---

### Task 6: Node trailing "+" quick-add

**Files:**
- Modify: `web/src/features/workflow-canvas/CanvasActionsContext.tsx`
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`
- Modify: `web/src/features/workflow-canvas/WorkflowNode.tsx`

- [ ] **Step 1: Extend `CanvasActions`**

In `CanvasActionsContext.tsx`, add to the interface (after `onInsertOnEdge`, line 12) and the default object (after line 21):

```tsx
  // 节点尾部「+」：在该节点下方快加下游节点（screenX/Y 用于浮层选择器定位）。
  onQuickAddFrom: (nodeId: string, screenX: number, screenY: number) => void
```

```tsx
  onQuickAddFrom: noop,
```

- [ ] **Step 2: Add the `onQuickAddFrom` handler in WorkflowCanvas**

Insert after `onInsertOnEdge` (after line 396):

```tsx
  // 节点尾部「+」快加（Phase D）：新节点落在 source 节点正下方（版式整洁，不取光标），
  // 选择器浮层落在点击 screen 坐标；选中类型后走 create(source=nodeId)。
  const onQuickAddFrom = useCallback(
    (nodeId: string, screenX: number, screenY: number) => {
      const src = getNodes().find((n) => n.id === nodeId)
      const flow = src
        ? { x: src.position.x, y: src.position.y + 120 }
        : screenToFlowPosition({ x: screenX, y: screenY })
      setPicker({ mode: "create", screenX, screenY, flow, source: nodeId })
    },
    [getNodes, screenToFlowPosition],
  )
```

- [ ] **Step 3: Add `onQuickAddFrom` to the `canvasActions` memo**

Update the memo (lines 398-401):

```tsx
  const canvasActions = useMemo(
    () => ({ onDuplicateNode, onDeleteNode, onDeleteEdge, onInsertOnEdge, onQuickAddFrom }),
    [onDuplicateNode, onDeleteNode, onDeleteEdge, onInsertOnEdge, onQuickAddFrom],
  )
```

- [ ] **Step 4: Add the "+" button to WorkflowNode**

In `WorkflowNode.tsx`: pull `onQuickAddFrom` from the actions hook (line 24), add `group relative` to the card's className (line 35: `... shadow-sm min-w-[140px]` → append ` group relative`), and add the button right before the source `<Handle>` (line 75). Full edit of those spots:

Line 24:
```tsx
  const { onDuplicateNode, onDeleteNode, onQuickAddFrom } = useCanvasActions()
```

Line 35 className (append `group relative`):
```tsx
      className="group relative flex items-center gap-2.5 rounded-lg border border-line bg-bg-surface px-3 py-2 shadow-sm min-w-[140px]"
```

Insert before `<Handle type="source" ... />` (line 75):
```tsx
      {/* 尾部「+」快加（Phase D）：hover 提亮，nodrag 防触发画布拖拽；run mode 隐藏。 */}
      {!isRunMode && (
        <button
          type="button"
          aria-label="添加下游节点"
          title="添加下游节点"
          className="nodrag nopan absolute -bottom-3 left-1/2 z-10 grid h-5 w-5 -translate-x-1/2 place-items-center rounded-full border border-line bg-bg-raised text-[12px] leading-none text-text-2 opacity-0 shadow transition group-hover:opacity-100 hover:text-text-1"
          onClick={(e) => {
            e.stopPropagation()
            onQuickAddFrom(id, e.clientX, e.clientY)
          }}
        >
          +
        </button>
      )}
```

- [ ] **Step 5: Verify build + no regression**

Run: `cd web && pnpm test`
Expected: PASS.

- [ ] **Step 6: Manual verify (note for reviewer)**

Hover a node → "+" fades in below it; click → type picker at cursor → pick → new node appears below, connected from the source node; cycle-impossible so no toast. "+" hidden in run mode.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/workflow-canvas/CanvasActionsContext.tsx web/src/features/workflow-canvas/WorkflowCanvas.tsx web/src/features/workflow-canvas/WorkflowNode.tsx
git commit -m "feat(web): node trailing + quick-add downstream node"
```

---

### Task 7: Extract `doPaste`/`selectAll` callbacks

**Files:**
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`

- [ ] **Step 1: Add the two callbacks**

Insert after `onQuickAddFrom` (Task 6 location). `doPaste(at?)` translates clones so their bounding-box top-left lands at `at` (flow coords) when given, else the existing +32/+32 offset:

```tsx
  // 粘贴（Phase D 抽取）：at 给定时把克隆整体平移到该 flow 落点（右键菜单用），
  // 否则沿用 +32/+32（键盘 ⌘V 用）。克隆 fresh id + remap 内部边。
  const doPaste = useCallback(
    (at?: { x: number; y: number }) => {
      const clip = clipboard.current
      if (!clip || clip.nodes.length === 0) return
      let offset = { x: 32, y: 32 }
      if (at) {
        const minX = Math.min(...clip.nodes.map((n) => n.position.x))
        const minY = Math.min(...clip.nodes.map((n) => n.position.y))
        offset = { x: at.x - minX, y: at.y - minY }
      }
      takeSnapshot()
      const { nodes: cloned, edges: clonedEdges } = cloneSelection(
        clip.nodes,
        clip.edges,
        new Set(clip.nodes.map((n) => n.id)),
        offset,
        prompts,
        getNodes(),
      )
      setRfNodes((nds) => [
        ...(nds as RFNode[]).map((n) => ({ ...n, selected: false })),
        ...cloned,
      ])
      setRfEdges((eds) => [...eds, ...clonedEdges])
    },
    [prompts, getNodes, setRfNodes, setRfEdges, takeSnapshot],
  )

  // 全选（Phase D 抽取）：键盘 ⌘A / 右键菜单 共用。
  const selectAll = useCallback(() => {
    setRfNodes((nds) => (nds as RFNode[]).map((n) => ({ ...n, selected: true })))
  }, [setRfNodes])
```

- [ ] **Step 2: Route the keydown `v` branch through `doPaste`; add `a` (select-all)**

In the keydown effect, replace the `key === "v"` branch body (lines 497-515) with:

```tsx
      } else if (key === "v") {
        if (!clipboard.current) return
        e.preventDefault()
        doPaste()
      } else if (key === "a") {
        e.preventDefault()
        selectAll()
```

Then add `doPaste` and `selectAll` to the effect's dependency array (line 543).

- [ ] **Step 3: Verify build + no regression**

Run: `cd web && pnpm test`
Expected: PASS. ⌘C then ⌘V still pastes with +32/+32 offset; ⌘A selects all nodes.

- [ ] **Step 4: Commit**

```bash
git add web/src/features/workflow-canvas/WorkflowCanvas.tsx
git commit -m "refactor(web): extract doPaste/selectAll; add Cmd+A select-all"
```

---

### Task 8: `CanvasContextMenu` component

**Files:**
- Create: `web/src/features/workflow-canvas/CanvasContextMenu.tsx`
- Test: `web/src/features/workflow-canvas/CanvasContextMenu.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { CanvasContextMenu } from "./CanvasContextMenu"

describe("CanvasContextMenu", () => {
  it("renders nothing when closed", () => {
    const { container } = render(
      <CanvasContextMenu open={false} screenX={0} screenY={0} items={[]} onClose={() => {}} />,
    )
    expect(container.querySelector('[data-slot="canvas-context-menu"]')).toBeNull()
  })

  it("renders one button per item and fires onClick + onClose", () => {
    const onClick = vi.fn()
    const onClose = vi.fn()
    render(
      <CanvasContextMenu
        open
        screenX={10}
        screenY={20}
        items={[{ label: "删除", onClick, danger: true }]}
        onClose={onClose}
      />,
    )
    fireEvent.click(screen.getByRole("menuitem", { name: "删除" }))
    expect(onClick).toHaveBeenCalledOnce()
    expect(onClose).toHaveBeenCalledOnce()
  })

  it("disabled item does not fire onClick", () => {
    const onClick = vi.fn()
    render(
      <CanvasContextMenu
        open
        screenX={0}
        screenY={0}
        items={[{ label: "粘贴", onClick, disabled: true }]}
        onClose={() => {}}
      />,
    )
    fireEvent.click(screen.getByRole("menuitem", { name: "粘贴" }))
    expect(onClick).not.toHaveBeenCalled()
  })

  it("clicking the overlay calls onClose", () => {
    const onClose = vi.fn()
    render(
      <CanvasContextMenu open screenX={0} screenY={0} items={[]} onClose={onClose} />,
    )
    fireEvent.click(screen.getByTestId("context-menu-overlay"))
    expect(onClose).toHaveBeenCalledOnce()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/CanvasContextMenu.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the component**

Create `CanvasContextMenu.tsx` (mirrors `NodeTypePicker`'s overlay + fixed pattern):

```tsx
// 通用右键上下文菜单（Phase D）：透明遮罩点外关闭 + fixed 定位浮层。
// 菜单项由画布层按右键目标（pane/node/edge）构建并下发；选项点击后自动关菜单。
export interface ContextMenuItem {
  label: string
  onClick: () => void
  danger?: boolean
  disabled?: boolean
}

export interface CanvasContextMenuProps {
  open: boolean
  screenX: number
  screenY: number
  items: ContextMenuItem[]
  onClose: () => void
}

export function CanvasContextMenu({
  open,
  screenX,
  screenY,
  items,
  onClose,
}: CanvasContextMenuProps) {
  if (!open) return null
  return (
    <>
      <div
        data-testid="context-menu-overlay"
        className="fixed inset-0 z-40"
        onClick={onClose}
      />
      <div
        data-slot="canvas-context-menu"
        role="menu"
        className="fixed z-50 min-w-[140px] rounded-md border border-line bg-bg-raised p-1 shadow-lg"
        style={{ left: screenX, top: screenY }}
      >
        {items.map((item) => (
          <button
            key={item.label}
            type="button"
            role="menuitem"
            disabled={item.disabled}
            onClick={() => {
              if (item.disabled) return
              item.onClick()
              onClose()
            }}
            className={
              "flex w-full items-center rounded px-2.5 py-1.5 text-left text-[12px] hover:bg-bg-surface disabled:cursor-not-allowed disabled:opacity-40 " +
              (item.danger ? "text-danger" : "text-text-1")
            }
          >
            {item.label}
          </button>
        ))}
      </div>
    </>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && pnpm exec vitest run src/features/workflow-canvas/CanvasContextMenu.test.tsx`
Expected: PASS (4 cases).

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow-canvas/CanvasContextMenu.tsx web/src/features/workflow-canvas/CanvasContextMenu.test.tsx
git commit -m "feat(web): add CanvasContextMenu floating menu component"
```

---

### Task 9: Wire context menus into the canvas

**Files:**
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`

- [ ] **Step 1: Import the component and its item type**

Add near the other local imports (after line 52):

```tsx
import { CanvasContextMenu, type ContextMenuItem } from "./CanvasContextMenu"
```

- [ ] **Step 2: Add menu state**

Add after the `picker` state (after line 140):

```tsx
  // 右键上下文菜单态（Phase D）：kind 决定菜单项；targetId 为节点/边 id。
  const [menu, setMenu] = useState<
    | { kind: "pane"; screenX: number; screenY: number }
    | { kind: "node"; screenX: number; screenY: number; targetId: string }
    | { kind: "edge"; screenX: number; screenY: number; targetId: string }
    | null
  >(null)
```

- [ ] **Step 3: Build the menu items**

Add after `selectAll` (Task 7 location):

```tsx
  // 按右键目标构建菜单项（复用既有 handler / doPaste / selectAll / fitView / 泛化 picker）。
  const menuItems = useMemo<ContextMenuItem[]>(() => {
    if (!menu) return []
    if (menu.kind === "pane") {
      const flow = screenToFlowPosition({ x: menu.screenX, y: menu.screenY })
      return [
        {
          label: "添加节点",
          onClick: () =>
            setPicker({ mode: "create", screenX: menu.screenX, screenY: menu.screenY, flow }),
        },
        {
          label: "粘贴",
          disabled: !clipboard.current,
          onClick: () => doPaste(flow),
        },
        { label: "全选", onClick: selectAll },
        { label: "自动整理", onClick: onAutoTidy },
        { label: "适应视图", onClick: () => fitView({ duration: 300 }) },
      ]
    }
    if (menu.kind === "node") {
      return [
        { label: "复制", onClick: () => onDuplicateNode(menu.targetId) },
        {
          label: "从此添加下游",
          onClick: () => onQuickAddFrom(menu.targetId, menu.screenX, menu.screenY),
        },
        { label: "删除", danger: true, onClick: () => onDeleteNode(menu.targetId) },
      ]
    }
    return [
      {
        label: "插入节点",
        onClick: () => onInsertOnEdge(menu.targetId, menu.screenX, menu.screenY),
      },
      { label: "删除", danger: true, onClick: () => onDeleteEdge(menu.targetId) },
    ]
  }, [
    menu,
    screenToFlowPosition,
    doPaste,
    selectAll,
    onAutoTidy,
    fitView,
    onDuplicateNode,
    onQuickAddFrom,
    onDeleteNode,
    onInsertOnEdge,
    onDeleteEdge,
  ])
```

- [ ] **Step 4: Add the ReactFlow context-menu handlers**

Add these props to `<ReactFlow>` (alongside `onSelectionChange`, line 691):

```tsx
              onPaneContextMenu={(e) => {
                e.preventDefault()
                const ev = e as MouseEvent
                setMenu({ kind: "pane", screenX: ev.clientX, screenY: ev.clientY })
              }}
              onNodeContextMenu={(e, node) => {
                e.preventDefault()
                setMenu({ kind: "node", screenX: e.clientX, screenY: e.clientY, targetId: node.id })
              }}
              onEdgeContextMenu={(e, edge) => {
                e.preventDefault()
                setMenu({ kind: "edge", screenX: e.clientX, screenY: e.clientY, targetId: edge.id })
              }}
```

- [ ] **Step 5: Render the menu**

Add right after the `<NodeTypePicker .../>` block (after line 742):

```tsx
          <CanvasContextMenu
            open={!!menu}
            screenX={menu?.screenX ?? 0}
            screenY={menu?.screenY ?? 0}
            items={menuItems}
            onClose={() => setMenu(null)}
          />
```

- [ ] **Step 6: Verify build + no regression**

Run: `cd web && pnpm test`
Expected: PASS.

- [ ] **Step 7: Manual verify (note for reviewer)**

On `:5173`: right-click empty canvas → menu (添加节点 places at cursor; 粘贴 disabled when clipboard empty, else clones at cursor; 全选/自动整理/适应视图 work). Right-click node → 复制/从此添加下游/删除. Right-click edge → 插入节点/删除. Native browser menu is suppressed. Click-away/选项后 menu closes.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/workflow-canvas/WorkflowCanvas.tsx
git commit -m "feat(web): right-click context menus for pane/node/edge"
```

> **PR-D2 boundary:** push branch, open PR, land.

---

## Self-Review notes

- **Spec coverage:** D1.1 drag model → Task 3; D1.2 reconnect → Tasks 1-2; D2.0 picker generalization → Tasks 4-5; D2.1 trailing "+" → Task 6; D2.2 context menus → Tasks 8-9; D2.3 doPaste/selectAll extraction → Task 7. All spec sections mapped.
- **Type consistency:** helper names `reconnectEdge`/`createNode` used identically in tests, helpers, and wiring; `ContextMenuItem` shape matches between component, test, and `menuItems` builder; `picker` create `source?` widened once (Task 5) and consumed by Tasks 6/9.
- **Decisions locked from spec:** reconnect drop-on-empty reverts (no delete) — implemented by simply not handling `onReconnectEnd` (ReactFlow auto-reverts when `onReconnect` doesn't fire).
