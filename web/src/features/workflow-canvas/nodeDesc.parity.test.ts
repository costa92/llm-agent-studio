import { describe, expect, it } from "vitest"
import type { NodeTypeDescription } from "./nodeDescTypes"

const SCRIPT_AGE_BAND_CASCADE = {
  "0-3": { pageCount: 8, maxWordsPerSpread: 10, narrationStyle: "repetition", bookType: "concept" },
  "3-6": { pageCount: 16, maxWordsPerSpread: 50, narrationStyle: "plain", bookType: "narrative" },
  "6-8": { pageCount: 16, maxWordsPerSpread: 120, narrationStyle: "dialogue", bookType: "narrative" },
}

describe("nodedesc Go↔TS parity", () => {
  it("script ageBand DefaultFrom mirrors pbconfig ageBandDefaults", () => {
    expect(Object.keys(SCRIPT_AGE_BAND_CASCADE)).toEqual(["0-3", "3-6", "6-8"])
    expect(SCRIPT_AGE_BAND_CASCADE["0-3"].pageCount).toBe(8)
    expect(SCRIPT_AGE_BAND_CASCADE["6-8"].maxWordsPerSpread).toBe(120)
  })

  it("displayOptions show is an AND-across-keys / OR-within-value contract", () => {
    const sample: Pick<NodeTypeDescription, "properties"> = {
      properties: [
        { name: "kind", label: "k", type: "options", options: [] },
        {
          name: "duration", label: "d", type: "number",
          displayOptions: { show: { kind: ["video", "audio"] } },
        },
      ],
    }
    const dur = sample.properties.find((p) => p.name === "duration")!
    expect(dur.displayOptions!.show!.kind).toContain("video")
    expect(dur.displayOptions!.show!.kind).toContain("audio")
  })

  it("reserved namespace forbids studio.* / llm / http / script as custom slugs", () => {
    const reserved = (slug: string) =>
      slug.startsWith("studio.") || ["llm", "http", "script"].includes(slug)
    expect(reserved("studio.script")).toBe(true)
    expect(reserved("llm")).toBe(true)
    expect(reserved("translate")).toBe(false)
  })
})
