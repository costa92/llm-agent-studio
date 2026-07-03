// Shared showcase-case runner for the custom-node canvas path (n8n-style
// workflow), NOT any built-in pipeline. Every showcase scenario is the same
// shape — an LLM node feeding a script node feeding a storyboard fan-out:
//
//   register custom-node-type (kind=llm) →
//   create project (kind=custom) →
//   create workflow: <llm>(custom:<label>) → script-1(script, var ← <llm>.<field>)
//                    → board-1(storyboard) + inputsSchema[theme] →
//   run {inputs:{theme}} → poll /state until done →
//   assert IMAGE assets from the storyboard fan-out, and assert NO AUDIO asset.
//
// The no-audio assertion is the load-bearing bit: a non-picturebook storyboard
// fan-out produces IMAGES ONLY. (The built-in picturebook audio path was
// deleted; there is no audio here at all.)
//
// Scenarios differ only in data (label / prompts / node id / binding / theme),
// so they live in ./scenarios.mjs and share this runner.

import { loadConfig, apiLogin, pollState } from "./session.mjs"

// runShowcaseCase drives one scenario end-to-end. It reads the same env gating
// as the original per-case scripts:
//   E2E_FULL=1        full (paid) generation
//   E2E_SMOKE_ONLY=1  register + create + run-trigger (202) only
//   (neither set)     print a skip notice and exit 0
//   MIN_IMAGES        lower bound on storyboard fan-out image count (default 1)
//
// Returns a context object `{ session, cfg, projectId, workflowId, planId }`
// (undefined in skip mode; planId only when the run was triggered) so composed
// cases (e.g. case-hitl-review.mjs) can keep driving the SAME project after the
// generation completes.
export async function runShowcaseCase(def) {
  const cfg = loadConfig()
  const FULL = process.env.E2E_FULL === "1"
  const SMOKE_ONLY = process.env.E2E_SMOKE_ONLY === "1"
  const MIN_IMAGES = Number(process.env.MIN_IMAGES || "1")
  const tag = def.tag

  if (!FULL && !SMOKE_ONLY) {
    console.log(
      `[${tag}] SKIP — set E2E_FULL=1 to run the full (paid) generation,\n` +
        `        or E2E_SMOKE_ONLY=1 to only exercise register + create + run-trigger (202).`,
    )
    return
  }

  console.log(`[${tag}] API=${cfg.apiBase} org=${cfg.org} mode=${FULL ? "FULL" : "SMOKE_ONLY"}`)
  const session = await apiLogin(cfg.apiBase, cfg.email, cfg.password)

  // 1) register the LLM custom node type. Idempotent: the label is org-unique,
  //    so on a re-run we reuse the existing type instead of hitting 23505.
  const existing = await session.json(`/api/orgs/${cfg.org}/custom-node-types`)
  const existingItems = existing.items || (Array.isArray(existing) ? existing : [])
  let typeId = existingItems.find((t) => t.label === def.label)?.id
  if (typeId) {
    console.log(`[${tag}] reusing existing custom-node-type: ${typeId} (${def.label})`)
  } else {
    const created = await session.json(`/api/orgs/${cfg.org}/custom-node-types`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        label: def.label,
        color: def.color,
        kind: "llm",
        params: {
          model: "deepseek-chat",
          outputFormat: "json",
          systemPrompt: def.systemPrompt,
          userPrompt: def.userPrompt,
        },
      }),
    })
    typeId = created.id
    console.log(`[${tag}] custom-node-type registered: ${typeId} (${def.label}, kind=llm)`)
  }

  // 2) create the custom-kind project to hold the workflow
  const project = await session.json(`/api/orgs/${cfg.org}/projects`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: `${def.projectPrefix} ${new Date().toISOString().slice(0, 19)}`,
      brief: def.brief,
      kind: "custom",
    }),
  })
  const pid = project.id
  console.log(`[${tag}] project created: ${pid}`)

  // 3) create the workflow (llm → script → storyboard fan-out)
  const workflow = await session.json(`/api/projects/${pid}/workflows`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: def.workflowName,
      inputsSchema: [{ name: "theme", label: "主题", type: "text", target: "brief" }],
      nodes: [
        {
          id: def.llmNodeId,
          type: `custom:${def.label}`,
          typeId,
          typeVersion: 1,
          dependsOn: [],
        },
        {
          id: "script-1",
          type: "script",
          dependsOn: [def.llmNodeId],
          varBindings: [
            { name: def.scriptVar, sourceNodeId: def.llmNodeId, sourceField: def.scriptSourceField },
          ],
        },
        { id: "board-1", type: "storyboard", dependsOn: ["script-1"] },
      ],
    }),
  })
  const wf = workflow.id
  console.log(`[${tag}] workflow created: ${wf}`)

  // 4) run the workflow with the theme input → 202
  const run = await session.json(`/api/projects/${pid}/workflows/${wf}/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ inputs: { theme: def.theme } }),
  })
  console.log(`[${tag}] run triggered: planId=${run.planId} workflowId=${run.workflowId}`)
  if (!run.planId) throw new Error("run response missing planId")

  if (SMOKE_ONLY) {
    console.log(`[${tag}] SMOKE_ONLY — stopping after run-trigger. OK`)
    return { session, cfg, projectId: pid, workflowId: wf, planId: run.planId }
  }

  // 5) poll until done
  console.log(`[${tag}] polling /state until done …`)
  await pollState(session, pid, {
    timeoutMs: 20 * 60 * 1000,
    onTick: (s) => process.stdout.write(`\r  runStatus=${s.runStatus}   `),
  })
  console.log(`\n[${tag}] run done`)

  // 6) assert the documented asset shape: images from fan-out, NO audio.
  const assets = await session.json(`/api/projects/${pid}/assets`)
  const items = assets.items || assets.assets || (Array.isArray(assets) ? assets : [])
  const images = items.filter((a) => a.type === "image")
  const audio = items.filter((a) => a.type === "audio")
  console.log(`[${tag}] assets: ${images.length} image, ${audio.length} audio`)

  if (audio.length !== 0) {
    throw new Error(`expected NO audio in a custom storyboard fan-out, found ${audio.length}`)
  }
  // Shot count is LLM-determined, so assert a lower bound rather than an exact
  // count — the storyboard fan-out must produce at least one image.
  if (images.length < MIN_IMAGES) {
    throw new Error(
      `expected at least ${MIN_IMAGES} image asset(s) from the storyboard fan-out (set MIN_IMAGES to override), found ${images.length}`,
    )
  }
  console.log(`[${tag}] ✓ ${images.length} image assets (≥${MIN_IMAGES}), 0 audio (documented behavior)`)
  console.log(`[${tag}] OK`)
  return { session, cfg, projectId: pid, workflowId: wf, planId: run.planId }
}

// runShowcaseCaseMain is the thin entrypoint wrapper: run and translate a
// failure into a non-zero exit, matching the original per-case scripts.
export function runShowcaseCaseMain(def) {
  runShowcaseCase(def).catch((err) => {
    console.error(`\n[${def.tag}] FAIL:`, err.message)
    process.exit(1)
  })
}
