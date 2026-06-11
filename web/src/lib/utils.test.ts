import { describe, expect, it } from "vitest"
import { cn } from "./utils"

describe("cn", () => {
  it("merges class names and dedupes tailwind conflicts", () => {
    expect(cn("px-2", "px-4")).toBe("px-4")
    const hidden = false
    expect(cn("text-sm", hidden && "hidden", "font-bold")).toBe(
      "text-sm font-bold",
    )
  })
})
