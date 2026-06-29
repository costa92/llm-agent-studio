# 绘本成书导出（Picturebook Export）设计

**日期**：2026-06-29
**状态**：设计已批准，待两轮评审 → 写计划
**分支**：`feat/picturebook-export`

## 目标

把已审核（accepted）的绘本资产（每页图 + 旁白文字 + 可选音频）装订导出为**可下载的成书文件**，补上环节 6 的缺口（当前只有应用内阅读器）。格式 **PDF / EPUB3 / 资产 zip 三选一（导出时用户选）**；**异步任务 + 轮询**交付；产物落 BlobStore。

## 关键架构事实（带证据）

- **前端已有装订逻辑**（要镜像到后端）：`web/src/features/workflow/pictureBookPages.ts:16` `assemblePages()`——按 `shotId` 归集 `status==="accepted"` 的 image/audio（同 shot 多版本取 `version` 最大），首 shot=cover、末 shot=ending、其余=content，旁白取 `shot.action`，title=项目名（`runs.$runId.tsx:284` `bookTitle=project.name`）。`isBookReady()` 阈值=accepted image ≥ `ceil(内容页/2)` 且 ≥1。
- **后端同源数据**：`internal/studiosvc/artifacts.go` `Shots(...)`（`ORDER BY ordering ASC`，返回 `{id,shotNo,action,prompt,...}`）+ `Assets(...)`（按 project/status，返回 `{id,shotId,type,blob_key,status,version,...}`）。**后端镜像 assemblePages 几乎零成本**（同两表同排序）。
- **服务端读 blob 字节**：`blob.BlobStore` 只统一暴露 `Put/SignedURL/Delete`；`ReadKey` 是可选且签名不统一（localfs 无 ctx、github 有 ctx、s3/oss 无 ReadKey）。`assetContentHandler`（`m2handlers.go`）因此对非 localfs 一律 `SignedURL` + 302。**结论：服务端读字节唯一通用路径 = `SignedURL` + `http.Get`。**
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

**只复用模式不复用表**（worker 的 todos 队列会污染 run 生命周期）。新建 `export_jobs` 表（走 Go step 版本化迁移）：

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

拉字节走通用路径：`bs.SignedURL(ctx, asset.BlobKey, ttl)`（bs 按 `asset.StorageConfigID` 解析，同 assetContentHandler）→ `httpClient.Get(url)`。

## 三种格式渲染 + Go 库选型

`internal/picturebook/render_{pdf,epub,zip}.go`，接口 `Render(book) ([]byte, contentType, error)`。

### PDF — `github.com/signintech/gopdf`（做字形子集，体积小）
- **中文字体（最高风险）**：gopdf 子集化需**带 glyf 轮廓的 TTF**（非 CFF/OTF——Noto Sans CJK 官方是 OTF/CFF，子集会失败）。用 **Noto Sans SC TTF**（或思源黑体 TTF），OFL 可商用，`go:embed` 进二进制（一份 SC Regular 足够旁白），随附许可文件。**task 先 1h spike 验证子集化产出中文 + 体积合理**。
- 每页布局：上方 `object-contain` 缩放图（填满页宽保比例），下方居中旁白（自动换行）；cover/ending 居中大图 + 标题。
- 简体为目标受众；繁体/emoji 覆盖暂不保证。

### EPUB3（含嵌入音频）— `github.com/bmaupin/go-epub`，音频需 spike
- 图片/章节/CSS：go-epub `AddImage`/`AddSection`/`AddCSS` 成熟；每页一个 XHTML section（图 + `<p>` 旁白）；中文只需 XHTML UTF-8，**阅读器自带 CJK 字体，无需嵌字体**。
- **音频嵌入（风险）**：EPUB3 用 `<audio>` 引用 OPF manifest 内音频项。go-epub 的任意 media+manifest 能力**待实测**——
  - 若支持 `AddMedia` 类 API → `<audio controls src="...">`。
  - **兜底**：手工 `archive/zip` 写 EPUB（`mimetype` 非压缩首项 + `META-INF/container.xml` + `content.opf`(含 `media-type=audio/mpeg`) + XHTML + `audio/*`）。绘本结构固定，手工 OPF 不复杂。
- **task 先 spike go-epub 音频（半天），不行则走手工 OPF**。音频缺失页降级为纯图+文。

### 资产 zip — stdlib `archive/zip`（零新依赖）
打包全部 accepted 原始图/音 + 旁白文本 + manifest.json（页序列 + 每页 assetId/prompt/provider/model，便于复现）。结构：
```
book/cover.png  001_image.png 001_narration.txt 001_audio.mp3  002_...  manifest.json
```
扩展名按字节 `http.DetectContentType` 或沿用 worker `mimeToExt` 表。

## API 端点（鉴权 + org 隔离）

| 方法 路由 | 鉴权 | 说明 |
|---|---|---|
| `POST /api/projects/{id}/exports` | `proj(roleEditor,…)` | body `{format, planId?}`；校验 `kind=='picturebook'` + `IsBookReady` → 未就绪 **409**；INSERT pending → `201 {jobId}` |
| `GET /api/projects/{id}/exports` | `proj(roleViewer,…)` | 列该项目导出历史（可选） |
| `GET /api/projects/{id}/exports/{jobId}` | `proj(roleViewer,…)` | 轮询 `{id,format,status,sizeBytes,error,createdAt}`；**校验 jobId.project_id==id** 防越权 |
| `GET /api/exports/{jobId}/content` | 新建 `export(roleViewer,…)` scope | done 才给；按 `storage_config_id` 解析后端 → `302 SignedURL`；`Content-Disposition: attachment; filename="<项目名>.<ext>"` |

新增 `exportScope`（仿 `assetScope`）：经 `export_jobs.id → project_id → projects.org_id` 解析 org 做 RBAC。

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

依赖序：T1 → T2 → T3 → T4 → T5 → T6（T6 前端可与 T3-T5 部分并行，但单实现者顺序执行）。

- **T1** 迁移（export_jobs，Go step）+ `internal/exports/store.go`（Create/Claim/MarkRunning/MarkDone/MarkFailed/Get/List/Reap + 状态机测试）
- **T2** `internal/picturebook/assemble.go`（镜像 assemblePages）+ `Shot`/`Asset`/`Page` 类型 + 测试 + 双端 golden
- **T3** 渲染器：`render_zip.go`（最易，先）→ `render_pdf.go`（含 `go:embed` Noto Sans SC TTF + spike）→ `render_epub.go`（go-epub + 音频 spike / 手工 OPF 兜底）。接口 `Render(book)([]byte,string,error)`
- **T4** `internal/exports/runner.go`（claim/poll/lease + reaper，仿 worker）+ 拉字节(SignedURL+http.Get) + ResolveWriteTarget.Put
- **T5** `internal/httpapi/exporthandlers.go`（4 端点 + exportScope）+ 路由注册（httpapi.go）+ `cmd/studiod/main.go` 起 export runner+reaper + Deps 注入
- **T6** 前端：`ExportDialog.tsx` + api.ts hooks（useCreateExport/useExportJob 轮询）+ 运行页导出按钮

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
