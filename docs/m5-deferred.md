# M5 前端延后项与已知缺口

> 镜像计划 `2026-06-11-ai-studio-m5-frontend`（生态 umbrella 仓 `docs/superpowers/plans/`）§已知缺口与延期项，以及 UI-spec `2026-06-11-ai-studio-ui-spec` 与真实后端 handler 的差异。
>
> M5 范围决策：**前端专属里程碑，后端零改动**。所有前后端差异**只标记、不静默改后端**（CLAUDE.md 第3条：外科手术式改动）。任何需要新增后端代码的缺口都记录为后续里程碑的输入。

## 1. 验证范围（无浏览器）

| 项 | 为何 M5 出局 | 重启需要什么 |
|---|---|---|
| **真实浏览器 / Playwright E2E** | sandbox 无浏览器、无 Playwright | 有浏览器的 CI/环境里补端到端冒烟（登录→建项目→运行→看轨道随 SSE 推进） |
| **跨栈联调（dev server vs 活后端）** | headless 不可行；非完成判据 | 手动可选 smoke：起活 studiod（PG + `JWT_SECRET` + `HTTP_ADDR=:8083`）+ `pnpm dev`，浏览器联调 |

**M5 完成判据** = `pnpm build`（含 `tsc -b` 类型检查）+ `pnpm test`（vitest，152 例 / 23 文件）+ `pnpm lint`（eslint）三绿。承载逻辑（apiClient refresh-on-401、SSE reducer、role-gating、zod schema、keyset 分页）走 TDD（先写失败测试再实现）；纯展示视图 render/smoke 测试足矣。

## 2. 生产静态服务（**已由 M6（v0.6.0）关闭**）

- **现状**：dev 走 Vite proxy（`/api` → `:8083`，同源，无 CORS）。~~生产由 studiod 直接静态服务 `web/dist` 的能力当前后端未提供。~~
- **影响**：若需生产同源部署，是一处后端缺口。
- **M5 决策**：**只标记、不静默新增后端代码**。当前可由反向代理 / 独立静态托管承担前端分发；同源部署留待后续里程碑给 studiod 加 static file server（一处后端任务）。
- **更新（已由 M6 / v0.6.0 关闭）**：studiod 现经 `WEB_DIR` 静态服务 SPA——`internal/config/config.go:82,160`（`WebDir` / `WEB_DIR`）+ `cmd/studiod/main.go:383`（`os.DirFS`）+ `internal/httpapi/httpapi.go:354` 的 `GET /` SPA catch-all（`spaHandler`，`internal/httpapi/webui.go:19`）。此缺口已不复存在。

## 3. 前后端线缆契约差异（前端已兼容，部分记为后端 follow-up）

| 差异 | 现状 | 前端如何兼容 / 后端 follow-up |
|---|---|---|
| **`Asset.signedUrl` 不存在** | UI-spec §7.6 DTO 列了 `signedUrl`，但 `assets.Asset` 无此字段 | 可显示图一律走 `GET /api/assets/{id}/content`（302→签名 URL）：SPA 用 `apiFetch` + `redirect:"manual"` 读 `Location` 头，把签名 URL（无需 auth，HMAC 在 query）塞 `<img src>`；签名过期（图 onError）重拉一次刷新。**无需后端改动。** |
| **`GET /script` 是裸 JSON** | 不是 `{items}` 信封（`w.Write(content)` 直出原始剧本 JSON） | 单独 zod 容错解析（兜畸形）；其余多数列表是 `{items}`，仅项目列表与资产库是 `{items,next_cursor}`。 |
| **`todo_failed` 事件缺 `type` 字段** ~~（已补）~~ | ~~后端失败事件 payload 不带 `type`~~ | **前端 reducer 按 `todoId` 定位**（记录每个 todoId→stage/pip 映射，失败时回查），故失败节点不会"漂移"或滞留 running（见 commit `dcfdb6e`）。~~记为后端 follow-up~~ **更新：后端 follow-up 已完成**——`todo_failed` 现带 `{type,error}` payload（`internal/worker/worker.go:895` 及 async 分支 :1327），与其他 todo 事件对齐；前端 todoId 兜底可保留亦可移除。 |
| **SSE 白名单 9 种 > UI-spec 列的 7 种** | 多出 `asset_submitted`（M4 异步提交）/ `asset_prescreened`（M3 预筛） | 状态机已纳入全 9 种命名事件 + `message` 兜底；`asset_submitted` 维持 pip running（仅日志），`asset_prescreened` 日志 + pip 不变（不改审核态，审核仍走 HITL）。**符合现有后端，无缺口。** |
| **"运行历史" 无独立路由/端点** | 后端无专用历史端点 | 复用工作台 `GET /events` 回放 + 完成态项目（status∈{completed,review,failed,canceled}）只回放不开流。**符合现有后端，无缺口。** |
| **登录请求体大小写** | authz `struct{Email,Password}`，Go JSON 大小写不敏感 | 前端用 `{email,password}`（与 authz 自带测试一致）。**无缺口。** |

## 4. RBAC 编辑端探针缺口（乐观显示 + 后端强制）

- **现状**：access token 是 authz JWT，但角色是 per-(org,scope) 的，由后端 `ResolveRole` 决定，**不在 token claim 里**。前端**不解析 JWT 取角色**。
- **前端策略（UX，非安全边界）**：
  - **admin 判定**：对 admin-only 探针（`GET /api/orgs/{org}/model-configs`）的 `200`/`403` 推断当前用户在该 org 是否 admin，缓存进 Query。非 admin 时隐藏审核/成本/模型配置入口；直访路由 → `403` → 重定向 + "需要管理员权限"。
  - **editor 判定缺口**：**无 viewer-vs-editor 探针端点**。`新建项目` / `运行` / `取消` / `重新运行` 等 editor+ CTA **乐观显示**（所有非纯 viewer 上下文都渲染），真正的权限由后端 `scoped(roleEditor, ...)` 强制 —— viewer 点击会收到 `403`，前端 toast 提示。
- **后果**：viewer 可能短暂看到 editor CTA，点击后被后端拒。**这是有意的 UX 权衡**（避免为前端门禁新增后端探针端点）。**后端 follow-up（可选）**：若要前端精确隐藏 editor CTA，需一个返回当前用户在该 org 角色的轻量端点（如 `GET /api/orgs/{org}/me`）。

## 5. 视图/字段无后端支撑（按原型标"二期"）

| 项 | 现状 | M5 决策 |
|---|---|---|
| **视频/音频生成触发** | 后端 async submit→poll 引擎在，但当前 **audio=MiniMax T2A 同步真实**（另有 `POST /api/projects/{id}/lyrics-audio` 端点，`httpapi.go:234`）、**video 无真实 provider 适配器**（原骨架已下架，仅 `FakeAsync` 沙箱条目）；生成触发编排不在 M5 前端范围 | 前端只**展示/播放**库里返回的 `video`/`audio` 资产（`<video controls>` / `<audio controls>`），不新增生成触发 UI。 |
| **库的视频/音频过滤项** | 原型标"二期" disabled | FilterRail 的「视频/音频」类型选项 disabled 并标"二期"；类型过滤当前只走「图片」。生成管线在前端铺开后再启用。 |
| **模型配置 video/audio kind** | 同上 | ModelConfig 编辑表单的 video/audio kind 标"二期"；chat/image 可配。 |
| **`asset_prescreened` 预筛分** | M3 ReviewAgent advisory 预筛（`prescreenScore`/`prescreenFlags`） | 仅作日志/参考展示，**不**改审核态 —— HITL 仍是硬门禁（accept/reject/regenerate 走人工）。 |

## 6. 安全相关前端约束（与后端密钥语义一致）

- **access token 仅内存**：不进 localStorage/sessionStorage（防 XSS 窃取）；刷新走 httpOnly refresh cookie + `X-CSRF: 1` 双提交。页面刷新会丢 access token → 触发一次 refresh-on-load 重建会话（refresh cookie 仍在）。
- **永不下发密钥**：模型配置表单**绝不含 API key 字段**，文案明示密钥服务端管理；`model_configs.params_json` 含密钥型 param → 后端 `ErrSecretParam`(400) → 前端 toast。API 响应面（model-configs/catalog/cost）不含任何 key 字段。
- **签名 URL**：资产内容经 `GET /api/assets/{id}/content`（302→短 TTL HMAC 签名 URL）取图；前端不持有/不构造签名，过期即重拉。
