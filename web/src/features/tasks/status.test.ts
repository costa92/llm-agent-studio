import { describe, expect, it } from "vitest"
import type { TaskRow } from "@/lib/types"
import { quickAction, taskBucket } from "./status"

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
    lastActivityAt: "2026-06-12T00:00:00Z",
    ...over,
  }
}

describe("taskBucket", () => {
  it("maps planning and running to 运行中", () => {
    expect(taskBucket("planning")).toBe("运行中")
    expect(taskBucket("running")).toBe("运行中")
  })
  it("maps review/completed/failed/draft/canceled to their buckets", () => {
    expect(taskBucket("review")).toBe("待审核")
    expect(taskBucket("completed")).toBe("完成")
    expect(taskBucket("failed")).toBe("失败")
    expect(taskBucket("draft")).toBe("草稿")
    expect(taskBucket("canceled")).toBe("已取消")
  })
})

describe("quickAction", () => {
  it("routes review to the review page filtered by project (?project=)", () => {
    const action = quickAction(makeRow({ status: "review" }), "acme")
    expect(action.label).toBe("去审核")
    expect(action.to).toBe("/orgs/$org/review")
    expect(action.params).toEqual({ org: "acme" })
    expect(action.search).toEqual({ project: "p1" })
  })

  it("routes failed to the project workbench (查看)", () => {
    const action = quickAction(makeRow({ status: "failed", failed: true }), "acme")
    expect(action.label).toBe("查看")
    expect(action.to).toBe("/orgs/$org/projects/$id")
    expect(action.params).toEqual({ org: "acme", id: "p1" })
    expect(action.search).toBeUndefined()
  })

  it("routes running/completed/draft to the project workbench", () => {
    for (const status of ["running", "planning", "completed", "draft", "canceled"]) {
      const action = quickAction(makeRow({ status }), "acme")
      expect(action.to).toBe("/orgs/$org/projects/$id")
      expect(action.params).toEqual({ org: "acme", id: "p1" })
    }
  })
})
