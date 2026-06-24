import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import type { ScriptParams } from "@/lib/types"

// ScriptParamForm — 组织级 script 类型参数编辑表单（无工作流上下文）。
// 字段：code（Starlark 源，monospace）/ outputFormat。
// code 里用 {{变量名}} 引用上游输出；变量绑定 (varBindings) 是 per-node 的，由
// PropertiesPanel 处理，此处不涉及。无密钥（D1：脚本禁 {{secret:}}），故无密钥下拉、
// 无 allowResponseBody、无 method/url/headers。

export interface ScriptParamFormProps {
  value: ScriptParams
  onChange: (updated: ScriptParams) => void
}

export function ScriptParamForm({ value, onChange }: ScriptParamFormProps) {
  function patch(partial: Partial<ScriptParams>) {
    onChange({ ...value, ...partial })
  }

  return (
    <div className="flex flex-col gap-4">
      {/* code — 必填，Starlark 源，支持 {{变量名}} 模板 */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="script-code" className="text-[13px] font-medium text-text-1">
          脚本代码
          <span className="ml-1 text-text-2 font-normal text-[12px]">（Starlark）</span>
        </Label>
        <Textarea
          id="script-code"
          placeholder={'output = upstream_text.upper()'}
          value={value.code}
          onChange={(e) => patch({ code: e.target.value })}
          className="text-[13px] font-mono"
          rows={8}
        />
        <p className="text-[11px] text-text-2">
          用 <code className="font-mono bg-bg-raised px-1 rounded">{"{{变量名}}"}</code> 引用上游输出；把结果赋给 <code className="font-mono bg-bg-raised px-1 rounded">output</code>。变量绑定在画布节点属性面板中配置。
        </p>
      </div>

      {/* outputFormat — text | json */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="script-outputFormat" className="text-[13px] font-medium text-text-1">
          输出格式
        </Label>
        <select
          id="script-outputFormat"
          aria-label="输出格式"
          value={value.outputFormat ?? "text"}
          onChange={(e) => patch({ outputFormat: e.target.value as "text" | "json" })}
          className="h-8 rounded-md border border-input bg-background px-2 text-[13px] text-text-1 focus:ring-1 focus:ring-ring"
        >
          <option value="text">文本 (text)</option>
          <option value="json">JSON</option>
        </select>
      </div>
    </div>
  )
}
