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
import { AssetThumb } from "@/features/workflow/AssetThumb.tsx"
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
  // AI 生成/上传成功后的预览 id（让用户在关闭前视觉确认新封面）。
  const [generatedCoverId, setGeneratedCoverId] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const generate = useGenerateCover(org)
  const upload = useUploadCover(org)
  const setCover = useSetCover(org)
  const options = useCoverOptions(project.id, open)

  function finish() {
    setOpen(false)
    setGeneratedCoverId(null)
    setSelectedFile(null)
    setPrompt("")
    onSuccess?.()
  }

  async function handleGenerate() {
    try {
      const res = await generate.mutateAsync({ projectId: project.id, prompt: prompt.trim() })
      toast.success("封面已设置")
      // 让用户先看到新封面再手动关闭，而非一帧就闪没
      setGeneratedCoverId(res.coverAssetId)
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
      const res = await upload.mutateAsync({ projectId: project.id, file: selectedFile })
      toast.success("封面已上传")
      setGeneratedCoverId(res.coverAssetId)
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
              disabled={!!generatedCoverId}
            />
            <div className="flex items-center gap-2">
              <Button
                variant="amber"
                onClick={() => void handleGenerate()}
                disabled={generate.isPending || !!generatedCoverId}
              >
                {generate.isPending && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                生成
              </Button>
              {generatedCoverId && (
                <Button
                  variant="ghost"
                  onClick={finish}
                  data-testid="cover-generated-done"
                >
                  完成
                </Button>
              )}
            </div>
          </section>

          {/* AI 生成 / 上传成功后的新封面预览：让用户在关闭前视觉确认。
              仅当 generatedCoverId 非空时渲染；覆盖在「选已有」之上的语义提示。 */}
          {generatedCoverId && (
            <section
              data-testid="cover-generated-preview"
              className="flex flex-col gap-2 rounded-[10px] border border-amber/40 bg-amber/5 p-3"
            >
              <div className="flex items-center gap-2 text-[12px] font-medium text-amber">
                <span>✓ 已设为新封面</span>
              </div>
              <div className="overflow-hidden rounded-[8px] border border-line">
                <AssetThumb
                  assetId={generatedCoverId}
                  className="aspect-video w-full object-cover"
                />
              </div>
            </section>
          )}

          {/* 选已有 */}
          <section className="flex flex-col gap-2">
            <h3 className="font-heading text-[13px] font-medium text-text-1">
              选已有
            </h3>
            {options.data && options.data.length > 0 ? (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(96px,1fr))] gap-2">
                {options.data.map((it) => {
                  // 当前封面 = project.coverAssetId（API 返回的最新值）；
                  // 刚生成的 generatedCoverId 同步覆盖同一字段以便用户重开时立即看到。
                  const isCurrent =
                    it.id === (generatedCoverId ?? project.coverAssetId)
                  return (
                    <button
                      key={it.id}
                      type="button"
                      onClick={() => void handlePick(it.id)}
                      disabled={setCover.isPending}
                      aria-current={isCurrent ? "true" : undefined}
                      className={
                        "group relative overflow-hidden rounded-[10px] border transition-colors disabled:opacity-60 " +
                        (isCurrent
                          ? "border-amber ring-1 ring-amber/40"
                          : "border-line hover:border-text-3")
                      }
                    >
                      <AssetThumb assetId={it.id} className="aspect-square w-full" />
                      {isCurrent && (
                        <span className="absolute left-1 top-1 rounded bg-amber px-1.5 py-0.5 font-heading text-[10px] font-medium text-bg-base">
                          当前封面
                        </span>
                      )}
                    </button>
                  )
                })}
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
              aria-label="选择封面图片"
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
