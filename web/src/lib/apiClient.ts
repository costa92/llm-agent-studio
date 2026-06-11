// 内存 access token —— 仅存进程内存，绝不进 localStorage（防 XSS）。
// T4 仅落地 token getter/setter 骨架，供 `_authed` 路由 beforeLoad 门禁使用。
// T5 在此扩展 apiFetch + single-flight refresh-on-401；T6 在登录流里调用 setAccessToken。
let accessToken: string | null = null

export function setAccessToken(token: string | null): void {
  accessToken = token
}

export function getAccessToken(): string | null {
  return accessToken
}
