import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { CatalogEntry, ModelConfig } from "@/lib/types"
import { CreateModelConfigForm } from "./ModelConfigPage"

afterEach(() => {
  vi.restoreAllMocks()
})

// catalog 含 text 类目 + 一条 available:false（runway 未配置服务端密钥）。
const CATALOG: CatalogEntry[] = [
  { provider: "openai", model: "gpt-image-1", kind: "image", label: "OpenAI GPT-Image-1", available: true },
  { provider: "openai", model: "gpt-4o-mini", kind: "text", label: "OpenAI GPT-4o-mini", available: true },
  { provider: "deepseek", model: "deepseek-chat", kind: "text", label: "DeepSeek Chat", available: true },
  { provider: "runway", model: "gen-3", kind: "video", label: "Runway Gen-3", available: false },
]

const CREATED: ModelConfig = {
  id: "new1",
  orgId: "acme",
  kind: "text",
  provider: "openai-compatible",
  model: "x",
  enabled: true,
  isDefault: false,
  baseUrl: "",
  hasApiKey: false,
}

describe("CreateModelConfigForm (BYO key)", () => {
  it("submits provider/model/baseUrl/apiKey and omits empty optionals", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    render(
      <CreateModelConfigForm catalog={CATALOG} onCreate={onCreate} onSuccess={onSuccess} />,
    )

    // 自由文本 model + base_url + 密钥（password）。
    await user.type(screen.getByLabelText("模型 (model)"), "gpt-4o-mini")
    await user.type(screen.getByLabelText("Base URL（可选）"), "https://api.openai.com/v1")
    await user.type(screen.getByLabelText(/API Key/), "sk-secret")

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    const arg = onCreate.mock.calls[0][0] as Record<string, unknown>
    expect(arg).toEqual(
      expect.objectContaining({
        provider: "openai",
        model: "gpt-4o-mini",
        kind: "image",
        baseUrl: "https://api.openai.com/v1",
        apiKey: "sk-secret",
        enabled: true,
        isDefault: false,
      }),
    )
    // 空 params 省略，不发 ""。
    expect(arg.params).toBeUndefined()
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(CREATED))
  })

  it("omits baseUrl/apiKey when left empty (non-compatible provider)", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()

    render(<CreateModelConfigForm catalog={CATALOG} onCreate={onCreate} />)
    await user.type(screen.getByLabelText("模型 (model)"), "gpt-image-1")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    const arg = onCreate.mock.calls[0][0] as Record<string, unknown>
    expect(arg.baseUrl).toBeUndefined()
    expect(arg.apiKey).toBeUndefined()
  })

  it("requires base_url + model for the openai-compatible provider (no onCreate)", async () => {
    const onCreate = vi.fn()
    const user = userEvent.setup()

    render(<CreateModelConfigForm catalog={CATALOG} onCreate={onCreate} />)
    await user.selectOptions(screen.getByLabelText("Provider"), "openai-compatible")
    // 不填 model / base_url 直接提交 → zod 拦下。
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(screen.getByText(/请填写 Base URL/)).toBeInTheDocument())
    expect(onCreate).not.toHaveBeenCalled()
  })

  it("submits openai-compatible once base_url + model are provided", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()

    render(<CreateModelConfigForm catalog={CATALOG} onCreate={onCreate} />)
    await user.selectOptions(screen.getByLabelText("Provider"), "openai-compatible")
    await user.type(screen.getByLabelText("模型 (model)"), "deepseek-chat")
    await user.type(screen.getByLabelText(/Base URL/), "https://api.deepseek.com/v1")
    await user.type(screen.getByLabelText(/API Key/), "sk-ds")

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: "openai-compatible",
        model: "deepseek-chat",
        baseUrl: "https://api.deepseek.com/v1",
        apiKey: "sk-ds",
      }),
    )
  })

  it("allows the 文本 (text) kind and submits kind:text", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()

    render(<CreateModelConfigForm catalog={CATALOG} onCreate={onCreate} />)
    await user.selectOptions(screen.getByLabelText("类型"), "text")
    await user.type(screen.getByLabelText("模型 (model)"), "gpt-4o-mini")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ kind: "text" }))
  })

  it("shows an informational hint for an available:false suggestion without blocking submit", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()

    render(<CreateModelConfigForm catalog={CATALOG} onCreate={onCreate} />)
    // runway 仅在 video 类目，且 available:false。
    await user.selectOptions(screen.getByLabelText("Provider"), "runway")
    await user.selectOptions(screen.getByLabelText("类型"), "video")

    expect(screen.getByText(/未配置服务端密钥/)).toBeInTheDocument()

    await user.type(screen.getByLabelText("模型 (model)"), "gen-3")
    await user.click(screen.getByRole("button", { name: "保存" }))

    // 信息提示不阻塞提交。
    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ provider: "runway", model: "gen-3", kind: "video" }),
    )
  })

  it("presents the API key field as write-only (password + helper text)", () => {
    render(<CreateModelConfigForm catalog={CATALOG} onCreate={vi.fn()} />)
    const key = screen.getByLabelText(/API Key/)
    expect(key).toHaveAttribute("type", "password")
    expect(key).toHaveAttribute("autocomplete", "off")
    expect(screen.getByText(/仅写入、加密存储，不会回显/)).toBeInTheDocument()
  })
})
