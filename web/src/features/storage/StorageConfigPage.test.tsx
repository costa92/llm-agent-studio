import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor, fireEvent } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { StorageConfig } from "@/lib/types"
import { StorageConfigForm, StorageConfigsTable } from "./StorageConfigPage"

afterEach(() => {
  vi.restoreAllMocks()
})

const CREATED: StorageConfig = {
  id: "sc-org-1",
  name: "primary",
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
  isDefault: true,
  hasSecret: true,
}

// github 既有配置：owner=accessKeyId / repo=bucket / branch=region。
const GH_CREATED: StorageConfig = {
  id: "sc-org-gh",
  name: "github-store",
  scope: "org",
  orgId: "acme",
  mode: "github",
  endpoint: "",
  region: "main",
  bucket: "my-assets",
  accessKeyId: "acme-org",
  publicPrefix: "assets",
  useSsl: true,
  enabled: true,
  isDefault: false,
  hasSecret: true,
}

describe("StorageConfigForm mode-conditional fields", () => {
  it("hides object-store fields for localfs (only publicPrefix)", () => {
    render(<StorageConfigForm initial={null} onSubmit={vi.fn()} isOrgScope />)
    // localfs（默认）：无 endpoint/bucket/secret，有 publicPrefix。
    expect(screen.queryByLabelText(/Endpoint/)).toBeNull()
    expect(screen.queryByLabelText(/Bucket/)).toBeNull()
    // RevealSecretInput 使用 aria-label="密钥输入"，localfs 不渲染密钥字段故为 null。
    expect(screen.queryByLabelText("密钥输入")).toBeNull()
    expect(screen.getByLabelText(/publicPrefix/)).toBeInTheDocument()
  })

  it("shows endpoint/bucket/accessKey/secret when S3 is selected", async () => {
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={vi.fn()} isOrgScope />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "s3")

    expect(screen.getByLabelText(/Endpoint/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Bucket/)).toBeInTheDocument()
    expect(screen.getByLabelText(/AccessKeyId/)).toBeInTheDocument()
    // RevealSecretInput aria-label="密钥输入"（取代原 label+htmlFor 的 /Secret/ 关联）。
    expect(screen.getByLabelText("密钥输入")).toBeInTheDocument()
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

  it("shows Owner/Repo/Branch/Token and hides s3-only blocks for github", async () => {
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={vi.fn()} isOrgScope />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "github")

    expect(screen.getByLabelText(/Owner/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Repo/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Branch/)).toBeInTheDocument()
    // RevealSecretInput aria-label="密钥输入" 取代原 /Token/ label 关联。
    expect(screen.getByLabelText("密钥输入")).toBeInTheDocument()
    // 隐藏 s3-only 区块：Endpoint（必填/可空标签）与 useSsl 复选框。
    expect(screen.queryByLabelText(/Endpoint/)).toBeNull()
    expect(screen.queryByLabelText(/使用 SSL/)).toBeNull()
  })

  it("blocks submit when github owner/repo missing (zod superRefine)", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={onSubmit} isOrgScope />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "github")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(screen.getByText(/请填写 Owner/)).toBeInTheDocument())
    expect(screen.getByText(/请填写 Repo/)).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  // 真实生产事故：把 jsDelivr CDN / raw.githubusercontent 直链误填进 github 模式的「API base」
  // ——后端在它后面拼 /repos/.../contents/...，URL 形态错位 + CDN 不可写，asset 6/6 失败。
  // helperText 必须把"这是 GitHub API 根，不是 CDN/直链"显式说清，且接受/拒绝模式要可预期。
  it("renders the github API-base helper text calling out GitHub API root (not CDN/raw)", async () => {
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={vi.fn()} isOrgScope />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "github")
    // 在 API base input 之后那一条 helper 提示，必须同时含 "API 根" 与
    // "jsDelivr / raw.githubusercontent" 三个明确信号——单点关键词拼写错了都能抓到。
    const apiInput = screen.getByLabelText(/API base/)
    const hint = apiInput.parentElement?.querySelector("p")
    expect(hint).not.toBeNull()
    const text = hint?.textContent ?? ""
    expect(text).toMatch(/API 根/)
    expect(text).toMatch(/jsDelivr/)
    expect(text).toMatch(/raw\.githubusercontent/)
  })

  it("submits localfs with an empty secret (no object fields required)", async () => {
    const onSubmit = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    render(<StorageConfigForm initial={null} onSubmit={onSubmit} isOrgScope />)
    // name 字段为必填，先填写配置名称
    await user.type(screen.getByLabelText(/配置名称/), "test-config")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ mode: "localfs", secret: "", name: "test-config" }),
    )
  })
})

describe("StorageConfigForm write-only secret", () => {
  it("renders the secret as a password field, blank, with keep-blank copy when hasSecret", async () => {
    const user = userEvent.setup()
    render(<StorageConfigForm initial={CREATED} onSubmit={vi.fn()} isOrgScope />)
    // initial.mode=s3 → secret 字段已渲染。
    // RevealSecretInput aria-label="密钥输入" 取代原 /Secret/ label 关联（selector 调整）。
    const secret = screen.getByLabelText("密钥输入")
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

  it("github Token is a password field and submits secret:'' when untouched (keep existing)", async () => {
    const onSubmit = vi.fn().mockResolvedValue(GH_CREATED)
    const user = userEvent.setup()
    render(<StorageConfigForm initial={GH_CREATED} onSubmit={onSubmit} isOrgScope />)
    // initial.mode=github → Token 字段已渲染，password、留空、keep-blank 文案。
    // RevealSecretInput aria-label="密钥输入" 取代原 /Token/ label 关联（selector 调整）。
    const token = screen.getByLabelText("密钥输入")
    expect(token).toHaveAttribute("type", "password")
    expect(token).toHaveValue("")
    expect(screen.getByText(/留空保持不变/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        mode: "github",
        secret: "",
        accessKeyId: "acme-org",
        bucket: "my-assets",
        region: "main",
      }),
    )
  })

  it("submits the typed secret when filled", async () => {
    const onSubmit = vi.fn().mockResolvedValue(CREATED)
    const user = userEvent.setup()
    render(<StorageConfigForm initial={CREATED} onSubmit={onSubmit} isOrgScope />)
    // RevealSecretInput aria-label="密钥输入" 取代原 /Secret/ label 关联（selector 调整）。
    await user.type(screen.getByLabelText("密钥输入"), "new-secret-key")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ secret: "new-secret-key" }),
    )
  })
})

describe("StorageConfigsTable", () => {
  it("渲染每条配置一行,含名称/类型/默认徽标", () => {
    render(<StorageConfigsTable configs={[
      { id:"c1", mode:"s3", name:"主桶", bucket:"b1", enabled:true, isDefault:true, hasSecret:true, scope:"org", orgId:"o", endpoint:"", region:"", accessKeyId:"", publicPrefix:"", useSsl:true },
      { id:"c2", mode:"github", name:"仓库", bucket:"repo", enabled:false, isDefault:false, hasSecret:false, scope:"org", orgId:"o", endpoint:"", region:"", accessKeyId:"owner", publicPrefix:"", useSsl:true },
    ]} onCreate={()=>{}} onEdit={()=>{}} onDelete={()=>{}} onSetDefault={()=>{}} />)
    expect(screen.getByText("主桶")).toBeInTheDocument()
    expect(screen.getByText("仓库")).toBeInTheDocument()
    expect(document.querySelectorAll('[data-slot="sc-row"]')).toHaveLength(2)
    // c1 是默认配置，应显示"默认"徽标（<span> badge）；c2 不是默认，应有"设为默认"按钮
    // 表头 <th> 也含"默认"文本，故用 getAllByText 并断言至少 2 个（表头 + 徽标）
    expect(screen.getAllByText("默认").length).toBeGreaterThanOrEqual(2)
    expect(screen.getAllByRole("button", { name: /设为默认/ })).toHaveLength(1)
  })

  it("点非默认行的「设为默认」触发回调", () => {
    const onSetDefault = vi.fn()
    render(<StorageConfigsTable configs={[
      { id:"c2", mode:"github", name:"仓库", bucket:"repo", enabled:true, isDefault:false, hasSecret:false, scope:"org", orgId:"o", endpoint:"", region:"", accessKeyId:"owner", publicPrefix:"", useSsl:true },
    ]} onCreate={()=>{}} onEdit={()=>{}} onDelete={()=>{}} onSetDefault={onSetDefault} />)
    fireEvent.click(screen.getByRole("button", { name: /设为默认/ }))
    expect(onSetDefault).toHaveBeenCalled()
  })
})
