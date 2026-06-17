import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { PictureBookConfigForm } from "./PictureBookConfigForm"
import { emptyPictureBookConfig } from "./pbConfig"
import type { PictureBookConfig } from "@/lib/types"

describe("PictureBookConfigForm", () => {
  it("选年龄段自动填充页数/旁白风格/书籍类型", async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()

    render(
      <PictureBookConfigForm
        value={emptyPictureBookConfig}
        onChange={onChange}
      />,
    )

    await user.click(screen.getByRole("button", { name: "3-6" }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).toEqual(
      expect.objectContaining({
        ageBand: "3-6",
        pageCount: 16,
        narrationStyle: "plain",
        bookType: "narrative",
      }),
    )
  })

  it("wordless 隐藏旁白音色与旁白风格", () => {
    const value: PictureBookConfig = {
      ...emptyPictureBookConfig,
      bookType: "wordless",
    }

    render(<PictureBookConfigForm value={value} onChange={() => {}} />)

    expect(screen.queryByText("旁白音色")).toBeNull()
    expect(screen.queryByText("旁白风格")).toBeNull()
  })

  it("切换主题 chip 把该主题并入 onChange", async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()

    render(
      <PictureBookConfigForm
        value={emptyPictureBookConfig}
        onChange={onChange}
      />,
    )

    await user.click(screen.getByRole("button", { name: "勇气" }))

    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0].themes).toContain("courage")
  })
})
