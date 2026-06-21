import { describe, it, expect, beforeEach, afterEach, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ThemeProvider, useTheme } from "./theme"

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

// 探针组件：把当前 theme 写进 DOM，并暴露一个切到 light 的按钮。
function Probe() {
  const { theme, setTheme } = useTheme()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <button onClick={() => setTheme("light")}>to-light</button>
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

  it("setTheme 持久化并写 data-theme", async () => {
    stubMatchMedia(false)
    const user = userEvent.setup()
    render(<ThemeProvider><Probe /></ThemeProvider>)
    await user.click(screen.getByText("to-light"))
    expect(localStorage.getItem("studio-theme")).toBe("light")
    expect(document.documentElement.getAttribute("data-theme")).toBe("light")
    expect(screen.getByTestId("theme").textContent).toBe("light")
  })

  it("useTheme 在 Provider 外抛错", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {})
    expect(() => render(<Probe />)).toThrow(/ThemeProvider/)
    spy.mockRestore()
  })
})
