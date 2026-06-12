import { ApiError } from "@/lib/apiClient"

// 存储配置保存错误 → toast 文案映射。
//   400：要存 secret 但服务端未配 STUDIO_CONFIG_ENC_KEY（storageconfig.ErrEncUnavailable），
//        或 validate 失败（非法 mode / 缺 bucket+endpoint 等）。两者都是客户端可纠正的 400。
//   按 body 文案区分：含 STUDIO_CONFIG_ENC_KEY 即缺加密密钥；其余按校验失败兜底。
export function storageConfigErrorMessage(err: unknown): string {
  if (err instanceof ApiError && err.status === 400) {
    if (err.body.includes("STUDIO_CONFIG_ENC_KEY")) {
      return "STUDIO_CONFIG_ENC_KEY 未配置，无法保存密钥"
    }
    return "保存失败，请检查 mode / endpoint / bucket 等字段"
  }
  if (err instanceof ApiError && err.status === 404) {
    return "未找到该存储配置（可能已删除）"
  }
  return "保存失败，请重试"
}
