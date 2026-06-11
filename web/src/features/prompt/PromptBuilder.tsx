import { useState } from "react"
import { Textarea } from "@/components/ui/textarea"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import { PromptBox } from "@/components/studio/PromptBox"
import { cn } from "@/lib/utils"
import type { Style } from "@/lib/types"

// 纯展示/受控视图：base prompt textarea + 风格 chip 选择 + build 预览（mono PromptBox）。
// 受控（value/onChange 由调用方持有）—— 可作为建项目表单 / 审核重生成表单的内嵌组件，
// 也可由下方 PromptBuilder 容器独立成页。
export interface PromptBuilderViewProps {
  // base prompt（受控）。
  prompt: string
  onPromptChange: (value: string) => void
  // 风格选择（受控）；"" = 不附加风格（后端原样返回 base）。
  style: string
  onStyleChange: (value: string) => void
  // 风格库（GET /api/prompt-styles）。
  styles: Style[] | undefined
  stylesLoading?: boolean
  // 触发 build 预览。
  onBuild: () => void
  building?: boolean
  // build 结果（拼装后的 prompt）。
  built: string | undefined
  buildError?: boolean
  // 嵌入态隐藏标题（默认带标题，独立成页用）。
  embedded?: boolean
}

export function PromptBuilderView({
  prompt,
  onPromptChange,
  style,
  onStyleChange,
  styles,
  stylesLoading = false,
  onBuild,
  building = false,
  built,
  buildError = false,
  embedded = false,
}: PromptBuilderViewProps) {
  const canBuild = prompt.trim() !== "" && !building

  return (
    <div className={cn("flex flex-col gap-5", !embedded && "p-6")}>
      {!embedded && (
        <header>
          <h1 className="font-heading text-[22px] font-bold text-text-1">
            Prompt Builder
          </h1>
          <p className="mt-1 text-[12.5px] text-text-3">
            输入基础 prompt、挑一个风格，预览拼装后的最终 prompt。
          </p>
        </header>
      )}

      <div className="flex flex-col gap-2">
        <Label htmlFor="prompt-base" className="text-text-2">
          基础 Prompt
        </Label>
        <Textarea
          id="prompt-base"
          value={prompt}
          onChange={(e) => onPromptChange(e.target.value)}
          placeholder="例如：黄昏的海边，一个少女望向远方"
          className="min-h-24 border-line bg-bg-base text-text-1"
        />
      </div>

      <div className="flex flex-col gap-2">
        <span className="text-sm font-medium text-text-2">风格</span>
        {stylesLoading ? (
          <p className="text-[12.5px] text-text-3">加载风格中…</p>
        ) : (
          <div className="flex flex-wrap gap-2" role="group" aria-label="风格选择">
            {(styles ?? []).map((s) => {
              const active = s.name === style
              return (
                <button
                  key={s.name}
                  type="button"
                  aria-pressed={active}
                  onClick={() => onStyleChange(active ? "" : s.name)}
                  className={cn(
                    "rounded-full border px-[11px] py-1 text-[12px] font-medium transition-colors",
                    active
                      ? "border-amber bg-amber/12 text-amber"
                      : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
                  )}
                >
                  {s.name}
                </button>
              )
            })}
          </div>
        )}
      </div>

      <div>
        <Button variant="amber" disabled={!canBuild} onClick={onBuild}>
          {building ? "预览中…" : "预览拼装"}
        </Button>
      </div>

      <div className="flex flex-col gap-2">
        <span className="text-sm font-medium text-text-2">预览</span>
        {buildError ? (
          <p className="text-[12.5px] text-danger">预览失败，请重试</p>
        ) : (
          <PromptBox prompt={built ?? ""} />
        )}
      </div>
    </div>
  )
}

// 容器：独立成页时自取风格库 + 持有 prompt/style 本地态 + 调 build mutation。
// 嵌入复用（建项目/重生成）的调用方应直接用上方受控的 PromptBuilderView。
export interface PromptBuilderProps {
  styles: Style[] | undefined
  stylesLoading?: boolean
  // (prompt, style) → 拼装后的 prompt。容器持有结果。
  onBuild: (prompt: string, style: string) => Promise<string>
}

export function PromptBuilder({
  styles,
  stylesLoading,
  onBuild,
}: PromptBuilderProps) {
  const [prompt, setPrompt] = useState("")
  const [style, setStyle] = useState("")
  const [built, setBuilt] = useState<string | undefined>(undefined)
  const [building, setBuilding] = useState(false)
  const [buildError, setBuildError] = useState(false)

  return (
    <PromptBuilderView
      prompt={prompt}
      onPromptChange={(v) => {
        setPrompt(v)
        setBuildError(false)
      }}
      style={style}
      onStyleChange={setStyle}
      styles={styles}
      stylesLoading={stylesLoading}
      building={building}
      built={built}
      buildError={buildError}
      onBuild={async () => {
        setBuilding(true)
        setBuildError(false)
        try {
          setBuilt(await onBuild(prompt, style))
        } catch {
          setBuildError(true)
        } finally {
          setBuilding(false)
        }
      }}
    />
  )
}
