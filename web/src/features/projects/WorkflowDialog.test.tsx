import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { WorkflowForm } from "./WorkflowDialog"
import { findGraphError } from "./WorkflowDialog.schema"
import type { Prompt, Workflow } from "@/lib/types"
import * as promptApi from "@/features/prompt/api"

// WorkflowNodesEditor 内部用 useCreatePrompt 做行内新建——测试里 mock 掉。
vi.mock("@/features/prompt/api", () => ({
  useCreatePrompt: vi.fn(() => ({ mutateAsync: vi.fn() })),
}))

afterEach(() => {
  vi.restoreAllMocks()
})

function makePrompt(over: Partial<Prompt> = {}): Prompt {
  return {
    id: "p1",
    orgId: "o1",
    name: "我的剧本",
    content: "draft",
    style: "",
    kind: "script",
    isDefault: false,
    createdAt: "2026-06-01T00:00:00Z",
    updatedAt: "2026-06-01T00:00:00Z",
    ...over,
  }
}

function makeWorkflow(over: Partial<Workflow> = {}): Workflow {
  return {
    id: "wf1",
    projectId: "p1",
    name: "默认工作流",
    nodes: [{ id: "script-1", type: "script", promptId: "", dependsOn: [] }],
    createdAt: "2026-06-01T00:00:00Z",
    updatedAt: "2026-06-01T00:00:00Z",
    ...over,
  }
}

describe("WorkflowForm", () => {
  it("renders default nodes for a new workflow and submits with name + nodes", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeWorkflow({ id: "new1" }))
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    render(<WorkflowForm onSubmit={onSubmit} onSuccess={onSuccess} />)

    // 默认两个节点：script-1 + storyboard-1（用 id 输入框值断言）。
    const idInputs = screen.getAllByPlaceholderText("e.g. script-1")
    expect(idInputs).toHaveLength(2)

    await user.type(screen.getByLabelText("工作流名称"), "我的工作流")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    const arg = onSubmit.mock.calls[0][0]
    expect(arg.name).toBe("我的工作流")
    expect(arg.nodes).toHaveLength(2)
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
  })

  it("adds a node when 添加节点 is clicked", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeWorkflow())
    const user = userEvent.setup()

    render(<WorkflowForm initial={makeWorkflow()} onSubmit={onSubmit} />)

    expect(screen.getAllByPlaceholderText("e.g. script-1")).toHaveLength(1)
    await user.click(screen.getByRole("button", { name: "+ 添加节点" }))
    expect(screen.getAllByPlaceholderText("e.g. script-1")).toHaveLength(2)
  })

  it("blocks submit and shows an error when the name is empty", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()

    render(<WorkflowForm initial={makeWorkflow({ name: "" })} onSubmit={onSubmit} />)

    await user.click(screen.getByRole("button", { name: "保存" }))

    expect(await screen.findByText("请输入工作流名称")).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("preselects the per-kind default for a newly added script node", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeWorkflow())
    const user = userEvent.setup()
    const prompts = [makePrompt({ id: "def1", isDefault: true })]

    // 起始无节点，避免与新增节点的 id 冲突 / 依赖干扰。
    render(
      <WorkflowForm
        initial={makeWorkflow({ name: "wf", nodes: [] })}
        org="o1"
        prompts={prompts}
        onSubmit={onSubmit}
      />,
    )

    await user.click(screen.getByRole("button", { name: "+ 添加节点" }))
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    const arg = onSubmit.mock.calls[0][0]
    expect(arg.nodes).toHaveLength(1)
    expect(arg.nodes[0].type).toBe("script")
    expect(arg.nodes[0].promptId).toBe("def1")
  })

  it("creates a prompt inline and assigns it to the node", async () => {
    const mutateAsync = vi
      .fn()
      .mockResolvedValue(makePrompt({ id: "new-p", name: "行内新建" }))
    vi.mocked(promptApi.useCreatePrompt).mockReturnValue({
      mutateAsync,
    } as unknown as ReturnType<typeof promptApi.useCreatePrompt>)

    const onSubmit = vi.fn().mockResolvedValue(makeWorkflow())
    const user = userEvent.setup()

    render(
      <WorkflowForm
        initial={makeWorkflow({
          name: "wf",
          nodes: [{ id: "s1", type: "script", promptId: "", dependsOn: [] }],
        })}
        org="o1"
        onSubmit={onSubmit}
      />,
    )

    // 节点内两个 combobox：[0]=任务类型，[1]=系统提示词。打开提示词下拉。
    const combos = screen.getAllByRole("combobox")
    await user.click(combos[1])
    await user.click(screen.getByRole("option", { name: "＋ 新建提示词" }))

    // 填写行内表单并保存。
    await user.type(screen.getByLabelText("名称"), "行内新建")
    await user.type(screen.getByLabelText("内容"), "some content")
    await user.click(screen.getByRole("button", { name: "保存新建提示词" }))

    await waitFor(() =>
      expect(mutateAsync).toHaveBeenCalledWith({
        name: "行内新建",
        content: "some content",
        style: "",
        kind: "script",
      }),
    )

    // 保存成功后行内表单关闭（名称输入消失）—— 等其完成再提交。
    await waitFor(() =>
      expect(screen.queryByLabelText("名称")).not.toBeInTheDocument(),
    )

    // 该节点 promptId 已更新为新建返回的 id —— 提交校验。
    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalled())
    const arg = onSubmit.mock.calls[0][0]
    expect(arg.nodes[0].promptId).toBe("new-p")
  })

  it("标准管线 button fills script → storyboard", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeWorkflow())
    const user = userEvent.setup()
    render(
      <WorkflowForm
        initial={makeWorkflow({
          name: "wf",
          nodes: [{ id: "x", type: "script", promptId: "", dependsOn: [] }],
        })}
        onSubmit={onSubmit}
      />,
    )
    await user.click(screen.getByRole("button", { name: "标准管线" }))
    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalled())
    const arg = onSubmit.mock.calls[0][0]
    expect(arg.nodes.map((n: { type: string }) => n.type)).toEqual([
      "script",
      "storyboard",
    ])
    expect(arg.nodes[1].dependsOn).toEqual(["script-1"])
  })

  it("supports inline custom prompt text (not saved to library)", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeWorkflow())
    const user = userEvent.setup()

    render(
      <WorkflowForm
        initial={makeWorkflow({
          name: "wf",
          nodes: [{ id: "s1", type: "script", promptId: "", dependsOn: [] }],
        })}
        org="o1"
        onSubmit={onSubmit}
      />,
    )

    // 打开提示词下拉 → 选「＋ 自定义输入」→ 出现文本框，手输内容。
    const combos = screen.getAllByRole("combobox")
    await user.click(combos[1])
    await user.click(screen.getByRole("option", { name: /自定义输入/ }))
    await user.type(
      screen.getByPlaceholderText(/直接输入本节点/),
      "临时手写提示词",
    )

    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalled())
    const arg = onSubmit.mock.calls[0][0]
    expect(arg.nodes[0].promptText).toBe("临时手写提示词")
    expect(arg.nodes[0].promptId).toBe("")
  })

  it("blocks submit and shows an error on duplicate node ids", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()

    const dup = makeWorkflow({
      name: "dup",
      nodes: [
        { id: "a", type: "script", promptId: "", dependsOn: [] },
        { id: "a", type: "storyboard", promptId: "", dependsOn: [] },
      ],
    })
    render(<WorkflowForm initial={dup} onSubmit={onSubmit} />)

    await user.click(screen.getByRole("button", { name: "保存" }))

    expect(await screen.findByText("存在重复的节点 ID: a")).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("blocks submit and shows cycle error when nodes form a mutual dependency (A↔B)", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()

    const cyclic = makeWorkflow({
      name: "cyclic",
      nodes: [
        { id: "A", type: "script", promptId: "", dependsOn: ["B"] },
        { id: "B", type: "storyboard", promptId: "", dependsOn: ["A"] },
      ],
    })
    render(<WorkflowForm initial={cyclic} onSubmit={onSubmit} />)

    await user.click(screen.getByRole("button", { name: "保存" }))

    expect(await screen.findByRole("alert")).toBeInTheDocument()
    const alertText = screen.getByRole("alert").textContent ?? ""
    expect(alertText).toMatch(/循环依赖/)
    expect(onSubmit).not.toHaveBeenCalled()
  })
})

describe("findGraphError", () => {
  it("returns null for a valid linear graph", () => {
    const nodes = [
      { id: "a", dependsOn: [] },
      { id: "b", dependsOn: ["a"] },
      { id: "c", dependsOn: ["b"] },
    ]
    expect(findGraphError(nodes)).toBeNull()
  })

  it("returns null for an empty graph", () => {
    expect(findGraphError([])).toBeNull()
  })

  it("returns a message for a direct cycle (A↔B)", () => {
    const nodes = [
      { id: "A", dependsOn: ["B"] },
      { id: "B", dependsOn: ["A"] },
    ]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toMatch(/循环依赖/)
  })

  it("returns a message for a self-loop (A→A)", () => {
    const nodes = [{ id: "A", dependsOn: ["A"] }]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toMatch(/循环依赖/)
  })

  it("returns a message for a longer cycle (A→B→C→A)", () => {
    const nodes = [
      { id: "A", dependsOn: ["C"] },
      { id: "B", dependsOn: ["A"] },
      { id: "C", dependsOn: ["B"] },
    ]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toMatch(/循环依赖/)
  })

  it("returns a message for a dependency on an unknown node", () => {
    const nodes = [
      { id: "a", dependsOn: [] },
      { id: "b", dependsOn: ["unknown-node"] },
    ]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toContain("unknown-node")
  })
})
