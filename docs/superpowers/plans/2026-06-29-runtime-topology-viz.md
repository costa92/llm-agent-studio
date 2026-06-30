# 运行态拓扑流程可视化 + 视图内设置面板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 `RunCanvas`（ReactFlow 运行只读画布）补数据流向动画、节点耗时（仅实时）、自动布局+导航、状态聚焦/高亮，并配一个视图内设置面板，偏好存 localStorage，零后端改动。

**Architecture:** 纯前端增强。耗时走 SSE 客户端累积，用 **seq 水位线**区分回放/实时（回放批次最大 seq 作 baseline，仅 `seq>水位线` 的帧算实时并推进水位线，天然幂等于重连全量回放）。自动布局复用 `seedPositions`/`layerize` 派生坐标。设置 `layout` 按项目持久化、其余偏好全局。`ProjectState` 仍是状态唯一权威源，计时是 ephemeral 叠加不写回。

**Tech Stack:** React 19 + TypeScript + @xyflow/react v12 + TanStack Query/Router + Tailwind v4 + radix-ui + zod + vitest。

**对应设计文档：** `docs/superpowers/specs/2026-06-29-runtime-topology-viz-design.md`

**工作分支：** `feat/runtime-topology-viz`（已存在）。所有提交落此分支，最后开 PR，不直推 main。

**全局测试命令：** 在 `web/` 下 `npm run test -- <文件相对路径>`（vitest）。lint：`npm run lint`。

---

## 文件结构

新增：
- `web/src/components/ui/popover.tsx` — radix Popover 包装层（仓内此前没有）
- `web/src/lib/autoLayout.ts` — `autoLayout(nodes, direction)` 派生坐标（泛化 seedPositions）
- `web/src/features/workflow/useNodeTiming.ts` — SSE seq-水位线计时累积 + `formatDuration`
- `web/src/features/workflow-canvas/useTopologySettings.ts` — localStorage 偏好（layout 每项目、余全局）
- `web/src/features/workflow-canvas/TopologySettingsPanel.tsx` — 齿轮 → Popover 设置面板

改：
- `web/src/lib/timeline.ts` 不动；`web/src/features/workflow-canvas/canvasModel.ts` — `seedPositions` 改调 `autoLayout`，`StudioNodeData` 加 `timing`/`highlightFailed`
- `web/src/features/workflow/useProductionTimeline.ts` — 加 `onReplay`/`onFrame` 副作用出口
- `web/src/features/workflow-canvas/StudioEdge.tsx` + `canvasTheme.css` — `data.active` 流动动画
- `web/src/features/workflow-canvas/WorkflowNode.tsx` — 耗时 chip + 失败红环
- `web/src/features/workflow-canvas/RunCanvas.tsx` — 串接全部

依赖顺序：T1/T2/T3 互独立 → T4 → T5（依赖 T4）→ T6/T7（依赖 T2 的类型）→ T8（依赖 T1/T3）→ T9（串接全部）。

---

## Task 1: Popover UI 组件

**Files:**
- Create: `web/src/components/ui/popover.tsx`
- Test: `web/src/components/ui/popover.test.tsx`

- [ ] **Step 1: 写失败测试**

```tsx
// web/src/components/ui/popover.test.tsx
import { describe, expect, it } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { Popover, PopoverTrigger, PopoverContent } from "./popover"

describe("Popover", () => {
  it("点触发器后渲染内容", () => {
    render(
      <Popover>
        <PopoverTrigger>打开</PopoverTrigger>
        <PopoverContent>面板内容</PopoverContent>
      </Popover>,
    )
    expect(screen.queryByText("面板内容")).toBeNull()
    fireEvent.click(screen.getByText("打开"))
    expect(screen.getByText("面板内容")).toBeTruthy()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/components/ui/popover.test.tsx`
Expected: FAIL（`./popover` 不存在 / 找不到模块）

- [ ] **Step 3: 写实现**

对齐 `sheet.tsx` 的 `radix-ui` 统一入口 + `data-slot` + token 风格。

```tsx
// web/src/components/ui/popover.tsx
"use client"

import * as React from "react"
import { Popover as PopoverPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"

function Popover({ ...props }: React.ComponentProps<typeof PopoverPrimitive.Root>) {
  return <PopoverPrimitive.Root data-slot="popover" {...props} />
}

function PopoverTrigger({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Trigger>) {
  return <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />
}

function PopoverContent({
  className,
  align = "end",
  sideOffset = 6,
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        data-slot="popover-content"
        align={align}
        sideOffset={sideOffset}
        className={cn(
          "z-50 w-72 rounded-lg border border-line bg-bg-surface p-3 text-sm text-text-1 shadow-lg outline-none data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
          className,
        )}
        {...props}
      />
    </PopoverPrimitive.Portal>
  )
}

export { Popover, PopoverTrigger, PopoverContent }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/components/ui/popover.test.tsx`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/components/ui/popover.tsx web/src/components/ui/popover.test.tsx
git commit -m "feat(web): add radix Popover ui wrapper

视图内设置面板需要；仓内此前只有 sheet，无 popover。对齐 sheet.tsx 风格。"
```

---

## Task 2: autoLayout 派生坐标 + 重构 seedPositions

**Files:**
- Create: `web/src/lib/autoLayout.ts`
- Test: `web/src/lib/autoLayout.test.ts`
- Modify: `web/src/features/workflow-canvas/canvasModel.ts`（`seedPositions` 改调 autoLayout）

> `StudioNodeData` 的 `timing`/`highlightFailed` 字段扩展放到 **T7**（紧挨消费点、彼时 `NodeTiming` 已由 T5 创建），避免本任务前向引用未创建的类型，保证逐任务独立编译。

- [ ] **Step 1: 写失败测试**

```ts
// web/src/lib/autoLayout.test.ts
import { describe, expect, it } from "vitest"
import { autoLayout } from "./autoLayout"
import type { WorkflowNode } from "@/lib/types"

const chain: WorkflowNode[] = [
  { id: "a", type: "script", promptId: "", dependsOn: [] },
  { id: "b", type: "storyboard", promptId: "", dependsOn: ["a"] },
  { id: "c", type: "asset", promptId: "", dependsOn: ["b"] },
]

describe("autoLayout", () => {
  it("TB：层 index→y，层内 index→x（与旧 seedPositions 同口径）", () => {
    const pos = autoLayout(chain, "TB")
    expect(pos.get("a")).toEqual({ x: 0, y: 0 })
    expect(pos.get("b")).toEqual({ x: 0, y: 140 })
    expect(pos.get("c")).toEqual({ x: 0, y: 280 })
  })

  it("LR：交换主/交叉轴", () => {
    const pos = autoLayout(chain, "LR")
    expect(pos.get("a")).toEqual({ x: 0, y: 0 })
    expect(pos.get("b")).toEqual({ x: 240, y: 0 })
    expect(pos.get("c")).toEqual({ x: 480, y: 0 })
  })

  it("同层多节点：层内 index 沿交叉轴展开", () => {
    const fanout: WorkflowNode[] = [
      { id: "root", type: "script", promptId: "", dependsOn: [] },
      { id: "x", type: "asset", promptId: "", dependsOn: ["root"] },
      { id: "y", type: "asset", promptId: "", dependsOn: ["root"] },
    ]
    const tb = autoLayout(fanout, "TB")
    expect(tb.get("root")).toEqual({ x: 0, y: 0 })
    expect(tb.get("x")).toEqual({ x: 0, y: 140 })
    expect(tb.get("y")).toEqual({ x: 240, y: 140 })
  })

  it("空图返回空 map", () => {
    expect(autoLayout([], "TB").size).toBe(0)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/lib/autoLayout.test.ts`
Expected: FAIL（`./autoLayout` 不存在）

- [ ] **Step 3: 写实现**

```ts
// web/src/lib/autoLayout.ts
import { layerize } from "@/lib/graphLayout"
import type { GraphNode, GraphEdge } from "@/lib/projectState"
import type { WorkflowNode } from "@/lib/types"

export type LayoutDirection = "TB" | "LR"

const PRIMARY = 140 // 层间距（主轴）
const CROSS = 240 // 层内间距（交叉轴）

// 自动分层布局：复用 layerize（边由 dependsOn 建 GraphEdge{from:id,to:dep}）。
// TB：layer i 第 j 个 → {x: j*CROSS, y: i*PRIMARY}（自顶向下，等价旧 seedPositions）。
// LR：交换主/交叉轴 → {x: i*CROSS_? ...}，即 layer 沿 x 推进、层内沿 y 展开。
export function autoLayout(
  nodes: WorkflowNode[],
  direction: LayoutDirection,
): Map<string, { x: number; y: number }> {
  const graphNodes: GraphNode[] = nodes.map((n) => ({
    id: n.id,
    label: n.id,
    type: n.type,
    status: "pending",
  }))
  const edges: GraphEdge[] = []
  for (const n of nodes) {
    for (const dep of n.dependsOn) {
      edges.push({ from: n.id, to: dep })
    }
  }
  const layers = layerize(graphNodes, edges)
  const out = new Map<string, { x: number; y: number }>()
  layers.forEach((layer, layerIndex) => {
    layer.forEach((gn, indexWithinLayer) => {
      if (direction === "TB") {
        out.set(gn.id, { x: indexWithinLayer * CROSS, y: layerIndex * PRIMARY })
      } else {
        out.set(gn.id, { x: layerIndex * CROSS, y: indexWithinLayer * PRIMARY })
      }
    })
  })
  return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/lib/autoLayout.test.ts`
Expected: PASS

- [ ] **Step 5: 重构 seedPositions 复用 autoLayout（避免两份分层逻辑）**

把 `canvasModel.ts` 的 `seedPositions` 函数体（`web/src/features/workflow-canvas/canvasModel.ts:26-49`）替换为委托：

```ts
// web/src/features/workflow-canvas/canvasModel.ts （替换 seedPositions 实现，保持签名/导出不变）
import { autoLayout } from "@/lib/autoLayout"
// ...（删去本文件内对 layerize / GraphNode / GraphEdge 的 import，若已无其它使用）

// 种子布局：节点无显式 position 时的兜底坐标（TB，等价历史实现，委托 autoLayout）。
export function seedPositions(
  nodes: WorkflowNode[],
): Map<string, { x: number; y: number }> {
  return autoLayout(nodes, "TB")
}
```

> `StudioNodeData` 不在本任务改动 —— `timing`/`highlightFailed` 字段放 T7（彼时 `NodeTiming` 已存在）。本任务只动 `seedPositions`，独立编译通过。

- [ ] **Step 6: 跑既有 canvasModel 测试确认零回归**

Run: `cd web && npm run test -- src/features/workflow-canvas/canvasModel.test.ts`
Expected: PASS（seedPositions 口径不变，既有快照/断言全绿）

- [ ] **Step 7: 提交**

```bash
git add web/src/lib/autoLayout.ts web/src/lib/autoLayout.test.ts web/src/features/workflow-canvas/canvasModel.ts
git commit -m "feat(web): autoLayout 派生坐标，seedPositions 委托复用

泛化 seedPositions 为 autoLayout(nodes, TB|LR)；TB 等价旧实现（零回归），
LR 交换主/交叉轴。避免运行态自动布局再造第三份分层逻辑。"
```

---

## Task 3: useTopologySettings（localStorage 偏好）

**Files:**
- Create: `web/src/features/workflow-canvas/useTopologySettings.ts`
- Test: `web/src/features/workflow-canvas/useTopologySettings.test.ts`

- [ ] **Step 1: 写失败测试**

```ts
// web/src/features/workflow-canvas/useTopologySettings.test.ts
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { act, renderHook } from "@testing-library/react"
import { useTopologySettings, DEFAULT_TOPOLOGY_SETTINGS } from "./useTopologySettings"

beforeEach(() => localStorage.clear())
afterEach(() => localStorage.clear())

describe("useTopologySettings", () => {
  it("无存储时返回默认值", () => {
    const { result } = renderHook(() => useTopologySettings("proj-A"))
    expect(result.current.settings).toEqual(DEFAULT_TOPOLOGY_SETTINGS)
  })

  it("update 持久化并立即生效", () => {
    const { result } = renderHook(() => useTopologySettings("proj-A"))
    act(() => result.current.update({ showTiming: true }))
    expect(result.current.settings.showTiming).toBe(true)
    // 重新挂载读回
    const { result: r2 } = renderHook(() => useTopologySettings("proj-A"))
    expect(r2.current.settings.showTiming).toBe(true)
  })

  it("layout 按项目隔离；全局偏好跨项目共享", () => {
    const { result: a } = renderHook(() => useTopologySettings("proj-A"))
    act(() => a.current.update({ layout: "LR", showTiming: true }))
    const { result: b } = renderHook(() => useTopologySettings("proj-B"))
    // layout 每项目：B 不继承 A 的 LR
    expect(b.current.settings.layout).toBe("saved")
    // showTiming 全局：B 继承
    expect(b.current.settings.showTiming).toBe(true)
  })

  it("坏 JSON 回落默认值，不抛", () => {
    localStorage.setItem("studio.topology.prefs", "{not json")
    localStorage.setItem("studio.topology.layout.proj-A", "garbage")
    const { result } = renderHook(() => useTopologySettings("proj-A"))
    expect(result.current.settings).toEqual(DEFAULT_TOPOLOGY_SETTINGS)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/features/workflow-canvas/useTopologySettings.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 写实现**

```ts
// web/src/features/workflow-canvas/useTopologySettings.ts
import { useCallback, useState } from "react"
import { z } from "zod"
import type { LayoutDirection } from "@/lib/autoLayout"

export type LayoutMode = "saved" | LayoutDirection // "saved" | "TB" | "LR"
export type FocusMode = "none" | "failed" | "running"

export interface TopologySettings {
  layout: LayoutMode // 每项目持久化
  fitOnUpdate: boolean
  showTiming: boolean
  flowAnimation: boolean
  focus: FocusMode
  hideCompleted: boolean
}

export const DEFAULT_TOPOLOGY_SETTINGS: TopologySettings = {
  layout: "saved",
  fitOnUpdate: true,
  showTiming: false,
  flowAnimation: true, // 历史 run 无 running 节点 → 无 active 边 → 不动画（零视觉影响）
  focus: "none",
  hideCompleted: false,
}

const PREFS_KEY = "studio.topology.prefs"
const layoutKey = (projectId: string) => `studio.topology.layout.${projectId}`

// 全局偏好（除 layout 外）。
const prefsSchema = z.object({
  fitOnUpdate: z.boolean(),
  showTiming: z.boolean(),
  flowAnimation: z.boolean(),
  focus: z.enum(["none", "failed", "running"]),
  hideCompleted: z.boolean(),
})
const layoutSchema = z.enum(["saved", "TB", "LR"])

function readSettings(projectId: string): TopologySettings {
  let prefs = {
    fitOnUpdate: DEFAULT_TOPOLOGY_SETTINGS.fitOnUpdate,
    showTiming: DEFAULT_TOPOLOGY_SETTINGS.showTiming,
    flowAnimation: DEFAULT_TOPOLOGY_SETTINGS.flowAnimation,
    focus: DEFAULT_TOPOLOGY_SETTINGS.focus,
    hideCompleted: DEFAULT_TOPOLOGY_SETTINGS.hideCompleted,
  }
  try {
    const raw = localStorage.getItem(PREFS_KEY)
    if (raw) {
      const parsed = prefsSchema.safeParse(JSON.parse(raw))
      if (parsed.success) prefs = parsed.data
    }
  } catch {
    // 坏数据回落默认
  }
  let layout: LayoutMode = DEFAULT_TOPOLOGY_SETTINGS.layout
  try {
    const rawL = localStorage.getItem(layoutKey(projectId))
    if (rawL) {
      const parsed = layoutSchema.safeParse(rawL)
      if (parsed.success) layout = parsed.data
    }
  } catch {
    // 回落 saved
  }
  return { layout, ...prefs }
}

// 运行态拓扑视图偏好。layout 按 projectId 隔离持久化，其余全局共享。
export function useTopologySettings(projectId: string): {
  settings: TopologySettings
  update: (patch: Partial<TopologySettings>) => void
} {
  const [settings, setSettings] = useState<TopologySettings>(() =>
    readSettings(projectId),
  )

  const update = useCallback(
    (patch: Partial<TopologySettings>) => {
      setSettings((prev) => {
        const next = { ...prev, ...patch }
        // 写盘：layout 按项目键；其余全局键。
        try {
          localStorage.setItem(layoutKey(projectId), next.layout)
          const { layout: _omit, ...prefs } = next
          void _omit
          localStorage.setItem(PREFS_KEY, JSON.stringify(prefs))
        } catch {
          // 写盘失败不阻断 UI（隐私模式等）
        }
        return next
      })
    },
    [projectId],
  )

  return { settings, update }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/features/workflow-canvas/useTopologySettings.test.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/features/workflow-canvas/useTopologySettings.ts web/src/features/workflow-canvas/useTopologySettings.test.ts
git commit -m "feat(web): useTopologySettings localStorage 偏好

layout 按项目隔离（避免工作流 A 手排被 B 切 TB 覆盖），其余偏好全局。
zod safeParse 坏数据回落默认。"
```

---

## Task 4: useProductionTimeline 加 onReplay/onFrame 副作用出口

**Files:**
- Modify: `web/src/features/workflow/useProductionTimeline.ts`
- Test: `web/src/features/workflow/useProductionTimeline.test.ts`（若不存在则新建本测试文件）

- [ ] **Step 1: 写失败测试**

```ts
// web/src/features/workflow/useProductionTimeline.test.ts（新增/追加）
import { describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { useProductionTimeline } from "./useProductionTimeline"
import type { StudioEvent, SseFrame } from "@/lib/types"

describe("useProductionTimeline onReplay/onFrame 出口", () => {
  it("回放批次经 onReplay 透出；实时帧经 onFrame 透出", async () => {
    const replayed: SseFrame[][] = []
    const live: SseFrame[] = []
    const events: StudioEvent[] = [
      { seq: 1, kind: "todo_started", todoId: "t1", payload: { type: "script" } },
    ]
    // fake SSE client：开流后推一帧实时事件再 close。
    const fakeSse = vi.fn(async (_url, _init, handlers) => {
      handlers.onopen?.(new Response())
      handlers.onmessage?.({
        id: "2",
        event: "todo_finished",
        data: JSON.stringify({ seq: 2, kind: "todo_finished", todoId: "t1", payload: {} }),
      })
    })

    renderHook(() =>
      useProductionTimeline({
        projectId: "p1",
        accessToken: "tok",
        status: "running",
        fetchAllEvents: async () => events,
        sseClient: fakeSse as never,
        onReplay: (f) => replayed.push(f),
        onFrame: (f) => live.push(f),
      }),
    )

    await waitFor(() => expect(replayed.length).toBe(1))
    expect(replayed[0][0].seq).toBe(1)
    await waitFor(() => expect(live.some((f) => f.seq === 2)).toBe(true))
  })
})
```

> 说明：`sseClient` 的 fake 形状对齐 `@microsoft/fetch-event-source` 的 `fetchEventSource(url, init)`，其 init 内含 `onopen/onmessage/onerror/onclose`。若本仓 `streamRunEvents`（`@/lib/sse`）对 client 的调用约定不同，按 `web/src/lib/sse.ts` 的真实签名调整 fake；本步先读 `sse.ts` 确认 `SseClient` 类型再定 fake 形状。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/features/workflow/useProductionTimeline.test.ts`
Expected: FAIL（`onReplay`/`onFrame` 参数不存在，回调未被调用）

- [ ] **Step 3: 写实现**

在 `UseProductionTimelineArgs`（`useProductionTimeline.ts:44-55`）加两个可选回调：

```ts
  // 回放批次到达（一次性，全部历史帧）。计时层据此取 seq 水位线 baseline。
  onReplay?: (frames: SseFrame[]) => void
  // 每个 SSE 帧到达（含服务端 after=0 回放与实时，计时层按 seq 水位线过滤）。
  onFrame?: (frame: SseFrame) => void
```

仿 `onStateRef` 加 ref（`useProductionTimeline.ts:97-102` 附近）：

```ts
  const onStateRef = useRef(onState)
  const onReplayRef = useRef(onReplay)
  const onFrameRef = useRef(onFrame)
  useEffect(() => {
    onStateRef.current = onState
    onReplayRef.current = onReplay
    onFrameRef.current = onFrame
  })
```

回放点（`useProductionTimeline.ts:130-132`）改为：

```ts
        const events = await fetchAllEvents(projectId, planId)
        if (cancelled) return
        const frames = events.map(toFrame)
        dispatch({ type: "replayed", frames })
        onReplayRef.current?.(frames)
```

SSE 帧点（`useProductionTimeline.ts:151-157`）两处补 onFrame：

```ts
            onEvent: (frame) => {
              if (!cancelled) {
                dispatch({ type: "frame", frame })
                onFrameRef.current?.(frame)
              }
            },
            onMessage: (frame) => {
              if (!cancelled) {
                dispatch({ type: "frame", frame })
                onFrameRef.current?.(frame)
              }
            },
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/features/workflow/useProductionTimeline.test.ts`
Expected: PASS

- [ ] **Step 5: 跑既有制片轨道相关测试确认零回归**

Run: `cd web && npm run test -- src/features/workflow`
Expected: PASS（新增回调全可选，不影响既有调用方）

- [ ] **Step 6: 提交**

```bash
git add web/src/features/workflow/useProductionTimeline.ts web/src/features/workflow/useProductionTimeline.test.ts
git commit -m "feat(web): useProductionTimeline 加 onReplay/onFrame 出口

供计时层接出逐帧，不另开 SSE 连接。回调全可选，既有调用方零影响。"
```

---

## Task 5: useNodeTiming（seq 水位线计时）+ formatDuration

**Files:**
- Create: `web/src/features/workflow/useNodeTiming.ts`
- Test: `web/src/features/workflow/useNodeTiming.test.ts`

- [ ] **Step 1: 写失败测试**

```ts
// web/src/features/workflow/useNodeTiming.test.ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { act, renderHook } from "@testing-library/react"
import { useNodeTiming, formatDuration } from "./useNodeTiming"
import type { SseFrame } from "@/lib/types"

const f = (seq: number, kind: string, todoId: string): SseFrame => ({
  seq, kind, todoId, payload: {},
})

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe("formatDuration", () => {
  it("亚分钟显秒一位小数", () => expect(formatDuration(1234)).toBe("1.2s"))
  it("满分钟显 m s", () => expect(formatDuration(63000)).toBe("1m03s"))
})

describe("useNodeTiming seq 水位线", () => {
  it("回放帧不计时（baseline 之内的 started/finished 不产生耗时）", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => {
      result.current.onReplay([f(1, "todo_started", "t1"), f(2, "todo_finished", "t1")])
    })
    expect(result.current.timingByTodoId.get("t1")).toBeUndefined()
  })

  it("实时 started→finished 算耗时", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => result.current.onReplay([])) // baseline = 0
    act(() => {
      result.current.onFrame(f(1, "todo_started", "t1"))
    })
    act(() => vi.advanceTimersByTime(3000))
    act(() => {
      result.current.onFrame(f(2, "todo_finished", "t1"))
    })
    const t = result.current.timingByTodoId.get("t1")
    expect(t?.finishedAt).toBeDefined()
    expect(t!.elapsedMs).toBeGreaterThanOrEqual(3000)
  })

  it("running 节点随时间跳秒（无 finished 时 elapsed 增长）", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => result.current.onReplay([]))
    act(() => result.current.onFrame(f(1, "todo_started", "t1")))
    const e0 = result.current.timingByTodoId.get("t1")!.elapsedMs
    act(() => vi.advanceTimersByTime(2000))
    const e1 = result.current.timingByTodoId.get("t1")!.elapsedMs
    expect(e1).toBeGreaterThan(e0)
  })

  it("重连全量回放幂等：实时 startedAt 不被重放的同 todoId started 覆盖/回跳", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => result.current.onReplay([]))
    act(() => result.current.onFrame(f(5, "todo_started", "t1")))
    act(() => vi.advanceTimersByTime(4000))
    const before = result.current.timingByTodoId.get("t1")!.elapsedMs
    // 重连：服务端从 after=0 全量回放，旧帧 seq<=水位线 → 被忽略
    act(() => {
      result.current.onFrame(f(1, "todo_started", "t1"))
      result.current.onFrame(f(5, "todo_started", "t1"))
    })
    const after = result.current.timingByTodoId.get("t1")!.elapsedMs
    expect(after).toBeGreaterThanOrEqual(before) // 不回跳归零
  })

  it("planId 变更重置累积", () => {
    const { result, rerender } = renderHook(({ p }) => useNodeTiming(p), {
      initialProps: { p: "plan1" },
    })
    act(() => result.current.onReplay([]))
    act(() => result.current.onFrame(f(1, "todo_started", "t1")))
    expect(result.current.timingByTodoId.get("t1")).toBeDefined()
    rerender({ p: "plan2" })
    expect(result.current.timingByTodoId.get("t1")).toBeUndefined()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/features/workflow/useNodeTiming.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 写实现**

```ts
// web/src/features/workflow/useNodeTiming.ts
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react"
import type { SseFrame } from "@/lib/types"

export interface NodeTiming {
  startedAt: number
  finishedAt?: number
  elapsedMs: number
}

// 耗时格式化：<60s 显秒一位小数（"3.4s"），>=60s 显 "1m03s"。
export function formatDuration(ms: number): string {
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  const total = Math.floor(ms / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}m${String(s).padStart(2, "0")}s`
}

interface State {
  watermark: number
  started: Record<string, number>
  finished: Record<string, number>
}
const INIT: State = { watermark: 0, started: {}, finished: {} }

type Action =
  | { type: "reset" }
  | { type: "replay"; maxSeq: number }
  | { type: "frame"; seq: number; kind: string; todoId: string; at: number }

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case "reset":
      return INIT
    case "replay":
      // 回放批次只抬高 baseline 水位线，绝不产生耗时。
      return { ...state, watermark: Math.max(state.watermark, action.maxSeq) }
    case "frame": {
      // seq 水位线：<=水位线 = 回放/重连重放/重复 → 忽略（幂等）。
      if (action.seq <= state.watermark) return state
      const next: State = {
        watermark: action.seq,
        started: state.started,
        finished: state.finished,
      }
      if (action.kind === "todo_started" && !(action.todoId in state.started)) {
        next.started = { ...state.started, [action.todoId]: action.at }
      } else if (action.kind === "todo_finished") {
        next.finished = { ...state.finished, [action.todoId]: action.at }
      }
      return next
    }
  }
}

// 运行态节点耗时：仅实时（seq>水位线）帧产生耗时，覆盖「回放假耗时」与「重连回跳」。
// onReplay/onFrame 交给 useProductionTimeline 的同名出口。planId 变更重置。
export function useNodeTiming(planId: string): {
  timingByTodoId: Map<string, NodeTiming>
  onReplay: (frames: SseFrame[]) => void
  onFrame: (frame: SseFrame) => void
} {
  const [state, dispatch] = useReducer(reducer, INIT)
  // running 跳秒：仅在有未完成节点时启动 interval，bump now。
  const [now, setNow] = useState(() => Date.now())

  // planId 变更：重置累积。
  const planRef = useRef(planId)
  useEffect(() => {
    if (planRef.current !== planId) {
      planRef.current = planId
      dispatch({ type: "reset" })
    }
  }, [planId])

  const onReplay = useCallback((frames: SseFrame[]) => {
    const maxSeq = frames.reduce((m, f) => Math.max(m, f.seq), 0)
    dispatch({ type: "replay", maxSeq })
  }, [])

  const onFrame = useCallback((frame: SseFrame) => {
    dispatch({
      type: "frame",
      seq: frame.seq,
      kind: frame.kind,
      todoId: frame.todoId,
      at: Date.now(),
    })
  }, [])

  const hasRunning = useMemo(
    () => Object.keys(state.started).some((id) => !(id in state.finished)),
    [state.started, state.finished],
  )

  useEffect(() => {
    if (!hasRunning) return
    setNow(Date.now())
    const h = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(h)
  }, [hasRunning])

  const timingByTodoId = useMemo(() => {
    const out = new Map<string, NodeTiming>()
    for (const [todoId, startedAt] of Object.entries(state.started)) {
      const finishedAt = state.finished[todoId]
      const elapsedMs = (finishedAt ?? now) - startedAt
      out.set(todoId, { startedAt, finishedAt, elapsedMs })
    }
    return out
  }, [state.started, state.finished, now])

  return { timingByTodoId, onReplay, onFrame }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/features/workflow/useNodeTiming.test.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/features/workflow/useNodeTiming.ts web/src/features/workflow/useNodeTiming.test.ts
git commit -m "feat(web): useNodeTiming seq 水位线计时

仅 seq>水位线 的实时帧产生耗时；回放批次只抬 baseline。天然幂等于重连
全量回放（旧帧 seq<=水位线被忽略，不回跳）。running 跳秒 interval 仅在有
未完成节点时存活，planId 变更重置。"
```

---

## Task 6: StudioEdge 数据流向动画

**Files:**
- Modify: `web/src/features/workflow-canvas/StudioEdge.tsx`
- Modify: `web/src/features/workflow-canvas/canvasTheme.css`（加 keyframes）
- Test: `web/src/features/workflow-canvas/StudioEdge.test.tsx`

- [ ] **Step 1: 写失败测试**

```tsx
// web/src/features/workflow-canvas/StudioEdge.test.tsx
import { describe, expect, it } from "vitest"
import { render } from "@testing-library/react"
import { ReactFlow, ReactFlowProvider } from "@xyflow/react"
import { StudioEdge } from "./StudioEdge"
import { CanvasActionsContext } from "./CanvasActionsContext"

// 渲染一条 active 边，断言 BaseEdge 路径带 data-active 标记（驱动 CSS 动画）。
function renderEdge(active: boolean) {
  const nodes = [
    { id: "a", position: { x: 0, y: 0 }, data: {} },
    { id: "b", position: { x: 0, y: 120 }, data: {} },
  ]
  const edges = [
    { id: "a->b", source: "a", target: "b", type: "studio", data: { active } },
  ]
  return render(
    <CanvasActionsContext.Provider
      value={{ onDeleteEdge: () => {}, onInsertOnEdge: () => {}, onDuplicateNode: () => {}, onDeleteNode: () => {}, onQuickAddFrom: () => {} } as never}
    >
      <ReactFlowProvider>
        <ReactFlow nodes={nodes} edges={edges} edgeTypes={{ studio: StudioEdge }} />
      </ReactFlowProvider>
    </CanvasActionsContext.Provider>,
  )
}

describe("StudioEdge data.active", () => {
  it("active 时边路径带 data-active=true", () => {
    const { container } = renderEdge(true)
    expect(container.querySelector('[data-slot="studio-edge-path"][data-active="true"]')).toBeTruthy()
  })
  it("非 active 时 data-active=false", () => {
    const { container } = renderEdge(false)
    expect(container.querySelector('[data-slot="studio-edge-path"][data-active="false"]')).toBeTruthy()
  })
})
```

> 若 `CanvasActionsContext` 的导出名/形状与上述不符，先读 `web/src/features/workflow-canvas/CanvasActionsContext.tsx` 对齐 provider value 与导入名。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/features/workflow-canvas/StudioEdge.test.tsx`
Expected: FAIL（无 `data-slot="studio-edge-path"` / 无 data-active）

- [ ] **Step 3: 写实现**

`StudioEdge.tsx`：从 props 取 `data`，给 `BaseEdge` 套一个带 `data-active` 的标记类（BaseEdge 透传 `className`/`data-*`？BaseEdge 渲染 `<path>`，支持 `className`）。改 `BaseEdge` 调用：

```tsx
// StudioEdge.tsx —— 函数签名加 data，BaseEdge 加 className + data-active
export function StudioEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  style,
  data,
}: EdgeProps) {
  // ...（hover/actions/path 不变）
  const active = (data as { active?: boolean } | undefined)?.active === true
  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={style}
        className={active ? "studio-edge-active" : undefined}
        data-slot="studio-edge-path"
        data-active={active ? "true" : "false"}
      />
      {/* EdgeLabelRenderer 控制簇不变 */}
      {/* ... */}
    </>
  )
}
```

`canvasTheme.css` 追加（stroke 用 amber token，motion-safe，流动虚线）：

```css
/* 运行态数据流向：active 边走流动虚线（执行前沿：源 done & 目标 running）。 */
.studio-edge-active {
  stroke: var(--amber);
  stroke-dasharray: 6 4;
}
@media (prefers-reduced-motion: no-preference) {
  .studio-edge-active {
    animation: studio-edge-flow 0.6s linear infinite;
  }
}
@keyframes studio-edge-flow {
  to {
    stroke-dashoffset: -20;
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/features/workflow-canvas/StudioEdge.test.tsx`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/features/workflow-canvas/StudioEdge.tsx web/src/features/workflow-canvas/canvasTheme.css web/src/features/workflow-canvas/StudioEdge.test.tsx
git commit -m "feat(web): StudioEdge data.active 数据流向动画

active 边（源done&目标running）走 amber 流动虚线，motion-safe 守护，
stroke 用 --amber token（禁硬编码）。RunCanvas 逐边注入 data.active。"
```

---

## Task 7: WorkflowNode 耗时 chip + 失败红环

**Files:**
- Modify: `web/src/features/workflow-canvas/WorkflowNode.tsx`
- Test: `web/src/features/workflow-canvas/WorkflowNode.test.tsx`（不存在则新建）

- [ ] **Step 1: 写失败测试**

```tsx
// web/src/features/workflow-canvas/WorkflowNode.test.tsx
import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { ReactFlow, ReactFlowProvider } from "@xyflow/react"
import { WorkflowNode } from "./WorkflowNode"
import { CanvasActionsContext } from "./CanvasActionsContext"
import type { StudioNodeData } from "./canvasModel"

function renderNode(data: StudioNodeData) {
  const nodes = [{ id: "n1", type: "studio", position: { x: 0, y: 0 }, data }]
  return render(
    <CanvasActionsContext.Provider
      value={{ onDeleteEdge: () => {}, onInsertOnEdge: () => {}, onDuplicateNode: () => {}, onDeleteNode: () => {}, onQuickAddFrom: () => {} } as never}
    >
      <ReactFlowProvider>
        <ReactFlow nodes={nodes} edges={[]} nodeTypes={{ studio: WorkflowNode }} />
      </ReactFlowProvider>
    </CanvasActionsContext.Provider>,
  )
}

const baseNode = { id: "n1", type: "asset", promptId: "", dependsOn: [] }

describe("WorkflowNode 耗时 chip / 失败红环", () => {
  it("有 timing 时显示格式化耗时", () => {
    renderNode({
      node: baseNode,
      run: { status: "done", todoId: "t1" },
      timing: { startedAt: 0, finishedAt: 3400, elapsedMs: 3400 },
    })
    expect(screen.getByText("3.4s")).toBeTruthy()
  })
  it("无 timing 时不显示耗时 chip", () => {
    const { container } = renderNode({ node: baseNode, run: { status: "done", todoId: "t1" } })
    expect(container.querySelector('[data-slot="canvas-node-timing"]')).toBeNull()
  })
  it("highlightFailed 且 failed 时加红环标记", () => {
    const { container } = renderNode({
      node: baseNode,
      run: { status: "failed", todoId: "t1" },
      highlightFailed: true,
    })
    expect(container.querySelector('[data-failed-highlight="true"]')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/features/workflow-canvas/WorkflowNode.test.tsx`
Expected: FAIL

- [ ] **Step 3: 写实现**

**先扩展 `StudioNodeData`**（`canvasModel.ts:14-18`，此时 `NodeTiming` 已由 T5 创建）：

```ts
// web/src/features/workflow-canvas/canvasModel.ts —— 文件顶部补 import
import type { NodeTiming } from "@/features/workflow/useNodeTiming"

// StudioNodeData 接口加两字段（其余不变）：
export interface StudioNodeData {
  node: WorkflowNode
  run?: RunNodeStatus
  // 运行态拓扑增强：实时耗时（仅实时观测到的节点有值）+ 失败高亮开关。
  timing?: NodeTiming
  highlightFailed?: boolean
  [key: string]: unknown
}
```

然后改 `WorkflowNode.tsx`：读 `data.timing` / `data.highlightFailed`，渲染右上角绝对定位 chip（`font-mono tabular-nums`，色 `text-2`，运行中 `aria-hidden`），失败时给外层容器加红环标记。

外层容器（`WorkflowNode.tsx:30-35`）改为带条件红环 + data 标记：

```tsx
  const timing = data.timing
  const isFailed = runStatus === "failed"
  const failedHighlight = data.highlightFailed === true && isFailed
  return (
    <div
      data-slot="canvas-node"
      data-status={isRunMode ? (runStatus ?? "pending") : undefined}
      data-failed-highlight={failedHighlight ? "true" : undefined}
      className={cn(
        "group relative flex items-center gap-2.5 rounded-lg border border-line bg-bg-surface px-3 py-2 shadow-sm min-w-[140px]",
        failedHighlight && "ring-2 ring-danger",
      )}
    >
      {/* 运行态耗时 chip：右上角绝对定位，不挤压两行文案。running 跳秒 aria-hidden。 */}
      {timing && (
        <span
          data-slot="canvas-node-timing"
          aria-hidden={timing.finishedAt == null ? true : undefined}
          className="absolute -top-2 -right-2 rounded bg-bg-raised px-1 font-mono text-[10px] tabular-nums text-text-2 shadow-sm"
        >
          {formatDuration(timing.elapsedMs)}
        </span>
      )}
      {/* ...原有内容不变... */}
```

文件顶部补 import：

```tsx
import { formatDuration } from "@/features/workflow/useNodeTiming"
```

> `cn` 已在本文件导入（`WorkflowNode.tsx:8`）。`data.timing`/`data.highlightFailed` 类型由本步扩展的 `StudioNodeData` 提供。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/features/workflow-canvas/WorkflowNode.test.tsx`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/features/workflow-canvas/canvasModel.ts web/src/features/workflow-canvas/WorkflowNode.tsx web/src/features/workflow-canvas/WorkflowNode.test.tsx
git commit -m "feat(web): WorkflowNode 耗时 chip + 失败红环

右上角绝对定位 chip（font-mono tabular-nums 防抖动，色 text-2 不占 amber，
running 态 aria-hidden 防读屏噪音）；highlightFailed 时失败节点 ring-danger。"
```

---

## Task 8: TopologySettingsPanel 设置面板

**Files:**
- Create: `web/src/features/workflow-canvas/TopologySettingsPanel.tsx`
- Test: `web/src/features/workflow-canvas/TopologySettingsPanel.test.tsx`

- [ ] **Step 1: 写失败测试**

```tsx
// web/src/features/workflow-canvas/TopologySettingsPanel.test.tsx
import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { TopologySettingsPanel } from "./TopologySettingsPanel"
import { DEFAULT_TOPOLOGY_SETTINGS } from "./useTopologySettings"

describe("TopologySettingsPanel", () => {
  it("齿轮带 aria-label；点开后切开关回调 update", () => {
    const update = vi.fn()
    render(
      <TopologySettingsPanel settings={DEFAULT_TOPOLOGY_SETTINGS} update={update} />,
    )
    const gear = screen.getByLabelText("视图设置")
    expect(gear).toBeTruthy()
    fireEvent.click(gear)
    fireEvent.click(screen.getByLabelText("显示耗时"))
    expect(update).toHaveBeenCalledWith({ showTiming: true })
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/features/workflow-canvas/TopologySettingsPanel.test.tsx`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 写实现**

用 Task 1 的 Popover + 既有 `checkbox`/`label`/`select`。分三组：布局 / 叠加 / 聚焦。

```tsx
// web/src/features/workflow-canvas/TopologySettingsPanel.tsx
import { Settings2 } from "lucide-react"
import { Popover, PopoverTrigger, PopoverContent } from "@/components/ui/popover"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type {
  TopologySettings,
  LayoutMode,
  FocusMode,
} from "./useTopologySettings"

export interface TopologySettingsPanelProps {
  settings: TopologySettings
  update: (patch: Partial<TopologySettings>) => void
}

export function TopologySettingsPanel({
  settings,
  update,
}: TopologySettingsPanelProps) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label="视图设置"
          title="视图设置"
          className="grid h-8 w-8 place-items-center rounded-md border border-line bg-bg-surface text-text-2 shadow-sm hover:text-text-1"
        >
          <Settings2 size={15} />
        </button>
      </PopoverTrigger>
      <PopoverContent>
        <div className="flex flex-col gap-3">
          {/* 布局 */}
          <section className="flex flex-col gap-1.5">
            <p className="text-[11px] font-semibold tracking-[0.06em] text-text-3">布局</p>
            <Select
              value={settings.layout}
              onValueChange={(v) => update({ layout: v as LayoutMode })}
            >
              <SelectTrigger aria-label="布局方向" className="h-8 text-[12px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="saved">自存坐标</SelectItem>
                <SelectItem value="TB">自动·竖向</SelectItem>
                <SelectItem value="LR">自动·横向</SelectItem>
              </SelectContent>
            </Select>
            <CheckRow
              id="fitOnUpdate"
              label="布局变更时自动适配视图"
              checked={settings.fitOnUpdate}
              onChange={(v) => update({ fitOnUpdate: v })}
            />
          </section>
          {/* 叠加 */}
          <section className="flex flex-col gap-1.5">
            <p className="text-[11px] font-semibold tracking-[0.06em] text-text-3">叠加</p>
            <CheckRow
              id="showTiming"
              label="显示耗时"
              checked={settings.showTiming}
              onChange={(v) => update({ showTiming: v })}
            />
            <CheckRow
              id="flowAnimation"
              label="数据流动画"
              checked={settings.flowAnimation}
              onChange={(v) => update({ flowAnimation: v })}
            />
          </section>
          {/* 聚焦 */}
          <section className="flex flex-col gap-1.5">
            <p className="text-[11px] font-semibold tracking-[0.06em] text-text-3">聚焦</p>
            <Select
              value={settings.focus}
              onValueChange={(v) => update({ focus: v as FocusMode })}
            >
              <SelectTrigger aria-label="聚焦模式" className="h-8 text-[12px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">不聚焦</SelectItem>
                <SelectItem value="failed">聚焦失败</SelectItem>
                <SelectItem value="running">聚焦进行中</SelectItem>
              </SelectContent>
            </Select>
            <CheckRow
              id="hideCompleted"
              label="隐藏已完成"
              checked={settings.hideCompleted}
              onChange={(v) => update({ hideCompleted: v })}
            />
          </section>
        </div>
      </PopoverContent>
    </Popover>
  )
}

function CheckRow({
  id,
  label,
  checked,
  onChange,
}: {
  id: string
  label: string
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className="flex items-center gap-2">
      <Checkbox
        id={id}
        aria-label={label}
        checked={checked}
        onCheckedChange={(v) => onChange(v === true)}
      />
      <Label htmlFor={id} className="text-[12px] text-text-1">
        {label}
      </Label>
    </div>
  )
}
```

> 若 `select.tsx` / `checkbox.tsx` 的导出名或 prop（如 `onCheckedChange`/`onValueChange`）与上述不符，先读对应组件对齐。`lucide-react` 已在依赖中（`StudioEdge`/其它处已用图标）。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/features/workflow-canvas/TopologySettingsPanel.test.tsx`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/features/workflow-canvas/TopologySettingsPanel.tsx web/src/features/workflow-canvas/TopologySettingsPanel.test.tsx
git commit -m "feat(web): TopologySettingsPanel 视图内设置面板

齿轮(aria-label=视图设置)→Popover，分布局/叠加/聚焦三组。聚焦下拉+隐藏
已完成 取代原 5×3 过滤矩阵。复用现成 checkbox/label/select。"
```

---

## Task 9: RunCanvas 串接（布局 / 计时 / 过滤 / 边动画 / 面板 / fitView / 空态）

**Files:**
- Modify: `web/src/features/workflow-canvas/RunCanvas.tsx`
- Test: `web/src/features/workflow-canvas/RunCanvas.test.tsx`（扩展既有）

- [ ] **Step 1: 写失败测试（扩展既有 RunCanvas.test.tsx）**

在既有文件追加。既有 mock 里 `useProjectState` 返回 `RUNNING_STATE`（script=done、storyboard=running）。新增：

```tsx
// 追加到 web/src/features/workflow-canvas/RunCanvas.test.tsx 的 describe 内
it("聚焦失败时非失败节点降透明（dim）", async () => {
  // 复用既有 helper 渲染 RunCanvas（runId="run1"），打开设置选「聚焦失败」。
  renderRunCanvas() // ← 既有测试已有的渲染封装；若无则按既有 it 内写法内联
  fireEvent.click(screen.getByLabelText("视图设置"))
  fireEvent.click(screen.getByLabelText("聚焦模式"))
  // 选 failed（select 展开后点选项）
  fireEvent.click(await screen.findByText("聚焦失败"))
  // RUNNING_STATE 无 failed 节点 → 全部 dim：断言至少一个 canvas-node 容器 style 含 opacity
  const dimmed = document.querySelector('.react-flow__node[style*="opacity"]')
  expect(dimmed).toBeTruthy()
})

it("隐藏已完成时 done 节点不渲染", () => {
  renderRunCanvas()
  fireEvent.click(screen.getByLabelText("视图设置"))
  fireEvent.click(screen.getByLabelText("隐藏已完成"))
  // script 节点 done → 被 hidden；storyboard running 仍在
  // 用 data-status 断言：done 状态节点应不可见
  expect(document.querySelector('[data-status="done"]')).toBeNull()
})

it("全被过滤时显示专属空态", () => {
  renderRunCanvas()
  fireEvent.click(screen.getByLabelText("视图设置"))
  fireEvent.click(screen.getByLabelText("隐藏已完成"))
  fireEvent.click(screen.getByLabelText("聚焦模式"))
  // 选 running 之外…实际：让两个节点都被滤掉的组合（隐藏已完成 + 聚焦失败）
  // 这里直接断言：当可见节点为 0 且总数>0 时出现「当前过滤隐藏了所有节点」
  // （具体组合按实现的过滤口径调整）
})
```

> 说明：本 task 的测试以「行为可见」为准，断言可能因 ReactFlow 测试渲染细节调整（如用 `data-slot`/`data-status` 选择器、或对纯函数过滤逻辑抽出单测）。**推荐**：把过滤/边-active 的纯计算抽成 RunCanvas 同文件内的导出纯函数（如 `computeNodeVisibility(status, settings)` 与 `computeEdgeActive(srcStatus, tgtStatus)`），对纯函数写确定性单测，组件层只做轻量冒烟。先写下面两个纯函数单测：

```tsx
// 追加：纯过滤/边-active 单测（确定性，不依赖 ReactFlow 渲染）
import { computeNodeVisibility, computeEdgeActive } from "./RunCanvas"
import { DEFAULT_TOPOLOGY_SETTINGS } from "./useTopologySettings"

describe("RunCanvas 过滤/边-active 纯函数", () => {
  it("隐藏已完成：done→hidden", () => {
    const v = computeNodeVisibility("done", { ...DEFAULT_TOPOLOGY_SETTINGS, hideCompleted: true })
    expect(v.hidden).toBe(true)
  })
  it("聚焦失败：非 failed→dim，failed→不 dim", () => {
    const s = { ...DEFAULT_TOPOLOGY_SETTINGS, focus: "failed" as const }
    expect(computeNodeVisibility("running", s).dimmed).toBe(true)
    expect(computeNodeVisibility("failed", s).dimmed).toBe(false)
  })
  it("边 active：源 done & 目标 running", () => {
    expect(computeEdgeActive("done", "running")).toBe(true)
    expect(computeEdgeActive("running", "running")).toBe(false)
    expect(computeEdgeActive("done", "done")).toBe(false)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npm run test -- src/features/workflow-canvas/RunCanvas.test.tsx`
Expected: FAIL（`computeNodeVisibility`/`computeEdgeActive` 未导出）

- [ ] **Step 3: 写实现 —— 纯函数 + 串接**

在 `RunCanvas.tsx` 顶部（组件外）加导出纯函数：

```tsx
import type { GraphNodeStatus } from "@/lib/projectState"
import type { TopologySettings } from "./useTopologySettings"

// 节点可见性：hideCompleted 隐藏 done；focus 把非聚焦目标降透明。
export function computeNodeVisibility(
  status: GraphNodeStatus,
  s: TopologySettings,
): { hidden: boolean; dimmed: boolean } {
  const hidden = s.hideCompleted && status === "done"
  let dimmed = false
  if (s.focus === "failed") dimmed = status !== "failed"
  else if (s.focus === "running") dimmed = status !== "running"
  return { hidden, dimmed }
}

// 边「执行前沿」：源已完成、目标进行中。
export function computeEdgeActive(
  sourceStatus: GraphNodeStatus | undefined,
  targetStatus: GraphNodeStatus | undefined,
): boolean {
  return sourceStatus === "done" && targetStatus === "running"
}
```

在 `RunCanvasInner` 里串接（替换/扩展 `RunCanvas.tsx:164-174` 的 rfNodes/rfEdges 构造 + 顶栏 + 画布）：

```tsx
  // 设置 + 计时（计时出口喂进 useProductionTimeline）。
  const { settings, update } = useTopologySettings(projectId)
  const timing = useNodeTiming(runId ?? "")
  const { fitView } = useReactFlow()

  // overlay 同现状
  const overlay = useMemo(() => overlayRunStatus(nodes, wfState), [nodes, wfState])

  // 自动布局坐标（layout!=="saved" 时覆盖自存 position）。
  const autoPos = useMemo(
    () => (settings.layout === "saved" ? null : autoLayout(nodes, settings.layout)),
    [nodes, settings.layout],
  )

  const { rfNodes, rfEdges, visibleCount } = useMemo(() => {
    const { nodes: rn, edges: re } = toReactFlow(nodes)
    let visible = 0
    const withRun: RFNode[] = rn.map((n) => {
      const run = overlay.get(n.id)
      const status: GraphNodeStatus = run?.status ?? "pending"
      const { hidden, dimmed } = computeNodeVisibility(status, settings)
      if (!hidden) visible += 1
      const t = settings.showTiming && run?.todoId ? timing.timingByTodoId.get(run.todoId) : undefined
      return {
        ...n,
        position: autoPos?.get(n.id) ?? n.position,
        hidden,
        style: dimmed ? { ...n.style, opacity: 0.35 } : n.style,
        data: {
          ...n.data,
          run,
          timing: t,
          highlightFailed: settings.focus === "failed",
        },
      }
    })
    // 边：active 注入 + 端点 hidden/dim 联动。
    const hiddenIds = new Set(withRun.filter((n) => n.hidden).map((n) => n.id))
    const dimIds = new Set(
      withRun.filter((n) => (n.style as { opacity?: number } | undefined)?.opacity != null).map((n) => n.id),
    )
    const edges: RFEdge[] = re.map((e) => {
      const active = computeEdgeActive(overlay.get(e.source)?.status, overlay.get(e.target)?.status)
      const edgeHidden = hiddenIds.has(e.source) || hiddenIds.has(e.target)
      const edgeDim = dimIds.has(e.source) || dimIds.has(e.target)
      return {
        ...e,
        hidden: edgeHidden,
        style: edgeDim ? { ...e.style, opacity: 0.35 } : e.style,
        data: { ...(e.data ?? {}), active: settings.flowAnimation && active },
      }
    })
    return { rfNodes: withRun, rfEdges: edges, visibleCount: visible }
  }, [nodes, overlay, settings, timing.timingByTodoId, autoPos])

  // 制片轨道 SSE：把计时出口接上。
  const { log, conn } = useProductionTimeline({
    projectId,
    accessToken: getAccessToken(),
    status: stateQuery.data?.status,
    enabled: !!runId,
    fetchAllEvents,
    planId: runId,
    onState: (s) => qc.setQueryData(["project-state", projectId, runId ?? ""], s),
    onReplay: timing.onReplay,
    onFrame: timing.onFrame,
  })

  // 布局变更且 fitOnUpdate：坐标 commit 后命令式 fitView。
  useEffect(() => {
    if (settings.layout !== "saved" && settings.fitOnUpdate) {
      const h = requestAnimationFrame(() => fitView({ duration: 200 }))
      return () => cancelAnimationFrame(h)
    }
  }, [settings.layout, settings.fitOnUpdate, fitView])
```

在画布区（`RunCanvas.tsx:307-337`）：把齿轮面板挂进顶栏控制簇（`RunCanvas.tsx:377` 那个 `right-[280px]` 浮层，加一个 `<TopologySettingsPanel settings={settings} update={update} />`），并加「全被过滤」空态：

```tsx
        {rfNodes.length > 0 && visibleCount === 0 && (
          <div className="pointer-events-none absolute inset-0 grid place-items-center">
            <p className="rounded-md border border-line bg-bg-surface/80 px-4 py-2 text-center text-[12.5px] text-text-3">
              当前过滤隐藏了所有节点
            </p>
          </div>
        )}
```

顶栏控制簇（`RunCanvas.tsx:378` 那个 `pointer-events-auto` 容器）内最前面加：

```tsx
          <TopologySettingsPanel settings={settings} update={update} />
```

文件顶部补 import：

```tsx
import { useEffect } from "react"
import { useReactFlow } from "@xyflow/react"
import type { GraphNodeStatus } from "@/lib/projectState"
import { autoLayout } from "@/lib/autoLayout"
import { useNodeTiming } from "@/features/workflow/useNodeTiming"
import { useTopologySettings } from "./useTopologySettings"
import { TopologySettingsPanel } from "./TopologySettingsPanel"
```

> `useReactFlow` 必须在 `<ReactFlowProvider>` 内调用 —— `RunCanvasInner` 已被外层 `RunCanvas` 的 `ReactFlowProvider` 包裹（`RunCanvas.tsx:84-90`），满足。`useMemo`/`useState` 已导入；补 `useEffect`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npm run test -- src/features/workflow-canvas/RunCanvas.test.tsx`
Expected: PASS（纯函数单测 + 既有只读硬化测试全绿）

- [ ] **Step 5: 全量前端测试 + lint + 构建确认零回归**

Run:
```bash
cd web && npm run test && npm run lint && npm run build
```
Expected: 全绿（既有 152 例 + 新增；tsc 编译通过；eslint 三绿）

- [ ] **Step 6: 提交**

```bash
git add web/src/features/workflow-canvas/RunCanvas.tsx web/src/features/workflow-canvas/RunCanvas.test.tsx
git commit -m "feat(web): RunCanvas 串接运行态拓扑可视化

接 useTopologySettings + useNodeTiming；按 layout 选自存/自动坐标；按
focus/hideCompleted 标 hidden/dim（含边联动）；逐边注入 data.active；挂
设置面板；fitOnUpdate 命令式 fitView；新增「全被过滤」空态。"
```

---

## Task 10: 收尾 —— 文档勾稽 + 开 PR

- [ ] **Step 1: 更新设计文档状态**

在 `docs/superpowers/specs/2026-06-29-runtime-topology-viz-design.md` 末尾追加一行实现完成标注（日期 + 分支 + 涉及文件），保持 spec↔impl 勾稽。

- [ ] **Step 2: 提交文档**

```bash
git add docs/superpowers/specs/2026-06-29-runtime-topology-viz-design.md
git commit -m "docs: 标注运行态拓扑可视化实现完成"
```

- [ ] **Step 3: push + 开 PR（遵循本仓 PR 纪律，不直推 main）**

```bash
git push -u origin feat/runtime-topology-viz
gh pr create --title "feat: 运行态拓扑流程可视化 + 视图内设置面板" \
  --body "见 docs/superpowers/specs/2026-06-29-runtime-topology-viz-design.md。纯前端：数据流向动画 / 节点耗时(仅实时) / 自动布局+导航 / 状态聚焦过滤 + 齿轮设置面板。零后端改动。3-agent 评审已折入。"
```

> 注：`gh pr edit/view` 在本环境 token 缺 read:org 会失败；如需改 PR 标题/正文用 `gh api -X PATCH repos/.../pulls/N`。

---

## Self-Review（已对 spec 逐节核对）

- **§1 四目标** → 数据流动画 T6+T9；耗时(仅实时) T5+T7+T9；自动布局+导航 T2+T9（MiniMap/Controls/fitView 既有）；状态聚焦/高亮 T7+T8+T9。✅
- **§3 约束** → seq 水位线（T5）落地「回放不计时+重连幂等」；onReplay/onFrame（T4）单连接；autoLayout 复用 seedPositions（T2）；边 data.active 注入（T6/T9）；popover 新增（T1）；token/amber/motion-safe（T6/T7）。✅
- **§4 计时规则** → T5 reducer 逐条实现（baseline 水位线、seq>水位线才实时、planId 重置、running interval 仅 hasRunning 存活）。✅
- **§6 设置项** → T3 schema（layout 每项目+余全局）+ T8 面板（布局/叠加/聚焦三组）。✅
- **§7 降级** → 无实时 startedAt→无 chip（T5/T7）；坏 JSON 回落（T3）；全被过滤空态（T9）；悬挂边/dim 边联动（T9）。✅
- **§8 测试** → autoLayout(T2)、useTopologySettings(T3)、useNodeTiming 含回放不计时/重连幂等/planId 重置(T5)、边 active 方向(T9)、过滤/空态(T9)、回归 lint+build(T9.5)。✅
- **占位符扫描**：无 TBD/TODO；每个实现步给完整代码；测试给完整断言。少数「先读 X 对齐签名」是对既有组件契约的核对提示，非占位（已给 fallback 路径）。✅
- **类型一致性**：`NodeTiming`(T5 创建) ↔ `StudioNodeData.timing`(T7 扩展，T5 之后) ↔ WorkflowNode 消费(T7)；`TopologySettings`/`LayoutMode`/`FocusMode`(T3) ↔ 面板(T8) ↔ RunCanvas 纯函数(T9) 命名一致。✅ 任务顺序已调整为「类型先创建后引用」——每个任务独立编译通过，无前向引用缺口。
