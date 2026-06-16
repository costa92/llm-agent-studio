import { describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { EditProjectForm } from "./EditProjectDialog"
import type { Project, StorageConfig, Style } from "@/lib/types"

function makeProject(over: Partial<Project> = {}): Project {
  return {
    id: "p1",
    orgId: "o1",
    name: "旧名",
    description: "旧需求",
    contentType: "短视频",
    targetPlatform: "抖音",
    style: "写实",
    status: "draft",
    createdBy: "u1",
    plannerProvider: "",
    plannerModel: "",
    imageProvider: "",
    imageModel: "",
    storageMode: "",
    ...over,
  }
}

const styles: Style[] = [
  { name: "写实", suffix: "" },
  { name: "动画", suffix: "" },
]

const storageConfigs: StorageConfig[] = [
  {
    id: "sc1",
    orgId: "o1",
    scope: "org",
    name: "主存储桶",
    mode: "s3",
    enabled: true,
    isDefault: true,
    endpoint: "https://s3.example.com",
    region: "us-east-1",
    bucket: "my-bucket",
    accessKeyId: "AKID",
    hasSecret: true,
    publicPrefix: "",
    useSsl: true,
  },
  {
    id: "sc2",
    orgId: "o1",
    scope: "org",
    name: "备用仓库",
    mode: "github",
    enabled: true,
    isDefault: false,
    endpoint: "",
    region: "",
    bucket: "my-repo",
    accessKeyId: "my-owner",
    hasSecret: false,
    publicPrefix: "",
    useSsl: false,
  },
]

describe("EditProjectForm", () => {
  it("submits edited basic info (name/description) alongside model fields", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject())
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    render(
      <EditProjectForm
        project={makeProject()}
        styles={styles}
        onSubmit={onSubmit}
        onSuccess={onSuccess}
      />,
    )

    const name = screen.getByLabelText("项目名称")
    await user.clear(name)
    await user.type(name, "新名字")

    const desc = screen.getByLabelText("创意需求")
    await user.clear(desc)
    await user.type(desc, "新的创意需求")

    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    const arg = onSubmit.mock.calls[0][0]
    expect(arg.name).toBe("新名字")
    expect(arg.description).toBe("新的创意需求")
    expect(arg.contentType).toBe("短视频")
    expect(arg.style).toBe("写实")
    // 模型字段仍随表单一起回传（保持原值）。
    expect(arg).toHaveProperty("plannerProvider", "")
    expect(arg).toHaveProperty("storageConfigId", "")
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
  })

  it("blocks submit and shows an error when the name is cleared", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()

    render(<EditProjectForm project={makeProject()} styles={styles} onSubmit={onSubmit} />)

    await user.clear(screen.getByLabelText("项目名称"))
    await user.click(screen.getByRole("button", { name: "保存" }))

    expect(await screen.findByText("请输入项目名称")).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("renders 继承组织默认 + each enabled config in the storage dropdown", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject())
    const user = userEvent.setup()

    render(
      <EditProjectForm
        project={makeProject()}
        styles={styles}
        storageConfigs={storageConfigs}
        onSubmit={onSubmit}
      />,
    )

    // 下拉触发按钮应显示"继承组织默认"（当前值为空 → 默认项）。
    const trigger = screen.getByRole("combobox", { name: /存储配置/ })
    expect(trigger).toBeInTheDocument()

    // 点开下拉，验证所有选项。
    await user.click(trigger)

    expect(await screen.findByRole("option", { name: "继承组织默认" })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: /主存储桶/ })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: /备用仓库/ })).toBeInTheDocument()
  })

  it("submits storageConfigId when a specific config is selected", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject())
    const user = userEvent.setup()

    render(
      <EditProjectForm
        project={makeProject()}
        styles={styles}
        storageConfigs={storageConfigs}
        onSubmit={onSubmit}
      />,
    )

    // 打开存储配置下拉，选择「主存储桶」。
    const trigger = screen.getByRole("combobox", { name: /存储配置/ })
    await user.click(trigger)
    await user.click(await screen.findByRole("option", { name: /主存储桶/ }))

    // 提交表单。
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit.mock.calls[0][0]).toMatchObject({ storageConfigId: "sc1" })
  })
})
