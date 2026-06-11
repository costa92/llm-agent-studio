import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { resolveAssetUrl } from "./assetThumb"
import { setAccessToken } from "@/lib/apiClient"

// jsdom 不实现 URL.createObjectURL/revokeObjectURL —— 装上可断言的 spy。
beforeEach(() => {
  let n = 0
  URL.createObjectURL = vi.fn(() => `blob:mock/${n++}`)
  URL.revokeObjectURL = vi.fn()
})

afterEach(() => {
  vi.restoreAllMocks()
  setAccessToken(null)
})

// 构造一个最小的 followed-redirect 最终响应：ok=true + blob()（不真用 Response，
// 避免 undici/jsdom Blob→stream 兼容问题）。res.url 为最终签名 URL（仅作真实语义说明）。
function blobResponse(): Response {
  return {
    ok: true,
    status: 200,
    url: "/api/blob/k1?sig=abc&exp=123",
    blob: vi.fn().mockResolvedValue(new Blob(["bytes"], { type: "image/png" })),
  } as unknown as Response
}

describe("resolveAssetUrl (authed fetch follows 302 → blob object URL)", () => {
  it("follows the redirect (redirect:'follow'), reads bytes, returns a blob object URL", async () => {
    // 真实浏览器语义：fetch 自动跟随 302 到签名 URL，最终是 200 + body。
    // 不依赖 manual-redirect 的 Location 头（那在真实浏览器里恒为 opaque/null）。
    setAccessToken("tok-1")
    const fetchMock = vi.fn().mockResolvedValue(blobResponse())
    vi.stubGlobal("fetch", fetchMock)

    const url = await resolveAssetUrl("as1")

    expect(url).toBe("blob:mock/0")
    // 请求命中 /content，且按真实浏览器语义 redirect:"follow"（非 manual）。
    expect(String(fetchMock.mock.calls[0][0])).toContain("/api/assets/as1/content")
    expect(fetchMock.mock.calls[0][1]?.redirect).toBe("follow")
    // /content 在 auth middleware 后 —— 必须带内存 Bearer（apiFetch 注入）。
    const headers = new Headers(fetchMock.mock.calls[0][1]?.headers)
    expect(headers.get("Authorization")).toBe("Bearer tok-1")
    expect(URL.createObjectURL).toHaveBeenCalledOnce()
  })

  it("returns null when the content fetch fails (non-2xx)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("not found", { status: 404 })),
    )
    expect(await resolveAssetUrl("as2")).toBeNull()
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })
})
