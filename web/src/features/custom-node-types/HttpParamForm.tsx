import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Button as UiButton } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import type { HttpParams } from "@/lib/types"

// HttpParamForm — 组织级 http 类型参数编辑表单（无工作流上下文）。
// 字段：method / url / headers(key-value) / bodyTemplate / outputFormat / allowResponseBody。
// url 必须是静态字面量（禁 {{...}}）；header 值可含 {{name}} 与 {{secret:NAME}}；
// bodyTemplate 仅 {{name}}（密钥不可进 body——后端强制，前端提示）。
// allowResponseBody 仅在「任一 header 引用了密钥」时显示（带 admin 背书文案）。

// 检测 url 是否含 {{...}} 模板（http url 必须静态字面量）。
const TEMPLATE_RE = /\{\{/
// 检测 header 值是否引用了 {{secret:NAME}}。
const SECRET_REF_RE = /\{\{\s*secret:/

// 把 headers 对象拍平成有序行（便于编辑空行/重复键）。空键行也保留以供继续输入。
type HeaderRow = { key: string; value: string }

function toRows(headers: Record<string, string>): HeaderRow[] {
  return Object.entries(headers).map(([key, value]) => ({ key, value }))
}

function fromRows(rows: HeaderRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const r of rows) {
    if (r.key.trim() !== "") out[r.key] = r.value
  }
  return out
}

export interface HttpParamFormProps {
  value: HttpParams
  onChange: (updated: HttpParams) => void
  // 组织密钥名列表（来自 useOrgSecrets）；供 header 值「插入密钥」下拉。
  secretNames: string[]
}

const METHODS: HttpParams["method"][] = ["GET", "POST", "PUT", "PATCH", "DELETE"]

export function HttpParamForm({ value, onChange, secretNames }: HttpParamFormProps) {
  function patch(partial: Partial<HttpParams>) {
    onChange({ ...value, ...partial })
  }

  const rows = toRows(value.headers ?? {})
  const urlInvalid = TEMPLATE_RE.test(value.url)
  // 任一 header 值引用了密钥 → 表单 secret-bearing。
  const secretBearing = Object.values(value.headers ?? {}).some((v) => SECRET_REF_RE.test(v))

  function patchRows(next: HeaderRow[]) {
    patch({ headers: fromRows(next) })
  }

  function setRow(index: number, partial: Partial<HeaderRow>) {
    const next = rows.map((r, i) => (i === index ? { ...r, ...partial } : r))
    patchRows(next)
  }

  function addRow() {
    // 用临时占位键避免 fromRows 丢弃空键行——直接在受控态外用 rows 局部插入。
    patchRows([...rows, { key: "Header-Name", value: "" }])
  }

  function removeRow(index: number) {
    patchRows(rows.filter((_, i) => i !== index))
  }

  function insertSecretIntoRow(index: number, secretName: string) {
    if (secretName === "") return
    const cur = rows[index]?.value ?? ""
    setRow(index, { value: `${cur}{{secret:${secretName}}}` })
  }

  return (
    <div className="flex flex-col gap-4">
      {/* method */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="http-method" className="text-[13px] font-medium text-text-1">
          请求方法 (method)
        </Label>
        <select
          id="http-method"
          aria-label="请求方法 method"
          value={value.method}
          onChange={(e) => patch({ method: e.target.value as HttpParams["method"] })}
          className="h-8 rounded-md border border-input bg-background px-2 text-[13px] text-text-1 focus:ring-1 focus:ring-ring"
        >
          {METHODS.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </div>

      {/* url — 必须静态字面量 */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="http-url" className="text-[13px] font-medium text-text-1">
          URL
        </Label>
        <Input
          id="http-url"
          placeholder="如 https://api.example.com/v1/translate"
          value={value.url}
          aria-invalid={urlInvalid}
          onChange={(e) => patch({ url: e.target.value })}
          className="text-[13px]"
        />
        {/* 常驻提示：URL 为空时创建按钮就已禁用，需说明约束，否则用户只见按钮不可点却不知原因。 */}
        <p className="text-[11px] text-text-3">
          必须是静态地址，不支持 <code className="font-mono">{"{{变量}}"}</code> 模板（SSRF 防护）。
        </p>
        {urlInvalid && (
          <p className="text-[11px] text-danger">
            URL 不能包含 <code className="font-mono">{"{{...}}"}</code> 模板，必须是静态字面量地址。
          </p>
        )}
      </div>

      {/* headers — key/value 行；value 可插入 {{name}} 或 {{secret:NAME}} */}
      <div className="flex flex-col gap-1.5">
        <Label className="text-[13px] font-medium text-text-1">请求头 (headers)</Label>
        <div className="flex flex-col gap-2">
          {rows.map((row, i) => (
            <div key={i} className="flex flex-col gap-1 rounded border border-line/60 p-2">
              <div className="flex items-center gap-2">
                <Input
                  aria-label={`请求头名 ${i + 1}`}
                  placeholder="Header-Name"
                  value={row.key}
                  onChange={(e) => setRow(i, { key: e.target.value })}
                  className="text-[12px]"
                />
                <UiButton
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => removeRow(i)}
                  aria-label={`删除请求头 ${i + 1}`}
                >
                  删除
                </UiButton>
              </div>
              <Input
                aria-label={`请求头值 ${i + 1}`}
                placeholder="值，可用 {{变量名}} 或 {{secret:密钥名}}"
                value={row.value}
                onChange={(e) => setRow(i, { value: e.target.value })}
                className="text-[12px]"
              />
              {secretNames.length > 0 && (
                <select
                  aria-label={`插入密钥 ${i + 1}`}
                  value=""
                  onChange={(e) => {
                    insertSecretIntoRow(i, e.target.value)
                    e.currentTarget.value = ""
                  }}
                  className="h-7 self-start rounded-md border border-input bg-background px-2 text-[11px] text-text-2 focus:ring-1 focus:ring-ring"
                >
                  <option value="">插入密钥…</option>
                  {secretNames.map((n) => (
                    <option key={n} value={n}>
                      {`{{secret:${n}}}`}
                    </option>
                  ))}
                </select>
              )}
            </div>
          ))}
          <UiButton type="button" variant="outline" size="sm" onClick={addRow} className="self-start">
            添加请求头
          </UiButton>
        </div>
        <p className="text-[11px] text-text-2">
          值里用 <code className="font-mono">{"{{变量名}}"}</code> 引用上游输出；用「插入密钥」引用组织密钥（绝不在前端回显密钥值）。
        </p>
      </div>

      {/* bodyTemplate — 仅 {{name}}，密钥不可进 body */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="http-bodyTemplate" className="text-[13px] font-medium text-text-1">
          请求体模板
          <span className="ml-1 text-text-2 font-normal text-[12px]">（可选）</span>
        </Label>
        <Textarea
          id="http-bodyTemplate"
          placeholder='如 {"text": "{{draft}}"}'
          value={value.bodyTemplate ?? ""}
          onChange={(e) => patch({ bodyTemplate: e.target.value || undefined })}
          className="text-[13px]"
          rows={3}
        />
        <p className="text-[11px] text-text-2">
          仅支持 <code className="font-mono">{"{{变量名}}"}</code>；密钥不能用于请求体（后端拒绝）。
        </p>
      </div>

      {/* outputFormat — text | json */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="http-outputFormat" className="text-[13px] font-medium text-text-1">
          输出格式
        </Label>
        <select
          id="http-outputFormat"
          aria-label="输出格式"
          value={value.outputFormat ?? "text"}
          onChange={(e) => patch({ outputFormat: e.target.value as "text" | "json" })}
          className="h-8 rounded-md border border-input bg-background px-2 text-[13px] text-text-1 focus:ring-1 focus:ring-ring"
        >
          <option value="text">文本 (text)</option>
          <option value="json">JSON</option>
        </select>
      </div>

      {/* allowResponseBody — 仅 secret-bearing 时可见，带 admin 背书文案 */}
      {secretBearing && (
        <div className="flex flex-col gap-1.5 rounded border border-amber/40 bg-amber/5 p-2.5">
          <div className="flex items-start gap-2">
            <Checkbox
              id="http-allowResponseBody"
              checked={value.allowResponseBody ?? false}
              onCheckedChange={(c) => patch({ allowResponseBody: c === true })}
              className="mt-0.5"
            />
            <Label
              htmlFor="http-allowResponseBody"
              className="text-[12px] font-medium text-text-1 leading-relaxed"
            >
              我确认此端点不回显密钥，允许在运行结果中显示响应体
            </Label>
          </div>
          <p className="text-[11px] text-text-2">
            该类型的请求头引用了密钥。默认抑制响应体（仅显示状态码），以防回显的响应体泄露密钥。勾选即承担背书责任。
          </p>
        </div>
      )}
    </div>
  )
}
