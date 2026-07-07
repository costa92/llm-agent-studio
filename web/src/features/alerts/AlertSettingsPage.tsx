import { useFormContext } from "react-hook-form"
import { z } from "zod"
import { toast } from "sonner"
import { Button } from "@/components/studio/Button"
import { Label } from "@/components/ui/label"
import { SingletonConfigForm } from "@/features/common/crud"
import type { AlertSettings } from "@/lib/types"
import { useAlertSettings, useUpdateAlertSettings } from "./api"

// ¥ ↔ micros 换算（对齐 cost/format.ts：¥1 = 1_000_000 micros）。表单以 ¥ 录入，
// 提交时转 micros；回填时 micros 转 ¥。
const MICROS_PER_YUAN = 1_000_000

// rhf+zod 表单 schema：数值阈值以字符串录入（input），条件必填/正数校验放 handleSubmit
// （关闭某告警时允许留空，zod 静态 schema 表达不了条件必填）。
const formSchema = z.object({
  email: z.string(),
  enabled: z.boolean(),
  budgetEnabled: z.boolean(),
  budgetThresholdYuan: z.string(),
  budgetWindowHours: z.string(),
  stuckEnabled: z.boolean(),
  stuckThresholdMinutes: z.string(),
  backlogEnabled: z.boolean(),
  backlogThreshold: z.string(),
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

// 正整数解析：非法/<=0 返回 null（供条件校验判空）。
function positiveInt(s: string): number | null {
  const n = Number(s)
  return Number.isFinite(n) && n > 0 && Number.isInteger(n) ? n : null
}
// 正数（可小数，用于 ¥ 阈值）：非法/<=0 返回 null。
function positiveNum(s: string): number | null {
  const n = Number(s)
  return Number.isFinite(n) && n > 0 ? n : null
}

// settings → 表单值（未配置的 org 后端返回零值默认，直接映射；阈值默认取直觉值）。
function toValues(settings: AlertSettings | undefined): FormValues {
  return {
    email: settings?.email ?? "",
    enabled: settings?.enabled ?? false,
    budgetEnabled: settings?.budgetEnabled ?? false,
    budgetThresholdYuan: settings?.budgetThresholdMicros
      ? String(settings.budgetThresholdMicros / MICROS_PER_YUAN)
      : "",
    budgetWindowHours: String(settings?.budgetWindowHours || 24),
    stuckEnabled: settings?.stuckEnabled ?? false,
    stuckThresholdMinutes: String(settings?.stuckThresholdMinutes || 30),
    backlogEnabled: settings?.backlogEnabled ?? false,
    backlogThreshold: String(settings?.backlogThreshold || 50),
  }
}

// 小复选框（对齐既有 alert-enabled 样式）。
function Toggle({
  id,
  label,
  checked,
  onChange,
}: {
  id: string
  label: string
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className="flex items-center gap-2">
      <input
        id={id}
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="h-4 w-4 rounded border-line bg-bg-base text-amber focus:ring-amber"
      />
      <Label htmlFor={id} className="cursor-pointer">
        {label}
      </Label>
    </div>
  )
}

// 表单字段块（通过 useFormContext 读写），布局对齐 MailConfigSection。
function AlertSettingsFields() {
  const { register, setValue, watch } = useFormContext<FormValues>()
  const enabled = watch("enabled")
  const budgetEnabled = watch("budgetEnabled")
  const stuckEnabled = watch("stuckEnabled")
  const backlogEnabled = watch("backlogEnabled")

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
        <p className="text-[11px] text-text-3">所有告警共用此收件邮箱；开启任一告警都需先填写。</p>
      </div>

      <Toggle
        id="alert-enabled"
        label="启用 run 失败邮件告警"
        checked={enabled}
        onChange={(v) => setValue("enabled", v, { shouldDirty: true })}
      />

      <div className="mt-2 border-t border-line pt-3">
        <h3 className="mb-2 text-[13px] font-semibold text-text-1">运营告警（周期性检查）</h3>

        {/* 成本超阈告警 */}
        <div className="flex flex-col gap-2 py-2">
          <Toggle
            id="alert-budget"
            label="成本超阈告警"
            checked={budgetEnabled}
            onChange={(v) => setValue("budgetEnabled", v, { shouldDirty: true })}
          />
          {budgetEnabled && (
            <div className="ml-6 flex flex-wrap items-center gap-2 text-[13px] text-text-2">
              <span>近</span>
              <input
                type="number"
                min={1}
                aria-label="成本窗口小时数"
                {...register("budgetWindowHours")}
                className={`${fieldClass} w-20`}
              />
              <span>小时成本超过</span>
              <span>¥</span>
              <input
                type="number"
                min={0}
                step="0.01"
                aria-label="成本阈值（元）"
                {...register("budgetThresholdYuan")}
                className={`${fieldClass} w-28`}
              />
              <span>时告警</span>
            </div>
          )}
        </div>

        {/* 卡顿运行告警 */}
        <div className="flex flex-col gap-2 py-2">
          <Toggle
            id="alert-stuck"
            label="卡顿运行告警"
            checked={stuckEnabled}
            onChange={(v) => setValue("stuckEnabled", v, { shouldDirty: true })}
          />
          {stuckEnabled && (
            <div className="ml-6 flex flex-wrap items-center gap-2 text-[13px] text-text-2">
              <span>运行超过</span>
              <input
                type="number"
                min={1}
                aria-label="卡顿时长（分钟）"
                {...register("stuckThresholdMinutes")}
                className={`${fieldClass} w-24`}
              />
              <span>分钟无进展时告警</span>
            </div>
          )}
        </div>

        {/* 审核积压告警 */}
        <div className="flex flex-col gap-2 py-2">
          <Toggle
            id="alert-backlog"
            label="审核积压告警"
            checked={backlogEnabled}
            onChange={(v) => setValue("backlogEnabled", v, { shouldDirty: true })}
          />
          {backlogEnabled && (
            <div className="ml-6 flex flex-wrap items-center gap-2 text-[13px] text-text-2">
              <span>待审核资产超过</span>
              <input
                type="number"
                min={1}
                aria-label="审核积压阈值（条）"
                {...register("backlogThreshold")}
                className={`${fieldClass} w-24`}
              />
              <span>条时告警</span>
            </div>
          )}
        </div>
      </div>
    </>
  )
}

// org 级告警设置页（/orgs/$org/alerts，roleAdmin 门禁由路由的 AdminGate 承担）。
// 单记录 upsert：拉一条 → 表单 → 保存。
export function AlertSettingsView({ org }: { org: string }) {
  const settings = useAlertSettings(org)
  const update = useUpdateAlertSettings(org)

  function handleSubmit(values: FormValues) {
    const email = values.email.trim()
    const anyEnabled =
      values.enabled ||
      values.budgetEnabled ||
      values.stuckEnabled ||
      values.backlogEnabled
    // 镜像后端校验（开启任一告警需有效邮箱；非空邮箱须形似邮箱），前端先拦截省一次 400。
    if (anyEnabled && !looksLikeEmail(email)) {
      toast.error("开启告警需要有效的告警邮箱")
      return
    }
    if (email !== "" && !looksLikeEmail(email)) {
      toast.error("告警邮箱格式不正确")
      return
    }

    // 开启的运营告警需正阈值。
    let budgetThresholdMicros = 0
    let budgetWindowHours = 0
    if (values.budgetEnabled) {
      const yuan = positiveNum(values.budgetThresholdYuan)
      const hours = positiveInt(values.budgetWindowHours)
      if (yuan === null) {
        toast.error("成本告警的阈值需为正数（元）")
        return
      }
      if (hours === null) {
        toast.error("成本告警的窗口小时数需为正整数")
        return
      }
      budgetThresholdMicros = Math.round(yuan * MICROS_PER_YUAN)
      budgetWindowHours = hours
    }
    let stuckThresholdMinutes = 0
    if (values.stuckEnabled) {
      const mins = positiveInt(values.stuckThresholdMinutes)
      if (mins === null) {
        toast.error("卡顿告警的时长需为正整数（分钟）")
        return
      }
      stuckThresholdMinutes = mins
    }
    let backlogThreshold = 0
    if (values.backlogEnabled) {
      const n = positiveInt(values.backlogThreshold)
      if (n === null) {
        toast.error("审核积压告警的阈值需为正整数（条）")
        return
      }
      backlogThreshold = n
    }

    update.mutate(
      {
        email,
        enabled: values.enabled,
        budgetEnabled: values.budgetEnabled,
        budgetThresholdMicros,
        budgetWindowHours,
        stuckEnabled: values.stuckEnabled,
        stuckThresholdMinutes,
        backlogEnabled: values.backlogEnabled,
        backlogThreshold,
      },
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
          run 失败即时告警 + 成本 / 卡顿 / 审核积压的周期性运营告警，统一发送到告警邮箱（org 级限频）。
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
          title="告警配置"
          description="开启后按类型向告警邮箱发送告警；全部关闭则完全静默。运营告警默认关闭。"
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
