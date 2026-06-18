import { useEffect, type ReactNode } from "react"
import {
  useForm, FormProvider,
  type DefaultValues, type FieldValues, type Resolver,
} from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import type { ZodType } from "zod"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"

export interface CrudResourcePageProps {
  title: string
  description?: ReactNode
  createLabel?: string
  onCreate?: () => void
  isLoading: boolean
  isError: boolean
  onRetry?: () => void
  isEmpty: boolean
  emptyHint?: string
  headerExtra?: ReactNode
  children: ReactNode
}

// 配置/管理页外壳：页头(标题+描述+新增) + 加载/错误/空态；正常态渲染 children(列表 + 对话框由资源页挂载)。
export function CrudResourcePage({
  title, description, createLabel, onCreate, isLoading, isError, onRetry,
  isEmpty, emptyHint = "暂无数据。", headerExtra, children,
}: CrudResourcePageProps) {
  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1.5">
          <h1 className="font-heading text-[22px] font-bold text-text-1">{title}</h1>
          {description != null && <p className="text-[12px] text-text-3">{description}</p>}
        </div>
        {onCreate && (
          <Button variant="amber" onClick={onCreate}>{createLabel ?? "新增"}</Button>
        )}
      </header>
      {headerExtra}
      {isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">加载失败</p>
          {onRetry && <Button variant="ghost" onClick={onRetry}>重试</Button>}
        </div>
      ) : isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-10 rounded-lg" />)}
        </div>
      ) : isEmpty ? (
        <p className="py-8 text-center text-[13px] text-text-3">{emptyHint}</p>
      ) : (
        children
      )}
    </div>
  )
}

export interface SingletonConfigFormProps<T extends FieldValues> {
  title: string
  description?: ReactNode
  // ZodType<T, T> 让 Input=Output=T，满足 zodResolver v5 对 Input extends FieldValues 的要求。
  schema: ZodType<T, T>
  values: T | undefined
  isLoading: boolean
  submitLabel?: string
  submitting?: boolean
  onSubmit: (values: T) => void
  children: ReactNode
}

// 单记录 upsert 表单（PlatformAdmin 的全局邮件/全局存储用）：拉一条 → 表单 → 保存。
export function SingletonConfigForm<T extends FieldValues>({
  title, description, schema, values, isLoading, submitLabel = "保存",
  submitting = false, onSubmit, children,
}: SingletonConfigFormProps<T>) {
  // zodResolver v5 + zod v4：resolver 类型推断需要显式 cast 桥接泛型边界。
  const resolver = zodResolver(schema) as unknown as Resolver<T>
  const form = useForm<T>({ resolver, defaultValues: values as DefaultValues<T> })
  useEffect(() => {
    if (values && !form.formState.isDirty) form.reset(values)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [values])
  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">{title}</h2>
        {description != null && <p className="text-[12px] text-text-3">{description}</p>}
      </header>
      {isLoading ? (
        <Skeleton className="h-24 rounded-lg" />
      ) : (
        <FormProvider {...form}>
          <form className="flex flex-col gap-3" onSubmit={form.handleSubmit((v) => onSubmit(v))}>
            {children}
            <div className="flex justify-end">
              <Button type="submit" variant="amber" disabled={submitting}>{submitLabel}</Button>
            </div>
          </form>
        </FormProvider>
      )}
    </section>
  )
}
