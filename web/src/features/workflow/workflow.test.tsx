import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import { ScriptView } from "./ScriptView"
import { StoryboardView } from "./StoryboardView"
import type { ScriptDoc, Shot } from "./api"

describe("ScriptView", () => {
  it("renders empty state when no script", () => {
    render(<ScriptView script={null} isLoading={false} isError={false} />)
    expect(screen.getByText("剧本尚未生成")).toBeInTheDocument()
  })

  it("renders error state on malformed/failed load", () => {
    render(<ScriptView script={undefined} isLoading={false} isError />)
    expect(
      screen.getByText("剧本数据异常，请重新运行剧本阶段"),
    ).toBeInTheDocument()
  })

  it("renders title, logline and scenes", () => {
    const script: ScriptDoc = {
      title: "茶馆黄昏",
      logline: "少女与老茶师的传承",
      scenes: [
        { heading: "场景一 · 茶馆内", description: "黄昏光线洒入", dialogue: "老茶师：慢些。" },
      ],
    }
    render(<ScriptView script={script} isLoading={false} isError={false} />)
    expect(screen.getByText("茶馆黄昏")).toBeInTheDocument()
    expect(screen.getByText("少女与老茶师的传承")).toBeInTheDocument()
    expect(screen.getByText("场景一 · 茶馆内")).toBeInTheDocument()
    expect(screen.getByText("黄昏光线洒入")).toBeInTheDocument()
  })
})

describe("StoryboardView", () => {
  it("renders empty state when no shots", () => {
    render(<StoryboardView shots={[]} isLoading={false} isError={false} />)
    expect(screen.getByText("分镜尚未拆解")).toBeInTheDocument()
  })

  it("renders the shot grid with shotNo / action / prompt", () => {
    const shots: Shot[] = [
      { shotNo: 1, camera: "中景·推", action: "茶馆内黄昏", prompt: "guofeng, dusk teahouse" },
      { shotNo: 2, camera: "特写", action: "茶汤特写", prompt: "close up tea" },
    ]
    render(<StoryboardView shots={shots} isLoading={false} isError={false} />)
    expect(screen.getByText("#1")).toBeInTheDocument()
    expect(screen.getByText("茶馆内黄昏")).toBeInTheDocument()
    expect(screen.getByText("guofeng, dusk teahouse")).toBeInTheDocument()
    expect(screen.getByText("#2")).toBeInTheDocument()
  })

  it("renders an illustration thumbnail for mapped shots when illustrationByShotId is provided", () => {
    // fetch 永不 resolve → AssetThumb 停在自持加载态占位（"加载中…"），
    // 无需真实解析 blob src，稳定断言插图确实渲染。
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})))
    const shots: Shot[] = [
      { id: "s1", shotNo: 1, action: "茶馆内黄昏", prompt: "guofeng" },
      { id: "s2", shotNo: 2, action: "茶汤特写", prompt: "close up tea" },
    ]
    render(
      <StoryboardView
        shots={shots}
        isLoading={false}
        isError={false}
        illustrationByShotId={{ s1: "img1" }}
      />,
    )
    // 仅 s1 有映射 → 恰好一个 AssetThumb（加载态占位）。
    expect(screen.getAllByText("加载中…")).toHaveLength(1)
    vi.unstubAllGlobals()
  })

  it("renders no illustration when illustrationByShotId is absent (guards drawer callers)", () => {
    // 运行抽屉两处调用点不传 illustrationByShotId → 零图片、零 fetch，纯文本渲染。
    const fetchSpy = vi.fn(() => new Promise<Response>(() => {}))
    vi.stubGlobal("fetch", fetchSpy)
    const shots: Shot[] = [
      { id: "s1", shotNo: 1, action: "茶馆内黄昏", prompt: "guofeng" },
    ]
    const { container } = render(
      <StoryboardView shots={shots} isLoading={false} isError={false} />,
    )
    expect(screen.queryByText("加载中…")).not.toBeInTheDocument()
    expect(container.querySelector("img")).toBeNull()
    expect(fetchSpy).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })
})
