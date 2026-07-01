import type { CustomNodeType } from "@/lib/types"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { TypeDialog } from "./TypeDialog"
import { useCustomNodeTypeCrud } from "./useCustomNodeTypeCrud"
import { DataView, ConfirmDialog } from "../common/crud"

// 节点管理模态「用户自定义节点」tab：组织级自定义节点类型的紧凑 CRUD 面板。
// 与路由页 CustomNodeTypeManager 共享 useCustomNodeTypeCrud 逻辑，只是外壳更紧凑
// （工具条 hint + 右侧「新建」，无全页标题/padding），适配 Dialog 内嵌。

export interface CustomNodeTypesPanelProps {
  org: string
}

export function CustomNodeTypesPanel({ org }: CustomNodeTypesPanelProps) {
  const { query, crud, secretNames, modelOptions, isAdmin, editTarget, dialogInitial, handleDialogSubmit } =
    useCustomNodeTypeCrud(org)
  const items = query.data ?? []

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <span className="text-[12px] text-text-3">
          组织自定义节点 · 可增删改（kind ∈ llm / http / script）。
        </span>
        <Button variant="amber" className="ml-auto px-3 py-1.5 text-[12px]" onClick={crud.openCreate}>
          新建自定义节点
        </Button>
      </div>

      {query.isError ? (
        <div className="flex flex-col items-center gap-3 py-8 text-center">
          <p className="text-text-2">加载失败</p>
          <Button variant="ghost" className="px-3 py-1.5 text-[12px]" onClick={() => void query.refetch()}>重试</Button>
        </div>
      ) : query.isLoading ? (
        <div className="flex flex-col gap-2">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-10 rounded-lg" />)}
        </div>
      ) : items.length === 0 ? (
        <p className="py-8 text-center text-[13px] text-text-3">暂无自定义节点类型，点击「新建自定义节点」开始。</p>
      ) : (
        <DataView
          layout="table"
          items={items}
          getId={(ct) => ct.id}
          columns={[
            {
              key: "label",
              header: "节点",
              cell: (ct: CustomNodeType) => (
                <span className="flex items-center gap-2">
                  <span
                    className="inline-block h-3 w-3 rounded-full flex-shrink-0"
                    style={{ backgroundColor: ct.color }}
                  />
                  {ct.label}
                </span>
              ),
            },
            { key: "kind", header: "KIND", cell: (ct: CustomNodeType) => ct.kind },
            { key: "slug", header: "Slug", className: "font-mono text-[12px] text-text-2", cell: (ct: CustomNodeType) => ct.slug },
          ]}
          rowActions={[
            { key: "edit", label: "编辑", onClick: crud.openEdit },
            { key: "delete", label: "删除", variant: "destructive" as const, onClick: crud.requestDelete },
          ]}
        />
      )}

      {/* 新建/编辑对话框 — key 变化时强制重新挂载，清除内部 useState(initial) 陈旧值。 */}
      {crud.dialog !== null && (
        <TypeDialog
          key={editTarget?.id ?? "create"}
          open={crud.dialog !== null}
          mode={crud.dialog.mode}
          initial={dialogInitial}
          submitting={crud.submitting}
          submitError={crud.submitError}
          secretNames={secretNames}
          modelOptions={modelOptions}
          isAdmin={isAdmin}
          onSubmit={handleDialogSubmit}
          onOpenChange={(open) => { if (!open) crud.closeDialog() }}
        />
      )}

      {/* 删除确认对话框。 */}
      <ConfirmDialog
        open={crud.deleteTarget !== null}
        title="确认删除自定义节点类型？"
        description={`删除「${crud.deleteTarget?.label ?? ""}」后无法撤销。`}
        confirmLabel="确认删除"
        variant="danger"
        confirming={crud.deleting}
        onConfirm={crud.confirmDelete}
        onCancel={crud.cancelDelete}
      />
    </div>
  )
}
