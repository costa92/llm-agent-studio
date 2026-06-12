import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ApiError } from "@/lib/apiClient"
import type {
  Aggregate,
  CatalogEntry,
  LedgerEntry,
  ModelConfig,
  ProjectAggregate,
} from "@/lib/types"
import {
  RANGE_PRESETS,
  costRatio,
  formatCount,
  formatCurrency,
  rangeToParams,
} from "./format"
import { modelConfigErrorMessage } from "./configError"
import { CostCenterView } from "./CostCenterPage"
import { CreateModelConfigForm, ModelConfigView } from "./ModelConfigPage"
import { AdminGate } from "./AdminGate"

afterEach(() => {
  vi.restoreAllMocks()
})

// ── 货币 / 计数格式化（costMicros 换算）────────────────────────────────
describe("formatCurrency", () => {
  it("converts costMicros (×1e6) to currency with 2 decimals", () => {
    // 12_500_000 micros = ¥12.50
    expect(formatCurrency(12_500_000)).toBe("¥12.50")
    expect(formatCurrency(0)).toBe("¥0.00")
    expect(formatCurrency(1_000_000)).toBe("¥1.00")
  })

  it("adds thousands separators for large amounts", () => {
    // 1_234_560_000 micros = ¥1,234.56
    expect(formatCurrency(1_234_560_000)).toBe("¥1,234.56")
  })

  it("honors a custom symbol", () => {
    expect(formatCurrency(5_000_000, "$")).toBe("$5.00")
  })
})

describe("formatCount", () => {
  it("adds thousands separators", () => {
    expect(formatCount(1234567)).toBe("1,234,567")
    expect(formatCount(0)).toBe("0")
  })
})

describe("costRatio", () => {
  it("returns share of the max (0..1), 0 when max is 0", () => {
    expect(costRatio(50, 100)).toBe(0.5)
    expect(costRatio(100, 100)).toBe(1)
    expect(costRatio(10, 0)).toBe(0)
  })
})

// ── 时间范围预设 → RFC3339 from/to ────────────────────────────────────
describe("rangeToParams", () => {
  const now = new Date("2026-06-11T00:00:00.000Z")

  it("returns no params for the 'all time' preset (days=null)", () => {
    const all = RANGE_PRESETS.find((p) => p.value === "all")!
    expect(rangeToParams(all, now)).toEqual({})
  })

  it("computes from = now - days, to = now (RFC3339)", () => {
    const d30 = RANGE_PRESETS.find((p) => p.value === "30d")!
    const r = rangeToParams(d30, now)
    expect(r.to).toBe("2026-06-11T00:00:00.000Z")
    expect(r.from).toBe("2026-05-12T00:00:00.000Z")
  })
})

// ── 创建模型配置错误映射（含密钥型 param → 400 ErrSecretParam）────────
describe("modelConfigErrorMessage", () => {
  it("maps 400 with a credentials body to the secret-param rejection message", () => {
    const err = new ApiError(
      400,
      "models: params must not contain credentials (API keys live in server env only) (field \"apikey\")\n",
    )
    expect(modelConfigErrorMessage(err)).toContain("密钥")
  })

  it("maps other 400s to the provider/model-required message", () => {
    const err = new ApiError(400, "bad request: provider+model required\n")
    expect(modelConfigErrorMessage(err)).toContain("provider")
  })

  it("falls back to a generic message for non-400 errors", () => {
    expect(modelConfigErrorMessage(new ApiError(500, "boom"))).toBe(
      "保存失败，请重试",
    )
    expect(modelConfigErrorMessage(new Error("x"))).toBe("保存失败，请重试")
  })
})

// ── AdminGate（角色门禁）──────────────────────────────────────────────
describe("AdminGate", () => {
  it("renders children for admin", () => {
    render(
      <AdminGate role={{ isAdmin: true, isLoading: false }}>
        <p>secret content</p>
      </AdminGate>,
    )
    expect(screen.getByText("secret content")).toBeInTheDocument()
  })

  it("blocks non-admin with a permission message", () => {
    render(
      <AdminGate role={{ isAdmin: false, isLoading: false }}>
        <p>secret content</p>
      </AdminGate>,
    )
    expect(screen.queryByText("secret content")).not.toBeInTheDocument()
    expect(screen.getByText("需要管理员权限")).toBeInTheDocument()
  })

  it("shows a placeholder while the role probe is loading", () => {
    render(
      <AdminGate role={{ isAdmin: false, isLoading: true }}>
        <p>secret content</p>
      </AdminGate>,
    )
    expect(screen.queryByText("secret content")).not.toBeInTheDocument()
    expect(screen.getByText("权限校验中…")).toBeInTheDocument()
  })
})

// ── CostCenterView render/smoke + 货币展示 ────────────────────────────
const AGG: Aggregate = {
  generations: 42,
  tokens: 123456,
  imageCount: 30,
  costMicros: 12_500_000,
}
const PROJECTS: ProjectAggregate[] = [
  {
    projectId: "p1",
    projectName: "夏日广告片",
    generations: 30,
    tokens: 100000,
    imageCount: 20,
    costMicros: 10_000_000,
  },
  {
    projectId: "p2",
    projectName: "冬日宣传片",
    generations: 12,
    tokens: 23456,
    imageCount: 10,
    costMicros: 2_500_000,
  },
]
const LEDGER: LedgerEntry[] = [
  {
    id: "g1",
    projectId: "p1",
    projectName: "夏日广告片",
    kind: "image",
    provider: "openai",
    model: "gpt-image-1",
    tokens: 0,
    imageCount: 1,
    costMicros: 400_000,
    latencyMs: 1200,
    createdAt: "2026-06-10T08:30:00.000Z",
  },
]

function costViewProps() {
  return {
    aggregate: AGG,
    projects: PROJECTS,
    generations: LEDGER,
    isLoading: false,
    isError: false,
    onRetry: vi.fn(),
    rangeValue: "30d",
    onRangeChange: vi.fn(),
  }
}

describe("CostCenterView", () => {
  it("renders the three stat cards with the cost formatted as currency", () => {
    render(<CostCenterView {...costViewProps()} />)
    expect(screen.getByText("本月成本")).toBeInTheDocument()
    expect(screen.getByText("¥12.50")).toBeInTheDocument()
    expect(screen.getByText("42")).toBeInTheDocument()
    expect(screen.getByText("123,456")).toBeInTheDocument()
  })

  it("renders the per-project rollup and the generations ledger", () => {
    render(<CostCenterView {...costViewProps()} />)
    expect(screen.getByText("按项目成本")).toBeInTheDocument()
    // 项目名在按项目条 + 台账行各出现一次。
    expect(screen.getAllByText("夏日广告片").length).toBeGreaterThan(0)
    expect(screen.getByText("生成明细")).toBeInTheDocument()
    expect(screen.getByText("openai · gpt-image-1")).toBeInTheDocument()
    // 项目条金额 + 台账金额都按货币展示。
    expect(screen.getByText("¥10.00")).toBeInTheDocument()
    expect(screen.getByText("¥0.40")).toBeInTheDocument()
  })

  it("calls onRangeChange when a preset is selected", async () => {
    const onRangeChange = vi.fn()
    const user = userEvent.setup()
    render(<CostCenterView {...costViewProps()} onRangeChange={onRangeChange} />)
    await user.click(screen.getByRole("button", { name: /近 30 天/ }))
    await user.click(await screen.findByText("近 7 天"))
    expect(onRangeChange).toHaveBeenCalledWith("7d")
  })

  it("renders an empty state when there is no usage", () => {
    render(
      <CostCenterView
        {...costViewProps()}
        aggregate={{ generations: 0, tokens: 0, imageCount: 0, costMicros: 0 }}
        projects={[]}
        generations={[]}
      />,
    )
    expect(screen.getByText("暂无成本数据")).toBeInTheDocument()
  })

  it("renders an error state with a retry button", async () => {
    const onRetry = vi.fn()
    const user = userEvent.setup()
    render(
      <CostCenterView
        {...costViewProps()}
        aggregate={undefined}
        projects={undefined}
        generations={undefined}
        isError
        onRetry={onRetry}
      />,
    )
    expect(screen.getByText("成本数据加载失败")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })
})

// ── ModelConfigView + 创建表单（400 密钥拒绝处理）─────────────────────
const CATALOG: CatalogEntry[] = [
  { provider: "openai", model: "gpt-image-1", kind: "image", label: "OpenAI GPT-Image-1", available: true },
  { provider: "runway", model: "gen-3", kind: "video", label: "Runway Gen-3", available: true },
]
const CONFIGS: ModelConfig[] = [
  {
    id: "mc1",
    orgId: "acme",
    kind: "image",
    provider: "openai",
    model: "gpt-image-1",
    enabled: true,
    isDefault: true,
  },
]

describe("ModelConfigView", () => {
  it("renders grouped configs with enabled/default badges", () => {
    render(
      <ModelConfigView
        configs={CONFIGS}
        catalog={CATALOG}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        onCreate={vi.fn()}
      />,
    )
    expect(screen.getByText("图像")).toBeInTheDocument()
    expect(screen.getByText("openai · gpt-image-1")).toBeInTheDocument()
    expect(screen.getByText("默认")).toBeInTheDocument()
    expect(screen.getByText("已启用")).toBeInTheDocument()
  })

  it("states keys are server-managed (no API key field) and shows empty state", () => {
    render(
      <ModelConfigView
        configs={[]}
        catalog={CATALOG}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        onCreate={vi.fn()}
      />,
    )
    expect(screen.getByText("尚未配置模型")).toBeInTheDocument()
    expect(
      screen.getByText(/密钥由服务端环境变量统一管理/),
    ).toBeInTheDocument()
  })
})

describe("CreateModelConfigForm", () => {
  it("submits the chosen catalog entry's provider/model/kind (no API key field)", async () => {
    const created: ModelConfig = { ...CONFIGS[0], id: "new1" }
    const onCreate = vi.fn().mockResolvedValue(created)
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        onCreate={onCreate}
        onSuccess={onSuccess}
      />,
    )
    // 表单不含任何 API key 输入。
    expect(screen.queryByLabelText(/api key/i)).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: "openai",
        model: "gpt-image-1",
        kind: "image",
        enabled: true,
        isDefault: false,
      }),
    )
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(created))
  })

  it("surfaces a 400 secret-param rejection without calling onSuccess", async () => {
    const onCreate = vi
      .fn()
      .mockRejectedValue(
        new ApiError(
          400,
          "models: params must not contain credentials (API keys live in server env only)",
        ),
      )
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    render(
      <CreateModelConfigForm
        catalog={CATALOG}
        onCreate={onCreate}
        onSuccess={onSuccess}
      />,
    )
    // userEvent 把 {/[ 当键描述符——JSON 的花括号需转义为 {{。
    await user.type(screen.getByLabelText(/参数/), '{{"apikey":"sk-leak"}')
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onSuccess).not.toHaveBeenCalled()
    // 表单内兜底错误文案出现（route 层另有 toast 走 modelConfigErrorMessage）。
    expect(await screen.findByRole("alert")).toBeInTheDocument()
  })

  it("rejects malformed params JSON locally before hitting the backend", async () => {
    const onCreate = vi.fn()
    const user = userEvent.setup()

    render(<CreateModelConfigForm catalog={CATALOG} onCreate={onCreate} />)
    await user.type(screen.getByLabelText(/参数/), "not json")
    await user.click(screen.getByRole("button", { name: "保存" }))

    expect(await screen.findByText("参数不是合法 JSON")).toBeInTheDocument()
    expect(onCreate).not.toHaveBeenCalled()
  })
})

// ── 模型可用性（provider 未配置密钥 → available:false 不可选）─────────
// 第一条不可用、第二条可用：默认选中应跳过不可用条，落到首个可用条。
const MIXED_CATALOG: CatalogEntry[] = [
  { provider: "openai", model: "gpt-image-1", kind: "image", label: "OpenAI GPT-Image-1", available: false },
  { provider: "runway", model: "gen-3", kind: "video", label: "Runway Gen-3", available: true },
]

describe("CreateModelConfigForm 可用性", () => {
  it("disables unavailable options and marks them with （未配置密钥）", () => {
    render(<CreateModelConfigForm catalog={MIXED_CATALOG} onCreate={vi.fn()} />)
    const unavailable = screen.getByRole("option", { name: /未配置密钥/ })
    expect(unavailable).toBeDisabled()
    expect(unavailable).toHaveTextContent(/OpenAI GPT-Image-1/)
    // 可用条不带标记、可选。
    const available = screen.getByRole("option", { name: /Runway Gen-3/ })
    expect(available).not.toBeDisabled()
    expect(available).not.toHaveTextContent(/未配置密钥/)
  })

  it("defaults the selection to the first AVAILABLE entry and submits it", async () => {
    const created: ModelConfig = { ...CONFIGS[0], id: "new2" }
    const onCreate = vi.fn().mockResolvedValue(created)
    const user = userEvent.setup()

    render(<CreateModelConfigForm catalog={MIXED_CATALOG} onCreate={onCreate} />)
    // 默认值 = 首个可用条（runway/gen-3），不是 catalog[0]（不可用的 openai）。
    expect(screen.getByLabelText("模型")).toHaveValue("runway/gen-3")

    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1))
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ provider: "runway", model: "gen-3", kind: "video" }),
    )
  })

  it("blocks submit with an inline error when the selected model is unavailable, without calling onCreate", async () => {
    const onCreate = vi.fn()
    const user = userEvent.setup()

    // 全部不可用 → 默认兜底到 catalog[0]（不可用），提交应被拦下。
    const allUnavailable: CatalogEntry[] = [
      { provider: "openai", model: "gpt-image-1", kind: "image", label: "OpenAI GPT-Image-1", available: false },
    ]
    render(<CreateModelConfigForm catalog={allUnavailable} onCreate={onCreate} />)
    // 无可用模型的内联提示。
    expect(screen.getByText(/当前没有可用模型/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "保存" }))

    expect(await screen.findByRole("alert")).toHaveTextContent(/未配置/)
    expect(onCreate).not.toHaveBeenCalled()
  })
})
