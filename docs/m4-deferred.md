# M4 延后项与已知窗口

> 镜像 spec `2026-06-11-ai-studio-phase2-design`（生态 umbrella 仓 `docs/superpowers/specs/`）§10（非目标）+ §11（风险 R10）+ §15.3（I5 决策）。
>
> M4 范围决策（Option A）：**只**交付能被 fake 活验、且对真实 provider 是薄壳的部分；任何需要新计算范式（训练、ffmpeg）或复合编排（配音、数字人）的能力都延后，避免给「准生产案例」引入未验证的重型子系统（CLAUDE.md 第2条）。

## 1. 显式非目标（spec §10）

| 项 | 为何 M4 出局 | 重启需要什么 |
|---|---|---|
| **配音（dubbing）** | 是「TTS + 时间轴对齐 + 混音」的组合流水线，依赖音频接缝先稳 + 剪辑能力 | M4 音频接缝落地后，新里程碑：对齐 + 混音逻辑 |
| **自动剪辑（ffmpeg）** | 非 generator——是后处理（拼接/转码/字幕烧录），需 ffmpeg 二进制 + 计算密集 worker，与「submit→poll 外部 SaaS」模型不同 | 独立「后处理流水线」设计：ffmpeg sidecar/进程池 + 新 todo type `edit` |
| **图片 LoRA** | 是 image 路径的训练/微调扩展（自定义风格权重），需训练编排 + 权重存储，超「调用现成 generator」 | image generator 支持 LoRA 参数透传 + 权重 BlobStore + 训练 job 类型 |
| **数字人（digital human）** | 父文档 §15 已定「评估后或拆独立里程碑」；是视频+音频+口型+驱动的复合系统 | 视频/音频接缝稳定后单独评估，大概率独立里程碑 |

## 2. 真实 SaaS 接线（延后 M5）

| 项 | 为何 M4 出局 | 重启需要什么 |
|---|---|---|
| **Runway/Kling/Veo/TTS 真实 HTTP** | 各家 submit/poll REST 形态不同 + 需密钥 + sandbox 不可达；M4 遵循「不为未验证的外部集成写臆测代码」 | **更新（Phase 2.1）**：原 M4 的必失败骨架适配器（Runway/Kling/Veo/OpenAI-TTS/Hailuo-02 + 体内 `// TODO(m5)`）已**整体下架**（`internal/models/store.go:88` 注释）——真实接线时重新引入 submit/poll HTTP（含 idemKey→client-token header 映射），不再是替换体内 TODO；引擎/接口不动（fake 与真实走同一 `AsyncGenerator`） |
| **外部 provider Cancel HTTP** | 真正向 provider 发取消的 REST 形态各异；M4 本地取消（停轮询 + 终态化 submitted 资产）已收口本地态 | M5 实现 `Canceler.Cancel(ctx, jobID)` 真 HTTP（当前 no-op 默认）；同时收口「已提交 job 仍计费」窗口 |

## 3. 同步短 TTS 路径（I5 刻意分歧，延后 M5）

**分歧记录**：spec §8.2/§4.2 规定短 TTS 应走同步 `Generate`（短音频不必走 submit→poll 的异步开销）。**M4 刻意偏离**：TTS 骨架实现为 `AsyncGenerator`（submit/poll），以保持 worker 异步引擎对 video/audio 的统一分流（单一类型断言 `routed.(AsyncGenerator)`，无 per-kind 特例分支）。

- **为何 M4 这样做**：M4 无真实 TTS HTTP，骨架的形态选择对活验无影响；统一 `AsyncGenerator` 让 worker 引擎代码更简单（CLAUDE.md 第2条：不为 stub 引入分叉路径）。
- **重启条件**：M5 接真实 TTS HTTP 时，按 spec §8.2 落同步 `Generate` 路径（短音频直接同步返回），届时 worker 已有的 `MediaGenerator`（非 async）分支即可承载，无需新增引擎逻辑。
- **更新（Phase 2.1，MiniMax 已收口）**：`internal/generate/audio/minimax.go`（`Generate`，:99）以同步 `MediaGenerator` 落地 MiniMax T2A，且**显式不实现 `AsyncGenerator`**——`routed.(AsyncGenerator)` 断言对它失败，走 sync 分支。即 spec §8.2 的同步短 TTS 路径对 MiniMax 已按 spec 收口；该 I5 分歧对 MiniMax 不再存在（其余 provider 若接入 TTS 仍按此约定落同步 `Generate`）。

## 4. 大文件流式拉回（spec §11 R10，延后 M5）

- **现状**：video/audio 结果 URL 经 `internal/fetch` 全量 `io.ReadAll` 入内存（`fetch.go` 内存模型）。M4 以 `VIDEO_FETCH_MAX_BYTES`（默认 512MB）硬上限 + 按 kind 并发软上限（`MAX_CONCURRENT_VIDEO`/`MAX_CONCURRENT_AUDIO`）兜 OOM。
- **残余风险**：单文件 > 512MB 直接拉回失败；多并发大文件拉回的内存天花板 ≈ `MAX_CONCURRENT_VIDEO × 512MB`，是运维容量规划输入。
- **重启条件**：M5 改 `fetch` 为流式模型，边拉边 `Blob.Put` 落 BlobStore（避免全量入内存）——属独立增强，不动 M4 引擎。

## 5. 已知窗口（M4 文档化，非 bug，与 M3 已接受语义一致）

| 窗口 | 性质 | 说明 |
|---|---|---|
| **submit-admission 在途上限是软/近似上限** | TOCTOU（与 M3 并发生成上限同性质） | `CountInFlightByKind` 数 `submitted` 资产存在 count-then-act 窗口，并行 worker 可能短暂越过精确上限。billing 总额由 org 配额 advisory-lock 串行硬约束兜底；在途上限是滥用面收窄、非硬限 |
| **asset-status / ledger 最终一致** | submit→poll-done 之间 | submit 单事务先 SetSubmitted + ledger upsert（在途行立即可被 `CountByOrgSince` 计入，避免配额击穿）；poll-done 才回填实际秒数/成本（`UpdateGenerationByAssetTodo`，幂等）。观察到 `submitted` 的调用方不能假设实际成本已回填——成本相对资产终态最终一致 |
| **best-effort cancel** | cancel 后外部 job 可能仍计费 | 本地取消停轮询 + 终态化 submitted 资产，但已提交的外部 job 可能在 provider 侧跑完并计费——与 M3「cancel 与 SetBlob race 时钱已花」语义一致，不追求强一致 |
| **regenerate video duration 留 0** | 时长精化延后 M5 | 重生成路径不精化时长，按秒计费以估算/0 秒入账，poll-done 回填实际秒数 |

## 6. 可选加固（spec §11 R6'，未做）

- **org 级「视频生成日上限」独立旋钮**：比通用 `GenQuota` 更严，专门收窄按秒计费的视频瞬时高额风险。M4 未做（现有 org 24h 硬配额 + advisory-lock submit 串行已覆盖 billing 滥用面）；如真实视频用量上线后成本失控，列为优先加固。
