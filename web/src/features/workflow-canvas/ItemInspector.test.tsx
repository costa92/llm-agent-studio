import { describe, expect, it } from "vitest"
import { fireEvent, render, screen, within } from "@testing-library/react"
import { ItemInspector } from "./ItemInspector"
import type { InspectorItem } from "@/lib/projectState"

// P5d per-item inspector：逐条渲染 node_outputs.items。
// JSON/text item 是优先项；binary ref 渲染为受控资产 chip（不直拉字节）。

describe("ItemInspector", () => {
  it("renders the item count (N 项)", () => {
    const items: InspectorItem[] = [{ json: { a: 1 } }, { json: { b: 2 } }]
    render(<ItemInspector items={items} />)
    expect(screen.getByText(/2 项/)).toBeInTheDocument()
  })

  it("renders an index switcher for N>1 and switches the visible item", () => {
    const items: InspectorItem[] = [
      { json: { shot: "first-shot" } },
      { json: { shot: "second-shot" } },
    ]
    render(<ItemInspector items={items} />)
    // 默认显示第 1 项。
    expect(screen.getByText(/first-shot/)).toBeInTheDocument()
    expect(screen.queryByText(/second-shot/)).toBeNull()
    // 切到第 2 项（index switcher 按钮 / 下一项）。
    const switcher = screen.getByRole("button", { name: /下一项|第 2 项|2/ })
    fireEvent.click(switcher)
    expect(screen.getByText(/second-shot/)).toBeInTheDocument()
  })

  it("no index switcher for a single item", () => {
    render(<ItemInspector items={[{ json: { only: 1 } }]} />)
    expect(screen.getByText(/1 项/)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /下一项/ })).toBeNull()
  })

  it("renders {text:...}-shaped item as plain text (not raw JSON)", () => {
    const items: InspectorItem[] = [{ json: { text: "你好世界" } }]
    render(<ItemInspector items={items} />)
    expect(screen.getByText("你好世界")).toBeInTheDocument()
    // 不应把 {"text":...} 的 JSON 花括号 dump 出来。
    expect(screen.queryByText(/"text"/)).toBeNull()
  })

  it("renders a subtle parse-error note when item.json carries _parseError", () => {
    const items: InspectorItem[] = [{ json: { _parseError: true, raw: "<<garbage" } }]
    render(<ItemInspector items={items} />)
    expect(screen.getByText(/原始内容解析失败/)).toBeInTheDocument()
  })

  it("renders binary ref as an asset chip showing ref fields", () => {
    const items: InspectorItem[] = [
      {
        json: { caption: "封面" },
        binary: {
          cover: { assetId: "asset-123", mimeType: "image/png", kind: "image", status: "done" },
        },
      },
    ]
    render(<ItemInspector items={items} />)
    const chip = screen.getByTestId("inspector-binary-chip")
    expect(within(chip).getByText(/image/)).toBeInTheDocument()
    expect(within(chip).getByText(/image\/png/)).toBeInTheDocument()
    expect(within(chip).getByText(/asset-123/)).toBeInTheDocument()
    expect(within(chip).getByText(/done/)).toBeInTheDocument()
  })

  it("renders plain JSON for an object item without a text field", () => {
    const items: InspectorItem[] = [{ json: { score: 42, label: "ok" } }]
    render(<ItemInspector items={items} />)
    // 对象 item 走 <pre> JSON 渲染：键名应出现。
    expect(screen.getByText(/"score"/)).toBeInTheDocument()
    expect(screen.getByText(/42/)).toBeInTheDocument()
  })
})
