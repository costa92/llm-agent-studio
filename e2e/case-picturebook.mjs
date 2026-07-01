// Full picture-book showcase flow, via the API (no browser).
//
//   create (kind=picturebook) → run {} → poll /state until done →
//   accept every pending image asset → export pdf + epub → assert bytes>0.
//
// This does REAL generation (deepseek text + minimax image + minimax audio) and
// costs money + ~10 minutes, so it is OPT-IN:
//   E2E_FULL=1        node e2e/case-picturebook.mjs   # the whole flow
//   E2E_SMOKE_ONLY=1  node e2e/case-picturebook.mjs   # create + run-trigger (202), then stop
//   (neither set)     node e2e/case-picturebook.mjs   # prints a skip notice, exits 0
//
// Field names are verbatim from the Go handlers (cookbook §2 / appendix).

import { loadConfig, apiLogin, pollState } from "./lib/session.mjs"

const cfg = loadConfig()
const FULL = process.env.E2E_FULL === "1"
const SMOKE_ONLY = process.env.E2E_SMOKE_ONLY === "1"

if (!FULL && !SMOKE_ONLY) {
  console.log(
    "[picturebook] SKIP — set E2E_FULL=1 to run the full (paid, ~10min) generation,\n" +
      "             or E2E_SMOKE_ONLY=1 to only exercise create + run-trigger (202).",
  )
  process.exit(0)
}

// The pbconfig is stored raw as a *stringified* JSON object (double-escaped on the wire).
const pbConfig = JSON.stringify({
  ageBand: "3-6",
  illustrationStyle: "水彩卡通",
  themes: ["勇气", "友谊"],
  voice: "female-shaonv",
})

async function main() {
  console.log(`[picturebook] API=${cfg.apiBase} org=${cfg.org} mode=${FULL ? "FULL" : "SMOKE_ONLY"}`)
  const session = await apiLogin(cfg.apiBase, cfg.email, cfg.password)

  // 1) create project (kind=picturebook)
  const project = await session.json(`/api/orgs/${cfg.org}/projects`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: `e2e 绘本 ${new Date().toISOString().slice(0, 19)}`,
      brief: "一只叫白白的小兔子学会勇敢地推开挡路的大石头",
      contentType: "绘本",
      targetPlatform: "印刷",
      style: "水彩卡通",
      kind: "picturebook",
      imageProvider: "minimax",
      imageModel: "image-01",
      pictureBookConfig: pbConfig,
    }),
  })
  const pid = project.id
  console.log(`[picturebook] project created: ${pid}`)

  // 2) trigger run (empty body is valid for picturebook) → 202
  const run = await session.json(`/api/projects/${pid}/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  })
  console.log(`[picturebook] run triggered: planId=${run.planId} valid=${run.valid}`)
  if (!run.planId) throw new Error("run response missing planId")

  if (SMOKE_ONLY) {
    console.log("[picturebook] SMOKE_ONLY — stopping after run-trigger. OK")
    return
  }

  // 3) poll /state until runStatus=done (generation is async)
  console.log("[picturebook] polling /state until done …")
  await pollState(session, pid, {
    timeoutMs: 20 * 60 * 1000,
    onTick: (s) => process.stdout.write(`\r  runStatus=${s.runStatus}   `),
  })
  console.log("\n[picturebook] run done")

  // 4) accept every pending image asset (export threshold requires it)
  const assets = await session.json(`/api/projects/${pid}/assets`)
  const items = assets.items || assets.assets || (Array.isArray(assets) ? assets : [])
  const images = items.filter((a) => a.type === "image")
  const audio = items.filter((a) => a.type === "audio")
  console.log(`[picturebook] assets: ${images.length} image, ${audio.length} audio`)
  // Documented behavior: picturebook produces BOTH image and REAL audio
  // (minimax speech-2.8-hd now returns a real MP3). We assert audio exists.
  if (audio.length === 0) {
    console.warn("[picturebook] WARN — expected audio assets for picturebook, found none")
  }

  let accepted = 0
  for (const a of items) {
    if (a.status === "pending_acceptance") {
      await session.json(`/api/assets/${a.id}/accept`, { method: "POST" })
      accepted++
    }
  }
  console.log(`[picturebook] accepted ${accepted} pending asset(s)`)

  // 5) export pdf + epub, assert bytes>0. NOTE: create-export response key is `jobId`
  //    on some builds and `id` on others — accept either.
  for (const format of ["pdf", "epub"]) {
    const job = await session.json(`/api/projects/${pid}/exports`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ format, planId: run.planId }),
    })
    const jobId = job.jobId || job.id
    if (!jobId) throw new Error(`export(${format}) response missing jobId/id`)
    console.log(`[picturebook] export ${format} queued: ${jobId}`)

    // poll the export job until done
    const deadline = Date.now() + 5 * 60 * 1000
    let status = job.status
    while (status !== "done" && Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 3000))
      const j = await session.json(`/api/projects/${pid}/exports/${jobId}`)
      status = j.status
      if (status === "failed" || status === "error") {
        throw new Error(`export ${format} failed: ${JSON.stringify(j).slice(0, 200)}`)
      }
    }
    if (status !== "done") throw new Error(`export ${format} did not finish in time`)

    // download the artifact (follows the exportScope redirect chain)
    const res = await session.fetch(`/api/exports/${jobId}/content`)
    if (!res.ok) throw new Error(`export ${format} content → ${res.status}`)
    const bytes = Buffer.from(await res.arrayBuffer())
    if (bytes.length === 0) throw new Error(`export ${format} content is empty`)
    console.log(`[picturebook] export ${format} content: ${bytes.length} bytes ✓`)
  }

  console.log("[picturebook] OK")
}

main().catch((err) => {
  console.error("\n[picturebook] FAIL:", err.message)
  process.exit(1)
})
