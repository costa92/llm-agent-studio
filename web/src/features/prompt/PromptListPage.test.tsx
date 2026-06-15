import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { PromptListPage } from "./PromptListPage"
import * as api from "./api"

vi.mock("./api", () => ({
  usePrompts: vi.fn(),
  usePromptStyles: vi.fn(),
  useCreatePrompt: vi.fn(),
  useUpdatePrompt: vi.fn(),
  useDeletePrompt: vi.fn(),
}))

afterEach(() => {
  vi.restoreAllMocks()
})

const PROMPTS = [
  {
    id: "p1",
    orgId: "org1",
    name: "Cute Cat",
    content: "a very cute cat",
    style: "日漫",
    createdAt: "2026-06-14T00:00:00Z",
    updatedAt: "2026-06-14T00:00:00Z",
  },
]

const STYLES = [
  { name: "日漫", suffix: "anime style --style anime" },
  { name: "吉卜力", suffix: "ghibli style --style ghibli" },
]

describe("PromptListPage", () => {
  it("renders prompts and handles copy", async () => {
    vi.mocked(api.usePrompts).mockReturnValue({
      data: PROMPTS,
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof api.usePrompts>)
    vi.mocked(api.usePromptStyles).mockReturnValue({
      data: STYLES,
      isLoading: false,
    } as unknown as ReturnType<typeof api.usePromptStyles>)
    vi.mocked(api.useCreatePrompt).mockReturnValue({} as unknown as ReturnType<typeof api.useCreatePrompt>)
    vi.mocked(api.useUpdatePrompt).mockReturnValue({} as unknown as ReturnType<typeof api.useUpdatePrompt>)
    vi.mocked(api.useDeletePrompt).mockReturnValue({} as unknown as ReturnType<typeof api.useDeletePrompt>)

    render(<PromptListPage org="org1" />)

    expect(screen.getByText("Cute Cat")).toBeInTheDocument()
    expect(screen.getByText("a very cute cat")).toBeInTheDocument()
    expect(screen.getByText("日漫")).toBeInTheDocument()
    // It should render built preview
    expect(screen.getByText("a very cute cat, anime style --style anime")).toBeInTheDocument()
  })

  it("handles creating a new prompt", async () => {
    const mutateAsync = vi.fn().mockResolvedValue({})
    vi.mocked(api.usePrompts).mockReturnValue({
      data: [],
      isLoading: false,
    } as unknown as ReturnType<typeof api.usePrompts>)
    vi.mocked(api.usePromptStyles).mockReturnValue({
      data: STYLES,
      isLoading: false,
    } as unknown as ReturnType<typeof api.usePromptStyles>)
    vi.mocked(api.useCreatePrompt).mockReturnValue({
      mutateAsync,
    } as unknown as ReturnType<typeof api.useCreatePrompt>)

    const user = userEvent.setup()
    render(<PromptListPage org="org1" />)

    await user.click(screen.getByRole("button", { name: /添加第一个提示词/ }))

    // Fill form
    await user.type(screen.getByLabelText("名称"), "New Prompt")
    await user.type(screen.getByLabelText("基础 Prompt"), "draw a bird")
    await user.click(screen.getByRole("button", { name: "吉卜力" }))

    // Preview should update
    expect(screen.getByText("draw a bird, ghibli style --style ghibli")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith({
        name: "New Prompt",
        content: "draw a bird",
        style: "吉卜力",
      })
    })
  })
})
