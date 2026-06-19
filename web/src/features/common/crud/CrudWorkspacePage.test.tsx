import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { CrudWorkspacePage } from "./CrudWorkspacePage"

// 公共 props 工厂：默认正常态，单测按需覆盖。
function baseProps(over: Partial<React.ComponentProps<typeof CrudWorkspacePage>> = {}) {
  return {
    title: "资产库",
    sidebar: <div data-testid="rail">rail</div>,
    isLoading: false,
    isError: false,
    isEmpty: false,
    children: <div data-testid="body">grid</div>,
    ...over,
  }
}

describe("CrudWorkspacePage", () => {
  it("正常态：渲染 title + headerActions + children", () => {
    render(
      <CrudWorkspacePage {...baseProps({ headerActions: <span data-testid="actions">N</span> })} />,
    )
    expect(screen.getByText("资产库")).toBeInTheDocument()
    expect(screen.getByTestId("actions")).toBeInTheDocument()
    expect(screen.getByTestId("body")).toBeInTheDocument()
  })

  it("sidebar 跨 loading/error/empty 三态恒在（只换右列）", () => {
    const loading = render(<CrudWorkspacePage {...baseProps({ isLoading: true })} />)
    expect(screen.getByTestId("rail")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
    loading.unmount()

    const errored = render(<CrudWorkspacePage {...baseProps({ isError: true })} />)
    expect(screen.getByTestId("rail")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
    errored.unmount()

    render(<CrudWorkspacePage {...baseProps({ isEmpty: true })} />)
    expect(screen.getByTestId("rail")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("isLoading：渲染 loadingSkeleton 槽，不渲染 children", () => {
    render(
      <CrudWorkspacePage
        {...baseProps({ isLoading: true, loadingSkeleton: <div data-testid="skel" /> })}
      />,
    )
    expect(screen.getByTestId("skel")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("isError：显默认 errorHint + 重试，点击调 onRetry", async () => {
    const onRetry = vi.fn()
    render(<CrudWorkspacePage {...baseProps({ isError: true, onRetry })} />)
    expect(screen.getByText("加载失败")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalled()
  })

  it("isError + errorHint：显自定义错误文案（Library 传「资产加载失败」）", () => {
    render(<CrudWorkspacePage {...baseProps({ isError: true, errorHint: "资产加载失败" })} />)
    expect(screen.getByText("资产加载失败")).toBeInTheDocument()
  })

  it("isEmpty：渲染 emptyState 槽，不渲染 children", () => {
    render(
      <CrudWorkspacePage
        {...baseProps({ isEmpty: true, emptyState: <div data-testid="empty" /> })}
      />,
    )
    expect(screen.getByTestId("empty")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("isLoading 无 loadingSkeleton：渲染默认骨架，不渲染 children", () => {
    render(<CrudWorkspacePage {...baseProps({ isLoading: true })} />)
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("isEmpty 无 emptyState：渲染默认「暂无数据。」文案", () => {
    render(<CrudWorkspacePage {...baseProps({ isEmpty: true })} />)
    expect(screen.getByText("暂无数据。")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("isEmpty + emptyHint：渲染自定义空态文案（与 CrudResourcePage 同款 emptyHint）", () => {
    render(<CrudWorkspacePage {...baseProps({ isEmpty: true, emptyHint: "暂无资产" })} />)
    expect(screen.getByText("暂无资产")).toBeInTheDocument()
  })
})
