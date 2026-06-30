import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { useState } from "react"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "sonner"
import { InputsSchemaPanel } from "./InputsSchemaPanel"
import { WorkflowCanvas } from "./WorkflowCanvas"
import type { InputField, WorkflowNode } from "@/lib/types"

// ── 隔离测试：受控面板（父持有 schema 状态）────────────────────────────
function Harness({ initial = [] }: { initial?: InputField[] }) {
  const [schema, setSchema] = useState<InputField[]>(initial)
  return <InputsSchemaPanel schema={schema} onChange={setSchema} />
}

describe("InputsSchemaPanel", () => {
  it("添加字段后出现一行字段编辑器", async () => {
    const user = userEvent.setup()
    render(<Harness />)
    expect(screen.queryByLabelText("字段名 1")).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "添加字段" }))
    expect(screen.getByLabelText("字段名 1")).toBeInTheDocument()
  })

  it("删除字段后该行消失", async () => {
    const user = userEvent.setup()
    render(<Harness initial={[{ name: "heroName", type: "text", target: "variable" }]} />)
    expect(screen.getByLabelText("字段名 1")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "删除字段 1" }))
    expect(screen.queryByLabelText("字段名 1")).not.toBeInTheDocument()
  })

  it("把类型改为单选后出现选项编辑器，可新增并编辑选项", async () => {
    const user = userEvent.setup()
    render(<Harness initial={[{ name: "tone", type: "text", target: "variable" }]} />)
    // 改 target 防止 select 触发的其他校验干扰；先改 type。
    await user.selectOptions(screen.getByLabelText("类型 1"), "select")
    await user.click(screen.getByRole("button", { name: "添加选项 1" }))
    const opt = screen.getByLabelText("选项值 1-1")
    await user.type(opt, "warm")
    expect(opt).toHaveValue("warm")
  })

  it("编辑目标（target）", async () => {
    const user = userEvent.setup()
    render(<Harness initial={[{ name: "brief", type: "textarea", target: "variable" }]} />)
    const target = screen.getByLabelText("目标 1")
    await user.selectOptions(target, "brief")
    expect(target).toHaveValue("brief")
  })

  it("name 非法 → 显示行内校验错误", () => {
    render(<Harness initial={[{ name: "1bad", type: "text", target: "variable" }]} />)
    expect(screen.getByRole("alert").textContent).toMatch(/字段名/)
  })

  it("select 无 options → 显示校验错误", () => {
    render(<Harness initial={[{ name: "tone", type: "select", target: "variable", options: [] }]} />)
    expect(screen.getByRole("alert").textContent).toMatch(/选项/)
  })

  it("multiselect × 非 pbConfig → 显示校验错误", () => {
    render(<Harness initial={[{ name: "tags", type: "multiselect", target: "variable" }]} />)
    expect(screen.getByRole("alert").textContent).toMatch(/pbConfig|绘本/)
  })

  it("合法字段不显示错误", () => {
    render(<Harness initial={[{ name: "heroName", type: "text", target: "variable" }]} />)
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })
})

// ── 集成测试：保存路径携带 inputsSchema ────────────────────────────────
const updateMutate = vi.fn()
const createMutate = vi.fn()
vi.mock("@/features/projects/workflowApi", () => ({
  useCreateWorkflow: () => ({ mutateAsync: createMutate, isPending: false }),
  useUpdateWorkflow: () => ({ mutateAsync: updateMutate, isPending: false }),
}))
vi.mock("@/features/builtin-node-types/api", () => ({
  useBuiltinNodeTypes: () => ({
    data: [
      { type: "script", label: "剧本", description: "" },
      { type: "storyboard", label: "分镜", description: "" },
    ],
  }),
}))
vi.mock("@/features/custom-node-types/api", () => ({
  useCustomNodeTypes: () => ({ data: [] }),
  useCreateCustomNodeType: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))
vi.mock("@/features/org-secrets/api", () => ({
  useOrgSecrets: () => ({ data: [] }),
}))
vi.mock("@/app/rbac", () => ({
  useRole: () => ({ isAdmin: true, isLoading: false }),
}))

function renderCanvas(nodes: WorkflowNode[] = []) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <WorkflowCanvas
        workflowId="w1"
        projectId="p1"
        org="acme"
        workflowName="测试管线"
        nodes={nodes}
      />
      <Toaster />
    </QueryClientProvider>,
  )
}

describe("WorkflowCanvas 保存携带 inputsSchema", () => {
  beforeEach(() => {
    updateMutate.mockResolvedValue({ id: "w1" })
    createMutate.mockResolvedValue({ id: "w1" })
  })
  afterEach(() => vi.clearAllMocks())

  it("在输入面板新增字段并保存 → mutation body 含 inputsSchema", async () => {
    const user = userEvent.setup()
    renderCanvas([{ id: "script-1", type: "script", promptId: "", dependsOn: [] }])
    // 切到「工作流输入」面板。
    await user.click(screen.getByRole("button", { name: "工作流输入" }))
    await user.click(screen.getByRole("button", { name: "添加字段" }))
    await user.type(screen.getByLabelText("字段名 1"), "heroName")
    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(updateMutate).toHaveBeenCalledTimes(1))
    const call = updateMutate.mock.calls[0][0] as {
      wfId: string
      input: { inputsSchema?: InputField[] }
    }
    expect(call.wfId).toBe("w1")
    expect(call.input.inputsSchema).toEqual([
      expect.objectContaining({ name: "heroName", type: "text", target: "variable" }),
    ])
  })
})
