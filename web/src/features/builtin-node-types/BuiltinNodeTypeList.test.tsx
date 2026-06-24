import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import type { BuiltinNodeType } from "@/lib/types"

// --- mock api hook --------------------------------------------------------
// 全局只读目录：useBuiltinNodeTypes 无 org 入参（query key 不含 org）。
const queryState = {
  value: {
    data: [] as BuiltinNodeType[],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  },
}

vi.mock("./api", () => ({
  useBuiltinNodeTypes: () => queryState.value,
}))

import { BuiltinNodeTypeList } from "./BuiltinNodeTypeList"

const ITEMS: BuiltinNodeType[] = [
  { type: "script", label: "剧本", description: "生成剧本文本" },
  { type: "storyboard", label: "分镜", description: "生成分镜脚本" },
  { type: "asset", label: "资产", description: "生成图片等资产" },
]

beforeEach(() => {
  queryState.value = { data: ITEMS, isLoading: false, isError: false, refetch: vi.fn() }
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("BuiltinNodeTypeList", () => {
  it("renders all 3 built-in node labels", () => {
    render(<BuiltinNodeTypeList />)
    expect(screen.getByText("剧本")).toBeInTheDocument()
    expect(screen.getByText("分镜")).toBeInTheDocument()
    expect(screen.getByText("资产")).toBeInTheDocument()
  })

  it("shows a 内置 · 只读 badge per row", () => {
    render(<BuiltinNodeTypeList />)
    expect(screen.getAllByText("内置 · 只读")).toHaveLength(3)
  })

  it("renders no action buttons (read-only: no 编辑/删除)", () => {
    render(<BuiltinNodeTypeList />)
    expect(screen.queryByRole("button", { name: /编辑/ })).toBeNull()
    expect(screen.queryByRole("button", { name: /删除/ })).toBeNull()
    expect(screen.queryByRole("button", { name: /新建/ })).toBeNull()
  })

  it("shows empty state when there are no types", () => {
    queryState.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
    render(<BuiltinNodeTypeList />)
    expect(screen.getByText(/暂无内置节点类型/)).toBeInTheDocument()
  })
})
