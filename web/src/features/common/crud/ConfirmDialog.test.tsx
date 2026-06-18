import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ConfirmDialog } from "./ConfirmDialog"

describe("ConfirmDialog", () => {
  it("确认调 onConfirm、取消调 onCancel 不调 onConfirm", async () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    render(
      <ConfirmDialog open title="确认移除成员？" description="将移除 alice"
        confirmLabel="确认移除" onConfirm={onConfirm} onCancel={onCancel} />,
    )
    expect(screen.getByText("确认移除成员？")).toBeInTheDocument()
    expect(screen.getByText("将移除 alice")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "取消" }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole("button", { name: "确认移除" }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it("open=false 不渲染内容", () => {
    render(<ConfirmDialog open={false} title="X" onConfirm={() => {}} onCancel={() => {}} />)
    expect(screen.queryByText("X")).not.toBeInTheDocument()
  })
})
