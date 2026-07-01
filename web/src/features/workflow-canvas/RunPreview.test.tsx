import { beforeEach, describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { RunPreview } from "./RunPreview"
import {
  classifyPreviewMode,
  extractStoryDoc,
  pairPages,
} from "./runPreviewModel"
import type { GraphNode } from "@/lib/projectState"
import type { Shot, ProjectAsset } from "@/features/workflow/api"

// AssetThumb 走 authed fetch → blob object URL；jsdom 无网络。
// 默认给一个可解析 URL，让 <img> 真正渲染（reader/music 有图断言）。
const { useResolvedAssetUrlMock } = vi.hoisted(() => ({
  useResolvedAssetUrlMock: vi.fn(() => ({ url: "blob:img", loading: false })),
}))
vi.mock("@/features/workflow/assetThumb", () => ({
  resolveAssetUrl: vi.fn().mockResolvedValue("blob:img"),
  useResolvedAssetUrl: () => useResolvedAssetUrlMock(),
}))

// api hooks：shots / assets / lyrics-audio 用 vi.hoisted 可控 mock。
const { useShotsMock, useProjectAssetsMock, lyricsMutateMock, useLyricsAudioMock } =
  vi.hoisted(() => ({
    useShotsMock: vi.fn(() => ({ data: [] as Shot[] })),
    useProjectAssetsMock: vi.fn(() => ({ data: [] as ProjectAsset[] })),
    lyricsMutateMock: vi.fn(),
    useLyricsAudioMock: vi.fn(() => ({
      mutate: lyricsMutateMock,
      isPending: false,
      isError: false,
    })),
  }))
vi.mock("@/features/workflow/api", () => ({
  useShots: (...a: unknown[]) => useShotsMock(...(a as [])),
  useProjectAssets: (...a: unknown[]) => useProjectAssetsMock(...(a as [])),
  useLyricsAudio: () => useLyricsAudioMock(),
}))

beforeEach(() => {
  lyricsMutateMock.mockReset()
  useLyricsAudioMock.mockReturnValue({
    mutate: lyricsMutateMock,
    isPending: false,
    isError: false,
  })
  useShotsMock.mockReturnValue({ data: [] as Shot[] })
  useProjectAssetsMock.mockReturnValue({ data: [] as ProjectAsset[] })
})

const customNode = (type: string, output: string, label = "自定义"): GraphNode => ({
  id: "n1",
  label,
  type,
  status: "done",
  output,
  outputFormat: "json",
})

const MUSIC_DOC = JSON.stringify({
  title: "夏夜晚风",
  lyrics: "第一句\n第二句\n副歌 高潮句",
  mood: "轻快 · 治愈",
  coverPrompt: "a summer night",
})
const STORY_DOC = JSON.stringify({
  title: "小熊找蜜",
  story: "从前有一只小熊…",
  moral: "坚持",
  coverPrompt: "a bear",
})

describe("classifyPreviewMode", () => {
  it("命中歌曲/作词工作流 → music", () => {
    const nodes = [customNode("custom:llm", MUSIC_DOC, "作词编曲")]
    expect(classifyPreviewMode(nodes)).toBe("music")
    expect(classifyPreviewMode([], "歌曲创作流")).toBe("music")
  })
  it("普通绘本工作流 → reader", () => {
    const nodes = [customNode("custom:llm", STORY_DOC, "绘本编剧")]
    expect(classifyPreviewMode(nodes)).toBe("reader")
    expect(classifyPreviewMode([], "绘本工作流")).toBe("reader")
  })
})

describe("extractStoryDoc", () => {
  it("音乐 JSON → 抽出 lyrics（无 story）", () => {
    const doc = extractStoryDoc([customNode("custom:llm", MUSIC_DOC)])
    expect(doc?.title).toBe("夏夜晚风")
    expect(doc?.lyrics).toContain("副歌")
    expect(doc?.story).toBeUndefined()
    expect(doc?.mood).toBe("轻快 · 治愈")
  })
  it("故事 JSON → 抽出 story（无 lyrics）", () => {
    const doc = extractStoryDoc([customNode("custom:llm", STORY_DOC)])
    expect(doc?.story).toContain("小熊")
    expect(doc?.lyrics).toBeUndefined()
  })
  it("畸形 output → 返回 null（不抛）", () => {
    expect(extractStoryDoc([customNode("custom:llm", "{not json")])).toBeNull()
    expect(extractStoryDoc([])).toBeNull()
  })
})

describe("pairPages", () => {
  it("按 shotId join 配图（宽松状态含 pending_acceptance）", () => {
    const shots: Shot[] = [
      { id: "s1", shotNo: 1, action: "第一镜" },
      { id: "s2", shotNo: 2, action: "第二镜" },
    ]
    const assets: ProjectAsset[] = [
      { id: "a1", shotId: "s1", type: "image", status: "pending_acceptance" },
    ]
    const pages = pairPages(shots, assets)
    expect(pages).toHaveLength(2)
    expect(pages[0]).toMatchObject({ shotId: "s1", imageAssetId: "a1", text: "第一镜" })
    expect(pages[1].imageAssetId).toBeUndefined()
  })
})

describe("RunPreview reader mode", () => {
  it("渲染带图的分镜页 + 文案", () => {
    useShotsMock.mockReturnValue({
      data: [{ id: "s1", shotNo: 1, action: "小熊走进森林" }] as Shot[],
    })
    useProjectAssetsMock.mockReturnValue({
      data: [{ id: "a1", shotId: "s1", type: "image", status: "pending_acceptance" }] as ProjectAsset[],
    })
    render(
      <RunPreview
        open
        onOpenChange={vi.fn()}
        projectId="p1"
        planId="plan1"
        nodes={[customNode("custom:llm", STORY_DOC, "绘本编剧")]}
        mode="reader"
      />,
    )
    // intro 页 → 下一页 → 分镜页；这里直接断言 intro 标题 + 页码存在。
    expect(screen.getByText("小熊找蜜")).toBeInTheDocument()
    expect(screen.getByText("从前有一只小熊…")).toBeInTheDocument()
    expect(screen.getByText(/1 \/ 2/)).toBeInTheDocument()
    // intro 首图渲染。
    expect(document.querySelector("img")).not.toBeNull()
  })
})

function renderMusic(nodes = [customNode("custom:llm", MUSIC_DOC, "作词编曲")]) {
  return render(
    <RunPreview
      open
      onOpenChange={vi.fn()}
      projectId="p1"
      planId="plan1"
      nodes={nodes}
      mode="music"
    />,
  )
}

describe("RunPreview music mode", () => {
  it("渲染歌词行（副歌高亮）", () => {
    renderMusic()
    const lines = document.querySelectorAll('[data-slot="lyric-line"]')
    expect(lines.length).toBe(3)
    expect(screen.getByText("第一句")).toBeInTheDocument()
    const chorus = screen.getByText("副歌 高潮句")
    expect(chorus.className).toContain("text-amber")
  })

  it("点播放 → 用 doc.lyrics 触发 TTS mutation", () => {
    renderMusic()
    const bar = document.querySelector('[data-slot="transport-bar"]')!
    const play = bar.querySelector("button")!
    expect(play).not.toBeDisabled()
    fireEvent.click(play)
    expect(lyricsMutateMock).toHaveBeenCalledTimes(1)
    expect(lyricsMutateMock.mock.calls[0][0]).toEqual({
      projectId: "p1",
      planId: "plan1",
      text: "第一句\n第二句\n副歌 高潮句",
    })
  })

  it("生成中 → 显示「生成朗读中…」", () => {
    useLyricsAudioMock.mockReturnValue({
      mutate: lyricsMutateMock,
      isPending: true,
      isError: false,
    })
    renderMusic()
    expect(screen.getByText("生成朗读中…")).toBeInTheDocument()
  })

  it("成功回填 audioAssetId → 挂载 <AssetAudio>（<audio>）", () => {
    // mutate 的 onSuccess 立即回调，模拟同步成功。
    lyricsMutateMock.mockImplementation((_vars, opts) =>
      opts?.onSuccess?.({ audioAssetId: "audio-9" }),
    )
    renderMusic()
    const bar = document.querySelector('[data-slot="transport-bar"]')!
    fireEvent.click(bar.querySelector("button")!)
    expect(bar.getAttribute("data-audio-ready")).toBe("true")
    expect(document.querySelector("audio")).not.toBeNull()
  })

  it("无歌词 → 播放键禁用", () => {
    const noLyrics = JSON.stringify({ title: "空", mood: "静", coverPrompt: "x" })
    renderMusic([customNode("custom:llm", noLyrics, "作词编曲")])
    const bar = document.querySelector('[data-slot="transport-bar"]')!
    expect(bar.querySelector("button")!).toBeDisabled()
  })
})
