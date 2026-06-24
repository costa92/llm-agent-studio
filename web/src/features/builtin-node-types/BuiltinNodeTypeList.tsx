import { CrudResourcePage, DataView } from "../common/crud"
import { NODE_COLOR } from "@/features/workflow-canvas/nodeColor"
import { useBuiltinNodeTypes } from "./api"

// ─── BuiltinNodeTypeList ───────────────────────────────────────────────────────
// 内置工作流节点类型一览（只读）。全局目录，系统定义、不可增删改；与组织级
// 自定义节点（custom:）页相互独立。路由：/orgs/{org}/builtin-node-types（admin-only）。
//
// 不传 onCreate / rowActions → CrudResourcePage 无「新建」、DataView 无「操作」列，整页只读。
// 颜色取自前端 NODE_COLOR（后端不返回 color；未知 type 用中性线色兜底）。

export function BuiltinNodeTypeList() {
  const query = useBuiltinNodeTypes()
  const items = query.data ?? []

  return (
    <CrudResourcePage
      title="内置节点"
      description="内置节点由系统定义，不可增删改；在工作流画布中直接使用。"
      isLoading={query.isLoading}
      isError={query.isError}
      onRetry={() => void query.refetch()}
      isEmpty={items.length === 0}
      emptyHint="暂无内置节点类型。"
    >
      <DataView
        layout="table"
        items={items}
        getId={(t) => t.type}
        columns={[
          {
            key: "label",
            header: "名称",
            cell: (t) => (
              <span className="flex items-center gap-2">
                <span
                  className="inline-block h-3 w-3 rounded-full flex-shrink-0"
                  style={{ backgroundColor: NODE_COLOR[t.type] ?? "var(--line)" }}
                />
                {t.label}
              </span>
            ),
          },
          { key: "type", header: "标识", className: "font-mono text-[12px] text-text-2", cell: (t) => t.type },
          { key: "description", header: "说明", className: "text-[13px] text-text-2", cell: (t) => t.description },
          {
            key: "builtin",
            header: "",
            className: "text-right",
            cell: () => (
              <span className="inline-flex items-center rounded-full border border-line px-2 py-0.5 text-[11px] text-text-3">
                内置 · 只读
              </span>
            ),
          },
        ]}
      />
    </CrudResourcePage>
  )
}
