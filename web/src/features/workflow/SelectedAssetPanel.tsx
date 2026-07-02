import { useState } from "react"
import { toast } from "sonner"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "@/components/studio/Button"
import { cn } from "@/lib/utils"
import { AssetThumb } from "./AssetThumb"
import { AssetPreviewActions } from "./AssetPreviewActions"
import { useAccept, useReject } from "@/features/review/api"
import { hitlErrorMessage } from "@/features/review/hitlError"
import { ConfirmRejectDialog } from "@/features/review/ConfirmRejectDialog"
import type { AssetDetail } from "@/lib/types"

export interface SelectedAssetPanelProps {
  org: string
  assetId: string
  // 与审核台同一 admin 信号（useRole(org).isAdmin）。
  isAdmin: boolean
  // 选中资产详情（容器经 useAsset 拉取）；加载中可空。
  detail?: AssetDetail
  className?: string
}

// 右栏选中资产面板：更大预览 + 元数据（type/version/status）+ pending 态内联采纳/拒绝。
// accept/reject 复用 features/review 的 hooks（自带失效，不发 toast）；toast + 错误文案在此发，
// 与审核台 409/429 对齐（hitlErrorMessage）。非 pending → 维持 AssetPreviewActions。
export function SelectedAssetPanel({ org, assetId, isAdmin, detail, className }: SelectedAssetPanelProps) {
  const accept = useAccept(org)
  const reject = useReject(org)
  const qc = useQueryClient()
  const asset = detail?.asset
  const isPending = asset?.status === "pending_acceptance"
  // 退回确认弹窗开合（与审核台一致：就地退回须显式确认，消除无确认退回的不一致）。
  const [confirmReject, setConfirmReject] = useState(false)

  function onAccept() {
    accept.mutate(assetId, {
      onSuccess: () => {
        toast.success("已采纳")
        void qc.invalidateQueries({ queryKey: ["asset", assetId] })
      },
      onError: (err) => toast.error(hitlErrorMessage(err)),
    })
  }
  function onReject() {
    reject.mutate(assetId, {
      onSuccess: () => {
        toast.success("已退回")
        void qc.invalidateQueries({ queryKey: ["asset", assetId] })
      },
      onError: (err) => toast.error(hitlErrorMessage(err)),
    })
  }

  const busy = accept.isPending || reject.isPending

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <AssetThumb assetId={assetId} alt="选中素材" className="h-[220px] w-full" />
      {asset && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12px]">
          <dt className="text-text-3">类型</dt>
          <dd className="text-text-1">{asset.type}</dd>
          <dt className="text-text-3">版本</dt>
          <dd className="text-text-1">v{asset.version}</dd>
          <dt className="text-text-3">状态</dt>
          <dd className="text-text-1">{asset.status}</dd>
        </dl>
      )}
      {isPending && isAdmin ? (
        <div className="flex gap-2">
          <Button variant="amber" className="flex-1" onClick={onAccept} disabled={busy}>采纳</Button>
          {/* 退回先开确认弹窗，确认才真正 reject（与审核台一致）。 */}
          <Button variant="ghost" className="flex-1" onClick={() => setConfirmReject(true)} disabled={busy}>拒绝</Button>
        </div>
      ) : (
        <AssetPreviewActions assetId={assetId} className="flex gap-2" />
      )}

      <ConfirmRejectDialog
        open={confirmReject}
        onOpenChange={setConfirmReject}
        onConfirm={() => {
          setConfirmReject(false)
          onReject()
        }}
      />
    </div>
  )
}
