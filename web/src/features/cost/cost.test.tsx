import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
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
import { ModelConfigView } from "./ModelConfigPage"
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

// ── ModelConfigView 列表（baseUrl + 密钥指示徽章）────────────────────
// 注：创建表单（BYO key）的测试见 ModelConfigPage.test.tsx。
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
    baseUrl: "",
    hasApiKey: false,
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
        onUpdate={vi.fn()}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByText("图像")).toBeInTheDocument()
    expect(screen.getByText("openai · gpt-image-1")).toBeInTheDocument()
    expect(screen.getByText("默认")).toBeInTheDocument()
    expect(screen.getByText("已启用")).toBeInTheDocument()
  })

  it("shows the env-fallback indicator when hasApiKey is false", () => {
    render(
      <ModelConfigView
        configs={CONFIGS}
        catalog={CATALOG}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={vi.fn()}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByText("用服务端密钥")).toBeInTheDocument()
    expect(screen.queryByText("已配置密钥")).not.toBeInTheDocument()
  })

  it("shows the configured-key badge and baseUrl when hasApiKey is true", () => {
    const withKey: ModelConfig[] = [
      {
        id: "mc2",
        orgId: "acme",
        kind: "text",
        provider: "openai-compatible",
        model: "deepseek-chat",
        enabled: true,
        isDefault: false,
        baseUrl: "https://api.deepseek.com/v1",
        hasApiKey: true,
      },
    ]
    render(
      <ModelConfigView
        configs={withKey}
        catalog={CATALOG}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={vi.fn()}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByText("已配置密钥")).toBeInTheDocument()
    expect(screen.getByText("https://api.deepseek.com/v1")).toBeInTheDocument()
  })

  it("states keys are write-only and shows empty state", () => {
    render(
      <ModelConfigView
        configs={[]}
        catalog={CATALOG}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        onCreate={vi.fn()}
        onUpdate={vi.fn()}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByText("尚未配置模型")).toBeInTheDocument()
    expect(screen.getByText(/仅写入、加密存储/)).toBeInTheDocument()
  })
})
