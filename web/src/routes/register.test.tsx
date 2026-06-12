import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { ReactNode } from "react"
import { setAccessToken } from "@/lib/apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"
import { AuthProvider } from "@/app/auth"
import { RegisterForm } from "./register"

afterEach(() => {
  setAccessToken(null)
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function wrap(node: ReactNode) {
  return render(<AuthProvider>{node}</AuthProvider>)
}

describe("RegisterForm", () => {
  it("on successful register calls onSuccess", async () => {
    installFetchRoutes({
      "/api/auth/register": () =>
        jsonResponse({ access_token: "tok", expires_in: 900 }),
    })
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    wrap(<RegisterForm onSuccess={onSuccess} />)

    await user.type(screen.getByLabelText("邮箱"), "a@b.com")
    await user.type(screen.getByLabelText("密码"), "password1")
    await user.type(screen.getByLabelText("确认密码"), "password1")
    await user.click(screen.getByRole("button", { name: /注册/ }))

    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
  })

  it("mismatched confirm shows an error and does NOT call register", async () => {
    const mock = installFetchRoutes({
      "/api/auth/register": () =>
        jsonResponse({ access_token: "tok", expires_in: 900 }),
    })
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    wrap(<RegisterForm onSuccess={onSuccess} />)

    await user.type(screen.getByLabelText("邮箱"), "a@b.com")
    await user.type(screen.getByLabelText("密码"), "password1")
    await user.type(screen.getByLabelText("确认密码"), "password2")
    await user.click(screen.getByRole("button", { name: /注册/ }))

    expect(await screen.findByText("两次输入的密码不一致")).toBeInTheDocument()
    // zod 校验拦下，不打注册接口。
    expect(mock).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it("on duplicate email (409) shows the already-registered message", async () => {
    installFetchRoutes({
      "/api/auth/register": () =>
        jsonResponse({ error: "conflict" }, { status: 409 }),
    })
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    wrap(<RegisterForm onSuccess={onSuccess} />)

    await user.type(screen.getByLabelText("邮箱"), "taken@b.com")
    await user.type(screen.getByLabelText("密码"), "password1")
    await user.type(screen.getByLabelText("确认密码"), "password1")
    await user.click(screen.getByRole("button", { name: /注册/ }))

    expect(
      await screen.findByText("该邮箱已注册，请直接登录"),
    ).toBeInTheDocument()
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it("validates password min length before submitting", async () => {
    const mock = installFetchRoutes({
      "/api/auth/register": () =>
        jsonResponse({ access_token: "tok", expires_in: 900 }),
    })
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    wrap(<RegisterForm onSuccess={onSuccess} />)

    await user.type(screen.getByLabelText("邮箱"), "a@b.com")
    await user.type(screen.getByLabelText("密码"), "short")
    await user.type(screen.getByLabelText("确认密码"), "short")
    await user.click(screen.getByRole("button", { name: /注册/ }))

    expect(await screen.findByText("密码至少 8 位")).toBeInTheDocument()
    expect(mock).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
  })
})
