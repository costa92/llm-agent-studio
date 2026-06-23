import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { CustomTypeDialog } from "./CustomTypeDialog"

describe("CustomTypeDialog", () => {
  it("submits name + first palette color for a new type", () => {
    const onSubmit = vi.fn()
    render(<CustomTypeDialog open mode="create" onSubmit={onSubmit} onCancel={() => {}} />)
    fireEvent.change(screen.getByLabelText("显示名"), { target: { value: "翻译" } })
    fireEvent.click(screen.getByRole("button", { name: "确认" }))
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ label: "翻译" }))
    expect(onSubmit.mock.calls[0][0].color).toMatch(/^#/)
  })

  it("disables 确认 when name is empty", () => {
    render(<CustomTypeDialog open mode="create" onSubmit={() => {}} onCancel={() => {}} />)
    expect(screen.getByRole("button", { name: "确认" })).toBeDisabled()
  })

  it("prefills name+color in edit mode", () => {
    render(
      <CustomTypeDialog open mode="edit" initial={{ label: "旧名", color: "#22b8a6" }} onSubmit={() => {}} onCancel={() => {}} />,
    )
    expect((screen.getByLabelText("显示名") as HTMLInputElement).value).toBe("旧名")
  })
})
