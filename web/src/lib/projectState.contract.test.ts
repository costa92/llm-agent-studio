import { describe, it, expect } from "vitest"
import type { StageRole, StageStatus2, RunStatus2, PipStatus2 } from "./projectState"

// 守护前后端枚举漂移:任一侧新增/改名而另一侧没跟 → 这里需同步更新,否则编译/断言红。
// 后端真相:internal/projectstate/state.go 的注释枚举域 + Compute 分支。
describe("ProjectState 枚举契约(与后端 projectstate 对齐)", () => {
  it("StageRole 恰为 5 个语义角色", () => {
    const roles: StageRole[] = ["planner", "script", "storyboard", "asset", "review"]
    expect(roles).toHaveLength(5)
  })
  it("StageStatus2 恰为 5 态", () => {
    const s: StageStatus2[] = ["blocked", "pending", "running", "done", "failed"]
    expect(s).toHaveLength(5)
  })
  it("RunStatus2 恰为 3 态", () => {
    const s: RunStatus2[] = ["idle", "running", "done"]
    expect(s).toHaveLength(3)
  })
  it("PipStatus2 恰为 4 态", () => {
    const s: PipStatus2[] = ["idle", "running", "done", "failed"]
    expect(s).toHaveLength(4)
  })
})
