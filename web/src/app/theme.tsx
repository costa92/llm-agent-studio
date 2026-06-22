import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

// 单一来源：此列表须与 index.html 内联脚本白名单、index.css 的 [data-theme] 选择器一致。
export const THEMES = ["dark-studio", "light", "cinematic"] as const
export type Theme = (typeof THEMES)[number]
const STORAGE_KEY = "studio-theme"

function isTheme(v: unknown): v is Theme {
  return typeof v === "string" && (THEMES as readonly string[]).includes(v)
}

// 首次（无合法存储值）按系统明暗偏好选：系统亮→light，系统暗→dark-studio。
function systemDefault(): Theme {
  return window.matchMedia("(prefers-color-scheme: light)").matches
    ? "light"
    : "dark-studio"
}

function readStored(): Theme {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (isTheme(v)) return v
  } catch {
    /* localStorage 不可用：回退系统默认 */
  }
  return systemDefault()
}

interface ThemeCtx {
  theme: Theme
  setTheme: (t: Theme) => void
}

const ThemeContext = createContext<ThemeCtx | null>(null)

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStored)

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme)
  }, [theme])

  // 用户未显式选择（localStorage 无值）时，跟随系统明暗的实时变化。
  // 一旦用户手动选过（localStorage 有值），不再被系统切换覆盖。
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: light)")
    const onChange = () => {
      let stored: string | null = null
      try {
        stored = localStorage.getItem(STORAGE_KEY)
      } catch {
        /* localStorage 不可用：视为未显式选择，跟随系统 */
      }
      if (!isTheme(stored)) {
        setThemeState(mq.matches ? "light" : "dark-studio")
      }
    }
    mq.addEventListener("change", onChange)
    return () => mq.removeEventListener("change", onChange)
  }, [])

  const setTheme = useCallback((t: Theme) => {
    try {
      localStorage.setItem(STORAGE_KEY, t)
    } catch {
      /* 隐私模式：本会话内仍生效，仅不持久化 */
    }
    setThemeState(t)
  }, [])

  const value = useMemo<ThemeCtx>(
    () => ({ theme, setTheme }),
    [theme, setTheme],
  )

  return <ThemeContext value={value}>{children}</ThemeContext>
}

export function useTheme(): ThemeCtx {
  const c = useContext(ThemeContext)
  if (!c) throw new Error("useTheme must be used within a ThemeProvider")
  return c
}
