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
  hooks.useReviewQueue.mockReturnValue({
    data: [],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  })
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
    hooks.useReviewQueue.mockReturnValue({
      data: [asset({ id: "a1" }), asset({ id: "a2" })],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    })
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
    hooks.useReviewQueue.mockReturnValue({
      data: [asset({ id: "a1" }), asset({ id: "a2" })],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    })
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

  it("single accept toasts 已采纳 and clears the selection", async () => {
    const acceptMutate = vi.fn(
      (_id: string, opts: { onSuccess: () => void }) => opts.onSuccess(),
    )
    hooks.useAccept.mockReturnValue({
      mutate: acceptMutate,
      mutateAsync: vi.fn().mockResolvedValue({}),
      isPending: false,
    })
    hooks.useReviewQueue.mockReturnValue({
      data: [asset({ id: "a1" })],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    })
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
