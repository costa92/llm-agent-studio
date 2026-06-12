import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { z } from "zod"
import { toast } from "sonner"
import { useRole } from "@/app/rbac"
import { AdminGate } from "@/features/cost/AdminGate"
import { ReviewBoardView } from "@/features/review/ReviewBoardPage"
import {
  useAccept,
  useAsset,
  useRegenerate,
  useReject,
  useReviewQueue,
} from "@/features/review/api"
import { hitlErrorMessage } from "@/features/review/hitlError"
import { requireOrgParam } from "@/app/org"

// T11：HITL 审核看板（admin 门禁，accept/reject/regenerate，版本血缘）。
// ?asset= typed search param 控制 Drawer 开合（UI-spec §7.6 / 5.1）。
const reviewSearchSchema = z.object({
  asset: z.string().optional(),
  // Phase 2 工作台「去审核」CTA 携来的项目筛选参数（Phase 3 接消费；此处仅声明使其有效/可跳转）。
  project: z.string().optional(),
})

export const Route = createFileRoute("/_authed/orgs/$org/review")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  validateSearch: reviewSearchSchema,
  component: ReviewPage,
})

function ReviewPage() {
  const { org } = Route.useParams()
  const { asset: selectedId } = Route.useSearch()
  const navigate = useNavigate()
  const role = useRole(org)
  const { isAdmin } = role

  const queue = useReviewQueue(org)
  const detail = useAsset(selectedId ?? "")
  const accept = useAccept(org)
  const reject = useReject(org)
  const regenerate = useRegenerate(org)

  // 更新 ?asset=（null = 关闭 Drawer）。
  function selectAsset(id: string | null): void {
    void navigate({
      to: "/orgs/$org/review",
      params: { org },
      search: id ? { asset: id } : {},
    })
  }

  function handleAccept(id: string): void {
    accept.mutate(id, {
      onSuccess: () => {
        toast.success("已采纳")
        selectAsset(null)
      },
      // 非 pending → 409 防重 toast。
      onError: (err) => toast.error(hitlErrorMessage(err)),
    })
  }

  // 退回走可撤销 toast（UI-spec §11 默认决策 1，不弹模态）。
  // 注：撤销仅是 UX 取消意图——后端无 un-reject 端点，故在确认窗口内才真正提交 reject。
  function handleReject(id: string): void {
    let undone = false
    toast("已退回该资产", {
      duration: 5000,
      action: {
        label: "撤销",
        onClick: () => {
          undone = true
        },
      },
      onAutoClose: () => {
        if (undone) return
        reject.mutate(id, {
          onSuccess: () => selectAsset(null),
          onError: (err) => toast.error(hitlErrorMessage(err)),
        })
      },
      onDismiss: () => {
        if (undone) return
        reject.mutate(id, {
          onSuccess: () => selectAsset(null),
          onError: (err) => toast.error(hitlErrorMessage(err)),
        })
      },
    })
  }

  function handleRegenerate(id: string, prompt: string): void {
    regenerate.mutate(
      { id, prompt },
      {
        onSuccess: () => {
          toast.success("已提交重生成")
          selectAsset(null)
        },
        // 非 pending → 409；配额超限 → 429。
        onError: (err) => toast.error(hitlErrorMessage(err)),
      },
    )
  }

  return (
    <AdminGate role={role}>
      <ReviewBoardView
        queue={queue.data}
        isLoading={queue.isLoading}
        isError={queue.isError}
        onRetry={() => void queue.refetch()}
        isAdmin={isAdmin}
        selectedId={selectedId ?? null}
        onSelect={selectAsset}
        detail={detail.data}
        detailLoading={selectedId != null && detail.isLoading}
        onAccept={handleAccept}
        onReject={handleReject}
        onRegenerate={handleRegenerate}
      />
    </AdminGate>
  )
}
