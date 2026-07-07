import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { usePromptStyles } from "@/features/projects/api"
import type { WorkflowSettings } from "@/lib/types"

// 画布级面板（与 PropertiesPanel / InputsSchemaPanel 并列）：编辑整条工作流的生成
// 设置。内容类型/风格解耦——生成风格由工作流决定，而非建项目时的隐藏默认。
// 受控组件：settings 来自父（WorkflowCanvas 状态），onChange 上抛新对象。
export interface WorkflowSettingsPanelProps {
  settings: WorkflowSettings
  onChange: (next: WorkflowSettings) => void
}

// radix Select 的 item 不能用空串值——用哨兵表示「继承项目（不指定）」。
const INHERIT = "__inherit__"

export function WorkflowSettingsPanel({
  settings,
  onChange,
}: WorkflowSettingsPanelProps) {
  const { data: styles } = usePromptStyles()

  function patch(p: Partial<WorkflowSettings>) {
    onChange({ ...settings, ...p })
  }

  const styleValue = settings.style ? settings.style : INHERIT

  return (
    <aside className="flex w-64 shrink-0 flex-col gap-3 overflow-y-auto border-l border-line bg-bg-surface p-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-[13px] font-semibold text-text-1">工作流设置</h2>
        <p className="text-[11px] text-text-3">
          本工作流的生成设置。留空 = 继承项目；运行期输入可再覆盖。
        </p>
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="wf-settings-style">风格</Label>
        <Select
          value={styleValue}
          onValueChange={(v) => patch({ style: v === INHERIT ? "" : v })}
        >
          <SelectTrigger id="wf-settings-style">
            <SelectValue placeholder="继承项目" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={INHERIT}>继承项目（不指定）</SelectItem>
            {(styles ?? []).map((s) => (
              <SelectItem key={s.name} value={s.name}>
                {s.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="wf-settings-content-type">内容类型（可选）</Label>
        <Input
          id="wf-settings-content-type"
          placeholder="继承项目"
          value={settings.contentType ?? ""}
          onChange={(e) => patch({ contentType: e.target.value })}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="wf-settings-target-platform">目标平台（可选）</Label>
        <Input
          id="wf-settings-target-platform"
          placeholder="继承项目"
          value={settings.targetPlatform ?? ""}
          onChange={(e) => patch({ targetPlatform: e.target.value })}
        />
      </div>
    </aside>
  )
}
