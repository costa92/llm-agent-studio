import { useMemo } from "react"
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  type Node,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import "./canvasTheme.css"
import type { WorkflowNode as WorkflowNodeType } from "@/lib/types"
import { toReactFlow, type StudioNodeData } from "./canvasModel"
import { WorkflowNode } from "./WorkflowNode"
import { NodePalette } from "./NodePalette"
import { PropertiesPanel } from "./PropertiesPanel"
import { NODE_COLOR } from "./nodeColor"

// Phase 1：只读工作流画布外壳。三栏布局（节点面板 / 画布 / 属性面板）。
// 不接 onConnect / onDrop / onNodesChange——纯渲染既有 DAG。编辑能力留到 Phase 2。
export interface WorkflowCanvasProps {
  workflowName: string
  nodes: WorkflowNodeType[]
  // 返回项目页（顶栏「返回」）。由路由层注入，画布本身不持有路由知识。
  onBack?: () => void
}

const nodeTypes = { studio: WorkflowNode }

function CanvasInner({ nodes }: { nodes: WorkflowNodeType[] }) {
  const { nodes: rfNodes, edges: rfEdges } = useMemo(
    () => toReactFlow(nodes),
    [nodes],
  )
  return (
    <div className="workflow-canvas relative flex-1">
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        nodeTypes={nodeTypes}
        fitView
        proOptions={{ hideAttribution: false }}
      >
        <Background />
        <Controls />
        <MiniMap
          nodeColor={(n) =>
            NODE_COLOR[(n as Node<StudioNodeData>).data.node.type] ??
            "var(--line)"
          }
        />
      </ReactFlow>
    </div>
  )
}

export function WorkflowCanvas({
  workflowName,
  nodes,
  onBack,
}: WorkflowCanvasProps) {
  return (
    <ReactFlowProvider>
      <div className="flex h-full flex-col bg-bg-base">
        {/* 顶栏：返回 / 工作流名 / 保存（占位，禁用）。 */}
        <header className="flex items-center justify-between border-b border-line bg-bg-surface px-4 py-2.5">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={onBack}
              className="text-[12px] text-text-3 hover:text-text-1"
            >
              ← 返回项目
            </button>
            <span className="text-[12px] text-text-3">/</span>
            <h1 className="text-[14px] font-semibold text-text-1">
              {workflowName}
            </h1>
          </div>
          <button
            type="button"
            disabled
            className="cursor-not-allowed rounded-md border border-line px-3 py-1.5 text-[12px] text-text-2 opacity-60"
            title="保存（即将上线）"
          >
            保存
          </button>
        </header>

        {/* 主区：三栏。 */}
        <div className="flex min-h-0 flex-1">
          <NodePalette />
          <CanvasInner nodes={nodes} />
          <PropertiesPanel />
        </div>
      </div>
    </ReactFlowProvider>
  )
}
