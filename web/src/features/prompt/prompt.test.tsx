import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { Style } from "@/lib/types"
import { PromptBuilder, PromptBuilderView } from "./PromptBuilder"

afterEach(() => {
  vi.restoreAllMocks()
})

const STYLES: Style[] = [
  { name: "日漫", suffix: "anime style --style anime" },
  { name: "写实", suffix: "photorealistic --style realistic" },
]

describe("PromptBuilderView", () => {
  it("renders prompt input, style chips, and preview box", () => {
    render(
      <PromptBuilderView
        prompt=""
        onPromptChange={vi.fn()}
        style=""
        onStyleChange={vi.fn()}
        styles={STYLES}
        onBuild={vi.fn()}
        built={undefined}
      />,
    )

    expect(screen.getByLabelText("基础 Prompt")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "日漫" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "写实" })).toBeInTheDocument()
    expect(screen.getByText("Prompt Builder")).toBeInTheDocument()
  })

  it("disables the build button while prompt is empty, enables once filled", () => {
    const { rerender } = render(
      <PromptBuilderView
        prompt=""
        onPromptChange={vi.fn()}
        style=""
        onStyleChange={vi.fn()}
        styles={STYLES}
        onBuild={vi.fn()}
        built={undefined}
      />,
    )
    expect(screen.getByRole("button", { name: "预览拼装" })).toBeDisabled()

    rerender(
      <PromptBuilderView
        prompt="海边少女"
        onPromptChange={vi.fn()}
        style=""
        onStyleChange={vi.fn()}
        styles={STYLES}
        onBuild={vi.fn()}
        built={undefined}
      />,
    )
    expect(screen.getByRole("button", { name: "预览拼装" })).toBeEnabled()
  })

  it("toggles style selection on chip click", async () => {
    const onStyleChange = vi.fn()
    const user = userEvent.setup()
    render(
      <PromptBuilderView
        prompt="海边少女"
        onPromptChange={vi.fn()}
        style=""
        onStyleChange={onStyleChange}
        styles={STYLES}
        onBuild={vi.fn()}
        built={undefined}
      />,
    )

    await user.click(screen.getByRole("button", { name: "日漫" }))
    expect(onStyleChange).toHaveBeenCalledWith("日漫")
  })

  it("renders the built preview text", () => {
    render(
      <PromptBuilderView
        prompt="海边少女"
        onPromptChange={vi.fn()}
        style="日漫"
        onStyleChange={vi.fn()}
        styles={STYLES}
        onBuild={vi.fn()}
        built="海边少女, anime style --style anime"
      />,
    )
    expect(
      screen.getByText("海边少女, anime style --style anime"),
    ).toBeInTheDocument()
  })
})

describe("PromptBuilder (container build flow)", () => {
  it("inputs a prompt, picks a style, builds, and shows the assembled preview", async () => {
    const onBuild = vi
      .fn()
      .mockResolvedValue("海边少女, anime style --style anime")
    const user = userEvent.setup()

    render(<PromptBuilder styles={STYLES} onBuild={onBuild} />)

    await user.type(screen.getByLabelText("基础 Prompt"), "海边少女")
    await user.click(screen.getByRole("button", { name: "日漫" }))
    await user.click(screen.getByRole("button", { name: "预览拼装" }))

    await waitFor(() => expect(onBuild).toHaveBeenCalledWith("海边少女", "日漫"))
    expect(
      await screen.findByText("海边少女, anime style --style anime"),
    ).toBeInTheDocument()
  })

  it("shows an error message when build fails", async () => {
    const onBuild = vi.fn().mockRejectedValue(new Error("boom"))
    const user = userEvent.setup()

    render(<PromptBuilder styles={STYLES} onBuild={onBuild} />)

    await user.type(screen.getByLabelText("基础 Prompt"), "海边少女")
    await user.click(screen.getByRole("button", { name: "预览拼装" }))

    expect(await screen.findByText("预览失败，请重试")).toBeInTheDocument()
  })
})
