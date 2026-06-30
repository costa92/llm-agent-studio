import { useEffect, useState } from "react"
import { toast } from "sonner"
import { getAccessToken } from "@/lib/apiClient"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useCreateExport, useExportJob, type ExportFormat } from "./api"

const FORMATS: { value: ExportFormat; label: string }[] = [
  { value: "pdf", label: "PDF" },
  { value: "epub", label: "EPUB" },
  { value: "zip", label: "ZIP" },
]

export interface ExportDialogProps {
  projectId: string
  open: boolean
  onClose: () => void
  // 当前查看的 plan id（与运行页一致）。省略则后端取最新 plan。
  planId?: string
}

// 绘本成书导出对话框：单选格式 → 创建导出任务 → 轮询任务态 → done 提供下载入口。
//   下载是「浏览器直连 GET」，经 ?token= 查询参带鉴权（302 attachment，让浏览器接管字节）。
export function ExportDialog({ projectId, open, onClose, planId }: ExportDialogProps) {
  const [format, setFormat] = useState<ExportFormat>("pdf")
  const [jobId, setJobId] = useState<string | null>(null)

  // 关闭后复位（下次打开重新选择 + 重新轮询）——派生式复位，避免在 effect 里 setState。
  const [trackedOpen, setTrackedOpen] = useState(open)
  if (trackedOpen !== open) {
    setTrackedOpen(open)
    if (!open) {
      setJobId(null)
      setFormat("pdf")
    }
  }

  const create = useCreateExport(projectId)
  const jobQuery = useExportJob(projectId, jobId)
  const job = jobQuery.data
  const status = job?.status

  // 任务落 failed 时提示一次（toast 非 setState，可安全置于 effect；按 status/error 变化触发）。
  useEffect(() => {
    if (status === "failed") {
      toast.error(job?.error ? `导出失败：${job.error}` : "导出失败")
    }
  }, [status, job?.error])

  // 浏览器直连下载 URL：?token= 兜底鉴权（authz v0.4.1），切勿用 apiJSON 取二进制。
  const token = getAccessToken()
  const downloadUrl = jobId
    ? `${window.location.origin}/api/exports/${jobId}/content${
        token ? `?token=${encodeURIComponent(token)}` : ""
      }`
    : ""

  // 任务进行中（已创建且未达终态）禁用「开始导出」，避免重复创建。
  const polling = !!jobId && status !== "done" && status !== "failed"
  const busy = create.isPending || polling

  async function submit() {
    try {
      const res = await create.mutateAsync({ format, planId })
      setJobId(res.jobId)
      toast.success("已开始导出")
    } catch (err) {
      const code = (err as { status?: number }).status
      toast.error(code === 429 ? "配额已用尽，请稍后再试" : "导出失败")
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>导出成书</DialogTitle>
          <DialogDescription>
            选择导出格式，生成后可下载绘本成书文件。
          </DialogDescription>
        </DialogHeader>

        {/* 格式单选：一组按钮，选中态用 default 实心、其余 outline。 */}
        <div className="flex gap-2">
          {FORMATS.map((f) => (
            <UiButton
              key={f.value}
              type="button"
              variant={format === f.value ? "default" : "outline"}
              onClick={() => setFormat(f.value)}
              disabled={busy}
            >
              {f.label}
            </UiButton>
          ))}
        </div>

        {/* 任务态区域：进行中 / 完成（下载）/ 失败（错误文案）。 */}
        {jobId && (
          <div className="text-sm">
            {polling && <p className="text-text-2">正在生成… 请稍候</p>}
            {status === "done" && (
              <div className="flex flex-col gap-2">
                <p className="text-text-2">导出完成，可下载成书文件。</p>
                <a
                  href={downloadUrl}
                  download
                  className={cn(
                    "inline-flex w-fit items-center rounded-lg border border-border bg-background px-2.5 py-1.5",
                    "text-sm font-medium hover:bg-muted hover:text-foreground",
                  )}
                >
                  下载
                </a>
              </div>
            )}
            {status === "failed" && (
              <p className="text-danger">
                导出失败{job?.error ? `：${job.error}` : ""}
              </p>
            )}
          </div>
        )}

        <DialogFooter>
          <UiButton variant="outline" onClick={onClose}>
            {status === "done" ? "关闭" : "取消"}
          </UiButton>
          <UiButton variant="default" onClick={submit} disabled={busy}>
            开始导出
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
