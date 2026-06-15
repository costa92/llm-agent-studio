import { describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { EditProjectForm } from "./EditProjectDialog"
import type { Project, Style } from "@/lib/types"

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
    expect(arg).toHaveProperty("storageMode", "")
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
})
