import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import type { BasicPrompt, Prompt } from "@/lib/types"
import type { DerivedDefault, NodeTypeDescription, Property } from "./nodeDescTypes"

export interface ModelOption { value: string; label: string }

export interface PropertiesFormProps {
  description: NodeTypeDescription
  value: Record<string, unknown>
  onChange: (next: Record<string, unknown>) => void
  secretNames: string[]
  prompts?: Prompt[]
  basics?: BasicPrompt[]
  org?: string
  modelOptions?: ModelOption[]
}

function matches(current: unknown, allowed: unknown[]): boolean {
  return allowed.some((a) => a === current)
}

function isVisible(p: Property, value: Record<string, unknown>): boolean {
  const d = p.displayOptions
  if (!d) return true
  if (d.show) {
    for (const [k, allowed] of Object.entries(d.show)) {
      if (!matches(value[k], allowed)) return false
    }
  }
  if (d.hide) {
    for (const [k, allowed] of Object.entries(d.hide)) {
      if (matches(value[k], allowed)) return false
    }
  }
  return true
}

function applyDefaultFrom(
  df: DerivedDefault | undefined,
  triggerValue: string,
  base: Record<string, unknown>,
): Record<string, unknown> {
  if (!df) return base
  const derived = df.map[triggerValue]
  if (!derived) return base
  return { ...base, ...derived }
}

export function PropertiesForm(props: PropertiesFormProps) {
  const { description, value, onChange } = props

  function patch(name: string, v: unknown) {
    onChange({ ...value, [name]: v })
  }

  function patchOption(p: Property, v: string) {
    onChange(applyDefaultFrom(p.defaultFrom, v, { ...value, [p.name]: v }))
  }

  return (
    <div className="flex flex-col gap-4">
      {description.properties.filter((p) => isVisible(p, value)).map((p) => (
        <div key={p.name} className="flex flex-col gap-1.5">
          <Label htmlFor={`pf-${p.name}`} className="text-[12px] text-text-2">
            {p.label}
            {p.required && <span className="ml-1 text-danger">*</span>}
          </Label>
          {renderWidget(p, props, patch, patchOption)}
        </div>
      ))}
    </div>
  )
}

function renderWidget(
  p: Property,
  props: PropertiesFormProps,
  patch: (name: string, v: unknown) => void,
  patchOption: (p: Property, v: string) => void,
) {
  const { value, secretNames } = props
  const id = `pf-${p.name}`
  const cur = value[p.name]

  switch (p.type) {
    case "textarea":
    case "json":
      return (
        <Textarea id={id} aria-label={p.label} rows={p.typeOptions?.rows ?? 3}
          value={(cur as string) ?? ""} placeholder={p.placeholder}
          onChange={(e) => patch(p.name, e.target.value)} className="text-[13px]" />
      )
    case "code":
      return (
        <Textarea id={id} aria-label={p.label} rows={p.typeOptions?.rows ?? 8}
          value={(cur as string) ?? ""} placeholder={p.placeholder}
          onChange={(e) => patch(p.name, e.target.value)} className="text-[13px] font-mono" />
      )
    case "number":
      return (
        <Input id={id} aria-label={p.label} type="number"
          // 数字字段同样渲染占位（此前遗漏，temperature 等可选数字空值时像坏掉的空框）。
          // 后端目录未给 temperature 占位，前端兜底「如 0.7」与组织级 LlmParamForm 一致。
          placeholder={p.placeholder ?? (p.name === "temperature" ? "如 0.7" : undefined)}
          value={(cur as number | string) ?? ""}
          onChange={(e) => patch(p.name, e.target.value === "" ? undefined : parseFloat(e.target.value))}
          className="text-[13px]" />
      )
    case "boolean":
      return (
        <input id={id} aria-label={p.label} type="checkbox"
          checked={cur === true} onChange={(e) => patch(p.name, e.target.checked)} />
      )
    case "options":
      return (
        <select id={id} aria-label={p.label} value={(cur as string) ?? ""}
          onChange={(e) => patchOption(p, e.target.value)}
          className="h-8 rounded-md border border-input bg-background px-2 text-[13px]">
          <option value="" disabled>请选择</option>
          {(p.options ?? []).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
      )
    case "collection":
    case "fixedCollection":
      return (
        <Textarea id={id} aria-label={p.label} rows={2}
          value={Array.isArray(cur) ? (cur as string[]).join("\n") : ""}
          onChange={(e) => patch(p.name, e.target.value.split("\n").filter(Boolean))}
          className="text-[13px]" />
      )
    case "keyValue":
      return <KeyValueWidget p={p} value={(cur as Record<string, string>) ?? {}} secretNames={secretNames}
        onChange={(next) => patch(p.name, next)} ariaLabel={p.label} id={id} />
    case "resourceLocator":
      return (
        <select id={id} aria-label={p.label} value={(cur as string) ?? ""}
          onChange={(e) => patch(p.name, e.target.value)}
          className="h-8 rounded-md border border-input bg-background px-2 text-[13px]">
          <option value="">（默认）</option>
          {(props.modelOptions ?? []).map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
        </select>
      )
    case "prompt":
      return <PromptWidget p={p} props={props} id={id} />
    case "string":
    default:
      return (
        <Input id={id} aria-label={p.label} value={(cur as string) ?? ""} placeholder={p.placeholder}
          onChange={(e) => patch(p.name, e.target.value)} className="text-[13px]" />
      )
  }
}

function KeyValueWidget(props: {
  p: Property; value: Record<string, string>; secretNames: string[]
  onChange: (next: Record<string, string>) => void; ariaLabel: string; id: string
}) {
  const { value, secretNames, onChange } = props
  const rows = Object.entries(value)
  function setRow(i: number, key: string, val: string) {
    const next: Record<string, string> = {}
    rows.forEach(([k, v], idx) => { const kk = idx === i ? key : k; if (kk.trim()) next[kk] = idx === i ? val : v })
    onChange(next)
  }
  return (
    <div className="flex flex-col gap-2" aria-label={props.ariaLabel}>
      {rows.map(([k, v], i) => (
        <div key={i} className="flex flex-col gap-1 rounded border border-line/60 p-2">
          <Input aria-label={`键 ${i + 1}`} value={k} onChange={(e) => setRow(i, e.target.value, v)} className="text-[12px]" />
          <Input aria-label={`值 ${i + 1}`} value={v} onChange={(e) => setRow(i, k, e.target.value)} className="text-[12px]" />
          {secretNames.length > 0 && (
            <select aria-label={`插入密钥 ${i + 1}`} value="" className="h-7 self-start rounded-md border border-input bg-background px-2 text-[11px]"
              onChange={(e) => { if (e.target.value) setRow(i, k, `${v}{{secret:${e.target.value}}}`); e.currentTarget.value = "" }}>
              <option value="">插入密钥…</option>
              {secretNames.map((n) => <option key={n} value={n}>{`{{secret:${n}}}`}</option>)}
            </select>
          )}
        </div>
      ))}
    </div>
  )
}

function PromptWidget(props: { p: Property; props: PropertiesFormProps; id: string }) {
  const { p, props: form, id } = props
  const kind = p.typeOptions?.promptKind ?? ""
  const cur = (form.value[p.name] as string) ?? "__default__"
  return (
    <select id={id} aria-label={p.label} value={cur}
      onChange={(e) => form.onChange({ ...form.value, [p.name]: e.target.value })}
      className="h-8 rounded-md border border-input bg-background px-2 text-[13px]">
      <option value="__default__">使用系统内置默认提示词</option>
      <option value="__custom__">＋ 自定义输入（不入库）</option>
      {form.org && <option value="__create__">＋ 新建提示词</option>}
      {(form.basics ?? []).filter((b) => b.kind === kind).map((b) => <option key={b.id} value={b.id}>{b.name}（基础）</option>)}
      {(form.prompts ?? []).filter((pr) => pr.kind === kind || pr.kind === "").map((pr) => <option key={pr.id} value={pr.id}>{pr.name}</option>)}
    </select>
  )
}
