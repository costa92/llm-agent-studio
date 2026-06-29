import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import { ExportDialog } from "./ExportDialog"
import { setAccessToken } from "@/lib/apiClient"
import { jsonResponse } from "@/test/helpers"

// sonner 单源：mock toast 以断言 success/error 调用。
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
import { toast } from "sonner"

afterEach(() => {
  vi.clearAllMocks()
  vi.unstubAllGlobals()
  setAccessToken(null)
})

// 每个用例新建 QueryClient（retry:false），用 Provider 包裹被测组件。
function renderDialog(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(createElement(QueryClientProvider, { client }, ui))
}

describe("ExportDialog", () => {
  it("选择格式 + 开始导出 → POST .../exports 带选中格式，并进入轮询态", async () => {
    const calls: { url: string; init?: RequestInit }[] = []
    const fetchMock = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
        const url = String(input)
        calls.push({ url, init })
        if (init?.method === "POST") {
          return jsonResponse({ jobId: "job1" }, { status: 201 })
        }
        // 轮询：保持 pending（不自动落终态），便于断言「正在生成」态。
        return jsonResponse({
          id: "job1",
          format: "epub",
          status: "pending",
          createdAt: "t",
        })
      },
    )
    vi.stubGlobal("fetch", fetchMock)

    renderDialog(<ExportDialog projectId="p1" open onClose={() => {}} />)
    fireEvent.click(screen.getByRole("button", { name: "EPUB" }))
    fireEvent.click(screen.getByRole("button", { name: "开始导出" }))

    await waitFor(() =>
      expect(calls.some((c) => c.init?.method === "POST")).toBe(true),
    )
    const post = calls.find((c) => c.init?.method === "POST")!
    expect(post.url).toContain("/api/projects/p1/exports")
    expect(JSON.parse(String(post.init!.body))).toMatchObject({ format: "epub" })

    // 创建成功 → toast.success + 进入「正在生成」轮询态。
    await waitFor(() =>
      expect(screen.getByText(/正在生成/)).toBeInTheDocument(),
    )
    expect(toast.success).toHaveBeenCalled()
  })

  it("轮询 pending → done 后出现下载入口（href 指向 /api/exports/{jobId}/content）", async () => {
    let getCount = 0
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
        if (init?.method === "POST") {
          return jsonResponse({ jobId: "job1" }, { status: 201 })
        }
        getCount += 1
        const status = getCount >= 2 ? "done" : "pending"
        return jsonResponse({
          id: "job1",
          format: "pdf",
          status,
          sizeBytes: 1234,
          createdAt: "t",
        })
      },
    )
    vi.stubGlobal("fetch", fetchMock)

    renderDialog(<ExportDialog projectId="p1" open onClose={() => {}} />)
    fireEvent.click(screen.getByRole("button", { name: "开始导出" }))

    const link = await waitFor(
      () => screen.getByRole("link", { name: /下载/ }),
      { timeout: 4000 },
    )
    expect(link.getAttribute("href")).toContain("/api/exports/job1/content")
  }, 10000)

  it("轮询 failed → toast.error + 展示错误文案", async () => {
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
        if (init?.method === "POST") {
          return jsonResponse({ jobId: "job1" }, { status: 201 })
        }
        return jsonResponse({
          id: "job1",
          format: "pdf",
          status: "failed",
          error: "渲染超时",
          createdAt: "t",
        })
      },
    )
    vi.stubGlobal("fetch", fetchMock)

    renderDialog(<ExportDialog projectId="p1" open onClose={() => {}} />)
    fireEvent.click(screen.getByRole("button", { name: "开始导出" }))

    await waitFor(() => expect(screen.getByText(/渲染超时/)).toBeInTheDocument())
    expect(toast.error).toHaveBeenCalled()
  })
})
