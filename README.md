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

### M3 已知限制与决策（后续里程碑处理）

- **worker 租约续约仍缺**：以 `WORKER_CALL_TIMEOUT`（默认 90s，强制 < `WORKER_LEASE`）兜底——单次 LLM/生成调用不可能超租约。真续约留给 M4 视频长任务（spec R4 的两套机制之一）。
- **取消保留 pending_acceptance 资产**（决策）：已花真实成本的待审资产在项目取消后仍可 accept/reject；只有在途 `generating` 资产被终态化为 `canceled`。
- **取消竞态已知窗口**（决策，worker discard 路径代码内有注释）：cancel 恰落在 `SetBlob` 与 `MarkDone` 之间时，`runAsset` 已完整跑完——该次生成成本已入账（有意：钱已实际花出），且 `asset_generated` SSE 事件（status=pending_acceptance）可能先于资产被翻转为 `canceled` 发出，订阅端可能短暂看到随后即被取消的资产。
- **run 内资产状态与账本的顺序窗口**（已知顺序约束，T15 执行中浮现）：`runAsset` 先 `SetBlob` 把资产行写到 `pending_acceptance`，**再** `RecordPriced` 提交 generation 账本行。因此观察到 `pending_acceptance` 的调用方不能假设该次生成的成本/配额账本已更新——配额/成本相对资产状态是**最终一致**的（窗口仅一次 runAsset 内的两步之间）。M3 不改代码，仅记为已知约束。
- **org 级并发 run 上限未做（延后 M4）**：spec §12 提及 org 级并发 run 上限；M3 交付的全局 `MAX_CONCURRENT_GENERATIONS` + org 24h 生成配额已覆盖 M3 的滥用面；per-org 并发上限需要在 claim SQL 中按 org 计数记账，留待 M4。
- **pricing 无 admin CRUD**（决策）：单价由迁移种子写入，运维经 SQL 调整；成本中心是只读聚合面。
- **blob 生命周期清扫未做**（spec R8）：被拒/孤儿资产与版本增长依赖后续保留策略 + 后台清扫。
- **otel metrics 计数器未做**（决策）：spec §12 范围是 trace wrap + span 属性 + 账本双写，traces-only；metrics SDK 是新依赖、留待真实需求。
- **docker-compose 仅 config 级验证**：沙箱无法拉镜像；`docker compose config -q` 通过，live bring-up 需在能访问镜像源的环境执行。
- **密钥审计基线**：provider/S3 凭据只经环境变量进程内持有；`model_configs.params_json` 拒收凭据形字段（`ErrSecretParam` → 400）；API 响应面（model-configs/catalog/cost）不含任何 key 字段。日志不打印配置对象。
