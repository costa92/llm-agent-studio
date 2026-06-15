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
import type { BasicPrompt, Prompt, WorkflowNode } from "@/lib/types"

// 工作流 DAG 节点编辑器（受控）。从 EditProjectDialog 原样抽出：
// 添加节点 / 删除节点（级联清理 dependsOn）/ 节点 id 重命名（级联更新 dependsOn）/
// 任务类型选择 / script&storyboard 的提示词选择 / 依赖节点勾选。
// basics = 内置基础提示词（按节点类型过滤展示，置于 org 自建提示词之前）。
// org 非空时支持：按 kind 预选默认提示词、默认 tag 标注、行内新建提示词。
export interface WorkflowNodesEditorProps {
  nodes: WorkflowNode[]
  onChange: (nodes: WorkflowNode[]) => void
  prompts?: Prompt[]
  basics?: BasicPrompt[]
  org?: string
}

export function WorkflowNodesEditor({
  nodes,
  onChange,
  prompts,
  basics,
  org,
}: WorkflowNodesEditorProps) {
  const createPrompt = useCreatePrompt(org ?? "")
  // 行内新建提示词：creatingFor = 正在新建的节点 index（null = 无）。
  const [creatingFor, setCreatingFor] = useState<number | null>(null)
  const [newName, setNewName] = useState("")
  const [newContent, setNewContent] = useState("")
  const [savingNew, setSavingNew] = useState(false)
  // 行内自定义提示词（不入库）：customFor = 正在手输的节点 index。一旦节点的
  // promptText 非空，文本框也常驻显示（值即真源）。
  const [customFor, setCustomFor] = useState<number | null>(null)

  // 某 kind 的 org 默认提示词 id（无则空串）。
  const defaultPromptIdFor = (kind: string) =>
    prompts?.find((p) => p.kind === kind && p.isDefault)?.id ?? ""

  return (
    <div className="flex flex-col gap-3 border border-border rounded-lg p-3 bg-muted/20">
      <div className="flex items-center justify-between border-b border-border pb-2">
        <h4 className="text-[13px] font-semibold text-text-1">工作流节点配置</h4>
        <button
          type="button"
          className="text-[12px] text-amber hover:text-amber/80 font-medium border border-amber/30 hover:border-amber px-2.5 py-1 rounded transition-colors cursor-pointer"
          onClick={() => {
            const newId = `node-${nodes.length + 1}`
            onChange([
              ...nodes,
              {
                id: newId,
                type: "script",
                promptId: defaultPromptIdFor("script"),
                dependsOn: [],
              },
            ])
          }}
        >
          + 添加节点
        </button>
      </div>

      {nodes.length === 0 ? (
        <p className="text-[11.5px] text-text-3 text-center py-2">暂无步骤，点击上方“添加节点”开始配置</p>
      ) : (
        <div className="flex flex-col gap-4">
          {nodes.map((node, index) => (
            <div key={index} className="flex flex-col gap-2.5 p-3 border border-border rounded bg-card/40 relative">
              <button
                type="button"
                className="absolute top-2 right-2 text-text-3 hover:text-danger text-[11px] cursor-pointer"
                onClick={() => {
                  const updated = nodes.filter((_, i) => i !== index)
                  const cleaned = updated.map(n => ({
                    ...n,
                    dependsOn: n.dependsOn.filter(d => d !== node.id)
                  }))
                  onChange(cleaned)
                }}
              >
                删除
              </button>

              <div className="grid grid-cols-2 gap-2 mt-1">
                <div className="flex flex-col gap-1">
                  <Label className="text-[11px] text-text-2">节点 ID (英文标识)</Label>
                  <input
                    type="text"
                    className="h-8 rounded border border-input px-2 text-[12px] bg-background text-text-1 focus:ring-1 focus:ring-ring"
                    value={node.id}
                    onChange={(e) => {
                      const oldId = node.id
                      const newId = e.target.value.trim()
                      const updated = [...nodes]
                      updated[index] = { ...node, id: newId }
                      const renamed = updated.map(n => ({
                        ...n,
                        dependsOn: n.dependsOn.map(d => d === oldId ? newId : d)
                      }))
                      onChange(renamed)
                    }}
                    placeholder="e.g. script-1"
                  />
                </div>

                <div className="flex flex-col gap-1">
                  <Label className="text-[11px] text-text-2">任务类型</Label>
                  <Select
                    value={node.type}
                    onValueChange={(val) => {
                      const updated = [...nodes]
                      updated[index] = {
                        ...node,
                        type: val,
                        promptId: defaultPromptIdFor(val),
                      }
                      onChange(updated)
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
              </div>

              {(node.type === "script" || node.type === "storyboard") && (
                <div className="flex flex-col gap-1">
                  <Label className="text-[11px] text-text-2">系统提示词 (Prompt Library)</Label>
                  <Select
                    value={
                      node.promptText
                        ? "__custom__"
                        : node.promptId || "__default__"
                    }
                    onValueChange={(val) => {
                      // 选「＋ 新建提示词」：不改 promptId，打开本节点行内新建表单。
                      if (val === "__create__") {
                        setCreatingFor(index)
                        setNewName("")
                        setNewContent("")
                        return
                      }
                      // 选「＋ 自定义输入」：展开手输文本框（值写入 node.promptText）。
                      if (val === "__custom__") {
                        setCustomFor(index)
                        return
                      }
                      // 行内新建后 promptId 可能短暂无匹配 SelectItem（列表尚未刷新），
                      // 此时 Radix 会回吐空串 onValueChange——忽略它，避免覆盖刚设的 id。
                      if (val === "") return
                      // 选了库/内置/默认 → 清掉自定义文本，退出手输模式。
                      setCustomFor(null)
                      const updated = [...nodes]
                      updated[index] = {
                        ...node,
                        promptId: val === "__default__" ? "" : val,
                        promptText: "",
                      }
                      onChange(updated)
                    }}
                  >
                    <SelectTrigger className="h-8 text-[12px]">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="text-[12px]">
                      <SelectItem value="__default__">使用系统内置默认提示词</SelectItem>
                      <SelectItem value="__custom__">＋ 自定义输入（不入库）</SelectItem>
                      {org && (
                        <SelectItem value="__create__">＋ 新建提示词</SelectItem>
                      )}
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

                  {creatingFor === index && (
                    <div className="mt-1 flex flex-col gap-2 rounded border border-amber/30 bg-amber/5 p-2.5">
                      <div className="flex flex-col gap-1">
                        <Label
                          htmlFor={`inline-prompt-name-${index}`}
                          className="text-[11px] text-text-2"
                        >
                          名称
                        </Label>
                        <Input
                          id={`inline-prompt-name-${index}`}
                          className="h-8 text-[12px]"
                          value={newName}
                          onChange={(e) => setNewName(e.target.value)}
                          placeholder="提示词名称"
                        />
                      </div>
                      <div className="flex flex-col gap-1">
                        <Label
                          htmlFor={`inline-prompt-content-${index}`}
                          className="text-[11px] text-text-2"
                        >
                          内容
                        </Label>
                        <Textarea
                          id={`inline-prompt-content-${index}`}
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
                          className="text-[11px] text-text-3 hover:text-text-1 px-2 py-1"
                          onClick={() => setCreatingFor(null)}
                        >
                          取消
                        </button>
                        <button
                          type="button"
                          aria-label="保存新建提示词"
                          className="text-[11px] text-amber hover:text-amber/80 font-medium border border-amber/30 hover:border-amber px-2.5 py-1 rounded disabled:opacity-50"
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
                                const updated = [...nodes]
                                updated[index] = { ...node, promptId: created.id }
                                onChange(updated)
                                setCreatingFor(null)
                              })
                              .finally(() => setSavingNew(false))
                          }}
                        >
                          保存
                        </button>
                      </div>
                    </div>
                  )}

                  {(customFor === index ||
                    (node.promptText != null && node.promptText !== "")) && (
                    <div className="mt-1 flex flex-col gap-2 rounded border border-border bg-muted/20 p-2.5">
                      <div className="flex items-center justify-between">
                        <Label
                          htmlFor={`inline-prompt-text-${index}`}
                          className="text-[11px] text-text-2"
                        >
                          自定义提示词（仅用于本节点，不存入提示词库）
                        </Label>
                        <button
                          type="button"
                          aria-label="清除自定义提示词"
                          className="text-[11px] text-text-3 hover:text-text-1"
                          onClick={() => {
                            setCustomFor(null)
                            const updated = [...nodes]
                            updated[index] = { ...node, promptText: "" }
                            onChange(updated)
                          }}
                        >
                          清除
                        </button>
                      </div>
                      <Textarea
                        id={`inline-prompt-text-${index}`}
                        className="min-h-16 text-[12px]"
                        value={node.promptText ?? ""}
                        onChange={(e) => {
                          const updated = [...nodes]
                          // 自定义文本优先：写入 promptText 同时清空 promptId。
                          updated[index] = {
                            ...node,
                            promptText: e.target.value,
                            promptId: "",
                          }
                          onChange(updated)
                        }}
                        placeholder="直接输入本节点使用的系统提示词…"
                      />
                    </div>
                  )}
                </div>
              )}

              <div className="flex flex-col gap-1">
                <Label className="text-[11px] text-text-2">依赖节点 (在这些节点完成后执行)</Label>
                <div className="flex flex-wrap gap-x-3 gap-y-1 p-2 border border-border rounded bg-background/50">
                  {nodes
                    .filter((n) => n.id !== node.id && n.id !== "")
                    .map((otherNode) => {
                      const isChecked = node.dependsOn.includes(otherNode.id)
                      return (
                        <label key={otherNode.id} className="flex items-center gap-1.5 text-[11.5px] text-text-2 cursor-pointer select-none">
                          <input
                            type="checkbox"
                            className="rounded text-primary border-input focus:ring-0 cursor-pointer"
                            checked={isChecked}
                            onChange={(e) => {
                              const checked = e.target.checked
                              const updated = [...nodes]
                              if (checked) {
                                updated[index] = {
                                  ...node,
                                  dependsOn: [...node.dependsOn, otherNode.id],
                                }
                              } else {
                                updated[index] = {
                                  ...node,
                                  dependsOn: node.dependsOn.filter((d) => d !== otherNode.id),
                                }
                              }
                              onChange(updated)
                            }}
                          />
                          {otherNode.id || "未命名节点"}
                        </label>
                      )
                    })}
                  {nodes.filter((n) => n.id !== node.id && n.id !== "").length === 0 && (
                    <span className="text-[11px] text-text-3">无其他节点可作为依赖</span>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
