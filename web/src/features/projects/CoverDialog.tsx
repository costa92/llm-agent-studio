import { useRef, useState } from "react"
import { Loader2 } from "lucide-react"
import { toast } from "sonner"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Textarea } from "@/components/ui/textarea"
import { Button } from "@/components/studio/Button"
import { AssetThumb } from "@/features/workflow/AssetThumb"
import type { Project } from "@/lib/types"
import {
  useCoverOptions,
  useGenerateCover,
  useSetCover,
  useUploadCover,
} from "./coverApi"

export interface CoverDialogProps {
  project: Project
  org: string
  trigger: React.ReactNode
  onSuccess?: () => void
}

// 项目封面设置对话框。三段：AI 生成 / 选已有 / 上传。
// 任一路径成功后 toast、关闭对话框并透传 onSuccess（容器一般无需额外动作——
// 各 mutation 已失效 ["projects", org]，卡片封面自动刷新）。
export function CoverDialog({ project, org, trigger, onSuccess }: CoverDialogProps) {
  const [open, setOpen] = useState(false)
  const [prompt, setPrompt] = useState("")
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const generate = useGenerateCover(org)
  const upload = useUploadCover(org)
  const setCover = useSetCover(org)
  const options = useCoverOptions(project.id, open)

  function finish() {
    setOpen(false)
    onSuccess?.()
  }

  async function handleGenerate() {
    try {
      await generate.mutateAsync({ projectId: project.id, prompt: prompt.trim() })
      toast.success("封面已生成")
      finish()
    } catch {
      toast.error("生成失败，请重试")
    }
  }

  async function handlePick(assetId: string) {
    try {
      await setCover.mutateAsync({ projectId: project.id, assetId })
      toast.success("已设为封面")
      finish()
    } catch {
      toast.error("设置失败，请重试")
    }
  }

  async function handleUpload() {
    if (!selectedFile) return
    try {
      await upload.mutateAsync({ projectId: project.id, file: selectedFile })
      toast.success("封面已上传")
      finish()
    } catch {
      toast.error("上传失败，请重试")
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[90vh] w-[95vw] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>设置封面</DialogTitle>
          <DialogDescription>
            用 AI 生成、从项目图片中挑选，或上传一张图片作为封面。
          </DialogDescription>
        </DialogHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto pr-1">
          {/* AI 生成 */}
          <section className="flex flex-col gap-2">
            <h3 className="font-heading text-[13px] font-medium text-text-1">
              AI 生成
            </h3>
            <Textarea
              rows={2}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="可选：描述想要的封面（留空则按项目名/风格自动生成）"
            />
            <div>
              <Button
                variant="amber"
                onClick={() => void handleGenerate()}
                disabled={generate.isPending}
              >
                {generate.isPending && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                生成
              </Button>
            </div>
          </section>

          {/* 选已有 */}
          <section className="flex flex-col gap-2">
            <h3 className="font-heading text-[13px] font-medium text-text-1">
              选已有
            </h3>
            {options.data && options.data.length > 0 ? (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(96px,1fr))] gap-2">
                {options.data.map((it) => (
                  <button
                    key={it.id}
                    type="button"
                    onClick={() => void handlePick(it.id)}
                    disabled={setCover.isPending}
                    className="overflow-hidden rounded-[10px] border border-line transition-colors hover:border-text-3 disabled:opacity-60"
                  >
                    <AssetThumb assetId={it.id} className="aspect-square w-full" />
                  </button>
                ))}
              </div>
            ) : (
              <p className="text-[12px] text-text-3">
                该项目暂无可选图片，先用 AI 生成或上传
              </p>
            )}
          </section>

          {/* 上传 */}
          <section className="flex flex-col gap-2">
            <h3 className="font-heading text-[13px] font-medium text-text-1">
              上传
            </h3>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              onChange={(e) => setSelectedFile(e.target.files?.[0] ?? null)}
              className="text-[12px] text-text-2 file:mr-3 file:rounded-md file:border file:border-line file:bg-bg-raised file:px-3 file:py-1.5 file:text-text-1"
            />
            {selectedFile && (
              <p className="text-[12px] text-text-3">{selectedFile.name}</p>
            )}
            <div>
              <Button
                variant="amber"
                onClick={() => void handleUpload()}
                disabled={!selectedFile || upload.isPending}
              >
                {upload.isPending && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                上传
              </Button>
            </div>
          </section>
        </div>
      </DialogContent>
    </Dialog>
  )
}
