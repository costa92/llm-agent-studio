import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { act, renderHook } from "@testing-library/react"
import { useTopologySettings, DEFAULT_TOPOLOGY_SETTINGS } from "./useTopologySettings"

beforeEach(() => localStorage.clear())
afterEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})

describe("useTopologySettings", () => {
  it("无存储时返回默认值", () => {
    const { result } = renderHook(() => useTopologySettings("proj-A"))
    expect(result.current.settings).toEqual(DEFAULT_TOPOLOGY_SETTINGS)
  })

  it("update 持久化并立即生效", () => {
    const { result } = renderHook(() => useTopologySettings("proj-A"))
    act(() => result.current.update({ showTiming: true }))
    expect(result.current.settings.showTiming).toBe(true)
    const { result: r2 } = renderHook(() => useTopologySettings("proj-A"))
    expect(r2.current.settings.showTiming).toBe(true)
  })

  it("layout 按项目隔离；全局偏好跨项目共享", () => {
    const { result: a } = renderHook(() => useTopologySettings("proj-A"))
    act(() => a.current.update({ layout: "LR", showTiming: true }))
    const { result: b } = renderHook(() => useTopologySettings("proj-B"))
    expect(b.current.settings.layout).toBe("saved")
    expect(b.current.settings.showTiming).toBe(true)
  })

  it("坏 JSON 回落默认值，不抛", () => {
    localStorage.setItem("studio.topology.prefs", "{not json")
    localStorage.setItem("studio.topology.layout.proj-A", "garbage")
    const { result } = renderHook(() => useTopologySettings("proj-A"))
    expect(result.current.settings).toEqual(DEFAULT_TOPOLOGY_SETTINGS)
  })

  it("同一实例 rerender 换 projectId 后反映新项目存储", () => {
    localStorage.setItem("studio.topology.layout.proj-B", "LR")
    const { result, rerender } = renderHook(
      ({ pid }) => useTopologySettings(pid),
      { initialProps: { pid: "proj-A" } },
    )
    expect(result.current.settings.layout).toBe("saved")
    rerender({ pid: "proj-B" })
    expect(result.current.settings.layout).toBe("LR")
  })

  it("写盘失败时状态仍更新，不抛", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("QuotaExceededError")
    })
    const { result } = renderHook(() => useTopologySettings("proj-A"))
    act(() => result.current.update({ showTiming: true }))
    expect(result.current.settings.showTiming).toBe(true)
  })
})
