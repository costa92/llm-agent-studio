import { describe, it, expect, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useFormContext } from "react-hook-form"
import { z } from "zod"
import { FormDialog } from "./FormDialog"

const schema = z.object({ name: z.string().min(1, "必填") })

function NameField() {
  const { register, formState } = useFormContext<{ name: string }>()
  return (
    <div>
      <input aria-label="名称" {...register("name")} />
      {formState.errors.name && <span>{formState.errors.name.message}</span>}
    </div>
  )
}

describe("FormDialog", () => {
  it("编辑模式预填 defaultValues，提交回调拿到值", async () => {
    const onSubmit = vi.fn()
    render(
      <FormDialog open mode="edit" title="编辑提示词" schema={schema}
        defaultValues={{ name: "旧名" }} onSubmit={onSubmit} onOpenChange={() => {}}>
        <NameField />
      </FormDialog>,
    )
    const input = screen.getByLabelText("名称") as HTMLInputElement
    expect(input.value).toBe("旧名")
    await userEvent.clear(input)
    await userEvent.type(input, "新名")
    await userEvent.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ name: "新名" }))
  })

  it("校验失败不提交，显示字段错误", async () => {
    const onSubmit = vi.fn()
    render(
      <FormDialog open mode="create" title="新建" schema={schema}
        defaultValues={{ name: "" }} onSubmit={onSubmit} onOpenChange={() => {}}>
        <NameField />
      </FormDialog>,
    )
    await userEvent.click(screen.getByRole("button", { name: "创建" }))
    expect(await screen.findByText("必填")).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("submitError 展示在底部", () => {
    render(
      <FormDialog open mode="create" title="新建" schema={schema}
        defaultValues={{ name: "" }} submitError="名称已存在"
        onSubmit={() => {}} onOpenChange={() => {}}>
        <NameField />
      </FormDialog>,
    )
    expect(screen.getByText("名称已存在")).toBeInTheDocument()
  })
})
