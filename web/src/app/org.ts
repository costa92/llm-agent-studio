import { redirect } from "@tanstack/react-router"

export function cleanOrg(value: string | undefined): string {
  return value?.trim() ?? ""
}

export function hasEmptyOrgPath(path: string): boolean {
  return /\/orgs\/(?=\/|$)/.test(path) || /^\/orgs\/(projects|review|assets|prompt|cost|model-configs)(\/|$)/.test(path)
}

export function sanitizeLoginRedirect(redirectTo: string | undefined): string {
  if (redirectTo == null || hasEmptyOrgPath(redirectTo)) return "/"
  return redirectTo
}

export function requireOrgParam(params: { org?: string }): void {
  if (cleanOrg(params.org) === "") {
    throw redirect({ to: "/" })
  }
}
