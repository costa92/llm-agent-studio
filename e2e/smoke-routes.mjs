// Cheap default e2e check: form-login through the live UI, then visit every
// _authed route from the cookbook (§4) and assert each one renders — i.e. it did
// NOT redirect back to /login and it did NOT paint an empty page / error boundary.
// No generation is triggered, so this is safe to run repeatedly (0 API cost).
//
// Usage:
//   E2E_ORG=<org> node e2e/smoke-routes.mjs
// Prereqs: studiod on :8083, vite on :5173, playwright-core resolvable
// (pnpm install in web/, or PLAUGHT... see e2e/README.md). CHROME_BIN optional.

import { loadConfig, apiLogin, launchBrowser, browserLogin } from "./lib/session.mjs"

const cfg = loadConfig()

// Org-level routes never need a project id — always visited.
function orgRoutes(org) {
  return [
    ["/", "landing / org home"],
    [`/orgs/${org}/projects`, "projects list"],
    [`/orgs/${org}/tasks`, "task center"],
    [`/orgs/${org}/assets`, "asset library"],
    [`/orgs/${org}/review`, "HITL review queue"],
    [`/orgs/${org}/prompt`, "prompt builder"],
    [`/orgs/${org}/model-configs`, "model config admin"],
    [`/orgs/${org}/custom-node-types`, "custom node type registry"],
    [`/orgs/${org}/builtin-node-types`, "built-in node catalog"],
    [`/orgs/${org}/storage-config`, "storage config admin"],
    [`/orgs/${org}/secrets`, "org secrets"],
    [`/orgs/${org}/members`, "member management"],
    [`/orgs/${org}/cost`, "cost center"],
  ]
}

// Project-scoped routes need a real project id; workflow route also wants ?wf=.
// We discover both via the API so the smoke test degrades gracefully on a fresh
// org (skips the param routes with a notice instead of 404-ing).
async function projectRoutes(session, org) {
  const routes = []
  let pid = null
  let wf = null
  try {
    const list = await session.json(`/api/orgs/${org}/projects?limit=20`)
    const items = list.items || list.projects || []
    if (items.length) pid = items[0].id
    // Prefer a project that has a workflow so we can exercise the canvas route.
    for (const p of items) {
      const wfs = await session
        .json(`/api/projects/${p.id}/workflows`)
        .catch(() => ({ items: [] }))
      const wfItems = wfs.items || wfs.workflows || (Array.isArray(wfs) ? wfs : [])
      if (wfItems.length) {
        pid = p.id
        wf = wfItems[0].id
        break
      }
    }
  } catch (err) {
    console.warn(`  (could not list projects: ${err.message})`)
  }

  if (!pid) return { routes, skipped: "no project on org → project-scoped routes skipped" }

  routes.push(
    [`/orgs/${org}/projects/${pid}`, "project overview"],
    [`/orgs/${org}/projects/${pid}/runs`, "runs list"],
  )
  if (wf) {
    routes.push([
      `/orgs/${org}/projects/${pid}/workflow?wf=${wf}`,
      "workflow canvas",
    ])
  } else {
    routes.push([`/orgs/${org}/projects/${pid}/workflow?mode=create`, "workflow canvas (blank)"])
  }
  return { routes, skipped: null }
}

async function checkRoute(page, uiBase, [route, label]) {
  const url = `${uiBase}${route}`
  await page.goto(url, { waitUntil: "networkidle", timeout: 30000 })
  await page.waitForTimeout(400)

  const finalPath = new URL(page.url()).pathname
  if (finalPath.startsWith("/login")) {
    throw new Error("redirected to /login (auth lost / route not authed)")
  }

  const verdict = await page.evaluate(() => {
    const root = document.querySelector("#root")
    const bodyText = (document.body.innerText || "").trim()
    const rendered = !!root && root.childElementCount > 0 && bodyText.length > 0
    const errorBoundary =
      /(应用出错|页面出错|渲染错误|出错了|something went wrong|unexpected application error)/i.test(
        bodyText,
      )
    return { rendered, errorBoundary, len: bodyText.length }
  })

  if (!verdict.rendered) throw new Error("empty render (#root has no visible content)")
  if (verdict.errorBoundary) throw new Error("error boundary rendered")
  return label
}

async function main() {
  console.log(`[smoke] UI=${cfg.uiBase} API=${cfg.apiBase} org=${cfg.org}`)

  // API session drives route discovery (project/workflow ids) and login-cred check.
  const session = await apiLogin(cfg.apiBase, cfg.email, cfg.password)
  const { routes: projRoutes, skipped } = await projectRoutes(session, cfg.org)
  const routes = [...orgRoutes(cfg.org), ...projRoutes]

  const browser = await launchBrowser()
  const page = await browser.newPage()
  let pass = 0
  const failures = []
  try {
    await browserLogin(page, cfg)
    console.log("[smoke] form-login OK\n")
    for (const r of routes) {
      try {
        const label = await checkRoute(page, cfg.uiBase, r)
        pass++
        console.log(`  PASS  ${r[0]}  (${label})`)
      } catch (err) {
        failures.push([r[0], err.message])
        console.log(`  FAIL  ${r[0]}  → ${err.message}`)
      }
    }
  } finally {
    await browser.close()
  }

  console.log("")
  if (skipped) console.log(`[smoke] note: ${skipped}`)
  console.log(`[smoke] ${pass}/${routes.length} routes rendered`)
  if (failures.length) {
    console.error(`[smoke] FAILED (${failures.length}):`)
    for (const [r, m] of failures) console.error(`  - ${r}: ${m}`)
    process.exit(1)
  }
  console.log("[smoke] OK")
}

main().catch((err) => {
  console.error("[smoke] fatal:", err.message)
  process.exit(1)
})
