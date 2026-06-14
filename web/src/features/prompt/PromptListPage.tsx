import { useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { Loader2, Plus, Edit, Trash2, Copy, Check, Wand2 } from "lucide-react"
import { toast } from "sonner"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { Badge } from "@/components/studio/Badge"
import { PromptBox } from "@/components/studio/PromptBox"
import { cn } from "@/lib/utils"
import type { Prompt } from "@/lib/types"
import {
  usePrompts,
  usePromptStyles,
  useCreatePrompt,
  useUpdatePrompt,
  useDeletePrompt,
} from "./api"

const formSchema = z.object({
  name: z.string().min(1, "名称必填"),
  content: z.string().min(1, "提示词内容必填"),
  style: z.string(),
})

interface FormValues {
  name: string
  content: string
  style: string
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

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingPrompt, setEditingPrompt] = useState<Prompt | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Prompt | null>(null)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const {
    register,
    handleSubmit,
    setValue,
    watch,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: "",
      content: "",
      style: "",
    },
  })

  const formValues = watch()
  const selectedStyleObj = styles.data?.find((s) => s.name === formValues.style)
  const previewBuilt = selectedStyleObj
    ? `${formValues.content.trim()}, ${selectedStyleObj.suffix}`
    : formValues.content

  const handleCopy = (id: string, text: string) => {
    navigator.clipboard.writeText(text)
    setCopiedId(id)
    toast.success("已复制到剪贴板")
    setTimeout(() => setCopiedId(null), 2000)
  }

  const openCreateDialog = () => {
    setEditingPrompt(null)
    reset({
      name: "",
      content: "",
      style: "",
    })
    setDialogOpen(true)
  }

  const openEditDialog = (p: Prompt) => {
    setEditingPrompt(p)
    reset({
      name: p.name,
      content: p.content,
      style: p.style,
    })
    setDialogOpen(true)
  }

  const onSubmit = async (values: FormValues) => {
    try {
      if (editingPrompt) {
        await updateMutation.mutateAsync({
          id: editingPrompt.id,
          input: values,
        })
        toast.success("提示词已更新")
      } else {
        await createMutation.mutateAsync(values)
        toast.success("提示词已保存")
      }
      setDialogOpen(false)
    } catch (err: any) {
      toast.error(err.message || "操作失败，请重试")
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await deleteMutation.mutateAsync(deleteTarget.id)
      toast.success("提示词已删除")
      setDeleteTarget(null)
    } catch (err: any) {
      toast.error(err.message || "删除失败，请重试")
    }
  }

  const getBuiltPrompt = (p: Prompt) => {
    const styleObj = styles.data?.find((s) => s.name === p.style)
    return styleObj ? `${p.content.trim()}, ${styleObj.suffix}` : p.content
  }

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex items-center justify-between">
        <div>
          <h1 className="font-heading text-[22px] font-bold text-text-1">提示词管理</h1>
          <p className="mt-1 text-[12.5px] text-text-3">
            在这里保存常用的提示词模板，并可一键附加风格后缀。
          </p>
        </div>
        {!prompts.isLoading && (
          <Button variant="amber" onClick={openCreateDialog}>
            <Plus className="mr-1.5 h-4 w-4" /> 添加提示词
          </Button>
        )}
      </header>

      {prompts.isLoading ? (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-44 rounded-xl" />
          ))}
        </div>
      ) : prompts.isError ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-2">提示词加载失败</p>
          <Button variant="ghost" onClick={() => void prompts.refetch()}>
            重试
          </Button>
        </div>
      ) : (prompts.data?.length ?? 0) === 0 ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center border border-dashed border-line rounded-xl bg-bg-surface">
          <Wand2 className="h-10 w-10 text-text-3 stroke-[1.5]" />
          <p className="text-text-2 font-medium">尚未保存提示词</p>
          <p className="text-text-3 text-xs max-w-sm">
            保存经常使用的基础提示词，即可在创建项目或重新生成时快速复用。
          </p>
          <Button variant="amber" className="mt-2" onClick={openCreateDialog}>
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
                      <UiButton
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-text-3 hover:text-text-1"
                        onClick={() => openEditDialog(p)}
                        aria-label={`编辑 ${p.name}`}
                      >
                        <Edit className="h-3.5 w-3.5" />
                      </UiButton>
                      <UiButton
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-text-3 hover:text-danger"
                        onClick={() => setDeleteTarget(p)}
                        aria-label={`删除 ${p.name}`}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </UiButton>
                    </div>
                  </div>

                  <div className="text-text-2 line-clamp-2 min-h-[36px]">
                    {p.content}
                  </div>

                  {p.style && (
                    <div className="flex flex-wrap gap-1.5">
                      <Badge variant="running">{p.style}</Badge>
                    </div>
                  )}
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
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{editingPrompt ? "编辑提示词" : "添加提示词"}</DialogTitle>
            <DialogDescription>
              配置提示词的名称、基础文本，并可选择附加的渲染风格。
            </DialogDescription>
          </DialogHeader>

          <form onSubmit={handleSubmit(onSubmit)} className="space-y-4 py-2">
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
              <Label className="text-text-2">附加风格</Label>
              {styles.isLoading ? (
                <p className="text-[12.5px] text-text-3">加载风格中…</p>
              ) : (
                <div className="flex flex-wrap gap-1.5">
                  {(styles.data ?? []).map((s) => {
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

            <DialogFooter className="pt-2">
              <UiButton
                type="button"
                variant="outline"
                onClick={() => setDialogOpen(false)}
              >
                取消
              </UiButton>
              <Button variant="amber" type="submit" disabled={isSubmitting}>
                {isSubmitting && <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />}
                保存
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* 删除确认对话框 */}
      <Dialog
        open={deleteTarget != null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认删除该提示词？</DialogTitle>
            <DialogDescription>
              {deleteTarget
                ? `删除提示词「${deleteTarget.name}」后无法撤销。确认要删除吗？`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <UiButton variant="outline" onClick={() => setDeleteTarget(null)}>
              取消
            </UiButton>
            <UiButton
              variant="destructive"
              onClick={handleDelete}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending && (
                <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />
              )}
              确认删除
            </UiButton>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
