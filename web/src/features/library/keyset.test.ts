import { describe, expect, it } from "vitest"
import { flattenPages, nextCursorParam, type LibraryPage } from "./keyset"
import { buildLibraryQuery } from "./filter"
import type { Asset } from "@/lib/types"

function makeAsset(id: string, over: Partial<Asset> = {}): Asset {
  return {
    id,
    projectId: "p1",
    shotId: "",
    todoId: "",
    type: "image",
    blobKey: "k",
    url: "",
    prompt: "p",
    style: "国风",
    provider: "openai",
    model: "gpt-image-1",
    status: "accepted",
    version: 1,
    parentAssetId: "",
    tags: [],
    prescreenScore: 0,
    prescreenFlags: [],
    prescreenNote: "",
    externalJobId: "",
    ...over,
  }
}

function page(ids: string[], next: string): LibraryPage {
  return { items: ids.map((id) => makeAsset(id)), next_cursor: next }
}

// ── keyset 游标停止条件 ─────────────────────────────────────────────
describe("nextCursorParam", () => {
  it("returns the cursor when next_cursor is non-empty (more pages)", () => {
    expect(nextCursorParam(page(["a", "b"], "b"))).toBe("b")
  })

  it("returns undefined when next_cursor is empty (no more pages → stop)", () => {
    expect(nextCursorParam(page(["a"], ""))).toBeUndefined()
  })
})

// ── 多页串接累积 + 去重 ─────────────────────────────────────────────
describe("flattenPages", () => {
  it("concatenates multiple pages in order", () => {
    const pages = [page(["a", "b"], "b"), page(["c", "d"], "d")]
    expect(flattenPages(pages).map((a) => a.id)).toEqual(["a", "b", "c", "d"])
  })

  it("returns [] for undefined (initial / loading)", () => {
    expect(flattenPages(undefined)).toEqual([])
  })

  it("dedupes by id across page boundaries (defensive)", () => {
    const pages = [page(["a", "b"], "b"), page(["b", "c"], "")]
    expect(flattenPages(pages).map((a) => a.id)).toEqual(["a", "b", "c"])
  })
})

// ── 只发后端支持的过滤项 ────────────────────────────────────────────
describe("buildLibraryQuery", () => {
  it("emits only non-empty backend-supported params", () => {
    const qs = buildLibraryQuery(
      { type: "image", status: "accepted", style: "国风", project: "p1", tag: "hero" },
      24,
    )
    const params = new URLSearchParams(qs)
    expect(params.get("type")).toBe("image")
    expect(params.get("status")).toBe("accepted")
    expect(params.get("style")).toBe("国风")
    expect(params.get("project")).toBe("p1")
    expect(params.get("tag")).toBe("hero")
    expect(params.get("limit")).toBe("24")
  })

  it("omits empty filter fields (backend skips empty-string filters)", () => {
    const qs = buildLibraryQuery({ type: "image" })
    const params = new URLSearchParams(qs)
    expect(params.get("type")).toBe("image")
    expect(params.has("status")).toBe(false)
    expect(params.has("style")).toBe(false)
    expect(params.has("project")).toBe(false)
    expect(params.has("tag")).toBe(false)
    // limit 未传则不下发（后端用默认 50）。
    expect(params.has("limit")).toBe(false)
  })
})
