import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { RunCanvas, SuppressedBodyPanel, parseHttpStatus } from "./RunCanvas"
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

vi.mock("@/features/workflow/api", () => ({
  usePlans: vi.fn(() => ({
    data: [{ id: "run1", projectId: "p1", status: "running", valid: true, fallbackUsed: false, createdAt: new Date().toISOString(), workflowId: "w1" }],
    refetch: vi.fn(),
  })),
  useProjectState: vi.fn(() => ({ data: RUNNING_STATE })),
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
