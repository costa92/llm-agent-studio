import { z } from "zod"
import type { PictureBookConfig, Project } from "@/lib/types"
import { emptyPictureBookConfig } from "./pbConfig"

// 绘本配置子 schema：形状对齐 PictureBookConfig；不做逐字段强约束（现状即无）。
const pbConfigSchema: z.ZodType<PictureBookConfig> = z.object({
  ageBand: z.enum(["", "0-3", "3-6", "6-8"]),
  bookType: z.string(),
  illustrationStyle: z.string(),
  narrationStyle: z.string(),
  themes: z.array(z.string()),
  pageCount: z.number(),
  voice: z.string(),
})

// 内容类型 / 目标平台为前端枚举（后端只存字符串，无白名单约束）。
// 单一来源：defaultsFor 取首项作默认，ProjectFields 渲染下拉选项亦取自此。
export const CONTENT_TYPES = ["短视频", "广告片", "动画", "宣传片"] as const
export const TARGET_PLATFORMS = ["抖音", "视频号", "B 站", "小红书", "通用"] as const

// 项目创建/编辑共享表单模型。把 kind（项目类型）与 pbConfig（绘本配置）
// 折入 rhf（原来是各 Dialog 的本地 useState），让单一 onSubmit 拿到全量。
// name/style 必填（后端缺则 400）；description（Edit 用）与各模型字段为可空字符串。
// 注意：brief 在 base 里「放宽」（z.string()），不在此处必填——Edit 的 defaultsFor
//   永远产出 brief:""（Project 类型无 brief），而 zodResolver v5 会校验整份
//   form.getValues()；若 base 里 brief.min(1)，Edit resolver 必在 brief 上失败，
//   handleSubmit 静默不触发（Edit briefRequired=false，错误还被藏起来）→ Edit 变哑弹。
//   故 brief 必填仅放到 createProjectFormSchema（Create 专用），见下。
// 注意：不对 pbConfig/kind 加任何 superRefine——现状绘本配置无前端必填校验，本重构不引入新校验。
export const projectFormSchema = z.object({
  name: z.string().min(1, "请输入项目名称"),
  brief: z.string(),
  description: z.string(),
  contentType: z.string().min(1, "请选择内容类型"),
  targetPlatform: z.string().min(1, "请选择目标平台"),
  style: z.string().min(1, "请选择风格"),
  plannerProvider: z.string(),
  plannerModel: z.string(),
  imageProvider: z.string(),
  imageModel: z.string(),
  storageConfigId: z.string(),
  kind: z.enum(["standard", "picturebook"]),
  pbConfig: pbConfigSchema,
})

// Create 专用 schema：在 base 上把 brief 收紧为必填。值形状与 base 一致，
// 故仍复用 ProjectFormValues。Edit 用 base（projectFormSchema），Create 用此。
export const createProjectFormSchema = projectFormSchema.extend({
  brief: z.string().min(1, "请输入创意需求"),
})

export type ProjectFormValues = z.infer<typeof projectFormSchema>

// pbConfig 序列化助手：Create/Edit 提交 pictureBookConfig 时统一走此，
// 避免各处忘记 JSON.stringify 或写法漂移（与现状 JSON.stringify(pbConfig) 等价）。
export const serializePbConfig = (pb: PictureBookConfig): string =>
  JSON.stringify(pb)

// 编辑时从 project.pictureBookConfig（原始 JSON）解析；空/解析失败回退空配置。
export function parsePbConfig(raw?: string): PictureBookConfig {
  if (!raw) return emptyPictureBookConfig
  try {
    const parsed = JSON.parse(raw) as Partial<PictureBookConfig>
    return { ...emptyPictureBookConfig, ...parsed }
  } catch {
    return emptyPictureBookConfig
  }
}

// initial 项目 → 表单默认值。无 initial = 空表单（新建）。
// 注意：style 默认空串——CreateProjectForm 会在 initial.style 缺省时用 styles[0]?.name
// 覆盖（保留现状「默认选首个风格」UX），故此处不擅自填首风格。
export function defaultsFor(
  initial?: Partial<Project> & { brief?: string },
): ProjectFormValues {
  return {
    name: initial?.name ?? "",
    brief: initial?.brief ?? "",
    description: initial?.description ?? "",
    contentType: initial?.contentType ?? CONTENT_TYPES[0],
    targetPlatform: initial?.targetPlatform ?? TARGET_PLATFORMS[0],
    style: initial?.style ?? "",
    plannerProvider: initial?.plannerProvider ?? "",
    plannerModel: initial?.plannerModel ?? "",
    imageProvider: initial?.imageProvider ?? "",
    imageModel: initial?.imageModel ?? "",
    storageConfigId: initial?.storageConfigId ?? "",
    kind: initial?.kind === "picturebook" ? "picturebook" : "standard",
    pbConfig: parsePbConfig(initial?.pictureBookConfig),
  }
}
