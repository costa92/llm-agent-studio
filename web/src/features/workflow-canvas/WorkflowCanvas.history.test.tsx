import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { WorkflowCanvas } from "./WorkflowCanvas"
import type { WorkflowNode } from "@/lib/types"

// workflowApi 的 mutation hooks 仅在「保存」时调用——本测试不触发保存，mock 即可。
vi.mock("@/features/projects/workflowApi", () => ({
  useCreateWorkflow: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useUpdateWorkflow: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
}))

afterEach(() => vi.restoreAllMocks())

function renderCanvas(nodes: WorkflowNode[]) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <WorkflowCanvas
        workflowId="w1"
        projectId="p1"
        org="acme"
        workflowName="测试管线"
        nodes={nodes}
      />
    </QueryClientProvider>,
  )
}

describe("WorkflowCanvas undo/redo wiring", () => {
  // 用「自动整理」作为可点击的变更入口（无需 ReactFlow 内部选中态，jsdom 友好）：
  // 它先 takeSnapshot 再重排，应使「撤销」按钮可用；点撤销后撤销态消失。
  it("enables 撤销 after a mutation and clears it on undo", async () => {
    const user = userEvent.setup()
    // 两节点、坐标偏离种子布局，便于自动整理产生实际位移。
    renderCanvas([
      { id: "script-1", type: "script", promptId: "", dependsOn: [], position: { x: 500, y: 500 } },
      {
        id: "storyboard-1",
        type: "storyboard",
        promptId: "",
        dependsOn: ["script-1"],
        position: { x: 500, y: 500 },
      },
    ])

    const undoBtn = await screen.findByRole("button", { name: "撤销" })
    // 初始：无历史 → 撤销禁用。
    expect(undoBtn).toBeDisabled()

    await user.click(screen.getByRole("button", { name: "自动整理" }))
    // 变更后撤销可用。
    await waitFor(() => expect(undoBtn).toBeEnabled())

    await user.click(undoBtn)
    // 撤销回到唯一基线 → 历史耗尽，撤销再次禁用。
    await waitFor(() => expect(undoBtn).toBeDisabled())
  })

  it("renders 撤销/重做 controls in the top bar", async () => {
    renderCanvas([
      { id: "script-1", type: "script", promptId: "", dependsOn: [] },
    ])
    expect(await screen.findByRole("button", { name: "撤销" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "重做" })).toBeInTheDocument()
  })
})
