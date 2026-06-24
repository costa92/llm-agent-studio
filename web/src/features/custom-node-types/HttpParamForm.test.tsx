import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { HttpParams } from "@/lib/types"
import { HttpParamForm } from "./HttpParamForm"

function baseValue(overrides: Partial<HttpParams> = {}): HttpParams {
  return {
    method: "GET",
    url: "https://api.example.com/v1",
    headers: {},
    outputFormat: "text",
    ...overrides,
  }
}

describe("HttpParamForm", () => {
  it("editing url calls onChange with the new url", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<HttpParamForm value={baseValue({ url: "" })} onChange={onChange} secretNames={[]} />)
    await user.type(screen.getByLabelText(/URL/), "h")
    expect(onChange).toHaveBeenCalled()
    expect(onChange.mock.calls.at(-1)![0].url).toContain("h")
  })

  it("changing method calls onChange with the new method", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<HttpParamForm value={baseValue()} onChange={onChange} secretNames={[]} />)
    await user.selectOptions(screen.getByLabelText(/方法|method/i), "POST")
    expect(onChange.mock.calls.at(-1)![0].method).toBe("POST")
  })

  it("entering {{ in url surfaces a validation hint and flags the url field invalid", () => {
    render(
      <HttpParamForm value={baseValue({ url: "https://x/{{name}}" })} onChange={vi.fn()} secretNames={[]} />,
    )
    expect(screen.getByText(/URL 不能包含/)).toBeInTheDocument()
    expect(screen.getByLabelText(/URL/)).toHaveAttribute("aria-invalid", "true")
  })

  it("a header value referencing {{secret:NAME}} marks the form secret-bearing → allowResponseBody toggle visible", () => {
    render(
      <HttpParamForm
        value={baseValue({ headers: { Authorization: "Bearer {{secret:PARTNER_KEY}}" } })}
        onChange={vi.fn()}
        secretNames={["PARTNER_KEY"]}
      />,
    )
    // 含密钥引用 → allowResponseBody 可见，带背书文案。
    expect(screen.getByText(/我确认此端点不回显密钥/)).toBeInTheDocument()
  })

  it("allowResponseBody toggle is hidden when no header references a secret", () => {
    render(
      <HttpParamForm
        value={baseValue({ headers: { "X-Foo": "bar" } })}
        onChange={vi.fn()}
        secretNames={["PARTNER_KEY"]}
      />,
    )
    expect(screen.queryByText(/我确认此端点不回显密钥/)).not.toBeInTheDocument()
  })

  it("picking a secret from the dropdown inserts {{secret:NAME}} into the header value", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <HttpParamForm
        value={baseValue({ headers: { Authorization: "Bearer " } })}
        onChange={onChange}
        secretNames={["PARTNER_KEY"]}
      />,
    )
    await user.selectOptions(screen.getByLabelText(/插入密钥/), "PARTNER_KEY")
    expect(onChange).toHaveBeenCalled()
    const last = onChange.mock.calls.at(-1)![0] as HttpParams
    expect(last.headers["Authorization"]).toContain("{{secret:PARTNER_KEY}}")
  })

  it("outputFormat select updates the value", async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<HttpParamForm value={baseValue()} onChange={onChange} secretNames={[]} />)
    await user.selectOptions(screen.getByLabelText(/输出格式/), "json")
    expect(onChange.mock.calls.at(-1)![0].outputFormat).toBe("json")
  })
})
