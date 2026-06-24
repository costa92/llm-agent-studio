# 内置节点扩展:`prescreen`(预审)设计 + 落地

> 状态:设计稿。多 agent 讨论收敛产物(3 提议者 + 1 评审);承接 builtin-node-catalog 分支(数据驱动目录),作为该机制的**首个新内置节点**。

## 概述

新增内置工作流节点 **`prescreen`(预审)**:把现有 `ReviewAgent`(内容安全+一致性评分,当前仅耦合在 asset 生成内自动跑)**节点化**,让用户在工作流里显式编排一个"评分闸门"——对上游文本节点的产出做安全/一致性/侵权风险评分,产出 JSON 评分(0-100)+ 风险标记,下游可读。

多 agent 讨论结论:`prescreen` 是唯一同时满足「与 custom llm 不重复 + 贴合当前文本输出设计 + 低成本」的扩展。被否:`translate/summarize`(与 custom llm 节点纯重复)、`cover`(产二进制 asset、下游不可读、且已有一键 HTTP 端点)、`compose/music`(需 ffmpeg + 二进制输入通道,违反 `resolveOutputText` 对 asset ref 的禁用,另立里程碑)、`narration-safety`(prescreen 的更窄绘本变体)。

## 为何不与 custom `llm` 重复

`runCustomLLM` 让用户写任意 systemPrompt,但 `ReviewAgent` 携带**已绑定、已校验的评分契约**(`reviewSystemPrompt` 固定 rubric + `score 0..100` 范围校验,`review.go:38/84`)与**稳定的 typed 输出** `ReviewOutput{Score,Flags,Note}`。用户手搓 prompt 拿到的是漂移、无结构的输出。prescreen 把一个**真实、已测的领域能力**提升到调色板。

## 输入机制(评审遗漏点,已核实)

内置节点**不走** custom 节点的 `varBindings`/`resolveVariables`——那是 custom 专属。内置节点经**计划结构 + `todos.depends_on` 数组**取上游(`runStoryboard:482-486` 用 `JOIN todos sb ON t.id = ANY(sb.depends_on) WHERE t.type='script'` 找上游脚本)。

`prescreen` 同范式但**类型无关**:取自己 `depends_on` 的、output_ref 可解析为文本(`script:`/`custom:`)的最新上游,`resolveOutputText` 成文本去评分。(v1 只评文本;asset 元数据评审留作后续。)

## 数据流

```
todo(prescreen) claimed (input_json={}, depends_on=[上游文本节点])
  → runPrescreen:
      1. SQL: 经 depends_on 找最新 script:/custom: 上游 output_ref
      2. resolveOutputText → 待评文本
      3. 读 project.style
      4. ReviewInput{Prompt:文本, Style} → routedChatModel ? RunWith : Run → ReviewOutput{Score,Flags,Note}
      5. json.Marshal(ReviewOutput) → INSERT node_outputs (format='json') → "custom:<id>"
  下游经既有 resolveOutputText 的 custom: 分支读这份 JSON 评分(作 {{name}} 变量)
  错误:描述性(无密钥,与 storyboard 同;不需不透明枚举)
```

## 扩展清单(已核实,无新基建/无迁移)

1. **`internal/builtinnode/catalog.go`**:catalog 加一条
   `{Type:"prescreen", Label:"预审", Description:"对上游文本做安全与一致性评分,产出 JSON 评分(0-100)+风险标记,供下游节点读取。"}`。`whitelistedTypes` 与前端 palette **自动派生**,无其它接线。
2. **`internal/worker/worker.go`**:`New()` 执行器 map(`script`/`storyboard`/`asset` 旁,~line 160-173)注册 `"prescreen"` → `w.runPrescreen`;实现 `runPrescreen(ctx, claimed)`(见数据流;`w.cfg.Review==nil` → 清晰报错"prescreen disabled")。复用 `resolveOutputText`/`routedChatModel`/`newID`/node_outputs INSERT,全是既有 helper。
3. **`internal/planner/planner.go`**:**无改动**(prescreen 节点 input_json 走既有 builtin 分支 → `{}`;运行期自解析。不入 LLM auto-planner 词表——仅用户自建工作流,YAGNI)。
4. **前端**:`web/src/features/workflow-canvas/nodeColor.ts` 加 `NODE_COLOR["prescreen"]`(选一主题色,如 `var(--review)` 或新 token)+ `TYPE_LABEL["prescreen"]="预审"`;palette/picker **自动**含它(数据驱动)。更新 `nodeColor.parity.test.ts` 期望(现 4 类型)。
5. **无 DB 迁移**(复用 node_outputs)。

## 落地顺序(TDD,subagent-driven)

- **P0 后端**:catalog 加 prescreen + `runPrescreen` + 执行器注册。测试:catalog 4 条且 whitelist 一致;`runPrescreen` worker 测试(fresh DB)——seed 一个上游 script(或 custom)todo + 其 output_ref,prescreen todo `depends_on` 它,跑 → 断言 node_outputs 落一行 `format=json` 含 `score/flags/note`,且下游 `resolveOutputText` 能读;`Review==nil` → 报错;无文本上游 → 报错。
- **P1 前端**:nodeColor 加 prescreen 色/标签 + parity 测试更新;vitest:palette/picker(mock 目录含 prescreen)渲染该 chip;内置节点只读页自动多一行(目录驱动)。
- **P2 验证**:全量 `build`/`vet`/`test`(fresh DB `-p 1`)+ web vitest/tsc;live 实机:建一个 script→prescreen 工作流跑通,看 node_outputs 评分。

## 测试要点

- catalog 一致性(4 类型;parity 测试 frontend TYPE_LABEL ⟷ 已知 4 标签)。
- runPrescreen:文本上游→JSON 评分落库;下游可读;Review nil / 无上游 → 清晰错误。
- 回归:script/storyboard/asset 执行不变;custom 节点不受影响。

## 非目标

- 评 asset 二进制元数据(v1 只评文本上游;后续可加 asset 分支)。
- 入 LLM auto-planner 默认管线(只供用户自建工作流)。
- 评分阈值自动 gating(下游节点读评分后自行决策,不在本节点)。
