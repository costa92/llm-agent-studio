import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { NodeTypePicker } from "./NodeTypePicker"

// 内置类型现由 useBuiltinNodeTypes() 数据驱动；mock 返回后端目录的 3 个内置项。
vi.mock("@/features/builtin-node-types/api", () => ({
  useBuiltinNodeTypes: vi.fn(() => ({
    data: [
      { type: "script", label: "剧本", description: "" },
      { type: "storyboard", label: "分镜", description: "" },
      { type: "asset", label: "资产", description: "" },
    ],
  })),
}))

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

  it("lists typed custom types alongside annotation types (Task 13)", () => {
    render(
      <NodeTypePicker
        open
        screenX={0}
        screenY={0}
        customTypes={[
          { type: "custom:note", label: "注释", color: "#999" },
          { type: "custom:translate", label: "翻译", color: "#7c93ff", typeId: "reg-1" },
        ]}
        onPick={() => {}}
        onClose={() => {}}
      />,
    )
    expect(screen.getByText("注释")).toBeInTheDocument()
    expect(screen.getByText("翻译")).toBeInTheDocument()
    // typed 条目应有 badge
    const badge = screen.getByLabelText("typed")
    expect(badge).toBeInTheDocument()
  })

  it("picking a typed type passes typeId in display (Task 13)", () => {
    const onPick = vi.fn()
    render(
      <NodeTypePicker
        open
        screenX={0}
        screenY={0}
        customTypes={[
          { type: "custom:translate", label: "翻译", color: "#7c93ff", typeId: "reg-1" },
        ]}
        onPick={onPick}
        onClose={() => {}}
      />,
    )
    fireEvent.click(screen.getByText("翻译"))
    expect(onPick).toHaveBeenCalledWith(
      "custom:translate",
      expect.objectContaining({ typeId: "reg-1" }),
    )
  })

  it("picking an annotation custom type passes no typeId (Task 13)", () => {
    const onPick = vi.fn()
    render(
      <NodeTypePicker
        open
        screenX={0}
        screenY={0}
        customTypes={[
          { type: "custom:note", label: "注释", color: "#999" },
        ]}
        onPick={onPick}
        onClose={() => {}}
      />,
    )
    fireEvent.click(screen.getByText("注释"))
    // annotation types: no typeId key
    expect(onPick).toHaveBeenCalledWith(
      "custom:note",
      expect.not.objectContaining({ typeId: expect.anything() }),
    )
  })
})
