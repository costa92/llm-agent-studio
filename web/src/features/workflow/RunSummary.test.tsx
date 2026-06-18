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
    projectId: "p1",
    version: 1,
    status: "running",
    runStatus: "running",
    stages: stages(),
    pips: [],
    assets: { total: 4, done: 1, pending: 0 },
    nodes: [],
    edges: [],
    isCustom: false,
    ...over,
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
  it("uses node count for isCustom workflows even when stages are present", () => {
    const s = computeRunSummary(makeState({
      isCustom: true,
      // 后端对有 plan 的自定义工作流同时下发 stages（5 段）与 nodes——必须用 nodes。
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
    expect(
      computeRunSummary(makeState({ runStatus: "done", status: "review" })).runLabel,
    ).toBe("已完成")
    expect(
      computeRunSummary(makeState({ runStatus: "idle", status: "draft" })).runLabel,
    ).toBe("空闲")
    const failed = computeRunSummary(makeState({ runStatus: "done", status: "failed" }))
    expect(failed.runLabel).toBe("失败")
    expect(failed.variant).toBe("rejected")
    const canceled = computeRunSummary(makeState({ runStatus: "done", status: "canceled" }))
    expect(canceled.runLabel).toBe("已取消")
    expect(canceled.variant).toBe("rejected")
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
