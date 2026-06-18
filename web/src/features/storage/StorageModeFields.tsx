import { Controller, useFormContext } from "react-hook-form"
import { z } from "zod"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import { Badge } from "@/components/studio/Badge"
import { RevealSecretInput } from "@/features/common/crud"
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

// rhf+zod 表单 schema（从 StorageConfigPage 提取，供 StorageModeFields + FormDialog 共享）。
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

const fieldClass =
  "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"

export interface StorageModeFieldsProps {
  // 既有配置（用于 hasSecret 提示）；null/undefined = 新建。
  initial?: StorageConfig | null
  // org 覆盖区显示「停用 = 回退全局」提示；全局区不显示。
  isOrgScope?: boolean
  // id 前缀，避免同页多表单 id 冲突。默认 "form"。
  idPrefix?: string
}

// mode-conditional 字段块（通过 useFormContext 读写）。
// 包含：mode 下拉 + per-mode 条件字段（localfs/s3/oss/cos/github）+ enabled 开关。
// 不含 name 字段（由外层表单保留）。
export function StorageModeFields({
  initial,
  isOrgScope = false,
  idPrefix = "form",
}: StorageModeFieldsProps) {
  const {
    register,
    control,
    setValue,
    watch,
    formState: { errors },
  } = useFormContext<FormValues>()

  const mode = watch("mode")
  const useSsl = watch("useSsl")
  const enabled = watch("enabled")
  const hasSecret = initial?.hasSecret ?? false

  const fid = (s: string) => `${idPrefix}-sc-${s}`
  const isLocal = mode === "localfs"
  const isGithub = mode === "github"
  // 哪些 mode 暴露对象存储字段（endpoint/bucket/accessKey/secret）。github 字段集不同，单独分支。
  const showObjectFields = mode === "s3" || mode === "oss" || mode === "cos"
  const showRegion = mode === "s3" || mode === "cos"
  // endpoint：s3/oss 必填；cos 可空（私有云覆盖）。
  const endpointRequired = mode === "s3" || mode === "oss"
  // endpoint 占位文案随 mode 切换（OSS/COS 形态差异较大）。
  const endpointPlaceholder =
    mode === "oss"
      ? "如 oss-cn-hangzhou.aliyuncs.com"
      : mode === "cos"
        ? "可空；私有云才覆盖"
        : "如 https://s3.amazonaws.com"

  return (
    <>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={fid("mode")}>存储类型 (mode)</Label>
        <select id={fid("mode")} {...register("mode")} className={fieldClass}>
          {MODES.map((m) => (
            <option key={m} value={m}>
              {MODE_LABELS[m]}
            </option>
          ))}
        </select>
        {isLocal && (
          <p className="text-[11.5px] text-text-3">本地磁盘（开发/默认）。</p>
        )}
      </div>

      {/* localfs：仅 publicPrefix（可空）。对象存储 mode 隐藏此区下方再展示。 */}
      {isLocal && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={fid("prefix")}>公开前缀 publicPrefix（可空）</Label>
          <input
            id={fid("prefix")}
            placeholder="如 /assets"
            {...register("publicPrefix")}
            className={fieldClass}
          />
        </div>
      )}

      {showObjectFields && (
        <>
          {(endpointRequired || mode === "cos") && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={fid("endpoint")}>
                {endpointRequired ? "Endpoint（必填）" : "Endpoint（可空）"}
              </Label>
              <input
                id={fid("endpoint")}
                aria-invalid={errors.endpoint != null}
                placeholder={endpointPlaceholder}
                {...register("endpoint")}
                className={fieldClass}
              />
              {errors.endpoint && (
                <p className="text-[12px] text-danger">{errors.endpoint.message}</p>
              )}
            </div>
          )}

          {showRegion && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={fid("region")}>
                {mode === "cos" ? "Region（必填）" : "Region（可空）"}
              </Label>
              <input
                id={fid("region")}
                aria-invalid={errors.region != null}
                placeholder={mode === "cos" ? "如 ap-guangzhou" : "如 us-east-1"}
                {...register("region")}
                className={fieldClass}
              />
              {errors.region && (
                <p className="text-[12px] text-danger">{errors.region.message}</p>
              )}
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            <Label htmlFor={fid("bucket")}>
              {mode === "cos" ? "Bucket（name-appid，必填）" : "Bucket（必填）"}
            </Label>
            <input
              id={fid("bucket")}
              aria-invalid={errors.bucket != null}
              placeholder={mode === "cos" ? "如 my-bucket-1250000000" : "bucket 名称"}
              {...register("bucket")}
              className={fieldClass}
            />
            {errors.bucket && (
              <p className="text-[12px] text-danger">{errors.bucket.message}</p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor={fid("accesskey")}>
              {mode === "cos" ? "AccessKeyId（SecretId）" : "AccessKeyId"}
            </Label>
            <input
              id={fid("accesskey")}
              autoComplete="off"
              placeholder={mode === "cos" ? "腾讯云 SecretId" : "access key id"}
              {...register("accessKeyId")}
              className={fieldClass}
            />
          </div>

          {/* secret/密钥：使用 RevealSecretInput，内部 aria-label="密钥输入"。
              hasSecret 时显示「已配置密钥」徽标 + RevealSecretInput alreadySet 展示留空提示。 */}
          <div className="flex flex-col gap-1.5">
            <span className="flex items-center gap-2">
              <Label>{mode === "cos" ? "Secret（SecretKey）" : "Secret"}</Label>
              {hasSecret && <Badge variant="done">已配置密钥</Badge>}
            </span>
            <Controller
              name="secret"
              control={control}
              render={({ field }) => (
                <RevealSecretInput
                  value={field.value}
                  onChange={field.onChange}
                  placeholder={mode === "cos" ? "腾讯云 SecretKey" : "secret access key"}
                  alreadySet={hasSecret}
                />
              )}
            />
            {!hasSecret && (
              <p className="text-[11.5px] text-text-3">密钥仅写入、加密存储，不会回显。</p>
            )}
          </div>

          {/* useSsl 仅 S3 兼容端点有意义。 */}
          {mode === "s3" && (
            <label className="flex items-center gap-2 text-[13px] text-text-1">
              <Checkbox
                checked={useSsl}
                onCheckedChange={(v) => setValue("useSsl", v === true)}
              />
              使用 SSL（HTTPS）
            </label>
          )}
        </>
      )}

      {/* github：字段含义与对象存储不同（accessKeyId=owner / bucket=repo / region=branch /
          publicPrefix=路径前缀 / secret=PAT / endpoint=API base）；共享 register 仅在本分支渲染，
          与 showObjectFields 分支互斥，故不会重复渲染同一字段。 */}
      {isGithub && (
        <>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={fid("gh-owner")}>Owner（owner，必填）</Label>
            <input
              id={fid("gh-owner")}
              autoComplete="off"
              aria-invalid={errors.accessKeyId != null}
              placeholder="用户或组织名"
              {...register("accessKeyId")}
              className={fieldClass}
            />
            {errors.accessKeyId && (
              <p className="text-[12px] text-danger">{errors.accessKeyId.message}</p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor={fid("gh-repo")}>Repo（必填）</Label>
            <input
              id={fid("gh-repo")}
              aria-invalid={errors.bucket != null}
              placeholder="仓库名"
              {...register("bucket")}
              className={fieldClass}
            />
            {errors.bucket && (
              <p className="text-[12px] text-danger">{errors.bucket.message}</p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor={fid("gh-branch")}>Branch（默认 main）</Label>
            <input
              id={fid("gh-branch")}
              placeholder="main"
              {...register("region")}
              className={fieldClass}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor={fid("gh-prefix")}>路径前缀 (path prefix，可空)</Label>
            <input
              id={fid("gh-prefix")}
              placeholder="如 assets"
              {...register("publicPrefix")}
              className={fieldClass}
            />
          </div>

          {/* token/PAT：使用 RevealSecretInput，内部 aria-label="密钥输入"。 */}
          <div className="flex flex-col gap-1.5">
            <span className="flex items-center gap-2">
              <Label>GitHub Token (PAT)</Label>
              {hasSecret && <Badge variant="done">已配置密钥</Badge>}
            </span>
            <Controller
              name="secret"
              control={control}
              render={({ field }) => (
                <RevealSecretInput
                  value={field.value}
                  onChange={field.onChange}
                  placeholder="ghp_… / fine-grained PAT"
                  alreadySet={hasSecret}
                />
              )}
            />
            {!hasSecret && (
              <p className="text-[11.5px] text-text-3">
                Token 仅写入、加密存储，不会回显（仅用于写入）。
              </p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor={fid("gh-api")}>API base（GitHub Enterprise 可选）</Label>
            <input
              id={fid("gh-api")}
              placeholder="https://api.github.com"
              {...register("endpoint")}
              className={fieldClass}
            />
            <p className="text-[11.5px] text-text-3">
              这是 <b>GitHub API 根</b>（默认 https://api.github.com ；GHE 用
              https://&lt;host&gt;[/&lt;subpath&gt;]/api/v3）。<b>不要填</b>{" "}
              jsDelivr（cdn.jsdelivr.net）或 raw.githubusercontent.com
              等 CDN / 直链主机——它们不是 API，写入会立刻 EOF，asset 6/6 跑挂。
            </p>
          </div>

          <p className="text-[11.5px] text-text-3">
            公开仓库；资产经 raw.githubusercontent.com 直链取件；单文件 ≤ ~100MB（图片适用，视频不适合）；Token 仅用于写入。
          </p>
        </>
      )}

      <label className="flex items-center gap-2 text-[13px] text-text-1">
        <Checkbox
          checked={enabled}
          onCheckedChange={(v) => setValue("enabled", v === true)}
        />
        启用该存储
      </label>
      {isOrgScope && (
        <p className="text-[11.5px] text-text-3">
          停用本组织存储将回退到全局默认。
        </p>
      )}
    </>
  )
}
