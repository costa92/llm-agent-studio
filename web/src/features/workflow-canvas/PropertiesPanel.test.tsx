import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { PropertiesPanel, extractTemplateVars } from "./PropertiesPanel"
import type { NodeTypeDescription } from "./nodeDescTypes"
import type { LlmParams, WorkflowNode } from "@/lib/types"

// useCreatePrompt 仅在「＋ 新建提示词」分支调用——这里不测该分支，mock 即可。
vi.mock("@/features/prompt/api", () => ({
  useCreatePrompt: vi.fn(() => ({ mutateAsync: vi.fn() })),
}))

// P5.1: resourceLocator/secret 数据源 hook——默认返回空（不崩溃），单测内按需 mockReturnValue。
import { useModelConfigs } from "@/features/cost/api"
import { useOrgSecrets } from "@/features/org-secrets/api"
vi.mock("@/features/cost/api", () => ({
  useModelConfigs: vi.fn(() => ({ data: undefined })),
}))
vi.mock("@/features/org-secrets/api", () => ({
  useOrgSecrets: vi.fn(() => ({ data: undefined })),
}))
const mockUseModelConfigs = vi.mocked(useModelConfigs)
const mockUseOrgSecrets = vi.mocked(useOrgSecrets)

afterEach(() => {
  vi.restoreAllMocks()
  // restoreAllMocks 不重置 vi.mock 工厂返回值——显式恢复默认空态，避免跨用例泄漏。
  mockUseModelConfigs.mockReturnValue({ data: undefined } as ReturnType<typeof useModelConfigs>)
  mockUseOrgSecrets.mockReturnValue({ data: undefined } as ReturnType<typeof useOrgSecrets>)
})

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

  it("shows 预审 (prescreen) in 任务类型 for a prescreen builtin node (not an empty select)", () => {
    // prescreen 是第一类内置节点（builtinnode/catalog.go），任务类型 Select 必须能解析它，
    // 否则 value="prescreen" 匹配不到任何 SelectItem → 触发器空白（QA 实测 BUG）。
    renderPanel(scriptNode({ type: "prescreen" }))
    const combo = screen.getAllByRole("combobox")[0]
    expect(within(combo).getByText("预审 (prescreen)")).toBeInTheDocument()
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

// P5：上游 studio.script 类型的 OutputSchema（字段级绑定的字段候选源）。
const scriptOutputSchema = [
  { name: "title", type: "string" },
  { name: "logline", type: "string" },
  { name: "characterSheet", type: "string" },
  { name: "scenes", type: "array" },
]

function renderTypedPanel(
  node: WorkflowNode,
  params: LlmParams,
  upstreamNodes: NonNullable<Parameters<typeof PropertiesPanel>[0]["upstreamNodes"]> = [
    { id: "script-1", label: "script-1" },
  ],
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
    // (label is honest: an editable PropertiesForm drops the “（只读）” suffix.)
    expect(screen.getByText("类型参数")).toBeInTheDocument()
    expect(screen.queryByText("类型参数（只读）")).not.toBeInTheDocument()
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

  it("populates the model resourceLocator from useModelConfigs (P5.1)", () => {
    mockUseModelConfigs.mockReturnValue({
      data: [
        { id: "m1", orgId: "org-1", kind: "text", provider: "openai", model: "gpt-4o", enabled: true, isDefault: false, baseUrl: "", hasApiKey: true },
        { id: "m2", orgId: "org-1", kind: "text", provider: "anthropic", model: "claude-3", enabled: true, isDefault: false, baseUrl: "", hasApiKey: true },
      ],
    } as ReturnType<typeof useModelConfigs>)
    const description: NodeTypeDescription = {
      type: "custom:translate", version: 1, label: "翻译", description: "", group: "transform",
      inputs: [], outputs: [],
      properties: [{ name: "model", label: "模型", type: "resourceLocator", typeOptions: { dataSource: "model" } }],
    }
    renderTypedPanel(typedNode(), defaultTypedParams, undefined, { description })
    const select = screen.getByLabelText("模型")
    // 选项来自 org 的 model-config 列表（非空），不再是空下拉。
    expect(within(select).getByRole("option", { name: "openai · gpt-4o" })).toBeInTheDocument()
    expect(within(select).getByRole("option", { name: "anthropic · claude-3" })).toBeInTheDocument()
  })

  it("filters the model resourceLocator to text-kind configs only — no image/audio (QA bug)", () => {
    // LLM(文本)节点只应能选文本模型；图像/音频模型对文本生成无意义（QA 实测：扁平列出全部 kind）。
    mockUseModelConfigs.mockReturnValue({
      data: [
        { id: "m1", orgId: "org-1", kind: "text", provider: "deepseek", model: "deepseek-chat", enabled: true, isDefault: false, baseUrl: "", hasApiKey: true },
        { id: "m2", orgId: "org-1", kind: "image", provider: "minimax", model: "image-01", enabled: true, isDefault: false, baseUrl: "", hasApiKey: true },
        { id: "m3", orgId: "org-1", kind: "audio", provider: "minimax", model: "speech-2.8-hd", enabled: true, isDefault: false, baseUrl: "", hasApiKey: true },
      ],
    } as ReturnType<typeof useModelConfigs>)
    const description: NodeTypeDescription = {
      type: "custom:translate", version: 1, label: "翻译", description: "", group: "transform",
      inputs: [], outputs: [],
      properties: [{ name: "model", label: "模型", type: "resourceLocator", typeOptions: { dataSource: "model" } }],
    }
    renderTypedPanel(typedNode(), defaultTypedParams, undefined, { description })
    const select = screen.getByLabelText("模型")
    expect(within(select).getByRole("option", { name: "deepseek · deepseek-chat" })).toBeInTheDocument()
    expect(within(select).queryByRole("option", { name: "minimax · image-01" })).toBeNull()
    expect(within(select).queryByRole("option", { name: "minimax · speech-2.8-hd" })).toBeNull()
  })

  it("populates secret-insert dropdown with NAMES only from useOrgSecrets (P5.1)", () => {
    mockUseOrgSecrets.mockReturnValue({
      data: [
        { id: "s1", orgId: "org-1", name: "STRIPE_KEY", hasValue: true },
        { id: "s2", orgId: "org-1", name: "OPENAI_KEY", hasValue: true },
      ],
    } as ReturnType<typeof useOrgSecrets>)
    const description: NodeTypeDescription = {
      type: "custom:translate", version: 1, label: "翻译", description: "", group: "transform",
      inputs: [], outputs: [],
      properties: [{ name: "headers", label: "请求头", type: "keyValue" }],
    }
    // KeyValue 行来自 parameters.headers——给一行 header 让密钥插入下拉渲染。
    const node = { ...typedNode(), parameters: { headers: { Authorization: "" } } }
    renderTypedPanel(node, defaultTypedParams, undefined, { description })
    // 密钥插入下拉只列出 NAME 形式的 {{secret:NAME}} 令牌。
    expect(screen.getByText("{{secret:STRIPE_KEY}}")).toBeInTheDocument()
    expect(screen.getByText("{{secret:OPENAI_KEY}}")).toBeInTheDocument()
  })

  it("never surfaces secret VALUES — OrgSecret DTO carries no value field (P5.1)", () => {
    // 安全断言：即便后端 DTO 误带 value，UI 也只投影 name。这里 mock 一个含 value 的脏对象，
    // 断言该 value 文本绝不出现在 DOM。
    // 故意注入 OrgSecret 不该有的 value 字段（脏 DTO），验证 UI 不泄漏；
    // 经 unknown 强转，因为带 value 的对象与 OrgSecret 不重叠。
    mockUseOrgSecrets.mockReturnValue({
      data: [
        { id: "s1", orgId: "org-1", name: "STRIPE_KEY", hasValue: true, value: "sk_live_SECRETVALUE" },
      ],
    } as unknown as ReturnType<typeof useOrgSecrets>)
    const description: NodeTypeDescription = {
      type: "custom:translate", version: 1, label: "翻译", description: "", group: "transform",
      inputs: [], outputs: [],
      properties: [{ name: "headers", label: "请求头", type: "keyValue" }],
    }
    const node = { ...typedNode(), parameters: { headers: { Authorization: "" } } }
    renderTypedPanel(node, defaultTypedParams, undefined, { description })
    // 名字出现（下拉已渲染），但 value 绝不出现。
    expect(screen.getByText("{{secret:STRIPE_KEY}}")).toBeInTheDocument()
    expect(screen.queryByText(/sk_live_SECRETVALUE/)).not.toBeInTheDocument()
    expect(document.body.innerHTML).not.toContain("sk_live_SECRETVALUE")
  })

  it("renders without crashing when model/secret hooks are still loading (undefined data) (P5.1)", () => {
    mockUseModelConfigs.mockReturnValue({ data: undefined } as ReturnType<typeof useModelConfigs>)
    mockUseOrgSecrets.mockReturnValue({ data: undefined } as ReturnType<typeof useOrgSecrets>)
    const description: NodeTypeDescription = {
      type: "custom:translate", version: 1, label: "翻译", description: "", group: "transform",
      inputs: [], outputs: [],
      properties: [
        { name: "model", label: "模型", type: "resourceLocator", typeOptions: { dataSource: "model" } },
        { name: "headers", label: "请求头", type: "keyValue" },
      ],
    }
    expect(() =>
      renderTypedPanel(typedNode(), defaultTypedParams, undefined, { description }),
    ).not.toThrow()
    // 加载态：model 下拉仅有「（默认）」占位，无 model 选项。
    const select = screen.getByLabelText("模型")
    expect(within(select).queryByRole("option", { name: /·/ })).toBeNull()
  })

  it("does NOT render a field picker when no upstream node is selected (no binding)", () => {
    // 无 sourceNodeId → 不渲染字段选择器，仅上游节点 Select（1 个 combobox）。
    renderTypedPanel(typedNode(), defaultTypedParams, [
      { id: "script-1", label: "剧本", outputSchema: scriptOutputSchema },
    ])
    expect(screen.getAllByRole("combobox")).toHaveLength(1)
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

// ── P5: field-level varBindings ──────────────────────────────────────────────

const scriptUpstream = [{ id: "script-1", label: "剧本", outputSchema: scriptOutputSchema }]

describe("PropertiesPanel field-level varBindings (P5)", () => {
  it("renders a field picker populated from the upstream node type's OutputSchema + 整个输出 default", async () => {
    const user = userEvent.setup()
    const node = typedNode("reg-1", [{ name: "draft", sourceNodeId: "script-1" }])
    renderTypedPanel(node, defaultTypedParams, scriptUpstream, { exprChannel: true })

    const fieldCombo = screen.getByRole("combobox", { name: "字段绑定 draft" })
    await user.click(fieldCombo)
    expect(screen.getByRole("option", { name: "（整个输出）" })).toBeInTheDocument()
    for (const f of ["title", "logline", "characterSheet", "scenes"]) {
      expect(screen.getByRole("option", { name: f })).toBeInTheDocument()
    }
  })

  it("security: field options come ONLY from OutputSchema names (no secret/params source)", async () => {
    const user = userEvent.setup()
    const node = typedNode("reg-1", [{ name: "draft", sourceNodeId: "script-1" }])
    renderTypedPanel(node, defaultTypedParams, scriptUpstream, { exprChannel: true })

    await user.click(screen.getByRole("combobox", { name: "字段绑定 draft" }))
    const optionTexts = screen
      .getAllByRole("option")
      .map((o) => o.textContent?.trim())
    // Exactly the whole-output sentinel + the 4 declared OutputSchema fields — nothing else.
    expect(new Set(optionTexts)).toEqual(
      new Set(["（整个输出）", "title", "logline", "characterSheet", "scenes"]),
    )
  })

  it("selecting a field calls patchVarBinding with sourceField", async () => {
    const user = userEvent.setup()
    const node = typedNode("reg-1", [{ name: "draft", sourceNodeId: "script-1" }])
    const { onPatch } = renderTypedPanel(node, defaultTypedParams, scriptUpstream, {
      exprChannel: true,
    })

    await user.click(screen.getByRole("combobox", { name: "字段绑定 draft" }))
    await user.click(screen.getByRole("option", { name: "characterSheet" }))

    expect(onPatch).toHaveBeenCalledWith({
      varBindings: [
        { name: "draft", sourceNodeId: "script-1", sourceField: "characterSheet" },
      ],
    })
  })

  it("selecting 整个输出 clears sourceField (whole-output, back-compat)", async () => {
    const user = userEvent.setup()
    const node = typedNode("reg-1", [
      { name: "draft", sourceNodeId: "script-1", sourceField: "title" },
    ])
    const { onPatch } = renderTypedPanel(node, defaultTypedParams, scriptUpstream, {
      exprChannel: true,
    })

    await user.click(screen.getByRole("combobox", { name: "字段绑定 draft" }))
    await user.click(screen.getByRole("option", { name: "（整个输出）" }))

    expect(onPatch).toHaveBeenCalledWith({
      varBindings: [{ name: "draft", sourceNodeId: "script-1" }],
    })
  })

  it("changing the source node resets the field (no sourceField carried over)", async () => {
    const user = userEvent.setup()
    const node: WorkflowNode = {
      ...typedNode("reg-1", [
        { name: "draft", sourceNodeId: "script-1", sourceField: "title" },
      ]),
      dependsOn: ["script-1", "script-2"],
    }
    const upstream = [
      { id: "script-1", label: "剧本一", outputSchema: scriptOutputSchema },
      { id: "script-2", label: "剧本二", outputSchema: scriptOutputSchema },
    ]
    const { onPatch } = renderTypedPanel(node, defaultTypedParams, upstream, {
      exprChannel: true,
    })

    // combos[0] = upstream-node Select, combos[1] = field Select.
    const combos = screen.getAllByRole("combobox")
    await user.click(combos[0])
    await user.click(screen.getByRole("option", { name: "剧本二" }))

    // Switching node drops the dangling sourceField.
    expect(onPatch).toHaveBeenCalledWith({
      varBindings: [{ name: "draft", sourceNodeId: "script-2" }],
    })
  })

  it("capability gate: exprChannel=false disables the field picker + shows hint; node select still works", () => {
    const node = typedNode("reg-1", [{ name: "draft", sourceNodeId: "script-1" }])
    renderTypedPanel(node, defaultTypedParams, scriptUpstream, { exprChannel: false })

    const combos = screen.getAllByRole("combobox")
    expect(combos).toHaveLength(2)
    expect(combos[0]).not.toBeDisabled() // whole-node binding still usable
    const fieldCombo = screen.getByRole("combobox", { name: "字段绑定 draft" })
    expect(fieldCombo).toBeDisabled()
    expect(
      screen.getByText("字段级绑定需开启表达式通道（STUDIO_EXPR_CHANNEL）"),
    ).toBeInTheDocument()
  })

  it("capability gate: exprChannel=true enables the field picker + no hint", () => {
    const node = typedNode("reg-1", [{ name: "draft", sourceNodeId: "script-1" }])
    renderTypedPanel(node, defaultTypedParams, scriptUpstream, { exprChannel: true })

    expect(screen.getByRole("combobox", { name: "字段绑定 draft" })).not.toBeDisabled()
    expect(
      screen.queryByText("字段级绑定需开启表达式通道（STUDIO_EXPR_CHANNEL）"),
    ).not.toBeInTheDocument()
  })

  it("degenerate: upstream type with empty OutputSchema renders no field picker", () => {
    const node = typedNode("reg-1", [{ name: "draft", sourceNodeId: "script-1" }])
    renderTypedPanel(
      node,
      defaultTypedParams,
      [{ id: "script-1", label: "无 schema 节点", outputSchema: [] }],
      { exprChannel: true },
    )
    // Only the upstream-node Select; no field picker.
    expect(screen.getAllByRole("combobox")).toHaveLength(1)
    expect(screen.queryByRole("combobox", { name: "字段绑定 draft" })).not.toBeInTheDocument()
  })
})
