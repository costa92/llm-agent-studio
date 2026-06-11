import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { ReactNode } from "react"
import { setAccessToken } from "@/lib/apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"
import { AuthProvider } from "@/app/auth"
import { LoginForm } from "./login"

afterEach(() => {
  setAccessToken(null)
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function wrap(node: ReactNode) {
  return render(<AuthProvider>{node}</AuthProvider>)
}

describe("LoginForm", () => {
  it("on successful login calls onSuccess", async () => {
    installFetchRoutes({
      "/api/auth/login": () =>
        jsonResponse({ access_token: "tok", expires_in: 900 }),
    })
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    wrap(<LoginForm onSuccess={onSuccess} />)

    await user.type(screen.getByLabelText("邮箱"), "a@b.com")
    await user.type(screen.getByLabelText("密码"), "secret")
    await user.click(screen.getByRole("button", { name: /登录/ }))

    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
  })

  it("on bad credentials shows an error and does not call onSuccess", async () => {
    installFetchRoutes({
      "/api/auth/login": () =>
        jsonResponse({ error: "invalid" }, { status: 401 }),
    })
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    wrap(<LoginForm onSuccess={onSuccess} />)

    await user.type(screen.getByLabelText("邮箱"), "a@b.com")
    await user.type(screen.getByLabelText("密码"), "wrong")
    await user.click(screen.getByRole("button", { name: /登录/ }))

    expect(await screen.findByText("邮箱或密码错误，请重试")).toBeInTheDocument()
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it("validates required fields before submitting", async () => {
    const mock = installFetchRoutes({
      "/api/auth/login": () =>
        jsonResponse({ access_token: "tok", expires_in: 900 }),
    })
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    wrap(<LoginForm onSuccess={onSuccess} />)

    await user.click(screen.getByRole("button", { name: /登录/ }))

    // zod 校验拦下空提交，不打登录接口。
    expect(mock).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
  })
})
