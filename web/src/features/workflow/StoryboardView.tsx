import { Skeleton } from "@/components/ui/skeleton"
import type { Shot } from "./api"

export interface StoryboardViewProps {
  shots: Shot[] | undefined
  isLoading: boolean
  isError: boolean
}

// 分镜栅格：auto-fill minmax(168px,1fr)——按容器宽自适应列数（抽屉窄时减列、宽时增列，
// 列宽拉伸填满不留右侧空白）。每格 shot 编号 + 镜头 + 描述 + prompt 摘要。
// 真实形态见 internal/agents/storyboard.go（shotNo/camera/scene/action/prompt/duration）。
export function StoryboardView({ shots, isLoading, isError }: StoryboardViewProps) {
  if (isLoading) {
    return (
      <div className="grid grid-cols-[repeat(auto-fill,minmax(168px,1fr))] gap-3.5 p-5">
        {Array.from({ length: 8 }).map((_, i) => (
          <Skeleton key={i} className="h-[172px] rounded-[10px]" />
        ))}
      </div>
    )
  }
  if (isError) {
    return (
      <div className="grid h-full place-items-center p-6 text-center text-text-2">
        <p>分镜数据加载失败</p>
      </div>
    )
  }
  if (!shots || shots.length === 0) {
    return (
      <div className="grid h-full place-items-center p-6 text-center text-text-3">
        <p>分镜尚未拆解</p>
      </div>
    )
  }

  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(168px,1fr))] gap-3.5 p-5">
      {shots.map((shot, i) => (
        <div
          key={shot.shotNo ?? i}
          className="flex flex-col gap-2 rounded-[10px] border border-line bg-bg-surface p-3.5"
        >
          <div className="flex items-center justify-between gap-2">
            <span className="font-mono text-[14px] font-semibold text-board">
              #{shot.shotNo ?? i + 1}
            </span>
            {shot.camera && (
              <span className="truncate text-[12px] text-text-3">{shot.camera}</span>
            )}
          </div>
          {shot.action && (
            <p className="line-clamp-4 text-[13.5px] leading-relaxed text-text-1">{shot.action}</p>
          )}
          {shot.prompt && (
            <p className="mt-auto line-clamp-3 font-mono text-[11.5px] leading-relaxed text-text-3">
              {shot.prompt}
            </p>
          )}
        </div>
      ))}
    </div>
  )
}
