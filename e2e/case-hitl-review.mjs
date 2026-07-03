// HITL 审核功能流用例（E2E_FULL=1，**真实付费生成**）。
//
// 此前 HITL 只有渲染冒烟（smoke-routes 只看审核路由能打开），accept / reject /
// regenerate 三个门禁转换零功能断言——本用例补上这条关键路径：
//
//   1. 复用共享 runner 跑最小工作流（childrens-story：LLM → script → storyboard
//      扇出，只出图）到生成完成；
//   2. 断言审核队列非空：GET /api/orgs/{org}/assets?status=pending_acceptance&project={pid}；
//   3. accept 一个 → 200 {status:"accepted"}，GET /api/assets/{id} 复核 accepted，
//      重复 accept → 409（非 pending 冲突守卫）；
//   4. reject 一个 → 200 {status:"rejected"}，GET 复核 rejected，
//      对已 rejected 的资产 regenerate → 409（只有 pending 可重生成）；
//   5. regenerate 一个 → 200 {newAssetId, todoId, status:"generating"}；断言
//      父资产转 rejected、子资产 version=父+1 且 parentAssetId=父 id、父的版本
//      血缘（GET /api/assets/{父}.versions）含子。**不等新生成完成**——真实生成
//      耗时且付费，触发成功 + 血缘落库即为断言终点。
//
// 分镜张数由 LLM 决定（不确定），断言按待审数量 N 分层收敛到确定性部分：
//   N≥3：accept / reject(+409) / regenerate 全做；
//   N=2：accept + regenerate（regenerate 内部含 reject 语义：父转 rejected）；
//   N=1：只做 regenerate（覆盖 reject 语义 + 血缘），并对已出局的父资产断言
//        accept → 409（仍然行使 accept 端点的冲突路径）。
//
// 门控与其他付费用例一致：E2E_FULL=1 才跑；否则打印跳过提示 exit 0。
// （E2E_SMOKE_ONLY 对本用例无意义：没有生成完成就没有待审资产。）
//
//   E2E_FULL=1 node e2e/case-hitl-review.mjs        # 或 (cd web && pnpm e2e:hitl)

import { SCENARIOS } from "./lib/scenarios.mjs"
import { runShowcaseCase } from "./lib/showcaseCase.mjs"

const TAG = "hitl"

// 断言辅助：失败即抛（终止用例，exit 1）。
function assert(cond, msg) {
  if (!cond) throw new Error(`assert failed: ${msg}`)
}

// expectStatus: 发请求并断言 HTTP 状态码（session.fetch 走自动续期）。
async function expectStatus(session, method, path, want, body) {
  const res = await session.fetch(path, {
    method,
    ...(body !== undefined
      ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }
      : {}),
  })
  const text = (await res.text()).slice(0, 200)
  assert(
    res.status === want,
    `${method} ${path} → ${res.status}（期望 ${want}）: ${text}`,
  )
  console.log(`[${TAG}]   ✓ ${method} ${path} → ${want}`)
  return text
}

async function main() {
  if (process.env.E2E_FULL !== "1") {
    console.log(
      `[${TAG}] SKIP — HITL 审核流需要真实生成产出待审资产，设 E2E_FULL=1 才跑（付费）。`,
    )
    return
  }
  // 防呆：SMOKE_ONLY 下 runner 在 run-trigger 后就停，没有待审资产可审。
  if (process.env.E2E_SMOKE_ONLY === "1") {
    console.log(`[${TAG}] SKIP — E2E_SMOKE_ONLY 与本用例互斥（无生成完成即无待审资产）。`)
    return
  }

  // 1) 复用共享 runner 跑最小工作流到生成完成（childrens-story，只出图）。
  const ctx = await runShowcaseCase(SCENARIOS["childrens-story"])
  const { session, cfg, projectId } = ctx
  console.log(`[${TAG}] 生成完成，进入审核断言（project=${projectId}）`)

  // 2) 审核队列非空（与前端 useReviewQueue 同一查询：status + project 过滤）。
  const queue = await session.json(
    `/api/orgs/${cfg.org}/assets?status=pending_acceptance&project=${projectId}`,
  )
  const pending = queue.items || []
  console.log(`[${TAG}] 审核队列：${pending.length} 个待审资产`)
  assert(pending.length >= 1, "生成完成后审核队列应至少有 1 个 pending_acceptance 资产")

  // 3-5) 按待审数量分层。
  const n = pending.length
  let acceptTarget = null
  let rejectTarget = null
  let regenTarget = null
  if (n >= 3) {
    ;[acceptTarget, rejectTarget, regenTarget] = pending
  } else if (n === 2) {
    ;[acceptTarget, regenTarget] = pending
    console.log(`[${TAG}] 注意：仅 2 个待审 → 显式 reject 折叠进 regenerate（父转 rejected）`)
  } else {
    ;[regenTarget] = pending
    console.log(`[${TAG}] 注意：仅 1 个待审 → 只做 regenerate；accept 走 409 冲突路径`)
  }

  // accept：pending → accepted；重复 accept → 409。
  if (acceptTarget) {
    const resp = await session.json(`/api/assets/${acceptTarget.id}/accept`, { method: "POST" })
    assert(resp.status === "accepted", `accept 响应 status=${resp.status}，期望 accepted`)
    const detail = await session.json(`/api/assets/${acceptTarget.id}`)
    assert(
      detail.asset.status === "accepted",
      `accept 后 GET 资产 status=${detail.asset.status}，期望 accepted`,
    )
    console.log(`[${TAG}] ✓ accept：${acceptTarget.id} → accepted（GET 复核一致）`)
    await expectStatus(session, "POST", `/api/assets/${acceptTarget.id}/accept`, 409)
  }

  // reject：pending → rejected；对 rejected 资产 regenerate → 409。
  if (rejectTarget) {
    const resp = await session.json(`/api/assets/${rejectTarget.id}/reject`, { method: "POST" })
    assert(resp.status === "rejected", `reject 响应 status=${resp.status}，期望 rejected`)
    const detail = await session.json(`/api/assets/${rejectTarget.id}`)
    assert(
      detail.asset.status === "rejected",
      `reject 后 GET 资产 status=${detail.asset.status}，期望 rejected`,
    )
    console.log(`[${TAG}] ✓ reject：${rejectTarget.id} → rejected（GET 复核一致）`)
    await expectStatus(session, "POST", `/api/assets/${rejectTarget.id}/regenerate`, 409, {
      prompt: "should conflict",
    })
  }

  // regenerate：父转 rejected + 子 v+1（血缘）+ todo 已生成。不等新生成完成。
  {
    const parentBefore = regenTarget
    const resp = await session.json(`/api/assets/${parentBefore.id}/regenerate`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prompt: `${parentBefore.prompt || "重画"}（e2e regenerate 修订）` }),
    })
    assert(resp.newAssetId, "regenerate 响应缺 newAssetId")
    assert(resp.todoId, "regenerate 响应缺 todoId")
    assert(resp.status === "generating", `regenerate 响应 status=${resp.status}，期望 generating`)
    console.log(
      `[${TAG}] ✓ regenerate 触发：parent=${parentBefore.id} → child=${resp.newAssetId} todo=${resp.todoId}`,
    )

    // 父资产：pending → rejected（regenerate 隐含的 reject 语义）。
    const parent = await session.json(`/api/assets/${parentBefore.id}`)
    assert(
      parent.asset.status === "rejected",
      `regenerate 后父资产 status=${parent.asset.status}，期望 rejected`,
    )
    // 子资产：v+1、parentAssetId 指回父。status 不做强断言的“等于 generating”——
    // worker 可能已领取并完成（竞态），只断言它处于血缘上合法的状态集合。
    const child = await session.json(`/api/assets/${resp.newAssetId}`)
    assert(
      child.asset.version === parent.asset.version + 1,
      `子资产 version=${child.asset.version}，期望 ${parent.asset.version + 1}`,
    )
    assert(
      child.asset.parentAssetID === parentBefore.id || child.asset.parentAssetId === parentBefore.id,
      `子资产 parentAssetId=${child.asset.parentAssetId}，期望 ${parentBefore.id}`,
    )
    const okStatuses = ["generating", "pending_acceptance"]
    assert(
      okStatuses.includes(child.asset.status),
      `子资产 status=${child.asset.status}，期望 ${okStatuses.join("/")} 之一`,
    )
    // 父的版本血缘应包含子（GET /api/assets/{id} → {asset, versions}）。
    const lineage = (parent.versions || []).map((v) => v.id)
    assert(lineage.includes(resp.newAssetId), "父资产 versions 血缘应包含新子资产")
    console.log(
      `[${TAG}] ✓ regenerate 血缘：父 rejected、子 v${child.asset.version}（status=${child.asset.status}）、versions 含子`,
    )

    // N=1 时补 accept 端点的冲突路径：对已 rejected 的父 accept → 409。
    if (!acceptTarget) {
      await expectStatus(session, "POST", `/api/assets/${parentBefore.id}/accept`, 409)
    }
  }

  console.log(`[${TAG}] OK — HITL accept/reject/regenerate 功能流全部断言通过`)
}

main().catch((err) => {
  console.error(`\n[${TAG}] FAIL:`, err.message)
  process.exit(1)
})
