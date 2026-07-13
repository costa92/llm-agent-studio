import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { TemplatePicker } from "./TemplatePicker"
import {
  useWorkflowTemplates,
  useInstantiateTemplate,
  useDeleteTemplate,
} from "@/features/projects/workflowTemplateApi"
import type { WorkflowTemplateMeta } from "@/lib/types"

// hooks 全 mock：本测试只验组件行为（分组渲染 / 点卡片 mutate / 成功回调），不打网络。
vi.mock("@/features/projects/workflowTemplateApi", () => ({
  useWorkflowTemplates: vi.fn(),
  useInstantiateTemplate: vi.fn(),
  useDeleteTemplate: vi.fn(),
}))
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }))

const mockTemplates = vi.mocked(useWorkflowTemplates)
const mockInstantiate = vi.mocked(useInstantiateTemplate)
const mockDelete = vi.mocked(useDeleteTemplate)

const TEMPLATES: WorkflowTemplateMeta[] = [
  { id: "standard", name: "通用管线", description: "脚本→分镜", group: "通用", source: "builtin" },
  { id: "music", name: "音乐创作", description: "歌词→编曲", group: "创作", source: "builtin" },
  { id: "poem", name: "写诗", description: "意象→成诗", group: "创作", source: "builtin" },
  { id: "mine-1", name: "我的科普", description: "自定义", group: "我的模板", source: "org", deletable: true },
]

function withTemplates(
  data: WorkflowTemplateMeta[],
  extra: Partial<ReturnType<typeof useWorkflowTemplates>> = {},
) {
  return {
    data,
    isLoading: false,
    isError: false,
    ...extra,
  } as unknown as ReturnType<typeof useWorkflowTemplates>
}

// mutate(vars, opts) 直接调 opts.onSuccess 模拟成功；返回一个新工作流。
function withMutation(
  mutate: ReturnType<typeof vi.fn>,
  isPending = false,
) {
  return {
    mutate,
    isPending,
  } as unknown as ReturnType<typeof useInstantiateTemplate>
}

beforeEach(() => {
  mockTemplates.mockReturnValue(withTemplates(TEMPLATES))
  mockInstantiate.mockReturnValue(withMutation(vi.fn()))
  mockDelete.mockReturnValue(
    withMutation(vi.fn()) as unknown as ReturnType<typeof useDeleteTemplate>,
  )
  vi.spyOn(window, "confirm").mockReturnValue(true)
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("TemplatePicker", () => {
  it("按 group 分组渲染模板卡片（名称 + 描述）", () => {
    render(
      <TemplatePicker
        org="org1"
        projectId="p1"
        onCreated={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    // 分组标题
    expect(screen.getByText("通用")).toBeInTheDocument()
    expect(screen.getByText("创作")).toBeInTheDocument()
    // 卡片名称 + 描述
    expect(screen.getByText("通用管线")).toBeInTheDocument()
    expect(screen.getByText("脚本→分镜")).toBeInTheDocument()
    expect(screen.getByText("音乐创作")).toBeInTheDocument()
    expect(screen.getByText("写诗")).toBeInTheDocument()
  })

  it("点卡片 → instantiate.mutate({templateId})，成功回调 onCreated(wf.id)", async () => {
    const onCreated = vi.fn()
    const mutate = vi.fn(
      (_vars: { templateId: string }, opts?: { onSuccess?: (wf: { id: string }) => void }) => {
        opts?.onSuccess?.({ id: "wf-new" })
      },
    )
    mockInstantiate.mockReturnValue(withMutation(mutate))

    const user = userEvent.setup()
    render(
      <TemplatePicker
        org="org1"
        projectId="p1"
        onCreated={onCreated}
        onCancel={vi.fn()}
      />,
    )
    await user.click(screen.getByText("音乐创作"))

    expect(mutate).toHaveBeenCalledTimes(1)
    expect(mutate.mock.calls[0][0]).toEqual({ templateId: "music" })
    expect(onCreated).toHaveBeenCalledWith("wf-new")
  })

  it("pending 期间卡片禁用（不重复 mutate）", async () => {
    const mutate = vi.fn()
    mockInstantiate.mockReturnValue(withMutation(mutate, true))

    const user = userEvent.setup()
    render(
      <TemplatePicker
        org="org1"
        projectId="p1"
        onCreated={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    await user.click(screen.getByText("通用管线"))
    expect(mutate).not.toHaveBeenCalled()
  })

  it("org 模板（我的模板）渲染删除按钮，内置模板没有", () => {
    render(
      <TemplatePicker
        org="org1"
        projectId="p1"
        onCreated={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    // org 项分组标题 + 删除按钮
    expect(screen.getByText("我的模板")).toBeInTheDocument()
    expect(screen.getByLabelText("删除模板 我的科普")).toBeInTheDocument()
    // 内置项无删除按钮
    expect(screen.queryByLabelText("删除模板 通用管线")).not.toBeInTheDocument()
  })

  it("点删除 → useDeleteTemplate.mutate(id)，且不触发 pick", async () => {
    const delMutate = vi.fn()
    mockDelete.mockReturnValue(
      withMutation(delMutate) as unknown as ReturnType<typeof useDeleteTemplate>,
    )
    const pickMutate = vi.fn()
    mockInstantiate.mockReturnValue(withMutation(pickMutate))

    const user = userEvent.setup()
    render(
      <TemplatePicker
        org="org1"
        projectId="p1"
        onCreated={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    await user.click(screen.getByLabelText("删除模板 我的科普"))

    expect(delMutate).toHaveBeenCalledTimes(1)
    expect(delMutate.mock.calls[0][0]).toBe("mine-1")
    // 删除不应触发从模板创建（pick）
    expect(pickMutate).not.toHaveBeenCalled()
  })
})
