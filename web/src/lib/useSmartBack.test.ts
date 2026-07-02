import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook } from "@testing-library/react"
import { useSmartBack } from "./useSmartBack"

// useRouter/useCanGoBack 走 mock：useSmartBack 只做「有历史→history.back，无历史→fallback」的分支决策。
const back = vi.fn()
let canGoBack = true
vi.mock("@tanstack/react-router", () => ({
  useRouter: () => ({ history: { back } }),
  useCanGoBack: () => canGoBack,
}))

afterEach(() => {
  back.mockReset()
  canGoBack = true
})

describe("useSmartBack", () => {
  it("有应用内历史时真正后退（不触发 fallback）", () => {
    canGoBack = true
    const fallback = vi.fn()
    const { result } = renderHook(() => useSmartBack(fallback))
    result.current()
    expect(back).toHaveBeenCalledTimes(1)
    expect(fallback).not.toHaveBeenCalled()
  })

  it("无历史（深链直进）时兜底执行 fallback", () => {
    canGoBack = false
    const fallback = vi.fn()
    const { result } = renderHook(() => useSmartBack(fallback))
    result.current()
    expect(fallback).toHaveBeenCalledTimes(1)
    expect(back).not.toHaveBeenCalled()
  })
})
