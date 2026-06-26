import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { LlmParams } from "@/lib/types"
import { LlmParamForm } from "./LlmParamForm"

const BASE: LlmParams = { userPrompt: "翻译：{{text}}" }

const MODELS = [
  { value: "gpt-4o", label: "openai · gpt-4o" },
  { value: "deepseek-chat", label: "deepseek · deepseek-chat" },
]

function renderForm(
  value: LlmParams,
  onChange = vi.fn(),
  modelOptions: { value: string; label: string }[] = [],
) {
  return {
    onChange,
    ...render(<LlmParamForm value={value} onChange={onChange} modelOptions={modelOptions} />),
  }
}

describe("LlmParamForm", () => {
  it("renders systemPrompt textarea", () => {
    renderForm(BASE)
    expect(screen.getByLabelText(/系统提示词/)).toBeInTheDocument()
  })

  it("renders userPrompt textarea with initial value", () => {
    renderForm(BASE)
    expect(screen.getByLabelText(/用户提示词/)).toHaveValue("翻译：{{text}}")
  })

  it("renders model as a config-backed select (not free-text input)", () => {
    renderForm(BASE, vi.fn(), MODELS)
    const field = screen.getByLabelText("模型")
    // 与画布 PropertiesPanel 一致：模型字段是 org model-config 支持的下拉，而非自由文本输入框。
    expect(field.tagName).toBe("SELECT")
    expect(screen.getByRole("option", { name: "openai · gpt-4o" })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "deepseek · deepseek-chat" })).toBeInTheDocument()
    // 「组织默认」空选项始终存在（model 可选）。
    expect(screen.getByRole("option", { name: /组织默认/ })).toBeInTheDocument()
  })

  it("selecting a model calls onChange with that model value", () => {
    const onChange = vi.fn()
    renderForm(BASE, onChange, MODELS)
    fireEvent.change(screen.getByLabelText("模型"), { target: { value: "gpt-4o" } })
    expect(onChange).toHaveBeenCalledTimes(1)
    expect((onChange.mock.calls[0][0] as LlmParams).model).toBe("gpt-4o")
  })

  it("clearing model selection (组织默认) sets model=undefined", () => {
    const onChange = vi.fn()
    renderForm({ ...BASE, model: "gpt-4o" }, onChange, MODELS)
    fireEvent.change(screen.getByLabelText("模型"), { target: { value: "" } })
    expect((onChange.mock.calls[0][0] as LlmParams).model).toBeUndefined()
  })

  it("keeps an already-set model visible even if not among org configs", () => {
    renderForm({ ...BASE, model: "legacy-model-x" }, vi.fn(), MODELS)
    const field = screen.getByLabelText("模型") as HTMLSelectElement
    expect(field.value).toBe("legacy-model-x")
    expect(screen.getByRole("option", { name: /legacy-model-x/ })).toBeInTheDocument()
  })

  it("renders temperature input", () => {
    renderForm(BASE)
    expect(screen.getByLabelText(/温度/)).toBeInTheDocument()
  })

  it("renders outputFormat select", () => {
    renderForm(BASE)
    expect(screen.getByLabelText(/输出格式/)).toBeInTheDocument()
  })

  it("calls onChange with updated systemPrompt", () => {
    const onChange = vi.fn()
    renderForm(BASE, onChange)
    const ta = screen.getByLabelText(/系统提示词/)
    fireEvent.change(ta, { target: { value: "你是翻译助手" } })
    expect(onChange).toHaveBeenCalledTimes(1)
    const updated = onChange.mock.calls[0][0] as LlmParams
    expect(updated.systemPrompt).toBe("你是翻译助手")
  })

  it("calls onChange with updated userPrompt", () => {
    const onChange = vi.fn()
    renderForm(BASE, onChange)
    const ta = screen.getByLabelText(/用户提示词/)
    fireEvent.change(ta, { target: { value: "请翻译：{{text}}" } })
    expect(onChange).toHaveBeenCalledTimes(1)
    const updated = onChange.mock.calls[0][0] as LlmParams
    expect(updated.userPrompt).toBe("请翻译：{{text}}")
  })

  it("calls onChange with updated temperature", () => {
    const onChange = vi.fn()
    renderForm(BASE, onChange)
    const input = screen.getByLabelText(/温度/)
    fireEvent.change(input, { target: { value: "0.8" } })
    expect(onChange).toHaveBeenCalledTimes(1)
    const updated = onChange.mock.calls[0][0] as LlmParams
    expect(updated.temperature).toBeCloseTo(0.8)
  })

  it("calls onChange with outputFormat=json when toggled", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderForm(BASE, onChange)
    // Click the SelectTrigger to open
    const trigger = screen.getByLabelText(/输出格式/)
    await user.click(trigger)
    // Select JSON item
    const jsonOption = screen.getByRole("option", { name: /json/i })
    await user.click(jsonOption)
    expect(onChange).toHaveBeenCalled()
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][0] as LlmParams
    expect(last.outputFormat).toBe("json")
  })

  it("does NOT render any variable-binding rows (no upstream nodes)", () => {
    renderForm(BASE)
    // 确认无「上游节点」/「来源节点」选择器 UI（org-level 表单无工作流上下文）
    expect(screen.queryByText(/上游节点/)).toBeNull()
    expect(screen.queryByText(/来源节点/)).toBeNull()
    // 无「添加变量绑定」/ 「变量来源」按钮
    expect(screen.queryByRole("button", { name: /变量|binding/i })).toBeNull()
  })
})
