import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { CatalogEntry, ModelConfig } from "@/lib/types"
import {
  CreateModelConfigForm,
  EditModelConfigDialog,
  ModelConfigView,
} from "./ModelConfigPage"

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

// 编辑模式：CreateModelConfigForm 预填既有配置，apiKey 默认留空（空 = 后端保留密钥）。
const EXISTING: ModelConfig = {
  id: "mc-edit",
  orgId: "acme",
  kind: "text",
  provider: "deepseek",
  model: "deepseek-chat",
  enabled: true,
  isDefault: true,
  baseUrl: "https://api.deepseek.com/v1",
  hasApiKey: true,
}

describe("CreateModelConfigForm (edit mode)", () => {
  it("prefills fields, omits the key by default, and submits a blank apiKey when untouched", async () => {
    const onCreate = vi.fn().mockResolvedValue(EXISTING)
    const user = userEvent.setup()

    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        initial={EXISTING}
        onCreate={onCreate}
      />,
    )

    // 预填：model / base_url。
    expect(screen.getByLabelText("模型 (model)")).toHaveValue("deepseek-chat")
    expect(screen.getByLabelText(/Base URL/)).toHaveValue("https://api.deepseek.com/v1")
    // 密钥字段留空，并显示「留空保持不变」提示。
    expect(screen.getByLabelText(/API Key/)).toHaveValue("")
    expect(screen.getByText(/留空保持不变/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    const arg = onCreate.mock.calls[0][0] as Record<string, unknown>
    // 未改密钥 → apiKey 省略（undefined），后端据此保留既有密钥。
    expect(arg.apiKey).toBeUndefined()
    expect(arg).toEqual(
      expect.objectContaining({
        provider: "deepseek",
        model: "deepseek-chat",
        kind: "text",
        baseUrl: "https://api.deepseek.com/v1",
        enabled: true,
        isDefault: true,
      }),
    )
  })

  it("查看密钥 reveals the stored key into the field and switches to plaintext", async () => {
    const onRevealKey = vi.fn().mockResolvedValue("sk-revealed-secret")
    const user = userEvent.setup()

    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        initial={EXISTING}
        onCreate={vi.fn().mockResolvedValue(EXISTING)}
        onRevealKey={onRevealKey}
      />,
    )

    const key = screen.getByLabelText(/API Key/)
    expect(key).toHaveValue("")
    expect(key).toHaveAttribute("type", "password")

    await user.click(screen.getByRole("button", { name: /查看密钥/ }))

    await waitFor(() => expect(onRevealKey).toHaveBeenCalledWith("mc-edit"))
    await waitFor(() => expect(key).toHaveValue("sk-revealed-secret"))
    // 回显后切为明文显示。
    expect(key).toHaveAttribute("type", "text")
  })

  it("hides 查看密钥 when the config has no stored key or no onRevealKey", () => {
    // 无 onRevealKey → 不显示。
    const { unmount } = render(
      <CreateModelConfigForm catalog={CATALOG} initial={EXISTING} onCreate={vi.fn()} />,
    )
    expect(
      screen.queryByRole("button", { name: /查看密钥/ }),
    ).not.toBeInTheDocument()
    unmount()

    // hasApiKey:false → 即便传了 onRevealKey 也不显示。
    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        initial={{ ...EXISTING, hasApiKey: false }}
        onCreate={vi.fn()}
        onRevealKey={vi.fn()}
      />,
    )
    expect(
      screen.queryByRole("button", { name: /查看密钥/ }),
    ).not.toBeInTheDocument()
  })

  it("submits the new key when the API key field is filled", async () => {
    const onCreate = vi.fn().mockResolvedValue(EXISTING)
    const user = userEvent.setup()

    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        initial={EXISTING}
        onCreate={onCreate}
      />,
    )
    await user.type(screen.getByLabelText(/API Key/), "sk-replacement")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ apiKey: "sk-replacement" }),
    )
  })
})

describe("EditModelConfigDialog", () => {
  it("calls onUpdate with the config id and a blank-key payload", async () => {
    const onUpdate = vi.fn().mockResolvedValue(EXISTING)
    const user = userEvent.setup()

    render(
      <EditModelConfigDialog
        config={EXISTING}
        catalog={CATALOG}
        onUpdate={onUpdate}
        trigger={<button>编辑</button>}
      />,
    )
    await user.click(screen.getByRole("button", { name: "编辑" }))
    // 弹窗内表单预填，直接保存（不动密钥）。
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1))
    expect(onUpdate.mock.calls[0][0]).toBe("mc-edit")
    const input = onUpdate.mock.calls[0][1] as Record<string, unknown>
    expect(input.apiKey).toBeUndefined()
    expect(input.model).toBe("deepseek-chat")
  })
})

// 删除：ModelConfigView 每行有删除按钮 → 确认弹窗；仅「确认删除」才调 onDelete。
const VIEW_CONFIGS: ModelConfig[] = [EXISTING]
const VIEW_CATALOG: CatalogEntry[] = CATALOG

describe("ModelConfigView delete", () => {
  function renderView(onDelete: () => Promise<void>) {
    return render(
      <ModelConfigView
        configs={VIEW_CONFIGS}
        catalog={VIEW_CATALOG}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={vi.fn()}
        onDelete={onDelete}
      />,
    )
  }

  it("deletes only after confirming, not on cancel", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderView(onDelete)

    // 打开确认弹窗后点「取消」→ 不删除。
    await user.click(screen.getByRole("button", { name: /删除 deepseek/ }))
    expect(screen.getByText("确认删除该模型配置？")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(onDelete).not.toHaveBeenCalled()

    // 再次打开 → 点「确认删除」→ 才删除（按 id）。
    await user.click(screen.getByRole("button", { name: /删除 deepseek/ }))
    await user.click(screen.getByRole("button", { name: "确认删除" }))
    await waitFor(() => expect(onDelete).toHaveBeenCalledTimes(1))
    expect(onDelete).toHaveBeenCalledWith("mc-edit")
  })
})

describe("CreateModelConfigForm 拉取官方模型", () => {
  it("拉取模型 fetches live models, shows chips, fills input on click", async () => {
    const onListModels = vi
      .fn()
      .mockResolvedValue({ models: ["gpt-4o", "o3-mini"], source: "live" })
    const user = userEvent.setup()
    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        onCreate={vi.fn().mockResolvedValue(CREATED)}
        onListModels={onListModels}
      />,
    )

    await user.click(screen.getByRole("button", { name: /拉取模型/ }))
    await waitFor(() => expect(onListModels).toHaveBeenCalledTimes(1))
    // request carries the selected provider (default = first catalog provider).
    expect(onListModels.mock.calls[0][0]).toMatchObject({ provider: "openai" })

    expect(await screen.findByText(/已从官方接口拉取 2 个模型/)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "gpt-4o" }))
    expect(screen.getByLabelText("模型 (model)")).toHaveValue("gpt-4o")
  })

  it("shows a fallback note when listing falls back to catalog", async () => {
    const onListModels = vi.fn().mockResolvedValue({
      models: ["image-01"],
      source: "catalog",
      error: "provider has no live model API",
    })
    const user = userEvent.setup()
    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        onCreate={vi.fn()}
        onListModels={onListModels}
      />,
    )
    await user.click(screen.getByRole("button", { name: /拉取模型/ }))
    expect(await screen.findByText(/已回退建议列表/)).toBeInTheDocument()
  })

  it("hides the 拉取模型 button when onListModels is absent", () => {
    render(<CreateModelConfigForm catalog={CATALOG} onCreate={vi.fn()} />)
    expect(
      screen.queryByRole("button", { name: /拉取模型/ }),
    ).not.toBeInTheDocument()
  })
})
