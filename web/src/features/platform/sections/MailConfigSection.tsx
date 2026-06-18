import { Controller, useFormContext } from "react-hook-form"
import { z } from "zod"
import { toast } from "sonner"
import { Button } from "@/components/studio/Button"
import { Label } from "@/components/ui/label"
import { SingletonConfigForm, RevealSecretInput } from "@/features/common/crud"
import { ApiError } from "@/lib/apiClient"
import type { MailConfig } from "@/lib/types"
import { useGlobalMailConfig, useUpsertGlobalMailConfig } from "../api"

// rhf+zod 表单 schema：smtpPass 始终可空（write-only：留空表示保留原密码）。
const formSchema = z.object({
  smtpHost: z.string(),
  smtpPort: z.number(),
  smtpUser: z.string(),
  smtpPass: z.string(),
  smtpFrom: z.string(),
  enabled: z.boolean(),
})
type FormValues = z.infer<typeof formSchema>

const fieldClass =
  "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"

// config → 表单值（smtpPass 永远从空白开始——write-only，不回显）。
function toValues(config: MailConfig | null | undefined): FormValues {
  return {
    smtpHost: config?.smtpHost ?? "",
    smtpPort: config?.smtpPort ?? 587,
    smtpUser: config?.smtpUser ?? "",
    smtpPass: "",
    smtpFrom: config?.smtpFrom ?? "",
    enabled: config?.enabled ?? true,
  }
}

// 表单字段块（通过 useFormContext 读写），保留原两列网格布局。
function MailConfigFields({ hasSecret }: { hasSecret: boolean }) {
  const { register, setValue, watch, control } = useFormContext<FormValues>()
  const enabled = watch("enabled")

  return (
    <>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="smtp-host">SMTP 主机 (Host)</Label>
          <input
            id="smtp-host"
            placeholder="如 smtp.example.com"
            {...register("smtpHost")}
            className={fieldClass}
            required
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="smtp-port">SMTP 端口 (Port)</Label>
          <input
            id="smtp-port"
            type="number"
            placeholder="如 587"
            {...register("smtpPort", {
              setValueAs: (v) => parseInt(v as string) || 587,
            })}
            className={fieldClass}
            required
          />
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="smtp-user">SMTP 用户 (User)</Label>
          <input
            id="smtp-user"
            placeholder="如 user@example.com"
            {...register("smtpUser")}
            className={fieldClass}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="smtp-pass">
            SMTP 密码 (Password)
            {hasSecret && (
              <span className="ml-2 text-[11px] text-amber">(已配置加密密码)</span>
            )}
          </Label>
          <Controller
            name="smtpPass"
            control={control}
            render={({ field }) => (
              <RevealSecretInput
                id="smtp-pass"
                value={field.value}
                onChange={field.onChange}
                placeholder={hasSecret ? "•••••••• (留空保留原密码)" : "SMTP 密码"}
              />
            )}
          />
        </div>
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="smtp-from">发送人邮箱 (From)</Label>
        <input
          id="smtp-from"
          placeholder="如 no-reply@example.com"
          {...register("smtpFrom")}
          className={fieldClass}
          required
        />
      </div>

      <div className="flex items-center gap-2 mt-1">
        <input
          id="smtp-enabled"
          type="checkbox"
          checked={enabled}
          onChange={(e) => setValue("enabled", e.target.checked)}
          className="h-4 w-4 rounded border-line bg-bg-base text-amber focus:ring-amber"
        />
        <Label htmlFor="smtp-enabled" className="cursor-pointer">启用邮件验证码发送</Label>
      </div>
    </>
  )
}

// ── 全局邮件配置 ────────────────────────────────────────────────
export function MailConfigSection() {
  const mailConfig = useGlobalMailConfig()
  const upsertMail = useUpsertGlobalMailConfig()

  function handleSubmit(values: FormValues) {
    if (!values.smtpHost.trim()) {
      toast.error("SMTP 主机不能为空")
      return
    }

    // 留空密码 → 省略 smtpPass，后端保留原配置。
    const payload = {
      smtpHost: values.smtpHost.trim(),
      smtpPort: values.smtpPort,
      smtpUser: values.smtpUser.trim(),
      smtpFrom: values.smtpFrom.trim(),
      enabled: values.enabled,
      ...(values.smtpPass ? { smtpPass: values.smtpPass } : {}),
    }

    upsertMail.mutate(payload, {
      onSuccess: () => {
        toast.success("全局邮件配置已保存")
        void mailConfig.refetch()
      },
      onError: (err: unknown) => {
        if (err instanceof ApiError && err.status === 412) {
          toast.error("保存失败，配置 SMTP 密码需要 JWT_SECRET")
        } else {
          toast.error("保存失败，请检查参数或重试")
        }
      },
    })
  }

  // GET 失败时保留原错误-重试态（SingletonConfigForm 不建模 isError）。
  if (mailConfig.isError) {
    return (
      <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
        <header className="flex flex-col gap-1">
          <h2 className="font-heading text-[15px] font-semibold text-text-1">全局邮件配置</h2>
          <p className="text-[12px] text-text-3">
            配置平台用户注册验证码发送的全局 SMTP 邮件服务器。留空密码表示保留原配置。
          </p>
        </header>
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">全局邮件配置加载失败</p>
          <Button variant="ghost" onClick={() => void mailConfig.refetch()}>
            重试
          </Button>
        </div>
      </section>
    )
  }

  return (
    <SingletonConfigForm<FormValues>
      title="全局邮件配置"
      description="配置平台用户注册验证码发送的全局 SMTP 邮件服务器。留空密码表示保留原配置。"
      schema={formSchema}
      values={toValues(mailConfig.data)}
      isLoading={mailConfig.isLoading}
      submitLabel={upsertMail.isPending ? "保存中..." : "保存邮件配置"}
      submitting={upsertMail.isPending}
      onSubmit={handleSubmit}
    >
      <MailConfigFields hasSecret={mailConfig.data?.hasSecret ?? false} />
    </SingletonConfigForm>
  )
}
