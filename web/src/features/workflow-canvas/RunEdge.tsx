import { BaseEdge, getBezierPath, type EdgeProps } from "@xyflow/react"

// 运行态只读边（不带编辑态的 +/× 控制簇——运行模式那对按钮本就是 noop 死控件）。
// 活动边（data.active：上游 done & 下游 running，见 runEdges.markActiveEdges）→ 琥珀描边 +
// 沿边路径流动的 SMIL 粒子，直观表达「数据正从上游流向下游」。空闲边渲默认静态线。
//
// ⚠️ SMIL animateMotion 不受 CSS `prefers-reduced-motion` 媒体查询约束（index.css 的全局停
// 动画只管 CSS animation/transition）。故 reduced-motion 下用 JS 读 matchMedia，不挂 animateMotion，
// 仅渲静态琥珀描边。
const PARTICLE_DUR = 1.6 // 秒
// 3 颗粒子错峰起始（沿边均匀分布的流动感）。
const PARTICLE_BEGINS = [0, PARTICLE_DUR / 3, (PARTICLE_DUR * 2) / 3]

function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  )
}

export function RunEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  style,
  data,
}: EdgeProps) {
  const [edgePath] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })
  const active = (data as { active?: boolean } | undefined)?.active === true
  const animate = active && !prefersReducedMotion()

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={
          active
            ? { ...style, stroke: "var(--amber)", strokeWidth: 2 }
            : style
        }
      />
      {animate && (
        <g data-slot="run-edge-particles" aria-hidden>
          {PARTICLE_BEGINS.map((begin, i) => (
            <circle key={i} r={2.5} fill="var(--amber)">
              <animateMotion
                dur={`${PARTICLE_DUR}s`}
                begin={`${begin}s`}
                repeatCount="indefinite"
                path={edgePath}
              />
            </circle>
          ))}
        </g>
      )}
    </>
  )
}
