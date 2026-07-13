import { createContext, useContext } from "react"

// 具体渲染主题（写入 html[data-theme]）。须与 index.html 内联脚本白名单、
// index.css 的 [data-theme] 选择器一致。
export const THEMES = ["dark-studio", "light", "cinematic"] as const
export type Theme = (typeof THEMES)[number]
// 用户的选择：「auto」= 跟随系统明暗，或一个具体主题。default = auto。
export type ThemeChoice = Theme | "auto"

export interface ThemeCtx {
  // 实际生效的渲染主题（用于触发器图标等）。
  theme: Theme
  // 用户的选择（用于 switcher 选中态；含 auto）。
  choice: ThemeChoice
  setChoice: (c: ThemeChoice) => void
}

export const ThemeContext = createContext<ThemeCtx | null>(null)

export function useTheme(): ThemeCtx {
  const c = useContext(ThemeContext)
  if (!c) throw new Error("useTheme must be used within a ThemeProvider")
  return c
}
