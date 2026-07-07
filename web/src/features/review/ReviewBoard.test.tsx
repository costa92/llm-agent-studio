import { beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ReviewBoard } from "./ReviewBoard"
import type { Asset } from "@/lib/types"

// 隔离外部依赖：toast、AssetThumb（走网络）、review/projects hooks、rbac 探针。
const { toast } = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))
vi.mock("sonner", () => ({ toast }))

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

// 容器内部用 useRole 取 isAdmin（AdminGate 由路由/宿主负责）——固定 admin。
vi.mock("@/app/rbac", () => ({
  useRole: () => ({ isAdmin: true, isLoading: false }),
}))

const { useProjectsMock } = vi.hoisted(() => ({ useProjectsMock: vi.fn() }))
vi.mock("@/features/projects/api", () => ({
  useProjects: () => useProjectsMock(),
}))

const hooks = vi.hoisted(() => ({
  useReviewQueue: vi.fn(),
  useAsset: vi.fn(),
  useAccept: vi.fn(),
  useReject: vi.fn(),
  useRegenerate: vi.fn(),
}))
vi.mock("./api", () => hooks)

function asset(over: Partial<Asset> = {}): Asset {
  return {
    id: "as1",
    projectId: "p1",
    shotId: "",
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
    parentAssetId: "",
    tags: [],
    prescreenScore: 0,
    prescreenFlags: [],
    prescreenNote: "",
    externalJobId: "",
    ...over,
  }
}

// useReviewQueue 现为 infinite query：容器读 data.pages 并 flatten。测试里把资产数组
// 包成单页信封（next_cursor 空 = 无下一页）。
function queueResult(items: Asset[]) {
  return {
    data: { pages: [{ items, next_cursor: "" }], pageParams: [""] },
    isLoading: false,
    isError: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
    refetch: vi.fn(),
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  useResolvedAssetUrlMock.mockReturnValue({ url: null, loading: false })
  useProjectsMock.mockReturnValue({ data: [] })
  hooks.useAsset.mockReturnValue({ data: undefined, isLoading: false })
  hooks.useReject.mockReturnValue({
    mutate: vi.fn(),
    mutateAsync: vi.fn(),
    isPending: false,
  })
  hooks.useRegenerate.mockReturnValue({
    mutate: vi.fn(),
    mutateAsync: vi.fn(),
    isPending: false,
  })
  hooks.useAccept.mockReturnValue({
    mutate: vi.fn(),
    mutateAsync: vi.fn().mockResolvedValue({}),
    isPending: false,
  })
  hooks.useReviewQueue.mockReturnValue(queueResult([]))
})

describe("ReviewBoard container", () => {
  it("resolves the project hex id to its name in the filter chip", () => {
    useProjectsMock.mockReturnValue({ data: [{ id: "p-hex", name: "我的绘本" }] })
    render(
      <ReviewBoard
        org="acme"
        projectFilter="p-hex"
        selectedId={null}
        onSelect={vi.fn()}
        onClearProjectFilter={vi.fn()}
      />,
    )
    expect(screen.getByText(/正在筛选项目：我的绘本/)).toBeInTheDocument()
  })

  it("serially accepts checked assets in queue order and toasts the summary", async () => {
    const order: string[] = []
    const acceptMutateAsync = vi.fn((id: string) => {
      order.push(id)
      return Promise.resolve({ id, status: "accepted" })
    })
    hooks.useAccept.mockReturnValue({
      mutate: vi.fn(),
      mutateAsync: acceptMutateAsync,
      isPending: false,
    })
    hooks.useReviewQueue.mockReturnValue(
      queueResult([asset({ id: "a1" }), asset({ id: "a2" })]),
    )
    const user = userEvent.setup()
    render(
      <ReviewBoard org="acme" projectFilter={null} selectedId={null} onSelect={vi.fn()} />,
    )
    const checks = screen.getAllByRole("checkbox")
    await user.click(checks[0])
    await user.click(checks[1])
    await user.click(screen.getByRole("button", { name: /采纳选中\(2\)/ }))

    await waitFor(() => expect(acceptMutateAsync).toHaveBeenCalledTimes(2))
    expect(order).toEqual(["a1", "a2"])
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("已采纳 2 张"))
  })

  it("reports partial failures without aborting the batch", async () => {
    const acceptMutateAsync = vi
      .fn()
      .mockResolvedValueOnce({})
      .mockRejectedValueOnce(new Error("409"))
    hooks.useAccept.mockReturnValue({
      mutate: vi.fn(),
      mutateAsync: acceptMutateAsync,
      isPending: false,
    })
    hooks.useReviewQueue.mockReturnValue(
      queueResult([asset({ id: "a1" }), asset({ id: "a2" })]),
    )
    const user = userEvent.setup()
    render(
      <ReviewBoard org="acme" projectFilter={null} selectedId={null} onSelect={vi.fn()} />,
    )
    await user.click(screen.getByRole("button", { name: /采纳全部待审\(2\)/ }))

    await waitFor(() => expect(acceptMutateAsync).toHaveBeenCalledTimes(2))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("已采纳 1 张 · 1 张失败"),
    )
  })

  it("serially rejects checked assets AFTER the batch confirm, toasting the summary", async () => {
    const order: string[] = []
    const rejectMutateAsync = vi.fn((id: string) => {
      order.push(id)
      return Promise.resolve({ id, status: "rejected" })
    })
    hooks.useReject.mockReturnValue({
      mutate: vi.fn(),
      mutateAsync: rejectMutateAsync,
      isPending: false,
    })
    hooks.useReviewQueue.mockReturnValue(
      queueResult([asset({ id: "a1" }), asset({ id: "a2" })]),
    )
    const user = userEvent.setup()
    render(
      <ReviewBoard org="acme" projectFilter={null} selectedId={null} onSelect={vi.fn()} />,
    )
    const checks = screen.getAllByRole("checkbox")
    await user.click(checks[0])
    await user.click(checks[1])
    // 退回选中 → 先弹批量确认（终态守卫），确认前不提交。
    await user.click(screen.getByRole("button", { name: /退回选中\(2\)/ }))
    expect(rejectMutateAsync).not.toHaveBeenCalled()
    await user.click(screen.getByRole("button", { name: "确认退回" }))

    await waitFor(() => expect(rejectMutateAsync).toHaveBeenCalledTimes(2))
    expect(order).toEqual(["a1", "a2"])
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("已退回 2 张"))
  })

  it("single accept toasts 已采纳 and clears the selection", async () => {
    const acceptMutate = vi.fn(
      (_id: string, opts: { onSuccess: () => void }) => opts.onSuccess(),
    )
    hooks.useAccept.mockReturnValue({
      mutate: acceptMutate,
      mutateAsync: vi.fn().mockResolvedValue({}),
      isPending: false,
    })
    hooks.useReviewQueue.mockReturnValue(queueResult([asset({ id: "a1" })]))
    hooks.useAsset.mockReturnValue({
      data: { asset: asset({ id: "a1" }), versions: [] },
      isLoading: false,
    })
    const onSelect = vi.fn()
    const user = userEvent.setup()
    render(
      <ReviewBoard org="acme" projectFilter={null} selectedId="a1" onSelect={onSelect} />,
    )
    // 抽屉内采纳按钮（✓ 采纳，kbd A）——用 kbd 提示区分于批量条按钮。
    await user.click(screen.getByRole("button", { name: /✓ 采纳/ }))
    expect(acceptMutate).toHaveBeenCalledWith("a1", expect.any(Object))
    expect(toast.success).toHaveBeenCalledWith("已采纳")
    expect(onSelect).toHaveBeenCalledWith(null)
  })
})
