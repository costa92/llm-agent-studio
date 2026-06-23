# 自定义节点 Phase 2A · 执行框架 + `llm` kind 设计

> Phase 1 让用户在画布上放置 `custom:<slug>` 节点并保存，但运行含自定义节点的工作流被 400 拒绝。Phase 2 让自定义节点**可执行**，采用 **kinds + instances** 模型。整个 Phase 2 分解为 A（执行框架 + `llm` kind，本设计）→ B（`http` kind）→ C（`script`/`python` kind）三个子项目；本文档只覆盖 **子项目 A**。

## 范围 / 非目标

**做（子项目 A）**：

- **kinds + instances 框架**：代码内置 `kind`（A 只实现 `llm`），用户在**组织级注册表**里创建“类型实例”（绑定一个 kind + 填参数）。
- **组织级类型注册表**：新表 `custom_node_types`（org 维度），含 CRUD + 组织隔离 + 删除占用守卫。
- **执行接线**：`PlanCustom` 把已解析的 kind+params 写进 todo 的 `input_json`；Worker 增加 `custom:*` dispatch fallback → 通用 `runCustom`（按 kind 分发）；运行闸门从「含任意自定义节点 → 400」改为「含**未绑定**自定义节点（注解）→ 400」。
- **`llm` kind（Rich）**：`systemPrompt` + `userPrompt` + **变量绑定**（`{{name}}` ← 选定上游输出）+ 可选 `model`/`temperature` + `outputFormat: text|json`。
- **通用产物表** `node_outputs`：执行结果落地，下游节点 + 运行视图可消费。
- **前端**：注册表管理 UI（建/列/改/删 typed 类型 + Rich 参数表单）；Phase 1 注解 CustomTypeDialog 保留；画布全入口同时列注解类型 + typed 类型；属性面板展示 typed 节点 kind+params；运行视图展示 `node_outputs`；运行闸门放开 typed-only 工作流。

**不做（留后续）**：

- `http` / `script` / `python` kind（子项目 B / C）。
- 内置 `storyboard` 接受 custom 父节点当作 “script”（其 SQL 过滤 `type='script'`）—— custom→内置 的类型化交接**不在 A**。
- 跨工作流复用机制以外的更复杂调度、版本化、回滚。
- 把 Phase 1 注解节点迁移进注册表（注解与 typed **共存**，互不强转）。

## 关键事实 / 约束（已核验，附 file:line 证据）

经架构评审对照真实代码确认：

1. **两条运行路径都已持有 `org_id`**：`p.OrgID` 经 `ps.Get` 载入并用于配额闸门（`internal/httpapi/handlers.go:409`、`internal/httpapi/workflowhandlers.go:170`）。→ run handler 解析注册表无需新增 org 接线。
2. **`runScript` 的 chat-model 路由可复用**：`worker.go:418-422` 经 `w.routedChatModel(ctx, projectID)`（`worker.go:945-954`，内部 `OrgIDForProject` → `Router.ChatModelFor`，`ok=false` 回退绑定模型）。`llm` executor 原样复用。
3. **`Config.CustomExecutors map[string]TaskExecutor` 已存在并被合并进 dispatch 表**（`worker.go:73`、合并于 `worker.go:165-167`）。`TaskExecutor = func(ctx, ClaimedTodo) (outputRef string, err error)`（`worker.go:54`），`ClaimedTodo.Input []byte` 即 `input_json`。
4. **运行闸门是每条路径一处 `HasCustomNode→400`**（`handlers.go:405`、`workflowhandlers.go:166`，相同中文 400 文案）。`HasCustomNode` 定义于 `planner.go:226-233`。
5. **dependsOn 本地 id → todo id 映射已存在**：`todos.CreateGraph`（`internal/todos/store.go:41-73`）建 `idMap[localID]=newID()` 并在插入事务内改写 `DependsOn`；`PlanCustom` 拿回完整 `idMap`（`planner.go:295`）并已用它把 ready 节点本地 id 映射成 todo id（`planner.go:303`）。→ 变量 `sourceNodeId` → 父 todo id **复用 `idMap` 即可**。
6. **`PlanCustom` 已能逐节点写任意 `input_json`**：逐节点 `inputMap` → `NodeSpec.InputJSON`（`planner.go:257-292`）。
7. **工作流节点在 Go 侧是 raw JSONB 往返**：`workflows.Workflow.Nodes` 是 `json.RawMessage`（`internal/workflows/store.go:35`），Create/Get/Update 存/返原始字节。→ 节点 JSON 上新增 `typeId` 在 Go 存储层不丢；但 `planner.WorkflowNode` 结构须加字段才能**读到**它。
8. **迁移是 `storage.Migrate` 里按序追加的 Go 字符串切片**（`internal/storage/storage.go:355-460`，幂等、增量）；`workflows`/`prompts` 表（`storage.go:330-390`）是 org-scoped + JSONB + 部分唯一索引的现成模板。
9. **GORM 铁律真实且一致**：`internal/storageconfig/store.go` 用 `INSERT … RETURNING`（`db.Raw(...).Row()` + `scanConfig`，不用 `gorm.Create`）、纯 `$N`、多语句用 `db.Transaction`、每条查询带 org 过滤；`todos/store.go:63` 示范 `pq.StringArray` NULL 列约定。
10. **组织隔离 + 删除占用守卫是可复制模板**：`storageconfig.Delete`（`store.go:334-361`）单事务内引用计数 + DELETE 返回 `ErrInUse`；`orgOwnedOrGlobal`（`store.go:405`）是“解析时校验项目 org 拥有该类型”的纵深防御范式。

### ⚠️ 评审发现的载荷性陷阱（必须在实现中显式处理）

- **T1（前端 typeId 不是免费透传）**：`toStudioNodes`（`web/src/features/workflow-canvas/canvasModel.ts:85-110`）**逐字段重建** `WorkflowNode`，label/color 仅因第 106-107 行显式拷贝才幸存。`typeId` 若不显式拷贝，**首次画布编辑保存即丢失**。→ 三处显式改动：`lib/types.ts` 加 `typeId?: string`、`toStudioNodes` 加 `if (n.typeId) out.typeId = n.typeId`、`planner.WorkflowNode` 加 `TypeId string`。
- **T2（既有未 org-scoped 读取，禁止复制）**：`PlanCustom` 的 prompt 查询 `planner.go:277` 是 `SELECT content FROM prompts WHERE id=$1`，**无 org 过滤**（既有跨租户读隐患）。注册表解析**必须** `WHERE id=$1 AND org_id=$2`，且在 **run handler**（持 org 上下文）解析后把 kind+params **传入** planner，planner 永不做未 scoped 的注册表读。
- **T3（运行视图产物面板是真·新接线）**：run overlay 按 `(type, 拓扑序号)` 关联画布节点与 todo（`web/src/features/workflow-canvas/runOverlay.ts:5-11`，todos 无 `local_id` 列）；`projectstate.GraphNode` 今天只暴露 `AssetID`（`internal/projectstate/state.go:34,411`）。→ A 的运行视图做**最小**面板：`Compute` join `node_outputs` 暴露 `outputRef`+文本/JSON 到 GraphNode，前端在既有选中面板渲染；不做更重的产物 UX。

## 数据模型

### 新表 1：`custom_node_types`（组织级注册表）

| 列 | 类型 | 说明 |
|----|------|------|
| `id` | TEXT PK | uuid |
| `org_id` | TEXT | 组织维度 |
| `slug` | TEXT | 由 label 规范化，**创建后不可改** |
| `label` | TEXT | 显示名 |
| `color` | TEXT | 预设调色板 hex |
| `kind` | TEXT | A 只允许 `"llm"` |
| `params` | JSONB | kind 专属配置（见下） |
| `created_at` / `updated_at` | TIMESTAMPTZ | |

唯一索引 `(org_id, slug)`。编辑只改 `label`/`color`/`params`，不改 `slug`/`kind`。

### 新表 2：`node_outputs`（通用产物）

| 列 | 类型 | 说明 |
|----|------|------|
| `id` | TEXT PK | uuid |
| `project_id` | TEXT | 项目维度（变量读取/运行视图按此 scope） |
| `todo_id` | TEXT | 产出该结果的 todo |
| `type` | TEXT | 产出节点类型（`custom:<slug>`） |
| `content` | JSONB/TEXT | 结果内容（文本或 JSON） |
| `format` | TEXT | `"text"` / `"json"` |
| `created_at` | TIMESTAMPTZ | |

**定位说明（对评审 B5 的回应）**：代码库现有产物是 per-artifact 具体表（`scripts`/`shots`/`assets`），无通用产物表。`node_outputs` 是**有意为 B/C 预留的扩展点**（http/script 同样产文本/JSON），不是“与现有存储一致”。配套单一 `resolveOutputText(outputRef)` helper 作为 ref→内容的唯一接缝（`script:`→scripts、`custom:`→node_outputs）。

两张表各一个新迁移切片，追加进 `storage.Migrate`，遵守 GORM 铁律（INSERT…RETURNING / 不 AutoMigrate / 纯 `$N` Raw / NULL 列中转）。

## 两类自定义节点（共存模型）

- **注解节点（Phase 1，不变）**：节点 JSON 自带 `label`+`color`，per-workflow，**无 `typeId`**，**不可运行**。纯草图。
- **Typed 节点（新）**：节点 JSON 带 `typeId`（注册表条目 id）+ 缓存 `label`/`color` 供渲染；引用组织级注册表条目（kind+params），**可运行**。

**判别器 = 节点 JSON 上显式的 `typeId` 字段**（对评审：不依赖“slug 是否在注册表”，避免后续注册某 slug 静默把已有注解变 typed）。planner：有 `typeId` → 解析注册表 → 可运行；无 `typeId` → 注解 → 拒绝运行。

## `llm` kind 参数（Rich）——类型行为 vs 节点绑定 分离

**关键校正（计划阶段评审发现）**：注册表条目是**组织级、可跨工作流复用**的；而变量的 `sourceNodeId` 是 **workflow-local 节点 id**，无法存在组织级注册表里（同一 typed 类型被多个工作流/多个节点复用，local id 各不相同）。故拆成两层：

**① 注册表 `params`（组织级类型行为，存 `custom_node_types.params`）**：

```jsonc
{
  "systemPrompt": "string，可空，支持 {{name}} 模板",
  "userPrompt": "string，支持 {{name}} 模板",
  "model": "可选，覆盖默认路由模型",
  "temperature": 0.7,            // 可选
  "outputFormat": "text"         // "text" | "json"
}
```

变量名是模板里 `{{name}}` 的**隐式声明**——注册表**不单列 `variables`**，模板即变量名单一来源。

**② 节点实例 `varBindings`（per-node，存工作流节点 JSON 上，与 `typeId` 一样随 raw-JSONB 透传）**：

```jsonc
"varBindings": [{ "name": "draft", "sourceNodeId": "<workflow-local 上游节点 id>" }]
```

- **绑定位置**：在**属性面板**逐节点绑定（属性面板有工作流上下文，知道上游节点）；组织级类型编辑器只写模板+行为，**不绑上游**（它没有工作流上下文）。
- **源约束**：`sourceNodeId` **必须 ∈ 该节点 `DependsOn`** 且指向**产文本节点**（`script` 或另一个 `custom`）；绑到 asset/shots（二进制/扇出）输出在 A 是**校验错误**。
- **模板替换**：执行时 `{{name}}` 替换为绑定上游的文本输出；未绑定的 `{{name}}` 留空。
- **`outputFormat: "json"`**：executor 指示模型产 JSON → 校验/解析 → `node_outputs.content` + `format="json"` 落地；解析失败按执行失败重试。

**合并（PlanCustom）**：todo 的 `input_json` = `{kind, params}`，其中 `params` = 注册表 `params` **＋ 注入** `variables:[{name, sourceTodoId}]`（由节点 `varBindings` 经 `idMap` 本地→todo 改写得到，见执行流）。executor 只读 `params.variables[].sourceTodoId`，不关心来源。

> 两个 per-node 透传字段（`typeId` + `varBindings`）都受 T1 约束：`toStudioNodes` 逐字段重建，必须显式拷贝，否则首次保存即丢失。

## 执行流

1. **解析（run handler，持 org）**：对每个 typed 节点（有 `typeId`），按 `WHERE id=$1 AND org_id=$2` 读注册表条目 → kind+params（**禁止**复制 `planner.go:277` 的未 scoped 读，见 T2）。把每节点解析结果传入 planner。
2. **PlanCustom（签名变更，对评审 B4）**：当前 `PlanCustom(ctx, projectID, workflowID, brief, nodes)` 无承载已解析类型的位置 → **新增形参**（如 `resolved map[nodeID]ResolvedType{Kind, Params}`），planner 保持 store-thin、不注入注册表 store、不接触 org。planner 把每节点变量的 `sourceNodeId`（本地）经 `idMap` 映射为父 todo id（复用既有机制，见事实 5），序列化 `{kind, params, variables:[{name, sourceTodoId}]}` 进该 todo 的 `input_json`。
3. **Worker dispatch（对评审：process() fallback）**：`process()` 查不到精确 executor **且** 类型以 `custom:` 开头 → 调用唯一 `runCustom`，按 `input_json.kind` 分发；`"llm"` → llm executor（复用 `routedChatModel`）。executor 对每个变量 `sourceTodoId` → 取该 todo 的 `output_ref` → `resolveOutputText` 取内容 → 替换模板 → 调模型 → 写 `node_outputs` → 返回 `custom:<id>`。`runCustom` 的 switch 即 B/C 扩展点。
4. **产物落地 + 推进**：返回非空 `output_ref` → worker `MarkDone(todoID, outputRef)`（`worker.go:359`）→ 解锁全部依赖已完成的下游（`todos/store.go:99-108`）。
5. **运行闸门翻转**：两条路径的 `HasCustomNode→400` 改为 `HasUnboundCustomNode→400`（任意**无 `typeId`** 的 custom 节点 = 注解）。该判别**只在此一处**计算，不外泄。typed-only 工作流放行。

### 并发/租约

变量多上游读取在 todo 被 claim 之后、dispatch ctx 内发生，todo 全程持租约，无额外租约/并发隐患（`runStoryboard` 已示范单父读取，`worker.go:470-487`）。

## 交互边界（显式）

- ✅ custom 节点**读**任意上游**文本**输出（script / 其它 custom）进变量。
- ❌ 内置 `storyboard` 接受 custom 父当 “script”（SQL 过滤 `type='script'`）—— **A 不做**。custom 节点与 custom/script-as-context 串联，不喂类型敏感的内置节点。

## 前端

- **注册表管理（组织级）**：建 typed 类型（选 kind=`llm` + Rich 参数表单：双 prompt、变量→上游映射、model、outputFormat）；列 / 改（label/color/params，级联同 type 节点）/ 删（占用守卫 → 409）。入口镜像 Phase 1：画布 palette「+ typed 类型」+ 独立管理面。
- **Phase 1 CustomTypeDialog 保留**给注解（仅 label+color）。
- **画布**：palette / NodeTypePicker 同时列注解类型（per-workflow）+ typed 注册表类型（org）。建 typed 节点写 `typeId` + 缓存 label/color。
- **属性面板**：typed 节点展示 kind+params（编辑 → 更新组织注册表条目）；注解节点不变。
- **运行视图（最小，见 T3）**：typed 节点把 `node_outputs` 内容以文本/JSON 面板呈现（复用既有选中节点面板 + GraphNode 新增 `outputRef`/文本字段）。
- **运行闸门**：含任意**注解** custom 节点时禁运行并提示；typed-only 放行。
- **typeId 三处改动（见 T1）**：`lib/types.ts`、`toStudioNodes`、（Go 侧）`planner.WorkflowNode`。

## 安全

组织隔离是关键面：注册表 CRUD 需组织成员/角色（复用 `storageconfig`/`project` authz 范式）；解析强制项目 org 拥有该类型（`WHERE org_id=$N`，见 T2）；`node_outputs` 与变量读取按 project scope；A 无新出网（`llm` 走既有 provider）。注册表 store 标记为需**独立安全评审**（同 storageconfig/project/worker 先例）。

## 测试

**Go（`GOWORK=off`，DB-gated 用 fresh DB）**：

- 注册表 store：CRUD、org 隔离（跨 org 读/改/删拒绝）、slug 唯一、删除占用 → `ErrInUse`。
- run handler 解析：typed 节点按 org-scoped 读注册表；跨 org `typeId` 解析失败/拒绝。
- `PlanCustom`（新签名）：typed 节点 `input_json` 形状 + 变量 `sourceNodeId`→`sourceTodoId` 映射正确；注解节点不解析。
- 运行闸门：注解→400、typed→202，两条路径都测。
- `runCustom`→llm executor（mock chat model）：变量替换、`outputFormat: json` 校验、写 `node_outputs`。
- `resolveOutputText`：`script:`/`custom:` ref 解析；asset/shots 源→校验错误。

**Web（vitest）**：

- Rich 参数表单（含变量绑定 + json 格式开关）。
- 注册表 CRUD UI。
- 画布 typed vs 注解 建节点（typeId 写入/不写入）。
- 运行闸门判定（注解禁、typed 放）。
- 运行视图 `node_outputs` 文本/JSON 面板。
- **回归**：`toStudioNodes` 保 `typeId` 往返（T1 防回归）。

**:5173 手验**：建 typed llm 类型（含变量）→ 放节点连上游 script → 保存 → 运行 → 看 node_outputs 输出；含注解节点时禁运行；纯内置工作流仍可运行（回归）。

## 落地顺序（建议，留给 writing-plans 细化）

后端优先：迁移 + 注册表 store → `PlanCustom` 签名 + 解析接线 → `runCustom`/llm executor + `node_outputs` + `resolveOutputText` → 运行闸门翻转 → run handler org-scoped 解析。然后前端：`typeId` 三处 → 注册表 CRUD + Rich 表单 → 画布双类型入口 → 属性面板 → 运行视图最小面板 → 运行闸门。A 是单一可交付单元；不在后端层拆 A1/A2，但运行视图最小面板天然切走最重的前端块。
