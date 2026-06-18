# SP-D 运行页 UX 打磨 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 打磨运行工作台（`WorkbenchView`）的 4 处体验——事件日志可读性、运行总状态/进度概览、DAG/阶段视图、选中资产面板。这是 **有意的视觉/交互改进**（区别于 SP-A/B 的零视觉变化重构），但 **零行为回归**：所有数据、SSE 实时累积、阶段点击抽屉、相册、绘本阅读器、去审核跳转、运行/取消/重跑均不变；只改呈现，复用既有 API。

**Architecture:** 运行页 = 路由 `_authed/orgs.$org.projects.$id.runs.$runId.tsx`（容器，组装数据 + 行为）→ 渲染 `features/workflow/WorkbenchPage.tsx` 的 `WorkbenchView`（三栏纯表现组件）→ 子组件 `components/studio/{EventLog,TimelineStage,PipGroup}.tsx` + `features/workflow/GraphView.tsx`。后端权威状态走 `ProjectState`（`lib/projectState.ts`），事件日志走 `LogLine[]`（`lib/timeline.ts`）。本计划只触前端表现层，不碰任何后端 / API / projectstate.Compute / SSE 帧。

**Tech Stack:** React 19 + TypeScript；class-variance-authority（cva）做变体；Tailwind v4（CSS 变量 `--amber/--script/--board/--asset/--review/--line` 等）；TanStack Router（文件式路由）+ React Query（`useAccept`/`useReject`/`useRole`）；sonner（toast，已封装在 hooks 内）；vitest + @testing-library/react + userEvent。

**约定与约束（贯穿全计划）：**
- **不弱化既有断言**：`workflow.test.tsx`、`GraphView.test.tsx`、`studio-base.test.tsx`、`timeline-components.test.tsx`、`useProductionTimeline.test.tsx`、`PictureBookReader.test.tsx`、`AssetGalleryModal.test.tsx` 的现有断言必须仍绿；仅在 **真实 DOM 结构变动** 时调整选择器，绝不删断言或放宽。
- **`react-refresh/only-export-components`**：`eslint.config.js` 仅对 `src/routes/**` 与 `src/app/auth.tsx` 关闭此规则（其余目录走 vite preset = warn）。因此：
  - 运行页路由文件（`...runs.$runId.tsx`）可自由 co-locate 非组件导出（规则已关）。
  - `components/studio/EventLog.tsx` 与 `features/workflow/WorkbenchPage.tsx` 新增的 **maps / helpers / 非组件类型** 必须放进 **sibling `*.schema.ts`**（house convention，见 `features/projects/WorkflowDialog.schema.ts`），不得与组件同文件导出。
- **不双 toast**：`useAccept`/`useReject` 自身不发 toast（见 `features/review/api.ts`——只做 `invalidateAfterHitl`），toast 由调用方在 `onSuccess`/`onError` 发；右栏快捷采纳/拒绝须 **复用审核台的同款文案**（`hitlErrorMessage` for 409/403/429），与 `orgs.$org.review.tsx:63-83` 行为对齐。
- **admin 门禁信号**：右栏 accept/reject 只在 admin 时可见，用 **与审核台同一信号** `useRole(org).isAdmin`（`app/rbac.ts`）。
- **无时间戳**：`LogLine` 无 `ts` 字段、SSE 帧不保证带时间——不引入真实时间戳，顺序只用 `seq`。
- **测试命令**（已核 `web/package.json`）：
  - 单测：`cd web && npx vitest run <path>`（`scripts.test` = `tsr generate && vitest run`，可直接用 `npx vitest run`）
  - 类型：`cd web && npx tsc --noEmit`
  - lint：`cd web && npx eslint <path>`

---

## File Structure

| 文件 | 动作 | 职责 |
| --- | --- | --- |
| `web/src/lib/timeline.ts` | Modify | `LogLine` 已带 `kind`；本计划在此 **新增** `KIND_LABEL` 映射 + `friendlyLabel(line)` helper（`kind → 友好短语`），供分组日志渲染。纯函数，无组件——本文件本就无组件，放此处不触 react-refresh。 |
| `web/src/components/studio/EventLog.schema.ts` | Create | EventLog 的分组逻辑（`groupByEmphasis`）+ 阶段中文小标题 `EMPHASIS_TITLE` + 折叠态「最新动态」摘要 helper。非组件导出，独立文件满足 react-refresh。 |
| `web/src/components/studio/EventLog.tsx` | Modify | 由平铺改为 **默认折叠 `<details>`「事件详情」**：折叠态显「最新动态」一行（最后一条友好文案 + 计数）；展开显 **按 emphasis 分组**（小标题 + 组内按 seq）。`lines` 仍是受控 prop（随 SSE 增长自然重渲染）。新增 `kind` 字段透传。 |
| `web/src/components/studio/EventLog.test.tsx` | Create | 分组 / 折叠 / 最新动态 / kind→友好文案 单测（现有 EventLog 断言在 `studio-base.test.tsx`，需同步迁移/适配，见 Task 1）。 |
| `web/src/features/workflow/RunSummary.tsx` | Create | 运行总状态/进度概览条组件：runStatus 文案 + 已完成阶段 X/N + 素材 done/total + 进度条（复用 `BarRow` 或内联）。纯表现，读 props。 |
| `web/src/features/workflow/RunSummary.schema.ts` | Create | `computeRunSummary(state)` 纯函数（X/N、done/total、ratio、runStatus 文案/着色），含 isCustom 退化为节点计数的分支。非组件导出。 |
| `web/src/features/workflow/RunSummary.test.tsx` | Create | computeRunSummary + RunSummary 渲染单测（各 runStatus、isCustom 退化、失败态着色）。 |
| `web/src/features/workflow/WorkbenchPage.tsx` | Modify | 顶栏下方接入 `<RunSummary>`；EventLog 调用补传 `kind`；与 `SlateBar` 协调避免重复（SlateBar 保留为底部细条，RunSummary 为信息条）。 |
| `web/src/components/studio/TimelineStage.tsx` | Modify | 连接线 done 实心 / pending 虚线灰着色更分明（现已有 linked 着色，强化 pending 虚线）。不改 data-slot/data-status（保断言）。 |
| `web/src/components/studio/PipGroup.tsx` | Modify | pip 增大点击热区（done 可点项加 padding 命中区）+ hover 态。保 role/data-slot/data-status。 |
| `web/src/features/workflow/GraphView.tsx` | Modify | done/pending 连接线着色对齐（成本可控即做：层间竖线在「上游 done」时着 agent 色，否则灰）。保 data-slot。 |
| `web/src/features/workflow/SelectedAssetPanel.tsx` | Create | 右栏选中资产面板：更大预览（AssetThumb）+ 资产元数据（type/version/status）+ pending_acceptance 时内联采纳/拒绝（复用 `useAccept`/`useReject`，admin-gated，`hitlErrorMessage`）；非 pending 维持 `AssetPreviewActions`。 |
| `web/src/features/workflow/SelectedAssetPanel.test.tsx` | Create | pending → 显示采纳/拒绝并调用 hook；非 pending → 显示 AssetPreviewActions；非 admin → 不显示采纳/拒绝；409 → hitl 文案。 |
| `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx` | Modify | `preview` 槽改为渲染 `<SelectedAssetPanel>`（传 org/assetId/isAdmin/选中 asset detail）；S5「去审核」内联 CTA 与顶栏共用 `onOpenReview`。规则已对 routes 关闭，可 co-locate。 |
| `web/src/features/workflow/WorkbenchPage.tsx`（S5 CTA） | Modify | 中栏 S5「人工审核」pending 阶段内联「去审核 →」CTA，接 `onOpenReview`（与顶栏同一 prop/handler）。 |

---

## Task 1 — 事件日志可读性（kind→友好文案 + 分组 + 折叠）

**Files:**
- Modify: `web/src/lib/timeline.ts`（在 `LogLine` 定义后，第 16 行之后追加 `KIND_LABEL` + `friendlyLabel`）
- Create: `web/src/components/studio/EventLog.schema.ts`
- Modify: `web/src/components/studio/EventLog.tsx`（整体替换 21-40 行渲染）
- Create: `web/src/components/studio/EventLog.test.tsx`
- Modify: `web/src/components/studio/studio-base.test.tsx`（51-61 行的 EventLog 断言需适配新折叠结构——见步骤）
- Modify: `web/src/features/workflow/WorkbenchPage.tsx`（200-206 行：EventLog 调用补传 `kind`）

> **关键 nuance（写进实现注释）：** `EventLog` 的 `lines` 是受控 prop，SSE 实时累积时 `WorkbenchView` 接 `log` prop 自然重渲染（见路由 `useProductionTimeline` → `log`），所以分组/折叠/最新动态随 `log` 增长实时更新，**无需任何内部累积状态**。折叠态「最新动态」= 最后一条（按 seq 最大）的友好文案 + 总条数。

### 步骤

- [ ] **写映射与 helper 的失败测试**：创建 `web/src/components/studio/EventLog.test.tsx`：
  ```tsx
  import { describe, it, expect } from "vitest"
  import { render, screen } from "@testing-library/react"
  import userEvent from "@testing-library/user-event"
  import { EventLog } from "./EventLog"
  import { groupByEmphasis, EMPHASIS_TITLE, latestSummary } from "./EventLog.schema"
  import { friendlyLabel } from "@/lib/timeline"

  describe("timeline.friendlyLabel", () => {
    it("maps known kinds to friendly Chinese phrases", () => {
      expect(friendlyLabel({ seq: 1, kind: "todo_ready", text: "todo_ready（script）", emphasis: "S2" })).toBe("剧本任务就绪")
      expect(friendlyLabel({ seq: 2, kind: "asset_prescreened", text: "asset_prescreened · 预筛", emphasis: "S4" })).toBe("素材预筛完成")
    })
    it("falls back to text for unknown kinds", () => {
      expect(friendlyLabel({ seq: 3, kind: "weird_kind", text: "原始文案", emphasis: undefined })).toBe("原始文案")
    })
  })

  describe("EventLog.schema", () => {
    it("groups lines by emphasis preserving seq order", () => {
      const groups = groupByEmphasis([
        { seq: 1, kind: "planner_started", text: "规划开始", emphasis: "S1" },
        { seq: 2, kind: "todo_ready", text: "todo_ready（script）", emphasis: "S2" },
        { seq: 3, kind: "asset_generated", text: "asset_generated · 待审", emphasis: "S4" },
        { seq: 4, kind: "todo_finished", text: "完成：script", emphasis: "S2" },
      ])
      expect(groups.map((g) => g.emphasis)).toEqual(["S1", "S2", "S4"])
      expect(groups.find((g) => g.emphasis === "S2")?.lines.map((l) => l.seq)).toEqual([2, 4])
      expect(EMPHASIS_TITLE.S2).toBe("剧本")
    })
    it("latestSummary returns last friendly text + count", () => {
      const s = latestSummary([
        { seq: 1, kind: "planner_started", text: "规划开始", emphasis: "S1" },
        { seq: 2, kind: "run_done", text: "运行结束", emphasis: undefined },
      ])
      expect(s).toEqual({ text: "运行结束", count: 2 })
    })
  })

  describe("EventLog (grouped + collapsed)", () => {
    it("renders empty state", () => {
      render(<EventLog lines={[]} />)
      expect(screen.getByText("暂无事件")).toBeInTheDocument()
    })
    it("collapsed by default showing latest summary, expands to grouped detail", async () => {
      const user = userEvent.setup()
      render(
        <EventLog
          lines={[
            { seq: 1, kind: "planner_started", text: "规划开始", emphasis: "S1" },
            { seq: 2, kind: "todo_finished", text: "完成：script", emphasis: "S2" },
          ]}
        />,
      )
      // 折叠态：最新动态行（最后一条友好文案 + 计数）。
      expect(screen.getByText(/最新动态/)).toBeInTheDocument()
      expect(screen.getByText("完成：script")).toBeInTheDocument()
      expect(screen.getByText(/共 2 条/)).toBeInTheDocument()
      // 展开 → 分组小标题出现。
      await user.click(screen.getByText("事件详情"))
      expect(screen.getByText("规划")).toBeInTheDocument()
      expect(screen.getByText("剧本")).toBeInTheDocument()
    })
  })
  ```
- [ ] **运行测试，确认 FAIL**：`cd web && npx vitest run src/components/studio/EventLog.test.tsx`
  期望：`Error: Failed to resolve import "./EventLog.schema"` / `friendlyLabel is not exported` —— FAIL（模块/导出不存在）。
- [ ] **实现 `timeline.ts` 的 kind→友好文案**：在 `web/src/lib/timeline.ts` 第 16 行（`LogLine` 接口收尾的 `}` 之后）插入：
  ```ts
  // kind → 友好中文短语（左栏事件日志降噪；未命中则回落原始 text）。纯前端表现。
  export const KIND_LABEL: Record<string, string> = {
    planner_started: "规划开始",
    todo_ready: "任务就绪",
    todo_started: "任务开始",
    todo_finished: "任务完成",
    todo_failed: "任务失败",
    asset_generated: "素材已生成",
    asset_submitted: "素材已提交",
    asset_prescreened: "素材预筛完成",
    run_done: "运行结束",
  }

  // 阶段限定的更具体短语（emphasis + kind 联合更友好；优先于 KIND_LABEL）。
  const STAGE_KIND_LABEL: Record<string, string> = {
    "S2:todo_ready": "剧本任务就绪",
    "S3:todo_ready": "分镜任务就绪",
    "S4:asset_prescreened": "素材预筛完成",
  }

  // 单行 → 友好文案：阶段限定优先 → kind 通用 → 回落原始 text。
  export function friendlyLabel(line: LogLine): string {
    if (line.emphasis) {
      const k = STAGE_KIND_LABEL[`${line.emphasis}:${line.kind}`]
      if (k) return k
    }
    return KIND_LABEL[line.kind] ?? line.text
  }
  ```
- [ ] **实现 `EventLog.schema.ts`**：创建 `web/src/components/studio/EventLog.schema.ts`：
  ```ts
  import type { LogLine, StageId } from "@/lib/timeline"
  import { friendlyLabel } from "@/lib/timeline"

  // 分组小标题（emphasis S1–S5 → 中文阶段名）。
  export const EMPHASIS_TITLE: Record<StageId, string> = {
    S1: "规划",
    S2: "剧本",
    S3: "分镜",
    S4: "素材",
    S5: "审核",
  }

  export interface LogGroup {
    emphasis: StageId | "other"
    lines: LogLine[]
  }

  // 按 emphasis 分组，保持首次出现顺序；组内按 seq 升序。无 emphasis 归入 "other"。
  export function groupByEmphasis(lines: LogLine[]): LogGroup[] {
    const order: (StageId | "other")[] = []
    const buckets = new Map<StageId | "other", LogLine[]>()
    for (const line of [...lines].sort((a, b) => a.seq - b.seq)) {
      const key: StageId | "other" = line.emphasis ?? "other"
      if (!buckets.has(key)) {
        buckets.set(key, [])
        order.push(key)
      }
      buckets.get(key)!.push(line)
    }
    return order.map((emphasis) => ({ emphasis, lines: buckets.get(emphasis)! }))
  }

  // 折叠态「最新动态」：最后一条（seq 最大）的友好文案 + 总条数。
  export function latestSummary(lines: LogLine[]): { text: string; count: number } | null {
    if (lines.length === 0) return null
    const last = lines.reduce((a, b) => (b.seq > a.seq ? b : a))
    return { text: friendlyLabel(last), count: lines.length }
  }
  ```
- [ ] **重写 `EventLog.tsx`**（受控 prop + 折叠 `<details>` + 分组）。整体替换 `web/src/components/studio/EventLog.tsx` 全文为：
  ```tsx
  import type { ReactNode } from "react"
  import { cn } from "@/lib/utils"
  import type { LogLine } from "@/lib/timeline"
  import { friendlyLabel } from "@/lib/timeline"
  import { groupByEmphasis, EMPHASIS_TITLE, latestSummary } from "./EventLog.schema"

  export interface EventLogProps {
    lines: LogLine[]
    className?: string
    emptyText?: ReactNode
  }

  // 默认折叠的「事件详情」：折叠态只显「最新动态」一行（最后一条友好文案 + 计数），
  // 展开显按 emphasis 分组（小标题 + 组内按 seq）。lines 受控（随 SSE 累积自然重渲染）。
  export function EventLog({ lines, className, emptyText = "暂无事件" }: EventLogProps) {
    if (lines.length === 0) {
      return <div className={cn("py-1 text-[11px] text-text-3", className)}>{emptyText}</div>
    }
    const summary = latestSummary(lines)
    const groups = groupByEmphasis(lines)
    return (
      <details className={cn("group rounded-[10px] border border-line bg-bg-surface", className)}>
        <summary className="flex cursor-pointer items-center justify-between gap-2 px-3 py-2 text-[12px] text-text-2 marker:content-none">
          <span className="font-medium text-text-1">事件详情</span>
          {summary && (
            <span className="truncate text-[11px] text-text-3">
              最新动态：{summary.text} · 共 {summary.count} 条
            </span>
          )}
        </summary>
        <div className="border-t border-line px-3 py-2">
          {groups.map((g) => (
            <div key={g.emphasis} className="mb-2 last:mb-0">
              <h5 className="mb-1 text-[10.5px] font-semibold tracking-[0.06em] text-text-3">
                {g.emphasis === "other" ? "其他" : EMPHASIS_TITLE[g.emphasis]}
              </h5>
              {g.lines.map((line) => (
                <div
                  key={line.seq}
                  className="border-b border-dashed border-[#23272e] py-[3px] font-mono text-[11px] text-text-3 last:border-b-0"
                >
                  {friendlyLabel(line)}
                </div>
              ))}
            </div>
          ))}
        </div>
      </details>
    )
  }
  ```
- [ ] **适配 `WorkbenchPage.tsx` 的 EventLog 调用**（补传 `kind`）。在 `web/src/features/workflow/WorkbenchPage.tsx` 把 200-206 行：
  ```tsx
            <EventLog
              lines={log.map((l) => ({
                seq: l.seq,
                text: l.text,
                emphasis: l.emphasis,
              }))}
            />
  ```
  改为直接透传完整 `LogLine`（含 `kind`）：
  ```tsx
            <EventLog lines={log} />
  ```
- [ ] **适配 `studio-base.test.tsx` 的 EventLog 断言**（DOM 真变了 → 调选择器，不弱化）。在 `web/src/components/studio/studio-base.test.tsx` 把 51-61 行的 `it("EventLog renders empty state then lines", …)` 替换为：
  ```tsx
    it("EventLog renders empty state then collapsed summary", () => {
      const { rerender } = render(<EventLog lines={[]} />)
      expect(screen.getByText("暂无事件")).toBeInTheDocument()
      rerender(
        <EventLog
          lines={[{ seq: 1, kind: "todo_finished", text: "剧本已生成", emphasis: "S2" }]}
        />,
      )
      // 折叠态：事件详情 summary + 最新动态行（kind 未命中具体映射 → friendlyLabel 回落 KIND_LABEL「任务完成」）。
      expect(screen.getByText("事件详情")).toBeInTheDocument()
      expect(screen.getByText(/最新动态/)).toBeInTheDocument()
    })
  ```
  > 注：原断言 `getByText("剧本已生成")` 在折叠态不再可见（在 `<details>` 内部，但 jsdom 仍渲染 DOM——getByText 仍能命中）。为避免脆弱，改断言 summary 行；分组展开断言归 `EventLog.test.tsx` 专测。`studio-base.test.tsx` 顶部 import 需确认含 `EventLog`（已在 9 行）。
- [ ] **运行测试，确认 PASS**：
  `cd web && npx vitest run src/components/studio/EventLog.test.tsx src/components/studio/studio-base.test.tsx`
  期望：两文件全部用例 PASS。
- [ ] **运行 workflow 回归（确认 EventLog 调用改动不破 WorkbenchView）**：
  `cd web && npx vitest run src/features/workflow/workflow.test.tsx`
  期望：全绿（workflow.test 传 `log: []`，EventLog 显空态「暂无事件」，无断言依赖日志结构）。
- [ ] **类型 + lint**：
  `cd web && npx tsc --noEmit && npx eslint src/lib/timeline.ts src/components/studio/EventLog.tsx src/components/studio/EventLog.schema.ts src/components/studio/EventLog.test.tsx src/features/workflow/WorkbenchPage.tsx`
  期望：无 error（含 react-refresh：maps 已分到 `EventLog.schema.ts`，`timeline.ts` 无组件）。
- [ ] **提交**：
  ```bash
  cd web && git add src/lib/timeline.ts src/components/studio/EventLog.tsx src/components/studio/EventLog.schema.ts src/components/studio/EventLog.test.tsx src/components/studio/studio-base.test.tsx src/features/workflow/WorkbenchPage.tsx
  git commit -m "feat(studio/run): group + collapse event log with friendly kind labels

降噪运行页左栏：kind→友好中文映射 + 按 emphasis 分组 + 默认折叠「事件详情」（折叠态显最新动态 + 计数）。受控 prop 随 SSE 累积自然更新，不引入时间戳。"
  ```

---

## Task 2 — 运行总状态/进度概览（RunSummary 条）

**Files:**
- Create: `web/src/features/workflow/RunSummary.schema.ts`
- Create: `web/src/features/workflow/RunSummary.tsx`
- Create: `web/src/features/workflow/RunSummary.test.tsx`
- Modify: `web/src/features/workflow/WorkbenchPage.tsx`（顶栏 header 之后、三栏 grid 之前接入 `<RunSummary state={state} />`；与 `SlateBar` 协调）

> **关键 nuance（写进实现注释）：** isCustom 工作流 stages 可能为空（节点制），X/N 退化为 `nodes` 计数（done 节点 / 总节点）。runStatus 文案/着色：`running`→生产中（amber/running），`done` 且 status≠failed/canceled→已完成（done），`idle`→空闲（pending）；终止失败/取消用 `state.status` 经 `statusVariant`/`statusLabel` 着色。素材计数直接读 `state.assets.{done,total}`。进度条 ratio = 阶段完成比例（custom 用节点比例）。

### 步骤

- [ ] **写 computeRunSummary 失败测试**：创建 `web/src/features/workflow/RunSummary.test.tsx`：
  ```tsx
  import { describe, it, expect } from "vitest"
  import { render, screen } from "@testing-library/react"
  import { RunSummary } from "./RunSummary"
  import { computeRunSummary } from "./RunSummary.schema"
  import type { ProjectState, StageState } from "@/lib/projectState"

  function stages(): StageState[] {
    return [
      { role: "planner", status: "done" },
      { role: "script", status: "done" },
      { role: "storyboard", status: "running" },
      { role: "asset", status: "blocked" },
      { role: "review", status: "blocked" },
    ]
  }
  function makeState(over: Partial<ProjectState> = {}): ProjectState {
    return {
      projectId: "p1", version: 1, status: "running", runStatus: "running",
      stages: stages(), pips: [], assets: { total: 4, done: 1, pending: 0 },
      nodes: [], edges: [], isCustom: false, ...over,
    }
  }

  describe("computeRunSummary", () => {
    it("counts done stages X/N + asset done/total + ratio for fixed pipeline", () => {
      const s = computeRunSummary(makeState())
      expect(s.stagesDone).toBe(2)
      expect(s.stagesTotal).toBe(5)
      expect(s.assetsDone).toBe(1)
      expect(s.assetsTotal).toBe(4)
      expect(s.ratio).toBeCloseTo(2 / 5)
      expect(s.runLabel).toBe("生产中")
    })
    it("falls back to node count for isCustom workflows", () => {
      const s = computeRunSummary(makeState({
        isCustom: true,
        stages: [],
        nodes: [
          { id: "a", label: "x", type: "script", status: "done" },
          { id: "b", label: "y", type: "asset", status: "running" },
        ],
        edges: [],
      }))
      expect(s.stagesDone).toBe(1)
      expect(s.stagesTotal).toBe(2)
      expect(s.ratio).toBeCloseTo(0.5)
    })
    it("labels done/idle/failed run states", () => {
      expect(computeRunSummary(makeState({ runStatus: "done", status: "review" })).runLabel).toBe("已完成")
      expect(computeRunSummary(makeState({ runStatus: "idle", status: "draft" })).runLabel).toBe("空闲")
      const failed = computeRunSummary(makeState({ runStatus: "done", status: "failed" }))
      expect(failed.runLabel).toBe("失败")
      expect(failed.variant).toBe("rejected")
    })
  })

  describe("RunSummary", () => {
    it("renders X/N stages, asset tally and a progress bar", () => {
      render(<RunSummary state={makeState()} />)
      expect(screen.getByText("生产中")).toBeInTheDocument()
      expect(screen.getByText(/阶段 2\/5/)).toBeInTheDocument()
      expect(screen.getByText(/素材 1\/4/)).toBeInTheDocument()
      expect(document.querySelector('[data-slot="run-summary"]')).not.toBeNull()
    })
  })
  ```
- [ ] **运行测试，确认 FAIL**：`cd web && npx vitest run src/features/workflow/RunSummary.test.tsx`
  期望：`Failed to resolve import "./RunSummary"` —— FAIL。
- [ ] **实现 `RunSummary.schema.ts`**：创建 `web/src/features/workflow/RunSummary.schema.ts`：
  ```ts
  import type { ProjectState } from "@/lib/projectState"
  import type { ProjectStatus } from "@/lib/types"
  import { statusVariant } from "@/features/projects/status"
  import type { StudioBadgeProps } from "@/components/studio/Badge"

  type BadgeVariant = NonNullable<StudioBadgeProps["variant"]>

  export interface RunSummaryData {
    runLabel: string
    variant: BadgeVariant
    stagesDone: number
    stagesTotal: number
    assetsDone: number
    assetsTotal: number
    ratio: number
  }

  // runStatus + 终止态 → 概览文案。失败/取消优先（避免 done 误显「已完成」）。
  function runLabel(state: ProjectState): { label: string; variant: BadgeVariant } {
    if (state.status === "failed") return { label: "失败", variant: "rejected" }
    if (state.status === "canceled") return { label: "已取消", variant: "rejected" }
    if (state.runStatus === "running") return { label: "生产中", variant: statusVariant("running") }
    if (state.runStatus === "done") return { label: "已完成", variant: statusVariant("completed") }
    return { label: "空闲", variant: statusVariant(state.status as ProjectStatus) }
  }

  // 阶段进度：固定管线用 stages；isCustom（stages 为空）退化为节点计数。
  export function computeRunSummary(state: ProjectState): RunSummaryData {
    const units =
      state.stages.length > 0
        ? state.stages.map((s) => s.status)
        : state.nodes.map((n) => n.status)
    const stagesTotal = units.length
    const stagesDone = units.filter((s) => s === "done").length
    const ratio = stagesTotal === 0 ? 0 : stagesDone / stagesTotal
    const { label, variant } = runLabel(state)
    return {
      runLabel: label,
      variant,
      stagesDone,
      stagesTotal,
      assetsDone: state.assets.done,
      assetsTotal: state.assets.total,
      ratio,
    }
  }
  ```
- [ ] **实现 `RunSummary.tsx`**：创建 `web/src/features/workflow/RunSummary.tsx`：
  ```tsx
  import { Badge } from "@/components/studio/Badge"
  import { cn } from "@/lib/utils"
  import type { ProjectState } from "@/lib/projectState"
  import { computeRunSummary } from "./RunSummary.schema"

  export interface RunSummaryProps {
    state: ProjectState
    className?: string
  }

  // 运行总状态/进度概览条：runStatus 文案徽标 + 阶段 X/N + 素材 done/total + 细进度条。
  // 纯表现，读权威 state；不引入新数据。与底部 SlateBar 分工：SlateBar=运行中底部动效，
  // RunSummary=常驻信息条（含完成/失败态）。
  export function RunSummary({ state, className }: RunSummaryProps) {
    const s = computeRunSummary(state)
    const pct = Math.max(0, Math.min(1, s.ratio)) * 100
    return (
      <div
        data-slot="run-summary"
        className={cn(
          "flex flex-wrap items-center gap-x-4 gap-y-1.5 border-b border-line px-4 py-2 sm:px-6",
          className,
        )}
      >
        <Badge variant={s.variant}>{s.runLabel}</Badge>
        <span className="text-[12px] text-text-2">
          阶段 <b className="font-mono font-medium text-text-1">{s.stagesDone}/{s.stagesTotal}</b>
        </span>
        <span className="text-[12px] text-text-2">
          素材 <b className="font-mono font-medium text-text-1">{s.assetsDone}/{s.assetsTotal}</b>
        </span>
        <span className="ml-auto h-2 w-40 max-w-[40%] overflow-hidden rounded-full bg-bg-base">
          <span
            className="block h-full rounded-full bg-amber transition-[width] duration-300"
            style={{ width: `${pct}%` }}
          />
        </span>
      </div>
    )
  }
  ```
- [ ] **运行测试，确认 PASS**：`cd web && npx vitest run src/features/workflow/RunSummary.test.tsx`
  期望：全绿。
- [ ] **接入 WorkbenchPage**：在 `web/src/features/workflow/WorkbenchPage.tsx` 顶部 import 区加：
  ```tsx
  import { RunSummary } from "./RunSummary"
  ```
  然后在 `</header>`（160 行）之后、三栏 grid 容器（163 行 `<div className="flex min-h-0 flex-1 …">`）之前插入：
  ```tsx
      <RunSummary state={state} />
  ```
- [ ] **运行 workflow 回归**：`cd web && npx vitest run src/features/workflow/workflow.test.tsx`
  期望：全绿（RunSummary 是新增节点，不改既有徽标/阶段/SlateBar 断言；`shows 待审核·N badge` 用例查 `data-slot="slate-bar"` 为 null 仍成立——SlateBar 未动）。
- [ ] **类型 + lint**：
  `cd web && npx tsc --noEmit && npx eslint src/features/workflow/RunSummary.tsx src/features/workflow/RunSummary.schema.ts src/features/workflow/RunSummary.test.tsx src/features/workflow/WorkbenchPage.tsx`
  期望：无 error（computeRunSummary 等非组件导出在 `.schema.ts`）。
- [ ] **提交**：
  ```bash
  cd web && git add src/features/workflow/RunSummary.tsx src/features/workflow/RunSummary.schema.ts src/features/workflow/RunSummary.test.tsx src/features/workflow/WorkbenchPage.tsx
  git commit -m "feat(studio/run): add RunSummary progress bar above workbench

顶栏下方常驻概览条：runStatus 文案 + 阶段 X/N + 素材 done/total + 进度条；isCustom 退化为节点计数；失败/取消态复用 statusVariant 着色。与 SlateBar 分工不重复。"
  ```

---

## Task 3 — DAG/阶段视图打磨（连接线着色 + pip 热区 + S5 内联 CTA）

**Files:**
- Modify: `web/src/components/studio/TimelineStage.tsx`（94-102 行连接线着色强化 pending 虚线）
- Modify: `web/src/components/studio/PipGroup.tsx`（46-58 行 done 可点 pip 增大热区 + hover）
- Modify: `web/src/features/workflow/GraphView.tsx`（39-44 行层间连接线 done 着色，成本可控）
- Modify: `web/src/features/workflow/WorkbenchPage.tsx`（中栏 S5「人工审核」pending 内联「去审核 →」CTA，接 `onOpenReview`）
- Modify: `web/src/components/studio/timeline-components.test.tsx`（pip 热区改 DOM → 适配 + 新增连接线/CTA 断言）

> **关键 nuance：** S5 内联 CTA 与顶栏 `onOpenReview` 是 **同一 prop / 同一 handler**（路由 407-413 行的 `navigate({to:"/orgs/$org/review", search:{project}})`），不新增 handler。pip 热区增大不得改 `data-slot="pip"`/`role`/`data-status`（保 `workflow.test` 第 115/205 行 `querySelectorAll('[data-slot="pip"]')` 与 `getByRole("button", {name:/a1/})`）。连接线着色不得改 `TimelineStage` 的 `data-slot="stage"`/`data-status`。

### 步骤

- [ ] **写连接线 + CTA + pip 热区的失败测试**。在 `web/src/components/studio/timeline-components.test.tsx` 末尾（最后一个 `describe` 收尾的 `})` 之前）追加：
  ```tsx
    it("TimelineStage connector is solid when linked, dashed-gray when not", () => {
      const { rerender } = render(<TimelineStage stage={stage({ status: "done", linked: true })} />)
      const linkedConn = document.querySelector('[data-slot="stage"] [data-slot="connector"]')
      expect(linkedConn?.getAttribute("data-linked")).toBe("true")
      rerender(<TimelineStage stage={stage({ status: "pending", linked: false })} />)
      const pendingConn = document.querySelector('[data-slot="stage"] [data-slot="connector"]')
      expect(pendingConn?.getAttribute("data-linked")).toBe("false")
    })
  ```
  并在 `WorkbenchView` 的 S5 CTA 测试归 `workflow.test.tsx`（同一 onOpenReview）。在 `web/src/features/workflow/workflow.test.tsx` 的 `describe("WorkbenchView …")` 内（`isCustom=false …` 用例之后）追加：
  ```tsx
    // T3：S5 人工审核 pending 阶段内联「去审核 →」CTA，与顶栏共用 onOpenReview。
    it("renders an inline S5 review CTA wired to the same onOpenReview", async () => {
      const onOpenReview = vi.fn()
      const user = userEvent.setup()
      const state = makeState({
        status: "review",
        runStatus: "done",
        stages: [
          { role: "planner", status: "done" },
          { role: "script", status: "done" },
          { role: "storyboard", status: "done" },
          { role: "asset", status: "done" },
          { role: "review", status: "pending" },
        ],
        pips: [{ todoId: "a1", status: "done", assetId: "as1" }],
        assets: { total: 1, done: 1, pending: 1 },
      })
      render(
        <WorkbenchView {...baseWorkbenchProps()} state={state} live={false} onOpenReview={onOpenReview} />,
      )
      // 顶栏 + 内联两处「去审核」；点击内联（最后一个）也触发同一 handler。
      const ctas = screen.getAllByRole("button", { name: /去审核/ })
      expect(ctas.length).toBeGreaterThanOrEqual(2)
      await user.click(ctas[ctas.length - 1])
      expect(onOpenReview).toHaveBeenCalled()
    })
  ```
- [ ] **运行测试，确认 FAIL**：
  `cd web && npx vitest run src/components/studio/timeline-components.test.tsx src/features/workflow/workflow.test.tsx`
  期望：新增用例 FAIL（`data-slot="connector"` 不存在；内联 CTA 不存在，`getAllByRole` 只找到 1 个）。
- [ ] **实现 TimelineStage 连接线着色**。在 `web/src/components/studio/TimelineStage.tsx` 把 94-102 行的连接线 `<span>` 替换为（加 `data-slot`/`data-linked` + pending 虚线）：
  ```tsx
      {/* 连接线：linked（上游 done）实心着 agent 色；否则虚线灰，态分明。 */}
      {!last && (
        <span
          aria-hidden
          data-slot="connector"
          data-linked={stage.linked}
          className={cn(
            "absolute left-[21px] top-[30px] bottom-0 w-0.5",
            stage.linked
              ? "bg-[var(--cur)]"
              : "border-l-2 border-dashed border-line bg-transparent",
          )}
        />
      )}
  ```
- [ ] **实现 PipGroup 热区 + hover**。在 `web/src/components/studio/PipGroup.tsx` 把 selectable 分支的 `<button>`（46-58 行）替换为（外层加大命中区 + hover 环，内层方块保 `data-slot="pip"`/`data-status` 不变）：
  ```tsx
        if (selectable) {
          return (
            <button
              key={pip.todoId}
              type="button"
              aria-label={title}
              onClick={() => onSelectPip(pip)}
              title={title}
              className="-m-1 grid place-items-center rounded-md p-1 transition-colors hover:bg-bg-raised cursor-pointer"
            >
              <span
                data-slot="pip"
                data-status={pip.status}
                className={pipVariants({ status: pip.status })}
              />
            </button>
          )
        }
  ```
  > 注：`data-slot="pip"` 从 `<button>` 下移到内层 `<span>`，`querySelectorAll('[data-slot="pip"]')` 计数不变（仍每 pip 一个）；`getByRole("button",{name:/a1/})`（aria-label 含 todoId）仍命中外层按钮。
- [ ] **实现 GraphView 层间连接线着色**（成本可控：上游层全 done → 实心 agent 色，否则灰）。在 `web/src/features/workflow/GraphView.tsx` 把 39-45 行（layer 容器 + 竖线）替换为：
  ```tsx
        {layers.map((layer, li) => {
          // 上一层全部 done → 连接线实心（进度已抵达本层）；否则灰。
          const prevDone = li > 0 && layers[li - 1].every((n) => n.status === "done")
          return (
            <div key={layer[0].id} data-slot="graph-layer" className="relative pb-[30px]">
              {li > 0 && (
                <span
                  aria-hidden
                  data-slot="graph-connector"
                  data-linked={prevDone}
                  className={cn(
                    "absolute left-1/2 -top-[30px] h-[30px] w-0.5 -translate-x-1/2",
                    prevDone ? "bg-asset" : "border-l-2 border-dashed border-line",
                  )}
                />
              )}
  ```
  并把该 `map` 的闭合（原 52 行 `))}` ）改为：
  ```tsx
            </div>
          )
        })}
  ```
  > 即把箭头函数体从隐式 return 改为带 `{ … return ( … ) }`。`cn` 已 import（GraphView 第 1 行）。`data-slot="graph"`/`graph-node`/`graph-layer` 不变，GraphView.test 全绿。
- [ ] **实现 S5 内联 CTA**。在 `web/src/features/workflow/WorkbenchPage.tsx` 的 `TimelineStage` 渲染块（246-250 行的 children）中，S4 pip 之后追加 S5 内联 CTA。把 247-249 行：
  ```tsx
                    {id === "S4" && pips.length > 0 && (
                      <PipGroup pips={pips} onSelectPip={onSelectPip} />
                    )}
  ```
  替换为：
  ```tsx
                    {id === "S4" && pips.length > 0 && (
                      <PipGroup pips={pips} onSelectPip={onSelectPip} />
                    )}
                    {id === "S5" && stage.status === "pending" && onOpenReview && (
                      <Button variant="ghost" className="mt-2" onClick={onOpenReview}>
                        去审核 →
                      </Button>
                    )}
  ```
  > `Button` 已 import（WorkbenchPage 第 3 行）。`stage` 是 map 回调参数（权威 `StageState`，含 `status`），`onOpenReview` 是 props（已解构，第 99 行）。
- [ ] **运行测试，确认 PASS**：
  `cd web && npx vitest run src/components/studio/timeline-components.test.tsx src/features/workflow/workflow.test.tsx src/features/workflow/GraphView.test.tsx`
  期望：全绿（含原 pip/stage/graph 断言 + 新增连接线/CTA 断言）。
- [ ] **类型 + lint**：
  `cd web && npx tsc --noEmit && npx eslint src/components/studio/TimelineStage.tsx src/components/studio/PipGroup.tsx src/features/workflow/GraphView.tsx src/features/workflow/WorkbenchPage.tsx`
  期望：无 error。
- [ ] **提交**：
  ```bash
  cd web && git add src/components/studio/TimelineStage.tsx src/components/studio/PipGroup.tsx src/features/workflow/GraphView.tsx src/features/workflow/WorkbenchPage.tsx src/components/studio/timeline-components.test.tsx src/features/workflow/workflow.test.tsx
  git commit -m "feat(studio/run): clearer DAG coloring + pip hit-area + inline S5 review CTA

连接线 done 实心/pending 虚线灰（TimelineStage + GraphView）；S4 pip 增大点击热区 + hover；S5 人工审核 pending 内联「去审核 →」CTA，与顶栏共用 onOpenReview。data-slot/role 不变，既有断言全绿。"
  ```

---

## Task 4 — 选中资产面板增强（更大预览 + 元数据 + pending accept/reject）

**Files:**
- Create: `web/src/features/workflow/SelectedAssetPanel.tsx`
- Create: `web/src/features/workflow/SelectedAssetPanel.test.tsx`
- Modify: `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`（preview 槽改渲染 `<SelectedAssetPanel>`；新增 `useRole`、选中 asset detail 查询）

> **关键 nuance：** accept/reject **复用** `useAccept`/`useReject`（`features/review/api.ts`，POST `/api/assets/{id}/accept|reject`，自带 `invalidateAfterHitl`，不发 toast）。toast 由 `SelectedAssetPanel` 在 `onSuccess`/`onError` 发，错误文案用 `hitlErrorMessage`（与审核台 409/429 对齐）。admin 门禁用 `useRole(org).isAdmin`（与审核台同信号）。pending 判定：asset.status === `"pending_acceptance"`。`SelectedAssetPanel` 用 `useAsset(assetId)`（`features/review/api.ts:40`）拉详情拿 type/version/status。非 pending 维持 `AssetPreviewActions`。

### 步骤

- [ ] **写 SelectedAssetPanel 失败测试**：创建 `web/src/features/workflow/SelectedAssetPanel.test.tsx`：
  ```tsx
  import { describe, it, expect, vi, beforeEach } from "vitest"
  import { render, screen } from "@testing-library/react"
  import userEvent from "@testing-library/user-event"
  import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
  import { SelectedAssetPanel } from "./SelectedAssetPanel"
  import type { AssetDetail, Asset } from "@/lib/types"

  // 隔离外部依赖：AssetThumb 走网络、apiJSON 走 fetch、toast 走 sonner。
  vi.mock("./AssetThumb.tsx", () => ({ AssetThumb: () => <div data-testid="thumb" /> }))
  vi.mock("@/lib/apiClient", () => ({
    apiJSON: vi.fn(),
    getAccessToken: () => "tok",
    ApiError: class ApiError extends Error { status = 0 },
  }))
  const toast = { success: vi.fn(), error: vi.fn() }
  vi.mock("sonner", () => ({ toast }))

  function asset(over: Partial<Asset> = {}): Asset {
    return {
      id: "as1", projectId: "p1", shotId: "s1", todoId: "t1", type: "image",
      blobKey: "", url: "", prompt: "", style: "", provider: "openai", model: "dall-e",
      status: "pending_acceptance", version: 2, parentAssetId: "", tags: [],
      prescreenScore: 0, prescreenFlags: [], prescreenNote: "", externalJobId: "", ...over,
    }
  }
  function detail(over: Partial<Asset> = {}): AssetDetail {
    return { asset: asset(over), versions: [] }
  }

  function wrap(ui: React.ReactNode) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
  }

  beforeEach(() => vi.clearAllMocks())

  describe("SelectedAssetPanel", () => {
    it("renders metadata (type/version/status) + thumb", () => {
      wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin={false} detail={detail()} />)
      expect(screen.getByTestId("thumb")).toBeInTheDocument()
      expect(screen.getByText(/image/)).toBeInTheDocument()
      expect(screen.getByText(/v2/)).toBeInTheDocument()
    })
    it("shows accept/reject for admin when pending_acceptance", () => {
      wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin detail={detail()} />)
      expect(screen.getByRole("button", { name: /采纳/ })).toBeInTheDocument()
      expect(screen.getByRole("button", { name: /拒绝/ })).toBeInTheDocument()
    })
    it("hides accept/reject for non-admin even when pending", () => {
      wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin={false} detail={detail()} />)
      expect(screen.queryByRole("button", { name: /采纳/ })).not.toBeInTheDocument()
    })
    it("falls back to AssetPreviewActions when not pending", () => {
      wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin detail={detail({ status: "accepted" })} />)
      expect(screen.queryByRole("button", { name: /采纳/ })).not.toBeInTheDocument()
      expect(screen.getByRole("button", { name: /复制链接/ })).toBeInTheDocument()
    })
    it("calls accept hook and toasts on success", async () => {
      const { apiJSON } = await import("@/lib/apiClient")
      ;(apiJSON as ReturnType<typeof vi.fn>).mockResolvedValue({ id: "as1", status: "accepted" })
      const user = userEvent.setup()
      wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin detail={detail()} />)
      await user.click(screen.getByRole("button", { name: /采纳/ }))
      expect(apiJSON).toHaveBeenCalledWith("/api/assets/as1/accept", { method: "POST" })
    })
  })
  ```
- [ ] **运行测试，确认 FAIL**：`cd web && npx vitest run src/features/workflow/SelectedAssetPanel.test.tsx`
  期望：`Failed to resolve import "./SelectedAssetPanel"` —— FAIL。
- [ ] **实现 `SelectedAssetPanel.tsx`**：创建 `web/src/features/workflow/SelectedAssetPanel.tsx`：
  ```tsx
  import { toast } from "sonner"
  import { Button } from "@/components/studio/Button"
  import { cn } from "@/lib/utils"
  import { AssetThumb } from "./AssetThumb.tsx"
  import { AssetPreviewActions } from "./AssetPreviewActions"
  import { useAccept, useReject } from "@/features/review/api"
  import { hitlErrorMessage } from "@/features/review/hitlError"
  import type { AssetDetail } from "@/lib/types"

  export interface SelectedAssetPanelProps {
    org: string
    assetId: string
    // 与审核台同一 admin 信号（useRole(org).isAdmin）。
    isAdmin: boolean
    // 选中资产详情（容器经 useAsset 拉取）；加载中可空。
    detail?: AssetDetail
    className?: string
  }

  // 右栏选中资产面板：更大预览 + 元数据（type/version/status）+ pending 态内联采纳/拒绝。
  // accept/reject 复用 features/review 的 hooks（自带失效，不发 toast）；toast + 错误文案在此发，
  // 与审核台 409/429 对齐（hitlErrorMessage）。非 pending → 维持 AssetPreviewActions。
  export function SelectedAssetPanel({
    org,
    assetId,
    isAdmin,
    detail,
    className,
  }: SelectedAssetPanelProps) {
    const accept = useAccept(org)
    const reject = useReject(org)
    const asset = detail?.asset
    const isPending = asset?.status === "pending_acceptance"

    function onAccept() {
      accept.mutate(assetId, {
        onSuccess: () => toast.success("已采纳"),
        onError: (err) => toast.error(hitlErrorMessage(err)),
      })
    }
    function onReject() {
      reject.mutate(assetId, {
        onSuccess: () => toast.success("已退回"),
        onError: (err) => toast.error(hitlErrorMessage(err)),
      })
    }

    const busy = accept.isPending || reject.isPending

    return (
      <div className={cn("flex flex-col gap-3", className)}>
        {/* 更大预览。 */}
        <AssetThumb assetId={assetId} alt="选中素材" className="h-[220px] w-full" />
        {/* 元数据。 */}
        {asset && (
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12px]">
            <dt className="text-text-3">类型</dt>
            <dd className="text-text-1">{asset.type}</dd>
            <dt className="text-text-3">版本</dt>
            <dd className="text-text-1">v{asset.version}</dd>
            <dt className="text-text-3">状态</dt>
            <dd className="text-text-1">{asset.status}</dd>
          </dl>
        )}
        {/* pending + admin → 内联采纳/拒绝；否则维持原 AssetPreviewActions。 */}
        {isPending && isAdmin ? (
          <div className="flex gap-2">
            <Button variant="amber" className="flex-1" onClick={onAccept} disabled={busy}>
              采纳
            </Button>
            <Button variant="ghost" className="flex-1" onClick={onReject} disabled={busy}>
              拒绝
            </Button>
          </div>
        ) : (
          <AssetPreviewActions assetId={assetId} className="flex gap-2" />
        )}
      </div>
    )
  }
  ```
- [ ] **运行测试，确认 PASS**：`cd web && npx vitest run src/features/workflow/SelectedAssetPanel.test.tsx`
  期望：全绿。
- [ ] **接入路由 preview 槽**。在 `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`：
  顶部 import 区（40-41 行附近）加：
  ```tsx
  import { useRole } from "@/app/rbac"
  import { useAsset } from "@/features/review/api"
  import { SelectedAssetPanel } from "@/features/workflow/SelectedAssetPanel"
  ```
  在 `const { isAdmin } = role` 处——`RunWorkbenchPage` 内 `const navigate = useNavigate()`（57 行）之后加：
  ```tsx
    const { isAdmin } = useRole(org)
  ```
  在 `const previewAssetId = …`（229-230 行）之后加选中详情查询：
  ```tsx
    const previewDetailQuery = useAsset(previewAssetId ?? "")
  ```
  把 preview 槽（375-382 行）：
  ```tsx
      preview={
        previewAssetId ? (
          <div className="flex flex-col gap-3">
            <AssetThumb assetId={previewAssetId} alt="选中素材" className="h-[150px] w-full" />
            <AssetPreviewActions assetId={previewAssetId} className="flex gap-2" />
          </div>
        ) : undefined
      }
  ```
  替换为：
  ```tsx
      preview={
        previewAssetId ? (
          <SelectedAssetPanel
            org={org}
            assetId={previewAssetId}
            isAdmin={isAdmin}
            detail={previewDetailQuery.data}
          />
        ) : undefined
      }
  ```
  > 移除路由对 `AssetThumb`/`AssetPreviewActions` 的直接 import（14-15 行）——它们已 orphan（SelectedAssetPanel 内部用）。删除这两行 import 以清自身产生的孤儿（house 纪律：清自己制造的孤儿）。
- [ ] **类型 + lint**：
  `cd web && npx tsc --noEmit && npx eslint src/features/workflow/SelectedAssetPanel.tsx src/features/workflow/SelectedAssetPanel.test.tsx src/routes/_authed/orgs.\$org.projects.\$id.runs.\$runId.tsx`
  期望：无 error（路由文件 react-refresh 已关；无未用 import 残留）。
- [ ] **运行 workflow 回归**（确认 WorkbenchView preview slot 仍渲染）：
  `cd web && npx vitest run src/features/workflow/workflow.test.tsx`
  期望：全绿（WorkbenchView 的 preview 是 ReactNode 槽，测试未断言其内部结构）。
- [ ] **提交**：
  ```bash
  cd web && git add src/features/workflow/SelectedAssetPanel.tsx src/features/workflow/SelectedAssetPanel.test.tsx "src/routes/_authed/orgs.\$org.projects.\$id.runs.\$runId.tsx"
  git commit -m "feat(studio/run): richer selected-asset panel with inline accept/reject

右栏更大预览 + 资产元数据（type/version/status）；pending_acceptance + admin 时内联采纳/拒绝，复用 useAccept/useReject（自带失效）+ hitlErrorMessage（409/429 与审核台对齐）；非 pending 维持 AssetPreviewActions。admin 门禁用 useRole 同信号。"
  ```

---

## Task 5 — 收尾（全绿 + 逐区浏览器烟雾 + finishing-a-development-branch）

**Files:** 无新增源文件；运行全量校验 + 浏览器烟雾。

> 复用既有截图 harness：`/tmp/sp-d-shots.cjs`（playwright-core from `/home/hellotalk/code/web/sentinel-web/node_modules`，系统 chrome `/usr/bin/google-chrome`，登录 demo@studio.com / demo12345，org `169278fcd0dec7d485c741215a578fab`；含 std-run / picture-book-run / project-detail 三个目标）。前置需本机 studiod :8083 + Vite :5173 在跑（见 memory: Studio dev runtime）。

### 步骤

- [ ] **全量类型检查**：`cd web && npx tsc --noEmit`
  期望：无输出（exit 0）。
- [ ] **全量 lint**：`cd web && npx eslint .`
  期望：无 error（warning 计数不增）。
- [ ] **全量单测**：`cd web && npx vitest run`
  期望：全部测试文件 PASS（含 `workflow.test`、`GraphView.test`、`studio-base.test`、`timeline-components.test`、`useProductionTimeline.test`、`PictureBookReader.test`、`AssetGalleryModal.test`、新增 `EventLog.test`/`RunSummary.test`/`SelectedAssetPanel.test`）。
- [ ] **确认 dev stack 在跑**（按需启动，见 memory「Studio dev runtime」）：studiod :8083 + Vite :5173。
  快速探活：`curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:5173`（期望 200）。
- [ ] **逐区浏览器烟雾截图**：`node /tmp/sp-d-shots.cjs`
  期望产出（逐区肉眼核对无行为回归）：
  - `/tmp/sp-d-run-std.png`（标准 run 页）：核对 **RunSummary 概览条**（runStatus + 阶段 X/N + 素材 done/total + 进度条）、**事件日志折叠**（「事件详情」+ 最新动态行）、**TimelineStage 连接线** done 实心 / pending 虚线、**S4 pip** 热区/hover、**S5 内联「去审核」CTA**、**右栏选中资产面板**（更大预览 + 元数据；admin 见采纳/拒绝）。
  - `/tmp/sp-d-run-pb.png`（绘本 run 页）：核对绘本「📖 阅读绘本」入口仍在、阅读器可开（行为不回归）。
  - `/tmp/sp-d-project-detail.png`（项目详情）：核对未受影响（旁证三栏布局未破）。
- [ ] **手动核对行为零回归清单**（在截图/浏览器中逐项确认）：
  - SSE 实时累积：运行中事件日志「最新动态」随新帧更新、展开分组实时归并。
  - 阶段点击（S2/S3）开抽屉、pip 点击切右栏预览、相册「查看全部素材」、绘本阅读器、顶栏「去审核」跳转、运行/取消/重跑——逐一仍可用。
  - 右栏 pending 资产采纳后队列/库失效（`invalidateAfterHitl`）、409（非 pending）显「该资产已被处理…」。
- [ ] **若烟雾发现回归**：用 superpowers:systematic-debugging 复现 → 定位 → 修 → 回跑对应单测 + 重截图，再继续。
- [ ] **完成开发分支**：调用 **superpowers:finishing-a-development-branch**，按其结构化选项决定合并 / PR / 清理（不擅自推送，遵 house git 纪律：仅用户要求时 push）。
- [ ] **最终提交（收尾，若有 lint/截图脚本等零散改动）**：
  ```bash
  cd web && git add -A
  git commit -m "chore(studio/run): SP-D run-page UX polish closeout — green tsc/eslint/vitest + smoke"
  ```
