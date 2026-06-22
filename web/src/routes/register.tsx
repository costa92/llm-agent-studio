import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { useState } from "react"
import { Loader2 } from "lucide-react"
import { toast } from "sonner"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import { useAuth } from "@/app/auth"
import { ApiError } from "@/lib/apiClient"
import { ThemeSwitcher } from "@/components/studio/ThemeSwitcher"

// 自助注册：rhf+zod 表单 + AuthProvider.register。
// 注册请求体 {email,password}（与后端契约一致）；成功即登录（响应 {access_token,expires_in} + Set-Cookie）。
// 409 邮箱已注册 / 400 入参非法。
const registerSearchSchema = z.object({
  email: z.string().optional(),
  verify: z.boolean().or(z.string().transform(v => v === "true")).optional(),
})

export const Route = createFileRoute("/register")({
  validateSearch: registerSearchSchema,
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
export function RegisterForm({
  onSuccess,
  initialEmail = "",
  initialStep = "register",
}: {
  onSuccess: () => void
  initialEmail?: string
  initialStep?: "register" | "verify"
}) {
  const { register: registerAccount, verify: verifyCode, resendVerification } = useAuth()
  const [step, setStep] = useState<"register" | "verify">(initialStep)
  const [emailToVerify, setEmailToVerify] = useState(initialEmail)
  const [verificationCode, setVerificationCode] = useState("")
  const [verifyError, setVerifyError] = useState<string | null>(null)
  const [isVerifying, setIsVerifying] = useState(false)
  const [resendCooldown, setResendCooldown] = useState(0)

  const [submitError, setSubmitError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: { email: initialEmail, password: "", confirm: "" },
  })

  const onSubmit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const res = await registerAccount(values.email, values.password)
      setEmailToVerify(values.email)
      if (res.verified) {
        onSuccess()
      } else {
        setStep("verify")
      }
    } catch (err) {
      // 409 → 邮箱已注册；其余统一兜底文案。
      if (err instanceof ApiError && err.status === 409) {
        setSubmitError("该邮箱已注册，请直接登录")
      } else {
        setSubmitError("注册失败，请重试")
      }
    }
  })

  const handleVerify = async (e: React.FormEvent) => {
    e.preventDefault()
    setVerifyError(null)
    if (verificationCode.length !== 6 || !/^\d+$/.test(verificationCode)) {
      setVerifyError("请输入 6 位数字验证码")
      return
    }

    setIsVerifying(true)
    try {
      await verifyCode(emailToVerify, verificationCode)
      onSuccess()
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        setVerifyError("验证码错误或已过期，请重新获取")
      } else {
        setVerifyError("验证失败，请重试")
      }
    } finally {
      setIsVerifying(false)
    }
  }

  const handleResend = async () => {
    try {
      await resendVerification(emailToVerify)
      toast.success("验证码已重新发送")
      setResendCooldown(60)
      const timer = setInterval(() => {
        setResendCooldown((prev) => {
          if (prev <= 1) {
            clearInterval(timer)
            return 0
          }
          return prev - 1
        })
      }, 1000)
    } catch {
      toast.error("发送失败，请重试")
    }
  }

  if (step === "verify") {
    return (
      <form onSubmit={handleVerify} className="flex w-72 flex-col gap-4" noValidate>
        <div className="flex flex-col gap-1 text-center">
          <p className="text-[13px] text-text-2">
            我们已向您的邮箱发送了验证码：
          </p>
          <p className="text-[13px] font-medium text-text-1 break-all">
            {emailToVerify}
          </p>
        </div>

        <div className="flex flex-col gap-1.5 mt-2">
          <Label htmlFor="verification-code">6 位数字验证码</Label>
          <Input
            id="verification-code"
            type="text"
            inputMode="numeric"
            pattern="[0-9]*"
            maxLength={6}
            placeholder="000000"
            autoComplete="one-time-code"
            value={verificationCode}
            onChange={(e) => setVerificationCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
            className="text-center font-mono text-lg tracking-widest"
          />
          {verifyError && (
            <p role="alert" className="text-[12px] text-danger mt-1">
              {verifyError}
            </p>
          )}
        </div>

        <Button type="submit" variant="amber" disabled={isVerifying || verificationCode.length !== 6}>
          {isVerifying && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          激活并登录
        </Button>

        <div className="flex justify-between items-center text-[12px] mt-2">
          <button
            type="button"
            onClick={() => setStep("register")}
            className="text-text-3 hover:underline"
          >
            返回注册
          </button>
          
          <button
            type="button"
            disabled={resendCooldown > 0}
            onClick={handleResend}
            className="text-amber hover:underline disabled:text-text-3 disabled:no-underline"
          >
            {resendCooldown > 0 ? `${resendCooldown} 秒后可重发` : "重新发送验证码"}
          </button>
        </div>
      </form>
    )
  }

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
  const { email, verify } = Route.useSearch()

  return (
    <div className="grid min-h-screen place-items-center bg-bg-base text-text-1">
      <div className="fixed right-4 top-4 z-10">
        <ThemeSwitcher />
      </div>
      <div className="flex flex-col items-center gap-6">
        <h1 className="font-heading text-[22px] font-bold text-amber">
          AI Studio
        </h1>
        <RegisterForm
          initialEmail={email}
          initialStep={verify ? "verify" : "register"}
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
