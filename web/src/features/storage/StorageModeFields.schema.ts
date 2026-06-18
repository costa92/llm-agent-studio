import { z } from "zod"
import type { StorageConfig, StorageMode } from "@/lib/types"

// mode 标签：localfs（本地磁盘）+ 三家对象存储（S3 兼容 / 阿里云 OSS / 腾讯云 COS）+ GitHub 仓库。
export const MODE_LABELS: Record<StorageMode, string> = {
  localfs: "本地磁盘",
  s3: "Amazon S3 / S3 兼容",
  oss: "阿里云 OSS",
  cos: "腾讯云 COS",
  github: "GitHub 仓库",
}
export const MODES: StorageMode[] = ["localfs", "s3", "oss", "cos", "github"]

// rhf+zod 表单 schema（供 StorageModeFields + FormDialog 共享）。
// 每个 mode 的必填字段不同（discriminated 校验走 superRefine）：
//   localfs：无必填（publicPrefix 可空）。
//   s3：bucket + endpoint 必填。
//   oss（阿里云）：bucket + endpoint 必填。
//   cos（腾讯云）：bucket + region 必填（endpoint 可空，私有云才覆盖）。
//   github：accessKeyId(owner) + bucket(repo) 必填。
// secret 永远可空：空 = 保留既有 secret（已配置时）；非空 = 替换。
export const formSchema = z
  .object({
    name: z.string().trim().min(1, "请填写配置名称"),
    mode: z.enum(["localfs", "s3", "oss", "cos", "github"]),
    endpoint: z.string().trim(),
    region: z.string().trim(),
    bucket: z.string().trim(),
    accessKeyId: z.string().trim(),
    secret: z.string(),
    publicPrefix: z.string().trim(),
    useSsl: z.boolean(),
    enabled: z.boolean(),
  })
  .superRefine((v, ctx) => {
    if (v.mode === "s3" || v.mode === "oss") {
      if (v.bucket === "")
        ctx.addIssue({ path: ["bucket"], code: z.ZodIssueCode.custom, message: "请填写 Bucket" })
      if (v.endpoint === "")
        ctx.addIssue({ path: ["endpoint"], code: z.ZodIssueCode.custom, message: "请填写 Endpoint" })
    }
    if (v.mode === "cos") {
      if (v.bucket === "")
        ctx.addIssue({ path: ["bucket"], code: z.ZodIssueCode.custom, message: "请填写 Bucket（name-appid）" })
      if (v.region === "")
        ctx.addIssue({ path: ["region"], code: z.ZodIssueCode.custom, message: "请填写 Region（如 ap-guangzhou）" })
    }
    if (v.mode === "github") {
      if (v.accessKeyId === "")
        ctx.addIssue({ path: ["accessKeyId"], code: z.ZodIssueCode.custom, message: "请填写 Owner（GitHub 用户/组织）" })
      if (v.bucket === "")
        ctx.addIssue({ path: ["bucket"], code: z.ZodIssueCode.custom, message: "请填写 Repo（仓库名）" })
    }
  })

export type FormValues = z.infer<typeof formSchema>

// initial → 表单默认值。secret 始终留空（空 = 保留既有）；hasSecret 决定提示文案。
export function defaultsFor(initial: StorageConfig | null | undefined): FormValues {
  return {
    name: initial?.name ?? "",
    mode: initial?.mode ?? "localfs",
    endpoint: initial?.endpoint ?? "",
    region: initial?.region ?? "",
    bucket: initial?.bucket ?? "",
    accessKeyId: initial?.accessKeyId ?? "",
    secret: "", // 永不预填密钥：空 = 保留既有。
    publicPrefix: initial?.publicPrefix ?? "",
    useSsl: initial?.useSsl ?? true,
    enabled: initial?.enabled ?? true,
  }
}
