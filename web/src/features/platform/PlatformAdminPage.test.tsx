import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type { PlatformAdmin, PlatformOrg, StorageConfig } from "@/lib/types"

// platform/api 钩子 mock：每个 describe 用 setHooks 注入需要的返回值。
const grantMutate = vi.fn()
const revokeMutate = vi.fn()
const whoami = { value: { data: true, isLoading: false } as { data?: boolean; isLoading: boolean } }
const orgs = {
  value: { data: [] as PlatformOrg[], isLoading: false, isError: false, refetch: vi.fn() },
}
const admins = {
  value: { data: [] as PlatformAdmin[], isLoading: false, isError: false, refetch: vi.fn() },
}

vi.mock("./api", () => ({
  usePlatformWhoami: () => whoami.value,
  usePlatformOrgs: () => orgs.value,
  usePlatformAdmins: () => admins.value,
  useGrantPlatformAdmin: () => ({ mutate: grantMutate, isPending: false }),
  useRevokePlatformAdmin: () => ({ mutate: revokeMutate, isPending: false }),
}))

// 全局存储钩子 mock（GlobalStorageSection 用）：返回已配置 config，不发请求。
const GLOBAL: StorageConfig = {
  id: "sc-global-1",
  scope: "global",
  orgId: "",
  mode: "s3",
  endpoint: "https://s3.amazonaws.com",
  region: "us-east-1",
  bucket: "global-bucket",
  accessKeyId: "AKIA",
  publicPrefix: "",
  useSsl: true,
  enabled: true,
  hasSecret: true,
}
vi.mock("@/features/storage/api", () => ({
  useGlobalStorageConfig: () => ({
    data: GLOBAL,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
  useUpsertGlobalStorageConfig: () => ({
    mutateAsync: vi.fn().mockResolvedValue(GLOBAL),
  }),
}))

import { PlatformAdminPage } from "./PlatformAdminPage"

function renderPage() {
  return render(
    <>
      <PlatformAdminPage />
      <Toaster />
    </>,
  )
}

beforeEach(() => {
  whoami.value = { data: true, isLoading: false }
  orgs.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
  admins.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("PlatformAdminPage guard", () => {
  it("shows permission empty state for non platform admin", () => {
    whoami.value = { data: false, isLoading: false }
    renderPage()
    expect(screen.getByText("需要平台管理员权限")).toBeInTheDocument()
    // 三段标题不应渲染。
    expect(screen.queryByText("全局存储配置")).not.toBeInTheDocument()
  })

  it("shows a skeleton while whoami is loading", () => {
    whoami.value = { data: undefined, isLoading: true }
    renderPage()
    expect(screen.queryByText("需要平台管理员权限")).not.toBeInTheDocument()
    expect(screen.queryByText("全局存储配置")).not.toBeInTheDocument()
  })

  it("renders the 3 sections for a platform admin", () => {
    renderPage()
    expect(screen.getByText("全局存储配置")).toBeInTheDocument()
    expect(screen.getByText("全部组织")).toBeInTheDocument()
    expect(screen.getByText("平台管理员")).toBeInTheDocument()
  })
})

describe("PlatformAdminPage orgs table", () => {
  it("renders org rows from usePlatformOrgs", () => {
    orgs.value = {
      data: [
        { id: "acme", name: "Acme", createdAt: "2026-01-01T00:00:00Z", memberCount: 3 },
        { id: "globex", name: "Globex", createdAt: "2026-02-01T00:00:00Z", memberCount: 1 },
      ],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    renderPage()
    expect(screen.getByText("Acme")).toBeInTheDocument()
    expect(screen.getByText("globex")).toBeInTheDocument()
    expect(screen.getByText("3")).toBeInTheDocument()
  })

  it("shows empty state when there are no orgs", () => {
    renderPage()
    expect(screen.getByText("暂无组织。")).toBeInTheDocument()
  })
})

describe("PlatformAdminPage admins", () => {
  it("calls grant with the typed email on 添加", async () => {
    const user = userEvent.setup()
    renderPage()
    await user.type(screen.getByLabelText("按邮箱添加"), "new@example.com")
    await user.click(screen.getByRole("button", { name: "添加" }))
    expect(grantMutate).toHaveBeenCalledTimes(1)
    expect(grantMutate.mock.calls[0][0]).toBe("new@example.com")
  })

  it("confirms before revoke and calls revoke with userId", async () => {
    admins.value = {
      data: [{ userId: "u1", email: "admin@example.com" }],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    const user = userEvent.setup()
    renderPage()

    await user.click(
      screen.getByRole("button", { name: "移除平台管理员 admin@example.com" }),
    )
    // 确认弹窗出现；取消则不调 revoke。
    expect(screen.getByText("确认移除平台管理员？")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(revokeMutate).not.toHaveBeenCalled()

    await user.click(
      screen.getByRole("button", { name: "移除平台管理员 admin@example.com" }),
    )
    await user.click(screen.getByRole("button", { name: "确认移除" }))
    await waitFor(() => expect(revokeMutate).toHaveBeenCalledTimes(1))
    expect(revokeMutate.mock.calls[0][0]).toBe("u1")
  })

  it("renders the admins list", () => {
    admins.value = {
      data: [{ userId: "u1", email: "admin@example.com" }],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    renderPage()
    const section = screen.getByText("平台管理员").closest("section") as HTMLElement
    expect(within(section).getByText("admin@example.com")).toBeInTheDocument()
  })
})
