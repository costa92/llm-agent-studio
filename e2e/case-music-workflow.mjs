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
// REAL generation → OPT-IN:
//   E2E_FULL=1        node e2e/case-music-workflow.mjs   # full flow
//   E2E_SMOKE_ONLY=1  node e2e/case-music-workflow.mjs   # register + create + run-trigger (202)
//   (neither set)     node e2e/case-music-workflow.mjs   # skip notice, exit 0
//
// MIN_IMAGES (default 1) sets the MINIMUM asserted storyboard fan-out image count.
// The exact count is LLM-determined (one image per generated shot), so this is a
// lower bound, not an equality check. The load-bearing assertion is "0 audio".
//
// The flow lives in ./lib/showcaseCase.mjs; the scenario data in ./lib/scenarios.mjs.

import { SCENARIOS } from "./lib/scenarios.mjs"
import { runShowcaseCaseMain } from "./lib/showcaseCase.mjs"

runShowcaseCaseMain(SCENARIOS.music)
