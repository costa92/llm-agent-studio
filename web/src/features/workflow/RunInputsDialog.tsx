import { useState } from "react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import type { InputField } from "@/lib/types"

// 运行期输入表单对话框：按 InputField[] schema 渲染类型化表单，前端拦截 required 缺失，
// 提交把 {<name>: <value>} 交回调用方（调用方再 POST {inputs}）。后端 400 为兜底。
// schema 来源是工作流的 wf.inputsSchema（workflow-only：运行期输入唯一入口）。

// Radix Select.Item 不允许空串 value，故用哨兵代表「未指定」；提交时还原为省略。
const NONE = "__none__"

// 表单内部值：text/textarea/select/number 统一存字符串（number 在提交时转数字）。
type FormValue = string

export interface RunInputsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  schema: InputField[]
  // 提交回调：传入 {<name>: <value>}（已按类型分流为字符串/数字/数组），由调用方发起 run。
  onSubmit: (inputs: Record<string, unknown>) => void | Promise<void>
  title?: string
  description?: string
  submitting?: boolean
}

// 解析字段 default（JSON 字面量优先；非法 JSON 回退为原始字符串）。
function parseDefault(raw?: string): unknown {
  if (raw === undefined || raw === "") return undefined
  try {
    return JSON.parse(raw)
  } catch {
    return raw
  }
}

// 字段初始表单值：标量 → 字符串；缺省 → 空。
function initialValue(f: InputField): FormValue {
  const d = parseDefault(f.default)
  if (d === undefined || d === null || typeof d === "object") return ""
  return String(d)
}

// required 缺失校验：返回 name→错误文案；空 = 通过。
function requiredErrors(
  schema: InputField[],
  values: Record<string, FormValue>,
): Record<string, string> {
  const errs: Record<string, string> = {}
  for (const f of schema) {
    if (!f.required) continue
    const v = values[f.name]
    if (String(v ?? "").trim() === "") errs[f.name] = `${f.label || f.name} 为必填`
  }
  return errs
}

// 按类型把表单值分流为提交载荷。空的可选 select/number/text 省略（避免后端枚举越界/类型 400）；
// number 转为数字字面量。
function buildInputs(
  schema: InputField[],
  values: Record<string, FormValue>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const f of schema) {
    const v = values[f.name]
    const s = String(v ?? "").trim()
    if (s === "") continue
    if (f.type === "number") {
      const n = Number(s)
      if (!Number.isNaN(n)) out[f.name] = n
      continue
    }
    out[f.name] = s
  }
  return out
}

export function RunInputsDialog({
  open,
  onOpenChange,
  schema,
  onSubmit,
  title = "填写运行输入",
  description = "本次运行的输入只作用于这一次 run，不改动项目或工作流配置。",
  submitting = false,
}: RunInputsDialogProps) {
  const [values, setValues] = useState<Record<string, FormValue>>(() => {
    const init: Record<string, FormValue> = {}
    for (const f of schema) init[f.name] = initialValue(f)
    return init
  })
  const [errors, setErrors] = useState<Record<string, string>>({})

  const setValue = (name: string, v: FormValue) =>
    setValues((prev) => ({ ...prev, [name]: v }))

  const handleSubmit = () => {
    const errs = requiredErrors(schema, values)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    void onSubmit(buildInputs(schema, values))
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col gap-4 sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 overflow-y-auto">
          {schema.map((f) => (
            <FieldControl
              key={f.name}
              field={f}
              value={values[f.name]}
              error={errors[f.name]}
              onChange={(v) => setValue(f.name, v)}
            />
          ))}
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button variant="amber" onClick={handleSubmit} disabled={submitting}>
            {submitting ? "运行中…" : "开始运行"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function FieldControl({
  field,
  value,
  error,
  onChange,
}: {
  field: InputField
  value: FormValue
  error?: string
  onChange: (v: FormValue) => void
}) {
  const id = `run-input-${field.name}`
  const label = field.label || field.name
  const options = field.options ?? []

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>
        {label}
        {field.required && (
          <span aria-hidden className="ml-1 text-danger">
            *
          </span>
        )}
      </Label>

      {field.type === "text" && (
        <Input
          id={id}
          value={String(value ?? "")}
          aria-invalid={error ? true : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      )}

      {field.type === "textarea" && (
        <Textarea
          id={id}
          value={String(value ?? "")}
          aria-invalid={error ? true : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      )}

      {field.type === "number" && (
        <Input
          id={id}
          type="number"
          value={String(value ?? "")}
          aria-invalid={error ? true : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      )}

      {field.type === "select" && (
        <Select
          value={String(value ?? "") || NONE}
          onValueChange={(v) => onChange(v === NONE ? "" : v)}
        >
          <SelectTrigger id={id} aria-invalid={error ? true : undefined}>
            <SelectValue placeholder="未指定" />
          </SelectTrigger>
          <SelectContent>
            {!field.required && <SelectItem value={NONE}>未指定</SelectItem>}
            {options
              .filter((o) => o.value !== "")
              .map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label || o.value}
                </SelectItem>
              ))}
          </SelectContent>
        </Select>
      )}

      {error && <p className="text-[11.5px] text-danger">{error}</p>}
    </div>
  )
}
