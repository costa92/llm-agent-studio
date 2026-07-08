import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { toast } from "sonner"
import { RunCanvas, SuppressedBodyPanel, parseHttpStatus } from "./RunCanvas"
import { computeNodeVisibility } from "./topologyUtils"
import { DEFAULT_TOPOLOGY_SETTINGS } from "./useTopologySettings"
import type { WorkflowNode } from "@/lib/types"
import type { ProjectState } from "@/lib/projectState"

// Phase 3.4：运行模式只读硬化测试。RunCanvas 渲染 run 态画布，不应出现任何编辑
// 入口（NodePalette「标准管线」、保存按钮）；节点应按 overlay 注入 run 状态渲染。
// 数据 hooks 全部 mock：RunCanvas 不依赖真实网络，仅看 state.nodes 叠加 + 只读 UI。

const RUNNING_STATE: ProjectState = {
  projectId: "p1",
  version: 1,
  status: "running",
  runStatus: "running",
  stages: [],
  pips: [],
  assets: { total: 1, done: 0, pending: 1 },
  // 与下方 workflow 节点同构（一个 script、一个 storyboard，拓扑序对应）。
  nodes: [
    { id: "rn-script", label: "脚本", type: "script", status: "done" },
    { id: "rn-board", label: "分镜", type: "storyboard", status: "running" },
  ],
  edges: [{ from: "rn-board", to: "rn-script" }],
  isCustom: true,
}

// 可变 state holder：默认 RUNNING_STATE，按测试覆盖（useProjectState mock 读它）。
let currentState: ProjectState = RUNNING_STATE
// 运行入口 mutation spy（useRunWorkflow mock 返回它）：断言弹 dialog 时不直接发请求。
const runWorkflowMutateAsync = vi.fn()

vi.mock("@/features/workflow/api", () => ({
  usePlans: vi.fn(() => ({
    data: [{ id: "run1", projectId: "p1", status: "running", valid: true, fallbackUsed: false, createdAt: new Date().toISOString(), workflowId: "w1" }],
    refetch: vi.fn(),
  })),
  useProjectState: vi.fn(() => ({ data: currentState })),
  useCancel: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useScript: vi.fn(() => ({ data: null, isLoading: false, isError: false })),
  useShots: vi.fn(() => ({ data: [], isLoading: false, isError: false })),
  useProjectAssets: vi.fn(() => ({ data: [] })),
  useProject: vi.fn(() => ({ data: undefined })),
  usePlanCost: vi.fn(() => ({ data: undefined, isLoading: true, isError: false })),
  fetchAllEvents: vi.fn(async () => []),
}))

// 运行入口已收敛到工作流端点（POST /workflows/{wfId}/run）。
vi.mock("@/features/projects/workflowApi", () => ({
  useRunWorkflow: vi.fn(() => ({ mutateAsync: runWorkflowMutateAsync, isPending: false })),
}))

vi.mock("@/features/workflow/useProductionTimeline", () => ({
  useProductionTimeline: vi.fn(() => ({ log: [], conn: "connected" })),
}))

// review/api：RunCanvas 直用 useAsset；打开融合审核抽屉后 ReviewBoard 用队列 + HITL mutation。
vi.mock("@/features/review/api", () => ({
  useAsset: vi.fn(() => ({ data: undefined })),
  useReviewQueue: vi.fn(() => ({ data: [], isLoading: false, isError: false, refetch: vi.fn() })),
  useAccept: vi.fn(() => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false })),
  useReject: vi.fn(() => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false })),
  useRegenerate: vi.fn(() => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false })),
}))

// 融合审核抽屉里的 ReviewBoard 会解析项目名。
vi.mock("@/features/projects/api", () => ({
  useProjects: vi.fn(() => ({ data: [] })),
}))

// toast：断言完成上升沿召唤只弹一次。
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), dismiss: vi.fn() },
}))

vi.mock("@/app/rbac", () => ({
  useRole: vi.fn(() => ({ isAdmin: true, role: "admin", can: () => true })),
}))

beforeEach(() => {
  currentState = RUNNING_STATE
})
afterEach(() => vi.clearAllMocks())

const WF_NODES: WorkflowNode[] = [
  { id: "script-1", type: "script", promptId: "", dependsOn: [], position: { x: 0, y: 0 } },
  { id: "storyboard-1", type: "storyboard", promptId: "", dependsOn: ["script-1"], position: { x: 0, y: 120 } },
]

function renderRun() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <RunCanvas
        projectId="p1"
        org="acme"
        runId="run1"
        nodes={WF_NODES}
        onSelectRun={vi.fn()}
      />
    </QueryClientProvider>,
  )
}

// 同 renderRun，但返回 container 且可传自定义工作流节点（用于点节点 → 选中态测试）。
function renderRunTo(nodes: WorkflowNode[] = WF_NODES) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <RunCanvas projectId="p1" org="acme" runId="run1" nodes={nodes} onSelectRun={vi.fn()} />
    </QueryClientProvider>,
  )
}

describe("RunCanvas read-only hardening", () => {
  it("renders no edit affordances (NodePalette 标准管线 / 保存) in run mode", () => {
    renderRun()
    // 编辑入口不应出现：标准管线一键填充按钮（NodePalette）。
    expect(screen.queryByRole("button", { name: /标准管线/ })).toBeNull()
    expect(screen.queryByRole("button", { name: /保存/ })).toBeNull()
    // 运行模式特有控件应在（确认确实进入 run 视图而非编辑视图）。
    expect(screen.getByText("运行汇总")).toBeInTheDocument()
    expect(screen.getByText("事件日志")).toBeInTheDocument()
  })

  it("renders nodes with run status injected from project state", () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <RunCanvas projectId="p1" org="acme" runId="run1" nodes={WF_NODES} onSelectRun={vi.fn()} />
      </QueryClientProvider>,
    )
    // 运行模式节点带 data-status（overlay 注入）。两节点 → 两状态点。
    const dots = container.querySelectorAll('[data-slot="canvas-node-status"]')
    expect(dots.length).toBe(2)
    // 至少一个 done、一个 running（与 RUNNING_STATE 同构映射）。
    const statuses = Array.from(
      container.querySelectorAll("[data-status]"),
    ).map((el) => el.getAttribute("data-status"))
    expect(statuses).toContain("done")
    expect(statuses).toContain("running")
  })
})

// P5d：运行视图右栏 per-item inspector + 缺省回落标量面板。
// 通过点画布节点（ReactFlow onNodeClick）设选中态，断言右栏渲染分支。
function clickNode(container: HTMLElement, index: number) {
  const nodes = container.querySelectorAll(".react-flow__node")
  const target = nodes[index]
  expect(target).toBeTruthy()
  fireEvent.click(target)
}

describe("RunCanvas per-item inspector (P5d)", () => {
  // 头号用例：storyboard fan-out。该节点同时打开 legacy 抽屉（coexist）——抽屉是
  // Radix 模态，会 aria-hide 右栏 inspector，故用 { hidden:true } 查右栏 inspector。
  it("storyboard fan-out: ItemInspector renders the N items in the right rail (coexists with drawer)", () => {
    currentState = {
      ...RUNNING_STATE,
      nodes: [
        { id: "rn-script", label: "脚本", type: "script", status: "done" },
        {
          id: "rn-board",
          label: "分镜",
          type: "storyboard",
          status: "done",
          items: [
            { json: { shot: "镜头甲" } },
            { json: { shot: "镜头乙" } },
            { json: { shot: "镜头丙" } },
          ],
        },
      ],
    }
    const { container } = renderRunTo()
    clickNode(container, 1) // storyboard-1
    // 右栏 inspector 渲染（getByText 不按 aria-hidden 过滤，抽屉同时开亦可命中）。
    expect(screen.getByText(/3 项/)).toBeInTheDocument()
    expect(screen.getByText(/镜头甲/)).toBeInTheDocument()
  })

  // 索引切换器机制：用 custom 节点（不开抽屉）验证 N>1 prev/next 切换。
  it("renders index switcher for N>1 and switches the visible item (custom node, no drawer)", () => {
    currentState = {
      ...RUNNING_STATE,
      nodes: [
        {
          id: "rn-custom",
          label: "翻译",
          type: "custom:translate",
          status: "done",
          output: "first",
          outputFormat: "text",
          items: [
            { json: { line: "第一句" } },
            { json: { line: "第二句" } },
          ],
        },
      ],
    }
    const customWf: WorkflowNode[] = [
      { id: "custom-1", type: "custom:translate", promptId: "", dependsOn: [], position: { x: 0, y: 0 } },
    ]
    const { container } = renderRunTo(customWf)
    clickNode(container, 0)
    expect(screen.getByText(/2 项/)).toBeInTheDocument()
    expect(screen.getByText(/第一句/)).toBeInTheDocument()
    expect(screen.queryByText(/第二句/)).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "下一项" }))
    expect(screen.getByText(/第二句/)).toBeInTheDocument()
    expect(screen.queryByText(/第一句/)).toBeNull()
  })

  it("FALLBACK: items undefined (old backend) → scalar custom panel renders, no crash", () => {
    // storyboard 节点无 items，但 custom 文本走标量面板：用 custom 节点带 output 验证回落。
    currentState = {
      ...RUNNING_STATE,
      nodes: [
        {
          id: "rn-custom",
          label: "翻译",
          type: "custom:translate",
          status: "done",
          output: "Bonjour",
          outputFormat: "text",
          // 注意：无 items 字段（老后端 / 标量节点）。
        },
      ],
    }
    const customWf: WorkflowNode[] = [
      { id: "custom-1", type: "custom:translate", promptId: "", dependsOn: [], position: { x: 0, y: 0 } },
    ]
    const { container } = renderRunTo(customWf)
    clickNode(container, 0)
    // 回落今天的标量面板：文本产物 + 内容，不抛、不出 ItemInspector「N 项」。
    expect(screen.getByText("Bonjour")).toBeInTheDocument()
    expect(screen.getByText(/文本产物/)).toBeInTheDocument()
    expect(screen.queryByText(/项$/)).toBeNull()
  })

  it("FALLBACK: empty items[] → scalar panel still renders (no item inspector)", () => {
    currentState = {
      ...RUNNING_STATE,
      nodes: [
        {
          id: "rn-custom",
          label: "翻译",
          type: "custom:translate",
          status: "done",
          output: "Hola",
          outputFormat: "text",
          items: [],
        },
      ],
    }
    const customWf: WorkflowNode[] = [
      { id: "custom-1", type: "custom:translate", promptId: "", dependsOn: [], position: { x: 0, y: 0 } },
    ]
    const { container } = renderRunTo(customWf)
    clickNode(container, 0)
    expect(screen.getByText("Hola")).toBeInTheDocument()
    expect(screen.queryByText(/0 项/)).toBeNull()
  })
})

// B4.4：http 节点响应体被安全策略抑制时的运行视图产物面板。
describe("SuppressedBodyPanel (http-status output)", () => {
  it("parseHttpStatus extracts the status code from {\"status\":N}", () => {
    expect(parseHttpStatus('{"status":200}')).toBe(200)
    expect(parseHttpStatus('{"status":403}')).toBe(403)
  })

  it("parseHttpStatus returns null for malformed content", () => {
    expect(parseHttpStatus("not json")).toBeNull()
    expect(parseHttpStatus('{"foo":1}')).toBeNull()
  })

  it("renders the suppressed-body label + status code, never the body", () => {
    render(<SuppressedBodyPanel content='{"status":200}' />)
    expect(screen.getByText(/响应体已按安全策略隐藏/)).toBeInTheDocument()
    expect(screen.getByText("200")).toBeInTheDocument()
  })

  it("renders the suppressed label even when status is unparseable", () => {
    render(<SuppressedBodyPanel content="opaque" />)
    expect(screen.getByText(/响应体已按安全策略隐藏/)).toBeInTheDocument()
    // 无可解析状态码时不渲染「状态码：」行。
    expect(screen.queryByText(/状态码/)).toBeNull()
  })
})

// #127：节点可见性纯函数（focus 聚焦 / hideCompleted 隐藏已完成）。
// 注：边 active 判定改用已合的 runEdges.markActiveEdges（其自带测试），故 #127 的
// computeEdgeActive 测试随该函数一并退役。
describe("RunCanvas computeNodeVisibility（focus / hideCompleted）", () => {
  it("隐藏已完成：done→hidden", () => {
    const v = computeNodeVisibility("done", { ...DEFAULT_TOPOLOGY_SETTINGS, hideCompleted: true })
    expect(v.hidden).toBe(true)
  })
  it("聚焦失败：非 failed→dim，failed→不 dim", () => {
    const s = { ...DEFAULT_TOPOLOGY_SETTINGS, focus: "failed" as const }
    expect(computeNodeVisibility("running", s).dimmed).toBe(true)
    expect(computeNodeVisibility("failed", s).dimmed).toBe(false)
  })
  it("聚焦进行中：非 running→dim", () => {
    const s = { ...DEFAULT_TOPOLOGY_SETTINGS, focus: "running" as const }
    expect(computeNodeVisibility("done", s).dimmed).toBe(true)
    expect(computeNodeVisibility("running", s).dimmed).toBe(false)
  })
  it("聚焦失败时 blocked 也 dim（非 failed）", () => {
    const s = { ...DEFAULT_TOPOLOGY_SETTINGS, focus: "failed" as const }
    expect(computeNodeVisibility("blocked", s).dimmed).toBe(true)
    expect(computeNodeVisibility("blocked", s).hidden).toBe(false)
  })
})

// PR-3：有逐页扇出资产的 storyboard → 渲成可折叠大功能容器；选中 → 右栏 Run Matrix。
describe("RunCanvas group container + Run Matrix (PR-3)", () => {
  // storyboard 扇出 2 个 asset todo（边 from=asset, to=storyboard todoId="rn-board"）。
  const FANOUT_STATE: ProjectState = {
    ...RUNNING_STATE,
    nodes: [
      { id: "rn-script", label: "脚本", type: "script", status: "done" },
      { id: "rn-board", label: "分镜", type: "storyboard", status: "done" },
      { id: "rn-a0", label: "图0", type: "asset", status: "done", assetId: "img0" },
      { id: "rn-a1", label: "图1", type: "asset", status: "running" },
    ],
    edges: [
      { from: "rn-board", to: "rn-script" },
      { from: "rn-a0", to: "rn-board" },
      { from: "rn-a1", to: "rn-board" },
    ],
  }

  it("storyboard 有扇出资产 → 渲大功能容器（[N 项] 徽标 + 状态条），不再平铺独立子节点", () => {
    currentState = FANOUT_STATE
    const { container } = renderRunTo()
    const group = container.querySelector('[data-slot="group-run-node"]')
    expect(group).toBeInTheDocument()
    expect(screen.getByText("2 项")).toBeInTheDocument()
    // 状态条每页一格（2 个 asset → 2 格）。
    const bar = container.querySelector('[data-slot="group-run-bar"]')!
    expect(bar.querySelectorAll("span").length).toBe(2)
    // 旧平铺子节点（asset-run-node）不再存在。
    expect(container.querySelector('[data-slot="asset-run-node"]')).toBeNull()
  })

  it("点容器主体 → 右栏渲 Run Matrix（图例 + 汇总），而非 storyboard ItemInspector", () => {
    currentState = FANOUT_STATE
    const { container } = renderRunTo()
    clickNode(container, 1) // storyboard-1 → groupRun
    expect(container.querySelector('[data-slot="run-matrix-legend"]')).toBeInTheDocument()
    expect(screen.getByText(/1\/2 完成/)).toBeInTheDocument()
    // 矩阵每页一格。
    expect(container.querySelectorAll('[data-slot="run-matrix-cell"]').length).toBe(2)
  })

  // 回归守卫（PR-7 minimap 状态色可见性）：jsdom 与运行态容器测量竞态一样「不测量节点」，
  // 若运行节点无 initialWidth/height，MiniMap 的 nodeHasDimensions 门控会把每个判为无尺寸 →
  // 一个方块都不画 → 状态着色完全看不见。补了 initialWidth/height 兜底后 minimap 应渲出节点。
  it("运行态 minimap 渲染节点方块（initialWidth 兜底测量竞态，否则 nodeHasDimensions 门控 0 节点）", () => {
    currentState = FANOUT_STATE
    const { container } = renderRunTo()
    expect(container.querySelectorAll(".react-flow__minimap-node").length).toBeGreaterThan(0)
  })
})

// 变体 A：run 内融合审核——生成完成上升沿召唤（toast/banner/主 CTA 提升）+ 就地开抽屉。
describe("RunCanvas completion summons in-run review (variant A)", () => {
  const DONE_STATE: ProjectState = {
    ...RUNNING_STATE,
    status: "review",
    runStatus: "done",
    assets: { total: 1, done: 1, pending: 1 },
  }

  function renderWith(state: ProjectState) {
    currentState = state
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const view = render(
      <QueryClientProvider client={qc}>
        <RunCanvas projectId="p1" org="acme" runId="run1" nodes={WF_NODES} onSelectRun={vi.fn()} />
      </QueryClientProvider>,
    )
    return { ...view, qc }
  }

  function rerenderWith(
    rerender: ReturnType<typeof render>["rerender"],
    qc: QueryClient,
    state: ProjectState,
  ) {
    currentState = state
    rerender(
      <QueryClientProvider client={qc}>
        <RunCanvas projectId="p1" org="acme" runId="run1" nodes={WF_NODES} onSelectRun={vi.fn()} />
      </QueryClientProvider>,
    )
  }

  it("does NOT fire on mount when the run is already done (SSE replay case)", () => {
    renderWith(DONE_STATE)
    expect(toast.success).not.toHaveBeenCalled()
  })

  it("fires the summon toast once on running→done + shows banner + elevates the CTA", () => {
    const { rerender, qc } = renderWith(RUNNING_STATE)
    // 运行中：无完成 banner、未召唤。
    expect(screen.queryByText(/本次生成完成/)).toBeNull()
    expect(toast.success).not.toHaveBeenCalled()

    // 翻到 done（上升沿）。
    rerenderWith(rerender, qc, DONE_STATE)
    // 召唤 toast 只弹一次，带「开始审核」动作。
    expect(toast.success).toHaveBeenCalledTimes(1)
    expect(toast.success).toHaveBeenCalledWith(
      "生成完成 · 1 张待审",
      expect.objectContaining({ action: expect.objectContaining({ label: "开始审核" }) }),
    )
    // 完成 banner 出现 + 主 CTA 提升为「开始审核」（banner + 浮层）。
    expect(screen.getByText(/本次生成完成 · 1 张待审/)).toBeInTheDocument()
    expect(screen.getAllByRole("button", { name: "开始审核" }).length).toBeGreaterThanOrEqual(1)
  })

  it("clicking 开始审核 opens the in-run review drawer (Dialog with the fused board)", async () => {
    const { rerender, qc } = renderWith(RUNNING_STATE)
    rerenderWith(rerender, qc, DONE_STATE)
    fireEvent.click(screen.getAllByRole("button", { name: "开始审核" })[0])
    // 抽屉内融合审核容器渲染（空队列 → 空态文案），证明就地开抽屉而非跳走。
    expect(await screen.findByText("没有待审资产")).toBeInTheDocument()
  })

  // 失败终态（runStatus=done 但 status=failed）：不显完成横幅/开始审核，改显失败细条。
  const FAILED_STATE: ProjectState = {
    ...RUNNING_STATE,
    status: "failed",
    runStatus: "done",
    assets: { total: 7, done: 0, failed: 7, pending: 0 },
  }

  it("failed run: no 完成 banner / 开始审核, shows failure strip with N/M failed", () => {
    renderWith(FAILED_STATE)
    expect(screen.queryByText(/本次生成完成/)).toBeNull()
    expect(screen.queryByRole("button", { name: "开始审核" })).toBeNull()
    expect(toast.success).not.toHaveBeenCalled()
    expect(screen.getByText(/本次运行失败 · 7\/7 素材写入失败/)).toBeInTheDocument()
  })

  it("does NOT fire the summon toast when the run ends failed", () => {
    const { rerender, qc } = renderWith(RUNNING_STATE)
    rerenderWith(rerender, qc, FAILED_STATE)
    expect(toast.success).not.toHaveBeenCalled()
  })
})

// 运行入口收敛：RunCanvas 走工作流端点（useRunWorkflow）；有必填 inputsSchema 时
// 点「运行」先弹 RunInputsDialog，不直接发请求（与项目页入口同一模式）。
describe("RunCanvas unified run entry (workflow endpoint + run inputs)", () => {
  it("有必填 schema 点运行先弹 RunInputsDialog，不直接发请求", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={queryClient}>
        <RunCanvas
          projectId="p1"
          org="acme"
          runId="run1"
          nodes={WF_NODES}
          workflowId="w1"
          inputsSchema={[{ name: "topic", label: "主题", type: "text", target: "variable", required: true }]}
          onSelectRun={vi.fn()}
        />
      </QueryClientProvider>,
    )
    fireEvent.click(screen.getByRole("button", { name: "重新运行" }))
    // 先弹运行期输入表单（默认标题），且未直接发起 run 请求。
    expect(screen.getByText("填写运行输入")).toBeInTheDocument()
    expect(runWorkflowMutateAsync).not.toHaveBeenCalled()
  })
})
