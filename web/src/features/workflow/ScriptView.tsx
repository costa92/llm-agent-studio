import { Skeleton } from "@/components/ui/skeleton"
import type { ScriptDoc } from "./api"

export interface ScriptViewProps {
  script: ScriptDoc | null | undefined
  isLoading: boolean
  // 解析失败 / 加载错误（zod 抛错 → query error）。
  isError: boolean
}

// 剧本视图：标题 + logline + 场景列表（heading/description/dialogue）。
// 真实形态见 internal/agents/script.go。loading/empty/error 三态。
export function ScriptView({ script, isLoading, isError }: ScriptViewProps) {
  if (isLoading) {
    return (
      <div className="flex flex-col gap-3 p-6">
        <Skeleton className="h-7 w-1/3 rounded-md" />
        <Skeleton className="h-20 w-full rounded-md" />
        <Skeleton className="h-20 w-full rounded-md" />
      </div>
    )
  }
  if (isError) {
    return (
      <div className="grid h-full place-items-center p-6 text-center text-text-2">
        <p>剧本数据异常，请重新运行剧本阶段</p>
      </div>
    )
  }
  if (script == null) {
    return (
      <div className="grid h-full place-items-center p-6 text-center text-text-3">
        <p>剧本尚未生成</p>
      </div>
    )
  }

  const scenes = script.scenes ?? []
  return (
    <div className="mx-auto flex max-w-[720px] flex-col gap-5 p-6">
      <header>
        {script.title && (
          <h1 className="font-heading text-[22px] font-bold text-text-1">{script.title}</h1>
        )}
        {script.logline && (
          <p className="mt-1 text-[13px] text-text-2">{script.logline}</p>
        )}
      </header>
      {scenes.length === 0 ? (
        <p className="text-[12.5px] text-text-3">（剧本暂无场景）</p>
      ) : (
        <ol className="flex flex-col gap-4">
          {scenes.map((scene, i) => (
            <li
              key={i}
              className="rounded-[10px] border border-line bg-bg-surface p-4"
            >
              {scene.heading && (
                <h3 className="mb-1.5 font-mono text-[12px] font-semibold text-script">
                  {scene.heading}
                </h3>
              )}
              {scene.description && (
                <p className="text-[12.5px] leading-relaxed text-text-2">
                  {scene.description}
                </p>
              )}
              {scene.dialogue && (
                <pre className="mt-2 whitespace-pre-wrap rounded-md border border-line bg-bg-base p-2.5 font-mono text-[11.5px] leading-relaxed text-text-2">
                  {scene.dialogue}
                </pre>
              )}
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
