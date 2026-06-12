import { describe, expect, it } from "vitest"
import { formatRelative } from "./relativeTime"

// 固定 now，避免依赖真实 Date.now()。基准：2026-06-12T12:00:00Z。
const NOW = Date.parse("2026-06-12T12:00:00Z")

describe("formatRelative", () => {
  it("returns 刚刚 for <1 minute", () => {
    expect(formatRelative("2026-06-12T11:59:30Z", NOW)).toBe("刚刚")
  })
  it("returns N分钟前 for <1 hour", () => {
    expect(formatRelative("2026-06-12T11:58:00Z", NOW)).toBe("2分钟前")
  })
  it("returns N小时前 for <24 hours", () => {
    expect(formatRelative("2026-06-12T09:00:00Z", NOW)).toBe("3小时前")
  })
  it("returns N天前 for <30 days", () => {
    expect(formatRelative("2026-06-10T12:00:00Z", NOW)).toBe("2天前")
  })
  it("falls back to a date for older timestamps", () => {
    const out = formatRelative("2026-01-01T12:00:00Z", NOW)
    expect(out).not.toMatch(/前|刚刚/)
  })
  it("treats future timestamps as 刚刚", () => {
    expect(formatRelative("2026-06-12T12:05:00Z", NOW)).toBe("刚刚")
  })
  it("returns the raw input for an unparseable string", () => {
    expect(formatRelative("not-a-date", NOW)).toBe("not-a-date")
  })
})
