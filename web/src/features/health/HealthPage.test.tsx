import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type { HealthFailure, HealthReport } from "@/lib/types"

// health/api 三个钩子 mock：每个 describe 用 setHooks 注入需要的返回值。
const repairMutateAsync = vi.fn()
const report = {
  value: {
    data: undefined as HealthReport | undefined,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  },
}
const events = {
  value: {
    data: [] as HealthFailure[],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  },
}

vi.mock("./api", () => ({
  useHealthReport: () => report.value,
  useHealthEvents: () => events.value,
  useRepairCheck: () => ({ mutateAsync: repairMutateAsync, isPending: false }),
}))

import { HealthPage } from "./HealthPage"

const REPORT_WITH_ISSUES: HealthReport = {
  system: {
    dbLatencyMs: 12,
    dbOk: true,
    stuckTodos: 3,
    lastEventAt: "2026-06-15T08:00:00Z",
    workerHealthy: true,
  },
  checks: [
    {
      id: "stuck_todos",
      title: "卡住的任务",
      severity: "warn",
      count: 3,
      samples: ["t-1", "t-2"],
      repairable: true,
    },
    {
      id: "orphan_assets",
      title: "孤儿资产",
      severity: "error",
      count: 5,
      samples: ["a-1"],
      repairable: false,
    },
  ],
}

const REPORT_ALL_CLEAR: HealthReport = {
  system: {
    dbLatencyMs: 8,
    dbOk: true,
    stuckTodos: 0,
    lastEventAt: "",
    workerHealthy: true,
  },
  checks: [
    {
      id: "stuck_todos",
      title: "卡住的任务",
      severity: "warn",
      count: 0,
      samples: [],
      repairable: true,
    },
  ],
}

function renderPage() {
  return render(
    <>
      <HealthPage />
      <Toaster />
    </>,
  )
}

beforeEach(() => {
  report.value = {
    data: REPORT_WITH_ISSUES,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }
  events.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
  repairMutateAsync.mockResolvedValue({ checkId: "stuck_todos", repaired: 3 })
})

afterEach(() => {
  vi.clearAllMocks()
  vi.restoreAllMocks()
})

describe("HealthPage system cards", () => {
  it("renders system health cards from useHealthReport", () => {
    renderPage()
    expect(screen.getByText("DB 状态")).toBeInTheDocument()
    expect(screen.getByText("DB 延迟")).toBeInTheDocument()
    expect(screen.getByText("12")).toBeInTheDocument()
    expect(screen.getByText("Worker 活性")).toBeInTheDocument()
    expect(screen.getByText("最近活动")).toBeInTheDocument()
  })
})

describe("HealthPage consistency checks", () => {
  it("shows 一键修复 for a repairable check with count>0 and calls repair on confirm", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true)
    const user = userEvent.setup()
    renderPage()

    const button = screen.getByRole("button", { name: "一键修复 卡住的任务" })
    await user.click(button)

    expect(window.confirm).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(repairMutateAsync).toHaveBeenCalledTimes(1))
    expect(repairMutateAsync.mock.calls[0][0]).toBe("stuck_todos")
    await waitFor(() =>
      expect(screen.getByText("已修复 3 条")).toBeInTheDocument(),
    )
  })

  it("does not call repair when confirm is cancelled", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(false)
    const user = userEvent.setup()
    renderPage()

    await user.click(screen.getByRole("button", { name: "一键修复 卡住的任务" }))
    expect(repairMutateAsync).not.toHaveBeenCalled()
  })

  it("shows 需人工处理 for a non-repairable check", () => {
    renderPage()
    expect(screen.getByText("需人工处理")).toBeInTheDocument()
    // 不可修复项不渲染修复按钮。
    expect(
      screen.queryByRole("button", { name: "一键修复 孤儿资产" }),
    ).not.toBeInTheDocument()
  })

  it("shows 数据正常 empty state when all checks count==0", () => {
    report.value = {
      data: REPORT_ALL_CLEAR,
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    renderPage()
    expect(screen.getByText("数据正常")).toBeInTheDocument()
  })
})

describe("HealthPage failures feed", () => {
  it("renders failure rows from useHealthEvents", () => {
    events.value = {
      data: [
        {
          todoId: "t-1",
          projectId: "p-1",
          projectName: "项目甲",
          orgId: "acme",
          type: "generate",
          agent: "writer",
          error: "boom: something failed",
          at: "2026-06-15T08:00:00Z",
        },
      ],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    }
    renderPage()
    expect(screen.getByText("项目甲")).toBeInTheDocument()
    expect(screen.getByText("boom: something failed")).toBeInTheDocument()
  })

  it("shows empty state when there are no failures", () => {
    renderPage()
    expect(screen.getByText("暂无失败记录")).toBeInTheDocument()
  })
})
