import { createFileRoute, useNavigate } from "@tanstack/react-router"
import {
  useCreateProject,
  useDeleteProject,
  useUpdateProject,
  useProjects,
  usePromptStyles,
} from "@/features/projects/api"
import { useRole } from "@/app/rbac"
import {
  useModelConfigs,
  useOrgTextModels,
  useOrgImageModels,
} from "@/features/cost/api"
import { useStorageConfigs } from "@/features/storage/api"
import { ProjectListView } from "@/features/projects/ProjectListPage"
import { ApiError } from "@/lib/apiClient"

// T9：项目列表 + 建项目视图。org 校验由父段布局 orgs.$org.projects.tsx 的 beforeLoad 承担。
export const Route = createFileRoute("/_authed/orgs/$org/projects/")({
  component: ProjectsPage,
})

function ProjectsPage() {
  const { org } = Route.useParams()
  const navigate = useNavigate()
  const projectsQuery = useProjects(org)
  const stylesQuery = usePromptStyles()
  const createProject = useCreateProject(org)
  const updateProject = useUpdateProject(org)
  const deleteProject = useDeleteProject(org)
  // 删除是 admin-only（后端 roleAdmin 强制）；useRole 探针决定入口可见性。
  const role = useRole(org)
  // T5：org 是否已有启用的生成模型配置（model-configs 列表里存在 enabled 项）。
  // 仅在查询成功（非加载/错误）且确无启用项时引导配置——避免加载中误闪引导。
  // admin-gated 端点；非 admin 拿不到列表 → 不引导（保持普通空态）。
  const modelConfigsQuery = useModelConfigs(org)
  const needsModelConfig =
    modelConfigsQuery.isSuccess &&
    !modelConfigsQuery.data.some((c) => c.enabled)
  // M5.1/M9: "新建项目"/"编辑"对话框的规划模型 + 图片模型下拉的源数据。
  const textModelsQuery = useOrgTextModels(org)
  const imageModelsQuery = useOrgImageModels(org)
  const storageConfigsQuery = useStorageConfigs(org)

  // 403 = 该 org 不存在或当前用户无权访问（跨租户/不存在 org）。渲染 access-denied 空态并
  // 隐藏「新建项目」等动作，而非把假 org 当真实工作区渲染完整壳。
  const isForbidden =
    projectsQuery.error instanceof ApiError &&
    projectsQuery.error.status === 403

  return (
    <ProjectListView
      projects={projectsQuery.data}
      isLoading={projectsQuery.isLoading}
      isError={projectsQuery.isError}
      isForbidden={isForbidden}
      onRetry={() => void projectsQuery.refetch()}
      org={org}
      needsModelConfig={needsModelConfig}
      onConfigureModel={() =>
        navigate({ to: "/orgs/$org/model-configs", params: { org } })
      }
      // 角色门禁：editor+ 才显示新建/编辑/封面入口（viewer 隐藏，避免点了必 403 的死胡同）；
      // 后端 createProjectHandler 仍以 roleEditor 强制。
      canCreate={role.canWrite}
      styles={stylesQuery.data ?? []}
      textModels={textModelsQuery.data}
      imageModels={imageModelsQuery.data}
      storageConfigs={storageConfigsQuery.data}
      onCreate={(input) => createProject.mutateAsync(input)}
      onUpdate={(input) => updateProject.mutateAsync(input)}
      canDelete={role.isAdmin}
      onDelete={(project) => deleteProject.mutateAsync({ id: project.id })}
      onOpenProject={(project) =>
        // T10：进项目工作台（制片轨道）——org-scoped 路径，org param 透传以保住导航轨。
        navigate({
          to: "/orgs/$org/projects/$id",
          params: { org, id: project.id },
        })
      }
    />
  )
}
