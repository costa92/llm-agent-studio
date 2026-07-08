import { afterEach, describe, expect, it, vi } from "vitest"
import {
  render,
  screen,
  waitFor,
  type RenderOptions,
} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactElement, type ReactNode } from "react"
import type { Project, StorageConfig, Style } from "@/lib/types"
import { ProjectListView } from "./ProjectListPage"
import { CreateProjectForm } from "./CreateProjectDialog"

// AssetThumb 走 authed fetch → blob object URL；jsdom 无网络。stub useResolvedAssetUrl
// 为 null（封面渲染 AssetThumb 的占位，不触网）——与 review.test.tsx 同款 mock。
vi.mock("@/features/workflow/assetThumb", () => ({
  resolveAssetUrl: vi.fn().mockResolvedValue(null),
  useResolvedAssetUrl: () => ({ url: null as string | null, loading: false }),
}))

afterEach(() => {
  vi.restoreAllMocks()
})

// ProjectListView 现在内嵌 CoverDialog（用 useQueryClient）→ 需 QueryClientProvider 包裹。
function renderWithClient(ui: ReactElement, options?: RenderOptions) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
  return render(ui, { wrapper, ...options })
}

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
    org: "o1",
    canCreate: true,
    styles: STYLES,
    onCreate: vi.fn(),
    onUpdate: vi.fn(),
    onOpenProject: vi.fn(),
  }
}

describe("ProjectListView", () => {
  it("renders project cards with status badge and opens on click", async () => {
    const onOpenProject = vi.fn()
    const user = userEvent.setup()
    renderWithClient(
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

  it("opens the edit dialog from the card 编辑 button and submits via onUpdate", async () => {
    const onUpdate = vi.fn().mockResolvedValue(makeProject())
    const user = userEvent.setup()
    renderWithClient(
      <ProjectListView
        {...baseViewProps()}
        projects={[makeProject()]}
        onUpdate={onUpdate}
      />,
    )

    await user.click(screen.getByRole("button", { name: "编辑" }))
    // 弹窗打开 → 名称回显当前值。
    expect(await screen.findByLabelText("项目名称")).toHaveValue("夏日广告片")

    const name = screen.getByLabelText("项目名称")
    await user.clear(name)
    await user.type(name, "改个名字")
    await user.click(screen.getByRole("button", { name: "保存" }))

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1))
    const arg = onUpdate.mock.calls[0][0]
    expect(arg.id).toBe("p1")
    expect(arg.name).toBe("改个名字")
  })

  it("renders the 无封面 placeholder for a project without a cover", () => {
    renderWithClient(
      <ProjectListView {...baseViewProps()} projects={[makeProject()]} />,
    )
    expect(screen.getByText("无封面")).toBeInTheDocument()
  })

  it("renders a cover (AssetThumb) for a project with coverAssetId", () => {
    renderWithClient(
      <ProjectListView
        {...baseViewProps()}
        projects={[makeProject({ coverAssetId: "asset-9" })]}
      />,
    )
    // useResolvedAssetUrl 被 stub 为 url=null → AssetThumb 落到占位（"图片不可用"），
    // 而非"无封面"（即说明走的是封面分支而非占位分支）。
    expect(screen.queryByText("无封面")).not.toBeInTheDocument()
    expect(screen.getByText("图片不可用")).toBeInTheDocument()
  })

  it("renders empty state with CTA when there are no projects", () => {
    renderWithClient(<ProjectListView {...baseViewProps()} projects={[]} />)
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
    renderWithClient(
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
    renderWithClient(
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
    renderWithClient(
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
    renderWithClient(
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

  it("renders an access-denied state and hides create CTAs when forbidden (403)", () => {
    renderWithClient(
      <ProjectListView
        {...baseViewProps()}
        projects={undefined}
        isForbidden
      />,
    )

    expect(screen.getByText("无权访问")).toBeInTheDocument()
    // 无权访问不给「重试」，也不露任何「新建项目」入口（header + 空态）。
    expect(screen.queryByRole("button", { name: "重试" })).not.toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "新建项目" }),
    ).not.toBeInTheDocument()
  })

  it("renders loading skeletons", () => {
    const { container } = renderWithClient(
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

    // 内容类型/风格解耦：不再预填任何生成默认，风格/内容类型/平台皆空（由工作流决定）。
    await user.click(screen.getByRole("button", { name: "创建" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "新项目",
        brief: "一句创意",
        style: "",
        contentType: "",
        targetPlatform: "",
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

  const STORAGE_CONFIGS: StorageConfig[] = [
    {
      id: "sc1", orgId: "o1", scope: "org", name: "主存储桶", mode: "s3",
      enabled: true, isDefault: true, endpoint: "https://s3.example.com",
      region: "us-east-1", bucket: "my-bucket", accessKeyId: "AKID",
      hasSecret: true, publicPrefix: "", useSsl: true,
    },
    {
      id: "sc2", orgId: "o1", scope: "org", name: "备用仓库", mode: "github",
      enabled: true, isDefault: false, endpoint: "", region: "", bucket: "my-repo",
      accessKeyId: "my-owner", hasSecret: false, publicPrefix: "", useSsl: false,
    },
  ]

  it("renders the storage dropdown and submits storageConfigId when a config is selected", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject({ id: "x" }))
    const user = userEvent.setup()
    render(<CreateProjectForm styles={STYLES} storageConfigs={STORAGE_CONFIGS} onSubmit={onSubmit} />)

    await user.type(screen.getByLabelText("项目名称"), "X")
    await user.type(screen.getByLabelText("创意需求"), "一句")

    const trigger = screen.getByRole("combobox", { name: /存储配置/ })
    await user.click(trigger)
    await user.click(await screen.findByRole("option", { name: /主存储桶/ }))

    await user.click(screen.getByRole("button", { name: "创建" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit.mock.calls[0][0]).toMatchObject({ storageConfigId: "sc1" })
  })

  it("omits storageConfigId when storage is left at inherit-default", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject({ id: "x" }))
    const user = userEvent.setup()
    render(<CreateProjectForm styles={STYLES} storageConfigs={STORAGE_CONFIGS} onSubmit={onSubmit} />)

    await user.type(screen.getByLabelText("项目名称"), "X")
    await user.type(screen.getByLabelText("创意需求"), "一句")
    await user.click(screen.getByRole("button", { name: "创建" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit.mock.calls[0][0].storageConfigId).toBeUndefined()
  })
})
