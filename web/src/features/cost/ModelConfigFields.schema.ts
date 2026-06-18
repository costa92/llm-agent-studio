import { z } from "zod"
import type { CatalogEntry, ModelConfig } from "@/lib/types"

// kind 标签：text（文本/对话）+ image/video/audio。
export const KIND_LABELS: Record<string, string> = {
  text: "文本",
  image: "图像",
  video: "视频",
  audio: "音频",
  chat: "对话",
}
export const DEFERRED_KINDS = new Set(["video", "audio"])

// 自定义 OpenAI 兼容 provider：纯自由 model + 必填 base_url。
export const COMPATIBLE_PROVIDER = "openai-compatible"

// rhf+zod 创建表单（BYO key）：provider（含 openai-compatible）+ kind + 自由 model
// + 可选 base_url + 可选 API key（写入即加密、永不回显）+ enabled/isDefault + params JSON。
// 校验：openai-compatible 必填 base_url + model（兼容端点离不开 base_url）。
export const formSchema = z
  .object({
    provider: z.string().min(1, "请选择 provider"),
    kind: z.string().min(1, "请选择类型"),
    model: z.string().trim().min(1, "请填写 model"),
    baseUrl: z.string().trim(),
    apiKey: z.string(),
    enabled: z.boolean(),
    isDefault: z.boolean(),
    // 可选 params JSON 文本。空 = 不带 params；非法 JSON 校验报错。
    paramsText: z.string(),
  })
  .refine(
    (v) => v.provider !== COMPATIBLE_PROVIDER || v.baseUrl.length > 0,
    { path: ["baseUrl"], message: "请填写 Base URL（OpenAI 兼容端点必填）" },
  )

export type FormValues = z.infer<typeof formSchema>

// catalog 里出现过的不重复 provider（保序）。
export function providersFor(catalog: CatalogEntry[]): string[] {
  return [...new Set(catalog.map((e) => e.provider))]
}

// initial 配置 → 表单默认值。
export function defaultsFor(
  initial: ModelConfig | null | undefined,
  providers: string[],
): FormValues {
  return {
    provider: initial?.provider ?? providers[0] ?? COMPATIBLE_PROVIDER,
    kind: initial?.kind ?? "image",
    model: initial?.model ?? "",
    baseUrl: initial?.baseUrl ?? "",
    apiKey: initial?.apiKey ?? "",
    enabled: initial?.enabled ?? true,
    isDefault: initial?.isDefault ?? false,
    paramsText: initial?.params ? JSON.stringify(initial.params) : "",
  }
}

// paramsText → params 对象。空 = undefined（不带）；非法 JSON / 非对象 → 抛出含文案的 Error。
export function parseParamsText(text: string): Record<string, unknown> | undefined {
  const trimmed = text.trim()
  if (trimmed === "") return undefined
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    throw new Error("参数不是合法 JSON")
  }
  if (typeof parsed !== "object" || parsed == null || Array.isArray(parsed)) {
    throw new Error("参数必须是 JSON 对象")
  }
  return parsed as Record<string, unknown>
}
