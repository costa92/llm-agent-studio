// Shared helpers for the studio e2e harness.
//
// Two consumption modes:
//   - API scripts (case-*.mjs): use apiLogin() -> session with auto-refresh, pollState().
//     Node 18+ global fetch only — no deps.
//   - UI smoke (smoke-routes.mjs): use loadPlaywright() + browserLogin() (needs playwright-core).
//
// Config is env-driven with dev defaults (see loadConfig / e2e/README.md).

import { createRequire } from "node:module"
import { fileURLToPath, pathToFileURL } from "node:url"
import path from "node:path"

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

export function loadConfig() {
  const cfg = {
    uiBase: process.env.E2E_BASE || "http://localhost:5173",
    apiBase: process.env.E2E_API || "http://localhost:8083",
    org: process.env.E2E_ORG || "169278fcd0dec7d485c741215a578fab",
    email: process.env.E2E_EMAIL || "demo@studio.com",
    password: process.env.E2E_PASSWORD || "SmokeP2A#2026",
  }
  return cfg
}

// ---------------------------------------------------------------------------
// API session (JWT access_token, expires_in=900s → auto re-login on 401)
// ---------------------------------------------------------------------------

// apiLogin(apiBase, email, password) -> session
// The returned session exposes `.token` (the raw JWT) plus `.fetch`/`.json`
// helpers that transparently re-login and retry once on a 401. Auto-refresh is
// load-bearing: a picture-book generation can outrun the 15-minute token TTL,
// so long polls MUST go through the session (not a captured token string).
export async function apiLogin(apiBase, email, password) {
  const doLogin = async () => {
    const res = await fetch(`${apiBase}/api/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, password }),
    })
    if (!res.ok) {
      throw new Error(`login failed ${res.status}: ${(await res.text()).slice(0, 300)}`)
    }
    const body = await res.json()
    if (!body.access_token) throw new Error("login response missing access_token")
    return body.access_token
  }

  let token = await doLogin()

  const withAuth = (opts, t) => ({
    ...opts,
    redirect: opts.redirect ?? "follow",
    headers: { Authorization: `Bearer ${t}`, ...(opts.headers || {}) },
  })

  const session = {
    apiBase,
    get token() {
      return token
    },
    async fetch(pathOrUrl, opts = {}) {
      const url = pathOrUrl.startsWith("http") ? pathOrUrl : `${apiBase}${pathOrUrl}`
      let res = await fetch(url, withAuth(opts, token))
      if (res.status === 401) {
        token = await doLogin()
        res = await fetch(url, withAuth(opts, token))
      }
      return res
    },
    // JSON helper: throws on non-2xx with a trimmed body for diagnostics.
    async json(pathOrUrl, opts = {}) {
      const res = await this.fetch(pathOrUrl, opts)
      const text = await res.text()
      let parsed = null
      if (text) {
        try {
          parsed = JSON.parse(text)
        } catch {
          parsed = text
        }
      }
      if (!res.ok) {
        const method = opts.method || "GET"
        throw new Error(`${method} ${pathOrUrl} → ${res.status}: ${text.slice(0, 300)}`)
      }
      return parsed
    },
  }
  return session
}

// pollState(session, projectId, opts) -> final /state payload
// Polls GET /api/projects/{id}/state until `until(state)` is true (default:
// runStatus === "done"). Throws on a failed/canceled run or on timeout.
//
// IMPORTANT: success/failure is judged on the AUTHORITATIVE `state.status`
// field (7-state: failed/canceled/completed/review/...), NOT on `runStatus`.
// runStatus collapses completed|review|failed|canceled ALL to "done" (see
// projectstate/state.go runStatusFor), so a failed run also reports
// runStatus==="done" — the old `runStatus==="failed"` branch was dead code and
// let a FAILED run pass as success. We throw on status failed/canceled BEFORE
// the `until` success check so a terminal failure can never false-green.
export async function pollState(session, projectId, opts = {}) {
  const {
    timeoutMs = 15 * 60 * 1000,
    intervalMs = 5000,
    until = (s) => s.runStatus === "done",
    onTick,
  } = opts
  const deadline = Date.now() + timeoutMs
  let last = null
  while (Date.now() < deadline) {
    last = await session.json(`/api/projects/${projectId}/state`)
    if (onTick) onTick(last)
    if (last.status === "failed" || last.status === "canceled") {
      throw new Error(`run ${last.status}: ${JSON.stringify(last).slice(0, 400)}`)
    }
    if (until(last)) return last
    await sleep(intervalMs)
  }
  throw new Error(
    `pollState timed out after ${timeoutMs}ms (last runStatus=${last?.runStatus ?? "?"})`,
  )
}

// ---------------------------------------------------------------------------
// Playwright loader (UI smoke only)
// ---------------------------------------------------------------------------

// Resolve playwright-core without assuming the caller's cwd. Node's ESM loader
// ignores NODE_PATH, so we resolve the package explicitly from a candidate list:
//   1. $PLAYWRIGHT_CORE  (a node_modules dir override)
//   2. <repo>/web/node_modules  (committed devDependency — the default)
//   3. the gstack skills node_modules  (fallback for offline sandboxes)
export async function loadPlaywright() {
  const here = path.dirname(fileURLToPath(import.meta.url)) // e2e/lib
  const repoRoot = path.resolve(here, "..", "..")
  const candidates = [
    process.env.PLAYWRIGHT_CORE,
    path.join(repoRoot, "web", "node_modules"),
    "/home/hellotalk/.claude/skills/gstack/node_modules",
  ].filter(Boolean)

  const require = createRequire(import.meta.url)
  const tried = []
  for (const base of candidates) {
    try {
      const resolved = require.resolve("playwright-core", { paths: [base] })
      return await import(pathToFileURL(resolved).href)
    } catch (err) {
      tried.push(`${base} (${err.code || err.message})`)
    }
  }
  throw new Error(
    "playwright-core not found. Run `pnpm install` in web/, or set PLAYWRIGHT_CORE " +
      `to a node_modules dir containing it. Tried:\n  ${tried.join("\n  ")}`,
  )
}

export async function launchBrowser() {
  const pw = await loadPlaywright()
  // `chromium` is a named export in some builds and lives on the default export
  // in others — accept either.
  const chromium = pw.chromium || pw.default?.chromium
  if (!chromium) throw new Error("playwright-core: could not find chromium launcher")
  return chromium.launch({
    executablePath: process.env.CHROME_BIN || "/usr/bin/google-chrome",
    headless: true,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  })
}

// browserLogin(page, cfg): form-login through the live UI. The SPA keeps the
// access_token in memory only (no localStorage), so the browser session must
// stay alive after this returns — you cannot inject a token into storage.
export async function browserLogin(page, cfg) {
  const { uiBase, email, password } = cfg
  await page.goto(`${uiBase}/login`, { waitUntil: "networkidle", timeout: 30000 })
  await page.fill("#email", email)
  await page.fill("#password", password)
  await Promise.all([
    page.waitForURL((url) => !new URL(url).pathname.startsWith("/login"), {
      timeout: 20000,
    }),
    page.click('button[type="submit"]'),
  ])
}
