import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
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
// matchMedia 缺失/抛错（老 webview、测试环境）时回退 dark-studio —— 与 index.html
// 内联脚本同款防御，避免 Provider 初始化崩溃（两处逻辑必须一致以保证不闪烁）。
function systemDefault(): Theme {
  try {
    return window.matchMedia("(prefers-color-scheme: light)").matches
      ? "light"
      : "dark-studio"
  } catch {
    return "dark-studio"
  }
}

// 解析当前应用主题：合法存储值优先，否则系统默认。与 index.html 内联脚本逐分支等价。
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
  // 用户是否在本会话显式选过主题。以内存为准（而非 localStorage），这样即便
  // 隐私模式 setItem 失败、选择没持久化，系统明暗变化也不会覆盖这次选择。
  const explicitlyChosen = useRef(false)

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme)
  }, [theme])

  // 用户未显式选择时，跟随系统明暗的实时变化；一旦手动选过（本会话内存标记或
  // localStorage 有合法值）即不再被系统切换覆盖。matchMedia 缺失则静默跳过监听。
  useEffect(() => {
    let mq: MediaQueryList
    try {
      mq = window.matchMedia("(prefers-color-scheme: light)")
    } catch {
      return
    }
    const onChange = () => {
      if (explicitlyChosen.current) return
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
    explicitlyChosen.current = true
    try {
      localStorage.setItem(STORAGE_KEY, t)
    } catch {
      /* 隐私模式：本会话内仍生效（靠 explicitlyChosen 防系统覆盖），仅不持久化 */
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
