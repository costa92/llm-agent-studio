import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { ScriptParams } from "@/lib/types"
import { ScriptParamForm } from "./ScriptParamForm"

function baseValue(overrides: Partial<ScriptParams> = {}): ScriptParams {
  return {
    code: "output = upstream_text.upper()",
    outputFormat: "text",
    ...overrides,
  }
}

describe("ScriptParamForm", () => {
  it("editing code calls onChange with the new code", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ScriptParamForm value={baseValue({ code: "" })} onChange={onChange} />)
    await user.type(screen.getByLabelText(/脚本代码/), "x")
    expect(onChange).toHaveBeenCalled()
    expect(onChange.mock.calls.at(-1)![0].code).toContain("x")
  })

  it("outputFormat select updates the value", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ScriptParamForm value={baseValue()} onChange={onChange} />)
    await user.selectOptions(screen.getByLabelText(/输出格式/), "json")
    expect(onChange.mock.calls.at(-1)![0].outputFormat).toBe("json")
  })

  it("has no secret picker (scripts forbid secrets)", () => {
    render(<ScriptParamForm value={baseValue()} onChange={vi.fn()} />)
    expect(screen.queryByText(/插入密钥/)).not.toBeInTheDocument()
    expect(screen.queryByText(/我确认此端点不回显密钥/)).not.toBeInTheDocument()
  })
})
