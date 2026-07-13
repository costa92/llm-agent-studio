import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { SaveTemplateDialog } from "./SaveTemplateDialog"
import { useSaveTemplate } from "@/features/projects/workflowTemplateApi"

// hook 全 mock：只验组件行为（填字段 → 提交带正确 payload → 成功 toast + 关闭）。
vi.mock("@/features/projects/workflowTemplateApi", () => ({
  useSaveTemplate: vi.fn(),
}))
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }))

import { toast } from "sonner"

const mockSave = vi.mocked(useSaveTemplate)

function withMutation(mutate: ReturnType<typeof vi.fn>, isPending = false) {
  return { mutate, isPending } as unknown as ReturnType<typeof useSaveTemplate>
}

beforeEach(() => {
  mockSave.mockReturnValue(withMutation(vi.fn()))
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("SaveTemplateDialog", () => {
  it("提交带正确 payload，成功后 toast + 关闭", async () => {
    const mutate = vi.fn(
      (_vars: unknown, opts?: { onSuccess?: () => void }) => {
        opts?.onSuccess?.()
      },
    )
    mockSave.mockReturnValue(withMutation(mutate))
    const onOpenChange = vi.fn()

    const user = userEvent.setup()
    render(
      <SaveTemplateDialog
        org="org1"
        projectId="p1"
        workflowId="wf-1"
        defaultName="我的工作流"
        open
        onOpenChange={onOpenChange}
      />,
    )
    // 默认名预填
    expect((screen.getByLabelText("模板名") as HTMLInputElement).value).toBe(
      "我的工作流",
    )
    await user.clear(screen.getByLabelText("模板名"))
    await user.type(screen.getByLabelText("模板名"), "科普管线")
    await user.type(screen.getByLabelText("描述"), "脚本→分镜→配图")
    await user.click(screen.getByRole("button", { name: "确认" }))

    expect(mutate).toHaveBeenCalledTimes(1)
    expect(mutate.mock.calls[0][0]).toEqual({
      name: "科普管线",
      description: "脚本→分镜→配图",
      projectId: "p1",
      workflowId: "wf-1",
    })
    expect(toast.success).toHaveBeenCalledWith("已存为模板")
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("名称为空时禁用确认", () => {
    render(
      <SaveTemplateDialog
        org="org1"
        projectId="p1"
        workflowId="wf-1"
        open
        onOpenChange={vi.fn()}
      />,
    )
    expect(screen.getByRole("button", { name: "确认" })).toBeDisabled()
  })
})
