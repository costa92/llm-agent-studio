import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { z } from "zod"
import { useRole } from "@/app/rbac"
import { AdminGate } from "@/features/cost/AdminGate"
import { ReviewBoard } from "@/features/review/ReviewBoard"
import { requireOrgParam } from "@/app/org"

// T11：HITL 审核看板（admin 门禁，accept/reject/regenerate，版本血缘）。
// ?asset= typed search param 控制 Drawer 开合（UI-spec §7.6 / 5.1）。
// 装配（队列/详情/mutation/handler）已抽入宿主无关的 <ReviewBoard>；本路由只负责
// URL ↔ 选中态/项目筛选的映射 + AdminGate。
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

  return (
    <AdminGate role={role}>
      <ReviewBoard
        org={org}
        projectFilter={project ?? null}
        onClearProjectFilter={clearProjectFilter}
        selectedId={selectedId ?? null}
        onSelect={selectAsset}
      />
    </AdminGate>
  )
}
