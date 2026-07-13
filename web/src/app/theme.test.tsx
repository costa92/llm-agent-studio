import { describe, it, expect, beforeEach, afterEach, vi } from "vitest"
import { render, screen, act } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ThemeProvider } from "./theme"
import { useTheme } from "./theme-context"

// 把 matchMedia 设成指定 matches 值（true=系统亮）。
function stubMatchMedia(matches: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })),
  )
}

// 可控 matchMedia：捕获 change 监听器，测试里可手动 fire（携带翻转后的 matches）。
function stubControllableMatchMedia(initialMatches: boolean) {
  const listeners = new Set<() => void>()
  const state = { matches: initialMatches }
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      get matches() {
        return state.matches
      },
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: (_: string, cb: () => void) => listeners.add(cb),
      removeEventListener: (_: string, cb: () => void) => listeners.delete(cb),
      dispatchEvent: () => false,
    })),
  )
  // 返回一个触发器：把系统偏好切到 matches 并通知监听器。
  return (matches: boolean) => {
    state.matches = matches
    listeners.forEach((cb) => cb())
  }
}

// 探针组件：把当前 theme/choice 写进 DOM，并暴露切到 light / auto 的按钮。
function Probe() {
  const { theme, choice, setChoice } = useTheme()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <span data-testid="choice">{choice}</span>
      <button onClick={() => setChoice("light")}>to-light</button>
      <button onClick={() => setChoice("auto")}>to-auto</button>
    </div>
  )
}

describe("ThemeProvider", () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute("data-theme")
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("无存储 + 系统暗 → 默认 dark-studio", () => {
    stubMatchMedia(false)
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark-studio")
  })

  it("无存储 + 系统亮 → 默认 light", () => {
    stubMatchMedia(true)
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("light")
  })

  it("有合法存储值 → 用存储值（忽略系统）", () => {
    stubMatchMedia(true)
    localStorage.setItem("studio-theme", "cinematic")
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("cinematic")
  })

  it("非法存储值 → 回退系统默认", () => {
    stubMatchMedia(false)
    localStorage.setItem("studio-theme", "xyz")
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
  })

  it("无存储 → choice 默认 auto", () => {
    stubMatchMedia(false)
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("choice").textContent).toBe("auto")
  })

  it("setChoice 持久化并写 data-theme", async () => {
    stubMatchMedia(false)
    const user = userEvent.setup()
    render(<ThemeProvider><Probe /></ThemeProvider>)
    await user.click(screen.getByText("to-light"))
    expect(localStorage.getItem("studio-theme")).toBe("light")
    expect(document.documentElement.getAttribute("data-theme")).toBe("light")
    expect(screen.getByTestId("theme").textContent).toBe("light")
    expect(screen.getByTestId("choice").textContent).toBe("light")
  })

  it("useTheme 在 Provider 外抛错", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {})
    expect(() => render(<Probe />)).toThrow(/ThemeProvider/)
    spy.mockRestore()
  })

  it("未显式选择时，系统明暗变化实时跟随", async () => {
    const fire = stubControllableMatchMedia(false) // 初始系统暗 → dark-studio
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
    await act(async () => fire(true)) // 系统切到亮
    expect(screen.getByTestId("theme").textContent).toBe("light")
    await act(async () => fire(false)) // 系统切回暗
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
  })

  it("已显式选择时，系统变化不覆盖用户选择", async () => {
    const fire = stubControllableMatchMedia(false)
    localStorage.setItem("studio-theme", "cinematic") // 用户已选
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("cinematic")
    await act(async () => fire(true)) // 系统切到亮 —— 不应覆盖
    expect(screen.getByTestId("theme").textContent).toBe("cinematic")
  })

  it("选具体主题后再选「auto」→ 恢复跟随系统并持久化 auto", async () => {
    const fire = stubControllableMatchMedia(false) // 系统暗
    localStorage.setItem("studio-theme", "light") // 已选具体主题
    const user = userEvent.setup()
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("light")
    await act(async () => fire(true)) // 系统切亮 —— 具体主题不跟随
    expect(screen.getByTestId("theme").textContent).toBe("light")
    await user.click(screen.getByText("to-auto")) // 切回 auto → 立即解析为当前系统(亮)=light
    expect(screen.getByTestId("choice").textContent).toBe("auto")
    expect(localStorage.getItem("studio-theme")).toBe("auto")
    expect(screen.getByTestId("theme").textContent).toBe("light")
    await act(async () => fire(false)) // 系统切暗 → auto 实时跟随 → dark-studio
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
  })

  it("显式选择即使未持久化（私有模式 setItem 抛错）也不被系统变化覆盖", async () => {
    const fire = stubControllableMatchMedia(false) // 系统暗 → 默认 dark-studio
    const setItemSpy = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("private mode")
      })
    const user = userEvent.setup()
    render(<ThemeProvider><Probe /></ThemeProvider>)
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
    await user.click(screen.getByText("to-light")) // setTheme 写盘抛错，但内存标记已置
    expect(screen.getByTestId("theme").textContent).toBe("light")
    await act(async () => fire(false)) // 系统切暗 —— 有 bug 才会覆盖回 dark-studio
    expect(screen.getByTestId("theme").textContent).toBe("light")
    setItemSpy.mockRestore()
  })

  it("matchMedia 缺失时不崩溃，回退 dark-studio", () => {
    vi.stubGlobal("matchMedia", undefined)
    expect(() =>
      render(<ThemeProvider><Probe /></ThemeProvider>),
    ).not.toThrow()
    expect(screen.getByTestId("theme").textContent).toBe("dark-studio")
  })

  it("localStorage.getItem 抛错时回退系统默认（系统亮→light，不硬编码 dark）", () => {
    stubMatchMedia(true) // 系统亮
    const getItemSpy = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("private mode")
      })
    render(<ThemeProvider><Probe /></ThemeProvider>)
    // readStored 的 catch 须落到 systemDefault（→light），与 index.html 内联脚本收敛；
    // 若回退成硬编码 dark-studio 则首屏会与内联脚本不一致而闪烁。
    expect(screen.getByTestId("theme").textContent).toBe("light")
    getItemSpy.mockRestore()
  })
})
