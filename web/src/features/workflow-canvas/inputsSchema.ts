import { z } from "zod"
import type { InputField } from "@/lib/types"

// 设计期输入字段的前端校验，与后端 internal/runinputs.ValidateSchema 对齐：
// name 须 ^[A-Za-z_][A-Za-z0-9_]*$；select 必带非空 options。校验风格复刻
// WorkflowDialog.schema.ts（superRefine 首个问题即停）。后端 400 为兜底，
// 此处只为让用户在保存前看到行内提示。

// 类型下拉选项（UI 文案 → value 对齐后端 type allowlist）。
export const INPUT_FIELD_TYPES: { value: InputField["type"]; label: string }[] = [
  { value: "text", label: "单行文本" },
  { value: "textarea", label: "多行文本" },
  { value: "number", label: "数字" },
  { value: "select", label: "单选" },
]

// 目标下拉选项（value 对齐后端 target allowlist）。
export const INPUT_FIELD_TARGETS: { value: InputField["target"]; label: string }[] = [
  { value: "variable", label: "自由变量 {{input:name}}" },
  { value: "brief", label: "覆盖 Brief" },
  { value: "contentType", label: "覆盖 内容类型" },
  { value: "targetPlatform", label: "覆盖 目标平台" },
  { value: "style", label: "覆盖 风格" },
]

const nameRe = /^[A-Za-z_][A-Za-z0-9_]*$/

export const inputFieldSchema = z
  .object({
    name: z.string(),
    label: z.string().optional(),
    type: z.enum(["text", "textarea", "number", "select"]),
    target: z.enum([
      "variable",
      "brief",
      "contentType",
      "targetPlatform",
      "style",
    ]),
    options: z
      .array(z.object({ value: z.string(), label: z.string().optional() }))
      .optional(),
    default: z.string().optional(),
    required: z.boolean().optional(),
  })
  .superRefine((f, ctx) => {
    if (!nameRe.test(f.name)) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["name"],
        message: "字段名须匹配 ^[A-Za-z_][A-Za-z0-9_]*$",
      })
      return
    }
    if (f.type === "select" && !(f.options ?? []).some((o) => o.value.trim())) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["options"],
        message: "单选必须至少有一个非空选项",
      })
    }
  })

// 返回该字段第一处校验错误的中文描述，无问题返回 null（行内提示用）。
export function inputFieldError(field: InputField): string | null {
  const r = inputFieldSchema.safeParse(field)
  if (r.success) return null
  return r.error.issues[0]?.message ?? "字段配置无效"
}
