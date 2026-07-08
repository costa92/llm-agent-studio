import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { AuditRecord } from "@/lib/types"
import { AuditLogView } from "./AuditLogPage"

function rec(over: Partial<AuditRecord>): AuditRecord {
  return {
    id: "a1",
    orgId: "acme",
    actorUserId: "u-1",
    actorEmail: "admin@studio.com",
    action: "member.add",
    targetType: "member",
    targetId: "u-9",
    detail: {},
    createdAt: "2026-07-08T10:00:00Z",
    ...over,
  }
}

const baseProps = {
  hasNextPage: false,
  isFetchingNextPage: false,
  onLoadMore: () => {},
  isLoading: false,
  isError: false,
  onRetry: () => {},
}

describe("AuditLogView", () => {
  it("渲染审计行：操作者 email + 中文动作标签 + 目标", () => {
    render(<AuditLogView {...baseProps} rows={[rec({})]} />)
    expect(screen.getByText("admin@studio.com")).toBeInTheDocument()
    // action 码映射为中文标签。
    expect(screen.getByText("添加成员")).toBeInTheDocument()
    expect(screen.getByText("u-9")).toBeInTheDocument()
  })

  it("未收录的 action 原样透出（前向兼容）", () => {
    render(<AuditLogView {...baseProps} rows={[rec({ action: "future.new_action" })]} />)
    expect(screen.getByText("future.new_action")).toBeInTheDocument()
  })

  it("actorEmail 为空时回退显示 actorUserId", () => {
    render(<AuditLogView {...baseProps} rows={[rec({ actorEmail: "" })]} />)
    expect(screen.getByText("u-1")).toBeInTheDocument()
  })

  it("空列表显示占位文案", () => {
    render(<AuditLogView {...baseProps} rows={[]} />)
    expect(screen.getByText("暂无审计记录")).toBeInTheDocument()
  })

  it("hasNextPage 时「加载更多」可用并回调", async () => {
    const onLoadMore = vi.fn()
    render(
      <AuditLogView {...baseProps} rows={[rec({})]} hasNextPage onLoadMore={onLoadMore} />,
    )
    await userEvent.click(screen.getByRole("button", { name: "加载更多" }))
    expect(onLoadMore).toHaveBeenCalledOnce()
  })

  it("错误态显示重试", () => {
    render(<AuditLogView {...baseProps} rows={undefined} isError />)
    expect(screen.getByText("审计流水加载失败")).toBeInTheDocument()
  })
})
