import { describe, it, expect, beforeEach } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ThemeProvider } from "@/app/theme"
import { ThemeSwitcher } from "./ThemeSwitcher"

function renderSwitcher() {
  return render(
    <ThemeProvider>
      <ThemeSwitcher />
    </ThemeProvider>,
  )
}

describe("ThemeSwitcher", () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute("data-theme")
  })

  it("触发按钮有可访问名称「切换主题」", () => {
    renderSwitcher()
    expect(screen.getByRole("button", { name: "切换主题" })).toBeInTheDocument()
  })

  it("打开菜单显示四项（含跟随系统）", async () => {
    const user = userEvent.setup()
    renderSwitcher()
    await user.click(screen.getByRole("button", { name: "切换主题" }))
    expect(await screen.findByText("跟随系统")).toBeInTheDocument()
    expect(screen.getByText("暗色工作室")).toBeInTheDocument()
    expect(screen.getByText("明亮")).toBeInTheDocument()
    expect(screen.getByText("影院感")).toBeInTheDocument()
  })

  it("点选「明亮」→ 持久化 light", async () => {
    const user = userEvent.setup()
    renderSwitcher()
    await user.click(screen.getByRole("button", { name: "切换主题" }))
    await user.click(await screen.findByText("明亮"))
    expect(localStorage.getItem("studio-theme")).toBe("light")
    expect(document.documentElement.getAttribute("data-theme")).toBe("light")
  })

  it("点选「跟随系统」→ 持久化 auto", async () => {
    const user = userEvent.setup()
    localStorage.setItem("studio-theme", "light") // 先有具体选择
    renderSwitcher()
    await user.click(screen.getByRole("button", { name: "切换主题" }))
    await user.click(await screen.findByText("跟随系统"))
    expect(localStorage.getItem("studio-theme")).toBe("auto")
  })
})
