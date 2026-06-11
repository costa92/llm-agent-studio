import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { WorkbenchView } from "./WorkbenchPage"
import { ScriptView } from "./ScriptView"
import { StoryboardView } from "./StoryboardView"
import type { ScriptDoc, Shot } from "./api"
import { foldEvents, initialTimeline } from "@/lib/timeline"
import type { SseFrame } from "@/lib/types"
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

function frame(seq: number, kind: string, todoId = "", payload?: unknown): SseFrame {
  return { seq, kind, todoId, payload }
}

function baseWorkbenchProps() {
  return {
    project: makeProject(),
    conn: "connected" as const,
    live: true,
    canRun: true,
    onRun: vi.fn(),
    onCancel: vi.fn(),
    isRunning: false,
  }
}

describe("WorkbenchView (production timeline)", () => {
  it("renders the 5 fixed stages and advances as events fold in", () => {
    // 回放一段事件：规划开始 → 剧本就绪/开始/完成。
    const state = foldEvents(initialTimeline(), [
      frame(1, "planner_started"),
      frame(2, "todo_ready", "t-s", { type: "script" }),
      frame(3, "todo_started", "t-s", { type: "script" }),
      frame(4, "todo_finished", "t-s", { type: "script" }),
    ])

    render(<WorkbenchView {...baseWorkbenchProps()} timeline={state} />)

    // 5 个固定阶段标题。
    expect(screen.getByText("Planner 规划")).toBeInTheDocument()
    expect(screen.getByText("剧本生成")).toBeInTheDocument()
    expect(screen.getByText("分镜拆解")).toBeInTheDocument()
    expect(screen.getByText("素材生成")).toBeInTheDocument()
    expect(screen.getByText("人工审核")).toBeInTheDocument()

    // S1 running（planner_started）、S2 done（todo_finished script）。
    const stages = document.querySelectorAll('[data-slot="stage"]')
    expect(stages[0].getAttribute("data-status")).toBe("running")
    expect(stages[1].getAttribute("data-status")).toBe("done")
    // SSE 指示器在线。
    expect(screen.getByText("实时连接")).toBeInTheDocument()
  })

  it("renders the asset pip group with done/N count when storyboard fans out", () => {
    const state = foldEvents(initialTimeline(), [
      frame(1, "planner_started"),
      frame(10, "todo_ready", "a1", { type: "asset" }),
      frame(11, "todo_ready", "a2", { type: "asset" }),
      frame(12, "todo_started", "a1", { type: "asset" }),
      frame(13, "asset_generated", "a1", { assetId: "as1" }),
    ])

    render(<WorkbenchView {...baseWorkbenchProps()} timeline={state} />)

    // pip 组：2 个 pip（a1 done / a2 idle）。
    const pips = document.querySelectorAll('[data-slot="pip"]')
    expect(pips).toHaveLength(2)
    expect(screen.getByText("素材生成 · 1/2")).toBeInTheDocument()
  })

  it("shows 待审核·N badge and hides slate bar after run_done", () => {
    const state = foldEvents(initialTimeline(), [
      frame(1, "planner_started"),
      frame(10, "todo_ready", "a1", { type: "asset" }),
      frame(12, "todo_started", "a1", { type: "asset" }),
      frame(13, "asset_generated", "a1", { assetId: "as1" }),
      frame(99, "run_done"),
    ])

    render(<WorkbenchView {...baseWorkbenchProps()} timeline={state} />)

    expect(screen.getByText("待审核 · 1")).toBeInTheDocument()
    // SlateBar 隐藏（run_done → slateVisible false）。
    expect(document.querySelector('[data-slot="slate-bar"]')).toBeNull()
  })

  it("shows the fallback WarnStrip when fallbackUsed", () => {
    render(
      <WorkbenchView
        {...baseWorkbenchProps()}
        timeline={initialTimeline()}
        fallbackUsed
      />,
    )
    expect(screen.getByRole("status")).toHaveTextContent("回落默认管线")
  })

  it("hides run/cancel controls for viewers (canRun=false) and fires onRun for editors", async () => {
    const onRun = vi.fn()
    const user = userEvent.setup()
    const { rerender } = render(
      <WorkbenchView
        {...baseWorkbenchProps()}
        timeline={initialTimeline()}
        canRun={false}
      />,
    )
    expect(screen.queryByRole("button", { name: /运行/ })).not.toBeInTheDocument()

    rerender(
      <WorkbenchView
        {...baseWorkbenchProps()}
        timeline={initialTimeline()}
        canRun
        onRun={onRun}
      />,
    )
    await user.click(screen.getByRole("button", { name: /运行/ }))
    expect(onRun).toHaveBeenCalledTimes(1)
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
