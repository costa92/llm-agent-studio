import { useCallback, useSyncExternalStore } from "react"

// 订阅一条 CSS media query 的匹配状态（SSR / 老 webview 安全）。
// 用 useSyncExternalStore 直接读 matchMedia，避免在 effect 里 setState。
// matchMedia 缺失时静默回落为「不匹配」（不崩溃）。
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (typeof window === "undefined" || !window.matchMedia) return () => {}
      const mql = window.matchMedia(query)
      // 老 Safari 用 addListener/removeListener，其余用 addEventListener。
      if (mql.addEventListener) {
        mql.addEventListener("change", onChange)
        return () => mql.removeEventListener("change", onChange)
      }
      mql.addListener(onChange)
      return () => mql.removeListener(onChange)
    },
    [query],
  )

  const getSnapshot = useCallback(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false
    return window.matchMedia(query).matches
  }, [query])

  // 服务端快照恒为 false（无窗口），与首帧客户端一致以避免 hydration 抖动。
  return useSyncExternalStore(subscribe, getSnapshot, () => false)
}
