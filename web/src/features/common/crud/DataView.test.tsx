import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { DataView } from "./DataView"

interface Row { id: string; name: string; kind: string }
const rows: Row[] = [
  { id: "a", name: "Alpha", kind: "text" },
  { id: "b", name: "Beta", kind: "image" },
]

describe("DataView", () => {
  it("table 模式渲染列 + 行动作", async () => {
    const onEdit = vi.fn()
    render(
      <DataView<Row> layout="table" items={rows} getId={(r) => r.id}
        columns={[{ key: "name", header: "名称", cell: (r) => r.name }]}
        rowActions={[{ label: "编辑", onClick: onEdit }]} />,
    )
    expect(screen.getByText("名称")).toBeInTheDocument()
    expect(screen.getByText("Alpha")).toBeInTheDocument()
    await userEvent.click(screen.getAllByRole("button", { name: "编辑" })[0])
    expect(onEdit).toHaveBeenCalledWith(rows[0])
  })

  it("cards 模式用 renderCard，actions 传入卡片", () => {
    render(
      <DataView<Row> layout="cards" items={rows} getId={(r) => r.id}
        rowActions={[{ label: "删除", onClick: () => {} }]}
        renderCard={(r, actions) => (
          <div data-testid="card">{r.name}{actions}</div>
        )} />,
    )
    expect(screen.getAllByTestId("card")).toHaveLength(2)
    expect(screen.getAllByRole("button", { name: "删除" })).toHaveLength(2)
  })

  it("cards 模式 groupBy 按组渲染分组标题", () => {
    render(
      <DataView<Row> layout="cards" items={rows} getId={(r) => r.id}
        groupBy={(r) => r.kind}
        renderCard={(r) => <div>{r.name}</div>} />,
    )
    expect(screen.getByText("text")).toBeInTheDocument()
    expect(screen.getByText("image")).toBeInTheDocument()
  })

  it("hidden 的行动作不渲染", () => {
    render(
      <DataView<Row> layout="table" items={rows} getId={(r) => r.id}
        columns={[{ key: "name", header: "名称", cell: (r) => r.name }]}
        rowActions={[{ label: "设默认", onClick: () => {}, hidden: (r) => r.id === "a" }]} />,
    )
    expect(screen.getAllByRole("button", { name: "设默认" })).toHaveLength(1)
  })
})
