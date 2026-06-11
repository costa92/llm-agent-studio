import { vi } from "vitest"

// 构造一个 JSON Response，供 mock fetch 返回。
export function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
    ...init,
  })
}

// 路由处理器签名：拿到 (url, init) 决定返回什么 Response。
export type RouteHandler = (
  url: string,
  init: RequestInit | undefined,
) => Response | Promise<Response>

// 用一组按 path 匹配的处理器替换全局 fetch；返回 mock 以便断言调用。
// 匹配：处理器 key 出现在请求 URL 里（substring）即命中。
export function installFetchRoutes(
  routes: Record<string, RouteHandler>,
): ReturnType<typeof vi.fn> {
  const mock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.toString()
            : input.url
      for (const [path, handler] of Object.entries(routes)) {
        if (url.includes(path)) {
          return handler(url, init)
        }
      }
      throw new Error(`no mock route for ${url}`)
    },
  )
  vi.stubGlobal("fetch", mock)
  return mock
}
