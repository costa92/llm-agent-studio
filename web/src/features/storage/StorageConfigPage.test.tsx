import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { StorageConfig } from "@/lib/types"
import { StorageConfigForm, StorageConfigView } from "./StorageConfigPage"

afterEach(() => {
  vi.restoreAllMocks()
})

const CREATED: StorageConfig = {
  id: "sc-org-1",
  scope: "org",
  orgId: "acme",
  mode: "s3",
  endpoint: "https://s3.amazonaws.com",
  region: "us-east-1",
  bucket: "my-bucket",
  accessKeyId: "AKIA",
  publicPrefix: "",
  useSsl: true,
  enabled: true,
  hasSecret: true,
}

describe("StorageConfigForm mode-conditional fields", () => {
  it("hides object-store fields for localfs (only publicPrefix)", () => {
    render(<StorageConfigForm initial={null} onSubmit={vi.fn()} isOrgScope />)
    // localfs（默认）：无 endpoint/bucket/secret，有 publicPrefix。
    expect(screen.queryByLabelText(/Endpoint/)).toBeNull()
    expect(screen.queryByLabelText(/Bucket/)).toBeNull()
    expect(screen.queryByLabelText(/Secret/)).toBeNull()
    expect(screen.getByLabelText(/publicPrefix/)).toBeInTheDocument()
  })

  it("shows endpoint/bucket/accessKey/secret when S3 is selected", async () => {
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={vi.fn()} isOrgScope />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "s3")

    expect(screen.getByLabelText(/Endpoint/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Bucket/)).toBeInTheDocument()
    expect(screen.getByLabelText(/AccessKeyId/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Secret/)).toBeInTheDocument()
  })

  it("shows region (not endpoint-required) for cos", async () => {
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={vi.fn()} isOrgScope />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "cos")
    // cos：region 必填，endpoint 可空。
    expect(screen.getByLabelText(/Region（必填）/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Endpoint（可空）/)).toBeInTheDocument()
  })

  it("blocks submit when S3 bucket/endpoint missing (zod superRefine)", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={onSubmit} isOrgScope />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "s3")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(screen.getByText(/请填写 Bucket/)).toBeInTheDocument())
    expect(screen.getByText(/请填写 Endpoint/)).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("submits localfs with an empty secret (no object fields required)", async () => {
    const onSubmit = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={onSubmit} isOrgScope />)
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ mode: "localfs", secret: "" }),
    )
  })
})

describe("StorageConfigForm write-only secret", () => {
  it("renders the secret as a password field, blank, with keep-blank copy when hasSecret", async () => {
    const user = userEvent.setup()
    render(<StorageConfigForm initial={CREATED} onSubmit={vi.fn()} isOrgScope />)
    // initial.mode=s3 → secret 字段已渲染。
    const secret = screen.getByLabelText(/Secret/)
    expect(secret).toHaveAttribute("type", "password")
    expect(secret).toHaveValue("")
    expect(screen.getByText(/留空保持不变/)).toBeInTheDocument()
    // hasSecret 指示徽标。
    expect(screen.getByText("已配置密钥")).toBeInTheDocument()
    void user
  })

  it("submits secret:'' when the field is untouched (keep existing)", async () => {
    const onSubmit = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    render(<StorageConfigForm initial={CREATED} onSubmit={onSubmit} isOrgScope />)
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    const arg = onSubmit.mock.calls[0][0] as Record<string, unknown>
    expect(arg.secret).toBe("")
    expect(arg).toEqual(
      expect.objectContaining({ mode: "s3", bucket: "my-bucket", endpoint: "https://s3.amazonaws.com" }),
    )
  })

  it("submits the typed secret when filled", async () => {
    const onSubmit = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    render(<StorageConfigForm initial={CREATED} onSubmit={onSubmit} isOrgScope />)
    await user.type(screen.getByLabelText(/Secret/), "new-secret-key")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ secret: "new-secret-key" }),
    )
  })
})

// View：org 区删除确认（仅确认才调 onOrgDelete）+ 全局区渲染。
function renderView(overrides?: {
  orgConfig?: StorageConfig | null
  onOrgDelete?: () => Promise<void>
}) {
  // 注意：用 "orgConfig" in overrides 判定，避免 null ?? CREATED 把显式 null 当未传。
  const orgConfig =
    overrides && "orgConfig" in overrides ? overrides.orgConfig : CREATED
  return render(
    <StorageConfigView
      orgConfig={orgConfig}
      orgLoading={false}
      orgError={false}
      onOrgRetry={vi.fn()}
      onOrgSubmit={vi.fn().mockResolvedValue(CREATED)}
      onOrgDelete={overrides?.onOrgDelete ?? vi.fn().mockResolvedValue(undefined)}
      globalConfig={null}
      globalLoading={false}
      globalError={false}
      onGlobalRetry={vi.fn()}
      onGlobalSubmit={vi.fn().mockResolvedValue(CREATED)}
    />,
  )
}

describe("StorageConfigView", () => {
  it("renders both org and global sections", () => {
    renderView()
    expect(screen.getByText("本组织存储")).toBeInTheDocument()
    expect(screen.getByText("全局默认存储")).toBeInTheDocument()
    // 全局区未配置 → 未配置徽标。
    expect(screen.getByText("未配置")).toBeInTheDocument()
  })

  it("deletes the org config only after confirming, not on cancel", async () => {
    const onOrgDelete = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderView({ onOrgDelete })

    await user.click(screen.getByRole("button", { name: "删除本组织存储配置" }))
    expect(screen.getByText("确认删除本组织存储配置？")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(onOrgDelete).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "删除本组织存储配置" }))
    await user.click(screen.getByRole("button", { name: "确认删除" }))
    await waitFor(() => expect(onOrgDelete).toHaveBeenCalledTimes(1))
  })

  it("disables the org delete button when org config is null", () => {
    renderView({ orgConfig: null })
    expect(screen.getByRole("button", { name: "删除本组织存储配置" })).toBeDisabled()
  })
})
