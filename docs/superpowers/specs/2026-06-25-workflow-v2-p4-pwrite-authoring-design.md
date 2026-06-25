# 工作流 v2 · P-write（P4）：可编辑创作写路径 设计

> 状态：设计稿（单 spec，待评审一轮）。父 spec = `docs/superpowers/specs/2026-06-24-workflow-v2-n8n-nodecentric-design.md`（n8n 节点中心重设，两轮评审已纳）。本 spec 只覆盖 **P-write** 这一薄片：让 `<PropertiesForm>` 真正**保存**节点参数、持久化进 on-disk `WorkflowNode` 信封、在 plan/run 期读回。引用父 spec 的发现编号（D-1/D-2、B-A1、S-1/S-7、I4、B-A7、M-3）作为锚点，不重述其论证。
>
> **载重前提（已核实，本 spec 设计的地基）**：P-write 与 **P3 生产翻转完全独立**。`ExprChannel`（`worker.go:1767`/`1840` 的 `w.cfg.ExprChannel` 开关）是**运行期变量解析**层（`{{name}}`→`$node` 通道切换），住在 `internal/worker`。P-write 是**创作期写路径**——把表单值写进 `workflows.nodes` JSON、在 PlanCustom 读出。两者无共享状态：翻不翻 ExprChannel，参数写路径都该独立成立。本 spec 不触碰 `internal/expr` 与 ExprChannel。

---

## §1 背景与目标

### 1.1 P-write 交付什么

父 spec 的相位 P1→P5 中，前四步已合入 main：

- **P1**：只读节点描述（`internal/nodedesc`，已落 `types.go`）+ 通用 `<PropertiesForm>` 渲染；`onChange` 在 typed 节点摘要处接的是 no-op（`PropertiesPanel.tsx:284` `onChange={() => {}}`）。
- **P2a**：items 底座；**P2b**：`internal/expr` 引擎；**P3**：运行期 cut-over + ExprChannel。

P1 落地后**框架已就位但写路径仍断**：`<PropertiesForm>`（`PropertiesForm.tsx:51`）实际已带完整 `onChange`/`patch`/`patchOption` 实现（不是父 spec §5 设想的"纯 no-op 渲染"——P1 实现把表单做成了受控组件），但：

1. 它只在 `PropertiesPanel.tsx:274-287` 的「类型参数（只读）」摘要块被调用，且那里 `onChange={() => {}}`——**值改了没人收**。
2. 收了也无处存：`planner.WorkflowNode`（`planner/planner.go:147`）没有 `Parameters` 字段；
3. 即便存进了内存节点，保存时也会被 `canvasModel.ts:toStudioNodes`（`canvasModel.ts:85-115`）的字段白名单**剥掉**（B-A1）。

**P-write 把这条链打通**：表单 `onChange` → 节点 `parameters` 内存态 → `toStudioNodes` 全节点 round-trip 保存 → `workflows.nodes` JSON 持久化 → PlanCustom 读 `parameters` 解释为 todo 输入 → worker 执行。

### 1.2 为什么是现在

P1-P3 已把"读"侧（描述、渲染、items、运行期解析）全部铺好。唯一缺口是"写"侧：所有 typed 自定义节点今天的可编辑参数仍只能经 `custom_node_types` 注册表（org 级、整类型共享）调，**不能 per-node 覆盖**。P-write 补上 per-node 参数创作，是 n8n「节点自配置」语义的最后一块拼图。

### 1.3 翻转独立性（重申，作为范围边界）

ExprChannel（运行期 `$node` 解析）与本写路径正交。P-write 落地不依赖、不等待、不修改 P3 翻转开关。涉及 worker 的唯一改动是**运行期重校验**（§6 S-1），它在执行入口对解析后的节点参数跑校验器，与变量解析通道无关。

---

## §2 范围 / Non-goals

### 2.1 范围（薄片，typed-custom 优先）

**一句话成功标准**：在画布上选中一个 typed 自定义节点（`custom:<slug>` + `typeId`，kind ∈ llm/http/script），改 `<PropertiesForm>` 里的**非危险**参数，保存，刷新后值还在，运行时该 per-node 覆盖生效，且危险参数被服务端在保存与运行两处校验把守。

落地面：
- 后端：`WorkflowNode` 加 `Parameters json.RawMessage` + `TypeVersion int`（§3）；PlanCustom 读 `Parameters`、按 `(type, typeVersion)` 选描述（§4）；校验器可对 node-parameter 形状调用、运行期重跑（§6）。
- 前端：`toStudioNodes` 全节点 round-trip + preserve-unknown + parity 测试（§5）；`PropertiesPanel` 把 typed 节点的 `<PropertiesForm>` `onChange` 从 no-op 接到真实持久化（§5）。

### 2.2 Non-goals（明确不做）

- **picturebook age-band 级联**（父 spec B-A2/B-A4，`internal/.../pbconfig.go` 的 `ParsePictureBookConfig`）：`DefaultFrom` schema 字段已存在（`nodedesc/types.go:58`/`PropertiesForm.tsx:40-49` `applyDefaultFrom`），但内置 `studio.script`/`studio.storyboard` 节点的级联回填、project-config 退役不在本片。本片只做 typed-custom，内置节点参数写路径留给后续。
- **运行期 characterSheet 跨节点类型化流**（B-A4/D-6）：`$node["script"].json.characterSheet` 依赖内置节点发类型化 items，属 P3 数据模型范畴，不在写路径。
- **`studio.asset` resource locator**（B-A8）：暴露 asset 节点 kind/prompt/voice/duration 为可编辑字段是**行为变更**（撞 storyboard 动态扇出，父 spec I2），不做。
- **表达式创作自动补全 UI**（B-A6 `OutputSchema` 驱动的补全）：那是 P5。本片 `<PropertiesForm>` 里的表达式字段就是纯文本输入。

---

## §3 数据模型变更

### 3.1 `WorkflowNode` 信封加两字段（D-1/D-2）

`planner/planner.go:147` 的 `WorkflowNode` 今天有：`id`/`type`/`promptId`/`promptText`/`dependsOn`/`typeId`/`varBindings`。加两字段：

```go
type WorkflowNode struct {
    ID          string           `json:"id"`
    Type        string           `json:"type"`
    PromptID    string           `json:"promptId"`
    PromptText  string           `json:"promptText"`
    DependsOn   []string         `json:"dependsOn"`
    TypeId      string           `json:"typeId"`
    VarBindings []CustomVariable `json:"varBindings"`
    // 新增（P-write）：
    TypeVersion int              `json:"typeVersion,omitempty"` // 放置/保存时写入当时 description.Version；执行器按 (type,typeVersion) 选描述
    Parameters  json.RawMessage  `json:"parameters,omitempty"`  // schema 化参数值（PropertiesForm value 对象的序列化）
}
```

- `TypeVersion`：对应 `nodedesc.NodeTypeDescription.Version`（`nodedesc/types.go:32`，当前 `const Version = 1`）。放置/保存节点时写入当时描述的 `Version`。执行器据 `(type, typeVersion)` 选描述解释参数——type v1→v2 改字段含义时，老图按 v1 语义读，不静默损坏（D-1）。无该字段的老节点（`omitempty`）= v1 默认。
- `Parameters`：`<PropertiesForm>` 的 `value: Record<string, unknown>`（`PropertiesForm.tsx:11`）序列化。**注意安全分层**（§6）：`Parameters` 只承载**非危险参数覆盖**；危险字段（http `url`/含 secret 的 `headers`/script `code`）**不进这里**，仍留注册表。

### 3.2 on-disk JSON 形状（钉死，含 parity 测试）

typed 节点保存进 `workflows.nodes` 的一行形状（新增键 **追加**，旧键不动）：

```json
{
  "id": "node-3",
  "type": "custom:my-llm",
  "typeId": "a1b2c3...",
  "dependsOn": ["script-1"],
  "position": {"x": 240, "y": 140},
  "varBindings": [{"name": "draft", "sourceNodeId": "script-1"}],
  "typeVersion": 1,
  "parameters": {"temperature": 0.2, "outputFormat": "json"}
}
```

- `parameters` 是 schema-keyed 对象（键=描述里 `Property.Name`）。
- on-disk 信封是**显式交付物**：加 parity 测试（§7）断言一个**未知** Property 经 load→save→reload 存活——这同时压住 B-A1（前端剥字段）与 D-2（值跨版本搁浅）。

### 3.3 迁移 / 兼容（无数据迁移）

- **无 m20 类数据迁移**。本片不回填历史节点（那是父 spec P3 的 m20，含项目配置烘焙——明确 Non-goal §2.2）。
- 老 `workflows.nodes` 行**无** `typeVersion`/`parameters` 键 = 合法。读路径把缺失 `TypeVersion` 当 `1`（默认描述版本）、缺失 `Parameters` 当**空覆盖**（仅用注册表 params，等价今天行为）。
- **为何无需数据迁移（已对 PlanCustom 核实）**：`workflows.nodes` 存的是**图结构 + 创作意图**，不是执行态。每次运行经 `PlanCustom`（`planner.go:270`）**重新规划**——把节点投影成 `todos.input_json` 后才执行。即没有"半执行的存量数据"会因新字段含义变化而损坏：旧行缺 `parameters` → PlanCustom 走「无覆盖」分支 → 产出与今天逐字节一致的 `{kind, params}` input。新字段是纯加性创作元数据，老行天然兼容。
- `Parameters json.RawMessage` 经 GORM/`workflows.nodes` JSONB 往返时遵循本仓 NULL 纪律（`[]byte`→`json.RawMessage` 中转，空当 `nil`/`{}`）；写仍走 workflow 保存的 `INSERT...RETURNING`/JSONB 列既有路径，本片不新增 store 写法。

---

## §4 读路径（先于前端写落地，D-2 排序）

### 4.1 PlanCustom 读 `Parameters` + 按 `(type, typeVersion)` 选描述

`PlanCustom`（`planner.go:270-422`）今天对 typed 节点的读路径（`planner.go:316-331`）是：

```go
if rt, ok := resolved[n.ID]; ok {
    var params map[string]interface{}
    json.Unmarshal(rt.Params, &params)   // rt.Params = 注册表 ResolvedType.Params（org 级，整类型共享）
    params["variables"] = vars            // 注入 VarBindings
    inputMap["kind"] = rt.Kind
    inputMap["params"] = params
}
```

注册表 params（`ResolvedType.Params`，`planner.go:183-186`，由 handler 从 `custom_node_types` 读）是**类型级共享值**。P-write 让节点 `Parameters` 作 **per-node 覆盖叠加其上**：

```go
if rt, ok := resolved[n.ID]; ok {
    // 1) 选描述：按 (n.Type, n.TypeVersion) 取 nodedesc 描述（缺 TypeVersion → v1）。
    // 2) base = 注册表 ResolvedType.Params（危险字段权威来源，§6）。
    // 3) overlay = n.Parameters（仅非危险键；§6 校验器把守）。
    // 4) merged = mergeNonDangerous(base, overlay, description)。
    // 5) merged["variables"] = vars；inputMap["kind"]=rt.Kind；inputMap["params"]=merged。
}
```

- **合并语义钉死**：`overlay` 只能覆盖描述里 `Constraints` 不标危险的 Property 键。危险键（http `url`/`headers`、script `code`）**忽略 overlay、永远取 base**（§6 S-1）。合并函数对每个 overlay 键查描述的 `Constraints`，落入危险词汇即丢弃该键并记审计——fail-closed。
- **描述选择**：`(type, typeVersion)` → 描述。本片只有 v1，但读路径**从第一天起按 typeVersion 选**，给未来 v2 留位（D-1 死元数据反模式的解药）。
- **排序铁律（D-2）**：后端读路径（`WorkflowNode.Parameters` 字段 + PlanCustom 合并 + 校验器可调）**必须先于**前端写它们落地，否则前端写出的值跨发布搁浅在 JSONB 里无人消费。分阶段（§8）据此排序。

### 4.2 内置节点读路径不变

内置节点（script/storyboard/asset，`planner.go:332-358`）的 prompt-precedence 逻辑本片**不动**——它们的参数写路径属 Non-goal。`Parameters` 字段对内置节点为空，PlanCustom 的内置分支照旧。

---

## §5 前端往返契约

### 5.1 `toStudioNodes` 全节点 round-trip + preserve-unknown（B-A1）

`canvasModel.ts:85-115` 的 `toStudioNodes` 今天**逐字段白名单重建**节点（`canvasModel.ts:94-113`：只拷 `id/type/promptId/dependsOn/position`，条件拷 `promptText/label/color/typeId/varBindings`）。代码注释已记录被同款 bug 咬过：

```
// typeId 不是免费透传：toStudioNodes 逐字段重建 WorkflowNode，必须显式拷贝，
// 否则首次画布编辑保存即丢 typeId (T1)。
```

每次画布编辑→保存会剥掉任何不在白名单的字段——`parameters`/`typeVersion` 会被静默丢弃，且老 web 客户端不识别的未来 Property 也丢=数据损坏。

**改法**：`toStudioNodes` 改为 **round-trip 完整节点对象 + preserve-unknown**：

```ts
return rfNodes.map((rf) => {
  const n = rf.data.node
  const dependsOn = rfEdges.filter((e) => e.target === rf.id).map((e) => e.source)
  return {
    ...n,                       // preserve-unknown：透传所有未识别字段
    id: rf.id,                  // 显式覆盖：id 取 RF（重命名权威）
    dependsOn,                  // 显式覆盖：EDGES 是 dependsOn 唯一真源（既有约定，canvasModel.ts:80-83）
    position: { x: Math.round(rf.position.x), y: Math.round(rf.position.y) }, // 取 live RF position
  }
}
```

保留既有两条不变量：`id` 取 RF（重命名级联）、`dependsOn` 从边推导（单一真源）、`position` 取 live 坐标。其余字段（`type`/`promptId`/`promptText`/`typeId`/`varBindings`/`parameters`/`typeVersion` + 任何未知键）经 `...n` 全保真透传。

### 5.2 `PropertiesForm` onChange → persist

`PropertiesForm.tsx:51` 的 `<PropertiesForm>` 已是受控组件（`onChange(next: Record<string, unknown>)`，`patch`/`patchOption` 已实现）。今天 `PropertiesPanel.tsx:274-287` 的「类型参数（只读）」块 `onChange={() => {}}`。

**改法**：typed 节点参数块从「只读摘要」升为「可编辑表单」：
- `value` 仍取节点参数；但优先级=节点 `parameters` 覆盖 ▸ 注册表 typedParams 兜底（与 §4.1 后端合并语义镜像）。
- `onChange={(next) => onPatch({ parameters: next, typeVersion: description.version })}`——把整个 value 对象写进节点 `parameters`、并钉当时描述版本。
- `onPatch` 是 `PropertiesPanel` 既有的 flat-key patch 通道（`PropertiesPanel.tsx:125`/`247`/`188`），写进画布内存节点，随画布保存经 §5.1 round-trip 落盘。
- **危险字段在前端的处理**：`<PropertiesForm>` 对带危险 `Constraints` 的 Property 仍渲染（UX 提示/镜像），但 §6 的服务端校验器是边界——前端写了危险覆盖，保存端 reject、运行端忽略。前端校验只是 UX，不是安全边界（B-A7）。

### 5.3 query-key / 缓存

`<PropertiesForm>` 的描述源自 `GET /api/orgs/{org}/node-types`（P1 已落，前端 `api.ts` + query-key），本片不改其缓存契约。

---

## §6 安全模型（S-1 registry-only + 双处校验，★ 载重）

> 这是本片唯一改变现有安全不变量的地方。父 spec §3.7 设想"把危险参数移上节点"——本片**不那么做**，采 S-1 锁定的 **registry-only + dual-validate** 取舍。本节须被独立安全评审逐字过。

### 6.1 现状安全不变量（已核实）

危险字段今天**编辑者从不直接控制**：

- worker 绑定的危险参数来自 **org-scoped `custom_node_types` 注册表**，保存期 `customnodetype/store.go:80-155` 的 `validate`→`validateHTTPParams`/`validateScriptParams` 校验**一次**（http url 必静态字面量 `store.go:119`；`{{secret:}}` 仅 header、禁 body `store.go:122`；script 禁 `{{secret:}}` `store.go:148`；method/outputFormat 枚举）。
- 节点只携 `TypeId`（引用，`planner.go:160`）+ `VarBindings`（且 `SourceNodeId` 必在 `DependsOn`，`planner.go:287-298`）。注册表 params 经 `ResolvedType.Params` 流入 PlanCustom，**编辑者 JSON 不触碰危险字段**。
- worker 运行期**不重跑** `validate*`，只做散点内联再确认（runCustomHTTP body `{{secret:}}` 复检 `worker.go:1908`、url `{{` 残留复检 `worker.go:1912`、`secretBearing && !allowResponseBody` → 只存 `{status}` `worker.go:1932-1936`）——这些今天足够，**纯因危险参数从未由编辑者 JSON 来**。

### 6.2 P-write 引入的威胁

`Parameters json.RawMessage` 住在 `workflows.nodes` JSON——**编辑者可控**。两条攻击路径：
1. 画布保存任意 `parameters`。
2. **直接 `PUT workflows.nodes` JSON 绕过画布**（编辑者是能改 workflow 的最低权限角色）。

若 P-write 让 `parameters` 覆盖危险字段，编辑者即可把 http 节点 `url` 设 `http://attacker/collect`、header `Authorization: {{secret:STRIPE_KEY}}`、`allowResponseBody:true`，worker 执行即外泄密钥。**本仓有跨租户写历史，fail-closed。**

### 6.3 不变量（registry-only + dual-validate）

**S-1 取舍：危险字段 registry-only。** 工作流节点**引用**一个注册的节点类型（by `typeId`/slug），只能携带**非危险参数覆盖**。危险字段（http `url`、含 secret 的 `headers`、script `code`）**只在 org-scoped `custom_node_types` 注册表**，编辑者**永远改不了**（要改得改注册表，那里有 save-time `validate*`）。

**节点 `parameters` 可覆盖 / 不可覆盖**（按描述 `Constraints` 判，`nodedesc/types.go:90-94` 词汇）：

| Property `Constraints` | 含义 | 节点 `parameters` 可覆盖？ |
|---|---|---|
| 无 / 普通 | 安全字段（temperature/outputFormat/systemPrompt 模板…） | ✅ 可覆盖 |
| `NoTemplate:true` | 禁 `{{ }}`（http `url` 须静态字面量） | ❌ registry-only |
| `NoSecret:true` | 禁 `{{secret:}}`（script `code`、http body） | ❌ registry-only（覆盖被忽略） |
| `SecretAllowedIn:[...]` | 仅这些子字段允许 `{{secret:}}`（http `headers`） | ❌ registry-only（含 secret 通道，不可编辑者控） |

**S-1/B-A7 双处校验（save + run，对编辑者可控 JSON）**：

- **保存端**：workflow 保存 handler（`workflowhandlers.go`）对每个 typed 节点的 `parameters` overlay 跑校验器——拒绝任何覆盖了危险（registry-only）键的 overlay。失败 → 整个保存 reject（fail-closed）、不静默吞掉非法键。这逼校验器可对 **node-parameter 形状**调用（不只 `UpsertInput`）——把 `customnodetype/store.go` 的 `validateHTTPParams`/`validateScriptParams` 拆出可复用的「校验这组参数键」入口，或加一个「校验 overlay 不含危险键」的薄函数（推荐后者：overlay 校验只需查 `Constraints`，比完整 param 校验轻，且不与注册表 base 校验语义耦合）。
- **运行端（worker，§4.1 合并已半保障，仍须显式重校验）**：worker 执行前对**合并后**的节点参数重跑校验断言——never trust stored JSON。§4.1 的合并已对危险键「忽略 overlay、取 base」，但运行端须**再断言**合并结果里危险字段确实是 base 值（防合并 bug / 防直接 PUT 绕过保存端后留下的脏 JSON 在某条新读路径泄漏）。具体：worker 在 `runCustom`（`worker.go:1705`）dispatch 前，对解析出的 `httpParams`/`scriptParams` 跑等价 `validate*` 断言（url 无 `{{`、body/code 无 `{{secret:}}`、method/format 枚举）。现有散点内联复检（`worker.go:1908/1912`）保留并归并进这个显式重校验入口。

**为何运行端校验是硬要求**：编辑者可 `PUT workflows.nodes` 绕过画布与（若有 bug 的）保存端校验。运行端是最后一道、也是唯一对"执行那一刻的真实参数"成立的门。父 spec S-1：「危险约束须在保存**与**运行两处对不可信输入成立」。

### 6.4 secret 通道与 `{status}` 守卫不变（继承现状）

本片不改 secret 解析（`worker.go:1868-1903`，运行期、worker 内、对注册表作者模板、可信 `OrgIDForProject` orgID、明文不落库）与 `secretBearing && !allowResponseBody` → 只存 `{status}` 守卫（`worker.go:1932`）。因为危险字段（含 secret 的 headers、allowResponseBody）**registry-only**，编辑者 `parameters` 碰不到它们，这两条守卫的输入面与今天逐字相同。这正是 registry-only 取舍相对"危险参数上节点"的安全优势：**不扩大 secret 攻击面**。

### 6.5 独立安全评审门禁

P-write 引入 schema 驱动的参数保存 + 双处校验，按 `customnodetype` 仓规须**独立安全评审**，合入前为门禁。评审须含：
- 跨租户 parity 测试（编辑者无法经 `parameters` 触碰他 org / 注册表危险字段）。
- 双校验测试（伪造节点带危险覆盖 → 保存**与**运行两处都拒，fail-closed，§7）。
- 直接 `PUT workflows.nodes` 绕画布的威胁覆盖。

---

## §7 测试策略

DB 测试用 **fresh PG、`GOWORK=off`、`-p 1`**（本仓铁律：脏数据撞 transient 唯一索引 + 并行 migrate race）。

1. **B-A1 round-trip parity（前端，Vitest）**：构造带 `parameters` + 一个**未知** Property 键的节点 → `toReactFlow` → `toStudioNodes` → 断言 `parameters` 与未知键经 load→save→reload **逐字存活**。复用 `nodeColor.parity.test.ts` 模式。
2. **PlanCustom 合并读路径（Go，DB-backed）**：
   - 节点 `Parameters` 覆盖非危险键 → PlanCustom 产出的 `todos.input_json.params` 含覆盖值。
   - 节点 `Parameters` 尝试覆盖危险键（url/code/secret-header）→ 合并**忽略 overlay、取注册表 base**。
   - 缺 `Parameters`/`TypeVersion` 的老节点行 → 产出与今天逐字节一致（无回归）。
3. **双处校验 fail-closed（S-1，安全门禁核心）**：
   - **保存端**：伪造 workflow（节点 `parameters` 含 `url: "http://attacker/{{x}}"` 或 `code` 含 `{{secret:K}}`）→ 保存 handler reject。
   - **运行端**：直接 `PUT`/INSERT 脏 `workflows.nodes`（绕过保存校验）→ worker 执行前重校验**拒绝**该节点、不发出站请求、不外泄。
   - 跨租户：org A 编辑者无法经 `parameters` 引用 org B 注册表 / 危险字段。
4. **typeVersion 选描述**：`(type, typeVersion=1)` 选 v1 描述；缺字段默认 v1（本片无 v2，断言选择逻辑存在且默认正确）。
5. **运行端等价校验回归**：现有 `runCustomHTTP`/`runCustomScript` 的 `{{secret:}}`/url 复检（`worker.go:1908/1912/1969`）行为不变——归并进显式重校验入口后跑既有 worker_custom 测试绿。

---

## §8 分阶段（可发布子步，各独立可测）

leaf-first，每步 branch→PR、fresh DB `-p 1`。排序遵守 D-2（后端读先于前端写）+ S-1（双校验先于/同步于写上线）。

- **P-write-1 — 后端信封 + 读路径（无前端写）**。`WorkflowNode` 加 `Parameters`/`TypeVersion`；PlanCustom 合并读路径（§4.1，非危险 overlay 叠加注册表 base，危险键取 base）+ 按 `(type,typeVersion)` 选描述。前端**未写**这些字段，故纯加性、零回归（老行无字段=今天行为）。*验证：测试 2 + 测试 4；既有 PlanCustom 测试绿。*
- **P-write-2 — 校验器可对 node-parameter 调用 + 运行期重校验**。从 `customnodetype/store.go` 拆出 overlay 校验入口（查 `Constraints` 拒危险键）；worker `runCustom` dispatch 前加显式重校验（归并 `worker.go:1908/1912` 散点复检）。*验证：测试 3 运行端 + 测试 5；安全门禁可挂。*
- **P-write-3 — 前端 round-trip + preserve-unknown**。`toStudioNodes` 改全节点透传（§5.1）+ B-A1 parity 测试。此步独立有价值（即修了今天 `parameters` 被剥的潜在 bug），且为下一步铺路。*验证：测试 1。*
- **P-write-4 — 可编辑写路径**。`PropertiesPanel` typed 节点 `<PropertiesForm>` `onChange`→`onPatch({parameters, typeVersion})`（§5.2）；保存端校验挂上（§6.3 保存端，测试 3 保存端）。**此步须独立安全评审门禁（§6.5）**。*验证：测试 3 全 + 端到端：编辑→保存→刷新→值在→运行覆盖生效。*

---

## §9 开放问题 / 待评审

1. **overlay 校验入口的形状**（§6.3）：推荐"薄 overlay 校验"（只查 `Constraints` 拒危险键，不复跑完整 param 校验）vs 复用完整 `validate*`（拆 `customnodetype/store.go` 让其可对任意 param 形状调用）。前者更轻、语义更窄、不耦合注册表 base 校验；后者更"一处校验逻辑"。**待评审定**——倾向前者，但需确认没有"非危险键也有跨字段约束"的反例（如 outputFormat 枚举——这其实也该 overlay 校验，那 overlay 校验就不止"查危险词汇"了）。
2. **`Parameters` overlay 的键全集是否需白名单**：除"拒危险键"外，是否该拒"描述里根本没有的键"（防编辑者塞任意 JSON 进 `parameters` 膨胀 input_json）？倾向 §3.2 的 preserve-unknown 与"拒未知键"矛盾——preserve-unknown 是为**跨版本前向兼容**（老客户端别丢新字段），而 input_json 注入应只取描述已知键。建议：round-trip 保 unknown（前端落盘），但 PlanCustom 合并时**只取描述已知的非危险键**注入 todo（运行面收窄）。待评审确认这层不矛盾。
3. **`TypeVersion` 写入时机**：放置节点时写当时描述 `Version`，还是保存时写？n8n 是放置时钉、之后不动（升级是显式操作）。本片只有 v1 无实际差异，但契约该钉死，避免 P5 引入 v2 时歧义。倾向**放置时钉**。
4. **直接 `PUT workflows.nodes` 的保存校验是否覆盖所有写入端**：`workflowhandlers.go` 是否是 `workflows.nodes` 的唯一写入路径？若有其它写入端（如 m12 backfill、CLI、test fixture）绕过保存校验，运行端重校验（§6.3）是唯一兜底——须枚举所有写入端确认运行端门禁确实是 last line。待安全评审枚举。
