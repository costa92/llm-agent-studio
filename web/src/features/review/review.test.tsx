import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { resolveReviewAction, isInputTarget } from "./keyboard"
import { hitlErrorMessage } from "./hitlError"
import { ApiError } from "@/lib/apiClient"
import { AdminGate } from "@/features/cost/AdminGate"
import { ReviewBoardView, type ReviewBoardViewProps } from "./ReviewBoardPage"
import type { Asset, AssetDetail } from "@/lib/types"

// AssetThumb / AssetMedia 走 authed fetch → blob object URL；jsdom 无网络。
// 默认 stub 为 null（显"加载中…/不可用"占位），避免触网；非图片媒体测试里再
// 用 mockReturnValueOnce 给一个可解析 URL，让 <video>/<audio> 真正渲染。
const { useResolvedAssetUrlMock } = vi.hoisted(() => ({
  useResolvedAssetUrlMock: vi.fn(() => ({
    url: null as string | null,
    loading: false,
  })),
}))
vi.mock("@/features/workflow/assetThumb", () => ({
  resolveAssetUrl: vi.fn().mockResolvedValue(null),
  useResolvedAssetUrl: () => useResolvedAssetUrlMock(),
}))

beforeEach(() => {
  useResolvedAssetUrlMock.mockReturnValue({ url: null, loading: false })
})

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
    projectFilter: null,
    onClearProjectFilter: vi.fn(),
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

  // P0-4：触屏/移动端可达性——三个动作按钮（采纳/退回/重生成）必须显式渲染，
  // 不能只靠键盘 A/R/E。admin + 选中资产时三按钮齐现，点击各自触发对应回调。
  it("renders all three HITL action buttons (touch-reachable) and each invokes its handler", async () => {
    const onAccept = vi.fn()
    const onReject = vi.fn()
    const onRegenerate = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({
          selectedId: "as1",
          detail: makeDetail(),
          onAccept,
          onReject,
          onRegenerate,
        })}
      />,
    )
    // 三按钮齐现（非仅键盘提示）。
    const acceptBtn = screen.getByRole("button", { name: /采纳/ })
    const rejectBtn = screen.getByRole("button", { name: /退回/ })
    const regenBtn = screen.getByRole("button", { name: /改 Prompt 重生成/ })
    expect(acceptBtn).toBeInTheDocument()
    expect(rejectBtn).toBeInTheDocument()
    expect(regenBtn).toBeInTheDocument()

    // 采纳 → onAccept(id)。
    await user.click(acceptBtn)
    expect(onAccept).toHaveBeenCalledWith("as1")

    // 退回 → 确认弹窗 → onReject(id)。
    await user.click(screen.getByRole("button", { name: /退回/ }))
    await user.click(screen.getByRole("button", { name: "确认退回" }))
    expect(onReject).toHaveBeenCalledWith("as1")

    // 重生成 → 编辑态 → 确认重生成 → onRegenerate(id, prompt)。
    await user.click(screen.getByRole("button", { name: /改 Prompt 重生成/ }))
    await user.click(screen.getByRole("button", { name: "确认重生成" }))
    expect(onRegenerate).toHaveBeenCalledWith("as1", "guofeng teahouse dusk")
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

  // T4：?project= 在看板头部显示筛选指示 + 清除入口。
  it("shows the project-filter chip and a clear control when projectFilter is set", async () => {
    const onClearProjectFilter = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({ projectFilter: "proj-1", onClearProjectFilter })}
      />,
    )
    expect(screen.getByText(/正在筛选项目/)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /查看全部/ }))
    expect(onClearProjectFilter).toHaveBeenCalledTimes(1)
  })

  it("does not show the project-filter chip when no projectFilter (org-wide)", () => {
    render(<ReviewBoardView {...baseProps()} />)
    expect(screen.queryByText(/正在筛选项目/)).not.toBeInTheDocument()
  })

  // T4：非图片资产（video/audio）在详情 Drawer 里可播放/有类型标识，而非破图。
  it("renders a <video> player for a video asset in the drawer", () => {
    useResolvedAssetUrlMock.mockReturnValue({ url: "blob:fake-video", loading: false })
    const detail = {
      asset: makeAsset({ type: "video" }),
      versions: [makeAsset({ type: "video" })],
    }
    render(<ReviewBoardView {...baseProps({ selectedId: "as1", detail })} />)
    // Sheet/Drawer 走 Radix portal → 渲染到 document.body，需全局查询。
    expect(document.querySelector("video")).not.toBeNull()
    useResolvedAssetUrlMock.mockReturnValue({ url: null, loading: false })
  })

  it("renders an <audio> player for an audio asset in the drawer", () => {
    useResolvedAssetUrlMock.mockReturnValue({ url: "blob:fake-audio", loading: false })
    const detail = {
      asset: makeAsset({ type: "audio" }),
      versions: [makeAsset({ type: "audio" })],
    }
    render(<ReviewBoardView {...baseProps({ selectedId: "as1", detail })} />)
    expect(document.querySelector("audio")).not.toBeNull()
    useResolvedAssetUrlMock.mockReturnValue({ url: null, loading: false })
  })

  // 音频卡在网格里可直接试听（懒加载「试听」按钮），无需先打开抽屉。
  it("renders a 试听 control for an audio asset card in the grid", () => {
    render(<ReviewBoardView {...baseProps({ queue: [makeAsset({ id: "aud1", type: "audio" })] })} />)
    expect(screen.getByRole("button", { name: "试听" })).toBeInTheDocument()
  })

  // T7：退回必须显式确认——点退回不直接触发 onReject，先开确认弹窗；
  //   确认才触发一次，取消零次。
  it("reject requires explicit confirmation: opens a dialog, fires onReject only on confirm", async () => {
    const onReject = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({ selectedId: "as1", detail: makeDetail(), onReject })}
      />,
    )
    await user.click(screen.getByRole("button", { name: /退回/ }))
    // 点退回后尚未提交。
    expect(onReject).not.toHaveBeenCalled()
    // 弹出确认窗口。
    await user.click(screen.getByRole("button", { name: "确认退回" }))
    expect(onReject).toHaveBeenCalledTimes(1)
    expect(onReject).toHaveBeenCalledWith("as1")
  })

  it("reject confirmation can be canceled with zero side effects", async () => {
    const onReject = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({ selectedId: "as1", detail: makeDetail(), onReject })}
      />,
    )
    await user.click(screen.getByRole("button", { name: /退回/ }))
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(onReject).not.toHaveBeenCalled()
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

// ── 项目名解析（chip 显示项目名而非 hex id，解析不到回退 id）───────────────
describe("ReviewBoardView project-name chip", () => {
  it("prefers projectName over the raw id in the filter chip", () => {
    render(
      <ReviewBoardView
        {...baseProps({ projectFilter: "proj-hex", projectName: "我的项目" })}
      />,
    )
    expect(screen.getByText(/正在筛选项目：我的项目/)).toBeInTheDocument()
  })

  it("falls back to the project id when projectName is unresolved", () => {
    render(<ReviewBoardView {...baseProps({ projectFilter: "proj-hex" })} />)
    expect(screen.getByText(/正在筛选项目：proj-hex/)).toBeInTheDocument()
  })
})

// ── 变体 A：inline 双栏模式（run 内融合抽屉）──────────────────────────────
// inlineDetail 时详情内联为右栏，不再走 overlay Sheet（规避运行抽屉 Dialog 内叠 Sheet
// 的嵌套模态）；未选时右栏出占位。默认（Sheet）行为须不变。
describe("ReviewBoardView inline dual-column (variant A)", () => {
  it("unselected: shows the right-rail placeholder and no Sheet overlay", () => {
    render(<ReviewBoardView {...baseProps({ inlineDetail: true, selectedId: null })} />)
    expect(screen.getByText("选择左侧资产查看详情")).toBeInTheDocument()
    // inline 模式不挂 Sheet（无 overlay 抽屉）。
    expect(document.querySelector('[data-slot="sheet-content"]')).toBeNull()
  })

  it("selected: renders the detail inline (not via a Sheet portal)", () => {
    render(
      <ReviewBoardView
        {...baseProps({ inlineDetail: true, selectedId: "as1", detail: makeDetail() })}
      />,
    )
    // 详情正文就地渲染（KV provider·model + 血缘 v1）。
    expect(screen.getByText("openai · gpt-image-1")).toBeInTheDocument()
    expect(screen.getByText("v1")).toBeInTheDocument()
    // 关键：inline 详情不落在 Sheet portal 里，占位也已让位。
    expect(document.querySelector('[data-slot="sheet-content"]')).toBeNull()
    expect(screen.queryByText("选择左侧资产查看详情")).not.toBeInTheDocument()
  })

  it("default (non-inline) still routes the detail through the Sheet overlay", () => {
    render(
      <ReviewBoardView
        {...baseProps({ selectedId: "as1", detail: makeDetail() })}
      />,
    )
    // 默认宿主行为不变：详情走 Radix Sheet（portal 到 body）。
    expect(document.querySelector('[data-slot="sheet-content"]')).not.toBeNull()
  })
})

// ── 审完闭环空态 CTA（注入回调 → 按钮；org 级无回调 → 纯文案）─────────────
describe("ReviewBoardView empty-state close-loop CTAs", () => {
  it("shows 返回作品 / 看成品预览 CTAs when callbacks are provided", async () => {
    const onBackToWork = vi.fn()
    const onOpenPreview = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [],
          projectName: "我的绘本",
          onBackToWork,
          onOpenPreview,
        })}
      />,
    )
    await user.click(screen.getByRole("button", { name: /返回作品《我的绘本》/ }))
    expect(onBackToWork).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole("button", { name: "看成品预览" }))
    expect(onOpenPreview).toHaveBeenCalledTimes(1)
  })

  it("keeps the empty state as plain text when no close-loop callbacks (org-wide)", () => {
    render(<ReviewBoardView {...baseProps({ queue: [] })} />)
    expect(screen.getByText("所有素材都处理完了")).toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: /返回作品/ }),
    ).not.toBeInTheDocument()
  })
})

// ── 分镜分组（变体 A 队列形态）+ 批量采纳（前端串行，onAcceptMany 启用）─────
describe("ReviewBoardView shot grouping + batch accept", () => {
  it("groups the queue by shot and 采纳本分镜 accepts that group in queue order", async () => {
    const onAcceptMany = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [
            makeAsset({ id: "a1", shotId: "shotA" }),
            makeAsset({ id: "a2", shotId: "shotB" }),
            makeAsset({ id: "a3", shotId: "shotA" }),
          ],
          onAcceptMany,
        })}
      />,
    )
    // 首次出现次序：shotA=分镜1，shotB=分镜2。
    expect(screen.getByText("分镜 1")).toBeInTheDocument()
    expect(screen.getByText("分镜 2")).toBeInTheDocument()
    const shotButtons = screen.getAllByRole("button", { name: "采纳本分镜" })
    expect(shotButtons).toHaveLength(2)
    // 分镜 1 = shotA = a1 + a3（保持队列顺序）。
    await user.click(shotButtons[0])
    expect(onAcceptMany).toHaveBeenCalledWith(["a1", "a3"])
  })

  it("renders a flat grid (no shot headers) when all shotId are empty", () => {
    render(
      <ReviewBoardView
        {...baseProps({ queue: [makeAsset({ id: "a1", shotId: "" })] })}
      />,
    )
    expect(screen.queryByText(/分镜 /)).not.toBeInTheDocument()
  })

  it("has no batch UI / checkboxes when onAcceptMany is absent", () => {
    render(<ReviewBoardView {...baseProps({ queue: [makeAsset({ shotId: "" })] })} />)
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: /采纳全部待审/ }),
    ).not.toBeInTheDocument()
  })

  it("accepts checked assets via 采纳选中 and clears the checkbox set", async () => {
    const onAcceptMany = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [
            makeAsset({ id: "a1", shotId: "" }),
            makeAsset({ id: "a2", shotId: "" }),
          ],
          onAcceptMany,
        })}
      />,
    )
    const checks = screen.getAllByRole("checkbox")
    await user.click(checks[0])
    expect(screen.getByText("已选 1")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /采纳选中\(1\)/ }))
    expect(onAcceptMany).toHaveBeenCalledWith(["a1"])
    // 采纳后勾选集清空。
    expect(screen.getByText("已选 0")).toBeInTheDocument()
  })

  it("采纳全部待审 accepts the whole queue in order", async () => {
    const onAcceptMany = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [
            makeAsset({ id: "a1", shotId: "" }),
            makeAsset({ id: "a2", shotId: "" }),
          ],
          onAcceptMany,
        })}
      />,
    )
    await user.click(screen.getByRole("button", { name: /采纳全部待审\(2\)/ }))
    expect(onAcceptMany).toHaveBeenCalledWith(["a1", "a2"])
  })
})

// ── P2 来源可见性：org 级混杂队列按项目分组 + 批量退回 ──────────────────
describe("ReviewBoardView project grouping + batch reject", () => {
  it("groups a cross-project queue by project and shows each source project name", () => {
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [
            makeAsset({ id: "a1", projectId: "p1", shotId: "" }),
            makeAsset({ id: "a2", projectId: "p2", shotId: "" }),
            makeAsset({ id: "a3", projectId: "p1", shotId: "" }),
          ],
          projectNames: { p1: "猫的冒险", p2: "产品定义" },
        })}
      />,
    )
    // 表头显示来源项目名（非无名「分镜 N」）。
    expect(screen.getByText("猫的冒险")).toBeInTheDocument()
    expect(screen.getByText("产品定义")).toBeInTheDocument()
    expect(screen.queryByText(/分镜 /)).not.toBeInTheDocument()
  })

  it("falls back to the project id when the name is unresolved", () => {
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [
            makeAsset({ id: "a1", projectId: "p1", shotId: "" }),
            makeAsset({ id: "a2", projectId: "p2", shotId: "" }),
          ],
          projectNames: { p1: "猫的冒险" },
        })}
      />,
    )
    expect(screen.getByText("猫的冒险")).toBeInTheDocument()
    // p2 未解析 → 回退 id，不崩。
    expect(screen.getByText("p2")).toBeInTheDocument()
  })

  it("退回本项目 opens the batch-confirm then rejects that project's assets", async () => {
    const onRejectMany = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [
            makeAsset({ id: "a1", projectId: "p1", shotId: "" }),
            makeAsset({ id: "a2", projectId: "p2", shotId: "" }),
            makeAsset({ id: "a3", projectId: "p1", shotId: "" }),
          ],
          projectNames: { p1: "猫的冒险", p2: "产品定义" },
          onAcceptMany: vi.fn(),
          onRejectMany,
        })}
      />,
    )
    // 项目 p1 有两个待退回资产。
    await user.click(screen.getAllByRole("button", { name: "退回本项目" })[0])
    expect(onRejectMany).not.toHaveBeenCalled()
    await user.click(screen.getByRole("button", { name: "确认退回" }))
    expect(onRejectMany).toHaveBeenCalledWith(["a1", "a3"])
  })

  it("退回选中 rejects the checked set through the batch confirm", async () => {
    const onRejectMany = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoardView
        {...baseProps({
          queue: [
            makeAsset({ id: "a1", shotId: "" }),
            makeAsset({ id: "a2", shotId: "" }),
          ],
          onAcceptMany: vi.fn(),
          onRejectMany,
        })}
      />,
    )
    await user.click(screen.getAllByRole("checkbox")[0])
    await user.click(screen.getByRole("button", { name: /退回选中\(1\)/ }))
    await user.click(screen.getByRole("button", { name: "确认退回" }))
    expect(onRejectMany).toHaveBeenCalledWith(["a1"])
  })
})

// ── P1 分页：header 计数与「加载更多」──────────────────────────────────
describe("ReviewBoardView pagination", () => {
  it("labels the header 已加载 (not 待审) while more pages remain, and shows 加载更多", () => {
    const onLoadMore = vi.fn()
    render(
      <ReviewBoardView
        {...baseProps({ hasNextPage: true, onLoadMore })}
      />,
    )
    // 还有下一页 → 不谎报总数。
    expect(screen.getByText(/已加载 2…/)).toBeInTheDocument()
    expect(screen.queryByText(/待审 2/)).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "加载更多" })).toBeInTheDocument()
  })

  it("labels the header 待审 and hides 加载更多 once fully loaded", () => {
    render(<ReviewBoardView {...baseProps({ hasNextPage: false })} />)
    expect(screen.getByText(/待审 2/)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "加载更多" })).not.toBeInTheDocument()
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
