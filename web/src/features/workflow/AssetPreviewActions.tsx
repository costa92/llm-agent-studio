import { getAccessToken } from "@/lib/apiClient"
import { toast } from "sonner"
import { ExternalLink, Copy } from "lucide-react"
import { Button } from "@/components/studio/Button"

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
    <div className={className}>
      <Button
        type="button"
        variant="ghost"
        className="flex-1 text-[12px] gap-1.5 py-1 px-3"
        onClick={handleOpen}
      >
        <ExternalLink className="h-3.5 w-3.5" />
        在新标签页打开
      </Button>
      <Button
        type="button"
        variant="ghost"
        className="flex-1 text-[12px] gap-1.5 py-1 px-3"
        onClick={handleCopy}
      >
        <Copy className="h-3.5 w-3.5" />
        复制链接
      </Button>
    </div>
  )
}
