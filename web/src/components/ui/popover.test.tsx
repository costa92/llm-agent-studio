import { describe, expect, it } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { Popover, PopoverTrigger, PopoverContent } from "./popover"

describe("Popover", () => {
  it("点触发器后渲染内容", () => {
    render(
      <Popover>
        <PopoverTrigger>打开</PopoverTrigger>
        <PopoverContent>面板内容</PopoverContent>
      </Popover>,
    )
    expect(screen.queryByText("面板内容")).toBeNull()
    fireEvent.click(screen.getByText("打开"))
    expect(screen.getByText("面板内容")).toBeTruthy()
  })
})
