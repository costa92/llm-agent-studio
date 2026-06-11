import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { TimelineStage } from "./TimelineStage"
import { PipGroup } from "./PipGroup"
import { SlateBar } from "./SlateBar"
import { LineageTrail } from "./LineageTrail"
import type { Stage } from "@/lib/timeline"

function stage(over: Partial<Stage> = {}): Stage {
  return { id: "S2", kind: "script", status: "running", linked: false, ...over }
}

describe("timeline presentational components", () => {
  it("TimelineStage renders name, sn label and tchip status", () => {
    render(<TimelineStage stage={stage({ status: "done" })} sub="ScriptAgent" />)
    expect(screen.getByText("剧本生成")).toBeInTheDocument()
    expect(screen.getByText("done")).toBeInTheDocument()
    expect(screen.getByText("ScriptAgent")).toBeInTheDocument()
  })

  it("TimelineStage marks data-status for styling/queries", () => {
    render(<TimelineStage stage={stage({ status: "failed" })} />)
    expect(document.querySelector('[data-slot="stage"]')?.getAttribute("data-status")).toBe(
      "failed",
    )
  })

  it("PipGroup renders one pip per item with its status", () => {
    render(
      <PipGroup
        pips={[
          { todoId: "a1", status: "done", assetId: "as1" },
          { todoId: "a2", status: "running" },
          { todoId: "a3", status: "idle" },
        ]}
      />,
    )
    const pips = document.querySelectorAll('[data-slot="pip"]')
    expect(pips).toHaveLength(3)
    expect(pips[0].getAttribute("data-status")).toBe("done")
    expect(pips[1].getAttribute("data-status")).toBe("running")
  })

  it("SlateBar shows only when visible", () => {
    const { rerender } = render(<SlateBar visible={false} />)
    expect(document.querySelector('[data-slot="slate-bar"]')).toBeNull()
    rerender(<SlateBar visible />)
    expect(document.querySelector('[data-slot="slate-bar"]')).not.toBeNull()
  })

  it("LineageTrail renders nodes and marks the current one", () => {
    render(
      <LineageTrail
        nodes={[
          { key: "v1", label: "v1 已退回" },
          { key: "v2", label: "v2 当前", current: true },
        ]}
      />,
    )
    expect(screen.getByText("v1 已退回")).toBeInTheDocument()
    const cur = screen.getByText("v2 当前")
    expect(cur).toHaveAttribute("data-current")
  })
})
