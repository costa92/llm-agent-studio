import { cn } from "@/lib/utils"
import { layerize } from "@/lib/graphLayout"
import type { GraphNode, GraphEdge } from "@/lib/projectState"

// 节点 agent 语义色(CSS 变量,见 src/index.css)。未知 type 用中性线色。
const NODE_COLOR: Record<string, string> = {
  planner: "var(--amber)",
  script: "var(--script)",
  storyboard: "var(--board)",
  asset: "var(--asset)",
  review: "var(--review)",
}

export interface GraphViewProps {
  nodes: GraphNode[]
  edges: GraphEdge[]
  // script/storyboard/asset 节点点击 → 容器打开对应工件抽屉或右栏预览。
  onSelectNode?: (node: GraphNode) => void
}

// 分层竖向 DAG:每层一行、同层节点并排;层间竖向连接线表达依赖方向。
// 复用 TimelineStage 的节点视觉语言(done 填色 / running 琥珀旋转环 / failed 红)。
export function GraphView({ nodes, edges, onSelectNode }: GraphViewProps) {
  if (nodes.length === 0) {
    return (
      <div
        data-slot="graph-empty"
        className="flex flex-col items-center justify-center gap-1.5 py-16 text-center"
      >
        <p className="text-[13px] text-text-2">等待规划…</p>
        <p className="text-[12px] text-text-3">工作流节点产出后在此渲染</p>
      </div>
    )
  }
  const layers = layerize(nodes, edges)
  return (
    <div data-slot="graph" className="mx-auto max-w-[560px]">
      {layers.map((layer, li) => {
        // 上一层全部 done 时连接线着该层首节点 agent 色；否则虚线灰。
        const prevLayer = li > 0 ? layers[li - 1] : null
        const prevAllDone = prevLayer != null && prevLayer.every((n) => n.status === "done")
        const connColor =
          prevAllDone && prevLayer && prevLayer.length > 0
            ? (NODE_COLOR[prevLayer[0].type] ?? "var(--line)")
            : undefined
        return (
          <div key={layer[0].id} data-slot="graph-layer" className="relative pb-[30px]">
            {li > 0 && (
              <span
                aria-hidden
                data-slot="graph-connector"
                data-linked={prevAllDone ? "true" : "false"}
                className={cn(
                  "absolute left-1/2 -top-[30px] h-[30px] w-0.5 -translate-x-1/2",
                  prevAllDone
                    ? "bg-[var(--conn-color)]"
                    : "border-l border-dashed border-line bg-transparent",
                )}
                style={connColor ? { ["--conn-color" as string]: connColor } : undefined}
              />
            )}
            <div className="flex flex-wrap items-start justify-center gap-3">
              {layer.map((node) => (
                <GraphNodeCard key={node.id} node={node} onSelectNode={onSelectNode} />
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function GraphNodeCard({
  node,
  onSelectNode,
}: {
  node: GraphNode
  onSelectNode?: (node: GraphNode) => void
}) {
  const color = NODE_COLOR[node.type] ?? "var(--line)"
  const isDone = node.status === "done"
  const isRunning = node.status === "running"
  const isFailed = node.status === "failed"
  const clickable =
    !!onSelectNode &&
    (node.type === "script" || node.type === "storyboard" || !!node.assetId)

  const inner = (
    <>
      <div
        className={cn(
          "relative grid h-7 w-7 place-items-center rounded-full border-2 bg-bg-base",
          isDone && "border-[var(--cur)] bg-[var(--cur)]",
          isRunning && "border-amber",
          isFailed && "border-danger bg-danger/15",
          !isDone && !isRunning && !isFailed && "border-line",
        )}
        style={{ ["--cur" as string]: color }}
      >
        {isRunning && (
          <span
            aria-hidden
            className="absolute -inset-1.5 rounded-full border-2 border-dashed border-amber motion-safe:animate-[spin_3s_linear_infinite]"
          />
        )}
        <span
          className={cn(
            "font-sans text-[10px] font-bold",
            isDone ? "text-bg-base" : isFailed ? "text-danger" : "text-text-3",
          )}
        >
          {isDone ? "✓" : ""}
        </span>
      </div>
      <span className="mt-1 max-w-[110px] truncate text-center text-[11.5px] text-text-2">
        {node.label}
      </span>
    </>
  )

  if (clickable) {
    return (
      <button
        type="button"
        aria-label={node.label}
        data-slot="graph-node"
        data-status={node.status}
        onClick={() => onSelectNode!(node)}
        className="flex flex-col items-center rounded-md p-1 transition-colors hover:bg-bg-raised"
      >
        {inner}
      </button>
    )
  }
  return (
    <div
      data-slot="graph-node"
      data-status={node.status}
      className="flex flex-col items-center p-1"
    >
      {inner}
    </div>
  )
}
