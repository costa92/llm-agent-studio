import { useState } from "react"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useCreatePrompt } from "@/features/prompt/api"
import { useModelConfigs } from "@/features/cost/api"
import { useOrgSecrets } from "@/features/org-secrets/api"
import type { BasicPrompt, HttpParams, LlmParams, Prompt, ScriptParams, WorkflowNode } from "@/lib/types"
import { defaultPromptIdFor } from "./canvasModel"
import { isCustomType, nodeDisplay } from "./nodeColor"
import { PropertiesForm } from "./PropertiesForm"
import type { NodeTypeDescription, OutputField } from "./nodeDescTypes"

// 字段选择器「整个输出」哨兵 value——Radix Select 禁止 value=""（mount 即抛），
// 故整输出用非空哨兵；写回时映射回空 sourceField（= 整输出 / 向后兼容）。
const WHOLE_OUTPUT = "__whole__"

// 属性面板（Phase 2）：选中单个节点时编辑其字段。行为逐字移植自
// features/projects/WorkflowNodesEditor.tsx——提示词选择哨兵
//（__default__/__custom__/__create__）、行内新建、type→promptId 重置、空串回吐守卫。
// 与旧编辑器不同处：依赖（dependsOn）不在此面板编辑——连线在画布上以「边」为唯一真源
// （见 canvasModel.toStudioNodes）；id 重命名的级联由画布的 onRename 处理（重键边）。

// 从模板字符串中提取所有唯一 {{name}} 令牌名。
// 跨 systemPrompt + userPrompt 两个模板合并去重（Set 保序）。
// 恶意 {{ 无对应 }} 不崩溃（仅 match 完整 {{name}} 形式）。
export function extractTemplateVars(
  systemPrompt: string | undefined,
  userPrompt: string,
): string[] {
  const re = /\{\{([^{}]+?)\}\}/g
  const seen = new Set<string>()
  const result: string[] = []
  const combined = (systemPrompt ?? "") + "\n" + userPrompt
  let m: RegExpExecArray | null
  while ((m = re.exec(combined)) !== null) {
    const name = m[1].trim()
    if (!seen.has(name)) {
      seen.add(name)
      result.push(name)
    }
  }
  return result
}

// http 类型的 {{name}} 令牌：跨 url + 所有 header 值 + bodyTemplate 合并去重。
// 排除 {{secret:NAME}} 引用（密钥不是工作流变量，不产生绑定行）。
export function extractHttpTemplateVars(params: HttpParams): string[] {
  const re = /\{\{([^{}]+?)\}\}/g
  const seen = new Set<string>()
  const result: string[] = []
  const combined = [
    params.url,
    ...Object.values(params.headers ?? {}),
    params.bodyTemplate ?? "",
  ].join("\n")
  let m: RegExpExecArray | null
  while ((m = re.exec(combined)) !== null) {
    const name = m[1].trim()
    if (name.startsWith("secret:")) continue
    if (!seen.has(name)) {
      seen.add(name)
      result.push(name)
    }
  }
  return result
}

// script 类型的 {{name}} 令牌：从 code 模板提取去重（与 llm/http 一致）。
export function extractScriptTemplateVars(params: ScriptParams): string[] {
  const re = /\{\{([^{}]+?)\}\}/g
  const seen = new Set<string>()
  const result: string[] = []
  let m: RegExpExecArray | null
  while ((m = re.exec(params.code ?? "")) !== null) {
    const name = m[1].trim()
    if (!seen.has(name)) {
      seen.add(name)
      result.push(name)
    }
  }
  return result
}

export interface PropertiesPanelProps {
  // 选中节点的 studio 字段（null = 未选中）。
  node: WorkflowNode | null
  prompts?: Prompt[]
  basics?: BasicPrompt[]
  org: string
  // 现有节点 id 集合（不含当前节点），用于重命名查重。
  otherIds: string[]
  // 改 studio 字段（type/promptId/promptText）——不含 id（id 走 onRename）。
  onPatch: (patch: Partial<WorkflowNode>) => void
  // 重命名当前节点 id → 级联重键边（画布实现）。
  onRename: (newId: string) => void
  onDelete: () => void
  // 自定义节点：打开编辑该类型的对话框（仅 isCustomType 节点时传入）。
  onEditType?: () => void
  // typed llm 节点（node.typeId 非空 + kind=llm）时：org 注册表中该 typeId 对应的 LlmParams。
  // 用于解析 {{name}} 令牌和展示只读参数摘要。annotation/非 llm 节点时为 undefined。
  typedParams?: LlmParams
  // typed http 节点（node.typeId 非空 + kind=http）时：对应的 HttpParams（由画布层注入）。
  // 与 typedParams 互斥——按 kind 取其一。
  typedHttpParams?: HttpParams
  // typed script 节点（node.typeId 非空 + kind=script）时：对应的 ScriptParams（由画布层注入）。
  // 与 typedParams/typedHttpParams 互斥——按 kind 取其一。
  typedScriptParams?: ScriptParams
  // 当前节点 dependsOn 的上游节点列表（id + display label + 该上游类型的 OutputSchema）。
  // typed 节点变量绑定的候选 Select 来源；outputSchema 由画布层按上游 node.type 从 node-types
  // 目录解析注入（P5 字段级绑定的字段候选——安全上只列 OutputSchema 字段名，绝不列 secret/params）。
  upstreamNodes?: { id: string; label: string; outputSchema?: OutputField[] }[]
  // P5：ExprChannel 能力旗标。OFF 时字段级绑定运行期 fail-closed，故前端禁用字段选择器
  // （primary UX gate，§12 amendment 1）；整输出绑定不受影响。缺省 false。
  exprChannel?: boolean
  // typed 节点的注册表 NodeTypeDescription（由画布层按 node.type 解析注入）。
  // 提供时，类型参数改由通用 <PropertiesForm> 渲染并可编辑（P-write-4）：onChange
  // 经 onPatch({ parameters, typeVersion }) 持久化到节点 envelope——parameters/
  // typeVersion 是 WorkflowNode 上的扁平键，与 onPatch 兼容。危险/RegistryOnly 字段
  // 在后端 resolve 层 default-deny + save 时校验拒绝。缺省时回退到手写的逐 kind 摘要 JSX。
  description?: NodeTypeDescription
}

export function PropertiesPanel({
  node,
  prompts,
  basics,
  org,
  otherIds,
  onPatch,
  onRename,
  onDelete,
  onEditType,
  typedParams,
  typedHttpParams,
  typedScriptParams,
  upstreamNodes = [],
  description,
  exprChannel = false,
}: PropertiesPanelProps) {
  const createPrompt = useCreatePrompt(org)
  // P5.1：resourceLocator/secret 数据源。modelOptions ← org 的 model-config 列表
  // （resourceLocator dataSource="model" 的选项）；secretNames ← org 密钥的 NAME（DTO 永不含 value）。
  // 仅当渲染可编辑 PropertiesForm（typed + description）时才用到；hook 始终调用以遵守 hooks 规则。
  const modelConfigs = useModelConfigs(org)
  // 仅文本模型可注入 LLM 节点的 model resourceLocator——图像/音频模型对文本生成无意义。
  // 白名单 kind==="text"（对齐 useOrgTextModels 的口径），稳健于 audio/speech 标签差异。
  const modelOptions = (modelConfigs.data ?? [])
    .filter((m) => m.kind === "text")
    .map((m) => ({
      value: m.model,
      label: `${m.provider} · ${m.model}`,
    }))
  const orgSecrets = useOrgSecrets(org)
  const secretNames = (orgSecrets.data ?? []).map((s) => s.name)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState("")
  const [newContent, setNewContent] = useState("")
  const [savingNew, setSavingNew] = useState(false)
  const [custom, setCustom] = useState(false)
  // 行内 id 草稿：允许用户临时输入空/重复值并显示错误，仅在合法时提交重命名。
  const [idDraft, setIdDraft] = useState<string | null>(null)

  if (!node) {
    return (
      <aside className="flex w-64 shrink-0 flex-col gap-3 border-l border-line bg-bg-surface p-3">
        <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
          属性
        </h4>
        <div className="flex flex-1 flex-col items-center justify-center gap-1.5 px-4 text-center">
          <p className="text-[13px] text-text-2">未选择节点</p>
          <p className="text-[12px] leading-relaxed text-text-3">
            点击画布中的节点，在此查看并编辑它的配置
          </p>
        </div>
      </aside>
    )
  }

  const idValue = idDraft ?? node.id
  const trimmed = idValue.trim()
  const idError =
    trimmed === ""
      ? "节点 ID 不能为空"
      : otherIds.includes(trimmed)
        ? `存在重复的节点 ID: ${trimmed}`
        : null

  const showPrompt = node.type === "script" || node.type === "storyboard"

  // typed 节点（node.typeId 非空 + 注入了 llm/http/script 参数）：展示参数摘要 + 变量绑定行。
  const isTypedLlm = !!(node.typeId && typedParams)
  const isTypedHttp = !!(node.typeId && typedHttpParams)
  const isTypedScript = !!(node.typeId && typedScriptParams)
  const isTyped = isTypedLlm || isTypedHttp || isTypedScript

  // typed 节点：从模板里提取唯一 {{name}} 令牌（llm 取 prompt；http 取 url+headers+body，排除 {{secret:...}}；script 取 code）。
  const templateVars = isTypedLlm
    ? extractTemplateVars(typedParams!.systemPrompt, typedParams!.userPrompt)
    : isTypedHttp
      ? extractHttpTemplateVars(typedHttpParams!)
      : isTypedScript
        ? extractScriptTemplateVars(typedScriptParams!)
        : []

  // 未绑定变量：模板里声明了 {{name}} 却没绑到任何上游节点的令牌（排除运行期输入
  // {{input:...}} 与密钥 {{secret:...}}——它们不走节点变量绑定通道）。运行期这类
  // 令牌不会被替换（worker 见 SourceTodoId 为空即跳过，原样残留在提示词里），run 仍报
  // valid:true 静默跑，用户无从察觉——故在此做软告警，提示先补齐绑定。
  const unboundVars = templateVars.filter((name) => {
    if (name.startsWith("input:") || name.startsWith("secret:")) return false
    const b = node.varBindings?.find((x) => x.name === name)
    return !b?.sourceNodeId
  })

  // varBindings 合并更新：按 name 替换/追加绑定，不清除其它已存在的绑定。
  // node 在此处一定非 null（早返回已在上方处理），断言为 WorkflowNode 避免 TS18047。
  function patchVarBinding(name: string, sourceNodeId: string, sourceField?: string) {
    const existing = (node as WorkflowNode).varBindings ?? []
    const updated = existing.filter((b) => b.name !== name)
    const binding: { name: string; sourceNodeId: string; sourceField?: string } = {
      name,
      sourceNodeId,
    }
    // 仅在非空时携带 sourceField——空 = 整输出（向后兼容，与今天逐字节一致）。
    const field = sourceField?.trim()
    if (field) binding.sourceField = field
    onPatch({ varBindings: [...updated, binding] })
  }

  return (
    <aside className="flex w-64 shrink-0 flex-col gap-3 overflow-y-auto border-l border-line bg-bg-surface p-3 pb-6">
      <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
        属性
      </h4>

      {/* 节点 ID（重命名级联到边） */}
      <div className="flex flex-col gap-1">
        <Label className="text-[11px] text-text-2">节点 ID (英文标识)</Label>
        <input
          type="text"
          className="h-8 rounded border border-input bg-background px-2 text-[12px] text-text-1 focus:ring-1 focus:ring-ring"
          value={idValue}
          onChange={(e) => setIdDraft(e.target.value)}
          onBlur={() => {
            if (!idError && trimmed !== node.id) onRename(trimmed)
            setIdDraft(null)
          }}
          placeholder="e.g. script-1"
        />
        {idError && <p className="text-[11px] text-danger">{idError}</p>}
      </div>

      {/* 任务类型：自定义节点只读展示；内置节点保留 Select。 */}
      {isCustomType(node.type) ? (
        <div className="flex flex-col gap-1">
          <Label className="text-[11px] text-text-2">类型</Label>
          <div className="flex items-center gap-2">
            <span
              className="inline-block h-3 w-3 shrink-0 rounded-full"
              style={{ backgroundColor: nodeDisplay(node).color }}
            />
            <span className="text-[12px] text-text-1">{nodeDisplay(node).label}</span>
            {isTyped && (
              <span className="rounded bg-amber/20 px-1 text-[10px] font-medium text-amber">
                typed
              </span>
            )}
          </div>
          {onEditType && !isTyped && (
            <button
              type="button"
              className="mt-1 self-start rounded border border-line px-2.5 py-1 text-[11px] text-text-2 hover:border-amber hover:text-amber"
              onClick={onEditType}
            >
              编辑类型
            </button>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-1">
          <Label className="text-[11px] text-text-2">任务类型</Label>
          <Select
            value={node.type}
            onValueChange={(val) => {
              setCustom(false)
              onPatch({
                type: val,
                promptId: defaultPromptIdFor(prompts, val),
                promptText: "",
              })
            }}
          >
            <SelectTrigger className="h-8 text-[12px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="text-[12px]">
              <SelectItem value="script">剧本生成 (script)</SelectItem>
              <SelectItem value="storyboard">分镜拆解 (storyboard)</SelectItem>
              <SelectItem value="asset">生成资源 (asset)</SelectItem>
              <SelectItem value="prescreen">预审 (prescreen)</SelectItem>
            </SelectContent>
          </Select>
        </div>
      )}

      {/* typed 节点：参数摘要（有描述则经 PropertiesForm 可编辑，否则只读手写摘要）+ 变量绑定行。annotation/builtin 节点走下面的提示词选择块。 */}
      {isTyped && (
        <>
          {/* 参数摘要：description 存在 → 可编辑表单；缺省 → 只读摘要回退 */}
          <div className="flex flex-col gap-1 rounded border border-line/60 bg-bg-base p-2">
            <Label className="text-[10px] font-semibold uppercase tracking-wider text-text-3">
              {description ? "类型参数" : "类型参数（只读）"}
            </Label>
            {description ? (
              <PropertiesForm
                description={description}
                value={
                  ((node as WorkflowNode).parameters ??
                    (isTypedLlm
                      ? typedParams
                      : isTypedHttp
                        ? typedHttpParams
                        : typedScriptParams)) as Record<string, unknown>
                }
                onChange={(next) =>
                  onPatch({ parameters: next, typeVersion: description.version })
                }
                secretNames={secretNames}
                modelOptions={modelOptions}
              />
            ) : (
              <>
            {isTypedLlm && (
              <>
                {typedParams!.systemPrompt && (
                  <div className="flex flex-col gap-0.5">
                    <span className="text-[10px] text-text-3">系统提示词</span>
                    <span className="line-clamp-2 text-[11px] text-text-2">{typedParams!.systemPrompt}</span>
                  </div>
                )}
                <div className="flex flex-col gap-0.5">
                  <span className="text-[10px] text-text-3">用户提示词模板</span>
                  <span className="line-clamp-2 text-[11px] text-text-2">{typedParams!.userPrompt}</span>
                </div>
                {typedParams!.outputFormat && (
                  <div className="flex items-center gap-1">
                    <span className="text-[10px] text-text-3">输出格式</span>
                    <span className="text-[11px] text-text-2">{typedParams!.outputFormat}</span>
                  </div>
                )}
              </>
            )}
            {isTypedHttp && (
              <>
                <div className="flex items-center gap-1">
                  <span className="text-[10px] text-text-3">请求</span>
                  <span className="text-[11px] text-text-2 font-mono">{typedHttpParams!.method}</span>
                </div>
                <div className="flex flex-col gap-0.5">
                  <span className="text-[10px] text-text-3">URL</span>
                  <span className="line-clamp-2 text-[11px] text-text-2 break-all">{typedHttpParams!.url}</span>
                </div>
                {typedHttpParams!.outputFormat && (
                  <div className="flex items-center gap-1">
                    <span className="text-[10px] text-text-3">输出格式</span>
                    <span className="text-[11px] text-text-2">{typedHttpParams!.outputFormat}</span>
                  </div>
                )}
              </>
            )}
            {isTypedScript && (
              <>
                <div className="flex flex-col gap-0.5">
                  <span className="text-[10px] text-text-3">脚本代码（Starlark）</span>
                  <span className="line-clamp-2 text-[11px] text-text-2 font-mono break-all">{typedScriptParams!.code}</span>
                </div>
                {typedScriptParams!.outputFormat && (
                  <div className="flex items-center gap-1">
                    <span className="text-[10px] text-text-3">输出格式</span>
                    <span className="text-[11px] text-text-2">{typedScriptParams!.outputFormat}</span>
                  </div>
                )}
              </>
            )}
              </>
            )}
            <button
              type="button"
              className="mt-1 self-start rounded border border-line px-2 py-0.5 text-[10px] text-text-3 hover:border-amber hover:text-amber"
              onClick={onEditType}
            >
              编辑类型
            </button>
          </div>

          {/* 变量绑定行：每个 {{name}} 一行，Select 候选 = dependsOn 上游节点。 */}
          {templateVars.length > 0 && (
            <div className="flex flex-col gap-2">
              <Label className="text-[11px] text-text-2">变量绑定</Label>
              {/* 未绑定变量软告警：不阻断保存/运行，但提示这些 {{name}} 运行期不会被替换。 */}
              {unboundVars.length > 0 && (
                <p
                  role="alert"
                  className="rounded border border-amber/40 bg-amber/10 px-2 py-1 text-[10px] text-amber"
                >
                  {`${unboundVars.length} 个变量未绑定：`}
                  {unboundVars.map((n) => `{{${n}}}`).join("、")}
                  ，运行时不会被替换，请先绑定上游节点。
                </p>
              )}
              {templateVars.map((name) => {
                const existing = node.varBindings?.find((b) => b.name === name)
                // 选中上游节点的 OutputSchema 字段（字段级绑定候选）。仅当已选上游节点
                // 且该类型声明了 OutputSchema 时渲染字段选择器。
                const selectedUpstream = existing?.sourceNodeId
                  ? upstreamNodes.find((u) => u.id === existing.sourceNodeId)
                  : undefined
                const outputFields = selectedUpstream?.outputSchema ?? []
                return (
                  <div key={name} className="flex flex-col gap-0.5">
                    <span className="text-[10px] text-text-3">
                      {`{{${name}}}`}
                    </span>
                    <Select
                      value={existing?.sourceNodeId ?? ""}
                      onValueChange={(val) => {
                        // 换上游节点 → 清空 sourceField（字段属于具体上游类型，换节点字段失效）。
                        if (val) patchVarBinding(name, val)
                      }}
                    >
                      <SelectTrigger className="h-7 text-[11px]">
                        <SelectValue placeholder="选择上游节点" />
                      </SelectTrigger>
                      <SelectContent className="text-[11px]">
                        {upstreamNodes.length === 0 ? (
                          // Radix Select throws on an empty-string value=""; render a
                          // plain non-item hint instead (Blocker 1).
                          <div className="px-2 py-1.5 text-[11px] text-text-3">
                            先连接上游节点
                          </div>
                        ) : (
                          upstreamNodes.map((u) => (
                            <SelectItem key={u.id} value={u.id}>
                              {u.label}
                            </SelectItem>
                          ))
                        )}
                      </SelectContent>
                    </Select>
                    {/* P5 字段选择器：候选 ONLY 来自上游 OutputSchema 字段名（绝不列 secret/params）。
                        ExprChannel OFF → disabled + 提示（primary UX gate）。整输出仍可用。 */}
                    {outputFields.length > 0 && (
                      <>
                        <Select
                          value={existing?.sourceField || WHOLE_OUTPUT}
                          disabled={!exprChannel}
                          onValueChange={(val) => {
                            // 哨兵 __whole__ → 清空 sourceField（整输出）；否则绑该字段。
                            patchVarBinding(
                              name,
                              existing!.sourceNodeId,
                              val === WHOLE_OUTPUT ? "" : val,
                            )
                          }}
                        >
                          <SelectTrigger
                            className="h-7 text-[11px]"
                            aria-label={`字段绑定 ${name}`}
                          >
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent className="text-[11px]">
                            <SelectItem value={WHOLE_OUTPUT}>（整个输出）</SelectItem>
                            {outputFields.map((f) => (
                              <SelectItem key={f.name} value={f.name}>
                                {f.name}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        {!exprChannel && (
                          <span className="text-[10px] text-text-3">
                            字段级绑定需开启表达式通道（STUDIO_EXPR_CHANNEL）
                          </span>
                        )}
                      </>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </>
      )}

      {/* 提示词选择（仅内置 script/storyboard，typed 节点跳过） */}
      {!isCustomType(node.type) && showPrompt && (
        <div className="flex flex-col gap-1">
          <Label className="text-[11px] text-text-2">
            系统提示词 (Prompt Library)
          </Label>
          <Select
            value={node.promptText ? "__custom__" : node.promptId || "__default__"}
            onValueChange={(val) => {
              if (val === "__create__") {
                setCreating(true)
                setNewName("")
                setNewContent("")
                return
              }
              if (val === "__custom__") {
                setCustom(true)
                return
              }
              // 行内新建后 promptId 可能短暂无匹配 SelectItem，Radix 回吐空串——忽略。
              if (val === "") return
              setCustom(false)
              onPatch({
                promptId: val === "__default__" ? "" : val,
                promptText: "",
              })
            }}
          >
            <SelectTrigger className="h-8 text-[12px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent className="text-[12px]">
              <SelectItem value="__default__">使用系统内置默认提示词</SelectItem>
              <SelectItem value="__custom__">＋ 自定义输入（不入库）</SelectItem>
              {org && <SelectItem value="__create__">＋ 新建提示词</SelectItem>}
              {basics
                ?.filter((b) => b.kind === node.type)
                .map((b) => (
                  <SelectItem key={b.id} value={b.id}>
                    {b.name}（基础）
                  </SelectItem>
                ))}
              {prompts
                ?.filter((p) => p.kind === node.type || p.kind === "")
                .map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.name} ({p.style || "通用"})
                    {p.isDefault && p.kind === node.type ? " · 默认" : ""}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>

          {creating && (
            <div className="mt-1 flex flex-col gap-2 rounded border border-amber/30 bg-amber/5 p-2.5">
              <div className="flex flex-col gap-1">
                <Label
                  htmlFor="inline-prompt-name"
                  className="text-[11px] text-text-2"
                >
                  名称
                </Label>
                <Input
                  id="inline-prompt-name"
                  className="h-8 text-[12px]"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="提示词名称"
                />
              </div>
              <div className="flex flex-col gap-1">
                <Label
                  htmlFor="inline-prompt-content"
                  className="text-[11px] text-text-2"
                >
                  内容
                </Label>
                <Textarea
                  id="inline-prompt-content"
                  className="min-h-16 text-[12px]"
                  value={newContent}
                  onChange={(e) => setNewContent(e.target.value)}
                  placeholder="提示词内容"
                />
              </div>
              <div className="flex items-center justify-end gap-2">
                <button
                  type="button"
                  aria-label="取消新建提示词"
                  className="px-2 py-1 text-[11px] text-text-3 hover:text-text-1"
                  onClick={() => setCreating(false)}
                >
                  取消
                </button>
                <button
                  type="button"
                  aria-label="保存新建提示词"
                  className="rounded border border-amber/30 px-2.5 py-1 text-[11px] font-medium text-amber hover:border-amber hover:text-amber/80 disabled:opacity-50"
                  disabled={savingNew || !newName.trim() || !newContent.trim()}
                  onClick={() => {
                    if (!org) return
                    setSavingNew(true)
                    void createPrompt
                      .mutateAsync({
                        name: newName.trim(),
                        content: newContent.trim(),
                        style: "",
                        kind: node.type,
                      })
                      .then((created) => {
                        onPatch({ promptId: created.id, promptText: "" })
                        setCreating(false)
                      })
                      .finally(() => setSavingNew(false))
                  }}
                >
                  保存
                </button>
              </div>
            </div>
          )}

          {(custom || (node.promptText != null && node.promptText !== "")) && (
            <div className="mt-1 flex flex-col gap-2 rounded border border-border bg-muted/20 p-2.5">
              <div className="flex items-center justify-between">
                <Label
                  htmlFor="inline-prompt-text"
                  className="text-[11px] text-text-2"
                >
                  自定义提示词（仅用于本节点，不存入提示词库）
                </Label>
                <button
                  type="button"
                  aria-label="清除自定义提示词"
                  className="text-[11px] text-text-3 hover:text-text-1"
                  onClick={() => {
                    setCustom(false)
                    onPatch({ promptText: "" })
                  }}
                >
                  清除
                </button>
              </div>
              <Textarea
                id="inline-prompt-text"
                className="min-h-16 text-[12px]"
                value={node.promptText ?? ""}
                onChange={(e) =>
                  // 自定义文本优先：写入 promptText 同时清空 promptId。
                  onPatch({ promptText: e.target.value, promptId: "" })
                }
                placeholder="直接输入本节点使用的系统提示词…"
              />
            </div>
          )}
        </div>
      )}

      <button
        type="button"
        className="mt-1 rounded-md border border-line px-2.5 py-1.5 text-[12px] text-danger hover:border-danger"
        onClick={onDelete}
      >
        删除节点
      </button>
    </aside>
  )
}
