import type { ReactNode } from "react"
import { Plus, Trash2, X } from "lucide-react"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import type { InputField } from "@/lib/types"
import {
  INPUT_FIELD_TARGETS,
  INPUT_FIELD_TYPES,
  inputFieldError,
} from "./inputsSchema"

// 画布级面板（与 PropertiesPanel 并列）：编辑整条工作流的运行期输入声明列表。
// 受控组件：schema 来自父（WorkflowCanvas 状态），onChange 上抛新数组；行内
// 校验只读 schema 派生（纯渲染，无内部副作用）。后端 ValidateSchema 为兜底。
export interface InputsSchemaPanelProps {
  schema: InputField[]
  onChange: (next: InputField[]) => void
}

const emptyField: InputField = {
  name: "",
  label: "",
  type: "text",
  target: "variable",
  required: false,
}

const selectCls =
  "h-8 rounded-md border border-input bg-background px-2 text-[13px]"

export function InputsSchemaPanel({ schema, onChange }: InputsSchemaPanelProps) {
  function patch(i: number, p: Partial<InputField>) {
    onChange(schema.map((f, idx) => (idx === i ? { ...f, ...p } : f)))
  }
  function add() {
    onChange([...schema, { ...emptyField }])
  }
  function remove(i: number) {
    onChange(schema.filter((_, idx) => idx !== i))
  }

  return (
    <aside className="flex w-64 shrink-0 flex-col gap-3 overflow-y-auto border-l border-line bg-bg-surface p-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-[13px] font-semibold text-text-1">工作流输入</h2>
        <p className="text-[11px] text-text-3">
          运行该工作流时填写的类型化输入。变量经 {"{{input:name}}"} 注入；
          其余可覆盖 brief。
        </p>
      </div>

      {schema.length === 0 && (
        <p className="rounded border border-dashed border-line/70 p-3 text-center text-[11px] text-text-3">
          暂无输入字段
        </p>
      )}

      <div className="flex flex-col gap-3">
        {schema.map((f, i) => {
          const err = inputFieldError(f)
          const showOptions = f.type === "select"
          return (
            <div
              key={i}
              className="flex flex-col gap-2 rounded border border-line/60 p-2.5"
            >
              <div className="flex items-center justify-between">
                <span className="text-[11px] text-text-3">字段 {i + 1}</span>
                <button
                  type="button"
                  aria-label={`删除字段 ${i + 1}`}
                  title="删除字段"
                  onClick={() => remove(i)}
                  className="text-text-3 hover:text-danger"
                >
                  <Trash2 className="h-3.5 w-3.5" aria-hidden />
                </button>
              </div>

              <Field label="名称">
                <Input
                  aria-label={`字段名 ${i + 1}`}
                  value={f.name}
                  placeholder="heroName"
                  onChange={(e) => patch(i, { name: e.target.value })}
                  className="text-[13px]"
                />
              </Field>

              <Field label="标签">
                <Input
                  aria-label={`显示标签 ${i + 1}`}
                  value={f.label ?? ""}
                  placeholder="主角名字"
                  onChange={(e) => patch(i, { label: e.target.value })}
                  className="text-[13px]"
                />
              </Field>

              <Field label="类型">
                <select
                  aria-label={`类型 ${i + 1}`}
                  value={f.type}
                  onChange={(e) =>
                    patch(i, { type: e.target.value as InputField["type"] })
                  }
                  className={selectCls}
                >
                  {INPUT_FIELD_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.label}
                    </option>
                  ))}
                </select>
              </Field>

              <Field label="目标">
                <select
                  aria-label={`目标 ${i + 1}`}
                  value={f.target}
                  onChange={(e) =>
                    patch(i, { target: e.target.value as InputField["target"] })
                  }
                  className={selectCls}
                >
                  {INPUT_FIELD_TARGETS.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.label}
                    </option>
                  ))}
                </select>
              </Field>

              {showOptions && (
                <OptionsEditor index={i} field={f} patch={patch} />
              )}

              <Field label="默认值">
                <Input
                  aria-label={`默认值 ${i + 1}`}
                  value={f.default ?? ""}
                  onChange={(e) => patch(i, { default: e.target.value })}
                  className="text-[13px]"
                />
              </Field>

              <label className="flex items-center gap-2 text-[12px] text-text-2">
                <input
                  type="checkbox"
                  aria-label={`必填 ${i + 1}`}
                  checked={f.required === true}
                  onChange={(e) => patch(i, { required: e.target.checked })}
                />
                必填
              </label>

              {err && (
                <p role="alert" className="text-[11px] text-danger">
                  {err}
                </p>
              )}
            </div>
          )
        })}
      </div>

      <button
        type="button"
        onClick={add}
        className="flex items-center justify-center gap-1.5 rounded-md border border-line px-3 py-1.5 text-[12px] text-text-2 hover:border-amber hover:text-amber"
      >
        <Plus className="h-3.5 w-3.5" aria-hidden />
        添加字段
      </button>
    </aside>
  )
}

function Field(props: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label className="text-[12px] text-text-2">{props.label}</Label>
      {props.children}
    </div>
  )
}

function OptionsEditor(props: {
  index: number
  field: InputField
  patch: (i: number, p: Partial<InputField>) => void
}) {
  const { index: i, field: f, patch } = props
  const options = f.options ?? []
  function setOption(j: number, key: "value" | "label", v: string) {
    patch(i, {
      options: options.map((o, idx) => (idx === j ? { ...o, [key]: v } : o)),
    })
  }
  function addOption() {
    patch(i, { options: [...options, { value: "", label: "" }] })
  }
  function removeOption(j: number) {
    patch(i, { options: options.filter((_, idx) => idx !== j) })
  }
  return (
    <div className="flex flex-col gap-1.5">
      <Label className="text-[12px] text-text-2">选项</Label>
      <div className="flex flex-col gap-2">
        {options.map((o, j) => (
          <div
            key={j}
            className="flex items-center gap-1.5 rounded border border-line/60 p-1.5"
          >
            <div className="flex flex-1 flex-col gap-1">
              <Input
                aria-label={`选项值 ${i + 1}-${j + 1}`}
                value={o.value}
                placeholder="value"
                onChange={(e) => setOption(j, "value", e.target.value)}
                className="text-[12px]"
              />
              <Input
                aria-label={`选项标签 ${i + 1}-${j + 1}`}
                value={o.label ?? ""}
                placeholder="显示文案（可选）"
                onChange={(e) => setOption(j, "label", e.target.value)}
                className="text-[12px]"
              />
            </div>
            <button
              type="button"
              aria-label={`删除选项 ${i + 1}-${j + 1}`}
              title="删除选项"
              onClick={() => removeOption(j)}
              className="text-text-3 hover:text-danger"
            >
              <X className="h-3.5 w-3.5" aria-hidden />
            </button>
          </div>
        ))}
      </div>
      <button
        type="button"
        onClick={addOption}
        aria-label={`添加选项 ${i + 1}`}
        className="flex items-center gap-1 self-start rounded border border-line px-2 py-1 text-[11px] text-text-2 hover:border-amber hover:text-amber"
      >
        <Plus className="h-3 w-3" aria-hidden />
        添加选项
      </button>
    </div>
  )
}
