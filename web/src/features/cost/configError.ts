import { ApiError } from "@/lib/apiClient"

// 模型配置创建错误 → toast 文案映射。
//   400（含密钥型 param → models.ErrSecretParam，或 provider/model 缺失）：
//     m2handlers.go:269 缺 provider/model → "bad request: provider+model required"；
//     models/store.go:135 含密钥 key → ErrSecretParam（"...params must not contain credentials..."）。
//   两者都是 400 —— 按 body 文案区分：含 "credentials" 即密钥拒绝。
//   其余 = 通用失败。
export function modelConfigErrorMessage(err: unknown): string {
  if (err instanceof ApiError && err.status === 400) {
    // 服务端未配置 STUDIO_CONFIG_ENC_KEY → 无法加密保存 per-config API key（ErrEncUnavailable）。
    if (err.body.includes("encryption") || err.body.includes("STUDIO_CONFIG_ENC_KEY")) {
      return "服务端未配置密钥加密 (STUDIO_CONFIG_ENC_KEY)，无法保存 API key"
    }
    if (err.body.includes("credentials")) {
      return "参数不能包含密钥（请将 API key 填入下方密钥字段，而非 params）"
    }
    return "请填写 provider 和 model"
  }
  return "保存失败，请重试"
}
