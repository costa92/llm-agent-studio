// 内存 access token —— 仅存进程内存，绝不进 localStorage（防 XSS）。
// access token = Authorization: Bearer（authz middleware 只读此头，无 cookie 回退）。
// refresh token = httpOnly cookie authz_refresh（POST /api/auth/refresh，必须带 X-CSRF:1 + credentials:include）。
import type { LoginResponse } from "./types"

let accessToken: string | null = null

// 会话存在标记（非 httpOnly，可被 JS 读）：登录成功后置位，登出/刷新彻底失败时清除。
// 唯一用途——让冷启动的 tryRestoreSession 在「从未登录过」时跳过 /api/auth/refresh，
// 消除未认证用户控制台里的 401 噪声。绝不存任何令牌，只是一个「可能有会话」的提示位。
const SESSION_MARKER = "studio_has_session"

function markSession(): void {
  try {
    localStorage.setItem(SESSION_MARKER, "1")
  } catch {
    // localStorage 不可用（隐私模式等）→ 退化为「无标记」，tryRestoreSession 走保守跳过。
  }
}

// 清除会话标记：登出，或刷新令牌确认失效时调用（会话真的没了）。
export function clearSessionMarker(): void {
  try {
    localStorage.removeItem(SESSION_MARKER)
  } catch {
    // 同上：不可用即视为已无标记。
  }
}

function hasSessionMarker(): boolean {
  try {
    return localStorage.getItem(SESSION_MARKER) === "1"
  } catch {
    return false
  }
}

// 置位内存 token。非空即视为「有会话」→ 记标记（登录/注册/刷新成功共用此入口）。
// 传 null 只清内存 token，不动标记——冷启动内存丢 token 不等于登出，会话仍可能可恢复。
export function setAccessToken(token: string | null): void {
  accessToken = token
  if (token != null) markSession()
}

export function getAccessToken(): string | null {
  return accessToken
}

// 401 刷新失败 / 未认证时抛出，供登录流跳转。
export class AuthError extends Error {
  constructor(message = "authentication required") {
    super(message)
    this.name = "AuthError"
  }
}

// 非 2xx 响应（除 401 已被 apiFetch 内部处理）抛出。
export class ApiError extends Error {
  readonly status: number
  readonly body: string
  constructor(status: number, body: string) {
    super(`api error ${status}`)
    this.name = "ApiError"
    this.status = status
    this.body = body
  }
}

// single-flight：并发 401 共享同一个 refresh promise，只打一次刷新接口。
let refreshPromise: Promise<string> | null = null

// POST /api/auth/refresh —— double-submit CSRF（X-CSRF:1）+ httpOnly cookie（credentials:include）。
// 成功轮换内存 token 并返回新 token；失败清 token 抛 AuthError。
async function refresh(): Promise<string> {
  if (refreshPromise) return refreshPromise

  refreshPromise = (async () => {
    const res = await fetch("/api/auth/refresh", {
      method: "POST",
      headers: { "X-CSRF": "1" },
      credentials: "include",
    })
    if (!res.ok) {
      setAccessToken(null)
      clearSessionMarker() // 刷新令牌确认失效 → 会话真的没了，抹掉标记免得下次冷启动再打一次
      throw new AuthError("refresh failed")
    }
    const data = (await res.json()) as LoginResponse
    setAccessToken(data.access_token)
    return data.access_token
  })()

  try {
    return await refreshPromise
  } finally {
    refreshPromise = null
  }
}

// 冷启动/硬刷新静默恢复会话：内存 token 已没了，但 httpOnly 刷新 cookie 仍可能有效。
// 仅当内存无 token 时打一次 refresh（复用上面的 single-flight）；成功置内存 token 返回 true，
// 任何失败返回 false（绝不抛）。幂等：已有 token 直接返回 true，不发请求——可安全反复调用。
export async function tryRestoreSession(): Promise<boolean> {
  if (getAccessToken() != null) return true
  // 从未登录过（无会话标记）→ 直接跳过刷新，避免未认证冷启动打出 401 噪声。
  if (!hasSessionMarker()) return false
  try {
    await refresh()
    return true
  } catch {
    return false
  }
}

function withAuth(init: RequestInit | undefined, token: string | null): RequestInit {
  const headers = new Headers(init?.headers)
  if (token) headers.set("Authorization", `Bearer ${token}`)
  return { ...init, headers }
}

// 注入 Bearer；401 → single-flight refresh 一次 → 用新 token 重试一次。
// 刷新失败（refresh() 抛 AuthError）向上传播；其余非 401 状态原样返回（由 apiJSON 决定是否抛）。
export async function apiFetch(
  path: string,
  init?: RequestInit,
): Promise<Response> {
  const res = await fetch(path, withAuth(init, accessToken))
  if (res.status !== 401) return res

  // 401 → 刷新（并发共享）→ 用新 token 重试一次。
  const newToken = await refresh()
  return fetch(path, withAuth(init, newToken))
}

// typed 便捷包装：非 2xx 抛 ApiError，2xx 解析 JSON。
export async function apiJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await apiFetch(path, init)
  if (!res.ok) {
    throw new ApiError(res.status, await res.text())
  }
  return (await res.json()) as T
}
