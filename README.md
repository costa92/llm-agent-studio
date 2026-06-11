# llm-agent-studio

AI Studio — Multi-Agent 内容生产平台（生态 sibling 案例项目）。

## 开发

独立 sibling 仓，所有 Go 命令需 `GOWORK=off`：

```bash
GOWORK=off go test ./...
```

提交时 replace-guard 钩子会自动剥离本地 `replace`（如已安装生态钩子）。

## 里程碑

- **M1 (v0.1.0)**：文本管线骨架 —— project + LLM Planner（动态规划 + 校验/回落）+ todo 图 worker（租约队列）+ Script/Storyboard agent + SSE 时间线 + authz/RBAC + otel。
- **M2 (v0.2.0)**：图片生成 + 人工审核 + 资产库 —— BlobStore（localfs HMAC 签名 / S3 minio）+ MediaGenerator（image 适配 contract/llm.ImageGenerator）+ PromptBuilder 风格库 + AssetAgent + 按 shot 扇出 + HITL 采纳/退回/重生成（admin）+ 资产版本血缘 + 资产库检索 + 用量账本 + model_configs。
- **M3 (v0.3.0)**：准生产横切 —— 成本中心（pricing 表真实计价 + 时间范围/按项目/明细聚合）+ 模型路由生效（org 默认 model_config → registry，per-provider key 注册）+ ReviewAgent 自动预审（advisory，HITL 硬门禁）+ 限流/配额（org 24h 生成配额 429 + worker 背书 + 全局并发生成上限）+ 可观测补全（generator/worker span）+ 安全加固（SSRF-safe URL 回拉 / localfs 签名 URL PathEscape / model_configs 密钥字段拒收）+ 取消语义（在途资产终态化）+ docker-compose（postgres+minio+otel-collector+studiod）+ E2E 加固。
- **M4 (v0.4.0)**：异步视频/音频生成引擎（二期 Option A） —— **异步长任务引擎**（submit→poll todo 状态机 + 租约续约 heartbeat + 按 kind 并发隔离 + submit-admission 在途上限）+ **`AsyncGenerator` 接缝**（可选 `Submit`/`Poll` 接口对；image 适配器零改、仍单遍同步；video/audio 走 submit→poll）+ **FakeAsync generator**（sandbox 内零网络确定性活验）+ **key-gated 真实 provider 适配器骨架**（Runway/Kling/Veo/TTS：接口实现 + key-gated 注册 + `// TODO(m5)` HTTP stub，无真实 SaaS 接线）+ **新资产类型 video/audio** + **按秒计费**（`pricing.micros_per_second`）+ 异步账本（submit 预登记 upsert + poll-done 回填）+ SSRF-safe 512MB 拉回 + 孤儿 reaper + 全量复用 M3 横切（otel/quota/SSRF/SSE/authz）。**显式延后**：配音/自动剪辑(ffmpeg)/图片 LoRA/数字人/真实 SaaS HTTP+密钥/流式拉回——见 [docs/m4-deferred.md](docs/m4-deferred.md)。

### M4 异步引擎机制

- **submit→poll 状态机**：长任务拆成多次**短** dispatch——submit dispatch 秒级返回（todo 重排为轮询态），每次 poll dispatch 是一次状态查询（远 < `WORKER_CALL_TIMEOUT`），故 `CallTimeout < Lease` 不变量原样成立，无需放大。
- **租约续约 heartbeat**：`renewLease` 兜底「单次 dispatch 偶尔略长」，作用域绑定 dispatch ctx + 双守卫 `locked_by=$worker AND status='running'`，重排清租约后心跳自然 no-op（闭合 M3 deferred lease-renewal gap）。
- **poll_attempts 独立于 attempts**：轮询预算（`MAX_POLL_ATTEMPTS`，默认 60）与失败重试预算（`MaxAttempts=3`）分列；「成功重排」UPDATE 重置 `attempts=0`，避免正常轮询撞失败上限被误杀。
- **崩溃幂等**：确定性 idemKey = `hash(todoID)` 贯穿 Submit（真适配器转发为 provider client-token，fake 回显）+ asset 按 todo 幂等（部分唯一索引 `assets_todo_uniq` + `GetOrCreateForTodo`）+ submit 单事务（advisory-lock 配额 + SetSubmitted + ledger upsert + reschedule 原子）。reclaim 见 asset 已 submitted+external_job_id → 跳过 Submit、续轮询。
- **两道上限刻意不同性质**：submit-admission 在途上限（`CountInFlightByKind` 数 `submitted` 资产，只在 submit 分支检查、poll 不受限以免死锁）= 限外部在途 job；`MAX_CONCURRENT_VIDEO`/`MAX_CONCURRENT_AUDIO`（claim-SQL 按 kind running 计数）= **软上限**，限本地大文件拉回瞬间并发（OOM 天花板）；org 配额（advisory-lock 串行 submit 准入）= **硬上限**（按秒计费 billing-sensitive）。
- **终态失败分支**：`PollFailed`（provider 明确失败）或 poll 预算耗尽 → `SetAsyncFailed` + `MarkFailed` 立即终态，不走 attempts 退避；transient poll 错（网络抖动）不调 `SetAsyncFailed`（外部 job 还在跑），消耗 poll_attempts 后退避重排。
- **本地取消 + 孤儿 reaper**：cancel 经 submitted→canceled 资产扫描收口 + poll dispatch 检测 canceled todo 即停轮询；reaper 周期把 `submitted` 且 `submitted_at` 过 TTL 的资产终态化为 `failed`。外部 provider Cancel HTTP 延后 M5（`Canceler` 接口可选）。

### M4 配置项（环境变量）

| 旋钮 | 默认 | 说明 |
|---|---|---|
| `POLL_BACKOFF` | `5s` | 异步轮询基础退避 |
| `MAX_POLL_BACKOFF` | `30s` | 轮询退避上限 |
| `MAX_POLL_ATTEMPTS` | `60` | 单资产轮询预算（独立于失败重试 `MaxAttempts`） |
| `MAX_CONCURRENT_VIDEO` | `0`（不限） | 本地 video 拉回并发软上限（OOM 天花板维度） |
| `MAX_CONCURRENT_AUDIO` | `0`（不限） | 本地 audio 拉回并发软上限 |
| `LEASE_RENEW_INTERVAL` | `40s` | 租约续约心跳间隔（强制 < `WORKER_LEASE`） |
| `VIDEO_FETCH_MAX_BYTES` | `536870912`（512MB） | video/audio 结果拉回硬上限（全量入内存） |
| `RUNWAY_API_KEY` / `KLING_API_KEY` / `TTS_API_KEY` | 空 | key-gated 真实适配器；未配 key → 不注册 → 不被解析（Veo 复用 `GOOGLE_API_KEY`） |

### M3 已知限制与决策（后续里程碑处理）

- **worker 租约续约**（M4 已闭合）：M3 以 `WORKER_CALL_TIMEOUT`（默认 90s，强制 < `WORKER_LEASE`）兜底——单次 LLM/生成调用不可能超租约。**M4 已落地 `renewLease` heartbeat**（`LEASE_RENEW_INTERVAL`，作用域绑定 dispatch ctx + 双守卫），闭合此 gap。
- **取消保留 pending_acceptance 资产**（决策）：已花真实成本的待审资产在项目取消后仍可 accept/reject；只有在途 `generating` 资产被终态化为 `canceled`。
- **取消竞态已知窗口**（决策，worker discard 路径代码内有注释）：cancel 恰落在 `SetBlob` 与 `MarkDone` 之间时，`runAsset` 已完整跑完——该次生成成本已入账（有意：钱已实际花出），且 `asset_generated` SSE 事件（status=pending_acceptance）可能先于资产被翻转为 `canceled` 发出，订阅端可能短暂看到随后即被取消的资产。
- **run 内资产状态与账本的顺序窗口**（已知顺序约束，T15 执行中浮现）：`runAsset` 先 `SetBlob` 把资产行写到 `pending_acceptance`，**再** `RecordPriced` 提交 generation 账本行。因此观察到 `pending_acceptance` 的调用方不能假设该次生成的成本/配额账本已更新——配额/成本相对资产状态是**最终一致**的（窗口仅一次 runAsset 内的两步之间）。M3 不改代码，仅记为已知约束。
- **org 级并发 run 上限**：spec §12 提及 org 级并发 run 上限；M3 交付的全局 `MAX_CONCURRENT_GENERATIONS` + org 24h 生成配额已覆盖 M3 的滥用面。M4 补充了 submit-admission 在途上限（按 kind 数 `submitted` 资产）+ 按 kind 本地并发软上限；真正的「per-org 并发 run」按 org 计数记账仍未做（现有 org 24h 硬配额 + advisory-lock submit 串行已覆盖 billing 滥用面）。
- **pricing 无 admin CRUD**（决策）：单价由迁移种子写入，运维经 SQL 调整；成本中心是只读聚合面。
- **blob 生命周期清扫未做**（spec R8）：被拒/孤儿资产与版本增长依赖后续保留策略 + 后台清扫。
- **otel metrics 计数器未做**（决策）：spec §12 范围是 trace wrap + span 属性 + 账本双写，traces-only；metrics SDK 是新依赖、留待真实需求。
- **docker-compose 仅 config 级验证**：沙箱无法拉镜像；`docker compose config -q` 通过，live bring-up 需在能访问镜像源的环境执行。
- **密钥审计基线**：provider/S3 凭据只经环境变量进程内持有；`model_configs.params_json` 拒收凭据形字段（`ErrSecretParam` → 400）；API 响应面（model-configs/catalog/cost）不含任何 key 字段。日志不打印配置对象。

### M4 已知限制与决策（延后项见 [docs/m4-deferred.md](docs/m4-deferred.md)）

- **单文件 > 512MB 无法处理**（spec R10，延后 M5 流式拉回）：video/audio 结果经 `internal/fetch` 全量 `io.ReadAll` 入内存，`VIDEO_FETCH_MAX_BYTES`（默认 512MB）为硬上限，超限拉回失败。**内存天花板 ≈ `MAX_CONCURRENT_VIDEO × 512MB`**，是运维容量规划的输入。流式拉回到 BlobStore（改 `fetch` 为流式模型）延后 M5。
- **best-effort cancel（外部 job 可能仍计费）**：M4 的 cancel 是本地取消（停轮询 + 终态化 submitted 资产）；真正向 provider 发 HTTP 取消延后 M5（`Canceler` 接口当前 no-op 默认）。**已提交的外部 job 可能仍在 provider 侧跑完并计费**——与 M3 已接受的「cancel 与 SetBlob race 时钱已花」语义一致，文档化为已知窗口，不追求强一致。
- **submit-admission 在途上限是软/近似上限**：`CountInFlightByKind` 数 `submitted` 资产存在 count-then-act 的 TOCTOU 窗口（与 M3 并发生成上限同性质）——并行 worker 可能短暂越过精确上限。billing 总额由 org 配额 advisory-lock 串行硬约束兜底；在途上限是滥用面收窄、非硬限。
- **asset-status / ledger 最终一致**：submit 单事务先 SetSubmitted + ledger upsert（在途行立即可被 `CountByOrgSince` 计入避免配额击穿），poll-done 才回填实际秒数/成本（`UpdateGenerationByAssetTodo`，幂等）。观察到 `submitted` 的调用方不能假设该次生成的实际成本已回填——成本相对资产终态是最终一致的（窗口在 submit→poll-done 之间）。
- **TTS 暂以 `AsyncGenerator` 交付（I5 刻意分歧）**：spec §8.2/§4.2 规定短 TTS 走同步 `Generate`，但 M4 为保持异步引擎统一，TTS 骨架实现为 `AsyncGenerator`（submit/poll）。同步短 TTS 路径延后 M5（重启条件：M5 接真实 TTS HTTP 时按 spec 落同步 `Generate` 路径）——详见 [docs/m4-deferred.md](docs/m4-deferred.md)。
- **regenerate video 的 duration 留 0**：重生成路径不精化时长（duration 精化延后 M5）；按秒计费在 regenerate 场景以估算/0 秒入账，poll-done 回填实际秒数。
- **真实 SaaS 适配器仅骨架**：Runway/Kling/Veo/TTS 适配器体内 HTTP 标 `// TODO(m5)`，未配 key → 不注册 → 返回「not configured」错误而非真 SaaS HTTP。真实接线 + 密钥 + idemKey→client-token header 映射延后 M5；M4 全程经 FakeAsync + loopback fetcher 在 sandbox 内活验（零外部 API）。
