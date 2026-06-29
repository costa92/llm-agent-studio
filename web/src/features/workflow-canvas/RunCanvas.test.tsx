import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { RunCanvas, SuppressedBodyPanel, parseHttpStatus } from "./RunCanvas"
import { computeNodeVisibility, computeEdgeActive } from "./topologyUtils"
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

vi.mock("@/features/workflow/api", () => ({
  usePlans: vi.fn(() => ({
    data: [{ id: "run1", projectId: "p1", status: "running", valid: true, fallbackUsed: false, createdAt: new Date().toISOString(), workflowId: "w1" }],
    refetch: vi.fn(),
  })),
  useProjectState: vi.fn(() => ({ data: currentState })),
  useRun: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useCancel: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useScript: vi.fn(() => ({ data: null, isLoading: false, isError: false })),
  useShots: vi.fn(() => ({ data: [], isLoading: false, isError: false })),
  useProjectAssets: vi.fn(() => ({ data: [] })),
  fetchAllEvents: vi.fn(async () => []),
}))

vi.mock("@/features/workflow/useProductionTimeline", () => ({
  useProductionTimeline: vi.fn(() => ({ log: [], conn: "connected" })),
}))

vi.mock("@/features/review/api", () => ({
  useAsset: vi.fn(() => ({ data: undefined })),
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

describe("RunCanvas 过滤/边-active 纯函数", () => {
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
  it("边 active：源 done & 目标 running", () => {
    expect(computeEdgeActive("done", "running")).toBe(true)
    expect(computeEdgeActive("running", "running")).toBe(false)
    expect(computeEdgeActive("done", "done")).toBe(false)
  })
})
