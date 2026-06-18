import { Controller, useFormContext } from "react-hook-form"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"
import type {
  ModelConfig,
  Project,
  StorageConfig,
  Style,
} from "@/lib/types"
import { MODE_LABELS } from "@/features/storage/StorageConfigPage"
import { PictureBookConfigForm } from "./PictureBookConfigForm"
import {
  CONTENT_TYPES,
  TARGET_PLATFORMS,
  type ProjectFormValues,
} from "./ProjectFields.schema"

export interface ProjectFieldsProps {
  styles: Style[]
  // id 前缀，避免 Create/Edit 同时挂载时 id 冲突。Create 传 "create" / Edit 传 "edit"。
  fieldIdPrefix: string
  // 「创意需求」textarea 绑的 rhf 字段：Create=brief（必填），Edit=description（不校验）。
  briefFieldName?: "brief" | "description"
  briefRequired?: boolean
  // Edit=true：即使无 text 模型也渲染规划下拉（复刻 Edit 现状）。Create 不传（仅有模型时显示）。
  alwaysShowPlanner?: boolean
  textModels?: ModelConfig[]
  imageModels?: ModelConfig[]
  storageConfigs?: StorageConfig[]
  // Edit 用：风格补项 + 各「当前：…」提示。
  project?: Project
}

// 项目创建/编辑共享字段块（经 useFormContext 读写）。
// 字段差异由 props 表达——同一组件供 Create / Edit 复用，呈现/行为各自不变。
export function ProjectFields({
  styles,
  fieldIdPrefix,
  briefFieldName = "brief",
  briefRequired = true,
  alwaysShowPlanner = false,
  textModels,
  imageModels,
  storageConfigs,
  project,
}: ProjectFieldsProps) {
  const {
    register,
    control,
    watch,
    setValue,
    formState: { errors },
  } = useFormContext<ProjectFormValues>()

  const pre = (s: string) => `${fieldIdPrefix}-${s}`
  const kind = watch("kind")

  // Edit 风格补项：项目当前风格若不在 styles 列表，补一项避免回显丢失。
  const hasCurrentStyle =
    !project?.style || styles.some((s) => s.name === project.style)

  // 规划下拉显示条件：Edit（alwaysShowPlanner）无条件显示；Create 仅有 text 模型时显示。
  const showPlanner = alwaysShowPlanner || (textModels != null && textModels.length > 0)
  const plannerModels = textModels ?? []

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label htmlFor={pre("name")}>项目名称</Label>
        <Input id={pre("name")} aria-invalid={errors.name != null} {...register("name")} />
        {errors.name && <p className="text-[12px] text-danger">{errors.name.message}</p>}
      </div>

      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label htmlFor={pre("brief")}>创意需求</Label>
        <Textarea
          id={pre("brief")}
          rows={2}
          placeholder="用一句话描述你想要的作品"
          aria-invalid={briefRequired && errors.brief != null}
          {...register(briefFieldName)}
        />
        {briefRequired && errors.brief && (
          <p className="text-[12px] text-danger">{errors.brief.message}</p>
        )}
      </div>

      {/* 项目类型：标准 / 儿童绘本。选绘本展开 PictureBookConfigForm。 */}
      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label>项目类型</Label>
        <div className="flex gap-2">
          {(
            [
              { v: "standard", label: "标准" },
              { v: "picturebook", label: "儿童绘本" },
            ] as const
          ).map((opt) => (
            <button
              key={opt.v}
              type="button"
              onClick={() => setValue("kind", opt.v, { shouldValidate: false })}
              className={cn(
                "rounded-md border px-4 py-[7px] text-[13px] font-medium transition-colors",
                kind === opt.v
                  ? "border-amber bg-amber text-[#1a1408]"
                  : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
              )}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {kind === "picturebook" && (
        <div className="sm:col-span-2">
          <Controller
            control={control}
            name="pbConfig"
            render={({ field }) => (
              <PictureBookConfigForm value={field.value} onChange={field.onChange} />
            )}
          />
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={pre("contentType")}>内容类型</Label>
        <Controller
          control={control}
          name="contentType"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id={pre("contentType")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CONTENT_TYPES.map((ct) => (
                  <SelectItem key={ct} value={ct}>
                    {ct}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={pre("targetPlatform")}>目标平台</Label>
        <Controller
          control={control}
          name="targetPlatform"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id={pre("targetPlatform")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TARGET_PLATFORMS.map((tp) => (
                  <SelectItem key={tp} value={tp}>
                    {tp}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={pre("style")}>风格</Label>
        <Controller
          control={control}
          name="style"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id={pre("style")} aria-invalid={errors.style != null}>
                <SelectValue placeholder="选择风格" />
              </SelectTrigger>
              <SelectContent>
                {!hasCurrentStyle && project?.style && (
                  <SelectItem value={project.style}>{project.style}</SelectItem>
                )}
                {styles.map((s) => (
                  <SelectItem key={s.name} value={s.name}>
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
        {errors.style && <p className="text-[12px] text-danger">{errors.style.message}</p>}
      </div>

      {/* 规划模型下拉：Edit 无条件显示（alwaysShowPlanner），Create 仅有 text 模型时显示。 */}
      {showPlanner && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={pre("plannerModel")}>
            {project ? "规划用模型" : "规划用模型（可选）"}
          </Label>
          <ModelPairSelect
            triggerId={pre("plannerModel")}
            models={plannerModels}
            providerName="plannerProvider"
            modelName="plannerModel"
          />
          {project ? (
            <p className="text-[11.5px] text-text-3">
              当前：{project.plannerProvider && project.plannerModel
                ? `${project.plannerProvider} · ${project.plannerModel}`
                : "组织默认"}。保存后下次 run 起生效。
            </p>
          ) : (
            <p className="text-[11.5px] text-text-3">
              留空 = 走组织默认；选某个模型则本次及后续 run 都用该模型。
            </p>
          )}
        </div>
      )}

      {imageModels && imageModels.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={pre("imageModel")}>
            {project ? "图片生成模型" : "图片生成模型（可选）"}
          </Label>
          <ModelPairSelect
            triggerId={pre("imageModel")}
            models={imageModels}
            providerName="imageProvider"
            modelName="imageModel"
          />
          {project ? (
            <p className="text-[11.5px] text-text-3">
              当前：{project.imageProvider && project.imageModel
                ? `${project.imageProvider} · ${project.imageModel}`
                : "组织默认"}。保存后下次 run 起生效。
            </p>
          ) : (
            <p className="text-[11.5px] text-text-3">
              留空 = 走组织默认；选某个模型则本次及后续 run 都用该模型生成图片。
            </p>
          )}
        </div>
      )}

      {/* 存储配置下拉：仅 Edit 传 storageConfigs 时渲染。 */}
      {storageConfigs && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={pre("storageConfigId")}>存储配置</Label>
          <Controller
            control={control}
            name="storageConfigId"
            render={({ field }) => (
              <Select
                value={field.value || "__default__"}
                onValueChange={(v) => field.onChange(v === "__default__" ? "" : v)}
              >
                <SelectTrigger id={pre("storageConfigId")} aria-invalid={false}>
                  <SelectValue placeholder="继承组织默认" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">继承组织默认</SelectItem>
                  {storageConfigs.filter((c) => c.enabled).map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}（{MODE_LABELS[c.mode]}）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
          <p className="text-[11.5px] text-text-3">
            当前：{(() => {
              if (!project?.storageConfigId) return "继承组织默认"
              const c = storageConfigs.find((c) => c.id === project.storageConfigId)
              return c ? `${c.name}（${MODE_LABELS[c.mode]}）` : project.storageConfigId
            })()}。保存后下一次资源生成或加载起生效。
          </p>
        </div>
      )}
    </div>
  )
}

// provider+model 成对下拉（规划/图片共用）。__default__ = 空（走 org 默认），
// 其余编码为 `${provider}::${model}`。保留现状双 Controller 嵌套语义。
function ModelPairSelect({
  triggerId,
  models,
  providerName,
  modelName,
}: {
  triggerId: string
  models: ModelConfig[]
  providerName: "plannerProvider" | "imageProvider"
  modelName: "plannerModel" | "imageModel"
}) {
  const { control } = useFormContext<ProjectFormValues>()
  return (
    <Controller
      control={control}
      name={providerName}
      render={({ field: provField }) => (
        <Controller
          control={control}
          name={modelName}
          render={({ field: modField }) => (
            <Select
              value={
                provField.value && modField.value
                  ? `${provField.value}::${modField.value}`
                  : "__default__"
              }
              onValueChange={(v) => {
                if (v === "__default__") {
                  provField.onChange("")
                  modField.onChange("")
                  return
                }
                const sep = v.indexOf("::")
                if (sep < 0) return
                provField.onChange(v.slice(0, sep))
                modField.onChange(v.slice(sep + 2))
              }}
            >
              <SelectTrigger id={triggerId} aria-invalid={false}>
                <SelectValue placeholder="使用组织默认" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">使用组织默认</SelectItem>
                {models.map((m) => {
                  const key = `${m.provider}::${m.model}`
                  return (
                    <SelectItem key={key} value={key}>
                      {m.provider} · {m.model}
                      {m.isDefault ? "（默认）" : ""}
                    </SelectItem>
                  )
                })}
              </SelectContent>
            </Select>
          )}
        />
      )}
    />
  )
}
