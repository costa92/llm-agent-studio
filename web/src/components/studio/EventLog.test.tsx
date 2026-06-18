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
  it("sorts within group by seq even when input is shuffled", () => {
    const groups = groupByEmphasis([
      { seq: 4, kind: "todo_finished", text: "完成：script", emphasis: "S2" },
      { seq: 1, kind: "planner_started", text: "规划开始", emphasis: "S1" },
      { seq: 2, kind: "todo_ready", text: "todo_ready（script）", emphasis: "S2" },
    ])
    // groupByEmphasis 先按 seq 排序再迭代，首次出现顺序由 seq 决定（S1=seq1 先于 S2=seq2）。
    expect(groups.map((g) => g.emphasis)).toEqual(["S1", "S2"])
    expect(groups.find((g) => g.emphasis === "S2")?.lines.map((l) => l.seq)).toEqual([2, 4])
  })
  it("latestSummary returns last logFor text + count", () => {
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
    expect(screen.getByText(/最新动态/)).toBeInTheDocument()
    expect(screen.getByText("完成：script")).toBeInTheDocument()
    expect(screen.getByText(/共 2 条/)).toBeInTheDocument()
    await user.click(screen.getByText("事件详情"))
    expect(screen.getByText("规划")).toBeInTheDocument()
    expect(screen.getByText("剧本")).toBeInTheDocument()
    // 展开行用 friendlyLabel（todo_finished → 任务完成），区别于折叠态的原始文案。
    expect(screen.getByText("任务完成")).toBeInTheDocument()
  })
})
