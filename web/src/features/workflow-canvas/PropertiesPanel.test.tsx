import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { PropertiesPanel } from "./PropertiesPanel"
import type { WorkflowNode } from "@/lib/types"

// useCreatePrompt 仅在「＋ 新建提示词」分支调用——这里不测该分支，mock 即可。
vi.mock("@/features/prompt/api", () => ({
  useCreatePrompt: vi.fn(() => ({ mutateAsync: vi.fn() })),
}))

afterEach(() => vi.restoreAllMocks())

function scriptNode(over: Partial<WorkflowNode> = {}): WorkflowNode {
  return { id: "script-1", type: "script", promptId: "", dependsOn: [], ...over }
}

function renderPanel(node: WorkflowNode | null, extra: Partial<Parameters<typeof PropertiesPanel>[0]> = {}) {
  const onPatch = vi.fn()
  const onRename = vi.fn()
  const onDelete = vi.fn()
  render(
    <PropertiesPanel
      node={node}
      org="org-1"
      otherIds={[]}
      onPatch={onPatch}
      onRename={onRename}
      onDelete={onDelete}
      {...extra}
    />,
  )
  return { onPatch, onRename, onDelete }
}

describe("PropertiesPanel", () => {
  it("shows empty hint when nothing is selected", () => {
    renderPanel(null)
    expect(screen.getByText("选择一个节点查看属性")).toBeInTheDocument()
  })

  it("custom sentinel sets promptText path and clears promptId on edit", async () => {
    const user = userEvent.setup()
    const { onPatch } = renderPanel(scriptNode())

    // 两个 combobox：[0]=任务类型，[1]=系统提示词。打开提示词下拉选「＋ 自定义输入」。
    const combos = screen.getAllByRole("combobox")
    await user.click(combos[1])
    await user.click(screen.getByRole("option", { name: /自定义输入/ }))

    // textarea 出现；输入文本 → onPatch 写 promptText 且清空 promptId。
    const ta = screen.getByPlaceholderText("直接输入本节点使用的系统提示词…")
    await user.type(ta, "x")
    expect(onPatch).toHaveBeenCalledWith({ promptText: "x", promptId: "" })
  })

  it("type change resets promptId via defaultPromptIdFor", async () => {
    const user = userEvent.setup()
    const { onPatch } = renderPanel(scriptNode({ promptId: "p-old" }))

    // 任务类型 Select（combobox[0]）：从 script 改到 asset。
    const combos = screen.getAllByRole("combobox")
    await user.click(combos[0])
    await user.click(screen.getByRole("option", { name: "生成资源 (asset)" }))

    expect(onPatch).toHaveBeenCalledWith({
      type: "asset",
      promptId: "", // 无 prompts → defaultPromptIdFor 返回 ""。
      promptText: "",
    })
  })

  it("rejects duplicate id with an inline message and does not rename", async () => {
    const user = userEvent.setup()
    const { onRename } = renderPanel(scriptNode(), { otherIds: ["dup"] })

    const idInput = screen.getByPlaceholderText("e.g. script-1")
    await user.clear(idInput)
    await user.type(idInput, "dup")
    expect(screen.getByText("存在重复的节点 ID: dup")).toBeInTheDocument()

    // 失焦不应触发重命名（重复）。
    await user.tab()
    expect(onRename).not.toHaveBeenCalled()
  })

  it("renames on blur when id is valid", async () => {
    const user = userEvent.setup()
    const { onRename } = renderPanel(scriptNode(), { otherIds: ["other"] })

    const idInput = screen.getByPlaceholderText("e.g. script-1")
    await user.clear(idInput)
    await user.type(idInput, "renamed")
    await user.tab()
    expect(onRename).toHaveBeenCalledWith("renamed")
  })

  it("delete button fires onDelete", async () => {
    const user = userEvent.setup()
    const { onDelete } = renderPanel(scriptNode())
    await user.click(screen.getByRole("button", { name: "删除节点" }))
    expect(onDelete).toHaveBeenCalledTimes(1)
  })

  it("hides prompt picker for asset nodes", () => {
    renderPanel(scriptNode({ type: "asset" }))
    expect(
      screen.queryByText("系统提示词 (Prompt Library)"),
    ).not.toBeInTheDocument()
  })
})
