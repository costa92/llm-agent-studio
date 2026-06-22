import { useCallback, useRef, useState } from "react"
import { useReactFlow } from "@xyflow/react"
import type { RFNode, RFEdge } from "./canvasModel"

// 撤销/重做基础设施（Phase A）。基于 ReactFlow store 的 getNodes/getEdges/setNodes/setEdges。
// 必须在 <ReactFlowProvider> 内使用（CanvasInner 满足）。
// 快照为 nodes/edges 数组的浅拷贝——画布所有变更处理器都「整体替换」节点对象
// （patchSelected/renameSelected/addNodeAt 等均用 spread 新建对象，绝不原地变更），
// 故浅拷贝足够；若将来出现原地变更某节点对象，则需对那部分深拷贝。
type Snapshot = { nodes: RFNode[]; edges: RFEdge[] }

export function useUndoRedo(opts?: { maxHistory?: number }) {
  const { getNodes, getEdges, setNodes, setEdges } = useReactFlow<RFNode, RFEdge>()
  const past = useRef<Snapshot[]>([])
  const future = useRef<Snapshot[]>([])
  const [canUndo, setCanUndo] = useState(false)
  const [canRedo, setCanRedo] = useState(false)

  // 每个变更处理器在「应用变更之前」调一次：把当前状态压入 past，并清空 future
  //（新分支动作使重做栈失效）。past 超出 maxHistory 时丢弃最旧快照。
  const takeSnapshot = useCallback(() => {
    past.current.push({ nodes: [...getNodes()], edges: [...getEdges()] })
    if (past.current.length > (opts?.maxHistory ?? 100)) past.current.shift()
    future.current = []
    setCanUndo(true)
    setCanRedo(false)
  }, [getNodes, getEdges, opts?.maxHistory])

  const undo = useCallback(() => {
    const prev = past.current.pop()
    if (!prev) return
    future.current.push({ nodes: [...getNodes()], edges: [...getEdges()] })
    setNodes(prev.nodes)
    setEdges(prev.edges)
    setCanUndo(past.current.length > 0)
    setCanRedo(true)
  }, [getNodes, getEdges, setNodes, setEdges])

  const redo = useCallback(() => {
    const next = future.current.pop()
    if (!next) return
    past.current.push({ nodes: [...getNodes()], edges: [...getEdges()] })
    setNodes(next.nodes)
    setEdges(next.edges)
    setCanRedo(future.current.length > 0)
    setCanUndo(true)
  }, [getNodes, getEdges, setNodes, setEdges])

  return { takeSnapshot, undo, redo, canUndo, canRedo }
}
