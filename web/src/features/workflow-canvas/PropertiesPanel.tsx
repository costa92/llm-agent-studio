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
import type { BasicPrompt, HttpParams, LlmParams, Prompt, WorkflowNode } from "@/lib/types"
import { defaultPromptIdFor } from "./canvasModel"
import { isCustomType, nodeDisplay } from "./nodeColor"

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
  // 当前节点 dependsOn 的上游节点列表（id + display label）。typed 节点变量绑定的候选 Select 来源。
  upstreamNodes?: { id: string; label: string }[]
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
  upstreamNodes = [],
}: PropertiesPanelProps) {
  const createPrompt = useCreatePrompt(org)
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
        <div className="flex flex-1 items-center justify-center text-center">
          <p className="text-[12px] text-text-3">选择一个节点查看属性</p>
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

  // typed 节点（node.typeId 非空 + 注入了 llm 或 http 参数）：展示参数摘要 + 变量绑定行。
  const isTypedLlm = !!(node.typeId && typedParams)
  const isTypedHttp = !!(node.typeId && typedHttpParams)
  const isTyped = isTypedLlm || isTypedHttp

  // typed 节点：从模板里提取唯一 {{name}} 令牌（llm 取 prompt；http 取 url+headers+body，排除 {{secret:...}}）。
  const templateVars = isTypedLlm
    ? extractTemplateVars(typedParams!.systemPrompt, typedParams!.userPrompt)
    : isTypedHttp
      ? extractHttpTemplateVars(typedHttpParams!)
      : []

  // varBindings 合并更新：按 name 替换/追加绑定，不清除其它已存在的绑定。
  // node 在此处一定非 null（早返回已在上方处理），断言为 WorkflowNode 避免 TS18047。
  function patchVarBinding(name: string, sourceNodeId: string) {
    const existing = (node as WorkflowNode).varBindings ?? []
    const updated = existing.filter((b) => b.name !== name)
    onPatch({ varBindings: [...updated, { name, sourceNodeId }] })
  }

  return (
    <aside className="flex w-64 shrink-0 flex-col gap-3 overflow-y-auto border-l border-line bg-bg-surface p-3">
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
            </SelectContent>
          </Select>
        </div>
      )}

      {/* typed 节点：只读参数摘要 + 变量绑定行。annotation/builtin 节点走下面的提示词选择块。 */}
      {isTyped && (
        <>
          {/* 只读参数摘要 */}
          <div className="flex flex-col gap-1 rounded border border-line/60 bg-bg-base p-2">
            <Label className="text-[10px] font-semibold uppercase tracking-wider text-text-3">
              类型参数（只读）
            </Label>
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
              {templateVars.map((name) => {
                const existing = node.varBindings?.find((b) => b.name === name)
                return (
                  <div key={name} className="flex flex-col gap-0.5">
                    <span className="text-[10px] text-text-3">
                      {`{{${name}}}`}
                    </span>
                    <Select
                      value={existing?.sourceNodeId ?? ""}
                      onValueChange={(val) => {
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
