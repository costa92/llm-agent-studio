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

// 项目创建/编辑共享表单模型。把 kind（项目类型）与 pbConfig（绘本配置）
// 折入 rhf（原来是各 Dialog 的本地 useState），让单一 onSubmit 拿到全量。
// name/brief/style 必填（后端缺则 400）；description（Edit 用）与各模型字段为可空字符串。
// 注意：不对 pbConfig/kind 加任何 superRefine——现状绘本配置无前端必填校验，本重构不引入新校验。
export const projectFormSchema = z.object({
  name: z.string().min(1, "请输入项目名称"),
  brief: z.string().min(1, "请输入创意需求"),
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

export type ProjectFormValues = z.infer<typeof projectFormSchema>

// 编辑时从 project.pictureBookConfig（原始 JSON）解析；空/解析失败回退空配置。
function parsePbConfig(raw?: string): PictureBookConfig {
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
    contentType: initial?.contentType ?? "短视频",
    targetPlatform: initial?.targetPlatform ?? "抖音",
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
