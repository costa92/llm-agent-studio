import { describe, it, expect, vi, beforeEach } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { SelectedAssetPanel } from "./SelectedAssetPanel"
import type { AssetDetail, Asset } from "@/lib/types"

// 隔离外部依赖：AssetThumb 走网络、apiJSON 走 fetch、toast 走 sonner。
// vi.mock 调用被提升到文件顶部，不能引用后续声明的变量——用 vi.hoisted 提前创建共享引用。
const { toast } = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))
vi.mock("./AssetThumb.tsx", () => ({ AssetThumb: () => <div data-testid="thumb" /> }))
vi.mock("@/lib/apiClient", () => ({
  apiJSON: vi.fn(),
  getAccessToken: () => "tok",
  ApiError: class ApiError extends Error { status = 0 },
}))
vi.mock("sonner", () => ({ toast }))

function asset(over: Partial<Asset> = {}): Asset {
  return {
    id: "as1", projectId: "p1", shotId: "s1", todoId: "t1", type: "image",
    blobKey: "", url: "", prompt: "", style: "", provider: "openai", model: "dall-e",
    status: "pending_acceptance", version: 2, parentAssetId: "", tags: [],
    prescreenScore: 0, prescreenFlags: [], prescreenNote: "", externalJobId: "", ...over,
  }
}
function detail(over: Partial<Asset> = {}): AssetDetail {
  return { asset: asset(over), versions: [] }
}

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

beforeEach(() => vi.clearAllMocks())

describe("SelectedAssetPanel", () => {
  it("renders metadata (type/version/status) + thumb", () => {
    wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin={false} detail={detail()} />)
    expect(screen.getByTestId("thumb")).toBeInTheDocument()
    expect(screen.getByText(/image/)).toBeInTheDocument()
    expect(screen.getByText(/v2/)).toBeInTheDocument()
  })
  it("shows accept/reject for admin when pending_acceptance", () => {
    wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin detail={detail()} />)
    expect(screen.getByRole("button", { name: /采纳/ })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /拒绝/ })).toBeInTheDocument()
  })
  it("hides accept/reject for non-admin even when pending", () => {
    wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin={false} detail={detail()} />)
    expect(screen.queryByRole("button", { name: /采纳/ })).not.toBeInTheDocument()
  })
  it("falls back to AssetPreviewActions when not pending", () => {
    wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin detail={detail({ status: "accepted" })} />)
    expect(screen.queryByRole("button", { name: /采纳/ })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: /复制链接/ })).toBeInTheDocument()
  })
  it("calls accept hook and toasts on success", async () => {
    const { apiJSON } = await import("@/lib/apiClient")
    ;(apiJSON as ReturnType<typeof vi.fn>).mockResolvedValue({ id: "as1", status: "accepted" })
    const user = userEvent.setup()
    wrap(<SelectedAssetPanel org="acme" assetId="as1" isAdmin detail={detail()} />)
    await user.click(screen.getByRole("button", { name: /采纳/ }))
    expect(apiJSON).toHaveBeenCalledWith("/api/assets/as1/accept", { method: "POST" })
  })
})
