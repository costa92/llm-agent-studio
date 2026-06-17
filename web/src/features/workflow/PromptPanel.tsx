import { useState } from "react"
import { toast } from "sonner"
import { ChevronDown, ChevronRight, Copy } from "lucide-react"
import { cn } from "@/lib/utils"

// 提示词面板（绘本阅读器/相册灯箱）：默认折叠的「查看提示词」按钮，展开后只读展示
//   插图 prompt / 旁白文本 / 模型·音色，并提供「复制」（写插图 prompt 到剪贴板 + toast）。
export interface PromptPanelProps {
  illustrationPrompt?: string
  narration?: string
  provider?: string
  model?: string
  voice?: string
  className?: string
}

export function PromptPanel({
  illustrationPrompt,
  narration,
  provider,
  model,
  voice,
  className,
}: PromptPanelProps) {
  const [open, setOpen] = useState(false)

  // 模型·音色摘要行：有 provider/model 才拼 "provider · model"，voice 追加 "· 音色"。
  const modelLine = [
    provider && model ? `${provider} · ${model}` : provider || model || "",
    voice ? `音色 ${voice}` : "",
  ]
    .filter(Boolean)
    .join(" · ")

  const handleCopy = () => {
    // 复制插图 prompt（最常用）；无插图 prompt 时退回旁白。
    const text = illustrationPrompt || narration || ""
    navigator.clipboard
      .writeText(text)
      .then(() => toast.success("提示词已复制"))
      .catch(() => toast.error("复制失败"))
  }

  return (
    <div className={cn("text-[12px]", className)}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1 text-text-3 transition-colors hover:text-text-1"
      >
        {open ? (
          <ChevronDown className="h-3.5 w-3.5" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5" />
        )}
        查看提示词
      </button>
      {open && (
        <div className="mt-2 space-y-2 rounded-[10px] border border-line bg-bg-raised p-3">
          {illustrationPrompt && (
            <div className="space-y-1">
              <div className="text-[11px] font-medium text-text-3">插图提示词</div>
              <p className="whitespace-pre-wrap break-words text-text-2">
                {illustrationPrompt}
              </p>
            </div>
          )}
          {narration && (
            <div className="space-y-1">
              <div className="text-[11px] font-medium text-text-3">旁白</div>
              <p className="whitespace-pre-wrap break-words text-text-2">{narration}</p>
            </div>
          )}
          {modelLine && (
            <div className="space-y-1">
              <div className="text-[11px] font-medium text-text-3">模型·音色</div>
              <p className="text-text-2">{modelLine}</p>
            </div>
          )}
          <button
            type="button"
            onClick={handleCopy}
            className="inline-flex items-center gap-1 text-text-3 underline-offset-2 transition-colors hover:text-text-1 hover:underline"
          >
            <Copy className="h-3.5 w-3.5" />
            复制
          </button>
        </div>
      )}
    </div>
  )
}
