import { useStore, type ReactFlowState } from "@xyflow/react"

// 对齐辅助线覆盖层（C3）：把 getHelperLines 给出的 flow 坐标引导线渲染成屏幕空间的
// 横/竖线。通过 useStore 读视口 transform [tx, ty, zoom]，flow→screen：s = flow*zoom + t。
// 颜色用 var(--amber)（三主题 token，1px）；无引导线时不渲染。
// selector 定义在组件外，引用稳定，避免每次 render 触发 store 重订阅风暴。
const selectTransform = (s: ReactFlowState) => s.transform

export function HelperLines({
  horizontal,
  vertical,
}: {
  horizontal?: number
  vertical?: number
}) {
  const [tx, ty, zoom] = useStore(selectTransform)
  if (horizontal == null && vertical == null) return null
  return (
    <div className="pointer-events-none absolute inset-0 z-10 overflow-hidden">
      {vertical != null && (
        <div
          className="absolute top-0 bottom-0"
          style={{
            left: vertical * zoom + tx,
            width: 1,
            background: "var(--amber)",
          }}
        />
      )}
      {horizontal != null && (
        <div
          className="absolute left-0 right-0"
          style={{
            top: horizontal * zoom + ty,
            height: 1,
            background: "var(--amber)",
          }}
        />
      )}
    </div>
  )
}
