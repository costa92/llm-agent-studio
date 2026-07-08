import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import {
  QueryCache,
  QueryClient,
  QueryClientProvider,
  MutationCache,
} from "@tanstack/react-query"
import { createRouter, RouterProvider } from "@tanstack/react-router"
import { toast } from "sonner"
import { routeTree } from "./routeTree.gen"
import { AuthProvider } from "./app/auth"
import { ThemeProvider } from "./app/theme"
import { ApiError, AuthError } from "./lib/apiClient"
import "./index.css"

const router = createRouter({ routeTree })

// 全局会话过期处理：任一 query/mutation 抛 AuthError（apiClient 刷新令牌确认失效时抛）
// → 跳 /login（携当前 path 供登录后回跳）+ 提示，而非把用户留在静默 no-op 的僵尸空壳。
// 长驻页面（token 过期后未导航）也会在下一次 refetch/交互触发 AuthError 时被接住。
// dedupe：并发多 query 同时失效只跳一次；已在 /login 直接跳过（防回环与提示风暴）。
let handlingAuthError = false
function handleAuthError(error: unknown): void {
  if (!(error instanceof AuthError)) return
  if (handlingAuthError) return
  if (router.state.location.pathname === "/login") return
  handlingAuthError = true
  toast.error("会话已过期，请重新登录")
  void router
    .navigate({ to: "/login", search: { redirect: router.state.location.href } })
    .finally(() => {
      handlingAuthError = false
    })
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: handleAuthError }),
  mutationCache: new MutationCache({ onError: handleAuthError }),
  defaultOptions: {
    queries: {
      // 鉴权/资源不存在类错误重试无意义，只会刷屏控制台（裸 403/404 噪声）——快速失败落到
      // error 分支交由视图渲染 access-denied/not-found 空态。其余错误保留默认 3 次重试。
      retry: (failureCount, error) => {
        if (error instanceof AuthError) return false
        if (
          error instanceof ApiError &&
          (error.status === 401 || error.status === 403 || error.status === 404)
        ) {
          return false
        }
        return failureCount < 3
      },
    },
  },
})

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <AuthProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </AuthProvider>
    </ThemeProvider>
  </StrictMode>,
)
