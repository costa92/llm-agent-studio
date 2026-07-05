import { describe, it, expect, vi } from "vitest"
import { render, screen, fireEvent, waitFor } from "@testing-library/react"
import { RunInputsDialog } from "./RunInputsDialog"
import type { InputField } from "@/lib/types"

// RunInputsDialog 按 schema 渲染运行期表单，前端拦截 required 缺失，提交把
// {<name>: <value>} 交回调用方（调用方再 POST {inputs}）。

function submitBtn() {
  return screen.getByRole("button", { name: "运行" })
}

describe("RunInputsDialog", () => {
  it("按 schema 渲染字段（text/number），并显示 label", () => {
    const schema: InputField[] = [
      { name: "heroName", label: "主角名字", type: "text", target: "variable" },
      { name: "pageCount", label: "页数", type: "number", target: "variable" },
    ]
    render(
      <RunInputsDialog
        open
        onOpenChange={() => {}}
        schema={schema}
        onSubmit={() => {}}
      />,
    )
    expect(screen.getByText("主角名字")).toBeInTheDocument()
    expect(screen.getByText("页数")).toBeInTheDocument()
  })

  it("required 缺失 → 阻断提交并提示，onSubmit 不被调用", () => {
    const onSubmit = vi.fn()
    const schema: InputField[] = [
      { name: "heroName", label: "主角名字", type: "text", target: "variable", required: true },
    ]
    render(
      <RunInputsDialog open onOpenChange={() => {}} schema={schema} onSubmit={onSubmit} />,
    )
    fireEvent.click(submitBtn())
    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByText(/必填/)).toBeInTheDocument()
  })

  it("填好 required → 提交把 {name:value} 交给 onSubmit", async () => {
    const onSubmit = vi.fn()
    const schema: InputField[] = [
      { name: "heroName", label: "主角名字", type: "text", target: "variable", required: true },
    ]
    render(
      <RunInputsDialog open onOpenChange={() => {}} schema={schema} onSubmit={onSubmit} />,
    )
    fireEvent.change(screen.getByLabelText(/主角名字/), { target: { value: "阿力" } })
    fireEvent.click(submitBtn())
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ heroName: "阿力" }))
  })

  it("number 提交为数字字面量（非字符串），供后端 Validate 通过", async () => {
    const onSubmit = vi.fn()
    const schema: InputField[] = [
      { name: "pageCount", label: "页数", type: "number", target: "variable", default: "12" },
    ]
    render(
      <RunInputsDialog open onOpenChange={() => {}} schema={schema} onSubmit={onSubmit} />,
    )
    fireEvent.click(submitBtn())
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ pageCount: 12 }))
  })

  it("带 default 的 schema：预填全字段，提交带全部非空字段", async () => {
    const onSubmit = vi.fn()
    // JSON 字面量 default 形态。
    const schema: InputField[] = [
      { name: "voice", type: "select", target: "variable", options: [{ value: "warm", label: "warm" }], default: JSON.stringify("warm") },
      { name: "ageBand", type: "select", target: "variable", options: [{ value: "3-6", label: "3-6" }], default: JSON.stringify("3-6") },
      { name: "pageCount", type: "number", target: "variable", default: JSON.stringify(16) },
    ]
    render(
      <RunInputsDialog open onOpenChange={() => {}} schema={schema} onSubmit={onSubmit} />,
    )
    fireEvent.click(submitBtn())
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        voice: "warm",
        ageBand: "3-6",
        pageCount: 16,
      }),
    )
  })

  it("select 未选（NONE）的可选字段在提交时被省略（避免后端枚举越界 400）", async () => {
    const onSubmit = vi.fn()
    const schema: InputField[] = [
      { name: "bookType", type: "select", target: "variable", options: [{ value: "narrative", label: "故事绘本" }], default: JSON.stringify("") },
    ]
    render(
      <RunInputsDialog open onOpenChange={() => {}} schema={schema} onSubmit={onSubmit} />,
    )
    fireEvent.click(submitBtn())
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({}))
  })
})
