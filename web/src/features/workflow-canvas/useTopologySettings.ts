import { useCallback, useEffect, useRef, useState } from "react"
import { z } from "zod"
import type { LayoutDirection } from "@/lib/autoLayout"

export type LayoutMode = "saved" | LayoutDirection // "saved" | "TB" | "LR"
export type FocusMode = "none" | "failed" | "running"

export interface TopologySettings {
  layout: LayoutMode // 每项目持久化
  fitOnUpdate: boolean
  showTiming: boolean
  flowAnimation: boolean
  focus: FocusMode
  hideCompleted: boolean
}

export const DEFAULT_TOPOLOGY_SETTINGS: TopologySettings = {
  layout: "saved",
  fitOnUpdate: true,
  showTiming: false,
  flowAnimation: true, // 历史 run 无 running 节点 → 无 active 边 → 不动画（零视觉影响）
  focus: "none",
  hideCompleted: false,
}

const PREFS_KEY = "studio.topology.prefs"
const layoutKey = (projectId: string) => `studio.topology.layout.${projectId}`

const prefsSchema = z.object({
  fitOnUpdate: z.boolean(),
  showTiming: z.boolean(),
  flowAnimation: z.boolean(),
  focus: z.enum(["none", "failed", "running"]),
  hideCompleted: z.boolean(),
})
const layoutSchema = z.enum(["saved", "TB", "LR"])

function readSettings(projectId: string): TopologySettings {
  let prefs = {
    fitOnUpdate: DEFAULT_TOPOLOGY_SETTINGS.fitOnUpdate,
    showTiming: DEFAULT_TOPOLOGY_SETTINGS.showTiming,
    flowAnimation: DEFAULT_TOPOLOGY_SETTINGS.flowAnimation,
    focus: DEFAULT_TOPOLOGY_SETTINGS.focus,
    hideCompleted: DEFAULT_TOPOLOGY_SETTINGS.hideCompleted,
  }
  try {
    const raw = localStorage.getItem(PREFS_KEY)
    if (raw) {
      const parsed = prefsSchema.safeParse(JSON.parse(raw))
      if (parsed.success) prefs = parsed.data
    }
  } catch {
    // 坏数据回落默认
  }
  let layout: LayoutMode = DEFAULT_TOPOLOGY_SETTINGS.layout
  try {
    const rawL = localStorage.getItem(layoutKey(projectId))
    if (rawL) {
      const parsed = layoutSchema.safeParse(rawL)
      if (parsed.success) layout = parsed.data
    }
  } catch {
    // 回落 saved
  }
  return { layout, ...prefs }
}

// 运行态拓扑视图偏好。layout 按 projectId 隔离持久化，其余全局共享。
export function useTopologySettings(projectId: string): {
  settings: TopologySettings
  update: (patch: Partial<TopologySettings>) => void
} {
  const [settings, setSettings] = useState<TopologySettings>(() =>
    readSettings(projectId),
  )

  // projectId 变化但 hook 未重挂时，重读新项目的存储，避免沿用旧项目的 layout。
  const prevProjectId = useRef(projectId)
  useEffect(() => {
    if (prevProjectId.current !== projectId) {
      prevProjectId.current = projectId
      setSettings(readSettings(projectId))
    }
  }, [projectId])

  const update = useCallback(
    (patch: Partial<TopologySettings>) => {
      setSettings((prev) => {
        const next = { ...prev, ...patch }
        try {
          localStorage.setItem(layoutKey(projectId), next.layout)
          // layout 单独存项目键，prefs 取其余字段存全局键。
          // eslint-disable-next-line @typescript-eslint/no-unused-vars -- 解构剔除 layout
          const { layout, ...prefs } = next
          localStorage.setItem(PREFS_KEY, JSON.stringify(prefs))
        } catch {
          // 写盘失败不阻断 UI
        }
        return next
      })
    },
    [projectId],
  )

  return { settings, update }
}
