import { afterEach, describe, expect, it, vi } from "vitest"
import { resolveAssetUrl } from "./assetThumb"
import { setAccessToken } from "@/lib/apiClient"

afterEach(() => {
  vi.restoreAllMocks()
  setAccessToken(null)
})

describe("resolveAssetUrl (302 Location → signed URL)", () => {
  it("reads the Location header from a manual-redirect 302", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(null, {
        status: 302,
        headers: { Location: "https://blob.example/signed?sig=abc&exp=123" },
      }),
    )
    vi.stubGlobal("fetch", fetchMock)

    const url = await resolveAssetUrl("as1")

    expect(url).toBe("https://blob.example/signed?sig=abc&exp=123")
    // 请求走 redirect:"manual"（读 Location 而非自动跟随）。
    expect(String(fetchMock.mock.calls[0][0])).toContain("/api/assets/as1/content")
    expect(fetchMock.mock.calls[0][1]?.redirect).toBe("manual")
  })

  it("returns null when no Location is exposed (opaqueredirect)", async () => {
    // opaqueredirect 在浏览器是 status 0、无可读 header；jsdom 不允许构造 status 0，
    // 用无 Location 头的响应等价模拟"读不到签名 URL"的降级路径。
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(null, { status: 200 })),
    )
    expect(await resolveAssetUrl("as2")).toBeNull()
  })
})
