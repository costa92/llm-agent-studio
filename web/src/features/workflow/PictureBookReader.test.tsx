import { describe, it, expect, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { PictureBookReader, type PicturePage } from "./PictureBookReader"

// 子组件桩：聚焦阅读器翻页/分发逻辑，不触发真实 fetch / audio。
vi.mock("./AssetThumb", () => ({
  AssetThumb: ({ assetId }: { assetId: string }) => (
    <div data-testid="thumb">{assetId}</div>
  ),
}))
vi.mock("./AssetAudio", () => ({
  AssetAudio: ({ assetId }: { assetId: string }) => (
    <div data-testid="audio">{assetId}</div>
  ),
}))
vi.mock("./PromptPanel", () => ({
  PromptPanel: ({ illustrationPrompt }: { illustrationPrompt?: string }) => {
    return <div data-testid="prompt-panel">{illustrationPrompt}</div>
  },
}))

const pages: PicturePage[] = [
  { kind: "cover", title: "小熊的一天", illustrationAssetId: "cover-img" },
  {
    kind: "content",
    illustrationAssetId: "img-1",
    audioAssetId: "audio-1",
    narration: "小熊起床了",
    prompt: "一只小熊在床上",
    provider: "openai",
    model: "gpt-image-1",
  },
  {
    kind: "content",
    illustrationAssetId: "img-2",
    audioAssetId: "audio-2",
    narration: "小熊吃早餐",
    prompt: "小熊吃蜂蜜",
  },
  { kind: "ending", title: "全剧终", illustrationAssetId: "end-img" },
]

describe("PictureBookReader", () => {
  it("封面 → 开始阅读 → 内容页旁白 → 下一页 → 结尾", () => {
    render(
      <PictureBookReader pages={pages} open onOpenChange={() => {}} />,
    )
    // 封面：书名 + 开始阅读。
    expect(screen.getByText("小熊的一天")).toBeInTheDocument()
    fireEvent.click(screen.getByText("▶ 开始阅读"))

    // 第 1 内容页：旁白可见。
    expect(screen.getByText("小熊起床了")).toBeInTheDocument()

    // 下一页 → 第 2 内容页。
    fireEvent.click(screen.getByRole("button", { name: "下一页" }))
    expect(screen.getByText("小熊吃早餐")).toBeInTheDocument()

    // 再下一页 → 结尾。
    fireEvent.click(screen.getByRole("button", { name: "下一页" }))
    expect(screen.getByText("全剧终")).toBeInTheDocument()
    expect(screen.getByText("↺ 重新阅读")).toBeInTheDocument()
  })

  it("内容页查看提示词：展示该页 prompt", () => {
    render(
      <PictureBookReader pages={pages} open onOpenChange={() => {}} initialIndex={1} />,
    )
    expect(screen.getByTestId("prompt-panel")).toHaveTextContent("一只小熊在床上")
  })

  it("末内容页点下一页不越界", () => {
    // initialIndex 指向最后一页（结尾页）：无「下一页」按钮。
    render(
      <PictureBookReader pages={pages} open onOpenChange={() => {}} initialIndex={3} />,
    )
    expect(screen.queryByRole("button", { name: "下一页" })).not.toBeInTheDocument()
  })

  it("重新生成插图 / 编辑旁白入口回调", () => {
    const onRegen = vi.fn()
    const onEdit = vi.fn()
    render(
      <PictureBookReader
        pages={pages}
        open
        onOpenChange={() => {}}
        initialIndex={1}
        onRegenIllustration={onRegen}
        onEditNarration={onEdit}
      />,
    )
    fireEvent.click(screen.getByText("↻ 重新生成插图"))
    expect(onRegen).toHaveBeenCalledWith(pages[1])
  })
})
