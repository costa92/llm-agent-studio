import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { Project, Style } from "@/lib/types"
import { ProjectListView } from "./ProjectListPage"
import { CreateProjectForm } from "./CreateProjectDialog"

afterEach(() => {
  vi.restoreAllMocks()
})

const STYLES: Style[] = [
  { name: "日漫", suffix: "anime" },
  { name: "写实", suffix: "realistic" },
]

function makeProject(over: Partial<Project> = {}): Project {
  return {
    id: "p1",
    orgId: "acme",
    name: "夏日广告片",
    description: "一支清爽的夏季饮品广告",
    contentType: "广告片",
    targetPlatform: "抖音",
    style: "写实",
    status: "running",
    createdBy: "u1",
    ...over,
  }
}

function baseViewProps() {
  return {
    isLoading: false,
    isError: false,
    onRetry: vi.fn(),
    canCreate: true,
    styles: STYLES,
    onCreate: vi.fn(),
    onOpenProject: vi.fn(),
  }
}

describe("ProjectListView", () => {
  it("renders project cards with status badge and opens on click", async () => {
    const onOpenProject = vi.fn()
    const user = userEvent.setup()
    render(
      <ProjectListView
        {...baseViewProps()}
        projects={[makeProject({ status: "running" })]}
        onOpenProject={onOpenProject}
      />,
    )

    expect(screen.getByText("夏日广告片")).toBeInTheDocument()
    expect(screen.getByText("生产中")).toBeInTheDocument()

    await user.click(screen.getByText("夏日广告片"))
    expect(onOpenProject).toHaveBeenCalledTimes(1)
  })

  it("renders empty state with CTA when there are no projects", () => {
    render(<ProjectListView {...baseViewProps()} projects={[]} />)
    expect(screen.getByText("还没有项目")).toBeInTheDocument()
    expect(
      screen.getByText("用一句创意需求开始你的第一支作品"),
    ).toBeInTheDocument()
    expect(
      screen.getAllByRole("button", { name: "新建项目" }).length,
    ).toBeGreaterThan(0)
  })

  it("nudges to configure a model in the empty state when no enabled config exists", async () => {
    const onConfigureModel = vi.fn()
    const user = userEvent.setup()
    render(
      <ProjectListView
        {...baseViewProps()}
        projects={[]}
        needsModelConfig
        onConfigureModel={onConfigureModel}
      />,
    )
    expect(
      screen.getByText("先配置一个生成模型再开始制作"),
    ).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "去配置模型" }))
    expect(onConfigureModel).toHaveBeenCalledTimes(1)
  })

  it("keeps the normal empty state when a model is configured", () => {
    render(
      <ProjectListView
        {...baseViewProps()}
        projects={[]}
        needsModelConfig={false}
      />,
    )
    expect(screen.getByText("还没有项目")).toBeInTheDocument()
    expect(
      screen.queryByText("先配置一个生成模型再开始制作"),
    ).not.toBeInTheDocument()
  })

  it("hides the create CTA for non-editors (canCreate=false)", () => {
    render(
      <ProjectListView {...baseViewProps()} projects={[]} canCreate={false} />,
    )
    expect(screen.getByText("还没有项目")).toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "新建项目" }),
    ).not.toBeInTheDocument()
  })

  it("renders error state with a retry button", async () => {
    const onRetry = vi.fn()
    const user = userEvent.setup()
    render(
      <ProjectListView
        {...baseViewProps()}
        projects={undefined}
        isError
        onRetry={onRetry}
      />,
    )

    expect(screen.getByText("项目加载失败")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it("renders loading skeletons", () => {
    const { container } = render(
      <ProjectListView {...baseViewProps()} projects={undefined} isLoading />,
    )
    expect(container.querySelectorAll('[data-slot="skeleton"]').length).toBe(6)
  })
})

describe("CreateProjectForm", () => {
  it("submits the form and calls onSuccess with the created project", async () => {
    const created = makeProject({ id: "new1", name: "新项目" })
    const onSubmit = vi.fn().mockResolvedValue(created)
    const onSuccess = vi.fn()
    const user = userEvent.setup()

    render(
      <CreateProjectForm
        styles={STYLES}
        onSubmit={onSubmit}
        onSuccess={onSuccess}
      />,
    )

    await user.type(screen.getByLabelText("项目名称"), "新项目")
    await user.type(screen.getByLabelText("创意需求"), "一句创意")

    // 风格默认选中首个（日漫）——无需与 radix Select 交互（jsdom 不支持指针捕获）。
    await user.click(screen.getByRole("button", { name: "创建" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "新项目",
        brief: "一句创意",
        style: "日漫",
        contentType: "短视频",
        targetPlatform: "抖音",
      }),
    )
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(created))
  })

  it("blocks submit and shows validation errors when required fields are empty", async () => {
    const onSubmit = vi.fn()
    const user = userEvent.setup()

    render(<CreateProjectForm styles={STYLES} onSubmit={onSubmit} />)

    await user.click(screen.getByRole("button", { name: "创建" }))

    expect(await screen.findByText("请输入项目名称")).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  // M5.1: 规划模型下拉只在 org 有 text 模型时显示；空 = 不带 override 字段。
  it("hides the planner-model selector when no text models are configured", () => {
    render(<CreateProjectForm styles={STYLES} onSubmit={vi.fn()} textModels={[]} />)
    expect(screen.queryByLabelText("规划用模型（可选）")).toBeNull()
  })

  it("hides the planner-model selector when textModels is undefined", () => {
    render(<CreateProjectForm styles={STYLES} onSubmit={vi.fn()} />)
    expect(screen.queryByLabelText("规划用模型（可选）")).toBeNull()
  })

  it("shows the planner-model selector and submits without override by default", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject({ id: "x" }))
    const user = userEvent.setup()
    render(
      <CreateProjectForm
        styles={STYLES}
        onSubmit={onSubmit}
        textModels={[
          {
            id: "m1",
            orgId: "o",
            kind: "text",
            provider: "minimax",
            model: "minimax-text-01",
            enabled: true,
            isDefault: true,
            baseUrl: "",
            useSsl: true,
            hasApiKey: true,
          },
        ]}
      />,
    )
    // 下拉 trigger 必须渲染（默认选项："使用组织默认"）。
    expect(screen.getByLabelText("规划用模型（可选）")).toBeInTheDocument()
    await user.type(screen.getByLabelText("项目名称"), "X")
    await user.type(screen.getByLabelText("创意需求"), "一句")
    await user.click(screen.getByRole("button", { name: "创建" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    const got = onSubmit.mock.calls[0][0]
    // 默认没改 → 不带 plannerProvider/plannerModel（让后端 = 无 override）。
    expect(got.plannerProvider).toBeUndefined()
    expect(got.plannerModel).toBeUndefined()
  })

  it("submits without override when the planner-model dropdown is left at default", async () => {
    // jsdom + Radix Select 的 pointer-capture 模拟不可靠——真实"在 Radix 下拉里
    // 选一个选项"走的是浮层+键盘路径，单测里很容易 flaky。这里只验证
    // 用户没动下拉时（默认 = 用组织默认）提交不带 override 字段。
    // 真正"选了 ollama → 提交时带 override"靠 Playwright/E2E 覆盖。
    const onSubmit = vi.fn().mockResolvedValue(makeProject({ id: "x" }))
    const user = userEvent.setup()
    render(
      <CreateProjectForm
        styles={STYLES}
        onSubmit={onSubmit}
        textModels={[
          {
            id: "m2",
            orgId: "o",
            kind: "text",
            provider: "ollama",
            model: "gemma4:26b",
            enabled: true,
            isDefault: false,
            baseUrl: "",
            useSsl: true,
            hasApiKey: false,
          },
        ]}
      />,
    )
    await user.type(screen.getByLabelText("项目名称"), "X")
    await user.type(screen.getByLabelText("创意需求"), "一句")
    await user.click(screen.getByRole("button", { name: "创建" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    const got = onSubmit.mock.calls[0][0]
    expect(got.plannerProvider).toBeUndefined()
    expect(got.plannerModel).toBeUndefined()
  })
})
