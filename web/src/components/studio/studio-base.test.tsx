import { describe, it, expect } from "vitest"
import { render, screen } from "@testing-library/react"
import { Badge } from "./Badge"
import { Button } from "./Button"
import { Kbd } from "./Kbd"
import { StatCard } from "./StatCard"
import { BarRow } from "./BarRow"
import { WarnStrip } from "./WarnStrip"
import { EventLog } from "./EventLog"
import { SseIndicator } from "./SseIndicator"

describe("studio base components", () => {
  it("Badge renders label + variant class", () => {
    render(<Badge variant="running">生产中</Badge>)
    expect(screen.getByText("生产中")).toBeInTheDocument()
  })

  it("Kbd renders shortcut", () => {
    render(<Kbd>A</Kbd>)
    expect(screen.getByText("A")).toBeInTheDocument()
  })

  it("Button renders children + optional Kbd slot", () => {
    render(
      <Button variant="amber" kbd="R">
        运行
      </Button>,
    )
    expect(screen.getByRole("button", { name: /运行/ })).toBeInTheDocument()
    expect(screen.getByText("R")).toBeInTheDocument()
  })

  it("StatCard renders label, value and unit", () => {
    render(<StatCard label="本月成本" value="123" unit="元" />)
    expect(screen.getByText("本月成本")).toBeInTheDocument()
    expect(screen.getByText("123")).toBeInTheDocument()
    expect(screen.getByText("元")).toBeInTheDocument()
  })

  it("BarRow renders label and value, clamps ratio width", () => {
    render(<BarRow label="项目甲" ratio={1.5} value="¥99" />)
    expect(screen.getByText("项目甲")).toBeInTheDocument()
    expect(screen.getByText("¥99")).toBeInTheDocument()
  })

  it("WarnStrip renders message with status role", () => {
    render(<WarnStrip>已降级到备用模型</WarnStrip>)
    expect(screen.getByRole("status")).toHaveTextContent("已降级到备用模型")
  })

  it("EventLog renders empty state then lines", () => {
    const { rerender } = render(<EventLog lines={[]} />)
    expect(screen.getByText("暂无事件")).toBeInTheDocument()
    rerender(
      <EventLog
        lines={[{ seq: 1, text: "剧本已生成", emphasis: "S2" }]}
      />,
    )
    expect(screen.getByText("剧本已生成")).toBeInTheDocument()
    expect(screen.getByText("S2")).toBeInTheDocument()
  })

  it("SseIndicator renders status label", () => {
    const { rerender } = render(<SseIndicator status="connected" />)
    expect(screen.getByText("实时连接")).toBeInTheDocument()
    rerender(<SseIndicator status="reconnecting" />)
    expect(screen.getByText("重连中…")).toBeInTheDocument()
    rerender(<SseIndicator status="disconnected" />)
    expect(screen.getByText("已断开")).toBeInTheDocument()
  })
})
