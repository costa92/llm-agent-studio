import { describe, expect, it } from "vitest"
import { markActiveEdges } from "./runEdges"
import type { RunNodeStatus } from "./runOverlay"
import type { RFEdge } from "./canvasModel"

function overlayOf(entries: Record<string, RunNodeStatus["status"]>): Map<string, RunNodeStatus> {
  const m = new Map<string, RunNodeStatus>()
  for (const [id, status] of Object.entries(entries)) m.set(id, { status })
  return m
}

const EDGES: RFEdge[] = [
  { id: "script-1->storyboard-1", source: "script-1", target: "storyboard-1", type: "studio" },
  { id: "storyboard-1->asset-1", source: "storyboard-1", target: "asset-1", type: "studio" },
]

describe("markActiveEdges", () => {
  it("活动边：上游 done & 下游 running → active=true", () => {
    const overlay = overlayOf({ "script-1": "done", "storyboard-1": "running", "asset-1": "pending" })
    const out = markActiveEdges(EDGES, overlay)
    expect(out[0].data?.active).toBe(true) // script(done) → storyboard(running)
    expect(out[1].data?.active).toBe(false) // storyboard(running) → asset(pending)
  })

  it("非活动：上游未 done（如 running→running、done→done）", () => {
    expect(
      markActiveEdges(EDGES, overlayOf({ "script-1": "running", "storyboard-1": "running" }))[0].data
        ?.active,
    ).toBe(false)
    expect(
      markActiveEdges(EDGES, overlayOf({ "script-1": "done", "storyboard-1": "done" }))[0].data
        ?.active,
    ).toBe(false)
  })

  it("端点未命中 overlay（pending/未匹配）→ 非活动，不抛", () => {
    const out = markActiveEdges(EDGES, overlayOf({ "script-1": "done" }))
    expect(out[0].data?.active).toBe(false) // target storyboard-1 缺失
  })

  it("enabled=false（顶栏关流动）→ 全部非活动", () => {
    const overlay = overlayOf({ "script-1": "done", "storyboard-1": "running" })
    const out = markActiveEdges(EDGES, overlay, false)
    expect(out.every((e) => e.data?.active === false)).toBe(true)
  })

  it("保留原边其它字段，仅注入 data.active（不破坏既有 data）", () => {
    const withData: RFEdge[] = [
      { id: "a->b", source: "a", target: "b", type: "studio", data: { foo: 1 } },
    ]
    const out = markActiveEdges(withData, overlayOf({ a: "done", b: "running" }))
    expect(out[0].data).toEqual({ foo: 1, active: true })
    expect(out[0].id).toBe("a->b")
    expect(out[0].type).toBe("studio")
  })
})
