import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { ApiError } from "@/lib/apiClient"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import {
  useProject,
  usePlans,
} from "@/features/workflow/api"
import { ProjectRunsTable } from "@/features/workflow/ProjectRunsTable"
import { useDeleteProject, useUpdateProject, usePromptStyles } from "@/features/projects/api"
import { useRole } from "@/app/rbac"
import {
  useWorkflows,
  useRunWorkflow,
  useDeleteWorkflow,
} from "@/features/projects/workflowApi"
import { useOrgTextModels, useOrgImageModels } from "@/features/cost/api"
import { useStorageConfigs } from "@/features/storage/api"
import { DeleteProjectDialog } from "@/features/projects/DeleteProjectDialog"
import { EditProjectDialog } from "@/features/projects/EditProjectDialog"
import { RunInputsDialog } from "@/features/workflow/RunInputsDialog"
import { statusLabel, statusVariant } from "@/features/projects/status"
import type { InputField, ProjectStatus, Workflow } from "@/lib/types"

export const Route = createFileRoute("/_authed/orgs/$org/projects/$id/")({
  component: RunsListPage,
})

function RunsListPage() {
  const { org, id } = Route.useParams()
  const navigate = useNavigate()
  const projectQuery = useProject(id)
  const project = projectQuery.data

  const plansQuery = usePlans(id)

  // 运行期输入弹窗：工作流 inputsSchema 非空则先弹表单，填完再 run；
  // 无 schema 直接 run（零回归）。把「填完后做什么」装进 submit 闭包。
  const [runDialog, setRunDialog] = useState<{
    schema: InputField[]
    title: string
    submit: (inputs: Record<string, unknown>) => Promise<void>
  } | null>(null)

  // M5.1/M9: 项目级"编辑模型配置"。
  const updateProject = useUpdateProject(org)
  // 删除项目（admin-only 探针可见；后端 roleAdmin 强制）。成功后导航回项目列表。
  const deleteProject = useDeleteProject(org)
  const role = useRole(org)
  const textModelsQuery = useOrgTextModels(org)
  const imageModelsQuery = useOrgImageModels(org)
  const stylesQuery = usePromptStyles()
  const storageConfigsQuery = useStorageConfigs(org)

  // 工作流：一个项目可有多条 DAG 工作流，各自独立运行。
  const workflowsQuery = useWorkflows(id)
  const workflows = workflowsQuery.data || []
  const runWorkflow = useRunWorkflow(id)
  const deleteWorkflow = useDeleteWorkflow(id)

  // 工作流 run 入口：inputsSchema 非空 → 先弹运行期表单；空 → 直接跑（零回归）。
  function handleRunWorkflow(wf: Workflow) {
    if (wf.inputsSchema && wf.inputsSchema.length > 0) {
      setRunDialog({
        schema: wf.inputsSchema,
        title: `运行工作流「${wf.name}」`,
        submit: (inputs) => doRunWorkflow(wf.id, inputs),
      })
      return
    }
    void doRunWorkflow(wf.id)
  }

  async function doRunWorkflow(wfId: string, inputs?: Record<string, unknown>) {
    try {
      const res = await runWorkflow.mutateAsync({ wfId, inputs })
      if (res.fallbackUsed) {
        toast.warning("工作流校验未通过，已回落默认管线")
      } else {
        toast.success("已开始运行")
      }
      // Phase 3：运行成功后直接进画布运行模式（res 带 workflowId + planId）。
      void navigate({
        to: "/orgs/$org/projects/$id/workflow",
        params: { org, id },
        search: { wf: res.workflowId, run: res.planId },
      })
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        toast.error("配额已用尽，请稍后再试")
        return
      }
      if (err instanceof ApiError && err.status === 400) {
        const msg = err.body.replace(/^invalid workflow:\s*/i, "").replace(/^custom workflow:\s*/i, "").trim()
        toast.error(msg || "工作流配置无效")
        return
      }
      toast.error("运行失败")
    }
  }

  async function handleDeleteWorkflow(wfId: string, name: string) {
    if (!window.confirm(`确认删除工作流「${name}」？此操作不可撤销。`)) return
    try {
      await deleteWorkflow.mutateAsync(wfId)
      toast.success("工作流已删除")
    } catch {
      toast.error("删除失败")
    }
  }

  if (projectQuery.isLoading || plansQuery.isLoading) {
    return (
      <div className="p-6 space-y-4">
        <Skeleton className="h-[40px] w-[200px]" />
        <Skeleton className="h-[150px] w-full rounded-xl" />
        <Skeleton className="h-[200px] w-full rounded-xl" />
      </div>
    )
  }

  if (projectQuery.isError || project == null) {
    return (
      <div className="grid h-full place-items-center text-text-2">
        <p>项目加载失败</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full overflow-y-auto bg-bg-surface p-6 sm:p-8">
      {/* Header */}
      <header className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-line pb-6 mb-8">
        <div>
          <div className="flex items-center gap-2 mb-1">
            <button
              type="button"
              onClick={() => void navigate({ to: "/orgs/$org/projects", params: { org } })}
              className="text-[12px] text-text-3 hover:text-text-1"
            >
              项目列表
            </button>
            <span className="text-[12px] text-text-3">/</span>
            <span className="text-[12px] text-text-2 font-semibold">项目详情</span>
          </div>
          <h1 className="text-2xl font-bold text-text-1 font-heading">{project.name}</h1>
          {/* M5.1/M9: 展示当前项目的规划和图片模型 + 编辑入口。 */}
          <p className="mt-1 text-[12.5px] text-text-3">
            规划模型：<b className="text-text-1">
              {project.plannerProvider && project.plannerModel
                ? `${project.plannerProvider} · ${project.plannerModel}`
                : "组织默认"}
            </b>
            {" · "}
            图片模型：<b className="text-text-1">
              {project.imageProvider && project.imageModel
                ? `${project.imageProvider} · ${project.imageModel}`
                : "组织默认"}
            </b>
            {" · "}
            <EditProjectDialog
              trigger={
                <button className="underline underline-offset-2 hover:text-text-1">
                  编辑项目
                </button>
              }
              project={project}
              textModels={textModelsQuery.data}
              imageModels={imageModelsQuery.data}
              styles={stylesQuery.data}
              storageConfigs={storageConfigsQuery.data}
              onSubmit={(input) =>
                updateProject.mutateAsync({ id: project.id, ...input })
              }
              onSuccess={() => toast.success("项目信息已更新")}
            />
          </p>
        </div>
        {/* 删除项目：admin 探针可见（后端 roleAdmin 强制）；输入项目名确认，
            成功后回项目列表（该项目一切路由此后 404）。 */}
        {role.isAdmin && (
          <div className="shrink-0">
            <DeleteProjectDialog
              project={project}
              onSubmit={() => deleteProject.mutateAsync({ id: project.id })}
              onSuccess={() => {
                toast.success("项目已删除")
                void navigate({ to: "/orgs/$org/projects", params: { org } })
              }}
              trigger={
                <Button variant="ghost" className="text-danger">
                  删除项目
                </Button>
              }
            />
          </div>
        )}
      </header>

      {/* 工作流 section：一个项目可有多条 DAG 工作流，各自独立运行。 */}
      <section className="bg-bg-surface border border-line rounded-xl p-5 shadow-sm mb-8">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-xs font-semibold tracking-wider text-text-3 uppercase">工作流</h3>
          <Button
            variant="amber"
            onClick={() =>
              void navigate({
                to: "/orgs/$org/projects/$id/workflow",
                params: { org, id },
                search: { mode: "create" },
              })
            }
          >
            新建工作流
          </Button>
        </div>

        {workflowsQuery.isError ? (
          <div className="flex flex-col items-center justify-center gap-3 text-center py-8">
            <p className="text-sm text-text-2">工作流加载失败</p>
            <Button variant="ghost" onClick={() => void workflowsQuery.refetch()}>
              重试
            </Button>
          </div>
        ) : workflows.length === 0 ? (
          <div className="flex flex-col items-center justify-center text-center py-8">
            <p className="text-sm text-text-2 mb-1 font-semibold">暂无工作流</p>
            <p className="text-xs text-text-3 max-w-xs">
              点击右上角“新建工作流”，按手动配置的 DAG 节点和依赖关系执行。
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            {/* min-width 让窄屏（375px）真的横向滚动而非挤压表头/裁掉行内操作。 */}
            <table className="w-full min-w-[640px] text-left border-collapse">
              <thead>
                <tr className="border-b border-line text-xs text-text-3">
                  <th className="pb-3 font-semibold whitespace-nowrap">名称</th>
                  <th className="pb-3 font-semibold whitespace-nowrap">节点数</th>
                  <th className="pb-3 font-semibold whitespace-nowrap">最近运行</th>
                  <th className="pb-3 font-semibold text-right whitespace-nowrap">操作</th>
                </tr>
              </thead>
              <tbody>
                {workflows.map((wf) => (
                  <tr key={wf.id} className="border-b border-line/50 hover:bg-bg-surface/30 transition-colors text-sm text-text-1">
                    <td className="py-3 font-semibold">{wf.name}</td>
                    <td className="py-3 text-text-2">{wf.nodes.length}</td>
                    <td className="py-3">
                      {wf.latestRunStatus ? (
                        <Badge variant={statusVariant(wf.latestRunStatus as ProjectStatus)}>
                          {statusLabel(wf.latestRunStatus as ProjectStatus)}
                        </Badge>
                      ) : (
                        <span className="text-text-3 text-xs">未运行</span>
                      )}
                    </td>
                    <td className="py-3 text-right whitespace-nowrap">
                      <div className="flex items-center justify-end gap-1">
                        {/* 空工作流（0 节点）不可运行：禁用按钮并给提示（后端也会 400 兜底）。
                            title 挂在 span 上——disabled 按钮不触发 hover 事件，tooltip 需外层承载。 */}
                        <span
                          title={
                            wf.nodes.length === 0
                              ? "工作流为空，请先在编辑器添加节点"
                              : undefined
                          }
                        >
                          <Button
                            variant="ghost"
                            onClick={() => handleRunWorkflow(wf)}
                            disabled={runWorkflow.isPending || wf.nodes.length === 0}
                          >
                            {runWorkflow.isPending &&
                            runWorkflow.variables?.wfId === wf.id
                              ? "运行中…"
                              : "开始运行"}
                          </Button>
                        </span>
                        <Button
                          variant="ghost"
                          onClick={() =>
                            void navigate({
                              to: "/orgs/$org/projects/$id/workflow",
                              params: { org, id },
                              search: { wf: wf.id, mode: "edit" },
                            })
                          }
                        >
                          编辑
                        </Button>
                        {wf.latestPlanId && (
                          <Button
                            variant="ghost"
                            onClick={() =>
                              void navigate({
                                to: "/orgs/$org/projects/$id/workflow",
                                params: { org, id },
                                search: { wf: wf.id, run: wf.latestPlanId as string },
                              })
                            }
                          >
                            查看产物
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          onClick={() => void handleDeleteWorkflow(wf.id, wf.name)}
                          disabled={deleteWorkflow.isPending}
                        >
                          删除
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* Main Grid Layout */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-8">
        {/* Left column: Brief & config */}
        <div className="lg:col-span-1 space-y-6">
          <section className="bg-bg-surface border border-line rounded-xl p-5 shadow-sm">
            <h3 className="text-xs font-semibold tracking-wider text-text-3 uppercase mb-3">创意 Brief</h3>
            <p className="text-sm leading-relaxed text-text-2 whitespace-pre-wrap">
              {project.description || "（无描述）"}
            </p>
          </section>

          <section className="bg-bg-surface border border-line rounded-xl p-5 shadow-sm space-y-4">
            <h3 className="text-xs font-semibold tracking-wider text-text-3 uppercase">项目配置</h3>
            <div className="grid grid-cols-2 gap-4 text-xs">
              <div>
                <span className="text-text-3 block mb-1">内容类型</span>
                <span className="text-text-1 font-semibold">{project.contentType || "未指定"}</span>
              </div>
              <div>
                <span className="text-text-3 block mb-1">目标平台</span>
                <span className="text-text-1 font-semibold">{project.targetPlatform || "未指定"}</span>
              </div>
              <div className="col-span-2">
                <span className="text-text-3 block mb-1">视觉风格</span>
                <span className="text-text-1 font-semibold">{project.style || "未指定"}</span>
              </div>
            </div>
          </section>
        </div>

        {/* Right column: Run history list */}
        <div className="lg:col-span-2">
          <section className="bg-bg-surface border border-line rounded-xl p-5 shadow-sm min-h-[400px] flex flex-col">
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-xs font-semibold tracking-wider text-text-3 uppercase">生成记录 / 运行历史</h3>
              <button
                type="button"
                onClick={() =>
                  void navigate({
                    to: "/orgs/$org/projects/$id/runs",
                    params: { org, id },
                  })
                }
                className="text-[12px] text-text-3 hover:text-text-1"
              >
                查看全部运行 →
              </button>
            </div>

            <ProjectRunsTable projectId={id} org={org} />
          </section>
        </div>
      </div>

      {/* 运行期输入表单：工作流 inputsSchema 非空时弹出，填完再发起 run。
          条件挂载 → 每次打开都以最新 schema 重置内部表单态（无 reset effect）。 */}
      {runDialog && (
        <RunInputsDialog
          open
          onOpenChange={(o) => {
            if (!o) setRunDialog(null)
          }}
          title={runDialog.title}
          schema={runDialog.schema}
          submitting={runWorkflow.isPending}
          onSubmit={async (inputs) => {
            await runDialog.submit(inputs)
            setRunDialog(null)
          }}
        />
      )}
    </div>
  )
}
