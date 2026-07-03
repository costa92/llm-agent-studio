# items 通道 cut-over（Phase 1.3 spec）

> 状态：待 owner 批准（spec-first，批准前不动代码）。
> 前置事实：workflow v2 的 items 数据模型（`internal/worker/items.go`）、node_outputs dual-write、
> `internal/expr` 求值、ExprChannel 默认 ON（`internal/config/config.go:146`）均已落地。
> 本 spec 只解决一件事：**把执行期输入解析从「legacy depends_on/output_ref 直读」切换到
> 「loadInputs/itemsForDep 的 items 规范通道」，并最终删除双通道**。
> 它是 Phase 3 编排功能（IF 分支、节点级 onError/retry、partial re-run）的前置：
> 不切换，每个新功能都要在两条通道各实现一遍。

## 1. 目标与非目标

**目标**

- 执行期一切上游输入读取收敛到一条权威通道：`loadInputs`/`itemsForDep`
  （`internal/worker/worker.go:2168` / `worker.go:2191`，项目 scoped、fail-closed）。
- 新 flag `STUDIO_ITEMS_CANONICAL`，接线与默认值策略完全参照 ExprChannel
  （config → main → worker，先默认 OFF，soak 后翻 ON，最后删 flag）。
- 三个 PR 完成，每步独立可回退，零新功能语义。

**非目标（明确不做）**

- 不引入 IF 分支、onError/retry、partial re-run、pairedItem（★D-3）。
- 不发射 asset 二进制 items（BinaryRef 至今从未被发射，`internal/worker/items.go:15-16`；
  `itemsForDep` 对 `asset:` 前缀返回 nil，`worker.go:2249`）——asset 输入形态维持 plan 期
  fan-out 写 input_json（`worker.go:573-577` 写、`worker.go:626-639` 读），不在本次范围。
- 不改 planner、不改 FE 编排语义、不改 node_outputs 的输出侧 dual-write。
- 不改变任何 run 的可观察输出（parity 是硬验收，见 §4）。

## 2. 现状：双通道差异面（实证）

执行期共有 4 类输入消费者，双通道覆盖不对称：

| 消费者 | legacy 通道（现役） | items 通道现状 |
|---|---|---|
| custom llm/http/script 的 varBindings | ExprChannel OFF 时 `resolveVariables`→`resolveOutputText`（裸 id 无项目过滤，`worker.go:1760-1789`、`2134-2157`） | **已权威**：ExprChannel ON（默认）时 `resolveVariablesExpr`→`exprNodeResolver`→`itemsForDep`（`internal/worker/expr_resolver.go:167-201`、`234-263`） |
| storyboard 读上游 script | depends_on JOIN 取 `output_ref LIKE 'script:%'` 最新一条，再直读 scripts 表（`worker.go:488-498`，scripts 读无项目过滤 `:496`）；无父边时回退「全项目最新 script」heuristic（`worker.go:499-505`） | 上游 script 已 emitItems（`worker.go:471`），但 runStoryboard **不读 items** |
| prescreen 读上游文本 | depends_on JOIN 过滤 `script:%/custom:%` 前缀取最新一条→`resolveOutputText`（`worker.go:988-995`） | 上游均已 dual-write items，但 runPrescreen **不读 items** |
| asset | 运行期不读上游输出（输入全部来自 plan 期 fan-out 的 input_json） | 无 items 表达（见非目标） |

`loadInputs` 本身目前只服务测试：`worker.go:2165-2167` 明确注释
"loadInputs is ADDITIVE — execution is NOT yet routed through it"。

**只有 legacy 支持的形态（cut-over 必须逐一给出等价或显式保留）**

1. storyboard 无父边时的全项目最新 script heuristic（M1 兼容，`worker.go:499-505`）。
2. prescreen / storyboard 的「多 dep 中按 updated_at 挑最新单个上游」选择语义
   （`worker.go:490-492`、`989-992`）——`loadInputs` 是全 deps items 顺序拼接
   （`worker.go:2175-2183`），无选择/过滤语义，需要 per-dep 分组变体。
3. 无项目过滤的裸 id 读（`resolveOutputText`，`worker.go:2139-2150`）——这是 S-2 漏洞面
   而非功能，items 通道故意 fail-closed（F1，`worker.go:2186-2190`），**不做等价**。

**只有 items 通道支持的形态**

1. 字段级 sourceField 绑定——legacy 直接 fail-closed 报错（`worker.go:1771-1773`）。
2. 多 item 结构（storyboard 每 shot 一个 item，`worker.go:593-603`）。
3. 项目 scoped + 直接 depends_on 白名单 + 空 items 拒绝（`expr_resolver.go:238-261`）。

**已知 benign 差异**：json 对象经 expr 通道 decode→re-marshal 可能 key 重排/空白归一，
soak 已把「text byte-identical、json 语义相等」定为验收包络
（`internal/worker/worker_expr_soak_test.go:23-52`）。本 cut-over 沿用同一包络。

**parity 测试现状（全部 DB-gated，`LLM_AGENT_STUDIO_PG_URL` + `make test-db` `-p 1`）**

- `TestExprChannel_DifferentialSoak`（worker_expr_soak_test.go:43）：同一执行 flag off/on
  各跑一次断言输出等价 + shadow probe 零 divergent——但只覆盖 custom 三 kind，
  **不覆盖 storyboard/prescreen 输入**。
- `TestLoadInputsReadsItems` / `TestLoadInputsFallsBackToScriptProjection`
  （worker_items_test.go:205/263）：只测 loadInputs 函数与 straddling 回退投影，不测执行路由。
- worker_expr_parity/nodeprobe/channel 测试：探针安全性与 ExprChannel 值源等价。

## 3. 切换设计

**新 flag**：`Config.ItemsCanonical bool`，env `STUDIO_ITEMS_CANONICAL`。
接线复刻 ExprChannel 三点：`internal/config/config.go`（字段 `:54` 旁 + LoadFromLookup `:146` 旁）
→ `cmd/studiod/main.go:324` 旁传入 worker → `internal/worker/worker.go:87` 旁新增字段。
组合约束：`STUDIO_ITEMS_CANONICAL=1` 且 `STUDIO_EXPR_CHANNEL=0` 在 config.Load 直接报错
（items 权威结构性依赖 expr 通道，同 P5 的 fail-closed 原则）。

**三步 PR 序列**

- **PR-A（flag 默认 OFF + 权威路径 + parity 扩展）**
  - `ItemsCanonical` 默认 OFF（`get("STUDIO_ITEMS_CANONICAL","") == "1"`）。
  - ON 分支：runStoryboard / runPrescreen 的输入改走 per-dep items 读
    （新增 `loadInputsByDep` 保留 dep 分组；选择语义「最新单个上游」用 dep 元数据等价重建；
    items 缺失时沿用 `itemsForDep` 的 output_ref 投影回退 ★M-4）。
    storyboard 无父边 heuristic 在 ON 分支**原样保留**（行为不变，删除留给 Phase 3 决策）。
  - custom 三 kind：ON 分支断言并复用现有 expr 通道（已 items 权威，无行为变化）。
  - parity 扩展：differential soak 增加 storyboard-输入、prescreen-输入场景，
    flag off/on 输出等价（text byte-identical / json 语义相等）。
  - 验收：默认 OFF 下现有全部测试**不改动即绿**；`make test` + `make test-db` 全绿；
    新增 soak 场景全绿。
- **PR-B（默认翻 ON + soak）**
  - config 一行翻转（`get("STUDIO_ITEMS_CANONICAL","1") != "0"`）+ config_test 断言翻转
    （对照 `internal/config/config_test.go:93-127` 的 ExprChannel 翻转写法）。
  - 验收：`make test-db`（fresh PG，`-p 1`）全绿；部署后观测窗口 ≥3 天或 ≥N 条真实 run，
    监控零 items 通道特有错误（"no items (denied)"、"not in dependsOn"）。
  - `STUDIO_ITEMS_CANONICAL=0` 为可逆 kill-switch。
- **PR-C（删 legacy 与 flag）**
  - 删除：runStoryboard 的 scripts JOIN 双路、runPrescreen 的 output_ref JOIN、
    `resolveVariables`、`resolveOutputText`（届时无调用者，H-1 约束解除）、
    `ExprChannel` 与 `ItemsCanonical` 两个 flag 及 env、legacy-only 测试
    （worker_expr_channel_test 的 flag-off 用例等）；`cmd/studiod/main.go:413` 的
    FE capability gate 收敛为常 true。
  - **保留** `itemsForDep` 的 output_ref 投影回退（`worker.go:2204-2250`）至少一个部署周期
    ——它是在途 run 的兼容层，不属于「legacy 通道」。
  - 验收：全绿 + grep 无 `resolveVariables|resolveOutputText|STUDIO_EXPR_CHANNEL|STUDIO_ITEMS_CANONICAL` 残留；`docs/architecture/run-flow.md:154`、
    `docs/architecture/subsystem-map.md:169/178` 同步更新。

## 4. parity 保证（每步硬验收）

等价包络沿用 ExprChannel soak 标准（worker_expr_soak_test.go:31-34）：
text 依赖 byte-identical；json 对象依赖语义相等（Unmarshal + DeepEqual）；其余一律视为
真实分歧，阻断翻默认。每个 PR 必须绿：`make vet build test`（无 DB）、`make test-db`
（fresh PG `-p 1`，CI 已有防假绿 skip 断言）。PR-B 额外要求生产/staging 观测窗口证据。

## 5. 风险与回滚

| 风险 | 缓解/回滚 |
|---|---|
| 「挑最新单个上游」选择语义重建出错（最大回归面） | PR-A 的 storyboard/prescreen differential soak 场景 + 默认 OFF；出错翻 `STUDIO_ITEMS_CANONICAL=0` 即回 legacy |
| 切 flag 时在途 run：dep 由旧代码完成、无 items 行 | `itemsForDep` 的 output_ref 投影回退（★M-4）覆盖 script:/shots:/custom:；PR-C 保留该回退 |
| ON 期间产出对 OFF 路径可读性（回退后） | 输出侧 dual-write（content/format + items）本次不动，双向可读 |
| json key 重排影响下游 prompt | ExprChannel 阶段已接受为 benign；包络不扩大 |
| 老项目依赖「全项目最新 script」heuristic | PR-A/B 原样保留该 heuristic，删除不在本 spec 范围 |

每个 PR 独立可回退：PR-A 纯增量（默认 OFF）；PR-B 单行 env/config 回退；
PR-C 是唯一不可 flag 回退的一步，故必须在 PR-B soak 证据充分后才合。
