import { toast } from "sonner"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import { AssetThumb } from "@/features/workflow/AssetThumb.tsx"
import { projectDisplayName } from "@/lib/projectName"
import type { CreateProjectInput, ModelConfig, Project, StorageConfig, Style } from "@/lib/types"
import { statusLabel, statusVariant } from "./status"
import { CreateProjectDialog } from "./CreateProjectDialog"
import { CoverDialog } from "./CoverDialog"
import { DeleteProjectDialog } from "./DeleteProjectDialog"
import { EditProjectDialog } from "./EditProjectDialog"

// RFC3339 时间串 → 本地绝对日期 YYYY-MM-DD。无效/缺失返回空串（调用方据此不渲染）。
function fmtDate(iso: string | undefined): string {
  if (!iso) return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const m = String(d.getMonth() + 1).padStart(2, "0")
  const day = String(d.getDate()).padStart(2, "0")
  return `${d.getFullYear()}-${m}-${day}`
}

// 「编辑」按钮提交的项目信息（= useUpdateProject 载荷去掉 id）。
export type UpdateProjectFields = {
  name: string
  description: string
  contentType: string
  targetPlatform: string
  style: string
  plannerProvider: string
  plannerModel: string
  imageProvider: string
  imageModel: string
  storageConfigId: string
  kind: string
}

export interface ProjectListViewProps {
  projects: Project[] | undefined
  isLoading: boolean
  isError: boolean
  /** 403：该 org 不存在或无访问权限 → 渲染 access-denied 空态并隐藏建项目动作。 */
  isForbidden?: boolean
  onRetry: () => void
  /** 当前 org（封面对话框失效 ["projects", org] 用）。 */
  org: string
  /** editor+ 才显示"新建项目"（viewer 隐藏）。 */
  canCreate: boolean
  styles: Style[]
  /** M5.1：org 下 kind=text 的启用模型，供"新建项目"/"编辑"对话框的规划模型下拉。 */
  textModels?: ModelConfig[]
  /** M9：org 下 kind=image 的启用模型，供"编辑"对话框的图片模型下拉。 */
  imageModels?: ModelConfig[]
  /** org 下的存储配置列表，供"新建项目"/"编辑"对话框的存储配置下拉。 */
  storageConfigs?: StorageConfig[]
  onCreate: (input: CreateProjectInput) => Promise<Project>
  /** 编辑项目信息（卡片「编辑」按钮）。id + 整表单字段 → 更新后的 Project。 */
  onUpdate: (input: { id: string } & UpdateProjectFields) => Promise<Project>
  /** roleAdmin 才显示「删除」（rbac.useRole 探针；后端 DELETE 仍强制 admin）。 */
  canDelete?: boolean
  /** 删除项目（卡片「删除」入口，DeleteProjectDialog 输入名确认后调）。 */
  onDelete?: (project: Project) => Promise<unknown>
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
  isForbidden = false,
  onRetry,
  org,
  canCreate,
  styles,
  textModels,
  imageModels,
  storageConfigs,
  onCreate,
  onUpdate,
  canDelete = false,
  onDelete,
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
        {canCreate && !isForbidden && (
          <CreateProjectDialog
            trigger={newButton}
            styles={styles}
            textModels={textModels}
            storageConfigs={storageConfigs}
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
      ) : isForbidden ? (
        // 403：跨租户 / 不存在的 org。不给「重试」（无权访问重试无意义），只说明状况。
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-1">无权访问</p>
          <p className="text-[12.5px] text-text-3">
            该组织不存在，或你没有访问权限。
          </p>
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
            // 卡片改为 div：封面带 + 「设封面」按钮与"打开项目"按钮平级，
            // 避免按钮嵌按钮（非法 HTML 且会劫持点击）。
            <div
              key={project.id}
              className="flex flex-col overflow-hidden rounded-xl border border-line bg-bg-surface transition-colors hover:border-text-3"
            >
              {/* 封面带：有封面渲染资产缩略图，否则占位。 */}
              {project.coverAssetId ? (
                <AssetThumb
                  assetId={project.coverAssetId}
                  alt={project.name}
                  className="aspect-video w-full rounded-t-lg object-cover"
                />
              ) : (
                <div className="grid aspect-video w-full place-items-center rounded-t-lg bg-bg-raised text-[11px] text-text-3">
                  无封面
                </div>
              )}

              {/* 主体：点击进项目工作台。hover 高亮 + cursor-pointer 明确整卡可点。 */}
              <button
                type="button"
                onClick={() => onOpenProject(project)}
                className="flex flex-1 cursor-pointer flex-col gap-3 p-[18px] text-left transition-colors hover:bg-bg-raised/40"
              >
                <div className="flex items-start justify-between gap-3">
                  <span
                    title={projectDisplayName(project.name, project.id)}
                    className="min-w-0 truncate font-heading text-[15px] font-medium text-text-1"
                  >
                    {projectDisplayName(project.name, project.id)}
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
                <div className="mt-auto flex flex-col gap-1 text-[11px] text-text-3">
                  {(project.contentType || project.targetPlatform || project.style) && (
                    <div className="flex gap-2">
                      {project.contentType && <span>{project.contentType}</span>}
                      {project.targetPlatform && <span>· {project.targetPlatform}</span>}
                      {project.style && <span>· {project.style}</span>}
                    </div>
                  )}
                  {fmtDate(project.createdAt) && (
                    <div className="flex flex-wrap gap-x-2">
                      <span>创建 {fmtDate(project.createdAt)}</span>
                      {fmtDate(project.updatedAt) && (
                        <span>· 更新 {fmtDate(project.updatedAt)}</span>
                      )}
                    </div>
                  )}
                </div>
              </button>

              {/* 「编辑」+「设封面」：与打开项目按钮平级。editor+ 才显示。 */}
              {canCreate && (
                <div className="flex items-center gap-2 border-t border-line px-[18px] py-2.5">
                  <EditProjectDialog
                    project={project}
                    textModels={textModels}
                    imageModels={imageModels}
                    styles={styles}
                    storageConfigs={storageConfigs}
                    onSubmit={(input) => onUpdate({ id: project.id, ...input })}
                    onSuccess={() => toast.success("项目信息已更新")}
                    trigger={
                      <Button variant="ghost" className="h-auto px-2 py-1 text-[12px]">
                        编辑
                      </Button>
                    }
                  />
                  <CoverDialog
                    project={project}
                    org={org}
                    trigger={
                      <Button variant="ghost" className="h-auto px-2 py-1 text-[12px]">
                        设封面
                      </Button>
                    }
                  />
                  {/* 「删除」：admin 探针可见（后端 roleAdmin 强制）；输入项目名确认。 */}
                  {canDelete && onDelete && (
                    <DeleteProjectDialog
                      project={project}
                      onSubmit={() => onDelete(project)}
                      onSuccess={() => toast.success("项目已删除")}
                      trigger={
                        <Button
                          variant="ghost"
                          className="ml-auto h-auto px-2 py-1 text-[12px] text-danger"
                        >
                          删除
                        </Button>
                      }
                    />
                  )}
                </div>
              )}
            </div>
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
              storageConfigs={storageConfigs}
              onSubmit={onCreate}
              onSuccess={onOpenProject}
            />
          )}
        </div>
      )}
    </div>
  )
}
