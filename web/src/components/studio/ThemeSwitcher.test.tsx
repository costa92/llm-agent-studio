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

  it("打开菜单显示三项主题", async () => {
    const user = userEvent.setup()
    renderSwitcher()
    await user.click(screen.getByRole("button", { name: "切换主题" }))
    expect(await screen.findByText("暗色工作室")).toBeInTheDocument()
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
})
