# 工作流 v2 · B（P5）：字段级 varBindings 设计

> 状态：设计稿（单 spec，待二轮对抗式评审 + codex 二审）。父 spec = `docs/superpowers/specs/2026-06-24-workflow-v2-n8n-nodecentric-design.md`（n8n 节点中心重设，S-2/S-3 安全不变量来源）；前置薄片 = `docs/superpowers/specs/2026-06-25-workflow-v2-p4-pwrite-authoring-design.md`（P-write，节点 `parameters` 写路径，已落 main）。本 spec 覆盖 **B = 字段级 varBindings**：让一个节点的 `{{name}}` 绑定不再只能绑「整个上游节点输出」，而能可选地绑「上游节点输出的某个字段」，字段候选来自上游节点类型声明的 `OutputSchema`（自动补全）。
>
> **载重前提（已核实，地基）**：
> 1. **字段级访问结构性依赖 ExprChannel ON。** 运行期变量解析有两条通道：`resolveVariablesExpr`（`internal/worker/expr_resolver.go:147`，经 expr 引擎 `$node["id"].json.<accessor>`）与 legacy `resolveVariables`→`resolveOutputText`（`worker.go:1776`/`2098`，返回**整段文本字符串**，无 item JSON 结构）。**legacy 通道拿不到结构化 JSON，物理上无法做字段访问。** 故 B 只在 `ExprChannel`（`config.go:55`，env `STUDIO_EXPR_CHANNEL=1`，默认 OFF，可逆）ON 时生效。这是本 spec 的**核心设计 fork**（§5）。
> 2. **OutputSchema 已端到端就位**（与父 spec 旧假设不同，须更正）。`nodedesc.NodeTypeDescription.OutputSchema`（`types.go:38`/`47-51`）已声明（`builtin.go`：studio.script `{title,logline,characterSheet,scenes}` `:28-33`、storyboard `{shotNo,description,narration}` `:51`、prescreen `{score,flags,note}` `:80`、llm `{text}` `:90`）；`nodeTypesHandler`（`nodetypeshandlers.go:53-60`）原样回传（`customFromRow` `:73` `d := base` 连 OutputSchema 一并拷给 custom:* 节点）；前端 `NodeTypeDescription.outputSchema`（`nodeDescTypes.ts:60`）已镜像。**所以 §6 的"OutputSchema→前端"工作量远小于预期——线缆已在，只缺把"上游节点的 type"喂给属性面板。**
> 3. **expr 引擎已支持 `$node["id"].json.<field>`**（P2b）。`evalMember`（`eval.go:94-124`）对 `map[string]any` 取键，**缺字段 → 返回 error**（`eval.go:112` `"field %q not found"`），即字段不存在 = fail-closed 报错，绝非静默空串（`expr.go:43` 注释明示）。

---

## §1 背景与目标

### 1.1 B 交付什么

今天一个节点的 `varBindings` 只能把模板令牌 `{{name}}` 绑到**整个**上游节点的输出：`{name, sourceNodeId}`（TS `types.ts:122`、Go `planner.CustomVariable` `planner.go:180-184`、UI `PropertiesPanel.tsx:186` `patchVarBinding` + 绑定行 `:357-397`）。运行期这会解析成 `{{ $node["<todoId>"].json.text }}`（文本型）或 `.json`（其它），accessor 由 `exprNodeAccessor`（`expr_resolver.go:174-190`）按上游 `node_outputs.format` 推断。**没有办法绑到上游输出的某个具体字段。**

**B = 给每条 varBinding 增加一个可选的「目标字段」**，字段候选来自上游节点**类型**声明的 `OutputSchema`（下拉/自动补全），运行期生成 `{{ $node["<todoId>"].json.<field> }}`。这是「现状（整节点绑定）」与「完整自由表达式编辑器（n8n 全量）」之间的中间地带。

成功标准（一句话）：在 ExprChannel ON 的环境里，画布上选中一个 typed 节点，给它的某个 `{{name}}` 选一个上游节点 **再选该上游节点 OutputSchema 里的一个字段**，保存、运行，该 `{{name}}` 解析为上游输出的那个字段值（而非整段文本）；不选字段 = 与今天逐字节一致（整输出）。

### 1.2 为什么是现在

P-write（P4）刚打通节点 `parameters` 写路径；expr 引擎（P2b）与 ExprChannel（P3）已就位；OutputSchema 线缆已端到端（§1 前提 2）。字段级绑定是「节点自配置 + 表达式」语义的一个**最小增量**：复用既有 `$node` 解析与既有 OutputSchema，只加一个 `SourceField` 维度。它**不是** B-A6 设想的完整表达式自动补全 UI，是其前驱薄片。

### 1.3 与 P-write 的关系

B 与 P-write 正交但同处 varBinding/resolve 子系统。P-write 写的是节点 `parameters`（注册表参数 per-node 覆盖）；B 写的是节点 `varBindings[].sourceField`（变量绑定的字段维度）。两者都住在 `WorkflowNode` 信封、都经画布 round-trip 保存（P-write-3 已修 `toStudioNodes` 全节点透传，`canvasModel.ts`，故 `sourceField` 这个新键天然被 preserve-unknown 透传，**B 无需再改 `toStudioNodes`**）。

---

## §2 范围 / Non-goals

### 2.1 范围

- 后端：`planner.CustomVariable` 加 `SourceField`（`planner.go:180`）；两遍 rewrite（`planner.go:333-337` pass1、`:399-410` pass2）携带它；worker `customVariable` 加 `SourceField`（`worker.go:1609`）；`resolveVariablesExpr`（`expr_resolver.go:147`）按 `SourceField` 构造 `.json.<field>` accessor；**注入闸**（§8.1，charset 硬校验）；legacy 通道对非空 `SourceField` **fail-closed**（§5）。
- API：把 `ExprChannel` 状态作为只读 capability 暴露给前端（§5.3）。
- 前端：`upstreamNodes` 多带上游节点的 `outputSchema`（由画布层从已有 `nodeTypeDescs` 目录解析，`WorkflowCanvas.tsx:1016`/`228`）；`PropertiesPanel` 绑定行加字段选择器（`PropertiesPanel.tsx:357-397`）；`patchVarBinding` 扩成 `(name, sourceNodeId, sourceField)`；TS `varBindings` 类型加 `sourceField`（`types.ts:122`）。

### 2.2 Non-goals（明确不做）

- **翻转 ExprChannel。** P3 翻转是用户的运维决策，必须保持可逆（父 spec）。本 spec 不假设、不依赖、不修改翻转开关本身——只读取其状态。
- **完整自由表达式编辑器 / 多级嵌套字段 / 数组下标 / `$node[...].json.a.b[0]`。** B 只做**单层** OutputSchema 顶层字段。深层路径属后续（虽然 expr 引擎本身支持，UI/校验不在本片）。
- **OutputSchema 的运行期真实形状对齐（D-6 typed-items 流）。** OutputSchema 是**声明契约**；运行期 `node_outputs.items[0].json` 是否真带该键，对内置 script/storyboard 的 characterSheet 等仍属 P3 数据模型范畴（父 spec/P4 §2.2 Non-goal）。本片**不保证**声明字段一定有运行期值——但缺字段 fail-closed（§1 前提 3 + §8.3），是**响亮失败**而非静默错值。见 §10 风险 R1。
- **修改 secret 解析 / `{status}` 守卫 / S-2 节点作用域。** 字段访问只在**已授权**的上游 item JSON 上加一个 `.member` 步，不拓宽节点可达集（§8.2）。

---

## §3 数据模型变更

### 3.1 `CustomVariable` 加 `SourceField`（Go + 运行期 + TS）

`planner.go:180-184` 的 `CustomVariable` 今天 = `{Name, SourceNodeId, SourceTodoId}`。加一字段：

```go
type CustomVariable struct {
    Name         string `json:"name"`
    SourceNodeId string `json:"sourceNodeId,omitempty"`
    SourceTodoId string `json:"sourceTodoId,omitempty"`
    // SourceField (B/P5) 可选：上游节点输出的目标字段名。空 = 绑整输出（=今天行为，
    // accessor 仍由 exprNodeAccessor 推断 .json.text / .json）。非空 = .json.<field>。
    // 必须匹配安全标识符正则（§8.1 注入闸）；候选来自上游类型 OutputSchema（§6）。
    SourceField string `json:"sourceField,omitempty"`
}
```

worker 运行期 `customVariable`（`worker.go:1609-1612`，pass-2 后形态，`{Name, SourceTodoId}`）同步加 `SourceField string json:"sourceField"`。

TS `WorkflowNode.varBindings`（`types.ts:122`）从 `{ name; sourceNodeId }[]` 扩成 `{ name; sourceNodeId; sourceField? }[]`。

### 3.2 持久化与往返

- `sourceField` 住在**节点实例**的 `varBindings`（workflow-local，与 `name`/`sourceNodeId` 同处，**非** org 级注册表）——因为它是 per-binding 创作意图。
- on-disk：`workflows.nodes[].varBindings[]` JSON 数组多一个键，**纯加性**。P-write-3 的 `toStudioNodes` 全节点透传 + preserve-unknown 已保证 `varBindings` 整体 round-trip（B 不再改前端往返层）。
- 老行无 `sourceField` 键 = 合法 = 整输出绑定（`omitempty`，等价今天）。**无数据迁移**。
- PlanCustom 两遍都把 `sourceField` 注入 `input_json.params.variables`（§4.1）。

---

## §4 解析路径（★ 载重）

### 4.1 PlanCustom 两遍携带 `SourceField`（store-thin 不破）

PlanCustom 把 `n.VarBindings` 投影进 `input_json.params.variables`，两处：

- **pass1**（`planner.go:333-337`）：`vars = append(vars, map{"name": v.Name, "sourceNodeId": v.SourceNodeId})` → 追加 `"sourceField": v.SourceField`（非空时）。
- **pass2**（`planner.go:399-410`，CreateGraph 后 local→todo 重写）：`out := map{"name": v.Name}`；置 `sourceTodoId` → 同时 `out["sourceField"] = v.SourceField`（非空时）。

planner 仍不读注册表、不读 OutputSchema（store-thin），`SourceField` 是纯透传字符串。

### 4.2 `resolveVariablesExpr` 构造字段 accessor（★）

`expr_resolver.go:147-167` 今天对每个 var 构造 `tpl := {{ $node["<todoId>"]<accessor> }}`，`accessor` 来自 `exprNodeAccessor`（`:174-190`，按 `node_outputs.format` 选 `.json.text` / `.json`）。改法：

```go
// resolveVariablesExpr 内，per var：
field := strings.TrimSpace(v.SourceField)
var accessor string
if field == "" {
    accessor = w.exprNodeAccessor(ctx, v.SourceTodoId, c.projectID, outputRef) // 默认：今天行为，逐字节不变
} else {
    if !safeFieldRe.MatchString(field) {            // §8.1 注入闸（run-time 边界，权威）
        return nil, fmt.Errorf("worker: variable %q invalid sourceField", v.Name)
    }
    accessor = ".json." + field                     // 字段级
}
tpl := `{{ $node["` + v.SourceTodoId + `"]` + accessor + ` }}`
```

- **默认（field 空）必须字节级等同今天**：仍走 `exprNodeAccessor`，不改其逻辑。这是零回归铁律。
- **member 语法 `.json.<field>`**：expr 引擎 `evalMember` 对 `map[string]any` 取键（`eval.go:109-114`）。字段名经 §8.1 charset 闸后是安全标识符，与 member token 语法（`tIdent`，`parser.go:160-176`）相容。`characterSheet` 等 camelCase 标识符合法。
- **缺字段 = fail-closed**：上游 item JSON 无该键 → `evalMember` 返回 `"field not found"`（`eval.go:112`）→ `expr.Resolve` 返回 error → `resolveVariablesExpr` 返回 error → run 失败（`expr_resolver.go:161-163`）。**非静默空串。**
- `exprNodeProbe`（`expr_resolver.go:84`，shadow 探针，ExprParity gated）可一并感知 `SourceField` 以保探针口径一致，但它纯 log、不影响运行——本片可选改（见 §9 测试 5）。

### 4.3 字段校验：charset 硬闸（必需）+ OutputSchema 成员校验（尽力）

两层，分工明确：

- **charset 硬闸（安全必需，§8.1）**：`SourceField` 必须匹配 `^[A-Za-z_][A-Za-z0-9_]*$`。**这是注入防线**（§8.1），不是 UX 校验。落在**两处**（防御纵深，镜像 P-write 双校验）：plan 期 PlanCustom 绑定校验循环（`planner.go:295-307`，已有 SourceNodeId∈DependsOn 校验，紧邻加 charset 闸，无需 schema）+ run 期 `resolveVariablesExpr`（§4.2，权威最后一道）。
- **OutputSchema 成员校验（尽力 UX，非安全边界）**：保存期可校验 `sourceField` 确在上游类型 OutputSchema 内，否则前端给警告。**但不强制 fail-closed**——理由：OutputSchema 是声明契约、运行期形状可能滞后（§2.2 Non-goal、§10 R1），强制成员校验会误拒「字段已存在于运行期但 schema 未声明」的合法绑定；且 charset 闸已封住注入，成员校验只是补全/防手滑。运行期缺字段已 fail-closed（§4.2）兜底。

---

## §5 ExprChannel 耦合（★ 核心 fork，须人审）

### 5.1 问题

字段级访问**结构性**只能在 ExprChannel ON 时工作（§1 前提 1：legacy `resolveVariables`→`resolveOutputText` 返回整段文本字符串，无 item JSON，无法取字段）。ExprChannel 默认 OFF 且是可逆运维开关。于是「ExprChannel OFF 时如何对待一条带 `SourceField` 的绑定」是必须裁决的 fork。三个选项：

- **(a) 静默降级**：OFF 时忽略 `SourceField`，降级为整节点绑定。→ **致命 footgun**：UI 承诺绑「characterSheet 字段」，运行期却悄悄解析整段文本，**静默错值**。本仓有跨租户写历史，对静默错值零容忍。**否决。**
- **(b) UI capability-gate**：OFF 时前端隐藏/禁用字段选择器，使用户根本无法创作一条 OFF 下不工作的绑定。→ 安全、早发现，但需把 ExprChannel 状态暴露给前端。
- **(c) 把翻转 ExprChannel 当作发 B 的一部分**：→ 越界，P3 翻转是用户运维决策，本 spec 不能假设可翻（Non-goal §2.2）。**否决。**

### 5.2 推荐：(a 的反面 fail-closed) + (b) 组合

**推荐 = run 期 fail-closed（取代静默降级）+ 前端 capability-gate 早发现，且不翻转 ExprChannel。**

1. **运行期 fail-closed（权威）**：legacy `resolveVariables`（`worker.go:1776`）一旦遇到 `SourceField` 非空的 var → **返回 error，run 失败**，带明确信息「字段级绑定需要 expr 通道（STUDIO_EXPR_CHANNEL=1）」。**绝不静默降级为整输出。** worker 是唯一知道 `ExprChannel` 真值的层（`w.cfg.ExprChannel`），故这道闸天然落在 worker legacy 分支，而非 planner（planner 不知 ExprChannel）。
2. **前端 capability-gate（早发现）**：API 把 `exprChannel` 真值作为只读 capability 暴露（§5.3）；前端在 OFF 时**禁用字段选择器并提示**「字段级绑定需开启 expr 通道」。这把「保存后运行才报错」提前到「创作时就拦」。
3. **不翻转**：B 的解析 + 创作链路全部就位，但 B 是否**生效**取决于用户何时翻 ExprChannel——保持其可逆运维决策。

**为何不是纯 (b)**：纯 UI gate 不防「ExprChannel 先 ON、创作了字段绑定、之后翻回 OFF」的回归——那些既有绑定在 OFF 下必须有定义良好的行为。run 期 fail-closed 给出响亮、安全的答案（run 报错，而非错值）。代价：翻回 OFF 会让已有字段绑定的 workflow 跑不动——这是可逆性 × 正确性的可接受代价（§10 R2），且响亮。

### 5.3 capability 暴露

`config.ExprChannel`（`config.go:55`/`:147`）在 `cmd/studiod/main.go` 单点加载、同时喂给 worker（`main.go:326`）与 httpapi server（同进程）。故 API 可读它。最小改动：在 `nodeTypesHandler`（`nodetypeshandlers.go:57-60`）的响应里加一个布尔，如 `{"version":…, "exprChannel": <bool>, "nodeTypes":[…]}`，前端 `useNodeTypes`（`api.ts`）/ `NodeTypesResponse`（`nodeDescTypes.ts:64`）同步加 `exprChannel?: boolean`。无需新端点。

---

## §6 OutputSchema → 前端（线缆已在，仅补"上游 type"）

### 6.1 已就位（更正父 spec 旧假设）

OutputSchema 已端到端（§1 前提 2）：Go 声明 → `nodeTypesHandler` 回传 → FE `nodeDescTypes.ts:60`。**无需新增 OutputSchema 线缆。**

### 6.2 缺口：属性面板不知道上游节点的 type

`PropertiesPanel` 只收到**被选中**节点的 `description`（`PropertiesPanel.tsx:117`，画布层 `nodeDesc` 单数，`WorkflowCanvas.tsx:229-231` 按 `selected.type` 解析）。要给上游节点 X 列字段，面板需要 X 的 OutputSchema，而它只有 `upstreamNodes: {id,label}[]`（`PropertiesPanel.tsx:111`，`WorkflowCanvas.tsx:1016-1022` 由 `rfNodes` 过滤 dependsOn 构造，只取 `{id, label}`）。

**改法（画布层解析，面板保持瘦）**：`WorkflowCanvas.tsx:1016-1022` 构造 `upstreamNodes` 时，对每个上游节点用其 `node.type` 在已有目录 `nodeTypeDescs`（`WorkflowCanvas.tsx:228`，全量 catalog 已在手）里查 description，把 `outputSchema` 一并带上：

```tsx
upstreamNodes={
  selected
    ? (rfNodes as RFNode[])
        .filter((n) => selected.dependsOn.includes(n.id))
        .map((n) => {
          const t = n.data.node.type
          const desc = nodeTypeDescs.find((d) => d.type === t)
          return { id: n.id, label: n.data.node.label ?? n.id, outputSchema: desc?.outputSchema ?? [] }
        })
    : []
}
```

`PropertiesPanelProps.upstreamNodes` 类型扩成 `{ id: string; label: string; outputSchema: OutputField[] }[]`。这样把"按 type 查 OutputSchema"留在已持目录的画布层，面板只消费现成的字段名列表，**不把整个 catalog 线进面板**。

---

## §7 UI 设计

绑定行（`PropertiesPanel.tsx:357-397`）今天每个 `{{name}}` 一行、一个上游节点 `<Select>`。改为**两段**：上游节点 `<Select>`（不变）+ 紧邻一个**字段选择器**：

- 字段选择器候选 = 选中的上游节点的 `outputSchema`（从扩展后的 `upstreamNodes` 取），加一个默认项 **「（整体输出）」value=""**。
- 选中节点但该节点类型 OutputSchema 为空（如 custom http/script 类型未声明 OutputSchema，`builtin.go:100-124` 无 OutputSchema）→ 字段选择器只有「（整体输出）」一项（或直接隐藏字段选择器）。这是正确退化。
- `patchVarBinding` 扩成 `(name, sourceNodeId, sourceField)`（`PropertiesPanel.tsx:186-190`）：`onPatch({ varBindings: [...updated, { name, sourceNodeId, sourceField }] })`。换上游节点时清空 `sourceField`（字段属于具体上游类型，换节点字段失效）。
- **ExprChannel OFF（§5.2 (b)）**：字段选择器 `disabled` + tooltip/提示「字段级绑定需开启 expr 通道」。整体输出绑定仍可用（向后兼容）。
- **安全（§8.2）**：候选**只**来自 OutputSchema 的 `name`——OutputSchema 里**没有** secret 字段（它是输出字段声明，不是参数/密钥），故"绝不列 secret"天然满足。绝不从注册表 params / headers / secrets 列任何东西。

---

## §8 安全模型（★ 载重，须独立安全评审逐字过）

字段级绑定**新增的唯一攻击面**是「用户可控的字段名被字符串拼进 expr 模板」。S-2 节点作用域与 S-3 secret 隔离**不被本片拓宽**——下证。

### 8.1 模板注入（唯一新风险）★

`resolveVariablesExpr` 用字符串拼接构造模板：`{{ $node["` + todoID + `"]` + ".json." + field + ` }}`。`todoID` 是生成的 UUID（安全）；**`field` 用户可控**。若不校验，`field = 'text }} INJECT {{ $node["other"].json.secretish'` 会让 `splitTemplate`（`expr.go:46`）把第一个 span 在注入的 `}}` 处提前闭合、其余文本变成新 span / 字面量——即**模板注入**，可能拼出非预期 `$node` 访问或破坏解析。

**防线 = charset 硬闸（§4.3）**：`SourceField` 必须匹配 `^[A-Za-z_][A-Za-z0-9_]*$`，否则 fail-closed 拒绝。安全标识符不含 `}`/`{`/`"`/`[`/`.`/空格，注入面归零。**双处校验**（防御纵深，镜像 P-write §6）：
- plan 期：PlanCustom 绑定循环（`planner.go:295-307`，无需 schema）。
- run 期：`resolveVariablesExpr`（`expr_resolver.go`，权威最后一道，挡住绕过画布/plan 的脏 JSON 直写 `workflows.nodes`）。

run 期是硬要求：编辑者可直接 `PUT`/INSERT 脏 `workflows.nodes`（父 spec/P4 §6.2 三写入端 + backfill 都可能绕过 plan/save 校验），故执行那一刻的字段名必须在 worker 内重校验。

### 8.2 节点作用域不被拓宽（S-2）

`$node["id"]` 解析经 `exprNodeResolver`（`expr_resolver.go:200-230`）：`NodeByID` 闭包断言 `id` ∈ 执行节点的**直接 depends_on** 且 `itemsForDep` **project-scoped**（`:204-227`）。字段级绑定**只**在 `SourceTodoId`（已是 dependsOn 内、已授权的上游 todo）的 item JSON 上加一个 `.member` 步——**不改 `$node["id"]` 的 id，不拓宽可达节点集**。即字段访问**严格收窄**在一个已授权节点内部的字段路径上，不可能跨到未授权节点。`SourceTodoId` 仍由 PlanCustom pass2 从 `SourceNodeId`（已校验 ∈ DependsOn，`planner.go:303-305`）映射而来——`SourceField` 不参与节点选择。

### 8.3 secret 不可达（S-3）

- 字段候选源**只**是 OutputSchema 字段名（§7）；OutputSchema 不含 secret。
- 运行期 item JSON（`node_outputs.items`）是上游节点的**输出**，secret 解析是 worker 内、对**注册表作者模板**的独立 pre-pass（`worker.go:1908-1943`），secret 明文**从不**落进 item JSON / node_outputs（http secret-bearing 还有 `{status}` 守卫 `worker.go:1974`）。故 `.json.<field>` 无论 field 为何都够不到 secret——它只能取上游已落库的非 secret 输出字段。
- 字段名缺失 → fail-closed 报错（§4.2），不泄露任何内容（expr error 不含值）。

### 8.4 安全评审门禁

合入前须独立安全评审，含：
- **注入回归**：`sourceField` 含 `}`/`"`/`.`/空格/`{{` → plan 与 run 两处都 fail-closed 拒绝；脏 JSON 直写 `workflows.nodes` 绕过画布 → run 期 charset 闸拒绝。
- **作用域 parity**：`sourceField` 无法令 `$node` 触达 dependsOn 之外 / 跨 project 节点（S-2 不变）。
- **secret 不可达**：构造一个 secret-bearing 上游 http 节点，下游字段绑定无法经任何 `sourceField` 取到 secret（只 `{status}` 可读）。
- **缺字段 fail-closed**：绑定声明的字段在运行期 item 不存在 → run 报错、无静默空串、无错值。
- **ExprChannel OFF fail-closed**：带 `sourceField` 的绑定在 ExprChannel OFF 下 run **报错**（非静默降级整输出）。

---

## §9 测试策略

DB 测试用 fresh PG、`GOWORK=off`、`-p 1`（本仓铁律）。

1. **PlanCustom 透传（Go, DB-backed）**：节点 varBinding 带 `sourceField` → 两遍后 `todos.input_json.params.variables[].sourceField` 存活；`sourceNodeId`→`sourceTodoId` 映射不丢 `sourceField`。空 `sourceField` → 产出与今天逐字节一致。
2. **resolveVariablesExpr 字段 accessor（Go, worker）**：ExprChannel ON + `sourceField="characterSheet"` + 上游 item JSON 有该键 → `{{name}}` 解析为该字段值；上游缺该键 → run fail-closed error；空 `sourceField` → 走 `exprNodeAccessor` 默认（`.json.text`/`.json`），与今天一致。
3. **注入闸双处（安全核心）**：`sourceField` 含 `}`/`"`/`.`/空格 → PlanCustom（plan）与 resolveVariablesExpr（run）都拒；直写脏 `workflows.nodes` 绕 plan → run 期拒。
4. **ExprChannel OFF fail-closed**：legacy `resolveVariables` 遇非空 `sourceField` → 返回 error（非静默整输出），错误信息含「需 expr 通道」。
5.（可选）**exprNodeProbe 口径**：探针感知 `sourceField` 后 class 分类仍稳定（纯 log，不阻断）。
6. **capability 暴露（Go, handler）**：`GET …/node-types` 响应含 `exprChannel` 布尔，反映 `cfg.ExprChannel`；OutputSchema 仍在响应里（防回归，证实 §1 前提 2）。
7. **前端（Vitest）**：(a) 选上游节点后字段选择器列其 OutputSchema 字段 + 「（整体输出）」默认；(b) 选字段 → `patchVarBinding` 写 `sourceField`；换上游节点 → `sourceField` 清空；(c) OutputSchema 空 → 字段选择器退化为仅整输出；(d) capability `exprChannel=false` → 字段选择器禁用 + 提示；(e) OutputSchema 永不含 secret（结构性，断言候选源仅 `outputSchema`）。

---

## §10 分阶段（leaf-first，各独立可测）

排序铁律：后端解析先于前端创作（避免前端写出无人消费的 `sourceField`）；安全闸与解析同步上线。

- **B-1 — 后端 schema 透传 + plan 期 charset 闸（无前端）**。`CustomVariable`/`customVariable` 加 `SourceField`；PlanCustom 两遍携带（§4.1）；PlanCustom 绑定循环加 charset 闸（§8.1）。`resolveVariablesExpr` 暂不改（accessor 仍默认）。纯加性、零回归。*验证：测试 1 + 测试 3 plan 端。*
- **B-2 — run 期字段 accessor + 注入闸 + ExprChannel fail-closed**。`resolveVariablesExpr` 按 `SourceField` 构 `.json.<field>` + run 期 charset 闸（§4.2/§8.1）；legacy `resolveVariables` 对非空 `SourceField` fail-closed（§5.2）。*验证：测试 2 + 测试 3 run 端 + 测试 4。须安全评审门禁（§8.4）。*
- **B-3 — capability 暴露**。`nodeTypesHandler` 响应加 `exprChannel`；FE `NodeTypesResponse`/`useNodeTypes` 加 `exprChannel`。*验证：测试 6。*
- **B-4 — 前端字段选择器**。`upstreamNodes` 带 `outputSchema`（画布层解析，§6.2）；`PropertiesPanel` 绑定行加字段选择器；`patchVarBinding(name, sourceNodeId, sourceField)`；TS `varBindings` 加 `sourceField`；ExprChannel OFF 禁用 + 提示（§5.2/§7）。*验证：测试 7 + 端到端：ON 下选字段→保存→刷新→运行解析为字段值。*

---

## §11 开放问题 / 风险（人审；不臆断）

1. **R1 — OutputSchema 声明 vs 运行期真实形状（内置节点）**：字段选择器会列 studio.script 的 `characterSheet` 等声明字段，但运行期 `node_outputs.items[0].json` 是否真带该键依赖 D-6 typed-items 流（P3 范畴，可能未全到位）。当前缓解 = 缺字段 fail-closed（响亮失败，非错值）。**问题**：是否应在 UI 上把「运行期已验证的字段」与「仅声明的字段」区分（如灰显/标注）？倾向先全列 + 依赖 fail-closed，UX 评审定夺。
2. **R2 — ExprChannel 翻回 OFF 的既有字段绑定**：§5.2 选 run 期 fail-closed，则「ON 时创作了字段绑定、运维翻回 OFF」的 workflow 整体跑不动（报错而非错值）。**可接受**（可逆性×正确性代价，且响亮），但需确认产品认可"翻回 OFF 是破坏性运维动作"。是否需要在翻转前做一次"是否存在字段绑定"的体检告警？留运维评审。
3. **B-fork 本身（§5）须人审拍板**：推荐 = run fail-closed + 前端 capability-gate + 不翻转。这是安全/正确性最优，但意味着「B 在 ExprChannel OFF 的生产环境里创作即被禁用、运行即报错」——即 **B 的价值兑现绑定于用户的 P3 翻转时机**。若产品希望 B「开箱即用」，唯一路径是翻 ExprChannel（§5.1 选项 c），那是独立的、可逆的运维决策，不在本 spec 授权范围。**这是唯一需要产品决策的真 fork——请勿默认，须明确拍板。**
4. **换上游节点时 `sourceField` 清空策略（§7）**：本 spec 定为"换节点即清字段"。另一选择 = 若新节点 OutputSchema 也含同名字段则保留。倾向清空（简单、避免跨类型悬挂字段），待评审。
5. **多层/嵌套字段**（`a.b`、数组下标）：expr 引擎支持，但 B 只做单层 OutputSchema 顶层字段（§2.2）。何时升级到路径选择器属后续 spec，本片不留 UI 口子（charset 闸禁 `.`，故多层须显式后续放开 + 重做注入分析）。

---

## §12 第二轮对抗评审 must-fix 修订（实现须honor；安全核心判 SOUND，缺陷在叙述+错误放置）

第二轮对抗评审（独立 Plan agent，逐条核码）判：**注入向量真实但 ASCII-anchored charset 闸完全封堵**（Go `regexp` 的 `$`=`\z` 不在尾随 `\n` 前匹配、引擎 identifier lexer 亦纯 ASCII，闸与 tokenizer 一致无 unicode 失配；`}`/`{`/`"`/`[`/`.`/空格 全在字符集外；即便通过闸的保留名如 `.json.JSON` 也只是失败闭合的 member lookup `eval.go:111-113`）；run 期闸是权威末线；S-2 作用域证明未拓宽（`$node["id"]` 选择来自 string literal、`.field` 是 `NodeByID` 返回**已授权 item** 之后的独立 member step，无法转向别的节点）；S-3 secret 隔离成立。**实现前须改以下 5 处（叙述/错误放置，非安全致命）**：

1. **§5.2 ExprChannel-OFF fail-closed 放置 + 措辞纠正**：legacy `resolveVariables`(worker.go:1776) 在 `:1779` 对空 `SourceTodoId` 先 `continue`；fail-closed 的 `if SourceField != "" → error` 须放在该 guard **之前**（或独立于它），否则空 SourceTodoId + 非空 SourceField 的绑定不报错就溜过。**且纠正「run 期报清晰提示」的claim：错误对 http/script kind 被映射成 opaque `errRequestFailed`(worker.go:1885-1887)，只有 LLM kind 逐字透传(worker.go:1812-1814)**——故「字段级绑定需开启 expr 通道」提示只在 LLM 出现。**前端 capability-gate 才是主 UX 守卫**（spec 须把它定为 primary，run 期 fail-closed 是安全兜底非 UX）。
2. **§8.1 plan-bypass 写面重标**：能带未经 plan-gate 的 `sourceField` 到 worker 的不是 `workflows.nodes`（PlanCustom 每次重跑会重过 planner.go:295-307 闸），而是**直写 `todos.input_json.params.variables[]`**（post-plan，re-run 用既有 todos 不重 plan）。run 期闸仍必需，只是 justification 文字要改对。
3. **§4.2 whitespace-only sourceField**：`strings.TrimSpace` 后 `""` 会**静默退化成整节点绑定**——违背本 spec 自己的 no-silent-degrade 立场。**改为拒绝**（非 trim-to-whole）。低危（UI 不产此值）但须一致。
4. **§4.2 / 探针**：`exprNodeProbe`(worker.go:1821/1899，gated on **另一个** ExprParity flag) 自建 `$node[...]<accessor>` 模板(expr_resolver.go:111)。当前安全仅因 run 在到达探针前已于 worker.go:1812-1814 abort（gating resolver 先返错）——**此乃未言明的 ordering 不变量**。must-fix：探针**保持不动**，或若改动则**同样套 charset 闸**；无论如何在 spec 记录「run aborts before probe」不变量。
5. **§8.1 plan-time 闸独立于 SourceNodeId**：现 `planner.go:299-306` 对空 `SourceNodeId` 先 continue；charset 闸若加在该 loop 内会跳过空 SourceNodeId+非空 SourceField 的绑定（inert 不可利用，但为清晰+防回归，**闸 SourceField 独立于 SourceNodeId**）。

额外（非 must-fix 但真工作量）：**capability handler 须改签名**——`nodeTypesHandler(s CustomNodeTypeStore)`(nodetypeshandlers.go:20) 今无 config 入参，§5.3 要把 `cfg.ExprChannel` 穿进构造器 + router 接线（可行低危，但比「map 加个 bool」多）。

**评审终判：上述 1/2 + §8.1 修订后 implementation-ready；安全机制正确，但 §5/§8.1 原文 justification 会误导实现者与安全评审，须先改。**
