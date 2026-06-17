import { describe, it, expect, vi, beforeEach } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { PromptPanel } from "./PromptPanel"

const writeText = vi.fn().mockResolvedValue(undefined)
vi.stubGlobal("navigator", { clipboard: { writeText } })

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

describe("PromptPanel", () => {
  beforeEach(() => writeText.mockClear())

  it("默认折叠：提示词文本不可见", () => {
    render(<PromptPanel illustrationPrompt="一只小熊" narration="从前有座山" />)
    expect(screen.queryByText("一只小熊")).not.toBeInTheDocument()
  })

  it("点击展开 → 显示插图 prompt / 旁白 / 模型", () => {
    render(
      <PromptPanel
        illustrationPrompt="一只小熊"
        narration="从前有座山"
        provider="openai"
        model="gpt-image-1"
        voice="alloy"
      />,
    )
    fireEvent.click(screen.getByText("查看提示词"))
    expect(screen.getByText("一只小熊")).toBeInTheDocument()
    expect(screen.getByText("从前有座山")).toBeInTheDocument()
    expect(screen.getByText(/openai/)).toBeInTheDocument()
    expect(screen.getByText(/gpt-image-1/)).toBeInTheDocument()
  })

  it("点复制 → 调 clipboard.writeText", () => {
    render(<PromptPanel illustrationPrompt="一只小熊" />)
    fireEvent.click(screen.getByText("查看提示词"))
    fireEvent.click(screen.getByText("复制"))
    expect(writeText).toHaveBeenCalledWith("一只小熊")
  })
})
