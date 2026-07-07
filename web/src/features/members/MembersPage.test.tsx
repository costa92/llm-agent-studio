import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type { OrgInvite, OrgMember } from "@/lib/types"
import { ApiError } from "@/lib/apiClient"

// members/api 钩子 mock：每个 it 用 setHooks 注入需要的返回值。
const addMutate = vi.fn()
const setRoleMutate = vi.fn()
const removeMutate = vi.fn()
const removeMutateAsync = vi.fn().mockResolvedValue({ ok: true })
const createInviteMutate = vi.fn()
const revokeInviteMutate = vi.fn()
const members = {
  value: { data: [] as OrgMember[], isLoading: false, isError: false, refetch: vi.fn() },
}
const invites = {
  value: { data: [] as OrgInvite[], isLoading: false, isError: false, refetch: vi.fn() },
}

vi.mock("./api", () => ({
  useOrgMembers: () => members.value,
  useAddMember: () => ({ mutate: addMutate, isPending: false }),
  useSetMemberRole: () => ({ mutate: setRoleMutate, isPending: false }),
  useRemoveMember: () => ({ mutate: removeMutate, mutateAsync: removeMutateAsync, isPending: false }),
  useOrgInvites: () => invites.value,
  useCreateInvite: () => ({ mutate: createInviteMutate, isPending: false }),
  useRevokeInvite: () => ({ mutate: revokeInviteMutate, isPending: false }),
}))

import { MembersPage } from "./MembersPage"

function renderPage() {
  return render(
    <>
      <MembersPage org="acme" />
      <Toaster />
    </>,
  )
}

const ALICE: OrgMember = { userId: "u1", email: "alice@example.com", role: "viewer" }
const BOB: OrgMember = { userId: "u2", email: "bob@example.com", role: "admin" }

const PENDING_INVITE: OrgInvite = {
  id: "iv1",
  orgId: "acme",
  email: "invitee@example.com",
  role: "editor",
  token: "tok-abc",
  status: "pending",
  invitedBy: "u0",
  createdAt: "2026-07-07T00:00:00Z",
  expiresAt: "2026-07-21T00:00:00Z",
}

beforeEach(() => {
  members.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
  invites.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("MembersPage", () => {
  it("renders member rows from useOrgMembers", () => {
    members.value = { data: [ALICE, BOB], isLoading: false, isError: false, refetch: vi.fn() }
    renderPage()
    expect(screen.getByText("alice@example.com")).toBeInTheDocument()
    expect(screen.getByText("bob@example.com")).toBeInTheDocument()
  })

  it("shows empty state when there are no members", () => {
    renderPage()
    expect(screen.getByText("暂无成员。")).toBeInTheDocument()
  })

  it("calls useAddMember with {email, role} on 添加", async () => {
    const user = userEvent.setup()
    renderPage()
    await user.type(screen.getByLabelText("按邮箱添加"), "new@example.com")
    await user.selectOptions(screen.getByLabelText("角色"), "editor")
    await user.click(screen.getByRole("button", { name: "添加" }))
    expect(addMutate).toHaveBeenCalledTimes(1)
    expect(addMutate.mock.calls[0][0]).toEqual({ email: "new@example.com", role: "editor" })
  })

  it("calls useSetMemberRole with {userId, role} on inline role change", async () => {
    members.value = { data: [ALICE], isLoading: false, isError: false, refetch: vi.fn() }
    const user = userEvent.setup()
    renderPage()
    await user.selectOptions(
      screen.getByLabelText("角色 alice@example.com"),
      "editor",
    )
    expect(setRoleMutate).toHaveBeenCalledTimes(1)
    expect(setRoleMutate.mock.calls[0][0]).toEqual({ userId: "u1", role: "editor" })
  })

  it("confirms before remove and calls useRemoveMember with userId", async () => {
    members.value = { data: [ALICE], isLoading: false, isError: false, refetch: vi.fn() }
    const user = userEvent.setup()
    renderPage()

    await user.click(screen.getByRole("button", { name: `移除成员 ${ALICE.email}` }))
    // 确认弹窗出现；取消则不调 remove。
    expect(screen.getByText("确认移除成员？")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(removeMutateAsync).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: `移除成员 ${ALICE.email}` }))
    await user.click(screen.getByRole("button", { name: "确认移除" }))
    await waitFor(() => expect(removeMutateAsync).toHaveBeenCalledTimes(1))
    expect(removeMutateAsync.mock.calls[0][0]).toBe("u1")
  })

  it("shows last-admin toast on remove 409", async () => {
    members.value = { data: [BOB], isLoading: false, isError: false, refetch: vi.fn() }
    // mutateAsync(userId) → rejects with ApiError 409。
    removeMutateAsync.mockRejectedValueOnce(new ApiError(409, "cannot remove or demote the last org admin"))
    const user = userEvent.setup()
    renderPage()
    await user.click(screen.getByRole("button", { name: `移除成员 ${BOB.email}` }))
    await user.click(screen.getByRole("button", { name: "确认移除" }))
    await waitFor(() =>
      expect(
        screen.getByText("不能移除或降级最后一个组织管理员"),
      ).toBeInTheDocument(),
    )
  })

  it("calls useCreateInvite with {email, role} on 邀请", async () => {
    const user = userEvent.setup()
    renderPage()
    await user.type(screen.getByLabelText("按邮箱邀请"), "invitee@example.com")
    await user.selectOptions(screen.getByLabelText("邀请角色"), "editor")
    await user.click(screen.getByRole("button", { name: "邀请" }))
    expect(createInviteMutate).toHaveBeenCalledTimes(1)
    expect(createInviteMutate.mock.calls[0][0]).toEqual({
      email: "invitee@example.com",
      role: "editor",
    })
  })

  it("renders pending invites and revokes on 撤销", async () => {
    invites.value = { data: [PENDING_INVITE], isLoading: false, isError: false, refetch: vi.fn() }
    const user = userEvent.setup()
    renderPage()
    expect(screen.getByText("invitee@example.com")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "撤销" }))
    expect(revokeInviteMutate).toHaveBeenCalledTimes(1)
    expect(revokeInviteMutate.mock.calls[0][0]).toBe("iv1")
  })

  it("shows already-member toast on invite 409", async () => {
    createInviteMutate.mockImplementation(
      (_input: unknown, opts: { onError: (e: unknown) => void }) => {
        opts.onError(new ApiError(409, "email is already a member"))
      },
    )
    const user = userEvent.setup()
    renderPage()
    await user.type(screen.getByLabelText("按邮箱邀请"), "dup@example.com")
    await user.click(screen.getByRole("button", { name: "邀请" }))
    await waitFor(() =>
      expect(screen.getByText("该邮箱已是本组织成员")).toBeInTheDocument(),
    )
  })

  it("shows 用户不存在 toast on add 404", async () => {
    // mutate(input, { onError }) → 触发 onError(ApiError 404)。
    addMutate.mockImplementation(
      (_input: unknown, opts: { onError: (e: unknown) => void }) => {
        opts.onError(new ApiError(404, "user not found"))
      },
    )
    const user = userEvent.setup()
    renderPage()
    await user.type(screen.getByLabelText("按邮箱添加"), "ghost@example.com")
    await user.click(screen.getByRole("button", { name: "添加" }))
    await waitFor(() =>
      expect(screen.getByText("用户不存在")).toBeInTheDocument(),
    )
  })
})
