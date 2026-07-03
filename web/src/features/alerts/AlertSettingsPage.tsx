import { useFormContext } from "react-hook-form"
import { z } from "zod"
import { toast } from "sonner"
import { Button } from "@/components/studio/Button"
import { Label } from "@/components/ui/label"
import { SingletonConfigForm } from "@/features/common/crud"
import type { AlertSettings } from "@/lib/types"
import { useAlertSettings, useUpdateAlertSettings } from "./api"

// rhf+zod 表单 schema：格式校验放 handleSubmit（关闭时允许留空，zod 静态 schema 表达不了条件必填）。
const formSchema = z.object({
  email: z.string(),
  enabled: z.boolean(),
})
type FormValues = z.infer<typeof formSchema>

const fieldClass =
  "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"

// 宽松邮箱形状校验，镜像后端 looksLikeEmail：非空 local@domain、domain 含点、无空白。
// 真正的有效性由 SMTP 投递时验证。
function looksLikeEmail(s: string): boolean {
  const at = s.indexOf("@")
  if (at <= 0 || at === s.length - 1) return false
  const domain = s.slice(at + 1)
  return domain.includes(".") && !/[ \t\r\n]/.test(s)
}

// settings → 表单值（未配置的 org 后端返回零值默认，直接映射）。
function toValues(settings: AlertSettings | undefined): FormValues {
  return {
    email: settings?.email ?? "",
    enabled: settings?.enabled ?? false,
  }
}

// 表单字段块（通过 useFormContext 读写），布局对齐 MailConfigSection。
function AlertSettingsFields() {
  const { register, setValue, watch } = useFormContext<FormValues>()
  const enabled = watch("enabled")

  return (
    <>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="alert-email">告警邮箱</Label>
        <input
          id="alert-email"
          type="email"
          placeholder="如 ops@example.com"
          {...register("email")}
          className={fieldClass}
        />
      </div>

      <div className="mt-1 flex items-center gap-2">
        <input
          id="alert-enabled"
          type="checkbox"
          checked={enabled}
          onChange={(e) => setValue("enabled", e.target.checked, { shouldDirty: true })}
          className="h-4 w-4 rounded border-line bg-bg-base text-amber focus:ring-amber"
        />
        <Label htmlFor="alert-enabled" className="cursor-pointer">
          启用 run 失败邮件告警
        </Label>
      </div>
    </>
  )
}

// org 级 run 失败邮件告警设置页（/orgs/$org/alerts，roleAdmin 门禁由路由的 AdminGate 承担）。
// 单记录 upsert：拉一条 → 表单 → 保存。
export function AlertSettingsView({ org }: { org: string }) {
  const settings = useAlertSettings(org)
  const update = useUpdateAlertSettings(org)

  function handleSubmit(values: FormValues) {
    const email = values.email.trim()
    // 镜像后端校验（开启需有效邮箱；非空邮箱须形似邮箱），前端先拦截省一次 400。
    if (values.enabled && !looksLikeEmail(email)) {
      toast.error("开启告警需要有效的告警邮箱")
      return
    }
    if (email !== "" && !looksLikeEmail(email)) {
      toast.error("告警邮箱格式不正确")
      return
    }
    update.mutate(
      { email, enabled: values.enabled },
      {
        onSuccess: () => toast.success("告警设置已保存"),
        onError: () => toast.error("保存失败，请检查参数或重试"),
      },
    )
  }

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex flex-col gap-1.5">
        <h1 className="font-heading text-[22px] font-bold text-text-1">告警</h1>
        <p className="text-[12px] text-text-3">
          run 失败时向指定邮箱发送告警邮件（一次 run 只发一封，org 级限频）。
        </p>
      </header>

      {settings.isError ? (
        <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
          <div className="flex flex-col items-center gap-3 py-10 text-center">
            <p className="text-text-2">告警设置加载失败</p>
            <Button variant="ghost" onClick={() => void settings.refetch()}>
              重试
            </Button>
          </div>
        </section>
      ) : (
        <SingletonConfigForm<FormValues>
          title="run 失败邮件告警"
          description="开启后，本组织内 run 终态失败时通知到告警邮箱；关闭则完全静默。"
          schema={formSchema}
          values={toValues(settings.data)}
          isLoading={settings.isLoading}
          submitLabel={update.isPending ? "保存中..." : "保存告警设置"}
          submitting={update.isPending}
          onSubmit={handleSubmit}
        >
          <AlertSettingsFields />
        </SingletonConfigForm>
      )}
    </div>
  )
}
