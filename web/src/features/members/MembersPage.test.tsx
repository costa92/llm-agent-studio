import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type { OrgMember } from "@/lib/types"
import { ApiError } from "@/lib/apiClient"

// members/api 钩子 mock：每个 it 用 setHooks 注入需要的返回值。
const addMutate = vi.fn()
const setRoleMutate = vi.fn()
const removeMutate = vi.fn()
const removeMutateAsync = vi.fn().mockResolvedValue({ ok: true })
const members = {
  value: { data: [] as OrgMember[], isLoading: false, isError: false, refetch: vi.fn() },
}

vi.mock("./api", () => ({
  useOrgMembers: () => members.value,
  useAddMember: () => ({ mutate: addMutate, isPending: false }),
  useSetMemberRole: () => ({ mutate: setRoleMutate, isPending: false }),
  useRemoveMember: () => ({ mutate: removeMutate, mutateAsync: removeMutateAsync, isPending: false }),
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

beforeEach(() => {
  members.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
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
    expect(screen.getByText("还没有成员，通过上方表单添加第一个成员。")).toBeInTheDocument()
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

    await user.click(screen.getAllByRole("button", { name: "移除" })[0])
    // 确认弹窗出现；取消则不调 remove。
    expect(screen.getByText("确认移除成员？")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(removeMutateAsync).not.toHaveBeenCalled()

    await user.click(screen.getAllByRole("button", { name: "移除" })[0])
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
    await user.click(screen.getAllByRole("button", { name: "移除" })[0])
    await user.click(screen.getByRole("button", { name: "确认移除" }))
    await waitFor(() =>
      expect(
        screen.getByText("不能移除或降级最后一个组织管理员"),
      ).toBeInTheDocument(),
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
