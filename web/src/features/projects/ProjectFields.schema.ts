import { z } from "zod"
import type { Project } from "@/lib/types"

// 内容类型 / 目标平台为前端枚举（后端只存字符串，无白名单约束）。
// 单一来源：defaultsFor 取首项作默认，ProjectFields 渲染下拉选项亦取自此。
export const CONTENT_TYPES = ["短视频", "广告片", "动画", "宣传片"] as const
export const TARGET_PLATFORMS = ["抖音", "视频号", "B 站", "小红书", "通用"] as const

// 项目创建/编辑共享表单模型。
// name 必填；contentType/targetPlatform/style 均可空——留空表示「不指定，由工作流决定」
//（内容类型/风格解耦：生成风格由工作流的设置驱动，而非建项目时的隐藏默认）。
// description（Edit 用）与各模型字段为可空字符串。
// 注意：brief 在 base 里「放宽」（z.string()），不在此处必填——Edit 的 defaultsFor
//   永远产出 brief:""（Project 类型无 brief），而 zodResolver v5 会校验整份
//   form.getValues()；若 base 里 brief.min(1)，Edit resolver 必在 brief 上失败，
//   handleSubmit 静默不触发（Edit briefRequired=false，错误还被藏起来）→ Edit 变哑弹。
//   故 brief 必填仅放到 createProjectFormSchema（Create 专用），见下。
export const projectFormSchema = z.object({
  // trim 后校验：拦掉纯空白名（min(1)）并封顶长度（与后端 project.Create 的 200 上限对齐）。
  name: z.string().trim().min(1, "请输入项目名称").max(200, "项目名称不能超过 200 个字符"),
  brief: z.string(),
  description: z.string(),
  contentType: z.string(),
  targetPlatform: z.string(),
  style: z.string(),
  plannerProvider: z.string(),
  plannerModel: z.string(),
  imageProvider: z.string(),
  imageModel: z.string(),
  storageConfigId: z.string(),
})

// Create 专用 schema：在 base 上把 brief 收紧为必填。值形状与 base 一致，
// 故仍复用 ProjectFormValues。Edit 用 base（projectFormSchema），Create 用此。
export const createProjectFormSchema = projectFormSchema.extend({
  brief: z.string().min(1, "请输入创意需求"),
})

export type ProjectFormValues = z.infer<typeof projectFormSchema>

// initial 项目 → 表单默认值。无 initial = 空表单（新建）。
// 注意：contentType/targetPlatform/style 默认空串——不再在建项目时预填任何生成默认，
// 留空即「不指定，由工作流决定」（内容类型/风格解耦）。
export function defaultsFor(
  initial?: Partial<Project> & { brief?: string },
): ProjectFormValues {
  return {
    name: initial?.name ?? "",
    brief: initial?.brief ?? "",
    description: initial?.description ?? "",
    contentType: initial?.contentType ?? "",
    targetPlatform: initial?.targetPlatform ?? "",
    style: initial?.style ?? "",
    plannerProvider: initial?.plannerProvider ?? "",
    plannerModel: initial?.plannerModel ?? "",
    imageProvider: initial?.imageProvider ?? "",
    imageModel: initial?.imageModel ?? "",
    storageConfigId: initial?.storageConfigId ?? "",
  }
}
