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
import { sanitizeLoginRedirect } from "@/app/org"
import { ApiError } from "@/lib/apiClient"
import { ThemeSwitcher } from "@/components/studio/ThemeSwitcher"

// T6：rhf+zod 登录表单 + AuthProvider 接入。
// 登录请求体 {email,password}（与 authz 自带测试一致；Go JSON 大小写不敏感）。
// 登录响应仅 {access_token,expires_in}——无角色对象；角色由 rbac.useRole 探针推断。
const loginSearchSchema = z.object({
  redirect: z.string().optional(),
})

export const Route = createFileRoute("/login")({
  validateSearch: loginSearchSchema,
  component: LoginPage,
})

const formSchema = z.object({
  email: z.string().min(1, "请输入邮箱"),
  password: z.string().min(1, "请输入密码"),
})

type FormValues = z.infer<typeof formSchema>

// 无路由依赖的纯表单组件，便于单测。登录成功调 onSuccess。
export function LoginForm({ onSuccess }: { onSuccess: () => void }) {
  const { login } = useAuth()
  const [submitError, setSubmitError] = useState<string | null>(null)
  const navigate = useNavigate()
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: { email: "", password: "" },
  })

  const onSubmit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      await login(values.email, values.password)
      onSuccess()
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        void navigate({
          to: "/register",
          search: { email: values.email, verify: true },
        })
      } else {
        // 凭据错（401）或其余失败统一映射为该文案（不区分，避免泄露账号是否存在）。
        setSubmitError("邮箱或密码错误，请重试")
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
          autoComplete="current-password"
          aria-invalid={errors.password != null}
          {...register("password")}
        />
        {errors.password && (
          <p className="text-[12px] text-danger">{errors.password.message}</p>
        )}
      </div>

      {submitError && (
        <p role="alert" className="text-[12px] text-danger">
          {submitError}
        </p>
      )}

      <Button type="submit" variant="amber" disabled={isSubmitting}>
        {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
        登录
      </Button>
    </form>
  )
}

function LoginPage() {
  const navigate = useNavigate()
  const { redirect } = Route.useSearch()

  return (
    <div className="grid min-h-screen place-items-center bg-bg-base text-text-1">
      <div className="fixed right-4 top-4 z-10">
        <ThemeSwitcher />
      </div>
      <div className="flex flex-col items-center gap-6">
        <h1 className="font-heading text-[22px] font-bold text-amber">
          AI Studio
        </h1>
        <LoginForm
          onSuccess={() => {
            // 登录成功 → 回跳来源页，否则去根落地（org 选择/默认项目列表）。
            navigate({ to: sanitizeLoginRedirect(redirect) })
          }}
        />
        <p className="text-[12px] text-text-3">
          还没有账号？
          <Link to="/register" className="text-amber hover:underline">
            注册
          </Link>
        </p>
      </div>
    </div>
  )
}
