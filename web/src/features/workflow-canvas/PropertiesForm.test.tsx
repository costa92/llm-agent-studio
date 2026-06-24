import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { PropertiesForm } from "./PropertiesForm"
import type { NodeTypeDescription } from "./nodeDescTypes"

afterEach(() => vi.restoreAllMocks())

function desc(props: NodeTypeDescription["properties"]): NodeTypeDescription {
  return { type: "t", version: 1, label: "T", description: "", group: "transform", inputs: [], outputs: [], properties: props }
}

function renderForm(d: NodeTypeDescription, value: Record<string, unknown> = {}, extra = {}) {
  const onChange = vi.fn()
  render(<PropertiesForm description={d} value={value} onChange={onChange} secretNames={[]} {...extra} />)
  return { onChange }
}

describe("PropertiesForm widgets", () => {
  it("renders a string input and emits onChange", async () => {
    const { onChange } = renderForm(desc([{ name: "url", label: "URL", type: "string" }]))
    await userEvent.type(screen.getByLabelText("URL"), "x")
    expect(onChange).toHaveBeenCalledWith({ url: "x" })
  })

  it("renders textarea / number / boolean / options", async () => {
    renderForm(desc([
      { name: "brief", label: "简报", type: "textarea" },
      { name: "temp", label: "温度", type: "number" },
      { name: "pb", label: "绘本", type: "boolean" },
      { name: "fmt", label: "格式", type: "options", options: [{ value: "text", label: "文本" }, { value: "json", label: "JSON" }] },
    ]))
    expect(screen.getByLabelText("简报").tagName).toBe("TEXTAREA")
    expect((screen.getByLabelText("温度") as HTMLInputElement).type).toBe("number")
    expect(screen.getByLabelText("绘本")).toBeInTheDocument()
    expect(screen.getByLabelText("格式")).toBeInTheDocument()
  })

  it("hides a property whose displayOptions.show is unmet, shows it when met", () => {
    const d = desc([
      { name: "pictureBook", label: "绘本", type: "boolean" },
      { name: "ageBand", label: "年龄段", type: "options", options: [{ value: "0-3", label: "0-3" }], displayOptions: { show: { pictureBook: [true] } } },
    ])
    const { rerender } = render(<PropertiesForm description={d} value={{ pictureBook: false }} onChange={() => {}} secretNames={[]} />)
    expect(screen.queryByLabelText("年龄段")).toBeNull()
    rerender(<PropertiesForm description={d} value={{ pictureBook: true }} onChange={() => {}} secretNames={[]} />)
    expect(screen.getByLabelText("年龄段")).toBeInTheDocument()
  })

  it("applies DefaultFrom cascade: picking ageBand sets derived fields", async () => {
    const d = desc([
      {
        name: "ageBand", label: "年龄段", type: "options",
        options: [{ value: "0-3", label: "0-3" }],
        defaultFrom: { field: "ageBand", map: { "0-3": { pageCount: 8, maxWordsPerSpread: 10 } } },
      },
      { name: "pageCount", label: "页数", type: "number" },
    ])
    const onChange = vi.fn()
    render(<PropertiesForm description={d} value={{}} onChange={onChange} secretNames={[]} />)
    await userEvent.selectOptions(screen.getByLabelText("年龄段"), "0-3")
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ ageBand: "0-3", pageCount: 8, maxWordsPerSpread: 10 }))
  })

  it("renders keyValue headers with a secret-insert dropdown", async () => {
    const onChange = vi.fn()
    render(<PropertiesForm
      description={desc([{ name: "headers", label: "请求头", type: "keyValue" }])}
      value={{ headers: { Authorization: "" } }}
      onChange={onChange}
      secretNames={["STRIPE_KEY"]}
    />)
    expect(screen.getByDisplayValue("Authorization")).toBeInTheDocument()
    expect(screen.getByText("插入密钥…")).toBeInTheDocument()
  })

  it("renders a prompt picker with the three sentinels", () => {
    render(<PropertiesForm
      description={desc([{ name: "systemPrompt", label: "系统提示词", type: "prompt", typeOptions: { promptKind: "script" } }])}
      value={{}}
      onChange={() => {}}
      secretNames={[]}
      prompts={[]}
      basics={[]}
      org="org-1"
    />)
    expect(screen.getByLabelText("系统提示词")).toBeInTheDocument()
  })

  it("renders code (monospace textarea) and resourceLocator (model)", () => {
    render(<PropertiesForm
      description={desc([
        { name: "code", label: "脚本代码", type: "code", typeOptions: { editor: "starlark", rows: 8 } },
        { name: "model", label: "模型", type: "resourceLocator", typeOptions: { dataSource: "model" } },
      ])}
      value={{}}
      onChange={() => {}}
      secretNames={[]}
      modelOptions={[{ value: "gpt-4o", label: "gpt-4o" }]}
    />)
    expect(screen.getByLabelText("脚本代码").tagName).toBe("TEXTAREA")
    expect(screen.getByLabelText("模型")).toBeInTheDocument()
  })
})
