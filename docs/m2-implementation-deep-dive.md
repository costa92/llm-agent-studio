# AI Studio — M2 实现深潜（图片生成 + BYOK 模型路由 + 成本账本）

- 日期：2026-06-12
- 类型：只读实现分析（READ-ONLY）— 不改任何源码
- 上游设计：`../docs/superpowers/specs/2026-06-10-ai-studio-design.md`（下称「设计 §x」）
- 实施计划：`../docs/superpowers/plans/2026-06-10-ai-studio-m2.md`（下称「M2 计划 Tx」）
- 范围：`internal/{storage,secretbox,models,modelrouter,generate,blob,prompt,assets,review,cost,worker,agents,httpapi}` + `cmd/studiod/main.go`

> 所有引用为 `path:line`，路径相对仓库根 `llm-agent-studio/`。本仓 Go 命令需 `GOWORK=off`。
> 文中凡涉及 M3/M4 的列、文件、端点都已显式标注，避免读者混淆里程碑边界。

---

## 1. 概述与边界

### M2 交付什么

M2 在 M1 文本管线（plan→todos→worker→run_events/SSE，产出 `scripts`/`shots`）之上，补齐 **PRD 一期完成线**：

1. **BlobStore 抽象**（`internal/blob/`）：字节落地的唯一处；localfs（dev，HMAC 签名回源 URL）+ in-memory fake；S3/OSS/COS/GitHub 为后续 storagerouter 接线。
2. **MediaGenerator 接缝**（`internal/generate/`）：素材生成的唯一外部 API 触点；image 适配 `contract/llm.ImageGenerator`，多 provider；fake 测试替身；registry 按 provider+model 解析。
3. **PromptBuilder + 风格库**（`internal/prompt/`）：7 种固定风格后缀注入。
4. **AssetAgent**（`internal/agents/asset.go`）：shot → PromptBuilder → MediaGenerator，纯函数无 I/O。
5. **storyboard 完成时按 shot 数 fan-out 出 N 个 `asset` todo**（`internal/worker/worker.go:457-503`）。
6. **资产端到端落库**：worker 在 `runAsset` 里 resolve 模型 → 生成 → 落 blob → `assets` 行 `generating→pending_acceptance` + `generations` 账本行（`worker.go:510-673`）。
7. **HITL accept/reject/regenerate（admin-only，版本血缘）**（`internal/review/review.go`）。
8. **资产库跨项目检索**（`internal/assets/store.go` Library；tags/style/project/type/status + keyset 分页）。
9. **model_configs CRUD + BYOK 静态加密**（`internal/models/store.go` + `internal/secretbox/`）。
10. **ModelRouter 按 org 解析模型**（`internal/modelrouter/router.go`，K5 per-(provider×model)）。
11. **generations 成本账本**（`internal/cost/store.go`）。

### 上游边界（M1：被消费，不重做）

M2 不动 M1 的 `projects/plans/todos/scripts/shots/run_events` 表与 worker 调度框架。M2 复用 M1 的：
- todo 状态机与 `claim`/`process`/`MarkDone`/`fail` 框架（`worker.go:154-352`）；
- `DeriveStatus`/`allDone` 纯计数派生（`worker.go:740`）—— fan-out 出的 asset todo 天然被纳入计数；
- SSE run_events 时间线（M2 新增 `asset_generated`、复用 `todo_ready/started/finished/failed`）。

### 下游边界（M3/M4：不是 M2，但出现在共享文件里）

读 `internal/storage/storage.go` 时务必区分：M2 表是 `m2Migrations`（`storage.go:125-176`），后续里程碑是叠加的 ALTER/新表：

| 里程碑 | 位置 | 内容 | 不属于 M2 |
|---|---|---|---|
| **M3** | `storage.go:182-201` | `assets.prescreen_score/flags/note`（ReviewAgent 自动预审）+ `pricing` 表（计价种子）| ✓ prescreen 列、pricing 表 |
| **M4** | `storage.go:208-233` | `todos.poll_attempts`、`assets.external_job_id/submitted_at`、**B1 `assets_todo_uniq` / B3 `generations_asset_todo_uniq` 部分唯一索引**、`pricing.micros_per_second`、video/audio 种子价 | ✓ 异步轮询、崩溃幂等索引、按秒计费 |
| **M5** | `storage.go:237-240` | `model_configs.base_url` + `api_key_enc`（BYOK 凭据列）| ⚠️ 见注 |
| **M6** | `storage.go:245-264` | `storage_configs` 表（per-org / global 对象存储）| ✓ storagerouter |

> **里程碑标号 vs 范围的偏差（重要）**：设计 §15 把 BYOK / model_configs / generations 账本明确列在 **M2** 完成线。但实现里 `model_configs` 的 **凭据列（`base_url`/`api_key_enc`）放在 `m5Migrations`**（`storage.go:235-240`），`generations.cost_micros` 的真实计价依赖 **`m3Migrations` 的 `pricing` 表**（`storage.go:186-200`）。也就是说：M2 建了 `model_configs` 与 `generations` 的**骨架表**，而「BYOK 加密存 key」「成本真实定价」的能力在代码演进中被推到 M3/M5 才补齐列与逻辑——但这些能力**语义上属于 PRD 一期/M2 的承诺**。本文按「M2 范围 = 设计 §15 的 M2 描述」展开，并在每处标出代码实际落在哪个 migration 段。

类似地，`internal/generate/` 下的 `fake_async.go`/`audio/`/`video/` 目录、`generate.go:48-96` 的 `AsyncGenerator`/`SubmitResult`/`PollStatus`/`Canceler` 全是 **M4**；`assets/store.go` 的 `SetSubmitted`/`SetAsyncFailed`/`CountInFlightByKind`/`ReapStaleSubmitted` 也是 M4。M2 的 image 走纯同步单遍路径（`worker.go:580-673`）。

---

## 2. 数据模型

### assets（`storage.go:126-146`）

M2 核心列（`status` 默认 `generating`，`version` 默认 1，`parent_asset_id` 默认空串）：

```
id, project_id (FK→projects ON DELETE CASCADE), shot_id, todo_id,
type ('image' 默认), blob_key, url, prompt, style, provider, model,
status ('generating' 默认), version (1), parent_asset_id (''), tags TEXT[] ('{}'),
created_at
```

索引：`assets_project_idx (project_id)`、`assets_status_idx (status)`、`assets_tags_gin USING GIN(tags)`（设计 §6 tags GIN 检索）。

> M3 追加列：`prescreen_score`（默认 -1 = 未筛）/`prescreen_flags`/`prescreen_note`（`storage.go:183-185`）。
> M4 追加列：`external_job_id`/`submitted_at` + 部分唯一索引 `assets_todo_uniq (todo_id) WHERE todo_id<>''`（`storage.go:210-216`）。
> `assets.Asset` 结构体（`assets/store.go:25-47`）已带这些 M3/M4 字段，但 M2 路径不写它们。

### 资产状态机（生命周期）

精确状态串（**绝非臆测，全部来自代码**）：`generating` → `pending_acceptance` → `accepted` / `rejected` / `failed`（M4 另有中间态 `submitted`）。每个转移的写入点：

| 转移 | 触发处 | 代码 |
|---|---|---|
| 插入 `generating` | fan-out 后 worker 首次落行 | `worker.go:684-687`（`GetOrCreateForTodo`，`Status:"generating"`）|
| `generating → pending_acceptance` | 字节落 blob 成功后 | `worker.go:652`（`SetBlob(...,"pending_acceptance")`）|
| `generating → failed` | 生成/blob put 失败 | `worker.go:626,638,642`（`SetBlob(...,"","","","","failed")`）|
| `pending_acceptance → accepted` | HITL 采纳 | `review/review.go:42-43` → `TransitionStatus("pending_acceptance","accepted")` |
| `pending_acceptance → rejected` | HITL 退回 / 重生成前置 | `review/review.go:47-48`、`:70` |
| 重试复用旧行 `failed/* → generating` | 同步重试 | `worker.go:695-698`（`TransitionStatus(a.Status,"generating")`）|

`SetBlob` 的状态守卫（关键 TOCTOU 仲裁）：`WHERE id=$1 AND status IN ('generating','submitted')`，返回 `won = (rowsAffected==1)`，保证多 worker 竞争时只有一个翻转成功并发 `asset_generated` 事件（F-INT-1，`assets/store.go:151-160`）。`'submitted'` 这个允许来源是 M4 异步路径，M2 image 只用到 `'generating'`。

### generations（`storage.go:147-163`）

```
id, project_id (FK), asset_id, todo_id, kind ('image' 默认),
provider, model, prompt, tokens, image_count, video_seconds,
cost_micros BIGINT (0 默认), latency_ms, created_at
```

索引：`generations_project_idx (project_id, created_at)`。每次 provider 调用一行（设计 §6）。M2 在 `worker.go:662-669` 通过 `Cost.RecordPriced` 写入。

> M4 部分唯一索引 `generations_asset_todo_uniq (asset_id, todo_id) WHERE asset_id<>'' AND todo_id<>''`（`storage.go:219-220`）支撑异步 submit/poll 去重；M2 同步路径不依赖它。

### model_configs（骨架 `storage.go:164-174` + M5 凭据列 `storage.go:238-239`）

```
id, org_id, kind ('image' 默认), provider, model,
enabled (true), is_default (false), params_json JSONB, created_at
-- M5 追加：base_url TEXT, api_key_enc BYTEA
```

索引：`model_configs_org_idx (org_id)`。**API key 不以明文入此表**——只存 `api_key_enc`（AES-256-GCM 密文，含 nonce）。

---

## 3. BYOK 模型配置与加密

### secretbox（AES-256-GCM，`internal/secretbox/secretbox.go`）

- 主密钥来自环境变量 `STUDIO_CONFIG_ENC_KEY`（base64 编码的 32 字节，`secretbox.go:18`）；`NewBoxFromEnv()`（`:53-55`）。
- 未配置 → **disabled box**（`aead==nil`），`Enabled()` 返回 false（`:57-58`）；`Encrypt`/`Decrypt` 直接返回 `ErrNoKey`（`:21,:63-64,:76-77`）。
- `Encrypt` 用随机 nonce，输出 `nonce||ciphertext`（自带 nonce，`:62-72`）；`Decrypt` 反解，GCM 校验失败（篡改）返回错误（`:75-89`）。
- 纯 stdlib（`crypto/aes`+`crypto/cipher`），符合 studio 自有横切的轻量取向。

### model_configs CRUD（`internal/models/store.go`）

`Store` 持 `pool` + `*secretbox.Box`（`store.go:156-163`）。

**写时凭据守卫（两层）：**
1. `params_json` **不得夹带凭据**：`secretKeyIn` 递归扫 key 名，命中 `apikey/secret/password/passwd/credential`（子串）或以 `token`/`_key` **结尾** 的字段 → `ErrSecretParam`（`store.go:38-67`，Create `:182-189` / Update `:243-250`）。
   - 评审修复：裸 `token` **故意不在**子串列表，避免误伤 `max_tokens`/`token_budget`（`store.go:36-38,43-51`；测试 `TestSecretParamMatchingExcludesTokenCounts`）。
2. **专用 `APIKey` 字段是唯一合法 key 入口**：非空则经 box 加密入库；box 未启用 → `ErrEncUnavailable`（**不静默丢弃 key**，`store.go:191-201`）。

**写时不回显 key（write-only）：** `ModelConfig`（返回给客户端的 DTO，`store.go:105-116`）只有 `BaseURL` + `HasAPIKey bool`，**没有 key 字段**。`Create` 把 `HasAPIKey: keyEnc != nil`（`store.go:216`）；`Update`/`List` 用 SQL `(api_key_enc IS NOT NULL) AS has_api_key` 读出布尔（`store.go:293,324`）。

**Update 的 key keep-or-replace 语义**：入参 `APIKey` 空=保留既有密文，非空=重新加密替换。SQL 用 `api_key_enc = CASE WHEN $9 THEN $10 ELSE api_key_enc END`（`replaceKey` 为 `$9`，`store.go:280`）。

**is_default / enabled 语义**：每个 `(org, kind)` 至多一个默认。`Create`/`Update` 在事务里先把同 org+kind 其它行 `is_default=false`（Update 排除自己避免竞态，`store.go:207-212,269-275`）。`ResolveForOrg` 只取 `enabled=true AND is_default=true` 的那一行（`store.go:345-350`）。

**跨 org 隔离**：`Update`/`Delete` 永远按 `(id AND org_id)` 定位，0 行受影响 → `ErrNotFound`（handler 映射 404，`store.go:286-288,314-316`）。

**唯一暴露明文 key 的路径**：`ResolveForOrg`（`store.go:345-370`）—— 解密 `api_key_enc` 填入 `ResolvedModel.APIKey`，**只在服务端运行层（ModelRouter）调用，绝不进 HTTP handler**（`store.go:118-126,342-344`）。box 未启用但行里有密文 → `ErrEncUnavailable`（`store.go:360-361`）。

### Catalog（`store.go:78-101`）

固定内置目录：image（openai gpt-image-1 / dall-e-3、google imagen-3、minimax image-01、volcengine seedream，设计 §13 R3：无 Flux/SDXL/Midjourney）；目录里还混入了 **M4** 的 video/audio 项（fake-*/runway/kling/veo/tts-1，`store.go:85-92`）和 **BYOK text 项**（deepseek/gpt-4o-mini/ollama，`store.go:93-99`）。

---

## 4. 模型路由（ModelRouter）

### 设计：provider-agnostic + factory 注入

`internal/modelrouter/router.go` **不 import 任何具体 provider 包**——构造经 `BuildChat`/`BuildMedia` factory func 注入（这俩 live 在 `cmd/studiod/main.go`），故此包 provider 无关、可单测（`router.go:1-7`）。

`Config`（`router.go:34-41`）：
- `Models resolver`（即 `*models.Store`，仅需 `ResolveForOrg(ctx,orgID,kind)`，`router.go:21-23`）
- `Registry registryDefaulter`（`*generate.Registry`，需 `Resolve`+`Default`，`router.go:26-29`）
- `DefaultChat llm.ChatModel`（env 默认 chat，fallback）
- `BuildChat func(provider, model, apiKey, baseURL string) (llm.ChatModel, error)`
- `BuildMedia func(kind, provider, model, apiKey, baseURL string) (generate.MediaGenerator, error)`

### 缓存策略：无缓存（每次现构）

`New` 注释明确：**「Building a provider client per call is acceptable for now (low volume) — no cache」**（`router.go:53-54`）。无 cache key/身份。这是 M2 的故意取舍（低流量），未来可加。

### ChatModelFor 解析算法（`router.go:74-93`）

```
models==nil                          → DefaultChat
ResolveForOrg(org,"text") err        → 记 warn，DefaultChat
!ok || APIKey=="" || BuildChat==nil  → DefaultChat
BuildChat(provider,model,key,baseURL) err → 记 warn，DefaultChat
否则                                 → 新构 BYOK chat model
```
**永不返回「有意义的 nil」**：任何 miss/err/build 失败都回落 `DefaultChat`（调用方依赖可用模型；若 `DefaultChat` 也 nil 则调用方自己处理——router 不臆造）。

### MediaGeneratorFor 解析算法（`router.go:99-134`）

```
models==nil || registry==nil         → registry.Default()（或 nil）
ResolveForOrg(org,kind) err          → 记 warn，registry.Default()
!ok（org 无该 kind 配置）            → registry.Default()（新 org 也能跑）
有 per-config key && BuildMedia!=nil  → BuildMedia(kind,provider,model,key,baseURL)
  └ build 失败                       → 记 warn，落到下一步
配置有但无 per-config key（或 build 失败）→ registry.Resolve(provider,model)（env-keyed adapter，保留 M3 路由）
  └ 无注册 adapter                   → 记 warn，registry.Default()
```
即三级回落：**BYOK 现构 → env-keyed registry adapter → registry default**。

### K5 如何被尊重（capabilities per provider×model）

关键 keystone：**能力是 per-(provider × model)，provider 实例在构造时绑定一个 model**。Router 本身不感知能力，K5 在 **factory 注入** 处兑现——`buildChatFactory`/`buildMediaFactory`（`cmd/studiod/main.go`）按 `provider` switch，**用 `WithModel(model)` + `WithAPIKey(key)` option 构造**，每个构造出来的 provider 实例就绑定了那个 model（其 `Info().Capabilities` 反映 THAT model）。Router 把 `(provider, model)` 当作不可分的解析键（`generate/registry.go:21` `key = provider+"/"+model`），从不只按 provider 解析。

`buildChatFactory`（`main.go:601-641`）switch：`openai`/`openai-compatible`、`deepseek`、`google`、`ollama`，每分支 `provider.New(WithModel(model), WithAPIKey(apiKey), [WithBaseURL(baseURL)]...)`，末尾 `obs.WrapModel(m, tp)`（otel 装饰，K3）。
`buildMediaFactory`（`main.go:649-700`）按 kind 三分支：image（openai/google/minimax/volcengine → `genimage.New(ig, nil)`）、video（runway/kling/veo，**M4**）、audio（openai tts，**M4**）。

### 路由序列（asset 生成时）

```
worker.runAsset
  └ Router!=nil:
      orgID = Projects.OrgIDForProject(projectID)            worker.go:549
      routed = Router.MediaGeneratorFor(orgID, kind)         worker.go:550
          └ models.ResolveForOrg(org,kind)  (解密 BYOK key)
          └ BuildMedia / registry.Resolve / registry.Default
  └ chat（storyboard 用）: routedChatModel → Router.ChatModelFor(orgID)   worker.go:797-805
```
路由错误**故意非致命**——「routing must never break generation」（`worker.go:540-543`）；解析不到就回落默认 generator。

---

## 5. 生成适配层（generate）

### 单一外部触点

设计 §99 单一职责：「`generate` 是唯一对接外部生成 API 处」。包注释（`generate/generate.go:1-5`）重申。AssetAgent 调 `MediaGenerator` 而不区分 image/video/audio。

### MediaGenerator 接缝（`generate.go:39-43`）

```go
type MediaGenerator interface {
    Kind() string  // "image" | "video" | "audio"
    Generate(ctx, req GenRequest) (GenResult, error)
}
```
- `GenRequest`（`:12-23`）：`Prompt`（已被 PromptBuilder 注入风格后缀）、`N`、`Size`、`Quality`、`Format`；`DurationSeconds`/`Voice` 是 **M4** 加性字段，image 忽略。
- `GenResult`（`:28-37`）：`Bytes`/`URL`（二选一为主载荷）、`MimeType`、`Provider`、`Model`、`Tokens`、`ImageCount`、`LatencyMS`——后三个喂 generations 账本。

> `AsyncGenerator`/`SubmitResult`/`PollStatus`/`PollResult`/`Canceler`（`generate.go:45-96`）全是 **M4** 异步长任务接缝，M2 image 不实现也不用。

### image 适配器（`internal/generate/image/image.go`）

`New(gen llm.ImageGenerator, puller Puller)`（`image.go:32-41`）包 `contract/llm.ImageGenerator`。两条交付路径（设计 §10「默认拉回落 BlobStore」）：
- provider 直返 inline `Bytes`（openai b64 / google）→ 透传（`image.go:65-68`）；
- provider 返 hosted `URL`（minimax / volcengine）→ **拉回字节**（`image.go:69-81`），使资产统一可寻址。
- 拉取经 **SSRF-safe `fetch.Fetcher`**（`Puller` 接口，nil → 30s 超时 / 32MB 上限 / 仅 `image/*`+`application/octet-stream`，`image.go:18-40`，设计 §12 安全加固）。
- 记 `LatencyMS = time.Since(start)`、`Tokens = resp.Usage.TotalTokens`、`ImageCount = len(resp.Images)`（`image.go:60-63`）；空 images / 既无 bytes 又无 URL → error（`image.go:56-58,69-71`）。

### sibling 库与 provider 绑定

- 包：`github.com/costa92/llm-agent-providers v0.7.0`（`go.mod`），import 别名 `{openai,deepseek,google,minimax,ollama,volcengine}provider`（`main.go:23-28`）。契约：`github.com/costa92/llm-agent-contract v0.5.0` 的 `llm.ImageGenerator`/`llm.ChatModel`。
- **构造时绑定 model**（K5）：`provider.New(WithModel(...), WithAPIKey(...))`。registry 注册时也按 catalog 逐条 key-gated 注册（`main.go:registerImageGenerators`：缺 key 则 `continue`，否则 `reg.Register(provider, model, obs.WrapGenerator(genimage.New(ig,nil), tp))`）。

### registry（`internal/generate/registry.go`）

map key = `provider+"/"+model`（`:21`）。`Register`/`SetDefault`/`Default`/`Resolve`（无匹配回落 default，否则 error）/`Generate`（resolve+run 便捷）。fake：`NewFake(results...)`（游标推进，耗尽报错）、`NewFakeLooping(r)`（单结果复用，适合 fan-out N shots），`Kind()=="image"`（`generate/fake.go`）。e2e 经 fake 注入，**绝不调真图片 API**。

---

## 6. 资产生成端到端数据流

追踪一个 asset todo（fan-out 产出，`type='asset'`, `status='ready'`）：

```
worker.RunOnce → claim (FOR UPDATE SKIP LOCKED + lease)        worker.go:154,191
  └ process → case "asset": runAsset                           worker.go:292-293
       │
       1. 解析 input_json {shotId,shotPrompt,style,kind,duration}   worker.go:511-527
       2. cctx = WithoutCancel(ctx)  ← 失败清理必须扛 CallTimeout   worker.go:536
       3. 模型路由 (BYOK):
            orgID = Projects.OrgIDForProject(projectID)          worker.go:549
            routed = Router.MediaGeneratorFor(orgID, kind)       worker.go:550
       4. 类型断言 AsyncGenerator? (M4 video/audio)              worker.go:575
            image 不是 → 走下方同步单遍路径
       5. 配额回查 (GenQuota>0): CountByOrgSince(24h) ≥ quota → 失败  worker.go:587-599
       6. createAsset → Assets.GetOrCreateForTodo(Status:"generating")  worker.go:603-612,684-687
       7. AssetAgent.RunWith(routed, {ShotPrompt|EditedPrompt, Style})   worker.go:614-623
            └ builder.Build(shotPrompt, style)  ← 注入风格后缀     agents/asset.go:48
            └ gen.Generate({Prompt:built, N:1, Size})            agents/asset.go:49
            └ image 适配器: GenerateImage → bytes 或 拉 URL        generate/image/image.go:48-81
            gerr != nil → SetBlob(...,"failed")  + return err     worker.go:624-628
       8. 落 blob: BlobStoreFor(orgID).Put("assets/<proj>/<id>", bytes, mime)  worker.go:633-644
            put 失败 → SetBlob(...,"failed") + return err          worker.go:641-643
            (out.Bytes 为空 = URL-only fallback, blobKey="")        worker.go:645-647
       9. SetBlob(id, blobKey, url, provider, model, "pending_acceptance")  worker.go:652
            (cctx — 完成的记账不可继承 per-call deadline)
      10. prescreen(ctx, ...)  ← M3 自动预审, advisory, 永不 fail   worker.go:659
      11. Cost.RecordPriced({ProjectID,AssetID,TodoID,Kind,Provider,Model,Prompt,Tokens,ImageCount,LatencyMS})  worker.go:662-669
      12. Events.Append("asset_generated", todoID, {assetId,status:"pending_acceptance"})  worker.go:671
      13. return "asset:<id>"                                    worker.go:672
  └ MarkDone(todoID, outputRef)  → todo done, unblock 后继          (process 后段)
```

fan-out 的来源（`runStoryboard`，`worker.go:457-503`）：storyboard todo 完成时，在写 `shots` 行的**同一事务**里，对每个 shot 调 `Todos.AddDynamic(tx, ...)` 插一个 `type='asset'`、`status='ready'`、`input={shotId,shotPrompt,style,kind:"image",duration}` 的 todo（`worker.go:478-490`）。事务提交后逐个发 `todo_ready` 事件（`worker.go:499-501`）。幂等：重跑 storyboard 先查是否已有 `type='asset' AND $todoID=ANY(depends_on)`，有则跳过（C1，`worker.go:442-455`）。

> 注意：fan-out 写的 `style` 取自 **projects 行**（`worker.go:418-422`），不取 storyboard todo 的空 input（B1 修复，否则整个风格库静默失效）。

---

## 7. 成本记账

### cost_micros 计算（`internal/cost/store.go`）

- `Generation` 结构（`store.go:19-32`）含 `Tokens/ImageCount/VideoSeconds/CostMicros/LatencyMS`。
- **单一计价 chokepoint `RecordPriced`**（`store.go:135-144`）：若入参 `CostMicros==0`，查 `PriceFor(provider,model)`，命中则 `CostMicros = ComputeCostMicros(...)`，再 `Record` 落行。worker 每次生成后调它（`worker.go:664`）。
- `ComputeCostMicros`（`store.go:125-129`）：
  ```go
  int64(imageCount)*p.MicrosPerImage +
  int64(tokens)*p.MicrosPer1kTokens/1000 +   // tokens → 每千归一
  int64(seconds)*p.MicrosPerSecond           // M4 按秒
  ```
- `PriceFor`（`store.go:110-122`）查 **`pricing` 表**：`SELECT micros_per_image, micros_per_1k_tokens, micros_per_second FROM pricing WHERE provider=$1 AND model=$2`；无行 → `ok=false`（不报错），调用方记 `cost_micros=0`（unpriced）。
- `latency_ms` 落库（`generations.latency_ms`）但**不参与计价**。

### M2 / M3 缝（seam）

`pricing` 表与 `RecordPriced/PriceFor/ComputeCostMicros` 的计价逻辑、聚合查询（`ByOrg/ByProject/ByOrgBetween/PerProjectByOrg/RecentByOrg`，`store.go:90-249`）**全部是 M3 的成本中心**（pricing 表在 `m3Migrations`，`storage.go:186-200`；`Price.MicrosPerSecond` 是 M4）。

代码注释自承：「**M2 carry: M2 recorded cost_micros=0 always**」（`store.go:133-134`）。即 M2 只保证 `generations` 账本**每次 provider 调用一行**（含 tokens/image_count/latency）；把 `cost_micros` 算成真实金额是 M3 加 pricing 表后的能力。设计 §15 把「generations 账本」列在 M2、「成本中心聚合」列在 M3——与代码一致。

---

## 8. HTTP 暴露面

路由注册在 `internal/httpapi/httpapi.go`，handler 在 `m2handlers.go`/`handlers.go`。中间件：`authOnly`（仅 `Authenticate`）、`scoped(minRole, scopeFn, h)`（`Authenticate → RequireScopeRole("org", minRole, scope)`），scope 解析器有 `assetScope`（`asset.OrgIDForAsset`）/`projectScope`/`orgScope`。

| 端点 | handler | RBAC | scope | 备注 |
|---|---|---|---|---|
| `GET /api/prompt-styles` | promptStylesHandler | authOnly | — | 风格库 |
| `POST /api/prompt/build` | promptBuildHandler | authOnly | — | 预览增强 prompt |
| `GET /api/model-catalog` | modelCatalogHandler | authOnly | — | 叠加 `ModelAvailable` 运行时可用标记 |
| `POST/GET /api/orgs/{org}/model-configs` | create/list | **admin** | org | BYOK key 加密、不回显 |
| `PUT/DELETE /api/orgs/{org}/model-configs/{id}` | update/delete | **admin** | org | keep-or-replace key；404 跨 org |
| `GET /api/orgs/{org}/assets` | libraryHandler | viewer | org | 过滤 project/type/status/style/tag + keyset 分页 |
| `GET /api/assets/{id}` | getAssetHandler | viewer | asset | 含版本血缘 |
| `GET /api/assets/{id}/content` | assetContentHandler | viewer | asset | **302 重定向到签名 URL**（不代理字节）；外链资产直接 302 到 provider URL |
| `GET /api/blob/{key...}` | blobHandler | **无鉴权** | — | localfs HMAC 校验：query `exp`+`sig`，`Verify` 后回源磁盘，设 nosniff + sandbox CSP |
| `POST /api/assets/{id}/accept` | acceptHandler | **admin** | asset | 非 pending → 409 |
| `POST /api/assets/{id}/reject` | rejectHandler | **admin** | asset | 非 pending → 409 |
| `POST /api/assets/{id}/regenerate` | regenerateHandler | **admin** | asset | body `{prompt}`；配额超限 429；返回 `{newAssetId,todoId,status:"generating"}` |

`ModelAvailable`（Deps 字段 `func(provider, kind string) bool`）：catalog handler 用它给每个目录项打 `available` 布尔——key-gated（缺 provider key 则不可用），nil → 全可用（测试模式）。它**不是**阻断 run 的硬门禁，只是 UI 提示；M2 的「无配置也能跑」靠 registry default 兜底（见 §4）。

> `GET /api/orgs/{org}/cost`、`/projects/{id}/cost`、`/cost/projects`、`/generations`（成本中心聚合，admin）是 **M3**；`/api/orgs/{org}/tasks`（跨项目任务看板）是 **M4**；`/api/platform/*` 与 `/storage-config` 系列分属 M3/M6。

签名 URL 直连而非代理（设计 §10/§29）：localfs `SignedURL` 产 `/api/blob/{key}?exp=<unix>&sig=<hex(HMAC-SHA256(key+"\n"+exp))>`，`blobHandler` 先 `Verify(key,exp,sig)` 再回源（`internal/blob/localfs/localfs.go`），无凭据外泄；S3 用 minio-go presigned GET（`go.mod` minio-go v7.2.0）。

---

## 9. 关键设计取舍 / keystones

- **K5（per-(provider×model) 能力）**：路由键恒为 `(provider, model)`（`registry.go:21`）；provider 实例在 factory 构造时 `WithModel(model)` 绑定（`main.go:601-700`）。Router 本体 provider 无关，靠注入兑现 K5。
- **K3（otel 装饰，非 hook）**：factory 末尾 `obs.WrapModel`/`obs.WrapGenerator`（`main.go:639` 等）。
- **generate 是唯一外部生成触点**：AssetAgent 与 worker 都不直接碰 provider SDK，只过 `MediaGenerator`（设计 §99）。
- **BlobStore 是唯一字节处**：worker 经 `Storage.BlobStoreFor(org).Put`（`worker.go:636`）。
- **BYOK 静态加密 + write-only**：key 经 AES-256-GCM 入 `api_key_enc`，DTO 只回 `HasAPIKey`；明文 key 仅 `ResolveForOrg` 内部可见。
- **签名 URL 直连 vs 字节代理**：`/assets/{id}/content` 302 到短 TTL 签名 URL（10min），不让 BFF 搬字节；localfs 回源 handler 独立 HMAC 校验。
- **路由非致命**：模型解析失败一律回落默认，不阻断生成（`worker.go:540-543`）。

---

## 10. 边界情况与失败处理

- **生成失败 → asset `failed`**：AssetAgent/blob put 任一失败，worker 在 `cctx`（`WithoutCancel`，扛 CallTimeout）上 `SetBlob(...,"failed")`（`worker.go:626,638,642`）；否则行会永久卡 `generating`（I1 修复，`worker.go:529-535`）。
- **缺 / 禁用模型**：org 无该 kind 默认配置 → registry default 兜底（`router.go:112-114`）；选了 model 但无注册 adapter（多半 provider key 没配）→ 记 warn + default（`router.go:127-133`、`worker.go:558-564`）。
- **key 解密不可用**：行有密文但 box disabled → `ResolveForOrg` 返 `ErrEncUnavailable`（`models/store.go:360-361`）；ChatModelFor/MediaGeneratorFor 把它当解析错误回落默认（`router.go:79,107`）。
- **HITL 防重**：accept/reject/regenerate 对非 `pending_acceptance` 资产 → `TransitionStatus` 0 行 → `ErrConflict` → HTTP 409（`review/review.go:51-59,70-76`）。
- **M2 幂等姿态**：
  - fan-out 幂等：重跑 storyboard 查已存在 asset todo 则跳过（C1，`worker.go:442-455`）。
  - 同步重试复用行：`createAsset` 用 `GetOrCreateForTodo`（非 `Create`），复用上次 `failed` 行并 `TransitionStatus → generating`，避免 `assets_todo_uniq` 冲突死循环（BUG#3，`worker.go:678-700`）。
  - **前向引用**：`assets_todo_uniq`（B1）/`generations_asset_todo_uniq`（B3）部分唯一索引是 **M4** 崩溃幂等保障（`storage.go:216,219-220`）。M2 image 同步路径不依赖 DB 级 upsert，靠 `GetOrCreateForTodo` 的应用层幂等。
- **取消竞态**：todo 中途被 cancel，runAsset 可能已 `pending_acceptance` → `discardCanceledAsset` 把 `pending_acceptance/submitted/generating` 扫到终态（`worker.go:331-345,841-866`）。

---

## 11. 测试覆盖

| 包 | 测试文件 | 关键断言 |
|---|---|---|
| storage | `storage_test.go` | `TestMigrateCreatesM2Tables`（assets/generations/model_configs 表 + lineage/检索列存在）|
| secretbox | `secretbox_test.go` | RoundTrip / TamperFails / NonceIsRandom / DisabledBox（ErrNoKey）/ BadKeyLength |
| models | `store_test.go` | Catalog；Create/List；DefaultForOrg；`RejectsSecretParams` 与 `ExcludesTokenCounts`（守卫精度）；`WithAPIKeyHidesKey`；`ResolveForOrgDecryptsKey`；`APIKeyDisabledBoxFails`；Update keep/replace key、scoped-by-org、is_default 清兄弟、404 |
| blob | `fake_test.go` / `localfs/localfs_test.go` | Put/Get/Delete；`SignedURLVerifies`（好签通过、坏签拒绝）；`VerifyRejectsExpired`；`PutBlocksPathTraversal`；`SignedURLEscapesKeyAndRoundTrips` |
| generate | `fake_test.go` / `registry_test.go` / `image/image_test.go` | fake 游标推进/耗尽报错、looping；registry 解析/未知报错/default 回落；adapter Bytes 透传 / URL 拉回字节 / 空 images 报错 |
| prompt | `prompt_test.go` | 7 风格目录、注入后缀、未知/空风格透传 |
| assets | `store_test.go` | CreateAndGet（v1 无 parent）；`RegenerateBumpsVersionWithLineage`（v2 parent=v1）；`SetStatus409Semantics`（守卫）；`LibrarySearchFiltersAndPaginates`；`GetOrCreateForTodoIsIdempotent`（M2 复用）；SetBlobAdvancesFromSubmitted（M4）|
| cost | `store_test.go` | RecordAndAggregateByProject；AggregateByOrg；`PriceForAndRecordPriced`（M3 计价）；范围聚合/per-project/recent；PerSecond+Upsert（M4）|
| worker | `asset_test.go` / `router_test.go` / `worker_test.go` | `RunAssetWritesAssetAndGeneration`（端到端落两行）；`RunStoryboardFansOutAssetTodos` + `FanOutIsIdempotent`；`RunAssetRoutesViaOrgDefaultModelConfig`；**router_test**：`RoutesChatModelViaRouter`（BuildChat 收到 stored config）/`RoutesMediaViaRouterBuildMedia`（BuildMedia 收到 kind+baseURL，BYOK 生效）；同步重试复用行不撞唯一键；失败终态；配额回查；CallTimeout |
| agents | `asset_test.go` | AssetAgent 构 prompt + 生成；传播 gen 错误；RunWith 覆盖 generator |
| httpapi | `m2handlers_test.go` | PromptStyles/Build；ModelCatalog + 可用性；**Accept 409 冲突 / OK**；`BlobHandlerVerifiesSignature`；`CreateModelConfigBYOKHidesKey`；`400OnDisabledBox`；Update 404 / BadRequest / BYOKHidesKey；Delete |

> DB-gated 测试在 `LLM_AGENT_STUDIO_PG_URL` 未设时 `t.Skip`（M2 计划 T1 step8）；纯逻辑测试（secretbox、blob HMAC、prompt、registry、generate fake、image adapter、catalog、版本号计算）始终运行。

---

## 12. 小结

- **M2 = 「图片素材生成 + BYOK 模型路由 + 资产 HITL/库 + 用量账本骨架」**，全部叠在 M1 的 DB 驱动 todo 状态机上，载重决策是 **storyboard 完成时同事务 fan-out N 个 asset todo**（`worker.go:457-503`）。
- **唯一外部触点 = `internal/generate`**，image 适配器把 `contract/llm.ImageGenerator` 的 bytes/URL 两条交付统一成 `GenResult.Bytes`（URL 经 SSRF-safe fetcher 拉回），落入唯一字节处 `BlobStore`。
- **K5 靠 factory 注入兑现**：`ModelRouter` provider 无关，`buildChat/buildMediaFactory`（`main.go:601-700`）按 provider switch + `WithModel` 绑定 model，路由键恒为 `(provider×model)`；解析失败一律非致命回落默认。
- **BYOK 安全姿态扎实**：AES-256-GCM 静态加密、key write-only（DTO 只 `HasAPIKey`）、明文仅 `ResolveForOrg` 内部可见、params 凭据守卫、跨 org 按 `(id,org_id)` 隔离、资产经短 TTL 签名 URL 直连不代理。
- **里程碑边界需当心**：`storage.go` 用 `m2…m6Migrations` 分段明确标注；但 `model_configs` 的 BYOK 凭据列实落在 **M5**、`generations.cost_micros` 的真实定价依赖 **M3 的 `pricing` 表**（M2 记 `cost_micros=0`）。这是「设计 §15 把这些列在 M2 / 代码演进把列与逻辑推后补齐」的偏差，分析与排障时应据 migration 段而非里程碑名定位。
- **可疑/未完全确认**：catalog（`models/store.go:85-99`）已混入 M4 video/audio 与 BYOK text 项，是渐进式叠加而非 M2 纯净态；ModelRouter「无缓存、每次现构」是显式 M2 取舍（`router.go:53-54`），高流量下需复核。
