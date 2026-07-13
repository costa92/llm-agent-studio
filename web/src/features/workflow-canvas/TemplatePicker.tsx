import { useMemo } from "react"
import { LayoutTemplate } from "lucide-react"
import { toast } from "sonner"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog"
import {
  useWorkflowTemplates,
  useInstantiateTemplate,
} from "@/features/projects/workflowTemplateApi"
import type { WorkflowTemplateMeta } from "@/lib/types"

// 工作流案例模板选择器：列出后端内置案例模板（按 group 分组），点卡片经
// from-template 端点实例化一条新工作流，成功后 onCreated(wf.id) 交给画布导航打开。
// 本文件只 export 组件函数（lint 铁律 react-refresh/only-export-components）。

export interface TemplatePickerProps {
  org: string
  projectId: string
  // 从模板成功创建后回调新工作流 id（由画布层导航到 ?wf=<id> 打开）。
  onCreated: (wfId: string) => void
  // 取消 / 关闭对话框。
  onCancel: () => void
}

export function TemplatePicker({
  org,
  projectId,
  onCreated,
  onCancel,
}: TemplatePickerProps) {
  const { data: templates = [], isLoading, isError } = useWorkflowTemplates(org)
  const instantiate = useInstantiateTemplate(projectId)

  // 纯派生：按 group 分组、保持首次出现顺序（禁 effect setState）。
  const groups = useMemo(() => {
    const order: string[] = []
    const byGroup = new Map<string, WorkflowTemplateMeta[]>()
    for (const t of templates) {
      const g = t.group || "其他"
      if (!byGroup.has(g)) {
        byGroup.set(g, [])
        order.push(g)
      }
      byGroup.get(g)!.push(t)
    }
    return order.map((g) => ({ group: g, items: byGroup.get(g)! }))
  }, [templates])

  const pick = (templateId: string) => {
    if (instantiate.isPending) return
    instantiate.mutate(
      { templateId },
      {
        onSuccess: (wf) => onCreated(wf.id),
        onError: () => toast.error("从模板创建工作流失败，请重试"),
      },
    )
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onCancel() }}>
      <DialogContent className="flex max-h-[86vh] w-full max-w-[min(94vw,720px)] flex-col gap-0 bg-bg-surface p-0 sm:max-w-[min(94vw,720px)]">
        <DialogHeader className="flex-row items-center gap-2 space-y-0 border-b border-line px-5 py-4">
          <LayoutTemplate className="h-[18px] w-[18px] text-amber" />
          <div className="flex flex-col gap-0.5">
            <DialogTitle className="text-[15px]">从模板开始</DialogTitle>
            <DialogDescription className="text-[12px] text-text-3">
              选一个案例模板，一键创建可运行的工作流
            </DialogDescription>
          </div>
        </DialogHeader>

        <div className="overflow-auto px-5 py-4">
          {isLoading && (
            <p className="py-6 text-center text-[12.5px] text-text-3">加载模板…</p>
          )}
          {isError && (
            <p className="py-6 text-center text-[12.5px] text-danger">
              模板加载失败
            </p>
          )}
          {!isLoading && !isError && templates.length === 0 && (
            <p className="py-6 text-center text-[12.5px] text-text-3">暂无可用模板</p>
          )}

          <div className="flex flex-col gap-5">
            {groups.map(({ group, items }) => (
              <section key={group} className="flex flex-col gap-2">
                <h5 className="text-[10px] font-semibold uppercase tracking-wider text-text-3">
                  {group}
                </h5>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                  {items.map((t) => (
                    <button
                      key={t.id}
                      type="button"
                      data-slot="template-card"
                      disabled={instantiate.isPending}
                      onClick={() => pick(t.id)}
                      className="flex flex-col items-start gap-1 rounded-md border border-line bg-bg-base px-3 py-2.5 text-left hover:border-amber disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      <span className="text-[13px] font-medium text-text-1">
                        {t.name}
                      </span>
                      {t.description && (
                        <span className="line-clamp-2 text-[11.5px] leading-snug text-text-3">
                          {t.description}
                        </span>
                      )}
                    </button>
                  ))}
                </div>
              </section>
            ))}
          </div>

          {instantiate.isPending && (
            <p className="mt-4 text-center text-[12px] text-text-3">正在创建工作流…</p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
