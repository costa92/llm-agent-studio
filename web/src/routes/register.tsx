import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { useState } from "react"
import { Loader2 } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import { useAuth } from "@/app/auth"
import { ApiError } from "@/lib/apiClient"

// 自助注册：rhf+zod 表单 + AuthProvider.register。
// 注册请求体 {email,password}（与后端契约一致）；成功即登录（响应 {access_token,expires_in} + Set-Cookie）。
// 409 邮箱已注册 / 400 入参非法。
export const Route = createFileRoute("/register")({
  component: RegisterPage,
})

const formSchema = z
  .object({
    email: z.string().email("请输入有效的邮箱"),
    password: z.string().min(8, "密码至少 8 位"),
    confirm: z.string().min(1, "请再次输入密码"),
  })
  .refine((v) => v.password === v.confirm, {
    path: ["confirm"],
    message: "两次输入的密码不一致",
  })

type FormValues = z.infer<typeof formSchema>

// 无路由依赖的纯表单组件，便于单测。注册成功调 onSuccess。
export function RegisterForm({ onSuccess }: { onSuccess: () => void }) {
  const { register: registerAccount } = useAuth()
  const [submitError, setSubmitError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: { email: "", password: "", confirm: "" },
  })

  const onSubmit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      await registerAccount(values.email, values.password)
      onSuccess()
    } catch (err) {
      // 409 → 邮箱已注册；其余统一兜底文案。
      if (err instanceof ApiError && err.status === 409) {
        setSubmitError("该邮箱已注册，请直接登录")
      } else {
        setSubmitError("注册失败，请重试")
      }
    }
  })

  return (
    <form onSubmit={onSubmit} className="flex w-72 flex-col gap-4" noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="email">邮箱</Label>
        <Input
          id="email"
          type="email"
          autoComplete="username"
          aria-invalid={errors.email != null}
          {...register("email")}
        />
        {errors.email && (
          <p className="text-[12px] text-danger">{errors.email.message}</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="password">密码</Label>
        <Input
          id="password"
          type="password"
          autoComplete="new-password"
          aria-invalid={errors.password != null}
          {...register("password")}
        />
        {errors.password && (
          <p className="text-[12px] text-danger">{errors.password.message}</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="confirm">确认密码</Label>
        <Input
          id="confirm"
          type="password"
          autoComplete="new-password"
          aria-invalid={errors.confirm != null}
          {...register("confirm")}
        />
        {errors.confirm && (
          <p className="text-[12px] text-danger">{errors.confirm.message}</p>
        )}
      </div>

      {submitError && (
        <p role="alert" className="text-[12px] text-danger">
          {submitError}
        </p>
      )}

      <Button type="submit" variant="amber" disabled={isSubmitting}>
        {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
        注册
      </Button>
    </form>
  )
}

function RegisterPage() {
  const navigate = useNavigate()

  return (
    <div className="grid min-h-screen place-items-center bg-bg-base text-text-1">
      <div className="flex flex-col items-center gap-6">
        <h1 className="font-heading text-[22px] font-bold text-amber">
          AI Studio
        </h1>
        <RegisterForm
          onSuccess={() => {
            // 注册即登录 → 去根落地（org 选择/默认项目列表）。
            navigate({ to: "/" })
          }}
        />
        <p className="text-[12px] text-text-3">
          已有账号？
          <Link to="/login" className="text-amber hover:underline">
            登录
          </Link>
        </p>
      </div>
    </div>
  )
}
