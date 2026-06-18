import { z } from "zod"

// 返回第一处问题的中文描述,无问题返回 null。前端与后端 ValidateCustomGraph 同义,
// 让用户在保存前就看到「循环依赖」,而不是运行时才 400。
// 从 WorkflowDialog.tsx 原样迁来（实现与文案逐字不变）。
export function findGraphError(nodes: { id: string; dependsOn: string[] }[]): string | null {
  const ids = new Set(nodes.map((n) => n.id))
  for (const n of nodes) {
    for (const dep of n.dependsOn) {
      if (!ids.has(dep)) return `节点「${n.id}」依赖了不存在的节点「${dep}」`
    }
  }
  // DFS 三色环检测
  const deps = new Map(nodes.map((n) => [n.id, n.dependsOn]))
  const color = new Map<string, number>() // 0 white,1 gray,2 black
  let cycleMsg: string | null = null
  const visit = (id: string): boolean => {
    color.set(id, 1)
    for (const dep of deps.get(id) ?? []) {
      const c = color.get(dep) ?? 0
      if (c === 1) {
        cycleMsg = `工作流存在循环依赖:「${id}」→「${dep}」`
        return true
      }
      if (c === 0 && visit(dep)) return true
    }
    color.set(id, 2)
    return false
  }
  for (const n of nodes) {
    if ((color.get(n.id) ?? 0) === 0 && visit(n.id)) return cycleMsg
  }
  return null
}

// 工作流表单 schema。name + nodes（DAG）。校验经 superRefine 复刻原 WorkflowForm
// submit 的 4 条分支（顺序、文案逐字一致）：name trim 空 → 0 节点 → 遍历节点
//（空 id → 重复 id）→ findGraphError。每个分支 addIssue 后 return，与现状「首个问题即停」语义一致。
export const workflowFormSchema = z
  .object({
    name: z.string(),
    nodes: z.array(
      z.object({
        id: z.string(),
        type: z.string(),
        promptId: z.string(),
        promptText: z.string().optional(),
        dependsOn: z.array(z.string()),
      }),
    ),
  })
  .superRefine((v, ctx) => {
    if (!v.name.trim()) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["name"],
        message: "请输入工作流名称",
      })
      return
    }
    if (v.nodes.length === 0) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["nodes"],
        message: "工作流必须包含至少一个节点",
      })
      return
    }
    const ids = new Set<string>()
    for (const n of v.nodes) {
      if (!n.id) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["nodes"],
          message: "所有节点 ID 不能为空",
        })
        return
      }
      if (ids.has(n.id)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["nodes"],
          message: `存在重复的节点 ID: ${n.id}`,
        })
        return
      }
      ids.add(n.id)
    }
    const graphErr = findGraphError(v.nodes)
    if (graphErr) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["nodes"],
        message: graphErr,
      })
    }
  })

export type WorkflowFormValues = z.infer<typeof workflowFormSchema>
