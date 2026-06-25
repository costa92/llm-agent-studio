import { describe, expect, it, vi, beforeEach } from "vitest"
import { fireEvent, render, screen, within } from "@testing-library/react"
import { ItemInspector } from "./ItemInspector"
import type { InspectorItem } from "@/lib/projectState"

// 资产解析 hook 桩：BinaryItemView 走与 AssetThumb/AssetMedia 同一受控字节路径
// （useResolvedAssetUrl → authed fetch → blob object URL）。测试只控制返回的 { url, loading }。
const resolved = vi.hoisted(() => ({ url: null as string | null, loading: false }))
vi.mock("@/features/workflow/assetThumb", () => ({
  useResolvedAssetUrl: () => resolved,
}))

// P5d per-item inspector：逐条渲染 node_outputs.items。
// JSON/text item 是优先项；binary ref 渲染为受控资产（image/video/audio），
// 失败/未就绪/不支持 kind 回落 ref chip。

describe("ItemInspector", () => {
  beforeEach(() => {
    resolved.url = null
    resolved.loading = false
  })
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

  it("renders a ready image binary ref as an <img>, not just a chip", () => {
    resolved.url = "blob:asset-123"
    const items: InspectorItem[] = [
      {
        json: { caption: "封面" },
        binary: {
          cover: { assetId: "asset-123", mimeType: "image/png", kind: "image", status: "pending_acceptance" },
        },
      },
    ]
    render(<ItemInspector items={items} />)
    const img = document.querySelector("img")
    expect(img).not.toBeNull()
    expect(img).toHaveAttribute("src", "blob:asset-123")
    // 渲染了真实资产时不应再降级到 chip。
    expect(screen.queryByTestId("inspector-binary-chip")).toBeNull()
  })

  it("renders a ready video binary ref as a <video>", () => {
    resolved.url = "blob:vid-9"
    const items: InspectorItem[] = [
      {
        json: {},
        binary: { clip: { assetId: "vid-9", mimeType: "video/mp4", kind: "video", status: "accepted" } },
      },
    ]
    render(<ItemInspector items={items} />)
    const video = document.querySelector("video")
    expect(video).not.toBeNull()
    expect(video).toHaveAttribute("src", "blob:vid-9")
  })

  it("renders a ready audio binary ref as an <audio>", () => {
    resolved.url = "blob:aud-7"
    const items: InspectorItem[] = [
      {
        json: {},
        binary: { vo: { assetId: "aud-7", mimeType: "audio/mpeg", kind: "audio", status: "accepted" } },
      },
    ]
    render(<ItemInspector items={items} />)
    const audio = document.querySelector("audio")
    expect(audio).not.toBeNull()
    expect(audio).toHaveAttribute("src", "blob:aud-7")
  })

  it("falls back to the chip (no broken <img>) when the asset is still generating", () => {
    // generating = 尚未有字节：不发字节请求，直接展示 chip + status。
    resolved.url = "blob:should-not-be-used"
    const items: InspectorItem[] = [
      {
        json: {},
        binary: { cover: { assetId: "asset-x", mimeType: "image/png", kind: "image", status: "generating" } },
      },
    ]
    render(<ItemInspector items={items} />)
    expect(document.querySelector("img")).toBeNull()
    const chip = screen.getByTestId("inspector-binary-chip")
    expect(within(chip).getByText(/asset-x/)).toBeInTheDocument()
    expect(within(chip).getByText(/generating/)).toBeInTheDocument()
  })

  it("falls back to the chip when the asset fetch fails (url null, not loading)", () => {
    resolved.url = null
    resolved.loading = false
    const items: InspectorItem[] = [
      {
        json: {},
        binary: { cover: { assetId: "asset-err", mimeType: "image/png", kind: "image", status: "pending_acceptance" } },
      },
    ]
    render(<ItemInspector items={items} />)
    expect(document.querySelector("img")).toBeNull()
    const chip = screen.getByTestId("inspector-binary-chip")
    expect(within(chip).getByText(/asset-err/)).toBeInTheDocument()
  })

  it("falls back to the chip for an unsupported kind", () => {
    resolved.url = "blob:whatever"
    const items: InspectorItem[] = [
      {
        json: {},
        binary: { blob: { assetId: "asset-pdf", mimeType: "application/pdf", kind: "document", status: "accepted" } },
      },
    ]
    render(<ItemInspector items={items} />)
    expect(document.querySelector("img")).toBeNull()
    expect(screen.getByTestId("inspector-binary-chip")).toBeInTheDocument()
  })

  it("renders multiple binary refs in one item", () => {
    resolved.url = "blob:multi"
    const items: InspectorItem[] = [
      {
        json: {},
        binary: {
          a: { assetId: "img-a", mimeType: "image/png", kind: "image", status: "accepted" },
          b: { assetId: "img-b", mimeType: "image/png", kind: "image", status: "accepted" },
        },
      },
    ]
    render(<ItemInspector items={items} />)
    expect(document.querySelectorAll("img")).toHaveLength(2)
  })

  it("shows a loading state while a ready asset's bytes resolve", () => {
    resolved.url = null
    resolved.loading = true
    const items: InspectorItem[] = [
      {
        json: {},
        binary: { cover: { assetId: "asset-l", mimeType: "image/png", kind: "image", status: "accepted" } },
      },
    ]
    render(<ItemInspector items={items} />)
    expect(document.querySelector("img")).toBeNull()
    // 加载中不应回落到 chip（chip 是终态降级，不是 loading 占位）。
    expect(screen.queryByTestId("inspector-binary-chip")).toBeNull()
    expect(screen.getByTestId("inspector-binary-loading")).toBeInTheDocument()
  })

  it("renders plain JSON for an object item without a text field", () => {
    const items: InspectorItem[] = [{ json: { score: 42, label: "ok" } }]
    render(<ItemInspector items={items} />)
    // 对象 item 走 <pre> JSON 渲染：键名应出现。
    expect(screen.getByText(/"score"/)).toBeInTheDocument()
    expect(screen.getByText(/42/)).toBeInTheDocument()
  })
})
