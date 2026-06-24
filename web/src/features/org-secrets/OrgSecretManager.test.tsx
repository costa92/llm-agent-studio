import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type { OrgSecret } from "@/lib/types"

// --- mock api hooks -------------------------------------------------------
const createMutateAsync = vi.fn()
const updateMutateAsync = vi.fn()
const deleteMutateAsync = vi.fn()

const queryState = {
  value: {
    data: [] as OrgSecret[],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  },
}

vi.mock("./api", () => ({
  useOrgSecrets: () => queryState.value,
  useCreateOrgSecret: () => ({
    mutate: vi.fn(),
    mutateAsync: createMutateAsync,
    isPending: false,
  }),
  useUpdateOrgSecret: () => ({
    mutate: vi.fn(),
    mutateAsync: updateMutateAsync,
    isPending: false,
  }),
  useDeleteOrgSecret: () => ({
    mutate: vi.fn(),
    mutateAsync: deleteMutateAsync,
    isPending: false,
  }),
}))

import { OrgSecretManager } from "./OrgSecretManager"

const SECRET_A: OrgSecret = { id: "sec-1", orgId: "acme", name: "PARTNER_KEY", hasValue: true }
const SECRET_B: OrgSecret = { id: "sec-2", orgId: "acme", name: "EMPTY_KEY", hasValue: false }

function renderManager() {
  return render(
    <>
      <OrgSecretManager org="acme" />
      <Toaster />
    </>,
  )
}

beforeEach(() => {
  queryState.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
  createMutateAsync.mockResolvedValue(SECRET_A)
  updateMutateAsync.mockResolvedValue(SECRET_A)
  deleteMutateAsync.mockResolvedValue({ ok: true })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("OrgSecretManager", () => {
  it("renders secret rows with name + hasValue badge", () => {
    queryState.value = { data: [SECRET_A, SECRET_B], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()
    expect(screen.getByText("PARTNER_KEY")).toBeInTheDocument()
    expect(screen.getByText("EMPTY_KEY")).toBeInTheDocument()
    // 已设置 badge 仅在 hasValue 行出现。
    expect(screen.getAllByText(/已设置/).length).toBe(1)
  })

  it("shows empty state when there are no secrets", () => {
    renderManager()
    expect(screen.getByText(/暂无密钥/)).toBeInTheDocument()
  })

  it("新建 opens a dialog with name + value(password) inputs", async () => {
    const user = userEvent.setup()
    renderManager()
    await user.click(screen.getByRole("button", { name: /新建密钥/ }))
    expect(screen.getByRole("dialog")).toBeInTheDocument()
    const valueInput = screen.getByLabelText(/密钥值/)
    expect(valueInput).toHaveAttribute("type", "password")
  })

  it("submitting 新建 calls create mutate with {name, value}", async () => {
    const user = userEvent.setup()
    renderManager()
    await user.click(screen.getByRole("button", { name: /新建密钥/ }))

    await user.clear(screen.getByLabelText(/名称/))
    await user.type(screen.getByLabelText(/名称/), "PARTNER_KEY")
    await user.type(screen.getByLabelText(/密钥值/), "s3cr3t")
    await user.click(screen.getByRole("button", { name: /创建/ }))

    await waitFor(() => {
      expect(createMutateAsync).toHaveBeenCalledTimes(1)
    })
    expect(createMutateAsync.mock.calls[0][0]).toEqual({ name: "PARTNER_KEY", value: "s3cr3t" })
  })

  it("editing submits {name, value} keyed by name; empty value keeps existing (helper text)", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [SECRET_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /编辑/ }))
    expect(screen.getByRole("dialog")).toBeInTheDocument()
    // 编辑态有「留空保留原值」提示。
    expect(screen.getByText(/留空保留原值/)).toBeInTheDocument()
    // 不改 value（留空）直接保存。
    await user.click(screen.getByRole("button", { name: /保存/ }))

    await waitFor(() => {
      expect(updateMutateAsync).toHaveBeenCalledTimes(1)
    })
    expect(updateMutateAsync.mock.calls[0][0]).toEqual({
      name: "PARTNER_KEY",
      input: { name: "PARTNER_KEY", value: "" },
    })
  })

  it("delete calls delete mutate", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [SECRET_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /删除/ }))
    await user.click(screen.getByRole("button", { name: /确认删除/ }))

    await waitFor(() => {
      expect(deleteMutateAsync).toHaveBeenCalledWith("PARTNER_KEY")
    })
  })

  it("never renders the secret value back (write-only)", () => {
    queryState.value = { data: [SECRET_A], isLoading: false, isError: false, refetch: vi.fn() }
    const { container } = renderManager()
    // 列表里不出现任何明文密钥字段——只展示 name + hasValue。
    expect(container.textContent).not.toContain("s3cr3t")
  })
})
