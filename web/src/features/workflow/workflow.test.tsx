import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { WorkbenchView } from "./WorkbenchPage"
import { ScriptView } from "./ScriptView"
import { StoryboardView } from "./StoryboardView"
import type { ScriptDoc, Shot } from "./api"
import type { ProjectState, StageState, PipState } from "@/lib/projectState"
import type { LogLine } from "@/lib/timeline"
import type { Project } from "@/lib/types"

function makeProject(over: Partial<Project> = {}): Project {
  return {
    id: "p1",
    orgId: "acme",
    name: "国风茶饮宣传短片",
    description: "为新中式茶饮品牌做一支 30 秒宣传短片",
    contentType: "短视频",
    targetPlatform: "抖音",
    style: "国风",
    status: "running",
    createdBy: "u1",
    ...over,
  }
}

// 全 blocked 的 5 阶段（按权威语义 role 顺序）。
function blockedStages(): StageState[] {
  return [
    { role: "planner", status: "blocked" },
    { role: "script", status: "blocked" },
    { role: "storyboard", status: "blocked" },
    { role: "asset", status: "blocked" },
    { role: "review", status: "blocked" },
  ]
}

function makeState(over: Partial<ProjectState> = {}): ProjectState {
  return {
    projectId: "p1",
    version: 1,
    status: "running",
    runStatus: "running",
    stages: blockedStages(),
    pips: [],
    assets: { total: 0, done: 0, pending: 0 },
    nodes: [],
    edges: [],
    isCustom: false,
    ...over,
  }
}

function baseWorkbenchProps() {
  return {
    project: makeProject(),
    log: [] as LogLine[],
    conn: "connected" as const,
    live: true,
    canRun: true,
    onRun: vi.fn(),
    onCancel: vi.fn(),
    isRunning: false,
  }
}

describe("WorkbenchView (authoritative ProjectState)", () => {
  it("renders the 5 fixed stages and reflects authoritative statuses", () => {
    const state = makeState({
      stages: [
        { role: "planner", status: "done" },
        { role: "script", status: "done", todoId: "t-s" },
        { role: "storyboard", status: "blocked" },
        { role: "asset", status: "blocked" },
        { role: "review", status: "blocked" },
      ],
    })

    render(<WorkbenchView {...baseWorkbenchProps()} state={state} />)

    // 5 个固定阶段标题。
    expect(screen.getByText("Planner 规划")).toBeInTheDocument()
    expect(screen.getByText("剧本生成")).toBeInTheDocument()
    expect(screen.getByText("分镜拆解")).toBeInTheDocument()
    expect(screen.getByText("素材生成")).toBeInTheDocument()
    expect(screen.getByText("人工审核")).toBeInTheDocument()

    // S1/S2 done（来自权威 state）。
    const stages = document.querySelectorAll('[data-slot="stage"]')
    expect(stages[0].getAttribute("data-status")).toBe("done")
    expect(stages[1].getAttribute("data-status")).toBe("done")
    // SSE 指示器在线。
    expect(screen.getByText("实时连接")).toBeInTheDocument()
  })

  it("renders the asset pip group with done/N count from assets tally", () => {
    const pips: PipState[] = [
      { todoId: "a1", status: "done", assetId: "as1" },
      { todoId: "a2", status: "idle" },
    ]
    const state = makeState({
      stages: [
        { role: "planner", status: "done" },
        { role: "script", status: "done" },
        { role: "storyboard", status: "done" },
        { role: "asset", status: "running" },
        { role: "review", status: "blocked" },
      ],
      pips,
      assets: { total: 2, done: 1, pending: 1 },
    })

    render(<WorkbenchView {...baseWorkbenchProps()} state={state} />)

    const pipEls = document.querySelectorAll('[data-slot="pip"]')
    expect(pipEls).toHaveLength(2)
    expect(screen.getByText("素材生成 · 1/2")).toBeInTheDocument()
  })

  it("shows 待审核·N badge when runStatus done and review", () => {
    const state = makeState({
      status: "review",
      runStatus: "done",
      pips: [{ todoId: "a1", status: "done", assetId: "as1" }],
      assets: { total: 1, done: 1, pending: 1 },
    })

    render(<WorkbenchView {...baseWorkbenchProps()} state={state} live={false} />)

    expect(screen.getByText("待审核 · 1")).toBeInTheDocument()
    // SlateBar 隐藏（runStatus done → slateVisible false）。
    expect(document.querySelector('[data-slot="slate-bar"]')).toBeNull()
  })

  it("shows the fallback WarnStrip when fallbackUsed", () => {
    render(
      <WorkbenchView {...baseWorkbenchProps()} state={makeState()} fallbackUsed />,
    )
    expect(screen.getByRole("status")).toHaveTextContent("回落默认管线")
  })

  it("hides run/cancel controls for viewers (canRun=false) and fires onRun for editors", async () => {
    const onRun = vi.fn()
    const user = userEvent.setup()
    const { rerender } = render(
      <WorkbenchView {...baseWorkbenchProps()} state={makeState()} canRun={false} />,
    )
    expect(screen.queryByRole("button", { name: /运行/ })).not.toBeInTheDocument()

    rerender(
      <WorkbenchView {...baseWorkbenchProps()} state={makeState()} canRun onRun={onRun} />,
    )
    await user.click(screen.getByRole("button", { name: /运行/ }))
    expect(onRun).toHaveBeenCalledTimes(1)
  })

  // T3：S2/S3 阶段可点（按钮语义），点击回调 onSelectStage(stageId)；S1/S4/S5 不伪装可点。
  it("makes S2/S3 stages clickable and fires onSelectStage; S1 stays non-interactive", async () => {
    const onSelectStage = vi.fn()
    const user = userEvent.setup()
    const state = makeState({
      stages: [
        { role: "planner", status: "done" },
        { role: "script", status: "done" },
        { role: "storyboard", status: "done" },
        { role: "asset", status: "blocked" },
        { role: "review", status: "blocked" },
      ],
    })
    render(
      <WorkbenchView {...baseWorkbenchProps()} state={state} onSelectStage={onSelectStage} />,
    )
    const scriptStage = screen.getByRole("button", { name: /剧本生成/ })
    await user.click(scriptStage)
    expect(onSelectStage).toHaveBeenCalledWith("S2")

    const boardStage = screen.getByRole("button", { name: /分镜拆解/ })
    await user.click(boardStage)
    expect(onSelectStage).toHaveBeenCalledWith("S3")

    // S1 Planner 规划 不是按钮（不伪装可点）。
    expect(
      screen.queryByRole("button", { name: /Planner 规划/ }),
    ).not.toBeInTheDocument()
  })

  // T3：点击已完成的 pip → onSelectPip(pip)。
  it("fires onSelectPip when a done pip is clicked", async () => {
    const onSelectPip = vi.fn()
    const user = userEvent.setup()
    const state = makeState({
      stages: [
        { role: "planner", status: "done" },
        { role: "script", status: "done" },
        { role: "storyboard", status: "done" },
        { role: "asset", status: "running" },
        { role: "review", status: "blocked" },
      ],
      pips: [{ todoId: "a1", status: "done", assetId: "as1" }],
      assets: { total: 1, done: 1, pending: 1 },
    })
    render(
      <WorkbenchView {...baseWorkbenchProps()} state={state} onSelectPip={onSelectPip} />,
    )
    const pip = screen.getByRole("button", { name: /a1/ })
    await user.click(pip)
    expect(onSelectPip).toHaveBeenCalledWith(
      expect.objectContaining({ todoId: "a1", assetId: "as1", status: "done" }),
    )
  })

  // T3：drawer slot 渲染在布局内（SSE/轨道保留挂载）。
  it("renders the drawer slot content (in-workbench inspection)", () => {
    render(
      <WorkbenchView
        {...baseWorkbenchProps()}
        state={makeState()}
        drawer={<div>剧本抽屉内容</div>}
      />,
    )
    expect(screen.getByText("剧本抽屉内容")).toBeInTheDocument()
  })

  // T2：run_done 后徽标旁出现「去审核」CTA，点击触发 onOpenReview。
  it("renders a review CTA after run done that fires onOpenReview", async () => {
    const onOpenReview = vi.fn()
    const user = userEvent.setup()
    const state = makeState({
      status: "review",
      runStatus: "done",
      pips: [{ todoId: "a1", status: "done", assetId: "as1" }],
      assets: { total: 1, done: 1, pending: 1 },
    })
    render(
      <WorkbenchView
        {...baseWorkbenchProps()}
        state={state}
        live={false}
        onOpenReview={onOpenReview}
      />,
    )
    await user.click(screen.getByRole("button", { name: /去审核/ }))
    expect(onOpenReview).toHaveBeenCalledTimes(1)
  })

  // 失败态徽标：status='failed' + runStatus done 时，绝不能显示「待审核 · 0」。
  it("shows the 失败 badge (rejected) when status is failed, NOT 待审核 · 0", () => {
    const state = makeState({
      status: "failed",
      runStatus: "done",
      pips: [{ todoId: "a1", status: "failed" }],
      assets: { total: 1, done: 0, pending: 0 },
      error: { todoId: "a1", role: "asset", message: "boom" },
    })
    render(
      <WorkbenchView
        project={makeProject({ status: "failed" })}
        state={state}
        log={[]}
        conn="connected"
        live={false}
        canRun={false}
        onRun={vi.fn()}
        onCancel={vi.fn()}
        isRunning={false}
      />,
    )
    // RunSummary 也渲染「失败」徽标（与 header 各一个）；getAllByText 允许多个。
    expect(screen.getAllByText("失败").length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/待审核/)).not.toBeInTheDocument()
  })

  // 错误条：state.error.message 必须在工作台显眼位置（红色条）出现。
  it("renders an error strip with state.error.message", () => {
    const errMsg = "worker: blob put: blob.github: get sha: Get ...: EOF"
    const state = makeState({
      status: "failed",
      runStatus: "done",
      error: { todoId: "a1", role: "asset", message: errMsg },
    })
    render(
      <WorkbenchView
        project={makeProject({ status: "failed" })}
        state={state}
        log={[]}
        conn="connected"
        live={false}
        canRun={false}
        onRun={vi.fn()}
        onCancel={vi.fn()}
        isRunning={false}
      />,
    )
    const alert = screen.getByRole("alert")
    expect(alert).toHaveTextContent(errMsg)
  })

  it("isCustom=true 渲染 GraphView 而非 5 段轨道", () => {
    render(
      <WorkbenchView
        {...baseWorkbenchProps()}
        state={makeState({
          isCustom: true,
          nodes: [{ id: "a", label: "剧本生成 #1", type: "script", status: "done" }],
          edges: [],
        })}
      />,
    )
    expect(document.querySelector('[data-slot="graph"]')).not.toBeNull()
    expect(document.querySelector('[data-slot="stage"]')).toBeNull()
  })

  it("isCustom=false 渲染 5 段轨道而非 GraphView", () => {
    render(<WorkbenchView {...baseWorkbenchProps()} state={makeState({ isCustom: false })} />)
    expect(document.querySelector('[data-slot="stage"]')).not.toBeNull()
    expect(document.querySelector('[data-slot="graph"]')).toBeNull()
  })
})

describe("ScriptView", () => {
  it("renders empty state when no script", () => {
    render(<ScriptView script={null} isLoading={false} isError={false} />)
    expect(screen.getByText("剧本尚未生成")).toBeInTheDocument()
  })

  it("renders error state on malformed/failed load", () => {
    render(<ScriptView script={undefined} isLoading={false} isError />)
    expect(
      screen.getByText("剧本数据异常，请重新运行剧本阶段"),
    ).toBeInTheDocument()
  })

  it("renders title, logline and scenes", () => {
    const script: ScriptDoc = {
      title: "茶馆黄昏",
      logline: "少女与老茶师的传承",
      scenes: [
        { heading: "场景一 · 茶馆内", description: "黄昏光线洒入", dialogue: "老茶师：慢些。" },
      ],
    }
    render(<ScriptView script={script} isLoading={false} isError={false} />)
    expect(screen.getByText("茶馆黄昏")).toBeInTheDocument()
    expect(screen.getByText("少女与老茶师的传承")).toBeInTheDocument()
    expect(screen.getByText("场景一 · 茶馆内")).toBeInTheDocument()
    expect(screen.getByText("黄昏光线洒入")).toBeInTheDocument()
  })
})

describe("StoryboardView", () => {
  it("renders empty state when no shots", () => {
    render(<StoryboardView shots={[]} isLoading={false} isError={false} />)
    expect(screen.getByText("分镜尚未拆解")).toBeInTheDocument()
  })

  it("renders the shot grid with shotNo / action / prompt", () => {
    const shots: Shot[] = [
      { shotNo: 1, camera: "中景·推", action: "茶馆内黄昏", prompt: "guofeng, dusk teahouse" },
      { shotNo: 2, camera: "特写", action: "茶汤特写", prompt: "close up tea" },
    ]
    render(<StoryboardView shots={shots} isLoading={false} isError={false} />)
    expect(screen.getByText("#1")).toBeInTheDocument()
    expect(screen.getByText("茶馆内黄昏")).toBeInTheDocument()
    expect(screen.getByText("guofeng, dusk teahouse")).toBeInTheDocument()
    expect(screen.getByText("#2")).toBeInTheDocument()
  })
})
