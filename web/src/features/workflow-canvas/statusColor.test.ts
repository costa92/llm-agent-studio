import { describe, expect, it } from "vitest"
import { minimapStatusColor, STATUS_VAR } from "./statusColor"

describe("minimapStatusColor", () => {
  it("按状态映射到语义色 token", () => {
    expect(minimapStatusColor("done")).toBe("var(--review)")
    expect(minimapStatusColor("running")).toBe("var(--amber)")
    expect(minimapStatusColor("failed")).toBe("var(--danger)")
    expect(minimapStatusColor("pending")).toBe("var(--line)")
    expect(minimapStatusColor("blocked")).toBe("var(--line)")
  })

  it("undefined → 兜底当 pending（线灰）", () => {
    expect(minimapStatusColor(undefined)).toBe(STATUS_VAR.pending)
  })

  it("done 用 --review 而非 --amber（避开与 running 撞色）", () => {
    expect(minimapStatusColor("done")).not.toBe(minimapStatusColor("running"))
  })
})
