import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { CrudResourcePage, SingletonConfigForm } from "./CrudResourcePage"
import { z } from "zod"
import { useFormContext } from "react-hook-form"

describe("CrudResourcePage", () => {
  it("加载态渲染 Skeleton，不渲染 children", () => {
    render(
      <CrudResourcePage title="提示词" isLoading isError={false} isEmpty={false}>
        <div data-testid="body">x</div>
      </CrudResourcePage>,
    )
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("错误态显示重试，点击调 onRetry", async () => {
    const onRetry = vi.fn()
    render(
      <CrudResourcePage title="提示词" isLoading={false} isError onRetry={onRetry} isEmpty={false}>
        <div />
      </CrudResourcePage>,
    )
    await userEvent.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalled()
  })

  it("空态显示 emptyHint", () => {
    render(
      <CrudResourcePage title="提示词" isLoading={false} isError={false} isEmpty emptyHint="暂无提示词">
        <div data-testid="body" />
      </CrudResourcePage>,
    )
    expect(screen.getByText("暂无提示词")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("正常态：标题 + 新增按钮(onCreate) + children", async () => {
    const onCreate = vi.fn()
    render(
      <CrudResourcePage title="提示词" createLabel="新增提示词" onCreate={onCreate}
        isLoading={false} isError={false} isEmpty={false}>
        <div data-testid="body">列表</div>
      </CrudResourcePage>,
    )
    expect(screen.getByText("提示词")).toBeInTheDocument()
    expect(screen.getByTestId("body")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "新增提示词" }))
    expect(onCreate).toHaveBeenCalled()
  })
})

describe("SingletonConfigForm", () => {
  const schema = z.object({ host: z.string() })
  function Field() {
    const { register } = useFormContext<{ host: string }>()
    return <input aria-label="主机" {...register("host")} />
  }
  it("预填 values 并提交", async () => {
    const onSubmit = vi.fn()
    render(
      <SingletonConfigForm title="邮件" schema={schema} values={{ host: "smtp.x" }}
        isLoading={false} onSubmit={onSubmit}>
        <Field />
      </SingletonConfigForm>,
    )
    expect((screen.getByLabelText("主机") as HTMLInputElement).value).toBe("smtp.x")
    await userEvent.click(screen.getByRole("button", { name: "保存" }))
    expect(onSubmit).toHaveBeenCalledWith({ host: "smtp.x" })
  })
})
