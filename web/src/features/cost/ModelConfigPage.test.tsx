import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { CatalogEntry, ModelConfig } from "@/lib/types"
import { ModelConfigView } from "./ModelConfigPage"

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

function renderView(overrides: Partial<Parameters<typeof ModelConfigView>[0]> = {}) {
  const defaults = {
    configs: [],
    catalog: CATALOG,
    isLoading: false,
    isError: false,
    onRetry: vi.fn(),
    onCreate: vi.fn().mockResolvedValue(CREATED),
    onUpdate: vi.fn().mockResolvedValue(EXISTING),
    onDelete: vi.fn().mockResolvedValue(undefined),
  }
  return render(<ModelConfigView {...defaults} {...overrides} />)
}

// ── 创建对话框 (via ModelConfigView) ──────────────────────────────────────────

describe("ModelConfigView — create dialog form binding", () => {
  it("submits provider/model/baseUrl/apiKey and omits empty optionals", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    renderView({ onCreate })

    // Open create dialog
    await user.click(screen.getByRole("button", { name: "添加模型" }))

    // Fill form
    await user.type(screen.getByLabelText("模型 (model)"), "gpt-4o-mini")
    await user.type(screen.getByLabelText("Base URL（可选）"), "https://api.openai.com/v1")
    await user.type(screen.getByLabelText("密钥输入"), "sk-secret")

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    const arg = onCreate.mock.calls[0][0] as Record<string, unknown>
    expect(arg).toEqual(
      expect.objectContaining({
        model: "gpt-4o-mini",
        baseUrl: "https://api.openai.com/v1",
        apiKey: "sk-secret",
        enabled: true,
        isDefault: false,
      }),
    )
    // 空 params 省略，不发 ""。
    expect(arg.params).toBeUndefined()
  })

  it("omits baseUrl/apiKey when left empty (non-compatible provider)", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    renderView({ onCreate })

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    await user.type(screen.getByLabelText("模型 (model)"), "gpt-image-1")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    const arg = onCreate.mock.calls[0][0] as Record<string, unknown>
    expect(arg.baseUrl).toBeUndefined()
    expect(arg.apiKey).toBeUndefined()
  })

  it("requires base_url + model for openai-compatible provider", async () => {
    const onCreate = vi.fn()
    const user = userEvent.setup()
    renderView({ onCreate })

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    await user.selectOptions(screen.getByLabelText("Provider"), "openai-compatible")
    // 不填 model / base_url 直接提交 → zod 拦下。
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(screen.getByText(/请填写 Base URL/)).toBeInTheDocument())
    expect(onCreate).not.toHaveBeenCalled()
  })

  it("submits openai-compatible once base_url + model are provided", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    renderView({ onCreate })

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    await user.selectOptions(screen.getByLabelText("Provider"), "openai-compatible")
    await user.type(screen.getByLabelText("模型 (model)"), "deepseek-chat")
    await user.type(screen.getByLabelText(/Base URL/), "https://api.deepseek.com/v1")
    await user.type(screen.getByLabelText("密钥输入"), "sk-ds")

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
    renderView({ onCreate })

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    await user.selectOptions(screen.getByLabelText("类型"), "text")
    await user.type(screen.getByLabelText("模型 (model)"), "gpt-4o-mini")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ kind: "text" }))
  })

  it("shows a hint for available:false suggestion without blocking submit", async () => {
    const onCreate = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    renderView({ onCreate })

    await user.click(screen.getByRole("button", { name: "添加模型" }))
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

  it("presents the API key field as write-only (password + helper text)", async () => {
    const user = userEvent.setup()
    renderView()

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    const key = screen.getByLabelText("密钥输入")
    expect(key).toHaveAttribute("type", "password")
    // 字段级 helper（区别于页头/对话框副标题里相近的文案）。
    expect(screen.getByText(/密钥仅写入、加密存储，不会回显/)).toBeInTheDocument()
  })
})

// ── 编辑对话框 (via ModelConfigView) ─────────────────────────────────────────

describe("ModelConfigView — edit dialog form binding", () => {
  it("prefills fields, omits the key by default, and submits blank apiKey when untouched", async () => {
    const onUpdate = vi.fn().mockResolvedValue(EXISTING)
    const user = userEvent.setup()
    renderView({ configs: [EXISTING], onUpdate })

    // Open edit dialog for the existing config
    await user.click(screen.getByRole("button", { name: /编辑 deepseek/ }))

    // 预填：model / base_url。
    expect(screen.getByLabelText("模型 (model)")).toHaveValue("deepseek-chat")
    expect(screen.getByLabelText(/Base URL/)).toHaveValue("https://api.deepseek.com/v1")
    // 密钥字段留空，并显示字段级「留空保持不变（已配置密钥）」提示
    // （区别于对话框副标题里相近的文案）。
    expect(screen.getByLabelText("密钥输入")).toHaveValue("")
    expect(screen.getByText(/留空保持不变（已配置密钥）/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1))
    const [id, input] = onUpdate.mock.calls[0] as [string, Record<string, unknown>]
    expect(id).toBe("mc-edit")
    // 未改密钥 → apiKey 省略（undefined），后端据此保留既有密钥。
    expect(input.apiKey).toBeUndefined()
    expect(input).toEqual(
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
    renderView({ configs: [EXISTING], onRevealKey })

    await user.click(screen.getByRole("button", { name: /编辑 deepseek/ }))

    const key = screen.getByLabelText("密钥输入")
    expect(key).toHaveValue("")
    expect(key).toHaveAttribute("type", "password")

    await user.click(screen.getByRole("button", { name: /显示已存/ }))

    await waitFor(() => expect(onRevealKey).toHaveBeenCalledWith("mc-edit"))
    await waitFor(() => expect(key).toHaveValue("sk-revealed-secret"))
    // 回显后切为明文显示。
    expect(key).toHaveAttribute("type", "text")
  })

  it("hides 查看密钥 when the config has no stored key or no onRevealKey", async () => {
    const user = userEvent.setup()

    // 无 onRevealKey → 不显示。
    const { unmount } = renderView({ configs: [EXISTING] })
    await user.click(screen.getByRole("button", { name: /编辑 deepseek/ }))
    expect(
      screen.queryByRole("button", { name: /显示已存/ }),
    ).not.toBeInTheDocument()
    unmount()

    // hasApiKey:false → 即便传了 onRevealKey 也不显示。
    const noKeyConfig = { ...EXISTING, hasApiKey: false }
    renderView({ configs: [noKeyConfig], onRevealKey: vi.fn() })
    await user.click(screen.getByRole("button", { name: /编辑 deepseek/ }))
    expect(
      screen.queryByRole("button", { name: /显示已存/ }),
    ).not.toBeInTheDocument()
  })

  it("submits the new key when the API key field is filled", async () => {
    const onUpdate = vi.fn().mockResolvedValue(EXISTING)
    const user = userEvent.setup()
    renderView({ configs: [EXISTING], onUpdate })

    await user.click(screen.getByRole("button", { name: /编辑 deepseek/ }))
    await user.type(screen.getByLabelText("密钥输入"), "sk-replacement")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1))
    const [, input] = onUpdate.mock.calls[0] as [string, Record<string, unknown>]
    expect(input.apiKey).toBe("sk-replacement")
  })
})

// ── 删除确认 ─────────────────────────────────────────────────────────────────

describe("ModelConfigView delete", () => {
  function renderViewWithDelete(onDelete: () => Promise<void>) {
    return renderView({ configs: [EXISTING], onDelete })
  }

  it("deletes only after confirming, not on cancel", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderViewWithDelete(onDelete)

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

// ── 拉取官方模型 ──────────────────────────────────────────────────────────────

describe("ModelConfigView — 拉取官方模型", () => {
  it("拉取模型 fetches live models, shows chips, fills input on click", async () => {
    const onListModels = vi
      .fn()
      .mockResolvedValue({ models: ["gpt-4o", "o3-mini"], source: "live" })
    const user = userEvent.setup()
    renderView({ onListModels })

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    await user.click(screen.getByRole("button", { name: /拉取模型/ }))
    await waitFor(() => expect(onListModels).toHaveBeenCalledTimes(1))
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
    renderView({ onListModels })

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    await user.click(screen.getByRole("button", { name: /拉取模型/ }))
    expect(await screen.findByText(/已回退建议列表/)).toBeInTheDocument()
  })

  it("hides the 拉取模型 button when onListModels is absent", async () => {
    const user = userEvent.setup()
    renderView()

    await user.click(screen.getByRole("button", { name: "添加模型" }))
    expect(
      screen.queryByRole("button", { name: /拉取模型/ }),
    ).not.toBeInTheDocument()
  })
})
