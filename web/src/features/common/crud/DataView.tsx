import type { ReactNode } from "react"
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table"
import { Button as UiButton } from "@/components/ui/button"
import type { Column, RowAction } from "./types"

interface DataViewProps<T> {
  items: T[]
  getId: (item: T) => string
  layout: "table" | "cards"
  rowActions?: RowAction<T>[]
  columns?: Column<T>[]
  renderCard?: (item: T, actions: ReactNode) => ReactNode
  groupBy?: (item: T) => string
  minWidthClass?: string
}

function ActionButtons<T>({ item, actions }: { item: T; actions: RowAction<T>[] }) {
  return (
    <>
      {actions
        .filter((a) => !a.hidden?.(item))
        .map((a) => (
          <UiButton key={a.key ?? a.label} variant={a.variant === "amber" ? "default" : a.variant ?? "ghost"}
            size="sm" aria-label={a.label} disabled={a.disabled?.(item)}
            onClick={() => a.onClick(item)}>
            {a.icon}{a.label}
          </UiButton>
        ))}
    </>
  )
}

// 双模式列表：table 用 columns + 末列 rowActions；cards 用 renderCard(item, actions) + 可选 groupBy。
// 空态由上层 CrudResourcePage 负责，这里假定 items 非空。
export function DataView<T>({
  items, getId, layout, rowActions = [], columns = [], renderCard, groupBy, minWidthClass,
}: DataViewProps<T>) {
  if (layout === "table") {
    return (
      <Table className={minWidthClass}>
        <TableHeader>
          <TableRow>
            {columns.map((c) => <TableHead key={c.key} className={c.className}>{c.header}</TableHead>)}
            {rowActions.length > 0 && <TableHead className="text-right">操作</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow key={getId(item)}>
              {columns.map((c) => <TableCell key={c.key} className={c.className}>{c.cell(item)}</TableCell>)}
              {rowActions.length > 0 && (
                <TableCell className="text-right">
                  <ActionButtons item={item} actions={rowActions} />
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }

  const renderItems = (list: T[]) =>
    list.map((item) => (
      <div key={getId(item)}>
        {renderCard?.(item, <ActionButtons item={item} actions={rowActions} />)}
      </div>
    ))

  if (groupBy) {
    const groups = new Map<string, T[]>()
    for (const item of items) {
      const k = groupBy(item)
      const arr = groups.get(k) ?? []
      arr.push(item)
      groups.set(k, arr)
    }
    return (
      <div className="flex flex-col gap-6">
        {[...groups.entries()].map(([key, list]) => (
          <section key={key} className="flex flex-col gap-3">
            <h3 className="text-[13px] font-semibold text-text-2">{key}</h3>
            <div className="flex flex-col gap-3">{renderItems(list)}</div>
          </section>
        ))}
      </div>
    )
  }
  return <div className="flex flex-col gap-3">{renderItems(items)}</div>
}
