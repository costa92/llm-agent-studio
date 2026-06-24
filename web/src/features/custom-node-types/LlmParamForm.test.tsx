import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { LlmParams } from "@/lib/types"
import { LlmParamForm } from "./LlmParamForm"

const BASE: LlmParams = { userPrompt: "翻译：{{text}}" }

function renderForm(value: LlmParams, onChange = vi.fn()) {
  return { onChange, ...render(<LlmParamForm value={value} onChange={onChange} />) }
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

  it("renders model input", () => {
    renderForm(BASE)
    expect(screen.getByLabelText(/模型/)).toBeInTheDocument()
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
