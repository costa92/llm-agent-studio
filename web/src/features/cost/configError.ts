import { ApiError } from "@/lib/apiClient"

// 模型配置创建错误 → toast 文案映射。
//   400（含密钥型 param → models.ErrSecretParam，或 provider/model 缺失）：
//     m2handlers.go:269 缺 provider/model → "bad request: provider+model required"；
//     models/store.go:135 含密钥 key → ErrSecretParam（"...params must not contain credentials..."）。
//   两者都是 400 —— 按 body 文案区分：含 "credentials" 即密钥拒绝。
//   其余 = 通用失败。
export function modelConfigErrorMessage(err: unknown): string {
  if (err instanceof ApiError && err.status === 400) {
    if (err.body.includes("credentials")) {
      return "参数不能包含密钥（API key 由服务端管理，请移除密钥字段）"
    }
    return "请填写 provider 和 model"
  }
  return "保存失败，请重试"
}
