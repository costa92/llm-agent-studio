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
// 把 assetId 编进 url（blob:<id>），让 <img src> 可反查是哪张图（封面 vs 分镜去重断言）。
const { useResolvedAssetUrlMock } = vi.hoisted(() => ({
  useResolvedAssetUrlMock: vi.fn((id: string) => ({ url: `blob:${id}`, loading: false })),
}))
vi.mock("@/features/workflow/assetThumb", () => ({
  resolveAssetUrl: vi.fn().mockResolvedValue("blob:img"),
  useResolvedAssetUrl: (id: string) => useResolvedAssetUrlMock(id),
}))

// api hooks：shots / assets / lyrics-audio / project 用 vi.hoisted 可控 mock。
const {
  useShotsMock,
  useProjectAssetsMock,
  useProjectMock,
  lyricsMutateMock,
  useLyricsAudioMock,
} = vi.hoisted(() => ({
  useShotsMock: vi.fn(() => ({ data: [] as Shot[] })),
  useProjectAssetsMock: vi.fn(() => ({ data: [] as ProjectAsset[] })),
  useProjectMock: vi.fn(() => ({ data: undefined as unknown })),
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
  useProject: (...a: unknown[]) => useProjectMock(...(a as [])),
  useLyricsAudio: () => useLyricsAudioMock(),
  // ExportDialog（头部「导出」按钮渲染）从同一模块导入这两个 hook；
  // 关闭态也会运行，故 mock 成惰性桩，避免真实 react-query 依赖。
  useCreateExport: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useExportJob: () => ({ data: undefined }),
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
  useProjectMock.mockReturnValue({ data: undefined })
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

// 造 N 个各带一张 image 资产的分镜（s1..sN / a1..aN）。
function makeShotPages(n: number): { shots: Shot[]; assets: ProjectAsset[] } {
  const shots: Shot[] = []
  const assets: ProjectAsset[] = []
  for (let i = 1; i <= n; i++) {
    shots.push({ id: `s${i}`, shotNo: i, action: `第 ${i} 镜` } as Shot)
    assets.push({
      id: `a${i}`,
      shotId: `s${i}`,
      type: "image",
      status: "pending_acceptance",
    } as ProjectAsset)
  }
  return { shots, assets }
}

function renderReader(opts: { shots: Shot[]; assets: ProjectAsset[]; coverAssetId?: string }) {
  useShotsMock.mockReturnValue({ data: opts.shots })
  useProjectAssetsMock.mockReturnValue({ data: opts.assets })
  useProjectMock.mockReturnValue({
    data: opts.coverAssetId !== undefined ? { coverAssetId: opts.coverAssetId } : undefined,
  })
  return render(
    <RunPreview
      open
      onOpenChange={vi.fn()}
      projectId="p1"
      planId="plan1"
      nodes={[customNode("custom:llm", STORY_DOC, "绘本编剧")]}
      mode="reader"
    />,
  )
}

// 当前可见图片的 src（blob:<assetId>）。同一时刻仅一张。
const currentImgSrc = () => document.querySelector("img")?.getAttribute("src")

describe("RunPreview reader mode", () => {
  it("渲染带图的分镜页 + 文案", () => {
    const { shots, assets } = makeShotPages(1)
    renderReader({ shots, assets })
    // 单分镜无专属封面：封面借用首镜，内容页被排除 → 共 1 页。
    expect(screen.getByText("小熊找蜜")).toBeInTheDocument()
    expect(screen.getByText("从前有一只小熊…")).toBeInTheDocument()
    expect(screen.getByText(/1 \/ 1/)).toBeInTheDocument()
    expect(document.querySelector("img")).not.toBeNull()
  })

  it("无专属封面：封面借用首镜且该镜从内容页排除（封面≠任一内容页图，总页 N 而非 N+1）", () => {
    const { shots, assets } = makeShotPages(3)
    renderReader({ shots, assets })
    const cover = currentImgSrc()
    // 封面 = 首镜图 a1。
    expect(cover).toBe("blob:a1")
    // 总页数 = N（1 封面 + N-1 内容），不是 N+1。
    expect(screen.getByText(/1 \/ 3/)).toBeInTheDocument()

    const next = screen.getByText("下一页")
    const seen: (string | null | undefined)[] = []
    fireEvent.click(next) // 内容第 1 页
    seen.push(currentImgSrc())
    expect(screen.getByText(/2 \/ 3/)).toBeInTheDocument()
    fireEvent.click(next) // 内容第 2 页（末页）
    seen.push(currentImgSrc())
    expect(screen.getByText(/3 \/ 3/)).toBeInTheDocument()
    // 内容页依次是 a2, a3——都不等于封面 a1（不重复）。
    expect(seen).toEqual(["blob:a2", "blob:a3"])
    expect(seen).not.toContain(cover)
    // 末页可达且「下一页」禁用。
    expect(screen.getByText("下一页")).toBeDisabled()
  })

  it("有专属封面：封面=coverAssetId，内容页保留全部 N 镜且封面≠任一分镜图", () => {
    const { shots, assets } = makeShotPages(3)
    renderReader({ shots, assets, coverAssetId: "cover-1" })
    expect(currentImgSrc()).toBe("blob:cover-1")
    // 专属封面独立于分镜 → 内容页保留全部 3 镜 → 总页 4。
    expect(screen.getByText(/1 \/ 4/)).toBeInTheDocument()

    const next = screen.getByText("下一页")
    const contentImgs: (string | null | undefined)[] = []
    fireEvent.click(next)
    contentImgs.push(currentImgSrc())
    fireEvent.click(next)
    contentImgs.push(currentImgSrc())
    fireEvent.click(next)
    contentImgs.push(currentImgSrc())
    expect(screen.getByText(/4 \/ 4/)).toBeInTheDocument()
    expect(contentImgs).toEqual(["blob:a1", "blob:a2", "blob:a3"])
    // 封面 id 有别于每张分镜图。
    expect(contentImgs).not.toContain("blob:cover-1")
    expect(screen.getByText("下一页")).toBeDisabled()
  })

  it("有专属封面但空白串 → 回退借用首镜（去重逻辑同无封面）", () => {
    const { shots, assets } = makeShotPages(2)
    renderReader({ shots, assets, coverAssetId: "  " })
    expect(currentImgSrc()).toBe("blob:a1")
    // 空白封面按无封面处理：总页 N=2。
    expect(screen.getByText(/1 \/ 2/)).toBeInTheDocument()
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

  it("有专属封面 → 用 coverAssetId 作封面（不借用分镜）", () => {
    useShotsMock.mockReturnValue({
      data: [{ id: "s1", shotNo: 1, action: "第一镜" }] as Shot[],
    })
    useProjectAssetsMock.mockReturnValue({
      data: [{ id: "a1", shotId: "s1", type: "image", status: "pending_acceptance" }] as ProjectAsset[],
    })
    useProjectMock.mockReturnValue({ data: { coverAssetId: "cover-m" } })
    renderMusic()
    const view = document.querySelector('[data-slot="music-view"]')!
    expect(view.querySelector("img")?.getAttribute("src")).toBe("blob:cover-m")
  })

  it("无专属封面且无分镜 → 显示「封面暂无产物」占位", () => {
    renderMusic()
    expect(screen.getByText("封面暂无产物")).toBeInTheDocument()
  })
})
