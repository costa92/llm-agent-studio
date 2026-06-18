import type { ReactNode } from "react"

// 行级动作描述符：表达 edit/delete/set-default/reveal/copy 等所有行内/卡片角操作。
export interface RowAction<T> {
  // key 用于 React list key，缺省退回到 label。多个动作 label 相同时必须显式传 key。
  key?: string
  label: string
  icon?: ReactNode
  onClick: (item: T) => void
  variant?: "ghost" | "amber" | "destructive"
  hidden?: (item: T) => boolean
  disabled?: (item: T) => boolean
}

// 表格列定义。
export interface Column<T> {
  key: string
  header: ReactNode
  cell: (item: T) => ReactNode
  className?: string
}
