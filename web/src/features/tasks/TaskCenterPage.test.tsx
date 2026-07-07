import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { TaskRow } from "@/lib/types"
import { TaskCenterView } from "./TaskCenterPage"

afterEach(() => {
  vi.restoreAllMocks()
})

const NOW = Date.parse("2026-06-12T12:00:00Z")

function makeRow(over: Partial<TaskRow> = {}): TaskRow {
  return {
    projectId: "p1",
    name: "猫的冒险",
    status: "running",
    progressDone: 3,
    progressTotal: 5,
    pendingReview: 0,
    failed: false,
    failingAgent: "",
    lastActivityAt: "2026-06-12T11:58:00Z",
    ...over,
  }
}

const COUNTS = { all: 4, running: 1, review: 1, completed: 1, failed: 1, draft: 0 }

function baseProps() {
  return {
    counts: COUNTS,
    isLoading: false,
    isError: false,
    onRetry: vi.fn(),
    onAction: vi.fn(),
    now: NOW,
  }
}

describe("TaskCenterView", () => {
  it("renders the header and tab counts", () => {
    render(<TaskCenterView {...baseProps()} rows={[makeRow()]} />)
    expect(screen.getByText("任务中心")).toBeInTheDocument()
    expect(screen.getByText("项目运行看板")).toBeInTheDocument()
    // 运行中 tab 旁显示 count 1。
    const runningTab = screen.getByRole("tab", { name: /运行中/ })
    expect(within(runningTab).getByText("1")).toBeInTheDocument()
  })

  it("renders a row with status badge, percent and relative time", () => {
    render(<TaskCenterView {...baseProps()} rows={[makeRow()]} />)
    expect(screen.getByText("猫的冒险")).toBeInTheDocument()
    expect(screen.getByText("生产中")).toBeInTheDocument()
    expect(screen.getByText("60%")).toBeInTheDocument()
    expect(screen.getByText("2分钟前")).toBeInTheDocument()
  })

  it("filters rows by the selected tab", async () => {
    const user = userEvent.setup()
    render(
      <TaskCenterView
        {...baseProps()}
        rows={[
          makeRow({ projectId: "p1", name: "猫的冒险", status: "running" }),
          makeRow({ projectId: "p2", name: "产品定义", status: "review" }),
        ]}
      />,
    )
    // 全部：两行都在。
    expect(screen.getByText("猫的冒险")).toBeInTheDocument()
    expect(screen.getByText("产品定义")).toBeInTheDocument()
    // 切到待审核：只剩 review 行。
    await user.click(screen.getByRole("tab", { name: /待审核/ }))
    expect(screen.queryByText("猫的冒险")).not.toBeInTheDocument()
    expect(screen.getByText("产品定义")).toBeInTheDocument()
  })

  it("surfaces 生成完成 as a signal separate from the review-gated 已交付 bucket", async () => {
    const user = userEvent.setup()
    render(
      <TaskCenterView
        {...baseProps()}
        rows={[
          // 进度打满但卡在审核（status=review）——「完成/已交付」桶恒为 0 会埋没它。
          makeRow({
            projectId: "p1",
            name: "生成完毕待审",
            status: "review",
            progressDone: 5,
            progressTotal: 5,
          }),
          makeRow({
            projectId: "p2",
            name: "还在跑",
            status: "running",
            progressDone: 2,
            progressTotal: 5,
          }),
        ]}
      />,
    )
    // 生成完成 tab 计数从 rows 现算 = 1（不依赖后端恒 0 的 completed 计数）。
    const genTab = screen.getByRole("tab", { name: /生成完成/ })
    expect(within(genTab).getByText("1")).toBeInTheDocument()
    // 切到生成完成：只剩进度打满那行，无关它是否已过审。
    await user.click(genTab)
    expect(screen.getByText("生成完毕待审")).toBeInTheDocument()
    expect(screen.queryByText("还在跑")).not.toBeInTheDocument()
  })

  it("fires onAction with the clicked row", async () => {
    const user = userEvent.setup()
    const onAction = vi.fn()
    const row = makeRow({ status: "review", projectId: "p2", name: "产品定义" })
    render(<TaskCenterView {...baseProps()} rows={[row]} onAction={onAction} />)
    await user.click(screen.getByRole("button", { name: /去审核/ }))
    expect(onAction).toHaveBeenCalledTimes(1)
    expect(onAction).toHaveBeenCalledWith(row)
  })

  it("shows the failing agent for a failed row instead of progress", () => {
    render(
      <TaskCenterView
        {...baseProps()}
        rows={[makeRow({ status: "failed", failed: true, failingAgent: "ScriptAgent" })]}
      />,
    )
    expect(screen.getByText("ScriptAgent 出错")).toBeInTheDocument()
  })

  it("forces 5 filled pips and 100% for a completed row", () => {
    const { container } = render(
      <TaskCenterView
        {...baseProps()}
        rows={[makeRow({ status: "completed", progressDone: 0, progressTotal: 0 })]}
      />,
    )
    expect(screen.getByText("100%")).toBeInTheDocument()
    const filled = container.querySelectorAll('[data-slot="task-pip"][data-filled="true"]')
    expect(filled).toHaveLength(5)
  })

  it("renders loading skeletons", () => {
    const { container } = render(
      <TaskCenterView {...baseProps()} rows={undefined} isLoading />,
    )
    expect(container.querySelectorAll('[data-slot="skeleton"]').length).toBe(5)
  })

  it("renders the empty state when there are no tasks", () => {
    render(<TaskCenterView {...baseProps()} rows={[]} />)
    expect(screen.getByText("暂无任务")).toBeInTheDocument()
  })

  it("renders the error state with retry", async () => {
    const user = userEvent.setup()
    const onRetry = vi.fn()
    render(<TaskCenterView {...baseProps()} rows={undefined} isError onRetry={onRetry} />)
    await user.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })
})
