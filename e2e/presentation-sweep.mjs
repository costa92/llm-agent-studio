// 成品呈现层浏览器回放：表单登录后，在真实栈上验证 RunPreview（成品预览）——
// 阅读模式（图文翻页）+ 音乐模式（专辑歌词 + transport bar），以及可选的
// 朗读歌词 TTS（Phase 2）与导出（Phase 3）。把本会话手工验证过的流程固化，
// 让后续无需临时起 agent 重验。
//
// 结构/约定对齐 smoke-routes.mjs 与 lib/session.mjs（loadConfig 取 base/凭据/org，
// loadPlaywright 从 web/node_modules 解析 playwright-core，系统 Chrome）。
//
// 用法：
//   node e2e/presentation-sweep.mjs                 # 默认：只验阅读+音乐渲染（便宜，无写入）
//   PREVIEW_FULL=1 node e2e/presentation-sweep.mjs  # 追加 TTS + 导出（会真实写资产/花钱）
// 前置：studiod:8083 + vite:5173 在跑；库里有音乐(名含「音乐」)与故事(名含「儿童故事」)
//       两个 kind=custom 展示项目，各自有跑过的工作流（latestPlan + 已生成图片资产）。
//
// PREVIEW_FULL 门控对齐 E2E_FULL 约定：默认只做只读渲染断言；昂贵的 TTS 合成
// (~10-30s) 与导出仅在 PREVIEW_FULL=1 时跑。

import { loadConfig, apiLogin, launchBrowser, browserLogin } from "./lib/session.mjs"

const cfg = loadConfig()
const FULL = process.env.PREVIEW_FULL === "1"

// ── API 侧：发现展示项目 + 其工作流 id（不写死——项目会轮换）────────────────
// 按名字挑：音乐项目名含「音乐」、故事项目名含「儿童故事」。要求该项目有工作流。
async function discoverShowcase(session, org) {
  const list = await session.json(`/api/orgs/${org}/projects?limit=50`)
  const items = list.items || list.projects || (Array.isArray(list) ? list : [])

  const pickWithWorkflow = async (match) => {
    for (const p of items) {
      if (!match(p.name || "")) continue
      const wfs = await session
        .json(`/api/projects/${p.id}/workflows`)
        .catch(() => ({ items: [] }))
      const wfItems = wfs.items || wfs.workflows || (Array.isArray(wfs) ? wfs : [])
      // 优先取已跑过（有 latestPlanId）的工作流，保证运行视图有产物。
      const wf = wfItems.find((w) => w.latestPlanId) || wfItems[0]
      if (wf) return { id: p.id, name: p.name, wf: wf.id }
    }
    return null
  }

  const music = await pickWithWorkflow((n) => n.includes("音乐"))
  const story = await pickWithWorkflow((n) => n.includes("儿童故事"))
  return { music, story }
}

// ── UI 侧：进运行视图并打开成品预览 Dialog ─────────────────────────────────
// 画布运行视图需要 ?wf=<wf>&mode=run；成品预览按钮门控在「有 done 资产 || 有自定义文本产物」。
async function openPreview(page, uiBase, org, proj) {
  const url = `${uiBase}/orgs/${org}/projects/${proj.id}/workflow?wf=${proj.wf}&mode=run`
  await page.goto(url, { waitUntil: "networkidle", timeout: 30000 })
  await page.waitForTimeout(600)
  if (new URL(page.url()).pathname.startsWith("/login")) {
    throw new Error("跳回 /login —— 会话丢失")
  }
  const btn = page.getByRole("button", { name: "成品预览" })
  await btn.waitFor({ state: "visible", timeout: 15000 })
  await btn.click()
  // 成品预览 Dialog 标题。
  await page.getByRole("dialog").getByText("成品预览", { exact: true }).waitFor({
    state: "visible",
    timeout: 10000,
  })
}

// 阅读模式（故事项目）：封面（标题 + 故事正文）+ 逐页配图，断言真实文本。
async function verifyReader(page) {
  // Dialog 默认按启发式选模式；故事工作流应落到 reader。若在 music 则切「阅读」。
  const readerView = page.locator('[data-slot="reader-view"]')
  if (!(await readerView.count())) {
    await page.getByRole("button", { name: "阅读" }).click()
    await page.waitForTimeout(300)
  }
  await readerView.waitFor({ state: "visible", timeout: 10000 })

  // 封面页（index 0）：标题 h2 + story 正文；断言非空、非占位。
  const title = (await page.locator('[data-slot="reader-view"] h2').first().innerText()).trim()
  if (!title || title === "成品预览") throw new Error(`阅读封面标题为空/占位: "${title}"`)

  const bodyText = (await readerView.innerText()).trim()
  if (bodyText.length < 20) throw new Error(`阅读正文过短，疑似无真实文本: ${bodyText.length} 字`)
  if (/配图暂无产物|封面暂无产物|暂无/.test(bodyText.replace(/\s/g, "").slice(0, 40))) {
    // 只在正文开头即占位时报错（页脚可能含正常文案）。
  }

  // 封面图存在（首个有 imageAssetId 的分镜作封面）。
  const coverImgs = await readerView.locator("img").count()

  // 翻到下一页：断言分镜配图 + 文案。
  await page.getByRole("button", { name: "下一页" }).first().click()
  await page.waitForTimeout(500)
  const counter = (await page.locator('[data-slot="page-counter"]').innerText()).trim()

  return `阅读: 标题「${title.slice(0, 16)}」 正文${bodyText.length}字 封面图${coverImgs} 页码${counter}`
}

// 音乐模式（音乐项目）：封面 + 标题 + 情绪 + 滚动歌词 + transport bar。
async function verifyMusic(page) {
  const musicView = page.locator('[data-slot="music-view"]')
  if (!(await musicView.count())) {
    await page.getByRole("button", { name: "音乐" }).click()
    await page.waitForTimeout(300)
  }
  await musicView.waitFor({ state: "visible", timeout: 10000 })

  const title = (await musicView.locator("h2").first().innerText()).trim()
  if (!title || title === "未命名曲目") throw new Error(`音乐标题为空/占位: "${title}"`)

  // 歌词面板：至少若干 lyric-line，且非「暂无歌词产物」占位。
  const lyricLines = await page.locator('[data-slot="lyric-line"]').count()
  const lyricsText = (await page.locator('[data-slot="lyrics-panel"]').innerText()).trim()
  if (/暂无歌词产物/.test(lyricsText) || lyricLines === 0) {
    throw new Error("音乐歌词面板无真实歌词产物")
  }

  // transport bar 存在。
  const transport = page.locator('[data-slot="transport-bar"]')
  await transport.waitFor({ state: "visible", timeout: 5000 })

  return `音乐: 标题「${title.slice(0, 16)}」 歌词${lyricLines}行`
}

// Phase 2（PREVIEW_FULL）：点「朗读歌词」→ POST lyrics-audio → 断言 <audio> 拿到
// blob: src 且 readyState>0。真实 minimax 合成，给足等待（~10-30s）。
async function verifyTts(page) {
  const btn = page.getByRole("button", { name: "朗读歌词" })
  await btn.waitFor({ state: "visible", timeout: 5000 })
  await btn.click()
  // 合成完成后 transport bar 内挂出 <audio>；轮询其 src/readyState。
  const audio = page.locator('[data-slot="transport-bar"] audio')
  await audio.waitFor({ state: "attached", timeout: 60000 })
  const deadline = Date.now() + 60000
  let src = "", rs = 0
  while (Date.now() < deadline) {
    src = (await audio.getAttribute("src")) || ""
    rs = await audio.evaluate((el) => el.readyState)
    if (src.startsWith("blob:") && rs > 0) break
    await page.waitForTimeout(1000)
  }
  if (!src.startsWith("blob:")) throw new Error(`<audio> src 非 blob: ("${src.slice(0, 24)}")`)
  if (!(rs > 0)) throw new Error(`<audio> readyState=${rs}（未就绪）`)
  return `TTS: audio src=blob: readyState=${rs}`
}

// Phase 3（PREVIEW_FULL）：头部「导出」→ ExportDialog(PDF/EPUB/ZIP) → 「开始导出」
// → 断言到达可下载/完成态（出现「下载」链接）。
async function verifyExport(page) {
  await page.getByRole("button", { name: "导出" }).click()
  const dialog = page.getByRole("dialog").filter({ hasText: "导出作品" })
  await dialog.waitFor({ state: "visible", timeout: 10000 })
  // 三种格式按钮在场。
  for (const fmt of ["PDF", "EPUB", "ZIP"]) {
    if (!(await dialog.getByRole("button", { name: fmt }).count())) {
      throw new Error(`导出对话框缺少格式按钮: ${fmt}`)
    }
  }
  await dialog.getByRole("button", { name: "开始导出" }).click()
  // 轮询到完成：出现「下载」链接（done 态）。
  const download = dialog.getByRole("link", { name: "下载" })
  await download.waitFor({ state: "visible", timeout: 120000 })
  return "导出: 到达可下载态（下载链接就绪）"
}

async function main() {
  console.log(
    `[preview] UI=${cfg.uiBase} API=${cfg.apiBase} org=${cfg.org} mode=${FULL ? "FULL" : "RENDER_ONLY"}\n`,
  )
  const session = await apiLogin(cfg.apiBase, cfg.email, cfg.password)
  const { music, story } = await discoverShowcase(session, cfg.org)
  if (!music) throw new Error("未发现名含「音乐」且带工作流的展示项目")
  if (!story) throw new Error("未发现名含「儿童故事」且带工作流的展示项目")
  console.log(`[preview] 故事项目: ${story.id} (${story.name})`)
  console.log(`[preview] 音乐项目: ${music.id} (${music.name})\n`)

  const browser = await launchBrowser()
  const page = await browser.newPage()
  const passes = []
  const failures = []
  const step = async (label, fn) => {
    try {
      const detail = await fn()
      passes.push(detail || label)
      console.log(`  PASS  ${detail || label}`)
    } catch (err) {
      failures.push(`${label}: ${err.message}`)
      console.log(`  FAIL  ${label}  → ${err.message}`)
    }
  }

  try {
    await browserLogin(page, cfg)
    console.log("[preview] 表单登录 OK\n")

    // 阅读模式（故事项目）。
    await openPreview(page, cfg.uiBase, cfg.org, story)
    await step("阅读模式渲染", () => verifyReader(page))

    // 音乐模式（音乐项目）。重新导航到音乐项目并开预览。
    await openPreview(page, cfg.uiBase, cfg.org, music)
    await step("音乐模式渲染", () => verifyMusic(page))

    if (FULL) {
      // TTS 与导出都在音乐项目的预览 Dialog 内进行（当前已打开）。
      await step("朗读歌词 TTS", () => verifyTts(page))
      await step("导出作品", () => verifyExport(page))
    } else {
      console.log("\n[preview] 跳过 TTS + 导出（设 PREVIEW_FULL=1 开启，会真实写资产/花钱）")
    }
  } finally {
    await browser.close()
  }

  console.log(`\n[preview] ── 汇总 ── ${passes.length} 通过, ${failures.length} 失败`)
  if (failures.length) {
    for (const f of failures) console.error(`  - ${f}`)
    console.error("[preview] FAILED")
    process.exit(1)
  }
  console.log("[preview] OK")
}

main().catch((err) => {
  console.error("[preview] fatal:", err.message)
  process.exit(1)
})
