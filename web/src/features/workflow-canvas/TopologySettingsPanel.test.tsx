import { describe, expect, it, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { TopologySettingsPanel } from "./TopologySettingsPanel"
import { DEFAULT_TOPOLOGY_SETTINGS } from "./useTopologySettings"

describe("TopologySettingsPanel", () => {
  it("齿轮带 aria-label；点开后切开关回调 update", () => {
    const update = vi.fn()
    render(
      <TopologySettingsPanel settings={DEFAULT_TOPOLOGY_SETTINGS} update={update} />,
    )
    const gear = screen.getByLabelText("视图设置")
    expect(gear).toBeTruthy()
    fireEvent.click(gear)
    fireEvent.click(screen.getByLabelText("显示耗时"))
    expect(update).toHaveBeenCalledWith({ showTiming: true })
  })
})
