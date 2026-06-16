import { useState } from "react"
import { useForm, useWatch } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { Loader2 } from "lucide-react"
import { toast } from "sonner"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { Badge } from "@/components/studio/Badge"
import { ApiError } from "@/lib/apiClient"
import {
  useStorageConfigs,
  useCreateStorageConfig,
  useUpdateStorageConfig,
  useDeleteStorageConfig,
  useSetDefaultStorageConfig,
} from "./api"
import type {
  StorageConfig,
  StorageMode,
  UpsertStorageConfigInput,
} from "@/lib/types"

// mode 标签：localfs（本地磁盘）+ 三家对象存储（S3 兼容 / 阿里云 OSS / 腾讯云 COS）+ GitHub 仓库。
export const MODE_LABELS: Record<StorageMode, string> = {
  localfs: "本地磁盘",
  s3: "Amazon S3 / S3 兼容",
  oss: "阿里云 OSS",
  cos: "腾讯云 COS",
  github: "GitHub 仓库",
}
const MODES: StorageMode[] = ["localfs", "s3", "oss", "cos", "github"]

// rhf+zod 表单。每个 mode 的必填字段不同（discriminated 校验走 superRefine）：
//   localfs：无必填（publicPrefix 可空）。
//   s3：bucket + endpoint 必填。
//   oss（阿里云）：bucket + endpoint 必填。
//   cos（腾讯云）：bucket + region 必填（endpoint 可空，私有云才覆盖）。
//   github：accessKeyId(owner) + bucket(repo) 必填（region=branch / endpoint=API base 可空；
//           token 走 secret，留空=保留既有，后端 New 在使用时强制要求）。
// secret 永远可空：空 = 保留既有 secret（已配置时）；非空 = 替换。
const formSchema = z
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
      // owner=accessKeyId、repo=bucket 必填（branch/token/API base 在 schema 层可空）。
      if (v.accessKeyId === "")
        ctx.addIssue({ path: ["accessKeyId"], code: z.ZodIssueCode.custom, message: "请填写 Owner（GitHub 用户/组织）" })
      if (v.bucket === "")
        ctx.addIssue({ path: ["bucket"], code: z.ZodIssueCode.custom, message: "请填写 Repo（仓库名）" })
    }
  })

type FormValues = z.infer<typeof formSchema>

// initial → 表单默认值。secret 始终留空（空 = 保留既有）；hasSecret 决定提示文案。
function defaultsFor(initial: StorageConfig | null | undefined): FormValues {
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

export interface StorageConfigFormProps {
  // 既有配置（用于预填 + hasSecret 提示）；null = 尚未配置。
  initial: StorageConfig | null | undefined
  // 提交 → POST（新建）或 PUT（更新）；返回 Promise 让表单 await，400 由调用方 toast。
  onSubmit: (input: UpsertStorageConfigInput) => Promise<StorageConfig>
  // org 覆盖区显示「停用 = 回退全局」提示；全局区不显示。
  isOrgScope: boolean
  // id 前缀，避免同页多表单 id 冲突。默认 "form"。
  idPrefix?: string
}

// 单个存储配置表单：name + mode 下拉 + per-mode 条件字段 + write-only secret。
export function StorageConfigForm({ initial, onSubmit, isOrgScope, idPrefix = "form" }: StorageConfigFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    setValue,
    control,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: defaultsFor(initial),
  })

  const mode = useWatch({ control, name: "mode" })
  const useSsl = useWatch({ control, name: "useSsl" })
  const enabled = useWatch({ control, name: "enabled" })

  // 同页有多个表单时按 idPrefix 给字段 id 加前缀，避免重复 id 破坏 label 关联。
  const fid = (s: string) => `${idPrefix}-sc-${s}`
  const isLocal = mode === "localfs"
  const isGithub = mode === "github"
  // 哪些 mode 暴露对象存储字段（endpoint/bucket/accessKey/secret）。github 字段集不同，单独分支。
  const showObjectFields = mode === "s3" || mode === "oss" || mode === "cos"
  const showRegion = mode === "s3" || mode === "cos"
  // endpoint：s3/oss 必填；cos 可空（私有云覆盖）。
  const endpointRequired = mode === "s3" || mode === "oss"

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const sc = await onSubmit({
        name: values.name.trim(),
        mode: values.mode,
        endpoint: values.endpoint.trim(),
        region: values.region.trim(),
        bucket: values.bucket.trim(),
        accessKeyId: values.accessKeyId.trim(),
        secret: values.secret, // 空 = 保留既有；后端据此不改 secret_enc。
        useSsl: values.useSsl,
        publicPrefix: values.publicPrefix.trim(),
        enabled: values.enabled,
      })
      // 提交成功后刷新表单基线（hasSecret 等由父组件 refetch 重渲染驱动）。
      void sc
    } catch (err) {
      // 后端 400（缺加密密钥 / 校验失败）等 → 调用方 toast；此处兜底 inline 文案。
      setSubmitError(err instanceof Error ? "保存失败，请检查参数" : "保存失败")
    }
  })

  const fieldClass =
    "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"

  // endpoint 占位文案随 mode 切换（OSS/COS 形态差异较大）。
  const endpointPlaceholder =
    mode === "oss"
      ? "如 oss-cn-hangzhou.aliyuncs.com"
      : mode === "cos"
        ? "可空；私有云才覆盖"
        : "如 https://s3.amazonaws.com"

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={fid("name")}>配置名称</Label>
        <input
          id={fid("name")}
          placeholder="如 主存储桶"
          aria-invalid={errors.name != null}
          {...register("name")}
          className={fieldClass}
        />
        {errors.name && (
          <p className="text-[12px] text-danger">{errors.name.message}</p>
        )}
      </div>

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

          <div className="flex flex-col gap-1.5">
            <span className="flex items-center gap-2">
              <Label htmlFor={fid("secret")}>
                {mode === "cos" ? "Secret（SecretKey）" : "Secret"}
              </Label>
              {initial?.hasSecret && <Badge variant="done">已配置密钥</Badge>}
            </span>
            <input
              id={fid("secret")}
              type="password"
              autoComplete="off"
              placeholder={mode === "cos" ? "腾讯云 SecretKey" : "secret access key"}
              {...register("secret")}
              className={fieldClass}
            />
            <p className="text-[11.5px] text-text-3">
              {initial?.hasSecret
                ? "留空保持不变（已配置密钥）；填写则替换为新密钥。"
                : "密钥仅写入、加密存储，不会回显。"}
            </p>
          </div>

          {/* useSsl 仅 S3 兼容端点有意义（OSS/COS 走 HTTPS 默认，但保留开关不破坏既有约定）。 */}
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

          <div className="flex flex-col gap-1.5">
            <span className="flex items-center gap-2">
              <Label htmlFor={fid("gh-token")}>GitHub Token (PAT)</Label>
              {initial?.hasSecret && <Badge variant="done">已配置密钥</Badge>}
            </span>
            <input
              id={fid("gh-token")}
              type="password"
              autoComplete="off"
              placeholder="ghp_… / fine-grained PAT"
              {...register("secret")}
              className={fieldClass}
            />
            <p className="text-[11.5px] text-text-3">
              {initial?.hasSecret
                ? "留空保持不变（已配置密钥）；填写则替换为新 Token。"
                : "Token 仅写入、加密存储，不会回显（仅用于写入）。"}
            </p>
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

      {submitError && (
        <p role="alert" className="text-[12px] text-danger">
          {submitError}
        </p>
      )}

      <div className="flex justify-end">
        <Button type="submit" variant="amber" disabled={isSubmitting}>
          {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          保存
        </Button>
      </div>
    </form>
  )
}

// 关键字段展示：按 mode 返回最具代表性的一个字段值。
function keyField(config: StorageConfig): string {
  switch (config.mode) {
    case "s3":
    case "oss":
    case "cos":
      return config.bucket
    case "github":
      return `${config.accessKeyId}/${config.bucket}`
    case "localfs":
      return config.publicPrefix || "—"
  }
}

export interface StorageConfigsTableProps {
  configs: StorageConfig[]
  onCreate: () => void
  onEdit: (config: StorageConfig) => void
  onDelete: (config: StorageConfig) => void
  onSetDefault: (config: StorageConfig) => void
}

export function StorageConfigsTable({
  configs,
  onCreate,
  onEdit,
  onDelete,
  onSetDefault,
}: StorageConfigsTableProps) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <UiButton size="sm" onClick={onCreate}>
          新增配置
        </UiButton>
      </div>
      <table className="w-full text-[13px] text-text-1">
        <thead>
          <tr className="border-b border-line text-left text-[12px] text-text-3">
            <th className="pb-2 pr-4 font-medium">名称</th>
            <th className="pb-2 pr-4 font-medium">类型</th>
            <th className="pb-2 pr-4 font-medium">关键字段</th>
            <th className="pb-2 pr-4 font-medium">启用</th>
            <th className="pb-2 pr-4 font-medium">默认</th>
            <th className="pb-2 pr-4 font-medium">密钥</th>
            <th className="pb-2 font-medium">操作</th>
          </tr>
        </thead>
        <tbody>
          {configs.map((config) => (
            <tr key={config.id} data-slot="sc-row" className="border-b border-line/50">
              <td className="py-2 pr-4">{config.name}</td>
              <td className="py-2 pr-4">{MODE_LABELS[config.mode]}</td>
              <td className="py-2 pr-4 font-mono text-[12px] text-text-2">{keyField(config)}</td>
              <td className="py-2 pr-4">
                <Badge variant={config.enabled ? "running" : "pending"}>
                  {config.enabled ? "已启用" : "已停用"}
                </Badge>
              </td>
              <td className="py-2 pr-4">
                {config.isDefault ? (
                  <Badge variant="done">默认</Badge>
                ) : (
                  <UiButton size="sm" onClick={() => onSetDefault(config)}>
                    设为默认
                  </UiButton>
                )}
              </td>
              <td className="py-2 pr-4">
                {config.hasSecret ? <Badge variant="done">已配置</Badge> : null}
              </td>
              <td className="py-2">
                <span className="flex items-center gap-2">
                  <UiButton size="sm" onClick={() => onEdit(config)}>
                    编辑
                  </UiButton>
                  <UiButton size="sm" variant="destructive" onClick={() => onDelete(config)}>
                    删除
                  </UiButton>
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export interface StorageConfigViewProps {
  org: string
}

// 组织存储配置页（admin-only）：多配置列表 + 新增/编辑/删除/设为默认。
export function StorageConfigView({ org }: StorageConfigViewProps) {
  const configsQuery = useStorageConfigs(org)
  const createMutation = useCreateStorageConfig(org)
  const updateMutation = useUpdateStorageConfig(org)
  const deleteMutation = useDeleteStorageConfig(org)
  const setDefaultMutation = useSetDefaultStorageConfig(org)

  const [dialogMode, setDialogMode] = useState<"create" | "edit" | null>(null)
  const [editTarget, setEditTarget] = useState<StorageConfig | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<StorageConfig | null>(null)

  function openCreate() {
    setEditTarget(null)
    setDialogMode("create")
  }

  function openEdit(config: StorageConfig) {
    setEditTarget(config)
    setDialogMode("edit")
  }

  function closeDialog() {
    setDialogMode(null)
    setEditTarget(null)
  }

  async function handleFormSubmit(input: UpsertStorageConfigInput): Promise<StorageConfig> {
    if (dialogMode === "create") {
      const sc = await createMutation.mutateAsync(input)
      toast.success("存储配置已创建")
      closeDialog()
      return sc
    } else {
      const sc = await updateMutation.mutateAsync({ id: editTarget!.id, input })
      toast.success("存储配置已更新")
      closeDialog()
      return sc
    }
  }

  function handleDelete(config: StorageConfig) {
    setDeleteTarget(config)
  }

  function confirmDelete() {
    if (!deleteTarget) return
    const id = deleteTarget.id
    setDeleteTarget(null)
    deleteMutation.mutateAsync(id).catch((err: unknown) => {
      if (err instanceof ApiError && err.status === 409) {
        toast.error("该存储有历史素材引用，请改用停用")
      } else {
        toast.error("删除失败")
      }
    })
  }

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">存储配置</h1>
        <p className="text-[12px] text-text-3">
          配置本组织专属的资产对象存储后端（本地磁盘 / S3 / 阿里云 OSS / 腾讯云 COS / GitHub 仓库）；
          未配置或停用时回退到全局默认。密钥仅写入、加密存储，不会回显。
        </p>
      </header>

      {configsQuery.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">存储配置加载失败</p>
          <Button variant="ghost" onClick={() => void configsQuery.refetch()}>
            重试
          </Button>
        </div>
      ) : configsQuery.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : (
        <StorageConfigsTable
          configs={configsQuery.data ?? []}
          onCreate={openCreate}
          onEdit={openEdit}
          onDelete={handleDelete}
          onSetDefault={(config) => setDefaultMutation.mutate(config.id)}
        />
      )}

      {/* 新增/编辑 Dialog */}
      <Dialog open={dialogMode !== null} onOpenChange={(open) => { if (!open) closeDialog() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{dialogMode === "create" ? "新增存储配置" : "编辑存储配置"}</DialogTitle>
            <DialogDescription>
              {dialogMode === "create"
                ? "填写新存储配置的参数，密钥仅加密存储、不回显。"
                : "修改存储配置参数；留空密钥字段则保持不变。"}
            </DialogDescription>
          </DialogHeader>
          <StorageConfigForm
            key={dialogMode === "create" ? "new" : (editTarget?.id ?? "edit")}
            initial={editTarget}
            onSubmit={handleFormSubmit}
            isOrgScope
            idPrefix={dialogMode === "create" ? "new" : (editTarget?.id ?? "edit")}
          />
        </DialogContent>
      </Dialog>

      {/* 删除确认 Dialog */}
      <Dialog open={deleteTarget !== null} onOpenChange={(open) => { if (!open) setDeleteTarget(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认删除存储配置？</DialogTitle>
            <DialogDescription>
              删除「{deleteTarget?.name}」后无法撤销。如该存储有历史素材引用，建议改用停用。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <UiButton variant="outline" onClick={() => setDeleteTarget(null)}>
              取消
            </UiButton>
            <UiButton variant="destructive" onClick={confirmDelete}>
              确认删除
            </UiButton>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
