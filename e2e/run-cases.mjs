// 有界并发的用例驱动器：把 lib/scenarios.mjs 里登记的场景，经共享 runner
// runShowcaseCase 批量跑，最多 N 个并发在飞。只做「调度 + 汇总」，不改任何
// 场景语义——E2E_FULL / E2E_SMOKE_ONLY 的门控原样透传给 runShowcaseCase。
//
// 用法：
//   CASES=science,ad CONCURRENCY=2 E2E_SMOKE_ONLY=1 node e2e/run-cases.mjs
//   node e2e/run-cases.mjs --concurrency=3            # 跑全部场景
//   E2E_FULL=1 CASES=music,poem node e2e/run-cases.mjs   # 真实付费生成
//   （E2E_FULL / E2E_SMOKE_ONLY 都不设时，每个场景各自打印跳过提示并算 OK）
//
// 选项：
//   CASES=slug,slug   要跑的场景（默认全部 SCENARIO_SLUGS）
//   CONCURRENCY / --concurrency=N   最多并发数（默认 2）

import { SCENARIOS, SCENARIO_SLUGS } from "./lib/scenarios.mjs"
import { runShowcaseCase } from "./lib/showcaseCase.mjs"

// --concurrency=N（命令行）优先于 CONCURRENCY（环境），再回落默认 2。
function parseConcurrency() {
  const flag = process.argv.find((a) => a.startsWith("--concurrency="))
  const raw = flag ? flag.slice("--concurrency=".length) : process.env.CONCURRENCY
  const n = Number(raw)
  return Number.isFinite(n) && n >= 1 ? Math.floor(n) : 2
}

function parseCases() {
  const raw = (process.env.CASES || "").trim()
  if (!raw) return SCENARIO_SLUGS
  const slugs = raw.split(",").map((s) => s.trim()).filter(Boolean)
  const unknown = slugs.filter((s) => !SCENARIOS[s])
  if (unknown.length) {
    console.error(
      `[run-cases] 未知场景：${unknown.join(", ")}。可用：${SCENARIO_SLUGS.join(", ")}`,
    )
    process.exit(2)
  }
  return slugs
}

// 有界并发池：共享一个游标，最多 concurrency 个 worker 同时从 slugs 取任务。
async function runPool(slugs, concurrency) {
  const results = new Array(slugs.length)
  let cursor = 0

  const worker = async () => {
    while (true) {
      const i = cursor++
      if (i >= slugs.length) return
      const slug = slugs[i]
      const start = Date.now()
      try {
        await runShowcaseCase(SCENARIOS[slug])
        results[i] = { slug, ok: true, ms: Date.now() - start }
      } catch (err) {
        results[i] = { slug, ok: false, ms: Date.now() - start, error: err.message }
        console.error(`[run-cases] [${slug}] FAIL: ${err.message}`)
      }
    }
  }

  const workers = Array.from({ length: Math.min(concurrency, slugs.length) }, worker)
  await Promise.all(workers)
  return results
}

async function main() {
  const slugs = parseCases()
  const concurrency = parseConcurrency()
  const mode = process.env.E2E_FULL === "1"
    ? "FULL"
    : process.env.E2E_SMOKE_ONLY === "1"
      ? "SMOKE_ONLY"
      : "SKIP"
  console.log(
    `[run-cases] 场景=[${slugs.join(", ")}] 并发=${concurrency} 门控=${mode}\n`,
  )

  const results = await runPool(slugs, concurrency)

  console.log("\n[run-cases] ── 汇总 ──")
  let failed = 0
  for (const r of results) {
    const secs = (r.ms / 1000).toFixed(1)
    if (r.ok) {
      console.log(`  OK    ${r.slug}  (${secs}s)`)
    } else {
      failed++
      console.log(`  FAIL  ${r.slug}  (${secs}s)  → ${r.error}`)
    }
  }
  console.log(`[run-cases] ${results.length - failed}/${results.length} 通过`)
  if (failed) process.exit(1)
}

main().catch((err) => {
  console.error("[run-cases] fatal:", err.message)
  process.exit(1)
})
