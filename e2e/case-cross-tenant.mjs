// 跨租户隔离用例（纯 API，0 生成费用，默认可跑）。
//
// 多租户隔离此前在 e2e 零覆盖（只有 DB-gated Go 测试，CI 无 PG 时假绿）。
// 本用例在真实栈上验证：org B 的成员对 org A 的项目/工作流/资产/成本端点
// 一律拿不到数据（403/404），且 org B 自己的资源正常。
//
// 流程：
//   1. demo 账号（org A 管理员）登录；在 org A 建一个项目 + 工作流当“靶子”，
//      再从 org A 资产库取一个已有资产 id（历史生成留下的；没有则跳过资产项）；
//   2. 自助注册新用户（POST /api/auth/register + 验证码——dev 模式 mock 邮件
//      落盘到仓库根 mails/，从最新匹配文件里解析 6 位验证码）→ verify 登录，
//      再用密码 apiLogin 复核可登录；
//   3. 新用户 POST /api/orgs 建 org B（创建者即 org_admin）；
//   4. 以 B 身份逐一打 org A 的读/写/运行/审核/成本端点 → 断言全部 403/404
//      （middleware 语义：非成员 → RoleNone → 403；缺失资源同样折叠成 403 安全
//      默认。两者都算隔离成立，逐条记录真实状态码）；
//   5. 断言 PUT 篡改未生效（org A 视角项目名未变）；
//   6. 正向对照：B 在 org B 建项目/读项目/列表/成本全部 200。
//
//   node e2e/case-cross-tenant.mjs     # 或 (cd web && pnpm e2e:cross-tenant)

import fs from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { loadConfig, apiLogin, sleep } from "./lib/session.mjs"

const TAG = "cross-tenant"
const here = path.dirname(fileURLToPath(import.meta.url))
const MAILS_DIR = path.resolve(here, "..", "mails") // studiod 以仓库根为 cwd 落盘 mock 邮件

function assert(cond, msg) {
  if (!cond) throw new Error(`assert failed: ${msg}`)
}

// findVerificationCode: 轮询 mails/ 目录，找发给 email 的最新 mock 邮件并解析
// 6 位验证码（文件名形如 <id>_<email>.txt，正文含 "code is: 123456"）。
async function findVerificationCode(email, { timeoutMs = 15000, intervalMs = 500 } = {}) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    let names = []
    try {
      names = await fs.readdir(MAILS_DIR)
    } catch {
      // 目录尚未创建（第一封邮件才会建）
    }
    const mine = names.filter((n) => n.endsWith(`_${email}.txt`)).sort()
    if (mine.length) {
      const latest = mine[mine.length - 1]
      const body = await fs.readFile(path.join(MAILS_DIR, latest), "utf8")
      const m = body.match(/code is:\s*(\d{6})/)
      if (m) return m[1]
    }
    await sleep(intervalMs)
  }
  throw new Error(`在 ${MAILS_DIR} 里 ${timeoutMs}ms 内没等到发给 ${email} 的验证码邮件`)
}

// deny: 以 sessionB 打 org A 的端点，断言状态码 ∈ {403,404}（跨租户必须拒绝）。
// 302/2xx 视为隔离穿透 → 立即失败（发现产品 bug 的信号）。
async function deny(session, method, pathname, body) {
  const res = await session.fetch(pathname, {
    method,
    redirect: "manual", // 资产 content 端点对放行方是 302 签名 URL——手动模式防跟随
    ...(body !== undefined
      ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }
      : {}),
  })
  const ok = res.status === 403 || res.status === 404
  assert(ok, `隔离穿透！${method} ${pathname} → ${res.status}（期望 403/404）`)
  console.log(`[${TAG}]   ✓ ${method} ${pathname} → ${res.status}（拒绝）`)
}

async function main() {
  const cfg = loadConfig()
  console.log(`[${TAG}] API=${cfg.apiBase} orgA=${cfg.org}`)

  // ── 1. org A：登录 + 准备靶子资源 ────────────────────────────────────────
  const sessionA = await apiLogin(cfg.apiBase, cfg.email, cfg.password)
  const ts = new Date().toISOString().slice(0, 19)
  const projA = await sessionA.json(`/api/orgs/${cfg.org}/projects`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: `e2e 跨租户靶子 ${ts}`, brief: "隔离用例专用", kind: "custom" }),
  })
  console.log(`[${TAG}] org A 靶子项目：${projA.id}`)
  const wfA = await sessionA.json(`/api/projects/${projA.id}/workflows`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: "靶子工作流",
      nodes: [{ id: "script-1", type: "script", dependsOn: [] }],
    }),
  })
  console.log(`[${TAG}] org A 靶子工作流：${wfA.id}`)
  // 资产靶子：取 org A 资产库里任一已有资产（历史生成留下）；没有则跳过资产项。
  const libA = await sessionA.json(`/api/orgs/${cfg.org}/assets?limit=1`)
  const assetA = (libA.items || [])[0]?.id || null
  console.log(`[${TAG}] org A 靶子资产：${assetA ?? "（库为空，跳过资产端点断言）"}`)

  // ── 2. 自助注册用户 B（register + mock 邮件验证码 + verify）──────────────
  const emailB = `e2e-tenant-${Date.now()}@example.com`
  const passwordB = "CrossTenant#2026"
  {
    const res = await fetch(`${cfg.apiBase}/api/auth/register`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: emailB, password: passwordB }),
    })
    const text = await res.text()
    assert(res.ok, `register → ${res.status}: ${text.slice(0, 200)}`)
    const body = JSON.parse(text)
    assert(body.verified === false, "register 应返回 verified:false（邮箱验证模式）")
  }
  const code = await findVerificationCode(emailB)
  console.log(`[${TAG}] 用户 B 注册：${emailB}（验证码 ${code}）`)
  {
    const res = await fetch(`${cfg.apiBase}/api/auth/verify`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: emailB, code }),
    })
    const text = await res.text()
    assert(res.ok, `verify → ${res.status}: ${text.slice(0, 200)}`)
    const body = JSON.parse(text)
    assert(body.access_token, "verify 应发放 access_token")
  }
  // 用密码正常登录（复核 verify 后密码可用 + 获得带自动续期的 session）。
  const sessionB = await apiLogin(cfg.apiBase, emailB, passwordB)
  console.log(`[${TAG}] 用户 B verify + 密码登录 OK`)

  // ── 3. 用户 B 建 org B ───────────────────────────────────────────────────
  const orgB = await sessionB.json(`/api/orgs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: `e2e-tenant-b-${Date.now()}` }),
  })
  assert(orgB.id, "创建 org B 应返回 id")
  console.log(`[${TAG}] org B 创建：${orgB.id}`)

  // ── 4. 以 B 身份逐一打 org A 端点 → 全部 403/404 ─────────────────────────
  console.log(`[${TAG}] 跨租户拒绝矩阵（B → org A）：`)
  // 项目级（projectScope: 非成员 → RoleNone → 403）
  await deny(sessionB, "GET", `/api/projects/${projA.id}`)
  await deny(sessionB, "PUT", `/api/projects/${projA.id}`, { name: "hacked-by-org-b" })
  await deny(sessionB, "GET", `/api/projects/${projA.id}/state`)
  await deny(sessionB, "GET", `/api/projects/${projA.id}/plans`)
  await deny(sessionB, "GET", `/api/projects/${projA.id}/assets`)
  await deny(sessionB, "GET", `/api/projects/${projA.id}/workflows`)
  await deny(sessionB, "POST", `/api/projects/${projA.id}/workflows/${wfA.id}/run`, { inputs: {} })
  await deny(sessionB, "GET", `/api/projects/${projA.id}/cost`)
  // org 级（orgScope: 非成员 → 403）
  await deny(sessionB, "GET", `/api/orgs/${cfg.org}/projects`)
  await deny(sessionB, "POST", `/api/orgs/${cfg.org}/projects`, { name: "hacked", kind: "custom" })
  await deny(sessionB, "GET", `/api/orgs/${cfg.org}/assets`)
  await deny(sessionB, "GET", `/api/orgs/${cfg.org}/cost`)
  await deny(sessionB, "GET", `/api/orgs/${cfg.org}/generations`)
  await deny(sessionB, "GET", `/api/orgs/${cfg.org}/members`)
  await deny(sessionB, "GET", `/api/orgs/${cfg.org}/custom-node-types`)
  // 资产级（assetScope: 资产→项目→org 解析后同样拒绝；含 HITL 审核动作）
  if (assetA) {
    await deny(sessionB, "GET", `/api/assets/${assetA}`)
    await deny(sessionB, "GET", `/api/assets/${assetA}/content`)
    await deny(sessionB, "POST", `/api/assets/${assetA}/accept`)
    await deny(sessionB, "POST", `/api/assets/${assetA}/reject`)
    await deny(sessionB, "POST", `/api/assets/${assetA}/regenerate`, { prompt: "hacked" })
  }

  // ── 5. 篡改未生效：org A 视角项目名原样 ──────────────────────────────────
  const projAAfter = await sessionA.json(`/api/projects/${projA.id}`)
  const nameAfter = projAAfter.name ?? projAAfter.project?.name
  assert(
    nameAfter === `e2e 跨租户靶子 ${ts}`,
    `org A 项目名被跨租户篡改：${nameAfter}`,
  )
  console.log(`[${TAG}] ✓ PUT 篡改未生效（org A 项目名原样）`)

  // ── 6. 正向对照：B 在自己的 org B 一切正常 ───────────────────────────────
  const projB = await sessionB.json(`/api/orgs/${orgB.id}/projects`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "org B 自己的项目", brief: "正向对照", kind: "custom" }),
  })
  assert(projB.id, "org B 建项目应成功")
  const gotB = await sessionB.json(`/api/projects/${projB.id}`)
  assert(
    (gotB.name ?? gotB.project?.name) === "org B 自己的项目",
    "org B 读自己的项目应成功",
  )
  const listB = await sessionB.json(`/api/orgs/${orgB.id}/projects`)
  const listItems = listB.items || listB.projects || []
  assert(
    listItems.some((p) => p.id === projB.id),
    "org B 项目列表应包含刚建的项目",
  )
  const costB = await sessionB.json(`/api/orgs/${orgB.id}/cost`)
  assert(costB !== null && typeof costB === "object", "org B 成本端点应 200")
  console.log(`[${TAG}] ✓ 正向对照：org B 建/读/列表/成本全部 200`)

  console.log(`[${TAG}] OK — 跨租户隔离全部断言通过`)
}

main().catch((err) => {
  console.error(`\n[${TAG}] FAIL:`, err.message)
  process.exit(1)
})
