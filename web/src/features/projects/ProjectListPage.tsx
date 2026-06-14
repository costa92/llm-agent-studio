import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import type { CreateProjectInput, ModelConfig, Project, Style } from "@/lib/types"
import { statusLabel, statusVariant } from "./status"
import { CreateProjectDialog } from "./CreateProjectDialog"

export interface ProjectListViewProps {
  projects: Project[] | undefined
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  /** editor+ 才显示"新建项目"（viewer 隐藏）。 */
  canCreate: boolean
  styles: Style[]
  /** M5.1：org 下 kind=text 的启用模型，供"新建项目"对话框的规划模型下拉。 */
  textModels?: ModelConfig[]
  onCreate: (input: CreateProjectInput) => Promise<Project>
  /** 点击卡片进工作台（路由在 T10 接入；T9 为可注入回调便于单测）。 */
  onOpenProject: (project: Project) => void
  /** T5：org 尚无启用的生成模型配置 → 空态先引导去配置模型。 */
  needsModelConfig?: boolean
  /** T5：跳模型配置页（容器接路由）。 */
  onConfigureModel?: () => void
}

// 纯展示视图（无路由/Query 依赖），便于单测 loading/empty/error/CTA 门禁。
export function ProjectListView({
  projects,
  isLoading,
  isError,
  onRetry,
  canCreate,
  styles,
  textModels,
  onCreate,
  onOpenProject,
  needsModelConfig = false,
  onConfigureModel,
}: ProjectListViewProps) {
  const newButton = (
    <Button variant="amber">新建项目</Button>
  )

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-5 p-6">
      <header className="flex items-center justify-between">
        <h1 className="font-heading text-[22px] font-bold text-text-1">项目</h1>
        {canCreate && (
          <CreateProjectDialog
            trigger={newButton}
            styles={styles}
            textModels={textModels}
            onSubmit={onCreate}
            onSuccess={onOpenProject}
          />
        )}
      </header>

      {isLoading ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-[120px] rounded-xl" />
          ))}
        </div>
      ) : isError ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-2">项目加载失败</p>
          <Button variant="ghost" onClick={onRetry}>
            重试
          </Button>
        </div>
      ) : projects && projects.length > 0 ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-4">
          {projects.map((project) => (
            <button
              key={project.id}
              type="button"
              onClick={() => onOpenProject(project)}
              className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-[18px] text-left transition-colors hover:border-text-3"
            >
              <div className="flex items-start justify-between gap-3">
                <span className="font-heading text-[15px] font-medium text-text-1">
                  {project.name}
                </span>
                <Badge variant={statusVariant(project.status)}>
                  {statusLabel(project.status)}
                </Badge>
              </div>
              {project.description && (
                <p className="line-clamp-2 text-[12.5px] text-text-2">
                  {project.description}
                </p>
              )}
              <div className="mt-auto flex gap-2 text-[11px] text-text-3">
                {project.contentType && <span>{project.contentType}</span>}
                {project.targetPlatform && <span>· {project.targetPlatform}</span>}
                {project.style && <span>· {project.style}</span>}
              </div>
            </button>
          ))}
        </div>
      ) : needsModelConfig ? (
        // T5：尚无启用的生成模型 → 先引导配置模型再开始制作。
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-1">先配置一个生成模型再开始制作</p>
          <p className="text-[12.5px] text-text-3">
            项目需要一个启用的生成模型来产出剧本与素材
          </p>
          <Button variant="amber" onClick={onConfigureModel}>
            去配置模型
          </Button>
        </div>
      ) : (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-1">还没有项目</p>
          <p className="text-[12.5px] text-text-3">
            用一句创意需求开始你的第一支作品
          </p>
          {canCreate && (
            <CreateProjectDialog
              trigger={<Button variant="amber">新建项目</Button>}
              styles={styles}
              textModels={textModels}
              onSubmit={onCreate}
              onSuccess={onOpenProject}
            />
          )}
        </div>
      )}
    </div>
  )
}
