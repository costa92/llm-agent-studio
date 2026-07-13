// 从 http-status 产物内容 {"status":N} 解析状态码；解析失败返回 null。
export function parseHttpStatus(content: string): number | null {
  try {
    const obj = JSON.parse(content) as { status?: unknown }
    return typeof obj.status === "number" ? obj.status : null
  } catch {
    return null
  }
}
