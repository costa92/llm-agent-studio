import { useId } from "react"
import { Settings2 } from "lucide-react"
import { Popover, PopoverTrigger, PopoverContent } from "@/components/ui/popover"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type {
  TopologySettings,
  LayoutMode,
  FocusMode,
} from "./useTopologySettings"

export interface TopologySettingsPanelProps {
  settings: TopologySettings
  update: (patch: Partial<TopologySettings>) => void
}

export function TopologySettingsPanel({
  settings,
  update,
}: TopologySettingsPanelProps) {
  const uid = useId()
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label="视图设置"
          title="视图设置"
          className="grid h-8 w-8 place-items-center rounded-md border border-line bg-bg-surface text-text-2 shadow-sm hover:text-text-1"
        >
          <Settings2 size={15} />
        </button>
      </PopoverTrigger>
      <PopoverContent>
        <div className="flex flex-col gap-3">
          <section className="flex flex-col gap-1.5">
            <p className="text-[11px] font-semibold tracking-[0.06em] text-text-3">布局</p>
            <Select
              value={settings.layout}
              onValueChange={(v) => update({ layout: v as LayoutMode })}
            >
              <SelectTrigger aria-label="布局方向" className="h-8 text-[12px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="saved">自存坐标</SelectItem>
                <SelectItem value="TB">自动·竖向</SelectItem>
                <SelectItem value="LR">自动·横向</SelectItem>
              </SelectContent>
            </Select>
            <CheckRow
              id={`${uid}-fitOnUpdate`}
              label="布局变更时自动适配视图"
              checked={settings.fitOnUpdate}
              onChange={(v) => update({ fitOnUpdate: v })}
            />
          </section>
          <section className="flex flex-col gap-1.5">
            <p className="text-[11px] font-semibold tracking-[0.06em] text-text-3">叠加</p>
            <CheckRow
              id={`${uid}-showTiming`}
              label="显示耗时"
              checked={settings.showTiming}
              onChange={(v) => update({ showTiming: v })}
            />
            <CheckRow
              id={`${uid}-flowAnimation`}
              label="数据流动画"
              checked={settings.flowAnimation}
              onChange={(v) => update({ flowAnimation: v })}
            />
          </section>
          <section className="flex flex-col gap-1.5">
            <p className="text-[11px] font-semibold tracking-[0.06em] text-text-3">聚焦</p>
            <Select
              value={settings.focus}
              onValueChange={(v) => update({ focus: v as FocusMode })}
            >
              <SelectTrigger aria-label="聚焦模式" className="h-8 text-[12px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">不聚焦</SelectItem>
                <SelectItem value="failed">聚焦失败</SelectItem>
                <SelectItem value="running">聚焦进行中</SelectItem>
              </SelectContent>
            </Select>
            <CheckRow
              id={`${uid}-hideCompleted`}
              label="隐藏已完成"
              checked={settings.hideCompleted}
              onChange={(v) => update({ hideCompleted: v })}
            />
          </section>
        </div>
      </PopoverContent>
    </Popover>
  )
}

function CheckRow({
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
      <Checkbox
        id={id}
        aria-label={label}
        checked={checked}
        onCheckedChange={(v) => onChange(v === true)}
      />
      <Label htmlFor={id} className="text-[12px] text-text-1">
        {label}
      </Label>
    </div>
  )
}
