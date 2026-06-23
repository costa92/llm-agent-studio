import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { CanvasContextMenu } from "./CanvasContextMenu"

describe("CanvasContextMenu", () => {
  it("renders nothing when closed", () => {
    const { container } = render(
      <CanvasContextMenu open={false} screenX={0} screenY={0} items={[]} onClose={() => {}} />,
    )
    expect(container.querySelector('[data-slot="canvas-context-menu"]')).toBeNull()
  })

  it("renders one button per item and fires onClick + onClose", () => {
    const onClick = vi.fn()
    const onClose = vi.fn()
    render(
      <CanvasContextMenu
        open
        screenX={10}
        screenY={20}
        items={[{ label: "删除", onClick, danger: true }]}
        onClose={onClose}
      />,
    )
    fireEvent.click(screen.getByRole("menuitem", { name: "删除" }))
    expect(onClick).toHaveBeenCalledOnce()
    expect(onClose).toHaveBeenCalledOnce()
  })

  it("disabled item does not fire onClick", () => {
    const onClick = vi.fn()
    render(
      <CanvasContextMenu
        open
        screenX={0}
        screenY={0}
        items={[{ label: "粘贴", onClick, disabled: true }]}
        onClose={() => {}}
      />,
    )
    fireEvent.click(screen.getByRole("menuitem", { name: "粘贴" }))
    expect(onClick).not.toHaveBeenCalled()
  })

  it("clicking the overlay calls onClose", () => {
    const onClose = vi.fn()
    render(
      <CanvasContextMenu open screenX={0} screenY={0} items={[]} onClose={onClose} />,
    )
    fireEvent.click(screen.getByTestId("context-menu-overlay"))
    expect(onClose).toHaveBeenCalledOnce()
  })
})
