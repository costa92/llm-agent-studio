import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { ApiError } from "@/lib/apiClient"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import {
  useProject,
  useRun,
  usePlans,
} from "@/features/workflow/api"
import { useUpdateProject, usePromptStyles } from "@/features/projects/api"
import {
  useWorkflows,
  useRunWorkflow,
  useDeleteWorkflow,
} from "@/features/projects/workflowApi"
import { useOrgTextModels, useOrgImageModels } from "@/features/cost/api"
import { useStorageConfigs } from "@/features/storage/api"
import { EditProjectDialog } from "@/features/projects/EditProjectDialog"
import { WorkflowDialog } from "@/features/projects/WorkflowDialog"
import { statusLabel, statusVariant } from "@/features/projects/status"
import type { ProjectStatus } from "@/lib/types"

export const Route = createFileRoute("/_authed/orgs/$org/projects/$id/")({
  component: RunsListPage,
})

function RunsListPage() {
  const { org, id } = Route.useParams()
  const navigate = useNavigate()
  const projectQuery = useProject(id)
  const project = projectQuery.data

  const plansQuery = usePlans(id)
  const plans = plansQuery.data || []

  const run = useRun(id)

  const [isRunning, setIsRunning] = useState(false)

  // M5.1/M9: 项目级"编辑模型配置"。
  const updateProject = useUpdateProject(org)
  const textModelsQuery = useOrgTextModels(org)
  const imageModelsQuery = useOrgImageModels(org)
  const stylesQuery = usePromptStyles()
  const storageConfigsQuery = useStorageConfigs(org)

  // 工作流：一个项目可有多条 DAG 工作流，各自独立运行。
  const workflowsQuery = useWorkflows(id)
  const workflows = workflowsQuery.data || []
  const runWorkflow = useRunWorkflow(id)
  const deleteWorkflow = useDeleteWorkflow(id)

  async function handleRunWorkflow(wfId: string) {
    try {
      const res = await runWorkflow.mutateAsync(wfId)
      if (res.fallbackUsed) {
        toast.warning("工作流校验未通过，已回落默认管线")
      } else {
        toast.success("已开始运行")
      }
      void navigate({
        to: "/orgs/$org/projects/$id/runs/$runId",
        params: { org, id, runId: res.planId },
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

  async function handleStartGeneration() {
    try {
      setIsRunning(true)
      const res = await run.mutateAsync()
      if (res.fallbackUsed) {
        toast.warning("Planner 输出畸形，已回落默认管线")
      } else {
        toast.success("已开始运行")
      }
      void navigate({
        to: "/orgs/$org/projects/$id/runs/$runId",
        params: { org, id, runId: res.planId },
      })
    } catch (err) {
      const status = (err as { status?: number }).status
      if (status === 429) {
        toast.error("配额已用尽，请稍后再试")
        return
      }
      toast.error("运行失败", {
        action: {
          label: "去配置模型",
          onClick: () =>
            void navigate({ to: "/orgs/$org/model-configs", params: { org } }),
        },
      })
    } finally {
      setIsRunning(false)
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
            <span
              onClick={() => void navigate({ to: "/orgs/$org/projects", params: { org } })}
              className="text-[12px] text-text-3 cursor-pointer hover:text-text-1"
            >
              项目列表
            </span>
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
        <Button
          variant="amber"
          onClick={handleStartGeneration}
          disabled={isRunning || run.isPending}
        >
          {isRunning || run.isPending ? "正在初始化..." : "开始新生成"}
        </Button>
      </header>

      {/* 工作流 section：一个项目可有多条 DAG 工作流，各自独立运行。 */}
      <section className="bg-bg-default border border-line rounded-xl p-5 shadow-sm mb-8">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-xs font-semibold tracking-wider text-text-3 uppercase">工作流</h3>
          <WorkflowDialog
            projectId={id}
            orgId={org}
            trigger={<Button variant="amber">新建工作流</Button>}
            onSuccess={() => toast.success("工作流已保存")}
          />
        </div>

        {workflows.length === 0 ? (
          <div className="flex flex-col items-center justify-center text-center py-8">
            <p className="text-sm text-text-2 mb-1 font-semibold">暂无工作流</p>
            <p className="text-xs text-text-3 max-w-xs">
              点击右上角“新建工作流”，按手动配置的 DAG 节点和依赖关系执行。
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr className="border-b border-line text-xs text-text-3">
                  <th className="pb-3 font-semibold">名称</th>
                  <th className="pb-3 font-semibold">节点数</th>
                  <th className="pb-3 font-semibold">最近运行</th>
                  <th className="pb-3 font-semibold text-right">操作</th>
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
                    <td className="py-3 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          onClick={() => void handleRunWorkflow(wf.id)}
                          disabled={runWorkflow.isPending}
                        >
                          运行
                        </Button>
                        <WorkflowDialog
                          projectId={id}
                          orgId={org}
                          initial={wf}
                          trigger={<Button variant="ghost">编辑</Button>}
                          onSuccess={() => toast.success("工作流已保存")}
                        />
                        {wf.latestPlanId && (
                          <Button
                            variant="ghost"
                            onClick={() =>
                              void navigate({
                                to: "/orgs/$org/projects/$id/runs/$runId",
                                params: { org, id, runId: wf.latestPlanId as string },
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
          <section className="bg-bg-default border border-line rounded-xl p-5 shadow-sm">
            <h3 className="text-xs font-semibold tracking-wider text-text-3 uppercase mb-3">创意 Brief</h3>
            <p className="text-sm leading-relaxed text-text-2 whitespace-pre-wrap">
              {project.description || "（无描述）"}
            </p>
          </section>

          <section className="bg-bg-default border border-line rounded-xl p-5 shadow-sm space-y-4">
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
          <section className="bg-bg-default border border-line rounded-xl p-5 shadow-sm min-h-[400px] flex flex-col">
            <h3 className="text-xs font-semibold tracking-wider text-text-3 uppercase mb-4">生成记录 / 运行历史</h3>
            
            {plans.length === 0 ? (
              <div className="flex-1 flex flex-col items-center justify-center text-center p-8">
                <div className="w-16 h-16 bg-bg-surface border border-line rounded-full flex items-center justify-center mb-4 text-text-3">
                  📋
                </div>
                <p className="text-sm text-text-2 mb-2 font-semibold">暂无生成记录</p>
                <p className="text-xs text-text-3 max-w-xs mb-4">
                  该项目尚未开始任何生成任务。点击右上角“开始新生成”按钮以启动制片管线。
                </p>
              </div>
            ) : (
              <div className="flex-1 overflow-x-auto">
                <table className="w-full text-left border-collapse">
                  <thead>
                    <tr className="border-b border-line text-xs text-text-3">
                      <th className="pb-3 font-semibold">序号</th>
                      <th className="pb-3 font-semibold">生成记录 ID</th>
                      <th className="pb-3 font-semibold">运行状态</th>
                      <th className="pb-3 font-semibold">管线回落</th>
                      <th className="pb-3 font-semibold">启动时间</th>
                      <th className="pb-3 font-semibold text-right">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {plans.map((plan, index) => {
                      const runNum = plans.length - index
                      
                      return (
                        <tr key={plan.id} className="border-b border-line/50 hover:bg-bg-surface/30 transition-colors text-sm text-text-1">
                          <td className="py-3 font-semibold">#{runNum}</td>
                          <td className="py-3 font-mono text-xs text-text-2 truncate max-w-[120px]" title={plan.id}>
                            {plan.id}
                          </td>
                          <td className="py-3">
                            <Badge variant={statusVariant(plan.status as ProjectStatus)}>
                              {statusLabel(plan.status as ProjectStatus)}
                            </Badge>
                          </td>
                          <td className="py-3">
                            {plan.fallbackUsed ? (
                              <Badge variant="rejected">
                                ⚠️ 已回落
                              </Badge>
                            ) : (
                              <span className="text-text-3 text-xs">-</span>
                            )}
                          </td>
                          <td className="py-3 text-xs text-text-2">
                            {new Date(plan.createdAt).toLocaleString()}
                          </td>
                          <td className="py-3 text-right">
                            <Button
                              variant="ghost"
                              onClick={() => {
                                void navigate({
                                  to: "/orgs/$org/projects/$id/runs/$runId",
                                  params: { org, id, runId: plan.id },
                                })
                              }}
                            >
                              进入工作台 →
                            </Button>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </div>
      </div>
    </div>
  )
}
