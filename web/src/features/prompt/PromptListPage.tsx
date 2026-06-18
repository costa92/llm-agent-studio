import { useState } from "react"
import { useFormContext } from "react-hook-form"
import { z } from "zod"
import { Plus, Edit, Trash2, Copy, Check, Wand2, Star } from "lucide-react"
import { toast } from "sonner"

import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { Badge } from "@/components/studio/Badge"
import { PromptBox } from "@/components/studio/PromptBox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { cn } from "@/lib/utils"
import type { Prompt } from "@/lib/types"
import {
  usePrompts,
  usePromptStyles,
  useCreatePrompt,
  useUpdatePrompt,
  useDeletePrompt,
  useSetPromptDefault,
} from "./api"
import {
  useCrudResource,
  CrudResourcePage,
  FormDialog,
  ConfirmDialog,
} from "../common/crud"

// 提示词类型标签：''=通用 / "script"=剧本 / "storyboard"=分镜。
const KIND_LABELS: Record<string, string> = {
  "": "通用",
  script: "剧本",
  storyboard: "分镜",
}

const formSchema = z.object({
  name: z.string().min(1, "名称必填"),
  content: z.string().min(1, "提示词内容必填"),
  style: z.string(),
  kind: z.string(),
})

type FormValues = z.infer<typeof formSchema>

// 提取提示词表单字段：通过 useFormContext 读写，styles 从父层传入。
function PromptFields({ styles }: { styles: { name: string; suffix: string }[] | undefined }) {
  const {
    register,
    setValue,
    watch,
    formState: { errors },
  } = useFormContext<FormValues>()

  const formValues = watch()
  const selectedStyleObj = styles?.find((s) => s.name === formValues.style)
  const previewBuilt = selectedStyleObj
    ? `${formValues.content.trim()}, ${selectedStyleObj.suffix}`
    : formValues.content

  return (
    <>
      <div className="space-y-1.5">
        <Label htmlFor="prompt-name" className="text-text-2">
          名称
        </Label>
        <Input
          id="prompt-name"
          placeholder="例如: 国风少女、赛博都市"
          className="border-line bg-bg-base text-text-1"
          {...register("name")}
        />
        {errors.name && (
          <p className="text-xs text-danger">{errors.name.message}</p>
        )}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="prompt-content" className="text-text-2">
          基础 Prompt
        </Label>
        <Textarea
          id="prompt-content"
          placeholder="在此输入主体事物的描述..."
          className="min-h-20 border-line bg-bg-base text-text-1"
          {...register("content")}
        />
        {errors.content && (
          <p className="text-xs text-danger">{errors.content.message}</p>
        )}
      </div>

      <div className="space-y-1.5">
        <Label className="text-text-2">提示词类型</Label>
        {/* Radix Select.Item 不允许空字符串 value，通用(kind="")用 "__general__" 哨兵。 */}
        <Select
          value={formValues.kind || "__general__"}
          onValueChange={(val) =>
            setValue("kind", val === "__general__" ? "" : val)
          }
        >
          <SelectTrigger className="border-line bg-bg-base text-text-1">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__general__">通用</SelectItem>
            <SelectItem value="script">剧本 (script)</SelectItem>
            <SelectItem value="storyboard">分镜 (storyboard)</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-1.5">
        <Label className="text-text-2">附加风格</Label>
        {styles === undefined ? (
          <p className="text-[12.5px] text-text-3">加载风格中…</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {styles.map((s) => {
              const active = s.name === formValues.style
              return (
                <button
                  key={s.name}
                  type="button"
                  onClick={() => setValue("style", active ? "" : s.name)}
                  className={cn(
                    "rounded-full border px-2.5 py-0.5 text-[11px] font-medium transition-colors",
                    active
                      ? "border-amber bg-amber/12 text-amber"
                      : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
                  )}
                >
                  {s.name}
                </button>
              )
            })}
          </div>
        )}
      </div>

      <div className="space-y-1.5 pt-1">
        <Label className="text-text-3 text-[11px]">拼装效果实时预览</Label>
        <PromptBox prompt={previewBuilt} className="bg-bg-base" />
      </div>
    </>
  )
}

interface PromptListPageProps {
  org: string
}

export function PromptListPage({ org }: PromptListPageProps) {
  const prompts = usePrompts(org)
  const styles = usePromptStyles()
  const createMutation = useCreatePrompt(org)
  const updateMutation = useUpdatePrompt(org)
  const deleteMutation = useDeletePrompt(org)
  const setDefaultMutation = useSetPromptDefault(org)

  const [copiedId, setCopiedId] = useState<string | null>(null)

  const crud = useCrudResource<Prompt>({
    getId: (p) => p.id,
    create: (v) => createMutation.mutateAsync(v as FormValues),
    update: (id, v) => updateMutation.mutateAsync({ id, input: v as FormValues }),
    remove: (id) => deleteMutation.mutateAsync(id),
    labels: {
      created: "提示词已保存",
      updated: "提示词已更新",
      deleted: "提示词已删除",
    },
    errorMessage: (_action, err) =>
      err instanceof Error ? err.message : "操作失败，请重试",
  })

  const handleCopy = (id: string, text: string) => {
    navigator.clipboard.writeText(text)
    setCopiedId(id)
    toast.success("已复制到剪贴板")
    setTimeout(() => setCopiedId(null), 2000)
  }

  const handleSetDefault = async (p: Prompt) => {
    try {
      await setDefaultMutation.mutateAsync(p.id)
      toast.success("已设为默认")
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "操作失败，请重试")
    }
  }

  const getBuiltPrompt = (p: Prompt) => {
    const styleObj = styles.data?.find((s) => s.name === p.style)
    return styleObj ? `${p.content.trim()}, ${styleObj.suffix}` : p.content
  }

  const editingTarget = crud.dialog?.mode === "edit" ? crud.dialog.target : null
  const dialogDefaultValues: FormValues = {
    name: editingTarget?.name ?? "",
    content: editingTarget?.content ?? "",
    style: editingTarget?.style ?? "",
    kind: editingTarget?.kind ?? "",
  }

  const isEmpty = (prompts.data?.length ?? 0) === 0

  return (
    <CrudResourcePage
      title="提示词管理"
      description="在这里保存常用的提示词模板，并可一键附加风格后缀。"
      createLabel="添加提示词"
      onCreate={prompts.isLoading ? undefined : crud.openCreate}
      isLoading={prompts.isLoading}
      isError={prompts.isError ?? false}
      onRetry={() => void prompts.refetch()}
      isEmpty={false}
    >
      {isEmpty ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center border border-dashed border-line rounded-xl bg-bg-surface">
          <Wand2 className="h-10 w-10 text-text-3 stroke-[1.5]" />
          <p className="text-text-2 font-medium">尚未保存提示词</p>
          <p className="text-text-3 text-xs max-w-sm">
            保存经常使用的基础提示词，即可在创建项目或重新生成时快速复用。
          </p>
          <Button variant="amber" className="mt-2" onClick={crud.openCreate}>
            <Plus className="mr-1.5 h-4 w-4" /> 添加第一个提示词
          </Button>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {prompts.data?.map((p) => {
            const built = getBuiltPrompt(p)
            return (
              <div
                key={p.id}
                className="flex flex-col justify-between rounded-xl border border-line bg-bg-surface p-4 text-[12.5px] transition-all hover:border-text-3"
              >
                <div className="flex flex-col gap-2.5">
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-semibold text-text-1 text-sm truncate">
                      {p.name}
                    </span>
                    <div className="flex items-center gap-1 shrink-0">
                      {!p.isDefault && (
                        <UiButton
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7 text-text-3 hover:text-amber"
                          onClick={() => void handleSetDefault(p)}
                          disabled={setDefaultMutation.isPending}
                          aria-label={`设为默认 ${p.name}`}
                        >
                          <Star className="h-3.5 w-3.5" />
                        </UiButton>
                      )}
                      <UiButton
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-text-3 hover:text-text-1"
                        onClick={() => crud.openEdit(p)}
                        aria-label={`编辑 ${p.name}`}
                      >
                        <Edit className="h-3.5 w-3.5" />
                      </UiButton>
                      <UiButton
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-text-3 hover:text-danger"
                        onClick={() => crud.requestDelete(p)}
                        aria-label={`删除 ${p.name}`}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </UiButton>
                    </div>
                  </div>

                  <div className="text-text-2 line-clamp-2 min-h-[36px]">
                    {p.content}
                  </div>

                  <div className="flex flex-wrap items-center gap-1.5">
                    <Badge variant="pending">
                      {KIND_LABELS[p.kind] ?? "通用"}
                    </Badge>
                    {p.isDefault && <Badge variant="done">默认</Badge>}
                    {p.style && <Badge variant="running">{p.style}</Badge>}
                  </div>
                </div>

                <div className="mt-4 pt-3 border-t border-line flex flex-col gap-2">
                  <div className="flex items-center justify-between text-[11px] text-text-3">
                    <span>拼装预览</span>
                    <button
                      type="button"
                      onClick={() => handleCopy(p.id, built)}
                      className="flex items-center gap-1 hover:text-text-1"
                      aria-label="复制完整提示词"
                    >
                      {copiedId === p.id ? (
                        <>
                          <Check className="h-3 w-3 text-done" />
                          <span className="text-done">已复制</span>
                        </>
                      ) : (
                        <>
                          <Copy className="h-3 w-3" />
                          <span>复制</span>
                        </>
                      )}
                    </button>
                  </div>
                  <PromptBox prompt={built} className="bg-bg-base/50" />
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* 创建 / 编辑对话框 */}
      <FormDialog<FormValues>
        open={crud.dialog != null}
        mode={crud.dialog?.mode ?? "create"}
        title={crud.dialog?.mode === "edit" ? "编辑提示词" : "添加提示词"}
        schema={formSchema}
        defaultValues={dialogDefaultValues}
        resetKey={editingTarget?.id ?? "create"}
        submitLabel="保存"
        submitting={crud.submitting}
        submitError={crud.submitError}
        onSubmit={(values) => void crud.submit(values)}
        onOpenChange={(open) => { if (!open) crud.closeDialog() }}
      >
        <PromptFields styles={styles.isLoading ? undefined : (styles.data ?? [])} />
      </FormDialog>

      {/* 删除确认对话框 */}
      <ConfirmDialog
        open={crud.deleteTarget != null}
        title="确认删除该提示词？"
        description={
          crud.deleteTarget
            ? `删除提示词「${crud.deleteTarget.name}」后无法撤销。确认要删除吗？`
            : ""
        }
        confirmLabel="确认删除"
        confirming={crud.deleting}
        onConfirm={crud.confirmDelete}
        onCancel={crud.cancelDelete}
      />
    </CrudResourcePage>
  )
}
