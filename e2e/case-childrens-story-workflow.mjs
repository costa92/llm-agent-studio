// Full children's-story showcase flow, via the API (no browser). This is the
// custom-node canvas path (n8n-style workflow), NOT the (deleted) built-in
// picturebook pipeline — a "children's book" is now a CUSTOM NODE WORKFLOW,
// structurally identical to the music case.
//
//   register custom-node-type (kind=llm, "儿童故事作家") →
//   create project (kind=custom) →
//   create workflow: story(custom:儿童故事作家) → script-1(script, var text←story.story)
//                    → board-1(storyboard) + inputsSchema[theme] →
//   run {inputs:{theme}} → poll /state until done →
//   assert IMAGE assets from the storyboard fan-out, and assert NO AUDIO asset.
//
// The no-audio assertion is the load-bearing bit: a *non-picturebook* storyboard
// fan-out produces IMAGES ONLY. (Real audio is a picturebook-only path, and the
// picturebook pipeline was deleted — so there is no audio here at all.)
//
// REAL generation → OPT-IN, same gating as case-music-workflow.mjs:
//   E2E_FULL=1        node e2e/case-childrens-story-workflow.mjs   # full flow
//   E2E_SMOKE_ONLY=1  node e2e/case-childrens-story-workflow.mjs   # register + create + run-trigger (202)
//   (neither set)     node e2e/case-childrens-story-workflow.mjs   # skip notice, exit 0
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
    "[story] SKIP — set E2E_FULL=1 to run the full (paid) generation,\n" +
      "        or E2E_SMOKE_ONLY=1 to only exercise register + create + run-trigger (202).",
  )
  process.exit(0)
}

async function main() {
  console.log(`[story] API=${cfg.apiBase} org=${cfg.org} mode=${FULL ? "FULL" : "SMOKE_ONLY"}`)
  const session = await apiLogin(cfg.apiBase, cfg.email, cfg.password)

  // 1) register the LLM custom node type. Idempotent: the slug is derived from the
  //    label and is org-unique, so on a re-run we reuse the existing "儿童故事作家"
  //    type instead of hitting the 23505 uniqueness violation.
  const LABEL = "儿童故事作家"
  const existing = await session.json(`/api/orgs/${cfg.org}/custom-node-types`)
  const existingItems = existing.items || (Array.isArray(existing) ? existing : [])
  let storyTid = existingItems.find((t) => t.label === LABEL)?.id
  if (storyTid) {
    console.log(`[story] reusing existing custom-node-type: ${storyTid} (${LABEL})`)
  } else {
    const storyType = await session.json(`/api/orgs/${cfg.org}/custom-node-types`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        label: LABEL,
        color: "#f59e0b",
        kind: "llm",
        params: {
          model: "deepseek-chat",
          outputFormat: "json",
          systemPrompt:
            '你是一位温暖细腻的儿童绘本作家。输出 JSON: {"title":..,"story":..,"moral":..,"coverPrompt":..}',
          userPrompt: "根据主题写一个温暖的儿童故事：{{theme}}",
        },
      }),
    })
    storyTid = storyType.id
    console.log(`[story] custom-node-type registered: ${storyTid} (${LABEL}, kind=llm)`)
  }

  // 2) create the custom-kind project to hold the workflow
  const project = await session.json(`/api/orgs/${cfg.org}/projects`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: `e2e 儿童故事工坊 ${new Date().toISOString().slice(0, 19)}`,
      brief: "给主题生成儿童故事与配图",
      kind: "custom",
    }),
  })
  const pid = project.id
  console.log(`[story] project created: ${pid}`)

  // 3) create the workflow (story LLM → script → storyboard fan-out)
  const workflow = await session.json(`/api/projects/${pid}/workflows`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: "故事绘本",
      inputsSchema: [{ name: "theme", label: "主题", type: "text", target: "brief" }],
      nodes: [
        {
          id: "story",
          type: "custom:儿童故事作家",
          typeId: storyTid,
          typeVersion: 1,
          dependsOn: [],
        },
        {
          id: "script-1",
          type: "script",
          dependsOn: ["story"],
          varBindings: [{ name: "text", sourceNodeId: "story", sourceField: "story" }],
        },
        { id: "board-1", type: "storyboard", dependsOn: ["script-1"] },
      ],
    }),
  })
  const wf = workflow.id
  console.log(`[story] workflow created: ${wf}`)

  // 4) run the workflow with the theme input → 202
  const run = await session.json(`/api/projects/${pid}/workflows/${wf}/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ inputs: { theme: "勇敢的小刺猬第一次交朋友" } }),
  })
  console.log(`[story] run triggered: planId=${run.planId} workflowId=${run.workflowId}`)
  if (!run.planId) throw new Error("run response missing planId")

  if (SMOKE_ONLY) {
    console.log("[story] SMOKE_ONLY — stopping after run-trigger. OK")
    return
  }

  // 5) poll until done
  console.log("[story] polling /state until done …")
  await pollState(session, pid, {
    timeoutMs: 20 * 60 * 1000,
    onTick: (s) => process.stdout.write(`\r  runStatus=${s.runStatus}   `),
  })
  console.log("\n[story] run done")

  // 6) assert the documented asset shape: images from fan-out, NO audio.
  const assets = await session.json(`/api/projects/${pid}/assets`)
  const items = assets.items || assets.assets || (Array.isArray(assets) ? assets : [])
  const images = items.filter((a) => a.type === "image")
  const audio = items.filter((a) => a.type === "audio")
  console.log(`[story] assets: ${images.length} image, ${audio.length} audio`)

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
  console.log(`[story] ✓ ${images.length} image assets (≥${MIN_IMAGES}), 0 audio (documented behavior)`)
  console.log("[story] OK")
}

main().catch((err) => {
  console.error("\n[story] FAIL:", err.message)
  process.exit(1)
})
