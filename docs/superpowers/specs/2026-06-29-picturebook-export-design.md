# 绘本成书导出（Picturebook Export）设计

**日期**：2026-06-29
**状态**：设计已批准，待两轮评审 → 写计划
**分支**：`feat/picturebook-export`

## 目标

把已审核（accepted）的绘本资产（每页图 + 旁白文字 + 可选音频）装订导出为**可下载的成书文件**，补上环节 6 的缺口（当前只有应用内阅读器）。格式 **PDF / EPUB3 / 资产 zip 三选一（导出时用户选）**；**异步任务 + 轮询**交付；产物落 BlobStore。

## 关键架构事实（带证据）

- **前端已有装订逻辑**（要镜像到后端）：`web/src/features/workflow/pictureBookPages.ts:16` `assemblePages()`——按 `shotId` 归集 `status==="accepted"` 的 image/audio（同 shot 多版本取 `version` 最大），首 shot=cover、末 shot=ending、其余=content，旁白取 `shot.action`，title=项目名（`runs.$runId.tsx:284` `bookTitle=project.name`）。`isBookReady()` 阈值=accepted image ≥ `ceil(内容页/2)` 且 ≥1。
- **后端同源数据**：`internal/studiosvc/artifacts.go` `Shots(...)`（`ORDER BY ordering ASC`，返回 `{id,shotNo,action,prompt,...}`）+ `Assets(...)`（按 project/status，返回 `{id,shotId,type,blob_key,status,version,...}`）。**后端镜像 assemblePages 几乎零成本**（同两表同排序）。
- **服务端读 blob 字节（评审修正 🔴 R1）**：`blob.BlobStore` 只统一暴露 `Put/SignedURL/Delete`；`ReadKey` 是可选且签名不统一（github `ReadKey(ctx,key)`、localfs `ReadKey(key)` 无 ctx、s3/oss 无 ReadKey；Fake 有 `Get(key)` 不在接口上）。**`SignedURL+http.Get` 对 localfs/Fake 不成立**——localfs `SignedURL` 返**相对路径** `/api/blob/{key}?...`（`localfs.go:88`，无 scheme/host），Fake 返 `fake://...`（`fake.go:38`），服务端 `http.Get` 直接 `unsupported protocol scheme`。`assetContentHandler` 能用 SignedURL 是因为它 **302 给浏览器**（浏览器按 app origin 解析相对 URL），服务端 runner 无此 origin、全仓也无 app base-URL 配置。**结论：runner 必须用读字节阶梯**：① type-assert `ReadKey(ctx,key)`（github，仿 `m2handlers.go:441-457` 已有 ladder）→ ② type-assert localfs `ReadKey(key)` → ③ Fake `Get(key)` → ④ 兜底 `SignedURL`+`http.Get`（s3/oss 绝对 URL）。
- **异步任务蓝本**：worker `claim()`（`internal/worker/worker.go:240`）用 `FOR UPDATE SKIP LOCKED` + 租约（`locked_by/locked_until`）；`reaper.go` ticker 兜底；`cmd/studiod/main.go:298-340` 起 worker pool + reaper。但 **worker 队列=todos 表、深耦合 run 生命周期**（`DeriveStatus` 计入全部 todo、`MarkFailed` 级联 cancel），导出塞 todos 会污染运行态。
- **迁移框架**：`storage.go` `Migrate()` —— 旧式幂等 DDL 列表 `m1…m19` + 版本化 Go step（`goSteps()`）。
- **路由/鉴权**：`internal/httpapi/httpapi.go` —— asset accept/reject 用 `asset(roleAdmin,…)`（按 asset 所属 org RBAC）；项目级读用 `proj(roleViewer,…)`（经 `OrgIDForProject` 解析 org）。下载用 302→SignedURL 成熟模式。
- **写产物**：`storagerouter.ResolveWriteTarget(ctx,orgID,projConfigID)` 返回 `(BlobStore, configID)`。

## 架构总览（端到端）

```
用户在运行页/绘本阅读器点「导出」→ 选格式(pdf|epub|zip)
  ▼ POST /api/projects/{id}/exports {format, planId?}   (proj roleEditor)
  │   校验 IsBookReady(后端镜像) → 未就绪 409 → INSERT export_jobs(pending) → 201 {jobId}
  ▼ 独立 export runner (studiod goroutine, 复用 claim/lease/reaper 模式, 独立 export_jobs 表)
  │   claim → 拉 shots+accepted assets → Assemble(镜像 assemblePages)
  │   → 每页 image/audio: SignedURL→http.Get 拉字节 → render(pdf|epub|zip) → []byte
  │   → ResolveWriteTarget.Put(exportKey) → UPDATE done(blob_key,size)；失败→failed(可退避重试)
  ▼ 前端轮询 GET /api/projects/{id}/exports/{jobId}  (proj roleViewer) → {status,...}
  ▼ done → GET /api/exports/{jobId}/content  (export scope roleViewer) → 302 SignedURL(产物, Content-Disposition attachment)
```

**核心取舍**：导出是**只读消费**（读 accepted 资产 + 渲染），不产 run 工件 → **独立于 todos 运行队列**，但**复用 worker 验证过的 claim/lease/reaper 模式**。

## 异步任务方案：独立 export_jobs 表 + 独立 runner

**只复用模式不复用表**（worker 的 todos 队列会污染 run 生命周期）。新建 `export_jobs` 表，**走 Go step 版本化迁移 m22**（Y8：现状 `goSteps()` 最新是 m21 `m21AddItemsColumn`，`storage.go:525-532`；新增 `{version:"m22", run:m22CreateExportJobs}` 追加进切片 + 一个 `func(ctx, pgx.Tx) error`。Go step 跑在 **pgx.Tx**，与 store.go 的 GORM 铁律是两回事，别混）：

```sql
CREATE TABLE IF NOT EXISTS export_jobs (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    plan_id           TEXT NOT NULL DEFAULT '',
    format            TEXT NOT NULL,                 -- 'pdf' | 'epub' | 'zip'
    status            TEXT NOT NULL DEFAULT 'pending', -- pending|running|done|failed
    blob_key          TEXT NOT NULL DEFAULT '',
    storage_config_id TEXT NOT NULL DEFAULT '',
    size_bytes        BIGINT NOT NULL DEFAULT 0,
    error             TEXT NOT NULL DEFAULT '',
    attempts          INT  NOT NULL DEFAULT 0,
    locked_by         TEXT NOT NULL DEFAULT '',
    locked_until      TIMESTAMPTZ,
    next_run_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS export_jobs_claim_idx   ON export_jobs (status, next_run_at);
CREATE INDEX IF NOT EXISTS export_jobs_project_idx ON export_jobs (project_id);
```

**状态机**：
```
pending ──claim──▶ running ──成功──▶ done(terminal)
   ▲                  │
   └──reschedule──────┤──失败&attempts<max──▶ pending(退避 next_run_at)
                      └──失败&attempts≥max──▶ failed(terminal, error)
租约过期(running & locked_until<now) → 被重新 claim（崩溃恢复，幂等覆盖 blob）
reaper: running & locked_until<now-TTL → failed（兜底防永久 strand）
```
`MarkDone`/`MarkFailed` 用 `WHERE id=$1 AND status='running'` 守卫（RowsAffected 仲裁竞态，照搬 todos/store.go 模式）。claim 用 `FOR UPDATE SKIP LOCKED`。

`internal/exports/store.go`：`Create / Claim / MarkRunning / MarkDone / MarkFailed / Get / ListByProject / Reap`，纯 `$N`，`INSERT...RETURNING`，GORM 铁律。

## 后端装订：镜像 assemblePages 到 Go

新建 `internal/picturebook/assemble.go`（纯函数，无 IO，易测）：

```go
type Page struct {
    Kind         string // cover|content|ending
    Title        string // cover/ending 用 project.Name
    Narration    string // shot.Action
    ImageAssetID string
    AudioAssetID string
}
func Assemble(projectName string, shots []Shot, assets []Asset) []Page
func IsBookReady(shots []Shot, assets []Asset) bool
```

逐字镜像 `pictureBookPages.ts:16-61`（accepted 过滤、按 shotId 取 version 最大、首尾 cover/ending、title=projectName）。**装订与渲染分离**：`Assemble` 只产页序列 + assetID，runner 再按 assetID 拉字节，便于单测装订逻辑、复用同一页序列喂三种渲染器。

> **双端 golden 对照测试**（防漂移）：同一组 shots/assets 输入，断言 Go `Assemble` 与 `pictureBookPages.test.ts` 同结构（仿 README 的 regex-parity-check 思路）。

拉字节走**读字节阶梯**（🔴 R1，不是单纯 SignedURL+http.Get）：bs 按 asset 的 `storage_config_id` 解析后端——`StorageConfigID==""` → `router.BlobStoreForMode(orgID, proj.StorageMode)`；否则 `router.BlobStoreForConfigID(ctx, orgID, cfgID)`（`storagerouter/router.go:137`），orgID 经 `OrgIDForProject` 取（Y2）。拿到 bs 后按 §架构事实的阶梯读字节（github ReadKey(ctx) → localfs ReadKey → Fake Get → s3/oss SignedURL+http.Get）。

> **🟡 Y1：`artifacts.Assets` 不返回 `storage_config_id`**（`artifacts.go:133-170` 选列无此列）。runner 解析后端必须有它——T2/T4 用**专用查询**（或扩 Assets 选列）补 `storage_config_id`，别直接复用 `artifacts.Assets`。装订用的字段（shotId/type/blob_key/status/version/action）`artifacts.Shots/Assets` 已够。

## 三种格式渲染 + Go 库选型

`internal/picturebook/render_{pdf,epub,zip}.go`，接口 `Render(book) ([]byte, contentType, error)`。

### PDF — `github.com/signintech/gopdf`（做字形子集，体积小；源码确认走 glyf 子集，无 CFF 处理）
- **中文字体（最高风险，评审 R1 收紧）**：gopdf 子集化需**静态、单字重、glyf-flavored TTF**——三个来源陷阱都要避开：① Noto Sans CJK 官方包是 `.otf`=CFF → 直接失败；② Google Fonts 网页下的 Noto Sans SC 现在是**可变字体**（含 fvar/gvar）→ gopdf 忽略变体退化默认实例、有边角风险，**不用**；③ **正解**：取静态单字重 glyf-TTF 实例——Fontsource 的 Noto Sans SC「full/static」TTF（`fontsource.org/fonts/noto-sans-sc`，OFL），或 fonttools `instancer` 从可变字体烘一个静态 Regular（构建期一次性）。`go:embed []byte` + `pdf.AddTTFFontData(family, data)` + `pdf.SetFont`（**不用** `AddTTFFont` 路径版，因要 embed）。随附 `OFL.txt` + 保留字体名。
- **spike 第一性命题（R1）**：把**将要 `go:embed` 的那个具体 TTF 文件**喂给 `AddTTFFontData`+`SetFont`+`Cell(nil,"中文旁白")`+`WritePdf`，断言无 error 且 PDF 文本层含中文。**验证「那一个文件」而非「Noto Sans SC 家族」**（同名家族不同打包格式天差地别）。
- 每页布局：上方缩放图（填满页宽保比例），下方居中旁白。**Y2：gopdf 无 CJK 断行器**——中文自动换行需用 `MeasureTextWidth` 逐字累加折行手算（实现复杂度，非可行性）；cover/ending 居中大图 + 标题。
- 简体为目标受众；繁体/emoji 覆盖暂不保证。嵌全集 ~8–10MB TTF v1 可接受，**预子集省体积是 YAGNI（首版不做）**。

### EPUB3（含嵌入音频）— `github.com/bmaupin/go-epub`（**pin v1.1.0**，已归档，评审 R2 上调）
- 图片/章节/CSS：`AddImage`/`AddSection`（接受任意 XHTML，不校验）/`AddCSS` 成熟；每页一个 XHTML section（图 + `<p>` 旁白）；中文只需 XHTML UTF-8，**阅读器自带 CJK 字体，无需嵌字体**。
- **音频嵌入（主路径已确认可行，非"待实测"）**：go-epub **v1.1.0 已有正式 `AddAudio(source, filename) (string, error)`**，Write 时经 `addToManifest` 写进 OPF manifest，media-type 用 `gabriel-vasile/mimetype` 检测（比 `http.DetectContentType` 强，正确识别 mp3→`audio/mpeg`，规避 mp3 误判 octet-stream 的坑）。**主路径**：`AddAudio` + `AddSection` 内嵌 `<audio controls src="../audios/xxx.mp3">`。mp3/mp4/m4a 是 EPUB3 Core Media Type，规范合法。
- **plan B（仅文档化，预计用不上）**：若 go-epub 归档导致某场景不可用，手工 `archive/zip` 写 EPUB（`mimetype` 非压缩首项 + `META-INF/container.xml` + `content.opf` 含 `media-type` + XHTML + `audio/*`）。
- **spike 验收（R2，补 epubcheck）**：造 1 页带音频最小 EPUB → 解 zip 断言 ① `mimetype` 非压缩首项 ② OPF manifest 含 `media-type="audio/mpeg"` item ③ XHTML 含 `<audio>` ④ **跑 W3C `epubcheck` 通过**（只断魔数/manifest 不够）。音频缺失页降级为纯图+文。

### 资产 zip — stdlib `archive/zip`（零新依赖）
打包全部 accepted 原始图/音 + 旁白文本 + manifest.json（页序列 + 每页 assetId/prompt/provider/model，便于复现）。结构：
```
book/cover.png  001_image.png 001_narration.txt 001_audio.mp3  002_...  manifest.json
```
扩展名按字节 `http.DetectContentType` 或沿用 worker `mimeToExt` 表。

## API 端点（鉴权 + org 隔离）

| 方法 路由 | 鉴权 | 说明 |
|---|---|---|
| `POST /api/projects/{id}/exports` | `proj(roleEditor,…)` | body `{format, planId?}`；非绘本(`kind!='picturebook'`) → **400**（Y5）；**planId 省略=取最新 plan**（`plansQuery` 第一个，`artifacts.go:197` `isLatestPlan`），解析出的 planId 落 export_jobs.plan_id（🔴 R2，必须确定单一 plan，否则跨 plan 混页）；`IsBookReady`(按该 planId 的 shots+assets) 未就绪 → **409**；INSERT pending → `201 {jobId}` |
| `GET /api/projects/{id}/exports` | `proj(roleViewer,…)` | 列该项目导出历史（可选） |
| `GET /api/projects/{id}/exports/{jobId}` | `proj(roleViewer,…)` | 轮询 `{id,format,status,sizeBytes,error,createdAt}`；**校验 jobId.project_id==id** 防越权 |
| `GET /api/exports/{jobId}/content` | 新建 `export(roleViewer,…)` scope | done 才给；按 `storage_config_id` 解析后端 → `302 SignedURL`；`Content-Disposition: attachment; filename="<项目名>.<ext>"` |

新增 `exportScope`（仿 `assetScope`，`httpapi.go:83-93`/`:192-194`）需三处接线（Y6）：(a) Deps store 方法 `export_jobs.id → project_id → org`（链式复用 `OrgIDForProject`）；(b) 新增 `export := func(min,h)` 闭包；(c) 新 Deps 字段。`GET /api/projects/{id}/exports/{jobId}` 用 `proj(roleViewer)` 只校 `{id}` 的 org，故 handler 内**必须校验 `jobId.project_id==id`** 防越权（spec 既定）。

**关键数据流细节（评审 Y）**：
- **planId 单一真源**：POST 解析出的 planId 同时传给 `Shots(planId)`、`Assets(planId)`、`Assemble`、`IsBookReady`——四者必须同 plan（🔴 R2）。
- **装订保真**（Y3/Y4）：实现者**逐字镜像 `pictureBookPages.ts:16-61`**，别照本 spec 摘要——`IsBookReady` 含 `shots<3 用 shots 兜底` + `contentCount<=0 守卫`；cover/ending 标题用 `project.Name || "绘本"`（`runs.$runId.tsx:284` 同款兜底），否则项目名为空时双端 golden 裂、与阅读器不一致。
- **写产物落后端**（Y7）：`ResolveWriteTarget(ctx, orgID, projConfigID)` 是**三返回值** `(BlobStore, configID, error)`（`router.go:161`）；projConfigID 传**项目自己的 `StorageConfigID`**，返回的 configID 写进 `export_jobs.storage_config_id`（与下载读回一致）。

## 前端

- **导出入口**：运行页 `runs.$runId.tsx` 在 `bookReady` 为真时，绘本阅读器入口旁加「导出成书」按钮。
- **格式选择**：复用统一 `Dialog` 组件，单选 pdf/epub/zip + 「开始导出」。
- **API + 轮询**：`web/src/features/workflow/api.ts` 加 `useCreateExport`(mutation) + `useExportJob(jobId)`（react-query `refetchInterval`，done/failed 停轮询）。沿用 `apiJSON` + toast single-source。
- **下载**：done → `<a href="/api/exports/{jobId}/content" download>`；失败 toast（区分 429/500）。
- 新建 `ExportDialog.tsx`（`web/src/features/workflow/`，与 PictureBookReader 同目录）。

## 边界与错误

| 场景 | 处理 |
|---|---|
| 未就绪（`IsBookReady=false`） | POST 409；前端按钮仅 `bookReady` 显示（双保险） |
| 缺图 | content 页无 accepted image → 渲染占位（PDF "插图缺失" 文字块），不中断 |
| 缺音 | PDF/zip 本不含播放器；EPUB 该页省略 `<audio>`；不算失败 |
| 大文件/内存 | 绘本页数有限；拉字节+渲染走流式 `Put`；runner 单 job `CallTimeout` |
| job 失败重试 | `attempts<max` → 退避 reschedule；幂等覆盖同 blob_key |
| 崩溃 strand | reaper ticker 把 running 超 TTL 的 job → failed |
| SignedURL 后 blob 已删 | http.Get 404 → 该页降级占位（图缺降级、全空才 failed） |

## 测试策略

- **装订纯函数**（`assemble_test.go`，无 DB）：cover/ending 判定、version 取最大、accepted 过滤、缺图/缺音、`IsBookReady` 阈值。**双端 golden 对照** `pictureBookPages.test.ts`。
- **状态机 DB-backed**（fresh DB，`-p 1`，铁律）：claim 互斥（两 runner 不重复 claim）、租约过期重 claim、MarkDone/MarkFailed 守卫、reaper 兜底。`pool.Exec` 种子失败要断言（勿吞错）。
- **渲染冒烟**：pdf/epub/zip 各产小绘本字节，断言魔数（%PDF / PK / epub mimetype）。
- **中文渲染**：PDF 嵌字体渲中文旁白 → 解析 PDF 文本层断言中文存在 + 体积合理（证明子集化生效）；EPUB 断言 XHTML UTF-8 中文正确。
- **音频嵌入**：EPUB 解 zip 断言 OPF manifest 含 audio item + XHTML 含 `<audio>`。
- **handler**：409(未就绪)、org 越权(别 org 的 jobId → 403/404)、done 前下载 404、done 后 302。
- DB-backed 测试用真 PG（kb_m3_pg，fresh DB）。

## 任务拆分

依赖序：T0(前置) → T1 → T2 → T3 → T4 → T5 → T6（单实现者顺序执行）。

- **T0（前置确认，并入 T2/T3）** 确认 studio 旁白音频实际 MIME（看 worker `mimeToExt` 表 / 抽样 blob `http.DetectContentType`）——mp3/wav/m4a 才与 go-epub mimetype 检测 + zip 扩展名推断对得上（Y5 lib）。
- **T1** 迁移 m22（`goSteps()` 追加，跑 **pgx.Tx**）+ `internal/exports/store.go`（Create/Claim[FOR UPDATE SKIP LOCKED]/MarkRunning/MarkDone/MarkFailed[WHERE status='running' 守卫]/Get/List/Reap + 状态机 DB-backed 测试）
- **T2** `internal/picturebook/assemble.go`（**逐字镜像 `pictureBookPages.ts:16-61`**，含 IsBookReady 的 shots<3 兜底/<=0 守卫、title `||"绘本"`）+ `Shot`/`Asset`/`Page` 类型 + 测试 + **双端 golden 对照** + **资产查询补 `storage_config_id`**（Y1）
- **T3** 渲染器接口 `Render(book)([]byte,string,error)`：`render_zip.go`（stdlib archive/zip，最易先做，扩展名用 mimeToExt）→ `render_pdf.go`（`go:embed` **静态 glyf TTF** + `AddTTFFontData` + 手算 CJK 折行 + **spike 验证具体文件**）→ `render_epub.go`（go-epub v1.1.0 `AddAudio`+`AddSection` 主路径 + **epubcheck spike**，手工 OPF 仅 plan B）
- **T4** `internal/exports/runner.go`（claim/poll/lease + reaper，仿 worker `:240-340`）+ **读字节阶梯**（🔴 R1：github ReadKey(ctx)→localfs ReadKey→Fake Get→s3/oss SignedURL+http.Get）+ `ResolveWriteTarget`(三返回值)→`Put`，DB-backed 测试用 Fake 后端（故必须走阶梯非 SignedURL）
- **T5** `internal/httpapi/exporthandlers.go`（4 端点）+ **planId 解析(省略=最新 plan，单传 Shots/Assets/Assemble/IsBookReady)** + 非绘本 400 + jobId.project_id==id 越权校验 + **exportScope 三处接线**(Deps 方法/闭包/字段) + 路由注册(httpapi.go) + `cmd/studiod/main.go` 起 export runner+reaper(仿 `:301-340`) + Deps 注入
- **T6** 前端：`ExportDialog.tsx`（格式单选）+ api.ts hooks（useCreateExport/useExportJob 轮询，done/failed 停）+ 运行页 `bookReady` 时导出按钮 + 下载链接

## 风险 / 需关注

- 🔴 **PDF 中文字体**：必须带 glyf 轮廓的 CJK TTF（非 OTF/CFF）+ 许可文件；task 3 先 spike 验证子集化产中文。
- 🔴 **EPUB3 音频嵌入**：go-epub 音频能力待实测；不行手工 `archive/zip` 写 OPF。
- 🟡 服务端读 blob 用 SignedURL+http.Get（不扩 BlobStore 接口）。
- 🟡 触发鉴权 `roleEditor`；下载 export scope。
- 🟡 迁移走 Go step（版本化）。
- 🟢 产物 TTL 清理本期不做（留 M2，不阻塞）。

## 范围排除（YAGNI）

- 产物保留期/TTL 自动清理。
- 自定义排版/主题/封面设计。
- 给 `BlobStore` 加统一 `Get`（用 SignedURL+http.Get 替代）。
- 繁体/emoji 字形完整覆盖。
- 多格式一次性批量导出（一次一格式）。
