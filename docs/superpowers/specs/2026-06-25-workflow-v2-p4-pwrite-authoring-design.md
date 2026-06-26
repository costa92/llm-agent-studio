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
3. 即便存进了内存节点，保存时也会被 `canvasModel.ts:toStudioNodes`（`canvasModel.ts:85-114`）的字段白名单**剥掉**（B-A1）。

**P-write 把这条链打通**：表单 `onChange` → 节点 `parameters` 内存态 → `toStudioNodes` 全节点 round-trip 保存 → 节点 store JSON 持久化（`workflows.nodes` 或 legacy `projects.workflow_nodes`，§6.2） → resolve 层（`resolveCustomTypes`）按 kind 合并 + danger-filter `parameters` → PlanCustom 消费已合并 blob 解释为 todo 输入 → worker 执行（运行期重校验）。

### 1.2 为什么是现在

P1-P3 已把"读"侧（描述、渲染、items、运行期解析）全部铺好。唯一缺口是"写"侧：所有 typed 自定义节点今天的可编辑参数仍只能经 `custom_node_types` 注册表（org 级、整类型共享）调，**不能 per-node 覆盖**。P-write 补上 per-node 参数创作，是 n8n「节点自配置」语义的最后一块拼图。

### 1.3 翻转独立性（重申，作为范围边界）

ExprChannel（运行期 `$node` 解析）与本写路径正交。P-write 落地不依赖、不等待、不修改 P3 翻转开关。涉及 worker 的唯一改动是**运行期重校验**（§6 S-1），它在执行入口对解析后的节点参数跑校验器，与变量解析通道无关。

---

## §2 范围 / Non-goals

### 2.1 范围（薄片，typed-custom 优先）

**一句话成功标准**：在画布上选中一个 typed 自定义节点（`custom:<slug>` + `typeId`，kind ∈ llm/http/script），改 `<PropertiesForm>` 里的**非危险**参数，保存，刷新后值还在，运行时该 per-node 覆盖生效，且危险参数被服务端在保存与运行两处校验把守。

落地面：
- 后端：`WorkflowNode` 加 `Parameters json.RawMessage` + `TypeVersion int`（§3）；`nodedesc.Constraints` 加 `RegistryOnly`（§6.3）；合并 + danger-filter 落在 **resolve 层**（`resolveCustomTypes`），按 resolved **kind** + `typeVersion` 选描述、allow-list 注入（§4），PlanCustom 保持 store-thin；完整 `validate*` 可对 node-parameter 形状调用、运行期重跑（§6）。
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
    TypeVersion int              `json:"typeVersion,omitempty"` // 放置/保存时写入当时 description.Version；resolve 层按 (resolved kind, typeVersion) 选描述，§4.3
    Parameters  json.RawMessage  `json:"parameters,omitempty"`  // schema 化参数值（PropertiesForm value 对象的序列化）
}
```

- `TypeVersion`：对应 `nodedesc.NodeTypeDescription.Version`（`nodedesc/types.go:11`，当前 `const Version = 1`）。放置/保存节点时写入当时描述的 `Version`。执行器据 **resolved kind + typeVersion** 选描述解释参数（§4.3，**非按 `custom:slug`**）——type v1→v2 改字段含义时，老图按 v1 语义读，不静默损坏（D-1）。无该字段的老节点（`omitempty`）= v1 默认；**`typeVersion` 存在但匹配不到已知版本 → fail-closed 拒绝（§4.3 M3），绝不静默回落**。
- `Parameters`：`<PropertiesForm>` 的 `value: Record<string, unknown>`（`PropertiesForm.tsx:11`）序列化。**注意安全分层**（§6）：`Parameters` 只承载**非危险参数覆盖**；危险/RegistryOnly 字段（http `url`/含 secret 的 `headers`/`bodyTemplate`/script `code`/`allowResponseBody`——§6.3 标记）**不进这里**，仍留注册表。

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
- **preserve-unknown 仅前端（disk）级（m3 范围澄清）**：load→save→reload 保未知键的不变量**只在前端**成立（`toStudioNodes` round-trip，§5.1，落 disk JSONB）。**服务端不保**：`PlanCustom` 把 `nodes` 经 typed `WorkflowNode` 结构体 re-marshal（`planner.go:305` `json.Marshal(nodes)` → `plans.raw_plan_json`），未知键**服务端被丢**。故 §7 的 B-A1 parity 测试（前端 Vitest）**不覆盖**服务端 drop——本 spec **不声称服务端 preserve-unknown**。运行注入面本就只取描述已知键（§4.2 drop-unknown），两者一致：unknown 键活在 disk（创作前向兼容），不进 runtime。

### 3.3 迁移 / 兼容（无数据迁移）

- **无 m20 类数据迁移**。本片不回填历史节点（那是父 spec P3 的 m20，含项目配置烘焙——明确 Non-goal §2.2）。
- 老 `workflows.nodes` 行**无** `typeVersion`/`parameters` 键 = 合法。读路径把缺失 `TypeVersion` 当 `1`（默认描述版本）、缺失 `Parameters` 当**空覆盖**（仅用注册表 params，等价今天行为）。
- **为何无需数据迁移（已对 PlanCustom 核实）**：`workflows.nodes` 存的是**图结构 + 创作意图**，不是执行态。每次运行经 `PlanCustom`（`planner.go:270`）**重新规划**——把节点投影成 `todos.input_json` 后才执行。即没有"半执行的存量数据"会因新字段含义变化而损坏：旧行缺 `parameters` → PlanCustom 走「无覆盖」分支 → 产出与今天逐字节一致的 `{kind, params}` input。新字段是纯加性创作元数据，老行天然兼容。
- `Parameters json.RawMessage` 经 GORM/`workflows.nodes` JSONB 往返时遵循本仓 NULL 纪律（`[]byte`→`json.RawMessage` 中转，空当 `nil`/`{}`）；写仍走 workflow 保存的 `INSERT...RETURNING`/JSONB 列既有路径，本片不新增 store 写法。

---

## §4 读路径（先于前端写落地，D-2 排序）★ 载重

> ★ Amendment 2/4/5（B2/M2/M3）：合并 + danger-filter **不住在 PlanCustom**（store-thin planner），而在 **handler/resolve 层**（`resolveCustomTypes`，`handlers.go:100`），按注册表 **kind**（`ResolvedType.Kind` ⊃ `ct.Kind`）选描述，**绝不按 `custom:<slug>` 选**。PlanCustom 消费一个**已合并、已过滤**的 params blob，自身不变。下文 §4.1（为何不能在 planner 里做）→ §4.2（合并契约：allow-list-by-description + RegistryOnly）→ §4.3（typeVersion fail-closed）。

### 4.1 为何合并 + danger-filter 不能住在 PlanCustom（B2，已核实）

父 spec 初稿设想「PlanCustom 按 `(n.Type, n.TypeVersion)` 取 nodedesc 描述、查其 `Constraints` 做合并」。**已核实此路不通**：

- `n.Type` 是 `custom:<slug>`（`planner.go:149`/`158`），而 `nodedesc.Builtins()` 的描述只覆盖 **base kind**（`llm`/`http`/`script`/`studio.*`，`builtin.go`）——**没有 `custom:<slug>` 的描述**。`custom:slug → base kind + 投影描述` 的投影逻辑住在 `customFromRow`（`nodetypeshandlers.go:72-96`，httpapi 包），它靠注册表 **kind**（`baseByKind[row.Kind]`，`nodetypeshandlers.go:44`）取 base 描述。
- planner **故意从不读注册表**（`planner.go:182` 注释 "the planner never reads the registry (store-thin)"），其 import 块（`planner.go:3-16`）不含 `nodedesc`/`customnodetype`/任何注册表包（只 import `internal/prompt`、`internal/todos`）。让 PlanCustom 按 kind 选描述会迫使它 import nodedesc + 拿到注册表 kind，破坏 store-thin 不变量。
- `resolveCustomTypes`（`handlers.go:100`）**已持有** kind（`ct.Kind`）+ org + 注册表句柄，且是 **两条 run 路径唯一的合流点**（`handlers.go:460` runHandler 与 `workflowhandlers.go:186` runWorkflowHandler 都先调它再调 PlanCustom）。这是合并 + 过滤的天然归宿。

**改法**：把 per-node 合并 + danger-filter 上移到 `resolveCustomTypes`（或它调用的合并 helper——该 helper 做 allow-list/过滤机制，**值校验仍复用完整 `validate*`**，§4.2/§6.3，勿混为「danger-word-only 检查」）。它产出**已合并、已过滤的 params blob**，仍经现有 `ResolvedType.Params` 字段流入 PlanCustom。PlanCustom 的 typed-node 分支（`planner.go:316-331`）**逐字不动**——它继续把 `rt.Params` 注入 `params`、叠 `variables`、写 `{kind, params}`，只是 `rt.Params` 现在已是合并结果而非裸注册表值。store-thin 保持。

```go
// resolveCustomTypes（handlers.go:100，已持 kind+org+registry）：
//   ct := res.Get(ctx, n.TypeId, orgID)         // ct.Kind + ct.Params(注册表 base)
//   desc := descByKind(ct.Kind, n.TypeVersion)  // §4.3：未知 typeVersion → fail-closed
//   merged := mergeAllowList(ct.Params, n.Parameters, desc)  // §4.2
//   resolved[n.ID] = ResolvedType{Kind: ct.Kind, Params: merged}
// PlanCustom 照旧消费 rt.Params（已合并），逐字不改。
```

### 4.2 合并契约：allow-list-by-description ∩ 非 RegistryOnly（M2/M4 钉死）

合并是**显式 allow-list**，不是「拒危险词」的黑名单：

```
inject_keys = { 描述里已知的 Property 键(by desc kind) }  ∩  { 非 RegistryOnly 的键 }
merged      = base(注册表)  叠加  overlay 中落在 inject_keys 的键
```

- **drop-unknown（M2）**：overlay 里描述**不认识**的键——**永不注入** runtime params（只保留在 disk 上 round-trip，§5.1）。这收窄运行面、防编辑者塞任意 JSON 膨胀 `input_json`（解 §9 旧开放问题 2）。
- **default-deny RegistryOnly（M1/M4）**：overlay 里命中 RegistryOnly 标记（§6.3）的键——**丢弃、取 base**，记审计。fail-closed。
- **复用完整校验器，不写「查危险词」的薄检查（m1 + §6.3 单一真源）**：被覆盖的非危险键（如 `outputFormat ∈ text|json`、`method` 枚举）**仍需完整校验**，不只是「不含危险词」。因此合并后对 merged 结果跑 §6 的**完整** `validate*`（NAME-hardcoded 那套），而非另写一个「danger-word-only」检查器——避免两套语义漂移。danger 分类的唯一真源是 §6.3 的 RegistryOnly 标记 + 完整校验器，**不靠 `Constraints` 词汇做 danger 判定**（见 §6.3 为何 `Constraints` 不够）。

### 4.3 描述选择：按 kind + typeVersion，未知 typeVersion → fail-closed（M3/D-1）

- 描述按 **resolved kind**（`ct.Kind`）+ `n.TypeVersion` 选，**绝不按 `custom:<slug>`**。
- 缺 `TypeVersion`（老节点，`omitempty`）→ 默认 **v1**（`nodedesc.Version=1`，`types.go:11`）。
- **`TypeVersion` 存在但匹配不到任何已知描述版本**（如节点钉 `typeVersion:2`，但当前二进制只识 `nodedesc.Version=1`，`types.go:11`）→ **fail-closed：plan 期拒绝该节点（PlanCustom/resolve 返回 error，整个 run 报错）**，**绝不静默回落 v1**。理由：静默回落正是 D-1（无声损坏）要防的事，且会误选承载 danger `Constraints` 的那版描述——选错版本 = 选错危险分类。
- 本片只有 v1，但读路径**从第一天起按 kind+typeVersion 选 + 对未知版本 fail-closed**，给未来 v2 留位且不留无声漂移口（D-1 死元数据反模式的解药）。

### 4.4 排序铁律（D-2）

后端读路径（`WorkflowNode.Parameters` 字段 + resolve 层合并/过滤 + 完整校验器可调）**必须先于**前端写它们落地，否则前端写出的值跨发布搁浅在 JSONB 里无人消费。分阶段（§8）据此排序。

### 4.5 内置节点读路径不变

内置节点（script/storyboard/asset，`planner.go:332-358`）的 prompt-precedence 逻辑本片**不动**——它们的参数写路径属 Non-goal。`Parameters` 字段对内置节点为空（resolve 层对无 `typeId` 的节点不进合并分支，`handlers.go:103` 跳过），PlanCustom 的内置分支照旧。

---

## §5 前端往返契约

### 5.1 `toStudioNodes` 全节点 round-trip + preserve-unknown（B-A1）

`canvasModel.ts:85-114` 的 `toStudioNodes` 今天**逐字段白名单重建**节点（`canvasModel.ts:94-112`：只拷 `id/type/promptId/dependsOn/position`，条件拷 `promptText/label/color/typeId/varBindings`）。代码注释已记录被同款 bug 咬过：

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
- `value` 仍取节点参数；但优先级=节点 `parameters` 覆盖 ▸ 注册表 typedParams 兜底（与 §4.2 后端合并语义镜像）。
- `onChange={(next) => onPatch({ parameters: next, typeVersion: description.version })}`——把整个 value 对象写进节点 `parameters`、并钉当时描述版本。
- `onPatch` 是 `PropertiesPanel` 既有的 flat-key patch 通道（`PropertiesPanel.tsx:125`/`247`/`188`），写进画布内存节点，随画布保存经 §5.1 round-trip 落盘。
- **危险字段在前端的处理**：`<PropertiesForm>` 对 RegistryOnly（§6.3）Property 仍渲染（UX 提示/镜像），但 §6 的服务端校验器是边界——前端写了危险/RegistryOnly 覆盖，保存端 reject、运行端 default-deny 取 base。前端校验只是 UX，不是安全边界（B-A7）。

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

### 6.2 P-write 引入的威胁 + `parameters` 的全部写入端枚举（B1，★ 已核实）

`Parameters json.RawMessage` 住在节点 JSON——**编辑者可控**。若 P-write 让 `parameters` 覆盖危险字段，编辑者即可把 http 节点 `url` 设 `http://attacker/collect`、header `Authorization: {{secret:STRIPE_KEY}}`、`allowResponseBody:true`，worker 执行即外泄密钥。**本仓有跨租户写历史，fail-closed。**

**关键：节点 `WorkflowNode[]` 不是单一 store/单一 save handler，而是两套 on-disk store + 各自 run 路径 + 一条 backfill 迁移。** 保存端校验若只挂一处会漏。三个写入端（逐一核实）：

| # | 写入端 | 落盘列 | save-time 校验现状 | run 路径 |
|---|---|---|---|---|
| W1 | `internal/workflows/store.go` `Create`（`store.go:66`）/`Update`（`store.go:173`），喂自 `workflowhandlers.go` create（`workflowhandlers.go:47`）/update（`workflowhandlers.go:77`） | `workflows.nodes` | **仅 graph-only** `ValidateCustomGraph`（create `workflowhandlers.go:61`、update `:91`）；不校验 `parameters` 内容 | `runWorkflowHandler`→`resolveCustomTypes`（`workflowhandlers.go:186`）→`PlanCustom`（`:191`） |
| W2 | legacy `internal/project/store.go` `Create`（`store.go:138`/`168`），喂自 `handlers.go` create-project（`handlers.go:294`/`313`） | `projects.workflow_nodes` | **零** save-time graph/参数校验（仅 run 期 `ValidateCustomGraph`，`handlers.go:424-434`）。注：`project/store.go:287` 显式**不在 Update 改 workflow_nodes**——legacy 唯一写入是 create | `runHandler`→`resolveCustomTypes`（`handlers.go:460`）→`PlanCustom(...,"",...)`（`handlers.go:465`） |
| W3 | backfill 迁移 `INSERT INTO workflows ... SELECT p.workflow_nodes`（`storage/storage.go:390-395`） | `workflows.nodes` | **零校验**：逐字 copy `projects.workflow_nodes`→`workflows.nodes`，含任何 `parameters`，无 save 钩子 | 之后走 W1 的 run 路径 |

**结论（双边界）**：
- **save-time 校验必须挂 BOTH 编辑者写路径**：W1（workflows，`workflowhandlers.go` create+update）**与** W2（legacy projects，`handlers.go` create-project）。任一漏挂即留绕过口。
- **run-time 重校验是权威的最后一道边界**：W3（backfill）天然绕过 save 校验、且任何**未来**新写入端也会绕过——因此**不能**指望 save 校验是充分的。run 期（`resolveCustomTypes` 合并的 default-deny + worker dispatch 前的完整 `validate*`，§6.3）是唯一对「执行那一刻真实参数」成立的门，必须独立成立。
- 攻击面入口仍含**直接 `PUT`/INSERT 脏 JSON 绕过画布**（编辑者是能改 workflow 的最低权限角色），落到 W1/W2 的列；这正是为何 run-time 是 last line。

### 6.3 不变量（registry-only + dual-validate）★ danger 分类单一真源 = `RegistryOnly` 标记

**S-1 取舍：危险字段 registry-only。** 工作流节点**引用**一个注册的节点类型（by `typeId`/slug），只能携带**非危险参数覆盖**。危险字段**只在 org-scoped `custom_node_types` 注册表**，编辑者**永远改不了**（要改得改注册表，那里有 save-time `validate*`）。

**danger 分类的单一真源 = `nodedesc.Constraints` 上新增的显式 `RegistryOnly bool`（M1）。** 不用「有某 danger `Constraint` ⇒ 危险」做判定——已核实那条规则有致命漏洞：

> `allowResponseBody`（`builtin.go:112`）是 exfil 启动器（翻 `secretBearing && !allowResponseBody` 守卫，`worker.go:1934`），但它是 plain `PropertyBoolean`、**无任何 `Constraints`**。若按「无 constraint ⇒ 可覆盖」，`{"allowResponseBody":true}` overlay 会被**误放行**，把一个 secret-bearing 的 http 节点变成全 body exfil oracle。

故 amend `nodedesc.Constraints`（`types.go:90-94`）**加 `RegistryOnly bool`**，并在 `builtin.go` 上标记到 **url（`:107`）/ headers（`:108`）/ bodyTemplate（`:109`）/ code（`:120`）AND allowResponseBody（`:112`）** 五者。过滤器（§4.2 合并 / 保存校验 / 运行重校验）的 danger 测试统一为：

> **default-deny 任何键 if `RegistryOnly==true` OR 携带任何 danger `Constraint`（NoTemplate/NoSecret/SecretAllowedIn）。** 即两者并集，且 `RegistryOnly` 覆盖「无 constraint」的漏洞。

**节点 `parameters` 可覆盖 / 不可覆盖**：

| 标记 | 例 | 节点 `parameters` 可覆盖？ |
|---|---|---|
| 无标记 / 无 danger constraint | temperature / outputFormat / systemPrompt 模板 | ✅ 可覆盖（仍须过完整校验，见下） |
| `RegistryOnly:true`（无 constraint 也标） | http `allowResponseBody`（exfil 启动器） | ❌ default-deny |
| `RegistryOnly:true` + `NoTemplate` | http `url`（须静态字面量） | ❌ registry-only |
| `RegistryOnly:true` + `NoSecret` | script `code`、http `bodyTemplate` | ❌ registry-only |
| `RegistryOnly:true` + `SecretAllowedIn` | http `headers`（含 secret 通道） | ❌ registry-only |

**S-1/B-A7 双处校验（save + run，对编辑者可控 JSON）——校验复用完整 `validate*`，不写「danger-word-only」薄检查（m1 单一真源）**：

danger 分类（RegistryOnly 标记）与**值校验**（`outputFormat ∈ text|json`、`method` 枚举等跨字段约束）是两件事。被覆盖的非危险键也需值校验，故**两端都把 overlay 与 base 合并后，对 merged 结果跑完整 `validate*`**（NAME-hardcoded 那套，`validateHTTPParams`/`validateScriptParams`），不另写一个只查危险词的薄函数——避免两套校验语义漂移。注意 `validate*` 今天是 **NAME-hardcoded**（按 JSON tag 解进 `url`/`headers`/`bodyTemplate`/`code`/`outputFormat` 字段，`store.go:102-132`/`:137-155`），**不读 `Constraints`**；它们与 RegistryOnly 标记**分工**：RegistryOnly 决定「哪些键禁覆盖」（过滤），`validate*` 决定「合并后的值是否合法」（值校验）。

- **保存端（挂 BOTH 编辑者写路径，§6.2 W1+W2）**：W1 `workflowhandlers.go` create+update、W2 `handlers.go` create-project，对每个 typed 节点：① 拒绝 overlay 含任何 RegistryOnly 键（default-deny，记审计）；② 对「base 叠合法 overlay」后的 merged params 跑完整 `validate*`。任一失败 → 整个保存 reject（fail-closed）、不静默吞键。这逼 `customnodetype/store.go` 的 `validate*` 拆出可对**任意 param 形状**调用的入口（不只 `UpsertInput`）。
- **运行端（worker，权威 last line）**：因 W3 backfill + 任何未来写入端绕过 save 校验，run 期须独立成立。两层：① §4.2 的 resolve 合并已对 RegistryOnly 键 default-deny、取 base；② worker 在 `runCustom`（`worker.go:1705`）dispatch 前，对解析出的 `httpParams`/`scriptParams` 跑等价 `validate*` 断言（url 无 `{{`、body/code 无 `{{secret:}}`、method/format 枚举），**再断言** RegistryOnly 字段确实是 base 值。现有散点内联复检（`worker.go:1908/1912`）保留并归并进这个显式重校验入口。

**为何运行端校验是硬要求**：编辑者可直接 `PUT`/INSERT 脏 JSON 绕过画布与（若有 bug 的）保存端校验，且 W3 backfill 结构性地绕过 save。运行端是最后一道、也是唯一对"执行那一刻的真实参数"成立的门。父 spec S-1：「危险约束须在保存**与**运行两处对不可信输入成立」。

### 6.4 secret 通道与 `{status}` 守卫不变（继承现状）

本片不改 secret 解析（`worker.go:1868-1903`，运行期、worker 内、对注册表作者模板、可信 `OrgIDForProject` orgID、明文不落库）与 `secretBearing && !allowResponseBody` → 只存 `{status}` 守卫（`worker.go:1932`）。因为危险字段（含 secret 的 headers、allowResponseBody）**registry-only**，编辑者 `parameters` 碰不到它们，这两条守卫的输入面与今天逐字相同。这正是 registry-only 取舍相对"危险参数上节点"的安全优势：**不扩大 secret 攻击面**。

### 6.5 独立安全评审门禁

P-write 引入 schema 驱动的参数保存 + 双处校验，按 `customnodetype` 仓规须**独立安全评审**，合入前为门禁。评审须含：
- 跨租户 parity 测试（编辑者无法经 `parameters` 触碰他 org / 注册表危险字段）。
- 双校验测试（伪造节点带危险/RegistryOnly 覆盖 → 保存**与**运行两处都拒，fail-closed，§7）。
- **`allowResponseBody` 回归**：`{"allowResponseBody":true}` overlay（无 `Constraints` 但 RegistryOnly）被两端 default-deny，secret-bearing 节点仍只存 `{status}`。
- **双写入端覆盖**：保存校验确实挂 BOTH W1（`workflowhandlers.go` create+update）+ W2（`handlers.go` create-project），非只挂一处。
- **W3 backfill / 脏 JSON 绕过**：直接 `PUT`/INSERT 脏 `workflows.nodes`（或经 backfill 复制脏 `projects.workflow_nodes`）→ run 期 default-deny + 完整 `validate*` 拒绝，不外泄。

---

## §7 测试策略

DB 测试用 **fresh PG、`GOWORK=off`、`-p 1`**（本仓铁律：脏数据撞 transient 唯一索引 + 并行 migrate race）。

1. **B-A1 round-trip parity（前端，Vitest）**：构造带 `parameters` + 一个**未知** Property 键的节点 → `toReactFlow` → `toStudioNodes` → 断言 `parameters` 与未知键经 load→save→reload **逐字存活**。复用 `nodeColor.parity.test.ts` 模式。
2. **resolve 层合并读路径（Go，DB-backed）**——合并在 `resolveCustomTypes`（`handlers.go:100`），按 resolved kind 选描述：
   - 节点 `Parameters` 覆盖非危险键（描述已知、非 RegistryOnly）→ 产出的 `todos.input_json.params` 含覆盖值。
   - 节点 `Parameters` 尝试覆盖 RegistryOnly 键（url/code/secret-header/`allowResponseBody`）→ 合并 default-deny、**取注册表 base**。
   - 节点 `Parameters` 含**描述未知**键 → drop-unknown，**不进** `input_json`（运行面收窄，M2）。
   - 缺 `Parameters`/`TypeVersion` 的老节点行 → 产出与今天逐字节一致（无回归）。
3. **双处校验 fail-closed（S-1，安全门禁核心）**：
   - **保存端 ×2 写路径（B1）**：伪造节点 `parameters` 含 `url: "http://attacker/{{x}}"` / `code` 含 `{{secret:K}}` / `allowResponseBody:true` → **W1 `workflowhandlers.go` create+update 与 W2 `handlers.go` create-project 都 reject**（两条都测，非只测 W1）。
   - **运行端**：直接 `PUT`/INSERT 脏 `workflows.nodes`（含经 W3 backfill 复制脏 `projects.workflow_nodes`，绕过保存校验）→ worker 执行前重校验**拒绝**该节点、不发出站请求、不外泄。
   - 跨租户：org A 编辑者无法经 `parameters` 引用 org B 注册表 / 危险字段。
4. **typeVersion 选描述 + fail-closed（M3）**：
   - `(kind, typeVersion=1)` 选 v1 描述；缺字段默认 v1（断言选择逻辑存在且默认正确）。
   - **节点钉 `typeVersion:2`、二进制只识 v1（`nodedesc.Version=1`）→ plan 期 fail-closed 报错**，绝不静默回落 v1（防 D-1 无声损坏 + 防误选危险分类）。
5. **运行端等价校验回归**：现有 `runCustomHTTP`/`runCustomScript` 的 `{{secret:}}`/url 复检（`worker.go:1908/1912/1969`）行为不变——归并进显式重校验入口后跑既有 worker_custom 测试绿。
6. **`allowResponseBody` RegistryOnly 回归（M1，无 `Constraints` 的危险键）**：`{"allowResponseBody":true}` overlay 经 resolve 合并被 default-deny（取 base `false`），secret-bearing 节点仍只存 `{status}`（`worker.go:1934` 守卫不被 overlay 翻转）。

> **测试 1（B-A1 前端 Vitest）的边界**：它断言的是**前端 disk** round-trip（`toStudioNodes` 保未知键），**不覆盖服务端**——`PlanCustom` 经 typed 结构体 re-marshal（`planner.go:305`）会 drop 未知键（§3.2 m3）。勿用测试 1 声称服务端 preserve-unknown。

---

## §8 分阶段（可发布子步，各独立可测）★ 排序含 straddle 数据丢失约束

leaf-first，每步 branch→PR、fresh DB `-p 1`。排序遵守三条铁律：D-2（后端读先于前端写）+ S-1（双校验先于/同步于写上线）+ **M4 straddle：round-trip/preserve-unknown 修复（P-write-3）须先于或与「写 `parameters` 的 UI」（P-write-4）同步上线**。

> **M4 — stale-FE-bundle 数据丢失隐患（载重排序理由）**：今天 `toStudioNodes`（`canvasModel.ts:85-114`）的白名单**剥掉** `parameters`/`typeVersion`。new-BE/old-FE straddle 下，一个**未更新** `toStudioNodes` 的旧 FE bundle 每次保存都会**静默抹掉**更新过的客户端写入的 `parameters`。这不是普通向后兼容问题——它是**主动数据破坏**：CDN/bundle 缓存窗口里仍在跑旧 JS 的客户端，一旦对同一 workflow 保存，就把别人刚写的参数清零。故 P-write-3（round-trip 修复）**必须在任何能写 `parameters` 的 UI（P-write-4）之前或同 deploy 落地**，且 deploy 须意识到 CDN/bundle 缓存的 stale-FE 窗口（强缓存失效前旧 bundle 仍可保存）= 数据丢失暴露面，必要时缩短缓存 / 加 bundle 版本门。

- **P-write-1 — 后端信封 + resolve 层读路径（无前端写）**。`WorkflowNode` 加 `Parameters`/`TypeVersion`；`nodedesc.Constraints` 加 `RegistryOnly`（§6.3）；合并 + danger-filter 落在 `resolveCustomTypes`（§4.1/§4.2，按 resolved **kind** 选描述、allow-list 注入、RegistryOnly default-deny、未知 typeVersion fail-closed §4.3）；PlanCustom 逐字不动（store-thin）。前端**未写**这些字段，纯加性、零回归。*验证：测试 2 + 测试 4 + 测试 6；既有 PlanCustom 测试绿。*
- **P-write-2 — 完整 `validate*` 可对 node-parameter 调用 + 运行期重校验**。从 `customnodetype/store.go` 拆出可对任意 param 形状调用的 `validate*` 入口（复用完整校验，非 danger-word-only，§4.2/§6.3）；worker `runCustom` dispatch 前加显式重校验 + RegistryOnly base 断言（归并 `worker.go:1908/1912` 散点复检）。*验证：测试 3 运行端 + 测试 5 + 测试 6；安全门禁可挂。*
- **P-write-3 — 前端 round-trip + preserve-unknown（M4：必须先于/同步 P-write-4）**。`toStudioNodes` 改全节点透传（§5.1）+ B-A1 parity 测试。此步独立有价值（修今天 `parameters`/`typeVersion` 被剥的 bug），且是 P-write-4 上线的**前置数据安全条件**（杜绝 stale-FE 抹参数）。*验证：测试 1。*
- **P-write-4 — 可编辑写路径**。`PropertiesPanel` typed 节点 `<PropertiesForm>` `onChange`→`onPatch({parameters, typeVersion})`（§5.2）；保存端校验挂上 **BOTH** W1（`workflowhandlers.go` create+update）+ W2（`handlers.go` create-project）（§6.2/§6.3，测试 3 保存端）。**前置**：P-write-3 已上线（M4）。**此步须独立安全评审门禁（§6.5）**。*验证：测试 3 全 + 端到端：编辑→保存→刷新→值在→运行覆盖生效。*

---

## §9 开放问题 / 待评审

> 上一稿的开放问题 1/2/4 已被本轮 amendment 裁决，归档于下；剩余真开放项见 3/5/6。

- **~~1. overlay 校验入口的形状~~ → 已裁决（§4.2/§6.3，m1）**：**复用完整 `validate*`** 对 merged 结果跑，不写「danger-word-only」薄检查。因被覆盖的非危险键（outputFormat 枚举、method）也需值校验，薄检查会漏。danger 分类与值校验分工：RegistryOnly 标记管「禁覆盖」，`validate*` 管「值合法」。
- **~~2. overlay 键白名单 / 拒未知键~~ → 已裁决（§3.2/§4.2，M2）**：preserve-unknown 与「不注入未知键」**不矛盾**且分层——前端 disk round-trip 保未知键（前向兼容），但 resolve 合并**只注入「描述已知 ∩ 非 RegistryOnly」键**（allow-list），未知键 drop、不进 runtime。
- **~~4. 枚举所有 `workflows.nodes` 写入端~~ → 已枚举（§6.2，B1）**：三写入端 W1（workflows store via workflowhandlers）/W2（legacy projects store via handlers create-project）/W3（backfill 迁移）。save 校验挂 W1+W2；run 期重校验是对 W3 + 未来写入端的权威 last line。
3. **`TypeVersion` 写入时机**：放置节点时写当时描述 `Version`，还是保存时写？n8n 是放置时钉、之后不动（升级是显式操作）。本片只有 v1 无实际差异，但契约该钉死，避免 P5 引入 v2 时歧义。倾向**放置时钉**。
5. **`RegistryOnly` 标记的落点确认（M1 实现细节）**：本 spec 钉死「url/headers/bodyTemplate/code/allowResponseBody 五者标 `RegistryOnly`」。`builtin.go` 是否还有其它**无 `Constraints` 但实质危险**的字段（类比 allowResponseBody）须一并标？待安全评审对 `nodedesc.Builtins()` 逐字段过一遍 RegistryOnly 完备性。
6. **未知 `typeVersion` fail-closed 的 UX（M3）**：plan 期拒绝是安全正确，但会让「用新版客户端钉了 v2、回滚到旧二进制」的 workflow 整体跑不动。可接受（fail-closed 优先），但需在 run 错误信息里明确「节点 typeVersion 高于当前支持版本，请升级」而非泛化报错。待 UX 评审。
