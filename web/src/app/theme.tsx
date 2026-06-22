import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

// 具体渲染主题（写入 html[data-theme]）。须与 index.html 内联脚本白名单、
// index.css 的 [data-theme] 选择器一致。
export const THEMES = ["dark-studio", "light", "cinematic"] as const
export type Theme = (typeof THEMES)[number]
// 用户的选择：「auto」= 跟随系统明暗，或一个具体主题。default = auto。
export type ThemeChoice = Theme | "auto"
const STORAGE_KEY = "studio-theme"

function isTheme(v: unknown): v is Theme {
  return typeof v === "string" && (THEMES as readonly string[]).includes(v)
}
function isChoice(v: unknown): v is ThemeChoice {
  return v === "auto" || isTheme(v)
}

// 系统明暗 → 具体主题：亮→light，暗→dark-studio。matchMedia 缺失/抛错（老 webview、
// 测试环境）回退 dark-studio —— 与 index.html 内联脚本同款防御，避免崩溃且保证不闪烁。
function systemTheme(): Theme {
  try {
    return window.matchMedia("(prefers-color-scheme: light)").matches
      ? "light"
      : "dark-studio"
  } catch {
    return "dark-studio"
  }
}

// 读取用户选择：合法（auto 或具体主题）则用之，否则回退 auto（跟随系统）。
// 与 index.html 内联脚本逐分支等价：auto / 缺失 / 非法 都解析为系统主题。
function readChoice(): ThemeChoice {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (isChoice(v)) return v
  } catch {
    /* localStorage 不可用：回退 auto */
  }
  return "auto"
}

// 选择 → 实际渲染主题。
function resolveTheme(choice: ThemeChoice): Theme {
  return choice === "auto" ? systemTheme() : choice
}

interface ThemeCtx {
  // 实际生效的渲染主题（用于触发器图标等）。
  theme: Theme
  // 用户的选择（用于 switcher 选中态；含 auto）。
  choice: ThemeChoice
  setChoice: (c: ThemeChoice) => void
}

const ThemeContext = createContext<ThemeCtx | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [choice, setChoiceState] = useState<ThemeChoice>(readChoice)
  const [theme, setThemeState] = useState<Theme>(() => resolveTheme(choice))

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme)
  }, [theme])

  // choice==="auto" 时订阅系统明暗变化并实时跟随；具体主题则不订阅（系统变化不影响）。
  // matchMedia 缺失/不支持 addEventListener 时静默跳过实时跟随（不崩溃）。
  useEffect(() => {
    if (choice !== "auto") return
    let mq: MediaQueryList
    try {
      mq = window.matchMedia("(prefers-color-scheme: light)")
    } catch {
      return
    }
    const onChange = () => setThemeState(systemTheme())
    mq.addEventListener("change", onChange)
    return () => mq.removeEventListener("change", onChange)
  }, [choice])

  const setChoice = useCallback((c: ThemeChoice) => {
    try {
      localStorage.setItem(STORAGE_KEY, c)
    } catch {
      /* 隐私模式：本会话内仍生效（choice state 即时更新），仅不持久化 */
    }
    setChoiceState(c)
    setThemeState(resolveTheme(c))
  }, [])

  const value = useMemo<ThemeCtx>(
    () => ({ theme, choice, setChoice }),
    [theme, choice, setChoice],
  )

  return <ThemeContext value={value}>{children}</ThemeContext>
}

export function useTheme(): ThemeCtx {
  const c = useContext(ThemeContext)
  if (!c) throw new Error("useTheme must be used within a ThemeProvider")
  return c
}
