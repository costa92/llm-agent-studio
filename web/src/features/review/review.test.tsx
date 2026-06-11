import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { resolveReviewAction, isInputTarget } from "./keyboard"
import { hitlErrorMessage } from "./hitlError"
import { ApiError } from "@/lib/apiClient"
import { AdminGate } from "@/features/cost/AdminGate"
import { ReviewBoardView, type ReviewBoardViewProps } from "./ReviewBoardPage"
import type { Asset, AssetDetail } from "@/lib/types"

// AssetThumb 走 authed fetch → blob object URL；jsdom 无网络——stub 为 null
// （AssetThumb 显"加载中…/不可用"占位），避免测试触网。
vi.mock("@/features/workflow/assetThumb", () => ({
  resolveAssetUrl: vi.fn().mockResolvedValue(null),
  useResolvedAssetUrl: vi.fn(() => ({ url: null, loading: false })),
}))

afterEach(() => {
  vi.restoreAllMocks()
})

// ── 键盘 dispatch（admin vs 非 admin + 输入聚焦禁用）─────────────────────
describe("resolveReviewAction", () => {
  it("maps ←/→ to prev/next for all roles", () => {
    const ctx = { isAdmin: false, inInput: false }
    expect(resolveReviewAction("ArrowLeft", ctx)).toBe("prev")
    expect(resolveReviewAction("ArrowRight", ctx)).toBe("next")
  })

  it("maps A/R/E to HITL actions only for admin", () => {
    const admin = { isAdmin: true, inInput: false }
    expect(resolveReviewAction("a", admin)).toBe("accept")
    expect(resolveReviewAction("R", admin)).toBe("reject")
    expect(resolveReviewAction("e", admin)).toBe("regenerate")
  })

  it("blocks A/R/E for non-admin (returns null), keeps arrows", () => {
    const viewer = { isAdmin: false, inInput: false }
    expect(resolveReviewAction("a", viewer)).toBeNull()
    expect(resolveReviewAction("r", viewer)).toBeNull()
    expect(resolveReviewAction("e", viewer)).toBeNull()
    expect(resolveReviewAction("ArrowRight", viewer)).toBe("next")
  })

  it("disables all shortcuts while an input is focused", () => {
    const ctx = { isAdmin: true, inInput: true }
    expect(resolveReviewAction("a", ctx)).toBeNull()
    expect(resolveReviewAction("ArrowLeft", ctx)).toBeNull()
  })
})

describe("isInputTarget", () => {
  it("detects input/textarea/select/contenteditable", () => {
    const input = document.createElement("input")
    const ta = document.createElement("textarea")
    const div = document.createElement("div")
    expect(isInputTarget(input)).toBe(true)
    expect(isInputTarget(ta)).toBe(true)
    expect(isInputTarget(div)).toBe(false)
    expect(isInputTarget(null)).toBe(false)
  })
})

// ── 409/429 → toast 文案（防重 + 配额）───────────────────────────────
describe("hitlErrorMessage", () => {
  it("maps 409 to the already-processed dedup message", () => {
    expect(hitlErrorMessage(new ApiError(409, "asset not pending_acceptance"))).toBe(
      "该资产已被处理（不是待审核状态）",
    )
  })

  it("maps 429 to the quota-exceeded message", () => {
    expect(
      hitlErrorMessage(new ApiError(429, "generation quota exceeded for org")),
    ).toBe("生成配额已用尽，请稍后再试")
  })

  it("falls back to a generic message for other errors", () => {
    expect(hitlErrorMessage(new Error("boom"))).toBe("操作失败，请重试")
    expect(hitlErrorMessage(new ApiError(500, "x"))).toBe("操作失败，请重试")
  })
})

// ── ReviewBoardView render/smoke ─────────────────────────────────────
function makeAsset(over: Partial<Asset> = {}): Asset {
  return {
    id: "as1",
    projectId: "p1",
    shotId: "s1",
    todoId: "t1",
    type: "image",
    blobKey: "k1",
    url: "",
    prompt: "guofeng teahouse dusk",
    style: "国风",
    provider: "openai",
    model: "gpt-image-1",
    status: "pending_acceptance",
    version: 2,
    parentAssetId: "as0",
    tags: [],
    prescreenScore: 0,
    prescreenFlags: [],
    prescreenNote: "",
    externalJobId: "",
    ...over,
  }
}

function makeDetail(): AssetDetail {
  return {
    asset: makeAsset(),
    versions: [makeAsset({ id: "as0", version: 1 }), makeAsset({ id: "as1", version: 2 })],
  }
}

function baseProps(over: Partial<ReviewBoardViewProps> = {}): ReviewBoardViewProps {
  return {
    queue: [makeAsset(), makeAsset({ id: "as2" })],
    isLoading: false,
    isError: false,
    onRetry: vi.fn(),
    isAdmin: true,
    selectedId: null,
    onSelect: vi.fn(),
    detail: undefined,
    detailLoading: false,
    onAccept: vi.fn(),
    onReject: vi.fn(),
    onRegenerate: vi.fn(),
    ...over,
  }
}

describe("ReviewBoardView", () => {
  it("renders the pending queue as asset cards", () => {
    render(<ReviewBoardView {...baseProps()} />)
    expect(screen.getAllByRole("button").filter((b) =>
      b.getAttribute("data-slot") === "asset-card",
    )).toHaveLength(2)
    expect(screen.getByText(/待审 2/)).toBeInTheDocument()
  })

  it("renders empty state when no pending assets", () => {
    render(<ReviewBoardView {...baseProps({ queue: [] })} />)
    expect(screen.getByText("没有待审资产")).toBeInTheDocument()
    expect(screen.getByText("所有素材都处理完了")).toBeInTheDocument()
  })

  it("renders error state with retry", async () => {
    const onRetry = vi.fn()
    const user = userEvent.setup()
    render(<ReviewBoardView {...baseProps({ queue: undefined, isError: true, onRetry })} />)
    expect(screen.getByText("审核队列加载失败")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it("shows HITL actions + lineage in the drawer for admin and fires accept", async () => {
    const onAccept = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({ selectedId: "as1", detail: makeDetail(), onAccept })}
      />,
    )
    // 版本血缘（v1 → v2 当前）。
    expect(screen.getByText("v1")).toBeInTheDocument()
    // KV provider·model。
    expect(screen.getByText("openai · gpt-image-1")).toBeInTheDocument()
    // 采纳按钮 → onAccept(selectedId)。
    await user.click(screen.getByRole("button", { name: /采纳/ }))
    expect(onAccept).toHaveBeenCalledWith("as1")
  })

  it("hides HITL actions in the drawer for non-admin (read-only browse)", () => {
    render(
      <ReviewBoardView
        {...baseProps({ isAdmin: false, selectedId: "as1", detail: makeDetail() })}
      />,
    )
    // 仍能浏览 prompt（只读），但无采纳/退回/重生成。
    expect(screen.getByText("guofeng teahouse dusk")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /采纳/ })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /退回/ })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /重生成/ })).not.toBeInTheDocument()
  })

  it("opens the prompt editor on [改 Prompt 重生成] and submits the edited prompt", async () => {
    const onRegenerate = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({ selectedId: "as1", detail: makeDetail(), onRegenerate })}
      />,
    )
    await user.click(screen.getByRole("button", { name: /改 Prompt 重生成/ }))
    const box = screen.getByLabelText("编辑 Prompt")
    await user.clear(box)
    await user.type(box, "新的 prompt")
    await user.click(screen.getByRole("button", { name: "确认重生成" }))
    expect(onRegenerate).toHaveBeenCalledWith("as1", "新的 prompt")
  })

  it("arrow keys move selection across the queue (all roles)", async () => {
    const onSelect = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({ isAdmin: false, selectedId: "as1", onSelect })}
      />,
    )
    await user.keyboard("{ArrowRight}")
    expect(onSelect).toHaveBeenCalledWith("as2")
  })

  it("A key fires accept for admin but is inert for non-admin", async () => {
    const onAccept = vi.fn()
    const user = userEvent.setup()
    const { rerender } = render(
      <ReviewBoardView
        {...baseProps({ isAdmin: false, selectedId: "as1", detail: makeDetail(), onAccept })}
      />,
    )
    await user.keyboard("a")
    expect(onAccept).not.toHaveBeenCalled()

    rerender(
      <ReviewBoardView
        {...baseProps({ isAdmin: true, selectedId: "as1", detail: makeDetail(), onAccept })}
      />,
    )
    await user.keyboard("a")
    expect(onAccept).toHaveBeenCalledWith("as1")
  })
})

// ── 审核路由 AdminGate（与成本/模型配置一致：路由级 admin 门禁）───────────
// 路由 orgs.$org.review 现把 ReviewBoardView 包进 AdminGate；此处复现该组合，
// 断言非 admin 见门禁文案而非看板，admin 见看板。
describe("review board admin gate (route-level)", () => {
  it("non-admin sees the admin-required message instead of the board", () => {
    render(
      <AdminGate role={{ isAdmin: false, isLoading: false }}>
        <ReviewBoardView {...baseProps({ isAdmin: false })} />
      </AdminGate>,
    )
    expect(screen.getByText("需要管理员权限")).toBeInTheDocument()
    expect(screen.queryByText(/待审 2/)).not.toBeInTheDocument()
  })

  it("admin sees the board through the gate", () => {
    render(
      <AdminGate role={{ isAdmin: true, isLoading: false }}>
        <ReviewBoardView {...baseProps()} />
      </AdminGate>,
    )
    expect(screen.getByText(/待审 2/)).toBeInTheDocument()
    expect(screen.queryByText("需要管理员权限")).not.toBeInTheDocument()
  })
})
