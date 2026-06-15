import { createFileRoute, useNavigate } from "@tanstack/react-router"
import {
  useCreateProject,
  useProjects,
  usePromptStyles,
} from "@/features/projects/api"
import { useModelConfigs, useOrgTextModels } from "@/features/cost/api"
import { ProjectListView } from "@/features/projects/ProjectListPage"

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
  // T5：org 是否已有启用的生成模型配置（model-configs 列表里存在 enabled 项）。
  // 仅在查询成功（非加载/错误）且确无启用项时引导配置——避免加载中误闪引导。
  // admin-gated 端点；非 admin 拿不到列表 → 不引导（保持普通空态）。
  const modelConfigsQuery = useModelConfigs(org)
  const needsModelConfig =
    modelConfigsQuery.isSuccess &&
    !modelConfigsQuery.data.some((c) => c.enabled)
  // M5.1: "新建项目"对话框的规划模型下拉的源数据。
  const textModelsQuery = useOrgTextModels(org)

  return (
    <ProjectListView
      projects={projectsQuery.data}
      isLoading={projectsQuery.isLoading}
      isError={projectsQuery.isError}
      onRetry={() => void projectsQuery.refetch()}
      org={org}
      needsModelConfig={needsModelConfig}
      onConfigureModel={() =>
        navigate({ to: "/orgs/$org/model-configs", params: { org } })
      }
      // 角色门禁：rbac 仅提供 admin 探针；editor 无可探测的只读端点（后端无 editor-gated GET）。
      // 故按 rbac 文档的"乐观显示 + 后端强制"策略乐观显示新建入口，editor+ 由后端 createProjectHandler 强制。
      canCreate
      styles={stylesQuery.data ?? []}
      textModels={textModelsQuery.data}
      onCreate={(input) => createProject.mutateAsync(input)}
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
