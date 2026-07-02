// Generic showcase-case runner. Picks a scenario from the registry by slug and
// runs it through the shared flow (LLM → script → storyboard fan-out).
//
//   CASE=<slug> E2E_FULL=1       node e2e/case-showcase.mjs   # full (paid) generation
//   CASE=<slug> E2E_SMOKE_ONLY=1 node e2e/case-showcase.mjs   # register + create + run-trigger (202)
//   CASE=<slug>                  node e2e/case-showcase.mjs   # skip notice, exit 0
//
// Available slugs: see ./lib/scenarios.mjs (music, childrens-story, science,
// ad, poem, travel). The music and childrens-story scenarios also have their
// own named entry scripts for backward compatibility.

import { SCENARIOS, SCENARIO_SLUGS } from "./lib/scenarios.mjs"
import { runShowcaseCaseMain } from "./lib/showcaseCase.mjs"

const slug = process.env.CASE
if (!slug) {
  console.error(`[showcase] set CASE=<slug>. Available: ${SCENARIO_SLUGS.join(", ")}`)
  process.exit(2)
}
const def = SCENARIOS[slug]
if (!def) {
  console.error(`[showcase] unknown CASE="${slug}". Available: ${SCENARIO_SLUGS.join(", ")}`)
  process.exit(2)
}

runShowcaseCaseMain(def)
