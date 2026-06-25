import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { PropertiesPanel, extractTemplateVars } from "./PropertiesPanel"
import type { NodeTypeDescription } from "./nodeDescTypes"
import type { LlmParams, WorkflowNode } from "@/lib/types"

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

// ── Task 13: extractTemplateVars unit tests ────────────────────────────────
describe("extractTemplateVars", () => {
  it("extracts unique tokens across both systemPrompt and userPrompt", () => {
    const vars = extractTemplateVars("系统：{{tone}}", "请将 {{draft}} 翻译，保持 {{tone}} 风格")
    expect(vars).toEqual(["tone", "draft"])
  })

  it("returns empty array for a template with no tokens", () => {
    expect(extractTemplateVars(undefined, "无变量提示词")).toEqual([])
  })

  it("deduplicates repeated tokens (one row per unique name)", () => {
    const vars = extractTemplateVars(undefined, "{{a}} and {{a}} and {{b}}")
    expect(vars).toEqual(["a", "b"])
  })

  it("does not crash on malformed {{ without closing }}", () => {
    expect(() => extractTemplateVars(undefined, "{{ broken")).not.toThrow()
    expect(extractTemplateVars(undefined, "{{ broken")).toEqual([])
  })

  it("handles undefined systemPrompt gracefully", () => {
    const vars = extractTemplateVars(undefined, "{{draft}}")
    expect(vars).toEqual(["draft"])
  })
})

// ── Task 13: typed node PropertiesPanel tests ────────────────────────────────

function typedNode(typeId = "reg-1", varBindings: WorkflowNode["varBindings"] = []): WorkflowNode {
  return {
    id: "custom-1",
    type: "custom:translate",
    typeId,
    promptId: "",
    dependsOn: ["script-1"],
    label: "翻译",
    color: "#7c93ff",
    varBindings,
  }
}

const defaultTypedParams: LlmParams = {
  systemPrompt: "你是翻译助手",
  userPrompt: "请翻译以下内容：{{draft}}",
  outputFormat: "text",
}

function renderTypedPanel(
  node: WorkflowNode,
  params: LlmParams,
  upstreamNodes = [{ id: "script-1", label: "script-1" }],
  extra: Partial<Parameters<typeof PropertiesPanel>[0]> = {},
) {
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
      typedParams={params}
      upstreamNodes={upstreamNodes}
      {...extra}
    />,
  )
  return { onPatch, onRename, onDelete }
}

describe("PropertiesPanel typed node (Task 13)", () => {
  it("typed node with {{draft}} template renders ONE binding row", () => {
    renderTypedPanel(typedNode(), defaultTypedParams)
    // 变量绑定标签
    expect(screen.getByText("变量绑定")).toBeInTheDocument()
    // {{draft}} 行标签
    expect(screen.getByText("{{draft}}")).toBeInTheDocument()
  })

  it("typed node does NOT render the built-in prompt-library Select", () => {
    renderTypedPanel(typedNode(), defaultTypedParams)
    expect(screen.queryByText("系统提示词 (Prompt Library)")).not.toBeInTheDocument()
  })

  it("typed node shows kind label and readonly params summary", () => {
    renderTypedPanel(typedNode(), defaultTypedParams)
    // 类型参数只读摘要 section
    expect(screen.getByText("类型参数（只读）")).toBeInTheDocument()
    // userPrompt 摘要
    expect(screen.getByText(/请翻译以下内容/)).toBeInTheDocument()
    // typed badge
    expect(screen.getByText("typed")).toBeInTheDocument()
  })

  it("selecting an upstream node sets node.varBindings[{name, sourceNodeId}]", async () => {
    const user = userEvent.setup()
    const { onPatch } = renderTypedPanel(typedNode(), defaultTypedParams)

    // 打开 Select for {{draft}}
    const combos = screen.getAllByRole("combobox")
    // The first combobox in typed panel is the varBinding for {{draft}}
    await user.click(combos[0])
    await user.click(screen.getByRole("option", { name: "script-1" }))

    expect(onPatch).toHaveBeenCalledWith({
      varBindings: [{ name: "draft", sourceNodeId: "script-1" }],
    })
  })

  it("two tokens {{a}}{{b}} render two binding rows", () => {
    const params: LlmParams = {
      userPrompt: "{{a}} and {{b}}",
    }
    renderTypedPanel(
      { ...typedNode(), dependsOn: ["n1", "n2"] },
      params,
      [{ id: "n1", label: "n1" }, { id: "n2", label: "n2" }],
    )
    expect(screen.getByText("{{a}}")).toBeInTheDocument()
    expect(screen.getByText("{{b}}")).toBeInTheDocument()
  })

  it("binding b does not clear binding a (varBindings merge)", async () => {
    const user = userEvent.setup()
    const params: LlmParams = {
      userPrompt: "{{a}} and {{b}}",
    }
    // Pre-existing binding for a
    const node = {
      ...typedNode(),
      dependsOn: ["n1", "n2"],
      varBindings: [{ name: "a", sourceNodeId: "n1" }],
    }
    const { onPatch } = renderTypedPanel(
      node,
      params,
      [{ id: "n1", label: "n1" }, { id: "n2", label: "n2" }],
    )

    // Find combos: first = {{a}} (has value n1), second = {{b}} (empty)
    const combos = screen.getAllByRole("combobox")
    // Pick n2 for {{b}}
    await user.click(combos[1])
    await user.click(screen.getByRole("option", { name: "n2" }))

    // Should include existing a binding and new b binding
    expect(onPatch).toHaveBeenCalledWith({
      varBindings: expect.arrayContaining([
        { name: "a", sourceNodeId: "n1" },
        { name: "b", sourceNodeId: "n2" },
      ]),
    })
  })

  it("typed node with no upstream renders without crashing and has no empty-string option (Blocker 1)", () => {
    // Radix Select throws on value="" when a SelectItem with value="" mounts on
    // open (jsdom). Guard: when upstreamNodes=[] we render a plain <div> hint
    // instead of <SelectItem value="">. Assert:
    // 1. The component renders without throwing.
    // 2. No Radix option element with an empty value is present in the DOM
    //    (the hint div is not a role="option").
    expect(() => renderTypedPanel(typedNode(), defaultTypedParams, [] /* no upstream */)).not.toThrow()
    // No <SelectItem value=""> should be in the DOM (Radix renders options as
    // role="option" with data-value; an empty-string value would be data-value="").
    const emptyOptions = document.querySelectorAll('[role="option"][data-value=""]')
    expect(emptyOptions).toHaveLength(0)
    // Confirm the panel did render (sanity: heading present).
    expect(screen.getByText("变量绑定")).toBeInTheDocument()
  })

  it("renders typed-param summary via PropertiesForm when description is provided, and persists edits as parameters/typeVersion (P-write-4)", async () => {
    const user = userEvent.setup()
    const description: NodeTypeDescription = {
      type: "custom:translate",
      version: 1,
      label: "翻译",
      description: "",
      group: "transform",
      inputs: [],
      outputs: [],
      properties: [{ name: "userPrompt", label: "用户提示词", type: "textarea" }],
    }
    const { onPatch } = renderTypedPanel(typedNode(), defaultTypedParams, undefined, {
      description,
    })
    // (a) PropertiesForm field renders.
    const field = screen.getByLabelText("用户提示词")
    expect(field).toBeInTheDocument()
    // (b) editing a field now persists via onPatch({ parameters, typeVersion }).
    await user.type(field, "!")
    expect(onPatch).toHaveBeenCalled()
    const arg = onPatch.mock.calls.at(-1)![0]
    expect(arg).toHaveProperty("parameters")
    expect(arg.typeVersion).toBe(1)
    // The edited field value is carried under parameters.
    expect((arg.parameters as Record<string, unknown>).userPrompt).toContain("!")
  })

  it("annotation custom node (no typeId) does not show typed UI, shows 编辑类型 button", () => {
    const annotationNode: WorkflowNode = {
      id: "custom-2",
      type: "custom:note",
      promptId: "",
      dependsOn: [],
      label: "注释",
      color: "#999",
    }
    const onEditType = vi.fn()
    render(
      <PropertiesPanel
        node={annotationNode}
        org="org-1"
        otherIds={[]}
        onPatch={vi.fn()}
        onRename={vi.fn()}
        onDelete={vi.fn()}
        onEditType={onEditType}
      />,
    )
    // No typed UI
    expect(screen.queryByText("变量绑定")).not.toBeInTheDocument()
    expect(screen.queryByText("类型参数（只读）")).not.toBeInTheDocument()
    // Has 编辑类型 button (annotation path)
    expect(screen.getByRole("button", { name: "编辑类型" })).toBeInTheDocument()
  })
})
