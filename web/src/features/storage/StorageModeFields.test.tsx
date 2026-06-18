import { describe, it, expect, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { FormProvider, useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { StorageModeFields, formSchema, defaultsFor } from "./StorageModeFields"
import type { FormValues } from "./StorageModeFields"
import type { StorageConfig } from "@/lib/types"

// 最简 FormProvider 包装器：注入 rhf 上下文供 StorageModeFields 的 useFormContext 使用。
function Wrapper({
  mode = "localfs",
  initial,
}: {
  mode?: FormValues["mode"]
  initial?: StorageConfig | null
}) {
  const methods = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: defaultsFor(initial ?? { mode } as StorageConfig),
  })
  return (
    <FormProvider {...methods}>
      <form>
        <StorageModeFields initial={initial} />
      </form>
    </FormProvider>
  )
}

describe("StorageModeFields", () => {
  it("localfs：只显示 publicPrefix，不显示 Endpoint/Bucket/密钥输入", () => {
    render(<Wrapper mode="localfs" />)
    expect(screen.getByLabelText(/publicPrefix/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/Endpoint/)).toBeNull()
    expect(screen.queryByLabelText(/Bucket/)).toBeNull()
    expect(screen.queryByLabelText("密钥输入")).toBeNull()
  })

  it("s3：从 localfs 切换后显示 Endpoint/Bucket/AccessKeyId/密钥输入", async () => {
    const user = userEvent.setup()
    render(<Wrapper mode="localfs" />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "s3")
    expect(screen.getByLabelText(/Endpoint（必填）/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Bucket（必填）/)).toBeInTheDocument()
    expect(screen.getByLabelText(/AccessKeyId/)).toBeInTheDocument()
    expect(screen.getByLabelText("密钥输入")).toBeInTheDocument()
  })

  it("cos：从 localfs 切换后显示 Region（必填）和 Endpoint（可空）", async () => {
    const user = userEvent.setup()
    render(<Wrapper mode="localfs" />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "cos")
    expect(screen.getByLabelText(/Region（必填）/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Endpoint（可空）/)).toBeInTheDocument()
  })

  it("github：从 localfs 切换後显示 Owner/Repo/Branch，隐藏 s3-only Endpoint（必填/可空）", async () => {
    const user = userEvent.setup()
    render(<Wrapper mode="localfs" />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "github")
    expect(screen.getByLabelText(/Owner/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Repo/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Branch/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/Endpoint（必填）/)).toBeNull()
    expect(screen.queryByLabelText(/Endpoint（可空）/)).toBeNull()
  })

  it("oss：从 localfs 切换後显示 Endpoint（必填）/Bucket（必填）/AccessKeyId/密钥输入，不显示 Region", async () => {
    const user = userEvent.setup()
    render(<Wrapper mode="localfs" />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "oss")
    expect(screen.getByLabelText(/Endpoint（必填）/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Bucket（必填）/)).toBeInTheDocument()
    expect(screen.getByLabelText(/AccessKeyId/)).toBeInTheDocument()
    expect(screen.getByLabelText("密钥输入")).toBeInTheDocument()
    // OSS 不显示 Region
    expect(screen.queryByLabelText(/Region/)).toBeNull()
  })

  it("hasSecret=true 时：密钥输入为 password 类型，显示「已配置密钥」徽标和留空保持不变提示", () => {
    const initial: StorageConfig = {
      id: "sc-1", name: "test", scope: "org", orgId: "o",
      mode: "s3", endpoint: "https://s3.amazonaws.com", region: "us-east-1",
      bucket: "b", accessKeyId: "AKIA", publicPrefix: "", useSsl: true,
      enabled: true, isDefault: false, hasSecret: true,
    }
    render(<Wrapper initial={initial} />)
    const secretInput = screen.getByLabelText("密钥输入")
    expect(secretInput).toHaveAttribute("type", "password")
    expect(secretInput).toHaveValue("")
    expect(screen.getByText(/留空保持不变/)).toBeInTheDocument()
    expect(screen.getByText("已配置密钥")).toBeInTheDocument()
  })

  it("s3：bucket/endpoint 缺失时展示 zod superRefine 错误", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()
    const methods = (() => {
      let capturedMethods: ReturnType<typeof useForm<FormValues>> | undefined
      function Inner() {
        const m = useForm<FormValues>({
          resolver: zodResolver(formSchema),
          defaultValues: defaultsFor(null),
        })
        capturedMethods = m
        return (
          <FormProvider {...m}>
            <form onSubmit={m.handleSubmit(onSubmit)}>
              <StorageModeFields />
              <button type="submit">保存</button>
            </form>
          </FormProvider>
        )
      }
      return { Inner, get: () => capturedMethods }
    })()
    render(<methods.Inner />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "s3")
    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(screen.getByText(/请填写 Bucket/)).toBeInTheDocument())
    expect(screen.getByText(/请填写 Endpoint/)).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("oss：bucket/endpoint 缺失时展示 zod superRefine 错误", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()
    function Inner() {
      const m = useForm<FormValues>({
        resolver: zodResolver(formSchema),
        defaultValues: defaultsFor(null),
      })
      return (
        <FormProvider {...m}>
          <form onSubmit={m.handleSubmit(onSubmit)}>
            <StorageModeFields />
            <button type="submit">保存</button>
          </form>
        </FormProvider>
      )
    }
    render(<Inner />)
    await user.selectOptions(screen.getByLabelText("存储类型 (mode)"), "oss")
    await user.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(screen.getByText(/请填写 Bucket/)).toBeInTheDocument())
    expect(screen.getByText(/请填写 Endpoint/)).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })
})
