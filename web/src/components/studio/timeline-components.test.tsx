import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { LineageTrail } from "./LineageTrail"

describe("timeline presentational components", () => {
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
