import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ApiError } from "@/lib/apiClient"
import type {
  Aggregate,
  CatalogEntry,
  LedgerEntry,
  MemberAggregate,
  ModelConfig,
  ProjectAggregate,
} from "@/lib/types"
import {
  RANGE_PRESETS,
  costRatio,
  filterLedgerByRange,
  formatCount,
  formatCurrency,
  isUnpriced,
  ledgerToCSV,
  rangeToParams,
} from "./format"
import { modelConfigErrorMessage } from "./configError"
import { CostCenterView } from "./CostCenterPage"
import { ModelConfigView } from "./ModelConfigPage"
import { AdminGate } from "./AdminGate"

afterEach(() => {
  vi.restoreAllMocks()
})

// 台账基准行：导出 / 未定价用例按需覆盖字段。
const LEDGER_ROW: LedgerEntry = {
  id: "base",
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
}

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

// ── 未定价判定（有用量却计费 ¥0）────────────────────────────────────
describe("isUnpriced", () => {
  it("flags usage>0 with costMicros==0", () => {
    expect(isUnpriced({ costMicros: 0, tokens: 5000 })).toBe(true)
    expect(isUnpriced({ costMicros: 0, tokens: 0, imageCount: 1 })).toBe(true)
    expect(isUnpriced({ costMicros: 0, tokens: 0, generations: 2 })).toBe(true)
  })

  it("does not flag when priced or when there is no usage", () => {
    expect(isUnpriced({ costMicros: 400_000, tokens: 5000 })).toBe(false)
    expect(isUnpriced({ costMicros: 0, tokens: 0 })).toBe(false)
    expect(isUnpriced({ costMicros: 0, tokens: 0, imageCount: 0, generations: 0 })).toBe(false)
  })
})

// ── 导出：按范围裁剪 + CSV 构建 ──────────────────────────────────────
describe("filterLedgerByRange", () => {
  const rows: LedgerEntry[] = [
    { ...LEDGER_ROW, id: "a", createdAt: "2026-06-01T00:00:00.000Z" },
    { ...LEDGER_ROW, id: "b", createdAt: "2026-06-10T00:00:00.000Z" },
    { ...LEDGER_ROW, id: "c", createdAt: "2026-06-20T00:00:00.000Z" },
  ]

  it("returns all rows when the range is open (all time)", () => {
    expect(filterLedgerByRange(rows, {})).toHaveLength(3)
  })

  it("keeps only rows within [from, to)", () => {
    const kept = filterLedgerByRange(rows, {
      from: "2026-06-05T00:00:00.000Z",
      to: "2026-06-15T00:00:00.000Z",
    })
    expect(kept.map((r) => r.id)).toEqual(["b"])
  })
})

describe("ledgerToCSV", () => {
  it("emits a header-only CSV for an empty ledger", () => {
    expect(ledgerToCSV([])).toBe("时间,项目,Provider·Model,类型,用量,金额(¥)")
  })

  it("formats a priced image row and escapes commas/quotes", () => {
    const csv = ledgerToCSV([
      { ...LEDGER_ROW, id: "a", projectName: "夏日, 广告", model: 'gpt "image"', imageCount: 2, tokens: 0, costMicros: 400_000 },
    ])
    const line = csv.split("\n")[1]
    // 含逗号/引号的单元格被整体包引号，内部引号翻倍；金额为货币单位数（无千分位）。
    expect(line).toContain('"夏日, 广告"')
    expect(line).toContain('"openai · gpt ""image"""')
    expect(line).toContain("2 图")
    expect(line).toContain("0.40")
  })

  it("marks unpriced rows as 未定价 and uses token usage for text rows", () => {
    const csv = ledgerToCSV([
      { ...LEDGER_ROW, id: "a", kind: "chat", tokens: 5000, imageCount: 0, costMicros: 0 },
    ])
    const line = csv.split("\n")[1]
    expect(line).toContain("5000 tok")
    expect(line.endsWith("未定价")).toBe(true)
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
const MEMBERS: MemberAggregate[] = [
  {
    userId: "u1",
    email: "alice@studio.com",
    generations: 25,
    tokens: 80000,
    imageCount: 15,
    costMicros: 9_000_000,
    unpriced: false,
  },
  {
    userId: "",
    email: "",
    generations: 17,
    tokens: 43456,
    imageCount: 15,
    costMicros: 3_500_000,
    unpriced: false,
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
    members: MEMBERS,
    generations: LEDGER,
    hasNextPage: false,
    isFetchingNextPage: false,
    onLoadMore: vi.fn(),
    isLoading: false,
    isError: false,
    onRetry: vi.fn(),
    rangeValue: "30d",
    onRangeChange: vi.fn(),
    onExport: vi.fn(),
    isExporting: false,
  }
}

describe("CostCenterView", () => {
  it("renders the three stat cards, labelling the cost by the active range", () => {
    render(<CostCenterView {...costViewProps()} />)
    // 抬头成本卡按活动预设标签命名（不再写死「本月成本」）。
    expect(screen.getByText("近 30 天成本")).toBeInTheDocument()
    expect(screen.queryByText("本月成本")).not.toBeInTheDocument()
    expect(screen.getByText("¥12.50")).toBeInTheDocument()
    expect(screen.getByText("42")).toBeInTheDocument()
    expect(screen.getByText("123,456")).toBeInTheDocument()
  })

  it("labels the cost card from a different active range", () => {
    render(<CostCenterView {...costViewProps()} rangeValue="7d" />)
    expect(screen.getByText("近 7 天成本")).toBeInTheDocument()
  })

  it("fires onExport when 导出 CSV is clicked", async () => {
    const onExport = vi.fn()
    const user = userEvent.setup()
    render(<CostCenterView {...costViewProps()} onExport={onExport} />)
    await user.click(screen.getByRole("button", { name: "导出 CSV" }))
    expect(onExport).toHaveBeenCalledTimes(1)
  })

  it("disables 导出 CSV while exporting", () => {
    render(<CostCenterView {...costViewProps()} isExporting />)
    expect(screen.getByRole("button", { name: "导出中…" })).toBeDisabled()
  })

  it("flags 未定价 when a row/project has usage but zero cost, and notes the exclusion", () => {
    // deepseek 文本生成：tokens>0 但 costMicros=0（无 pricing 行）→ 应标「未定价」。
    const unpricedLedger: LedgerEntry = {
      id: "g2",
      projectId: "p3",
      projectName: "文本项目",
      kind: "chat",
      provider: "deepseek",
      model: "deepseek-chat",
      tokens: 5000,
      imageCount: 0,
      costMicros: 0,
      latencyMs: 800,
      createdAt: "2026-06-10T09:00:00.000Z",
    }
    const unpricedProject: ProjectAggregate = {
      projectId: "p3",
      projectName: "文本项目",
      generations: 3,
      tokens: 5000,
      imageCount: 0,
      costMicros: 0,
    }
    render(
      <CostCenterView
        {...costViewProps()}
        projects={[...PROJECTS, unpricedProject]}
        generations={[...LEDGER, unpricedLedger]}
      />,
    )
    // 台账行 + 项目条各一个未定价徽章 + 页面提示（含「未定价」字样）。
    expect(screen.getAllByText("未定价").length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText(/未计入上方 ¥ 成本合计/)).toBeInTheDocument()
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

  it("renders the per-member rollup, showing email and 未归属 for empty actor", () => {
    render(<CostCenterView {...costViewProps()} />)
    expect(screen.getByText("按成员成本")).toBeInTheDocument()
    // 有 email 的成员按 email 显示；空 actor 归「未归属（历史）」。
    expect(screen.getByText("alice@studio.com")).toBeInTheDocument()
    expect(screen.getByText("未归属（历史）")).toBeInTheDocument()
    expect(screen.getByText("¥9.00")).toBeInTheDocument()
    expect(screen.getByText("¥3.50")).toBeInTheDocument()
  })

  it("flags 未定价 on a member whose usage priced to ¥0", () => {
    const unpricedMember: MemberAggregate = {
      userId: "u3",
      email: "bob@studio.com",
      generations: 4,
      tokens: 6000,
      imageCount: 0,
      costMicros: 0,
      unpriced: true,
    }
    render(
      <CostCenterView {...costViewProps()} members={[...MEMBERS, unpricedMember]} />,
    )
    expect(screen.getByText("bob@studio.com")).toBeInTheDocument()
    expect(screen.getAllByText("未定价").length).toBeGreaterThanOrEqual(1)
  })

  it("calls onRangeChange when a preset is selected", async () => {
    const onRangeChange = vi.fn()
    const user = userEvent.setup()
    render(<CostCenterView {...costViewProps()} onRangeChange={onRangeChange} />)
    await user.click(screen.getByRole("button", { name: /近 30 天/ }))
    await user.click(await screen.findByText("近 7 天"))
    expect(onRangeChange).toHaveBeenCalledWith("7d")
  })

  it("shows load-more only when hasNextPage and fires onLoadMore", async () => {
    const onLoadMore = vi.fn()
    const user = userEvent.setup()
    const { rerender } = render(<CostCenterView {...costViewProps()} />)
    expect(screen.queryByRole("button", { name: "加载更多" })).not.toBeInTheDocument()
    rerender(
      <CostCenterView {...costViewProps()} hasNextPage onLoadMore={onLoadMore} />,
    )
    await user.click(screen.getByRole("button", { name: "加载更多" }))
    expect(onLoadMore).toHaveBeenCalledTimes(1)
  })

  it("disables the load-more button while fetching the next page", () => {
    render(
      <CostCenterView {...costViewProps()} hasNextPage isFetchingNextPage />,
    )
    expect(screen.getByRole("button", { name: "加载中…" })).toBeDisabled()
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
    expect(screen.getByText(/仅管理员可显式解密查看且会记入审计/)).toBeInTheDocument()
  })
})
