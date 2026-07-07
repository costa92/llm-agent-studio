import { createFileRoute } from "@tanstack/react-router"
import { useState } from "react"
import { toast } from "sonner"
import { useRole } from "@/app/rbac"
import { AdminGate } from "@/features/cost/AdminGate"
import { CostCenterView } from "@/features/cost/CostCenterPage"
import {
  fetchAllGenerations,
  useGenerations,
  useOrgCost,
  useOrgCostProjects,
} from "@/features/cost/api"
import {
  RANGE_PRESETS,
  filterLedgerByRange,
  ledgerToCSV,
  rangeToParams,
} from "@/features/cost/format"
import { requireOrgParam } from "@/app/org"

// CSV 下载：加 UTF-8 BOM 让 Excel 正确识别中文列名/项目名；用完即撤销 objectURL。
function downloadCSV(filename: string, csv: string) {
  const blob = new Blob(["\uFEFF" + csv], { type: "text/csv;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

// T13：成本中心（admin-only）。导航入口已按角色隐藏（AppShell）；直访路由 → AdminGate 拦。
export const Route = createFileRoute("/_authed/orgs/$org/cost")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: CostPage,
})

function CostPage() {
  const { org } = Route.useParams()
  const role = useRole(org)
  const [rangeValue, setRangeValue] = useState("30d")
  const [isExporting, setIsExporting] = useState(false)

  // 导出当前时间范围的生成明细：取全量分页 → 按活动范围裁剪 → 手工拼 CSV 下载。
  // 明细端点不读 from/to，故裁剪在前端做（filterLedgerByRange）。空则提示不下载。
  async function handleExport() {
    if (isExporting) return
    setIsExporting(true)
    try {
      const preset =
        RANGE_PRESETS.find((p) => p.value === rangeValue) ?? RANGE_PRESETS[1]
      const rows = filterLedgerByRange(
        await fetchAllGenerations(org),
        rangeToParams(preset),
      )
      if (rows.length === 0) {
        toast.error("该时间范围内没有可导出的生成记录")
        return
      }
      const stamp = new Date().toISOString().slice(0, 10)
      downloadCSV(`成本明细-${preset.label}-${stamp}.csv`, ledgerToCSV(rows))
      toast.success(`已导出 ${rows.length} 条生成记录`)
    } catch {
      toast.error("导出失败，请重试")
    } finally {
      setIsExporting(false)
    }
  }

  // 钩子接 presetValue（稳定字符串）；range 生成挪进 queryFn 闭包，
  // 避免每次 render 推新 from/to 时间戳让 queryKey 永变 → refetch loop。
  const cost = useOrgCost(org, rangeValue)
  const projects = useOrgCostProjects(org, rangeValue)
  // 生成明细走 keyset 游标累积（useInfiniteQuery），多页信封串接成单数组。
  const generations = useGenerations(org)
  const generationRows = generations.data?.pages.flatMap((p) => p.items)

  return (
    <AdminGate role={role}>
      <CostCenterView
        aggregate={cost.data}
        projects={projects.data}
        generations={generationRows}
        hasNextPage={generations.hasNextPage}
        isFetchingNextPage={generations.isFetchingNextPage}
        onLoadMore={() => void generations.fetchNextPage()}
        isLoading={cost.isLoading || projects.isLoading || generations.isLoading}
        isError={cost.isError || projects.isError || generations.isError}
        onRetry={() => {
          void cost.refetch()
          void projects.refetch()
          void generations.refetch()
        }}
        rangeValue={rangeValue}
        onRangeChange={setRangeValue}
        onExport={() => void handleExport()}
        isExporting={isExporting}
      />
    </AdminGate>
  )
}
