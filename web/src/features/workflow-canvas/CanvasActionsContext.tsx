import { createContext, useContext } from "react"

// 画布交互回调上下文（Phase B）：把节点 / 边的删除·复制·插入回调下发给
// 自定义节点（WorkflowNode 的 NodeToolbar）和自定义边（StudioEdge），
// 避免把回调穿过纯的 toReactFlow / nodeTypes / edgeTypes。
// Provider 值在 CanvasInner 构建。
export interface CanvasActions {
  onDuplicateNode: (id: string) => void
  onDeleteNode: (id: string) => void
  onDeleteEdge: (id: string) => void
  // screenX/Y：浮层选择器的屏幕落点（边中点的屏幕坐标）。
  onInsertOnEdge: (id: string, screenX: number, screenY: number) => void
  // 节点尾部「+」：在该节点下方快加下游节点（screenX/Y 用于浮层选择器定位）。
  onQuickAddFrom: (nodeId: string, screenX: number, screenY: number) => void
}

const noop = () => {}

const CanvasActionsContext = createContext<CanvasActions>({
  onDuplicateNode: noop,
  onDeleteNode: noop,
  onDeleteEdge: noop,
  onInsertOnEdge: noop,
  onQuickAddFrom: noop,
})

export const CanvasActionsProvider = CanvasActionsContext.Provider

export function useCanvasActions(): CanvasActions {
  return useContext(CanvasActionsContext)
}
