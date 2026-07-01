// Full music-creation showcase flow, via the API (no browser). This is the
// custom-node canvas path (n8n-style workflow), NOT the built-in picturebook.
//
//   register custom-node-type (kind=llm, "作词编曲") →
//   create project (kind=custom) →
//   create workflow: lyrics(custom:作词编曲) → script-1(script, var song←lyrics.lyrics)
//                    → board-1(storyboard) + inputsSchema[theme] →
//   run {inputs:{theme}} → poll /state until done →
//   assert IMAGE assets from the storyboard fan-out, and assert NO AUDIO asset.
//
// The no-audio assertion is the load-bearing bit: a *non-picturebook* storyboard
// fan-out produces IMAGES ONLY. (Real audio is a picturebook-only path.)
//
// REAL generation → OPT-IN, same gating as case-picturebook.mjs:
//   E2E_FULL=1        node e2e/case-music-workflow.mjs   # full flow
//   E2E_SMOKE_ONLY=1  node e2e/case-music-workflow.mjs   # register + create + run-trigger (202)
//   (neither set)     node e2e/case-music-workflow.mjs   # skip notice, exit 0
//
// MIN_IMAGES (default 1) sets the MINIMUM asserted storyboard fan-out image count.
// The exact count is LLM-determined (one image per generated shot), so this is a
// lower bound, not an equality check. The load-bearing assertion is "0 audio".

import { loadConfig, apiLogin, pollState } from "./lib/session.mjs"

const cfg = loadConfig()
const FULL = process.env.E2E_FULL === "1"
const SMOKE_ONLY = process.env.E2E_SMOKE_ONLY === "1"
const MIN_IMAGES = Number(process.env.MIN_IMAGES || "1")

if (!FULL && !SMOKE_ONLY) {
  console.log(
    "[music] SKIP — set E2E_FULL=1 to run the full (paid) generation,\n" +
      "        or E2E_SMOKE_ONLY=1 to only exercise register + create + run-trigger (202).",
  )
  process.exit(0)
}

async function main() {
  console.log(`[music] API=${cfg.apiBase} org=${cfg.org} mode=${FULL ? "FULL" : "SMOKE_ONLY"}`)
  const session = await apiLogin(cfg.apiBase, cfg.email, cfg.password)

  // 1) register the LLM custom node type. Idempotent: the slug is derived from the
  //    label and is org-unique, so on a re-run we reuse the existing "作词编曲" type
  //    instead of hitting the 23505 uniqueness violation.
  const LABEL = "作词编曲"
  const existing = await session.json(`/api/orgs/${cfg.org}/custom-node-types`)
  const existingItems = existing.items || (Array.isArray(existing) ? existing : [])
  let lyricsTid = existingItems.find((t) => t.label === LABEL)?.id
  if (lyricsTid) {
    console.log(`[music] reusing existing custom-node-type: ${lyricsTid} (${LABEL})`)
  } else {
    const lyricsType = await session.json(`/api/orgs/${cfg.org}/custom-node-types`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        label: LABEL,
        color: "#7c93ff",
        kind: "llm",
        params: {
          model: "deepseek-chat",
          outputFormat: "json",
          systemPrompt:
            '你是华语流行音乐制作人。输出 JSON: {"title":..,"lyrics":..,"mood":..,"coverPrompt":..}',
          userPrompt: "根据主题创作一首歌：{{theme}}",
        },
      }),
    })
    lyricsTid = lyricsType.id
    console.log(`[music] custom-node-type registered: ${lyricsTid} (${LABEL}, kind=llm)`)
  }

  // 2) create the custom-kind project to hold the workflow
  const project = await session.json(`/api/orgs/${cfg.org}/projects`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: `e2e 音乐工坊 ${new Date().toISOString().slice(0, 19)}`,
      brief: "给主题生成歌曲与封面",
      kind: "custom",
    }),
  })
  const pid = project.id
  console.log(`[music] project created: ${pid}`)

  // 3) create the workflow (lyrics LLM → script → storyboard fan-out)
  const workflow = await session.json(`/api/projects/${pid}/workflows`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: "歌曲+封面",
      inputsSchema: [{ name: "theme", label: "主题", type: "text", target: "brief" }],
      nodes: [
        {
          id: "lyrics",
          type: "custom:作词编曲",
          typeId: lyricsTid,
          typeVersion: 1,
          dependsOn: [],
        },
        {
          id: "script-1",
          type: "script",
          dependsOn: ["lyrics"],
          varBindings: [{ name: "song", sourceNodeId: "lyrics", sourceField: "lyrics" }],
        },
        { id: "board-1", type: "storyboard", dependsOn: ["script-1"] },
      ],
    }),
  })
  const wf = workflow.id
  console.log(`[music] workflow created: ${wf}`)

  // 4) run the workflow with the theme input → 202
  const run = await session.json(`/api/projects/${pid}/workflows/${wf}/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ inputs: { theme: "夏夜的海边" } }),
  })
  console.log(`[music] run triggered: planId=${run.planId} workflowId=${run.workflowId}`)
  if (!run.planId) throw new Error("run response missing planId")

  if (SMOKE_ONLY) {
    console.log("[music] SMOKE_ONLY — stopping after run-trigger. OK")
    return
  }

  // 5) poll until done
  console.log("[music] polling /state until done …")
  await pollState(session, pid, {
    timeoutMs: 20 * 60 * 1000,
    onTick: (s) => process.stdout.write(`\r  runStatus=${s.runStatus}   `),
  })
  console.log("\n[music] run done")

  // 6) assert the documented asset shape: images from fan-out, NO audio.
  const assets = await session.json(`/api/projects/${pid}/assets`)
  const items = assets.items || assets.assets || (Array.isArray(assets) ? assets : [])
  const images = items.filter((a) => a.type === "image")
  const audio = items.filter((a) => a.type === "audio")
  console.log(`[music] assets: ${images.length} image, ${audio.length} audio`)

  if (audio.length !== 0) {
    throw new Error(
      `expected NO audio in a custom storyboard fan-out, found ${audio.length}`,
    )
  }
  // Shot count is LLM-determined, so assert a lower bound rather than an exact
  // count — the storyboard fan-out must produce at least one image.
  if (images.length < MIN_IMAGES) {
    throw new Error(
      `expected at least ${MIN_IMAGES} image asset(s) from the storyboard fan-out (set MIN_IMAGES to override), found ${images.length}`,
    )
  }
  console.log(`[music] ✓ ${images.length} image assets (≥${MIN_IMAGES}), 0 audio (documented behavior)`)
  console.log("[music] OK")
}

main().catch((err) => {
  console.error("\n[music] FAIL:", err.message)
  process.exit(1)
})
