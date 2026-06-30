# 绘本成书导出 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development。设计真源见 `docs/superpowers/specs/2026-06-29-picturebook-export-design.md`——每个任务假设你已读对应章节。

**Goal:** 把已审核绘本资产（图+文+可选音频）异步导出为可下载的 PDF/EPUB/zip（用户选格式），轮询交付。

**Architecture:** 独立 `export_jobs` 表 + 独立 runner（复用 worker 的 claim/lease/reaper 模式，不复用 todos 表）；后端镜像前端 `assemblePages` 装订；三种渲染器；读字节走阶梯（非单纯 SignedURL）。

**Tech Stack:** Go（GORM store 铁律 + pgx.Tx 迁移）；`signintech/gopdf`+`go:embed` 静态 glyf TTF；`bmaupin/go-epub` v1.1.0；stdlib `archive/zip`；React + react-query 轮询。DB-backed 测试 fresh PG（kb_m3_pg）+ `GOWORK=off ... -count=1 -p 1`。

**铁律**：store 写 `INSERT...RETURNING`、纯 `$N`、不 AutoMigrate、JSON/NULL 列 `[]byte` 中转；迁移 Go step 跑 pgx.Tx；DB-backed 测试 fresh DB + `pool.Exec` 种子失败要断言；前端验证用「改动文件零新 eslint error + vitest + build」。

---

## Task 1: 迁移 m22 + exports store + 状态机

**Files:** `internal/storage/storage.go`（goSteps 加 m22）；`internal/exports/store.go`（新）；`internal/exports/store_test.go`（新）

- [ ] **Step 1: 写失败状态机测试**（DB-backed fresh DB）：Create→pending；Claim 互斥（两并发只一个拿到，`FOR UPDATE SKIP LOCKED`）；MarkRunning/MarkDone/MarkFailed 用 `WHERE id=$1 AND status='running'` 守卫（RowsAffected 仲裁）；租约过期可重 claim；Reap 把 running 超 TTL → failed。
- [ ] **Step 2: 跑测试确认失败**（表/方法不存在）。
- [ ] **Step 3: 迁移** —— `goSteps()`（`storage.go:525`）追加 `{version:"m22", run:m22CreateExportJobs}`，`m22CreateExportJobs(ctx, tx pgx.Tx) error` 建 `export_jobs` 表 + 两索引（见 spec DDL）。**跑在 pgx.Tx，非 GORM**。
- [ ] **Step 4: store** —— `ExportJob` struct + 上述方法，纯 `$N`，`INSERT...RETURNING`，时间列/可空用 `[]byte` 或 sql.Null 中转。
- [ ] **Step 5: 跑测试确认通过** `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/<fresh>?sslmode=disable GOWORK=off go test ./internal/exports/... -count=1 -p 1`。
- [ ] **Step 6: commit** `feat(picturebook-export): m22 export_jobs 表 + store 状态机`

## Task 2: 装订 internal/picturebook/assemble.go

**Files:** `internal/picturebook/assemble.go`（新）；`internal/picturebook/assemble_test.go`（新）

- [ ] **Step 1: 先读** `web/src/features/workflow/pictureBookPages.ts:16-61` 与其 `.test.ts`，**逐字理解** assemblePages/isBookReady 规则。确认 studio 旁白音频实际 MIME（worker `mimeToExt` 表）。
- [ ] **Step 2: 写失败测试**：cover/ending 判定、同 shotId 取 version 最大、`status=="accepted"` 过滤、缺图/缺音、`IsBookReady`（含 shots<3 兜底、contentCount<=0 守卫、ceil(content/2)）、title `||"绘本"`。**双端 golden**：同输入断言与 `pictureBookPages.test.ts` 同结构。
- [ ] **Step 3: 跑测试确认失败**。
- [ ] **Step 4: 实现** —— `Shot`/`Asset`/`Page` 类型 + `Assemble(projectName, shots, assets) []Page` + `IsBookReady(shots, assets) bool`，逐字镜像 TS。`Asset` 含 `StorageConfigID`（Y1，从专用查询来，本任务先定义字段）。
- [ ] **Step 5: 跑测试确认通过** `GOWORK=off go test ./internal/picturebook/... -count=1`。
- [ ] **Step 6: commit** `feat(picturebook-export): 后端装订 Assemble（镜像 assemblePages）`

## Task 3: 三种渲染器

**Files:** `internal/picturebook/render_zip.go` / `render_pdf.go` / `render_epub.go` + 各测试；`go.mod`（gopdf/go-epub）；嵌入字体文件 + OFL.txt

接口：`type Renderer func(book []Page, pages []PageBytes) ([]byte, string, error)`（contentType 返回值）。`PageBytes` 含每页 image/audio 字节 + MIME（runner 拉好传入）。

- [ ] **Step 1: zip（先，最稳）** —— TDD：`render_zip.go` 用 stdlib `archive/zip` 打包图/音/narration.txt/manifest.json，扩展名用 mimeToExt；测试断言 zip 魔数 `PK` + 含预期条目。commit。
- [ ] **Step 2: pdf spike** —— 先拿到一个**静态单字重 glyf-flavored Noto Sans SC TTF**（Fontsource static / fonttools instancer），写最小 spike：`AddTTFFontData`+`SetFont`+`Cell(nil,"中文旁白")`+`WritePdf`，断言无 error + PDF 文本层含中文。**验证将要 embed 的那个具体文件**。spike 过了再写 render_pdf.go。
- [ ] **Step 3: render_pdf.go** —— `go:embed` 字体 + 每页布局（图缩放保比例 + 手算 CJK 折行 via `MeasureTextWidth`）+ cover/ending 标题。测试：渲含中文小绘本 → %PDF 魔数 + 文本层含中文 + 体积合理（证子集化）。commit（含 go.mod + 字体 + OFL.txt）。
- [ ] **Step 4: epub spike** —— go-epub v1.1.0 造 1 页带音频最小 EPUB → 解 zip 断言 mimetype 非压缩首项 + OPF manifest 含 audio item + XHTML 含 `<audio>` + **epubcheck 通过**。
- [ ] **Step 5: render_epub.go** —— `AddImage`/`AddSection`(内嵌 `<audio>`)/`AddAudio`/`AddCSS`；缺音页降级纯图文。测试同 spike 断言。commit（pin go-epub v1.1.0）。

## Task 4: runner + 读字节阶梯

**Files:** `internal/exports/runner.go`（新）+ 测试

- [ ] **Step 1: 写失败测试**（DB-backed + Fake blob 后端）：runner claim pending → 拉 shots+accepted assets → Assemble → 用 Fake `Get(key)`（**非 SignedURL**）拉字节 → render → Put 产物 → MarkDone(blob_key,size)；失败 → MarkFailed/退避；reaper 兜底。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现** —— claim/poll/lease 仿 worker `:240-340`；**读字节阶梯**（🔴 R1）：type-assert `ReadKey(ctx,key)`→`ReadKey(key)`→Fake `Get(key)`→`SignedURL`+`http.Get`；bs 按 asset `storage_config_id` 解析（空→BlobStoreForMode，否则 BlobStoreForConfigID）；写产物 `ResolveWriteTarget(ctx,orgID,projStorageConfigID)`（三返回值）→`Put`，返回 configID 写 export_jobs。`CallTimeout` 单 job。
- [ ] **Step 4: 跑测试确认通过**（fresh DB，Fake 后端）。
- [ ] **Step 5: commit** `feat(picturebook-export): export runner（读字节阶梯 + 渲染 + 落库）`

## Task 5: HTTP 端点 + exportScope + 接线

**Files:** `internal/httpapi/exporthandlers.go`（新）；`internal/httpapi/httpapi.go`（路由 + exportScope）；`cmd/studiod/main.go`（起 runner+reaper + Deps）；测试

- [ ] **Step 1: 写失败测试**（DB-backed）：POST 非绘本→400；POST 绘本未就绪→409；POST 就绪→201{jobId}（planId 省略取最新 plan）；GET status（别 org 的 jobId→403/404，jobId.project_id!=id→404）；GET content done 前→404、done→302 attachment。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现 handlers** —— 4 端点；planId 解析（省略=最新 plan，单传 Shots/Assets/Assemble/IsBookReady）；非绘本 400；jobId.project_id==id 校验；`Assemble(project.Name||"绘本", ...)`。
- [ ] **Step 4: exportScope + 接线** —— `exportScope`(export_jobs.id→project_id→org，复用 OrgIDForProject) + `export := func(min,h)` 闭包 + Deps 字段；路由注册；`cmd/studiod/main.go` 起 export runner pool + reaper（仿 worker `:301-340`）+ Deps 注入。
- [ ] **Step 5: 跑测试确认通过** + `GOWORK=off go build ./...`。
- [ ] **Step 6: commit** `feat(picturebook-export): 导出端点 + exportScope + studiod 接线`

## Task 6: 前端 ExportDialog + 轮询

**Files:** `web/src/features/workflow/ExportDialog.tsx`（新）；`web/src/features/workflow/api.ts`（hooks）；`web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`（按钮）；测试

- [ ] **Step 1: 写失败 vitest** —— ExportDialog 选格式提交调 useCreateExport；useExportJob 轮询（done 停轮询、显示下载；failed 停轮询、toast 错误）；导出按钮仅 `bookReady` 显示。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现** —— `useCreateExport`(mutation POST exports)、`useExportJob(jobId)`（react-query `refetchInterval`，done/failed 停）；`ExportDialog`（复用统一 Dialog，单选 pdf/epub/zip）；运行页 `bookReady` 时按钮 + done 后 `<a download>`。颜色 token，toast single-source。
- [ ] **Step 4: 验证** —— 改动文件 `npx eslint` 零新 error + vitest 全绿 + `npm run build`。
- [ ] **Step 5: commit** `feat(picturebook-export): 前端导出对话框 + 轮询下载`

---

## 执行序

T1 → T2 → T3 → T4 → T5 → T6（单实现者顺序）。每任务：implementer → spec 审 → 质量审 → 过。全绿后整体终审 → push → PR（不直推 main）。

## 风险提醒（实现者必看）

- 🔴 PDF 字体：必须是**静态单字重 glyf-TTF**（非 OTF/CFF、非可变字体）；spike 验证**那个具体文件**。
- 🔴 读字节：localfs/Fake 的 SignedURL 不能 http.Get；必须走阶梯。T4 DB 测试用 Fake → 不走阶梯必断。
- 🔴 planId：必须单 plan（省略=最新），否则跨 plan 混页。
- 🟡 go-epub 已归档，pin v1.1.0；EPUB spike 要过 epubcheck。
- 🟡 assets 查询要补 `storage_config_id`（artifacts.Assets 没这列）。
