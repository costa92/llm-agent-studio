import { getAccessToken } from "@/lib/apiClient"
import { toast } from "sonner"
import { ExternalLink, Copy } from "lucide-react"
import { Button } from "@/components/studio/Button"
import { cn } from "@/lib/utils"

export interface AssetPreviewActionsProps {
  assetId: string
  className?: string
}

export function AssetPreviewActions({ assetId, className }: AssetPreviewActionsProps) {
  const token = getAccessToken()
  const contentUrl = `${window.location.origin}/api/assets/${assetId}/content${
    token ? `?token=${encodeURIComponent(token)}` : ""
  }`

  const handleCopy = () => {
    navigator.clipboard.writeText(contentUrl)
      .then(() => {
        toast.success("链接已复制")
      })
      .catch((err) => {
        console.error("Failed to copy link: ", err)
        toast.error("复制失败")
      })
  }

  const handleOpen = () => {
    window.open(contentUrl, "_blank")
  }

  return (
    // 自适应：容器宽则两按钮并排；窄到放不下两个完整按钮时整体换行竖排。
    // whitespace-nowrap + min-w 兜底——按钮内文案绝不折行；放不下就整块换行，而非把字挤断。
    <div className={cn("flex flex-wrap gap-2", className)}>
      <Button
        type="button"
        variant="ghost"
        className="min-w-[9rem] flex-1 gap-1.5 whitespace-nowrap px-3 py-1 text-[12px]"
        onClick={handleOpen}
      >
        <ExternalLink className="h-3.5 w-3.5" />
        在新标签页打开
      </Button>
      <Button
        type="button"
        variant="ghost"
        className="min-w-[9rem] flex-1 gap-1.5 whitespace-nowrap px-3 py-1 text-[12px]"
        onClick={handleCopy}
      >
        <Copy className="h-3.5 w-3.5" />
        复制链接
      </Button>
    </div>
  )
}
