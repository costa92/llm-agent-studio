import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { LibraryView, type LibraryViewProps } from "./LibraryPage"
import { assetStatusLabel, assetStatusVariant } from "./assetStatus"
import type { Asset, AssetDetail, Project, Style } from "@/lib/types"

// AssetThumb 走 /content 302→签名 URL；jsdom 无网络 —— stub 为 null（显占位）。
vi.mock("@/features/workflow/assetThumb", () => ({
  resolveAssetUrl: vi.fn().mockResolvedValue(null),
}))

afterEach(() => {
  vi.restoreAllMocks()
})

function makeAsset(over: Partial<Asset> = {}): Asset {
  return {
    id: "a1",
    projectId: "p1",
    shotId: "s1",
    todoId: "t1",
    type: "image",
    blobKey: "k1",
    url: "",
    prompt: "guofeng teahouse",
    style: "国风",
    provider: "openai",
    model: "gpt-image-1",
    status: "accepted",
    version: 2,
    parentAssetId: "a0",
    tags: [],
    prescreenScore: 0,
    prescreenFlags: [],
    prescreenNote: "",
    externalJobId: "",
    ...over,
  }
}

const STYLES: Style[] = [
  { name: "国风", suffix: "" },
  { name: "赛博朋克", suffix: "" },
]
const PROJECTS: Project[] = [
  { id: "p1", name: "茶馆短片" } as Project,
]

function baseProps(over: Partial<LibraryViewProps> = {}): LibraryViewProps {
  return {
    assets: [makeAsset(), makeAsset({ id: "a2", status: "pending_acceptance" })],
    isLoading: false,
    isError: false,
    onRetry: vi.fn(),
    hasNextPage: false,
    isFetchingNextPage: false,
    onLoadMore: vi.fn(),
    filter: {},
    onFilterChange: vi.fn(),
    projects: PROJECTS,
    styles: STYLES,
    selectedId: null,
    onSelect: vi.fn(),
    detail: undefined,
    detailLoading: false,
    ...over,
  }
}

// ── status badge 映射 ───────────────────────────────────────────────
describe("assetStatus", () => {
  it("maps backend asset statuses to badge variant + label", () => {
    expect(assetStatusVariant("accepted")).toBe("done")
    expect(assetStatusVariant("rejected")).toBe("rejected")
    expect(assetStatusVariant("pending_acceptance")).toBe("pending")
    expect(assetStatusVariant("generating")).toBe("running")
    expect(assetStatusLabel("accepted")).toBe("已采纳")
    expect(assetStatusLabel("pending_acceptance")).toBe("待审核")
  })
})

describe("LibraryView", () => {
  it("renders the asset grid with status badge + version", () => {
    render(<LibraryView {...baseProps()} />)
    const cards = screen
      .getAllByRole("button")
      .filter((b) => b.getAttribute("data-slot") === "asset-card")
    expect(cards).toHaveLength(2)
    // version vtag。
    expect(screen.getAllByText("v2").length).toBeGreaterThan(0)
    // status badge（grid 内是 <span>；filter chip 是 <button>，故按 tag 区分）。
    const badges = screen
      .getAllByText(/已采纳|待审核/)
      .filter((el) => el.tagName === "SPAN")
    expect(badges.map((el) => el.textContent)).toEqual(
      expect.arrayContaining(["已采纳", "待审核"]),
    )
  })

  it("renders empty state", () => {
    render(<LibraryView {...baseProps({ assets: [] })} />)
    expect(screen.getByText("没有匹配的资产")).toBeInTheDocument()
    expect(screen.getByText("调整筛选条件试试")).toBeInTheDocument()
  })

  it("renders error state with retry", async () => {
    const onRetry = vi.fn()
    const user = userEvent.setup()
    render(<LibraryView {...baseProps({ assets: [], isError: true, onRetry })} />)
    expect(screen.getByText("资产加载失败")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it("shows load-more only when hasNextPage and fires onLoadMore", async () => {
    const onLoadMore = vi.fn()
    const user = userEvent.setup()
    const { rerender } = render(<LibraryView {...baseProps()} />)
    expect(screen.queryByRole("button", { name: "加载更多" })).not.toBeInTheDocument()

    rerender(<LibraryView {...baseProps({ hasNextPage: true, onLoadMore })} />)
    await user.click(screen.getByRole("button", { name: "加载更多" }))
    expect(onLoadMore).toHaveBeenCalledTimes(1)
  })

  it("toggles a status filter chip via onFilterChange", async () => {
    const onFilterChange = vi.fn()
    const user = userEvent.setup()
    render(<LibraryView {...baseProps({ onFilterChange })} />)
    await user.click(screen.getByRole("button", { name: "已采纳" }))
    expect(onFilterChange).toHaveBeenCalledWith({ status: "accepted" })
  })

  it("disables video/audio type chips (二期) so they cannot be selected", () => {
    render(<LibraryView {...baseProps()} />)
    const video = screen.getByRole("button", { name: /视频/ })
    const audio = screen.getByRole("button", { name: /音频/ })
    expect(video).toBeDisabled()
    expect(audio).toBeDisabled()
    // 图片可选。
    expect(screen.getByRole("button", { name: "图片" })).not.toBeDisabled()
  })

  it("renders version lineage in the detail drawer", () => {
    const detail: AssetDetail = {
      asset: makeAsset(),
      versions: [makeAsset({ id: "a0", version: 1 }), makeAsset({ id: "a1", version: 2 })],
    }
    render(<LibraryView {...baseProps({ selectedId: "a1", detail })} />)
    // 血缘 v1 → v2 当前。
    expect(screen.getByText("v1")).toBeInTheDocument()
    expect(screen.getByText("openai · gpt-image-1")).toBeInTheDocument()
  })
})
