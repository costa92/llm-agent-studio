import { Button } from "@/components/studio/Button"
import { Skeleton } from "@/components/ui/skeleton"
import { DataView } from "@/features/common/crud"
import type { Column } from "@/features/common/crud"
import type { PlatformOrg } from "@/lib/types"
import { usePlatformOrgs } from "../api"
import { formatCreatedAt } from "./utils"

// 列定义：表头/单元格/顺序与原 raw <Table> 一致（名称 / ID / 创建时间 / 成员数）。
const columns: Column<PlatformOrg>[] = [
  { key: "name", header: "名称", className: "text-text-1", cell: (o) => o.name },
  {
    key: "id",
    header: "ID",
    className: "font-mono text-[12px] text-text-3",
    cell: (o) => o.id,
  },
  {
    key: "createdAt",
    header: "创建时间",
    className: "text-text-2",
    cell: (o) => formatCreatedAt(o.createdAt),
  },
  {
    key: "memberCount",
    header: "成员数",
    className: "text-right text-text-2",
    cell: (o) => o.memberCount,
  },
]

// ── 全部组织 ────────────────────────────────────────────────────
export function OrgsSection() {
  const orgs = usePlatformOrgs()

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">全部组织</h2>
        <p className="text-[12px] text-text-3">服务端所有业务组织一览（含成员数）。</p>
      </header>

      {orgs.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">组织列表加载失败</p>
          <Button variant="ghost" onClick={() => void orgs.refetch()}>
            重试
          </Button>
        </div>
      ) : orgs.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : orgs.data && orgs.data.length > 0 ? (
        // 移动端：列保持宽度，容器横向滚动（min-w 触发 table-container 的 overflow-x-auto）。
        <DataView<PlatformOrg>
          layout="table"
          items={orgs.data}
          getId={(o) => o.id}
          columns={columns}
          minWidthClass="min-w-[640px]"
        />
      ) : (
        <p className="py-8 text-center text-[13px] text-text-3">暂无组织。</p>
      )}
    </section>
  )
}
