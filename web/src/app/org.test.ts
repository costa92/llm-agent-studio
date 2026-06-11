import { describe, expect, it } from "vitest"
import { cleanOrg, hasEmptyOrgPath, sanitizeLoginRedirect } from "./org"

describe("org routing helpers", () => {
  it("cleans org params", () => {
    expect(cleanOrg(" acme ")).toBe("acme")
    expect(cleanOrg(undefined)).toBe("")
  })

  it("detects empty org paths", () => {
    expect(hasEmptyOrgPath("/orgs//projects")).toBe(true)
    expect(hasEmptyOrgPath("/orgs/")).toBe(true)
    expect(hasEmptyOrgPath("/orgs/acme/projects")).toBe(false)
  })

  it("sanitizes login redirects with missing org", () => {
    expect(sanitizeLoginRedirect("/orgs//projects")).toBe("/")
    expect(sanitizeLoginRedirect("/orgs/acme/projects")).toBe("/orgs/acme/projects")
    expect(sanitizeLoginRedirect(undefined)).toBe("/")
  })
})
