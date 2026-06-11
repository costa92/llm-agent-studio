import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react"
import {
  getAccessToken,
  setAccessToken,
  ApiError,
} from "@/lib/apiClient"
import type { LoginResponse } from "@/lib/types"

// AuthProvider 管理登录/登出与 access token 生命周期。
// access token = Authorization: Bearer，仅存内存（apiClient 模块变量，不进 localStorage）。
// 登录：POST /api/auth/login {email,password} + credentials:include → {access_token,expires_in} + Set-Cookie 刷新令牌。
// 登出：POST /api/auth/logout（X-CSRF:1 + credentials:include）→ 204 清 cookie；前端无论成败都清内存 token。
interface AuthContextValue {
  isAuthenticated: boolean
  login: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  // 初始态：进程内已有 token（如热更新保留）即视为已认证；冷启动为 null。
  const [isAuthenticated, setIsAuthenticated] = useState(
    () => getAccessToken() != null,
  )

  const login = useCallback(async (email: string, password: string) => {
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify({ email, password }),
    })
    if (!res.ok) {
      // 凭据错 → 401；交由上层（登录视图）映射为文案。
      throw new ApiError(res.status, await res.text())
    }
    const data = (await res.json()) as LoginResponse
    setAccessToken(data.access_token)
    setIsAuthenticated(true)
  }, [])

  const logout = useCallback(async () => {
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        headers: { "X-CSRF": "1" },
        credentials: "include",
      })
    } catch {
      // 登出请求失败也无所谓——本地 token 仍要清，会话即作废。
    } finally {
      setAccessToken(null)
      setIsAuthenticated(false)
    }
  }, [])

  const value = useMemo<AuthContextValue>(
    () => ({ isAuthenticated, login, logout }),
    [isAuthenticated, login, logout],
  )

  return <AuthContext value={value}>{children}</AuthContext>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (ctx == null) {
    throw new Error("useAuth must be used within an AuthProvider")
  }
  return ctx
}
