import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import type { LlmParams } from "@/lib/types"

// LlmParamForm — 组织级 LLM 类型参数编辑表单（无工作流上下文）。
// 字段：systemPrompt / userPrompt / model / temperature / outputFormat。
// 变量绑定 (varBindings) 是 per-node 的，由 PropertiesPanel（Task 13）处理，此处不涉及。
export interface LlmParamFormProps {
  value: LlmParams
  onChange: (updated: LlmParams) => void
}

export function LlmParamForm({ value, onChange }: LlmParamFormProps) {
  function patch(partial: Partial<LlmParams>) {
    onChange({ ...value, ...partial })
  }

  return (
    <div className="flex flex-col gap-4">
      {/* systemPrompt — 可选 */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="llm-systemPrompt" className="text-[13px] font-medium text-text-1">
          系统提示词
          <span className="ml-1 text-text-2 font-normal text-[12px]">（可选）</span>
        </Label>
        <Textarea
          id="llm-systemPrompt"
          placeholder="输入系统提示词，如：你是一个专业翻译助手"
          value={value.systemPrompt ?? ""}
          onChange={(e) => patch({ systemPrompt: e.target.value || undefined })}
          className="text-[13px]"
          rows={3}
        />
      </div>

      {/* userPrompt — 必填，支持 {{variable}} 模板 */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="llm-userPrompt" className="text-[13px] font-medium text-text-1">
          用户提示词
        </Label>
        <Textarea
          id="llm-userPrompt"
          placeholder="输入用户提示词，可用 {{变量名}} 引用上游输出"
          value={value.userPrompt}
          onChange={(e) => patch({ userPrompt: e.target.value })}
          className="text-[13px]"
          rows={4}
        />
        <p className="text-[11px] text-text-2">
          用 <code className="font-mono bg-bg-raised px-1 rounded">{"{{变量名}}"}</code> 引用变量；变量绑定在画布节点属性面板中配置。
        </p>
      </div>

      {/* model — 可选，空则使用路由默认模型 */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="llm-model" className="text-[13px] font-medium text-text-1">
          模型
          <span className="ml-1 text-text-2 font-normal text-[12px]">（可选，空则使用组织默认）</span>
        </Label>
        <Input
          id="llm-model"
          placeholder="如 gpt-4o / claude-3-5-sonnet"
          value={value.model ?? ""}
          onChange={(e) => patch({ model: e.target.value || undefined })}
          className="text-[13px]"
        />
      </div>

      {/* temperature — 可选数字 */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="llm-temperature" className="text-[13px] font-medium text-text-1">
          温度
          <span className="ml-1 text-text-2 font-normal text-[12px]">（可选，0–2）</span>
        </Label>
        <Input
          id="llm-temperature"
          type="number"
          min={0}
          max={2}
          step={0.1}
          placeholder="如 0.7"
          value={value.temperature ?? ""}
          onChange={(e) => {
            const v = e.target.value
            patch({ temperature: v === "" ? undefined : parseFloat(v) })
          }}
          className="text-[13px]"
        />
      </div>

      {/* outputFormat — text | json */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="llm-outputFormat" className="text-[13px] font-medium text-text-1">
          输出格式
        </Label>
        <Select
          value={value.outputFormat ?? "text"}
          onValueChange={(v) => patch({ outputFormat: v as "text" | "json" })}
        >
          <SelectTrigger id="llm-outputFormat" className="h-8 text-[13px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent className="text-[13px]">
            <SelectItem value="text">文本 (text)</SelectItem>
            <SelectItem value="json">JSON</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  )
}
