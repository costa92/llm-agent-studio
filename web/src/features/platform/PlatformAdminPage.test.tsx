import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type {
  PlatformAdmin,
  PlatformOrg,
  PlatformUser,
  StorageConfig,
  UserDetail,
} from "@/lib/types"
import { ApiError } from "@/lib/apiClient"

// platform/api 钩子 mock：每个 describe 用 setHooks 注入需要的返回值。
const grantMutate = vi.fn()
const revokeMutate = vi.fn()
const deleteMutate = vi.fn()
const whoami = { value: { data: true, isLoading: false } as { data?: boolean; isLoading: boolean } }
const orgs = {
  value: { data: [] as PlatformOrg[], isLoading: false, isError: false, refetch: vi.fn() },
}
const admins = {
  value: { data: [] as PlatformAdmin[], isLoading: false, isError: false, refetch: vi.fn() },
}
const users = {
  value: { data: [] as PlatformUser[], isLoading: false, isError: false, refetch: vi.fn() },
}
// 详情按需返回；测试用 setHooks 注入。usePlatformUserDetail(userId) → 仅在 userId 非空时有数据。
const userDetail = {
  value: { data: undefined as UserDetail | undefined, isLoading: false, isError: false },
}

vi.mock("./api", () => ({
  usePlatformWhoami: () => whoami.value,
  usePlatformOrgs: () => orgs.value,
  usePlatformAdmins: () => admins.value,
  usePlatformUsers: () => users.value,
  usePlatformUserDetail: (userId: string | null) =>
    userId == null ? { data: undefined, isLoading: false, isError: false } : userDetail.value,
  useGrantPlatformAdmin: () => ({ mutate: grantMutate, isPending: false }),
  useRevokePlatformAdmin: () => ({ mutate: revokeMutate, isPending: false }),
  useDeleteUser: () => ({ mutate: deleteMutate, isPending: false }),
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

import {
  AllOrgsPage,
  AllUsersPage,
  PlatformGate,
  PlatformSettingsPage,
} from "./PlatformAdminPage"

function renderGate() {
  return render(
    <PlatformGate>
      <div>gate-child</div>
    </PlatformGate>,
  )
}

function renderSettings() {
  return render(
    <>
      <PlatformSettingsPage />
      <Toaster />
    </>,
  )
}

function renderAllOrgs() {
  return render(<AllOrgsPage />)
}

function renderAllUsers() {
  return render(
    <>
      <AllUsersPage />
      <Toaster />
    </>,
  )
}

beforeEach(() => {
  whoami.value = { data: true, isLoading: false }
  orgs.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
  admins.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
  users.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
  userDetail.value = { data: undefined, isLoading: false, isError: false }
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("PlatformGate", () => {
  it("shows permission empty state for non platform admin", () => {
    whoami.value = { data: false, isLoading: false }
    renderGate()
    expect(screen.getByText("需要平台管理员权限")).toBeInTheDocument()
    // 门禁未通过时不渲染子页。
    expect(screen.queryByText("gate-child")).not.toBeInTheDocument()
  })

  it("shows a skeleton while whoami is loading", () => {
    whoami.value = { data: undefined, isLoading: true }
    renderGate()
    expect(screen.queryByText("需要平台管理员权限")).not.toBeInTheDocument()
    expect(screen.queryByText("gate-child")).not.toBeInTheDocument()
  })

  it("renders children for a platform admin", () => {
    renderGate()
    expect(screen.getByText("gate-child")).toBeInTheDocument()
  })
})

describe("PlatformSettingsPage", () => {
  it("renders 全局存储配置 + 平台管理员 but not 全部组织", () => {
    renderSettings()
    expect(screen.getByText("全局存储配置")).toBeInTheDocument()
    expect(screen.getByText("平台管理员")).toBeInTheDocument()
    expect(screen.queryByText("全部组织")).not.toBeInTheDocument()
  })

  it("calls grant with the typed email on 添加", async () => {
    const user = userEvent.setup()
    renderSettings()
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
    renderSettings()

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
    renderSettings()
    const section = screen.getByText("平台管理员").closest("section") as HTMLElement
    expect(within(section).getByText("admin@example.com")).toBeInTheDocument()
  })
})

describe("AllOrgsPage", () => {
  it("renders org rows from usePlatformOrgs with member counts", () => {
    orgs.value = {
      data: [
        { id: "acme", name: "Acme", createdAt: "2026-01-01T00:00:00Z", memberCount: 3 },
        { id: "globex", name: "Globex", createdAt: "2026-02-01T00:00:00Z", memberCount: 1 },
      ],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    renderAllOrgs()
    expect(screen.getByText("Acme")).toBeInTheDocument()
    expect(screen.getByText("globex")).toBeInTheDocument()
    expect(screen.getByText("3")).toBeInTheDocument()
    expect(screen.getByText("1")).toBeInTheDocument()
  })

  it("shows empty state when there are no orgs", () => {
    renderAllOrgs()
    expect(screen.getByText("暂无组织。")).toBeInTheDocument()
  })
})

describe("AllUsersPage", () => {
  const USER_ADMIN: PlatformUser = {
    userId: "u1",
    email: "admin@example.com",
    createdAt: "2026-01-01T00:00:00Z",
    isPlatformAdmin: true,
    orgCount: 2,
  }
  const USER_PLAIN: PlatformUser = {
    userId: "u2",
    email: "alice@example.com",
    createdAt: "2026-02-01T00:00:00Z",
    isPlatformAdmin: false,
    orgCount: 1,
  }

  it("renders user rows from usePlatformUsers", () => {
    users.value = {
      data: [USER_ADMIN, USER_PLAIN],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    renderAllUsers()
    expect(screen.getByText("admin@example.com")).toBeInTheDocument()
    expect(screen.getByText("alice@example.com")).toBeInTheDocument()
  })

  it("opens the detail dialog with org list and soleOrgAdmin warning", async () => {
    users.value = {
      data: [USER_PLAIN],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    userDetail.value = {
      data: {
        userId: "u2",
        email: "alice@example.com",
        createdAt: "2026-02-01T00:00:00Z",
        isPlatformAdmin: false,
        orgs: [
          { orgId: "acme", orgName: "Acme", role: "admin", soleOrgAdmin: true },
          { orgId: "globex", orgName: "Globex", role: "member", soleOrgAdmin: false },
        ],
      },
      isLoading: false,
      isError: false,
    }
    const user = userEvent.setup()
    renderAllUsers()
    await user.click(screen.getByRole("button", { name: "查看 alice@example.com" }))
    expect(screen.getByText("用户详情")).toBeInTheDocument()
    expect(screen.getByText("Acme")).toBeInTheDocument()
    expect(screen.getByText("Globex")).toBeInTheDocument()
    expect(
      screen.getByText(/此用户是以下组织的唯一管理员：Acme/),
    ).toBeInTheDocument()
  })

  it("confirms before delete and calls useDeleteUser with userId", async () => {
    users.value = {
      data: [USER_PLAIN],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    const user = userEvent.setup()
    renderAllUsers()

    await user.click(screen.getByRole("button", { name: "删除 alice@example.com" }))
    expect(screen.getByText("确认删除用户？")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(deleteMutate).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "删除 alice@example.com" }))
    await user.click(screen.getByRole("button", { name: "确认删除" }))
    await waitFor(() => expect(deleteMutate).toHaveBeenCalledTimes(1))
    expect(deleteMutate.mock.calls[0][0]).toBe("u2")
  })

  it("shows 不能删除自己 toast on self-delete 409", async () => {
    users.value = {
      data: [USER_PLAIN],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    // mutate(userId, { onError }) → 触发 onError(ApiError 409 "cannot delete yourself")。
    deleteMutate.mockImplementation(
      (_id: string, opts: { onError: (e: unknown) => void }) => {
        opts.onError(new ApiError(409, "cannot delete yourself"))
      },
    )
    const user = userEvent.setup()
    renderAllUsers()
    await user.click(screen.getByRole("button", { name: "删除 alice@example.com" }))
    await user.click(screen.getByRole("button", { name: "确认删除" }))
    await waitFor(() =>
      expect(screen.getByText("不能删除自己")).toBeInTheDocument(),
    )
  })

  it("toggles admin: grant by email when off, revoke by userId when on", async () => {
    users.value = {
      data: [USER_ADMIN, USER_PLAIN],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    const user = userEvent.setup()
    renderAllUsers()

    // alice 非管理员 → 设为管理员 → grant(by email)。
    await user.click(screen.getByRole("button", { name: "设为管理员 alice@example.com" }))
    expect(grantMutate).toHaveBeenCalledTimes(1)
    expect(grantMutate.mock.calls[0][0]).toBe("alice@example.com")

    // admin 已是管理员 → 取消管理员 → revoke(by userId)。
    await user.click(screen.getByRole("button", { name: "取消管理员 admin@example.com" }))
    expect(revokeMutate).toHaveBeenCalledTimes(1)
    expect(revokeMutate.mock.calls[0][0]).toBe("u1")
  })
})
