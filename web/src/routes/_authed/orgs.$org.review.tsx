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
  // Phase 2 工作台「去审核」CTA 携来的项目筛选参数（Phase 3 T4 接消费）。
  project: z.string().optional(),
})

export const Route = createFileRoute("/_authed/orgs/$org/review")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  validateSearch: reviewSearchSchema,
  component: ReviewPage,
})

function ReviewPage() {
  const { org } = Route.useParams()
  const { asset: selectedId, project } = Route.useSearch()
  const navigate = useNavigate()
  const role = useRole(org)
  const { isAdmin } = role

  // T4：?project= 存在时把队列收窄到该项目；缺省即 org 级收件箱。
  const queue = useReviewQueue(org, project)
  const detail = useAsset(selectedId ?? "")
  const accept = useAccept(org)
  const reject = useReject(org)
  const regenerate = useRegenerate(org)

  // 更新 ?asset=（null = 关闭 Drawer）；保留当前 project 筛选。
  function selectAsset(id: string | null): void {
    void navigate({
      to: "/orgs/$org/review",
      params: { org },
      search: { ...(id ? { asset: id } : {}), ...(project ? { project } : {}) },
    })
  }

  // T4：清除项目筛选，回到 org 级全量队列（去掉 ?project=）。
  function clearProjectFilter(): void {
    void navigate({
      to: "/orgs/$org/review",
      params: { org },
      search: selectedId ? { asset: selectedId } : {},
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

  // 退回：显式确认提交（owner 推翻 UI-spec §11 默认决策 1 的可撤销 toast 模式——
  //   「关闭/忽略 undo toast 即提交」是静默退回的陷阱，改为模态确认，仅「确认退回」才发）。
  //   确认弹窗逻辑在 View（ReviewBoardView）里——此处 onReject 是确认后真正的提交，
  //   保留 409 防重处理 + 成功后关闭 Drawer。
  function handleReject(id: string): void {
    reject.mutate(id, {
      onSuccess: () => selectAsset(null),
      onError: (err) => toast.error(hitlErrorMessage(err)),
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
        projectFilter={project ?? null}
        onClearProjectFilter={clearProjectFilter}
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
