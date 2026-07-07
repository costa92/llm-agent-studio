import { beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type { AlertSettings } from "@/lib/types"

// alerts/api 钩子 mock：每个用例通过 query/mutation 容器注入需要的返回值。
const mutate = vi.fn()
const query = {
  value: {
    data: undefined as AlertSettings | undefined,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  },
}
const mutation = { value: { mutate, isPending: false } }

vi.mock("./api", () => ({
  useAlertSettings: () => query.value,
  useUpdateAlertSettings: () => mutation.value,
}))

import { AlertSettingsView } from "./AlertSettingsPage"

const SETTINGS: AlertSettings = {
  orgId: "acme",
  email: "ops@example.com",
  enabled: true,
  budgetEnabled: false,
  budgetThresholdMicros: 0,
  budgetWindowHours: 24,
  stuckEnabled: false,
  stuckThresholdMinutes: 30,
  backlogEnabled: false,
  backlogThreshold: 50,
}

// 全部运营告警关闭时的 payload 片段（阈值解析为 0）。
const OPS_OFF = {
  budgetEnabled: false,
  budgetThresholdMicros: 0,
  budgetWindowHours: 0,
  stuckEnabled: false,
  stuckThresholdMinutes: 0,
  backlogEnabled: false,
  backlogThreshold: 0,
}

function renderView() {
  return render(
    <>
      <AlertSettingsView org="acme" />
      <Toaster />
    </>,
  )
}

beforeEach(() => {
  mutate.mockReset()
  query.value = { data: SETTINGS, isLoading: false, isError: false, refetch: vi.fn() }
  mutation.value = { mutate, isPending: false }
})

describe("AlertSettingsView", () => {
  it("renders fetched settings into the form", () => {
    renderView()
    expect(screen.getByLabelText("告警邮箱")).toHaveValue("ops@example.com")
    expect(screen.getByLabelText("启用 run 失败邮件告警")).toBeChecked()
  })

  it("submits trimmed email + enabled via PUT mutation", async () => {
    const user = userEvent.setup()
    renderView()

    const email = screen.getByLabelText("告警邮箱")
    await user.clear(email)
    await user.type(email, "  alert@corp.io  ")
    await user.click(screen.getByRole("button", { name: "保存告警设置" }))

    await waitFor(() => expect(mutate).toHaveBeenCalledTimes(1))
    expect(mutate.mock.calls[0][0]).toEqual({ email: "alert@corp.io", enabled: true, ...OPS_OFF })
  })

  it("enables budget alert and converts ¥ threshold to micros", async () => {
    const user = userEvent.setup()
    renderView()

    await user.click(screen.getByLabelText("成本超阈告警"))
    const yuan = screen.getByLabelText("成本阈值（元）")
    await user.clear(yuan)
    await user.type(yuan, "50")
    await user.click(screen.getByRole("button", { name: "保存告警设置" }))

    await waitFor(() => expect(mutate).toHaveBeenCalledTimes(1))
    expect(mutate.mock.calls[0][0]).toEqual({
      email: "ops@example.com",
      enabled: true,
      ...OPS_OFF,
      budgetEnabled: true,
      budgetThresholdMicros: 50_000_000,
      budgetWindowHours: 24,
    })
  })

  it("blocks enabling backlog alert with empty threshold", async () => {
    const user = userEvent.setup()
    renderView()

    await user.click(screen.getByLabelText("审核积压告警"))
    // 留空阈值（原生 min 校验只在有值时触发，空值走我们的镜像校验 → toast）。
    await user.clear(screen.getByLabelText("审核积压阈值（条）"))
    await user.click(screen.getByRole("button", { name: "保存告警设置" }))

    await screen.findByText("审核积压告警的阈值需为正整数（条）")
    expect(mutate).not.toHaveBeenCalled()
  })

  it("blocks save when enabled without email (no mutation, toast error)", async () => {
    const user = userEvent.setup()
    renderView()

    // type="email" 的格式错误由浏览器原生校验拦截；留空 + 开启走我们的镜像校验 → toast。
    await user.clear(screen.getByLabelText("告警邮箱"))
    await user.click(screen.getByRole("button", { name: "保存告警设置" }))

    await screen.findByText("开启告警需要有效的告警邮箱")
    expect(mutate).not.toHaveBeenCalled()
  })

  it("blocks save with malformed email (native constraint, no mutation)", async () => {
    const user = userEvent.setup()
    renderView()

    const email = screen.getByLabelText("告警邮箱")
    await user.clear(email)
    await user.type(email, "not-an-email")
    await user.click(screen.getByRole("button", { name: "保存告警设置" }))

    expect(mutate).not.toHaveBeenCalled()
  })

  it("allows disabling with empty email (silence the org)", async () => {
    const user = userEvent.setup()
    renderView()

    await user.clear(screen.getByLabelText("告警邮箱"))
    await user.click(screen.getByLabelText("启用 run 失败邮件告警"))
    await user.click(screen.getByRole("button", { name: "保存告警设置" }))

    await waitFor(() => expect(mutate).toHaveBeenCalledTimes(1))
    expect(mutate.mock.calls[0][0]).toEqual({ email: "", enabled: false, ...OPS_OFF })
  })

  it("disables submit while saving (anti double-click)", () => {
    mutation.value = { mutate, isPending: true }
    renderView()
    expect(screen.getByRole("button", { name: "保存中..." })).toBeDisabled()
  })

  it("shows retry state when the query errors", () => {
    query.value = { data: undefined, isLoading: false, isError: true, refetch: vi.fn() }
    renderView()
    expect(screen.getByText("告警设置加载失败")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "重试" })).toBeInTheDocument()
  })
})
