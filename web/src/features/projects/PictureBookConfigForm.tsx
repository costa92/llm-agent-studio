import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Label } from "@/components/ui/label"
import { cn } from "@/lib/utils"
import type { PictureBookConfig } from "@/lib/types"
import {
  AGE_BANDS,
  BOOK_TYPES,
  ILLUSTRATION_STYLES,
  NARRATION_STYLES,
  PAGE_COUNTS,
  THEMES,
  ageDefaults,
  type AgeBand,
} from "./pbConfig"

// 受控绘本配置表单。父组件持 PictureBookConfig 状态，onChange 收整份新值。
// Radix Select.Item 不允许空串 value，故用 "__none__" 哨兵代表「未选」。
const NONE = "__none__"

export interface PictureBookConfigFormProps {
  value: PictureBookConfig
  onChange: (next: PictureBookConfig) => void
}

export function PictureBookConfigForm({
  value,
  onChange,
}: PictureBookConfigFormProps) {
  // 选年龄段：把该年龄段默认（页数/旁白风格/书籍类型）覆盖进当前值。
  const pickAge = (age: AgeBand) => {
    const d = ageDefaults[age]
    onChange({
      ...value,
      ageBand: age,
      pageCount: d.pageCount,
      narrationStyle: d.narrationStyle,
      bookType: d.bookType,
    })
  }

  const toggleTheme = (theme: string) => {
    const has = value.themes.includes(theme)
    onChange({
      ...value,
      themes: has
        ? value.themes.filter((t) => t !== theme)
        : [...value.themes, theme],
    })
  }

  // 无字书无旁白 → 隐藏旁白音色与旁白风格。
  const wordless = value.bookType === "wordless"

  return (
    <div className="flex flex-col gap-4 rounded-lg border border-line bg-bg-raised/40 p-4">
      {/* 年龄段：分段按钮。 */}
      <div className="flex flex-col gap-1.5">
        <Label>年龄段</Label>
        <div className="flex gap-2">
          {AGE_BANDS.map((age) => (
            <button
              key={age}
              type="button"
              onClick={() => pickAge(age)}
              className={cn(
                "rounded-md border px-4 py-[7px] text-[13px] font-medium transition-colors",
                value.ageBand === age
                  ? "border-amber bg-amber text-primary-foreground"
                  : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
              )}
            >
              {age}
            </button>
          ))}
        </div>
        <p className="text-[11.5px] text-text-3">
          选年龄段会自动填好页数、旁白风格与书籍类型，可再手动调整。
        </p>
      </div>

      {/* 两列下拉：书籍类型 / 插画风格 / 页数 / 旁白风格。 */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="pb-bookType">书籍类型</Label>
          <Select
            value={value.bookType || NONE}
            onValueChange={(v) =>
              onChange({ ...value, bookType: v === NONE ? "" : v })
            }
          >
            <SelectTrigger id="pb-bookType">
              <SelectValue placeholder="选择书籍类型" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>未指定</SelectItem>
              {BOOK_TYPES.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="pb-illustrationStyle">插画风格</Label>
          <Select
            value={value.illustrationStyle || NONE}
            onValueChange={(v) =>
              onChange({ ...value, illustrationStyle: v === NONE ? "" : v })
            }
          >
            <SelectTrigger id="pb-illustrationStyle">
              <SelectValue placeholder="选择插画风格" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>未指定</SelectItem>
              {ILLUSTRATION_STYLES.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="pb-pageCount">页数</Label>
          <Select
            value={value.pageCount ? String(value.pageCount) : NONE}
            onValueChange={(v) =>
              onChange({ ...value, pageCount: v === NONE ? 0 : Number(v) })
            }
          >
            <SelectTrigger id="pb-pageCount">
              <SelectValue placeholder="选择页数" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>未指定</SelectItem>
              {PAGE_COUNTS.map((n) => (
                <SelectItem key={n} value={String(n)}>
                  {n} 页
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {!wordless && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pb-narrationStyle">旁白风格</Label>
            <Select
              value={value.narrationStyle || NONE}
              onValueChange={(v) =>
                onChange({ ...value, narrationStyle: v === NONE ? "" : v })
              }
            >
              <SelectTrigger id="pb-narrationStyle">
                <SelectValue placeholder="选择旁白风格" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>未指定</SelectItem>
                {NARRATION_STYLES.map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        {/* 旁白音色：MVP 仅占位「默认音色」；组织音色列表暂不接入。
            TODO: 后续接 org 音色列表（GET 音色），把 voice 值改为具体音色 id。 */}
        {!wordless && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pb-voice">旁白音色</Label>
            <Select
              value={value.voice || NONE}
              onValueChange={(v) =>
                onChange({ ...value, voice: v === NONE ? "" : v })
              }
            >
              <SelectTrigger id="pb-voice">
                <SelectValue placeholder="默认音色" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>默认音色</SelectItem>
              </SelectContent>
            </Select>
          </div>
        )}
      </div>

      {/* 主题：多选 chips。 */}
      <div className="flex flex-col gap-1.5">
        <Label>主题（可多选）</Label>
        <div className="flex flex-wrap gap-2">
          {THEMES.map((o) => {
            const active = value.themes.includes(o.value)
            return (
              <button
                key={o.value}
                type="button"
                aria-pressed={active}
                onClick={() => toggleTheme(o.value)}
                className={cn(
                  "rounded-full border px-3 py-1 text-[12px] transition-colors",
                  active
                    ? "border-amber bg-amber/15 text-amber"
                    : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
                )}
              >
                {o.label}
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}
