import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { NodeTypePicker } from "./NodeTypePicker"

describe("NodeTypePicker", () => {
  it("renders 3 rows when open", () => {
    render(
      <NodeTypePicker
        open
        screenX={10}
        screenY={20}
        onPick={() => {}}
        onClose={() => {}}
      />,
    )
    expect(screen.getByText("剧本")).toBeInTheDocument()
    expect(screen.getByText("分镜")).toBeInTheDocument()
    expect(screen.getByText("资产")).toBeInTheDocument()
  })

  it("calls onPick with the type when a row is clicked", () => {
    const onPick = vi.fn()
    render(
      <NodeTypePicker
        open
        screenX={0}
        screenY={0}
        onPick={onPick}
        onClose={() => {}}
      />,
    )
    fireEvent.click(screen.getByText("剧本"))
    expect(onPick).toHaveBeenCalledWith("script")
  })

  it("calls onClose when the overlay is clicked", () => {
    const onClose = vi.fn()
    const { container } = render(
      <NodeTypePicker
        open
        screenX={0}
        screenY={0}
        onPick={() => {}}
        onClose={onClose}
      />,
    )
    const overlay = container.querySelector('[data-slot="picker-overlay"]')!
    fireEvent.click(overlay)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it("renders nothing when closed", () => {
    const { container } = render(
      <NodeTypePicker
        open={false}
        screenX={0}
        screenY={0}
        onPick={() => {}}
        onClose={() => {}}
      />,
    )
    expect(container.firstChild).toBeNull()
  })
})
