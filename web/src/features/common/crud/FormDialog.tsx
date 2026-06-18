import { useEffect, type ReactNode } from "react"
import {
  useForm, FormProvider,
  type DefaultValues, type FieldValues, type Resolver,
} from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import type { ZodType } from "zod"
import { Loader2 } from "lucide-react"
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"

interface FormDialogProps<T extends FieldValues> {
  open: boolean
  mode: "create" | "edit"
  title: string
  description?: ReactNode
  contentClassName?: string
  // ZodType<T, T> 让 Input=Output=T，满足 zodResolver v5 对 Input extends FieldValues 的要求。
  schema: ZodType<T, T>
  defaultValues: DefaultValues<T>
  // 资源页传编辑目标的 id（创建模式传常量如 "create"），resetKey 变化时强制 reset，
  // 防止对话框保持挂载/open 时切换编辑目标导致预填陈旧。
  resetKey?: string | number
  submitLabel?: string
  submitting?: boolean
  submitError?: string | null
  onSubmit: (values: T) => void
  onOpenChange: (open: boolean) => void
  children: ReactNode
}

// 表单对话框壳：拥有 Dialog 开合 + rhf FormProvider(zodResolver) + 提交/取消/错误。
// 字段由资源页作为 children 传入，用 useFormContext() 读写。
// open/mode/resetKey 变化时 reset，保证创建↔编辑切换、同类型不同目标切换预填正确。
export function FormDialog<T extends FieldValues>({
  open, mode, title, description, contentClassName, schema, defaultValues, resetKey, submitLabel,
  submitting = false, submitError, onSubmit, onOpenChange, children,
}: FormDialogProps<T>) {
  // zodResolver v5 + zod v4：resolver 类型推断需要显式 cast 桥接泛型边界。
  const resolver = zodResolver(schema) as unknown as Resolver<T>
  const form = useForm<T>({ resolver, defaultValues })
  useEffect(() => {
    if (open) form.reset(defaultValues)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, mode, resetKey])

  const label = submitLabel ?? (mode === "create" ? "创建" : "保存")
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={contentClassName}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description != null && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <FormProvider {...form}>
          <form
            className="flex flex-col gap-3"
            onSubmit={form.handleSubmit((v) => onSubmit(v as T))}
          >
            {children}
            {submitError != null && submitError !== "" && (
              <p className="text-[12px] text-red-400">{submitError}</p>
            )}
            <DialogFooter>
              <UiButton type="button" variant="outline" onClick={() => onOpenChange(false)}>
                取消
              </UiButton>
              <Button type="submit" variant="amber" disabled={submitting}>
                {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
                {label}
              </Button>
            </DialogFooter>
          </form>
        </FormProvider>
      </DialogContent>
    </Dialog>
  )
}
