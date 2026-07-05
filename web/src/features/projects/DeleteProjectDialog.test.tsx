import { describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { DeleteProjectDialog } from "./DeleteProjectDialog"

const project = { id: "p1", name: "宣传片A" }

function setup(over: { onSubmit?: () => Promise<unknown> } = {}) {
  const onSubmit = vi.fn(over.onSubmit ?? (() => Promise.resolve()))
  const onSuccess = vi.fn()
  render(
    <DeleteProjectDialog
      project={project}
      onSubmit={onSubmit}
      onSuccess={onSuccess}
      trigger={<button type="button">删除</button>}
    />,
  )
  return { onSubmit, onSuccess }
}

describe("DeleteProjectDialog", () => {
  it("keeps 确认删除 disabled until the typed name matches exactly", async () => {
    const { onSubmit } = setup()
    const user = userEvent.setup()

    await user.click(screen.getByRole("button", { name: "删除" }))
    const confirm = screen.getByRole("button", { name: "确认删除" })
    expect(confirm).toBeDisabled()

    // 部分/错误输入仍禁用（必须逐字匹配）。
    const input = screen.getByLabelText(/输入项目名称/)
    await user.type(input, "宣传片")
    expect(confirm).toBeDisabled()
    // disabled 按钮点击不触发提交。
    await user.click(confirm)
    expect(onSubmit).not.toHaveBeenCalled()

    await user.type(input, "A")
    expect(confirm).toBeEnabled()
  })

  it("submits on exact match and invokes onSuccess after the delete resolves", async () => {
    const { onSubmit, onSuccess } = setup()
    const user = userEvent.setup()

    await user.click(screen.getByRole("button", { name: "删除" }))
    await user.type(screen.getByLabelText(/输入项目名称/), "宣传片A")
    await user.click(screen.getByRole("button", { name: "确认删除" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    // 成功后弹窗关闭。
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "确认删除" })).not.toBeInTheDocument(),
    )
  })

  it("shows an inline error and keeps the dialog open when the delete fails", async () => {
    const { onSuccess } = setup({
      onSubmit: () => Promise.reject(new Error("boom")),
    })
    const user = userEvent.setup()

    await user.click(screen.getByRole("button", { name: "删除" }))
    await user.type(screen.getByLabelText(/输入项目名称/), "宣传片A")
    await user.click(screen.getByRole("button", { name: "确认删除" }))

    expect(await screen.findByRole("alert")).toHaveTextContent("删除失败，请重试")
    expect(onSuccess).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: "确认删除" })).toBeInTheDocument()
  })

  it("resets the typed name when the dialog is reopened", async () => {
    setup()
    const user = userEvent.setup()

    await user.click(screen.getByRole("button", { name: "删除" }))
    await user.type(screen.getByLabelText(/输入项目名称/), "宣传片A")
    await user.click(screen.getByRole("button", { name: "取消" }))

    await user.click(screen.getByRole("button", { name: "删除" }))
    expect(screen.getByLabelText(/输入项目名称/)).toHaveValue("")
    expect(screen.getByRole("button", { name: "确认删除" })).toBeDisabled()
  })
})
