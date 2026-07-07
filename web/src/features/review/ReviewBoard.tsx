import { toast } from "sonner"
import { useRole } from "@/app/rbac"
import { useProjects } from "@/features/projects/api"
import { flattenPages } from "@/features/library/keyset"
import { ReviewBoardView } from "./ReviewBoardPage"
import {
  useAccept,
  useAsset,
  useRegenerate,
  useReject,
  useReviewQueue,
} from "./api"
import { hitlErrorMessage } from "./hitlError"

// 宿主无关的审核容器：装配审核队列 + 详情 + HITL mutation + 三个 handler，渲染 ReviewBoardView。
// 选中态（selectedId）由外部注入——路由传 URL ?asset= 派生的；Stage2 run 抽屉传本地 useState。
// 容器不自己管 selectedId，只透传，故同一容器可复用于「审核台整页」与「run 内融合抽屉」两个宿主。
export interface ReviewBoardProps {
  org: string
  // 项目筛选（null/缺省 = org 级全量队列）。
  projectFilter?: string | null
  // 外部注入的选中资产 id（null = 详情抽屉关闭）。
  selectedId: string | null
  onSelect: (id: string | null) => void
  // 清除项目筛选回调（可选；缺省则筛选 chip 的「查看全部」无操作）。
  onClearProjectFilter?: () => void
  // 审完闭环 CTA（空态时）：返回作品 / 看成品预览（Stage2 抽屉注入；路由侧可暂不传）。
  onBackToWork?: () => void
  onOpenPreview?: () => void
  // 变体 A（run 内融合抽屉）：详情内联为右栏双栏布局，透传给 View。
  inlineDetail?: boolean
}

export function ReviewBoard({
  org,
  projectFilter,
  selectedId,
  onSelect,
  onClearProjectFilter,
  onBackToWork,
  onOpenPreview,
  inlineDetail,
}: ReviewBoardProps) {
  const role = useRole(org)
  const { isAdmin } = role

  // ?project= 存在时把队列收窄到该项目；缺省即 org 级收件箱。
  const queue = useReviewQueue(org, projectFilter ?? undefined)
  // keyset 多页累积成单个有序队列（去重防御同资产库）。
  const queueItems = flattenPages(queue.data?.pages)
  const detail = useAsset(selectedId ?? "")
  const accept = useAccept(org)
  const reject = useReject(org)
  const regenerate = useRegenerate(org)

  // 项目 hex id → 项目名（首页项目列表内解析；解析不到 View 回退 id，不崩）。
  const projects = useProjects(org)
  const projectName = projectFilter
    ? projects.data?.find((p) => p.id === projectFilter)?.name
    : undefined
  // 项目 id → 名映射：org 级混杂队列按项目分组时给每组表头一个可读来源名（P2 来源可见性）。
  const projectNames: Record<string, string> = {}
  for (const p of projects.data ?? []) projectNames[p.id] = p.name

  function handleAccept(id: string): void {
    accept.mutate(id, {
      onSuccess: () => {
        toast.success("已采纳")
        onSelect(null)
      },
      // 非 pending → 409 防重 toast。
      onError: (err) => toast.error(hitlErrorMessage(err)),
    })
  }

  // 退回：显式确认后真正提交（确认弹窗逻辑在 View）；保留 409 防重处理 + 成功后清选中。
  function handleReject(id: string): void {
    reject.mutate(id, {
      onSuccess: () => {
        toast.success("已退回")
        onSelect(null)
      },
      onError: (err) => toast.error(hitlErrorMessage(err)),
    })
  }

  function handleRegenerate(id: string, prompt: string): void {
    regenerate.mutate(
      { id, prompt },
      {
        onSuccess: () => {
          toast.success("已提交重生成")
          onSelect(null)
        },
        // 非 pending → 409；配额超限 → 429。
        onError: (err) => toast.error(hitlErrorMessage(err)),
      },
    )
  }

  // 批量采纳（仅 accept，前端串行）：逐个 await 避免并发 429/409；失败收集但不中断。
  //   结束发一条汇总 toast；采纳成功的资产随队列失效 refetch 离队。
  async function handleAcceptMany(ids: string[]): Promise<void> {
    let ok = 0
    let fail = 0
    for (const id of ids) {
      try {
        await accept.mutateAsync(id)
        ok += 1
      } catch {
        // 单张失败（如 409 已处理）不中断整批，累计后汇总。
        fail += 1
      }
    }
    if (fail > 0) {
      toast.error(`已采纳 ${ok} 张 · ${fail} 张失败`)
    } else {
      toast.success(`已采纳 ${ok} 张`)
    }
    // 批量采纳后清选中态：被采纳的资产已离队，避免 inline 右栏残留已采纳资产的详情
    //（与单张采纳成功后 onSelect(null) 行为一致）。
    onSelect(null)
  }

  // 批量退回（前端串行）：与批量采纳同构——后端无 bulk 端点（accept 也是前端串行），
  //   故对称地逐个 await reject.mutateAsync，失败收集不中断，结束发汇总 toast。
  //   退回是终态（无 un-reject 端点），故 View 侧先经批量确认弹窗才会调到这里。
  async function handleRejectMany(ids: string[]): Promise<void> {
    let ok = 0
    let fail = 0
    for (const id of ids) {
      try {
        await reject.mutateAsync(id)
        ok += 1
      } catch {
        fail += 1
      }
    }
    if (fail > 0) {
      toast.error(`已退回 ${ok} 张 · ${fail} 张失败`)
    } else {
      toast.success(`已退回 ${ok} 张`)
    }
    onSelect(null)
  }

  return (
    <ReviewBoardView
      queue={queueItems}
      isLoading={queue.isLoading}
      isError={queue.isError}
      onRetry={() => void queue.refetch()}
      hasNextPage={queue.hasNextPage}
      isFetchingNextPage={queue.isFetchingNextPage}
      onLoadMore={() => void queue.fetchNextPage()}
      isAdmin={isAdmin}
      projectFilter={projectFilter ?? null}
      projectName={projectName}
      projectNames={projectNames}
      onClearProjectFilter={onClearProjectFilter ?? (() => {})}
      selectedId={selectedId}
      onSelect={onSelect}
      detail={detail.data}
      detailLoading={selectedId != null && detail.isLoading}
      onAccept={handleAccept}
      onReject={handleReject}
      onRegenerate={handleRegenerate}
      onAcceptMany={(ids) => void handleAcceptMany(ids)}
      onRejectMany={(ids) => void handleRejectMany(ids)}
      onBackToWork={onBackToWork}
      onOpenPreview={onOpenPreview}
      inlineDetail={inlineDetail}
      actionPending={accept.isPending || reject.isPending || regenerate.isPending}
    />
  )
}
