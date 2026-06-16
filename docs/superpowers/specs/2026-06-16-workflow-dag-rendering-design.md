# Workflow DAG Rendering Design (子项目 A)

> **状态**:已设计、待实现。本文档是 spec,实现计划另见
> `docs/superpowers/plans/2026-06-16-workflow-dag-rendering.md`(由 writing-plans 产出)。

**目标**:让 web 端「agent 执行」如实渲染自定义工作流的真实 DAG(多 script / 分支 /
多父依赖),每个节点带独立 live 状态;默认管线项目的渲染与当前逐像素保持一致。

**架构一句话**:后端 `projectstate.Compute` 这个**唯一权威纯函数**额外吐出
`nodes[]/edges[]/isCustom`(不动现有 5-role 折叠与 status 派生);前端按
`isCustom` 在表现层路由到「现有 5 段轨道」或「新 GraphView」,两者读同一份权威
`ProjectState`——数据层零双源。

**技术栈**:Go(pgx / 纯函数 Compute)+ React/TypeScript(TanStack Query +
现有 SSE 通道)。自定义 DAG 用**分层竖向布局**,不引入 react-flow 等重依赖。

---

## 1. 背景与问题

当前无论后端 `projectstate.Compute` 还是前端 `WorkbenchView`,都把执行**硬编码成
固定 5 段线性管线**(S1 Planner → S2 剧本 → S3 分镜 → S4 素材 → S5 审核):

- `Compute` 把所有 `script` todo 折叠成单个 `scriptStatus`、所有 `storyboard`
  折叠成单个 `storyboardStatus`(见 `internal/projectstate/state.go:150-177`)。
- 前端 `WorkbenchView` 用 `ROLE_TO_STAGE` 把 role 映射到固定 S1-S5 着色轨道。

但项目已支持**自定义工作流(任意 DAG)**:`workflows.nodes` 是
`planner.WorkflowNode[]`(`id/type/promptId/dependsOn`),`PlanCustom` 据此建
todos,边落在 `todos.depends_on TEXT[]`。一个分支 DAG 跑起来,渲染时被压扁进固定
5 段直线——**实际执行的图 ≠ 渲染出来的图**。这是「执行与渲染逻辑设计不合理」的根源。

**关键事实(可行性)**:DAG 已完整持久化,无需 schema 迁移。
- `todos`:`id, type, status, depends_on TEXT[]`(边)、`created_at`、`plan_id`。
- `plans.workflow_id TEXT`:**仅** first-class workflows 表那条运行路径
  (`runWorkflowHandler` → `PlanCustom(…, wfID, …)`,`workflowhandlers.go:182`)写非空。
- **⚠ isCustom 不能只看 workflow_id**:项目级自定义(`projects.custom_workflow_enabled`
  + `workflow_nodes`)经 `runHandler` 跑时调 `PlanCustom(…, "", …)`
  (`handlers.go:413`),`PlanCustom` 用 `NULLIF($3,'')` 把空 id 落成 **NULL
  workflow_id**(`planner.go:239`)。这类「自定义但 workflow_id 为 NULL」的运行若只
  按 workflow_id 判定会被误渲染成默认轨道——正是要修的场景没被修。故
  `isCustom = workflow_id 非空 || projects.custom_workflow_enabled`。

**约束(label 来源)**:工作流节点的作者 id(如 `script-1`)**不落库**——它仅在
`todos.CreateGraph` 里把 `depends_on` 重映射成随机生成的 `todos.id`,自身不写表
(`todos` 无 `local_id` 列)。因此 DAG 的**边与结构完全准确**(depends_on 存的就是
生成 id),但节点标签**不能用作者 id**。本子项目选**类型派生标签**(零迁移):
label 由 `type` + 同类序号在 Compute 内生成(如「剧本生成 #1 / #2」「分镜拆解 #1」)。

## 2. 设计决策

**混合呈现 = 单一数据模型 + 两个表现渲染器**(非两套数据路径):

- 默认管线项目保留现有**策展式 5 段品牌化体验**(命名阶段 + S4 pip 网格 + agent 配色)。
- 自定义工作流渲染**真实 DAG**。
- 两个渲染器读**同一份**权威 `ProjectState`;差异只是表现层路由。**不引入数据层双源**
  (这正是 PR #50 刚消除的反模式,见
  [`project_studio-workflow-state-singlesource`])。

**自定义 DAG 布局 = 分层竖向**(非 react-flow 画布):与现有 TimelineStage 视觉一致、
零重依赖、窄屏友好。深分支若将来成为常态,再升级到画布(非本子项目)。

## 3. 数据流

```
todos(id,type,status,depends_on[]) ─┐
plans(workflow_id) ─────────────────┤
                                    ▼
              project.Store.LoadState   (单一装载点)
                                    ▼
              projectstate.Compute(Input) ProjectState   ← 唯一权威纯函数
                  ├─ stages[]   (折叠 5-role,保留)
                  ├─ nodes[]    (新)
                  ├─ edges[]    (新)
                  └─ isCustom   (新:workflow_id 非空 || project.custom_workflow_enabled)
                                    ▼
              REST GET /api/projects/{id}/state + SSE `event: state` (沿用)
                                    ▼
       WorkbenchView ── isCustom ? <GraphView> : <现有 5 段轨道>
```

## 4. 后端改动(无 schema 迁移)

### 4.1 `internal/project/store.go` — `LoadState`

- plans 的两条 SELECT(最新 plan / 指定 plan)当前是
  `SELECT id, valid, fallback_used FROM plans …`(`store.go:428-434`,**无
  workflow_id 列**),各新增列 `COALESCE(workflow_id,'')` + 新 scan 变量 →
  填 `in.WorkflowID`。
- LoadState 已加载的 `p`(project 行)有 `p.CustomWorkflowEnabled`
  (`store.go:69`)→ 填 `in.CustomWorkflowEnabled`(供 isCustom 判定,见 F1)。
- todos 的 SELECT 从 `id, type, status, COALESCE(error,'')` 扩为额外取
  `depends_on`(`TEXT[]` → `[]string`)和 `created_at`(`time.Time`),分别填进
  `Input.Todos[].DependsOn` 与 `Input.Todos[].CreatedAt`。
  **主查询 `ORDER BY updated_at ASC` 保持不变**(现有折叠/last-write 语义依赖它);
  graph 的稳定排序由 `buildGraph` 内部按 `(CreatedAt, ID)` 自行完成,不依赖此序。
  **不取 local_id**(列不存在);label 在 Compute 内由 type 派生。

### 4.2 `internal/projectstate/state.go`

新增类型(SEMANTIC,不含 UI):

```go
// GraphNode 是一个 todo 在执行图中的节点(自定义工作流渲染用)。
type GraphNode struct {
	ID      string `json:"id"`               // todo id
	Label   string `json:"label"`            // type 派生(如「剧本生成 #1」)
	Type    string `json:"type"`             // script|storyboard|asset|...
	Status  string `json:"status"`           // blocked|pending|running|done|failed
	AssetID string `json:"assetId,omitempty"` // asset 节点的产物 id,供右栏预览
}

// GraphEdge 是一条依赖边(from 依赖 to;to 先于 from 执行)。
type GraphEdge struct {
	From string `json:"from"` // 下游 todo id(依赖方)
	To   string `json:"to"`   // 上游 todo id(被依赖方)
}
```

`Input` 加 `WorkflowID string` 和 `CustomWorkflowEnabled bool`;`Input.Todos[]`
的 Todo(当前仅 ID/Type/Status/Error)加 `DependsOn []string` 和
`CreatedAt time.Time`。label 不从 Input 来,在 Compute 内由 type+序号生成。

`ProjectState` 加:

```go
Nodes    []GraphNode `json:"nodes"`
Edges    []GraphEdge `json:"edges"`
IsCustom bool        `json:"isCustom"`
```

新增纯函数 `buildGraph(todos []Todo, assetByTodo map[string]Asset) ([]GraphNode, []GraphEdge)`:
- **先按 `(CreatedAt, ID)` 稳定排序**(CreateGraph 同 tx 批量插入,created_at 可能
  并列,故 `ID` 作 tiebreaker)——使节点顺序与 #N 序号在 run 推进中不漂移
  (主查询的 `updated_at` 序会随 worker 改 todo 持续重排,不能用)。
- 每个 todo → 一个 GraphNode(status 复用 `todoStatusToStage`;label 由
  type + 该 type 内递增序号生成,如「剧本生成 #1」「剧本生成 #2」;asset 节点带
  assetId,来源同 Pip 的 assetByTodo)。
- 每条 `depends_on` → 一条 GraphEdge(From=todo.ID,To=dep);
  **丢弃指向不存在 todo 的悬挂边**(防御:跨 plan 残留/数据脏)。
- 返回**非 nil 空切片**(对齐现有 `Pips: []Pip{}`,使 JSON 出 `[]` 而非 `null`)。

`IsCustom = in.WorkflowID != "" || in.CustomWorkflowEnabled`(见 F1)。现有
`deriveStatus` / stages 折叠 / Pips / Assets **原样保留**,与 `buildGraph`
并存互不干扰。

> 约束:`projectstate.deriveStatus` 与 `project.DeriveStatus` 仍是两份故意保留的
> 相同实现,本子项目不动它们。

## 5. 前端改动

### 5.1 `web/src/lib/projectState.ts`

镜像后端新字段:`GraphNode`、`GraphEdge`、`isCustom: boolean`、
`nodes: GraphNode[]`、`edges: GraphEdge[]`。`projectState.contract.test.ts`
**新增** GraphNode.status 枚举断言(现有契约测试只数 4 个枚举数组长度、不对
ProjectState 字段做 shape 断言,加新字段不会自动红、也不会自动守——故需显式新增)。

### 5.2 新组件 `web/src/features/workflow/GraphView.tsx`

- 入参:`nodes`、`edges`、`onSelectNode?`(asset 节点点击 → 右栏预览)。
- **分层竖向布局**:由 edges 做拓扑分层(Kahn 算法,纯函数
  `layerize(nodes, edges)` 抽到 `web/src/lib/graphLayout.ts` 便于单测)。
  同层节点并排,层间连接线表达依赖。环已在保存期被挡(见 PR #50 的
  `findGraphError`/`ValidateCustomGraph`);`layerize` 对万一的残留环做防御
  (检测到则把剩余节点平铺到末层,不死循环)。
- 节点视觉**复用现有 5 态样式**(done 填色 / running 琥珀旋转环 / failed 红 /
  blocked 虚线 / pending),配色按 node.type 复用 `STAGE_META` 的 agent 色;
  未知 type 用中性色。
- 空 `nodes` → 占位「等待规划…」。

### 5.3 `web/src/features/workflow/WorkbenchPage.tsx`

中栏:`state.isCustom ? <GraphView nodes={state.nodes} edges={state.edges}
onSelectNode={…}/> : <现有 5 段轨道>`(组件 `WorkbenchView`,文件
`WorkbenchPage.tsx`)。左栏(brief / 项目信息 / WarnStrip / ErrorStrip /
EventLog)、右栏(选中工件预览)、顶栏(运行 / 取消 / 重运行 / SSE 指示 /
去审核)**两条路径完全共用**,不复制。asset 节点点击:容器用 `node.assetId`
**直接驱动 setSelection(kind:"asset")**——**不复用 `onSelectPip` 的 `PipState`
形参签名**(GraphNode `{id,label,type,status,assetId}` 与 `PipState`
`{todoId,status,assetId}` 字段/枚举不兼容);最终落到同一右栏预览动作。

## 6. 错误处理

- 节点 `failed` → 该节点红显;沿用 `state.error`(最后一个失败 todo)在左栏
  红条抬出原因(不变)。
- `isCustom` 为真但 `nodes` 为空(计划中/尚未建 todo)→ GraphView 占位。
- 悬挂边 / 残留环 → 后端 `buildGraph` 丢悬挂边、前端 `layerize` 末层平铺兜底,
  二者都不抛错、不死循环。

## 7. 测试

- **后端** `internal/projectstate/state_test.go`:`buildGraph` 表驱动——线性链 /
  分支(一父多子)/ 多父(多依赖汇聚)/ 含悬挂边 / 空 todo /
  **运行中途 storyboard fan-out 新增 asset 节点(节点数随 run 增长 + 打乱
  updated_at 后 (CreatedAt,ID) 序号仍稳定)**;断言 nodes 顺序、edges 集合、
  各节点 status 映射、assetId 透传。`IsCustom` 断言两条来源:**workflow_id 非空**
  与 **workflow_id=NULL 但 custom_workflow_enabled=true** 都判 true。
- **后端** `internal/project/store_test.go`:DB-backed,跑一个自定义工作流后
  `LoadState` 返回正确 nodes/edges/isCustom(用既有 PG 夹具,`-p 1` 新库)。
- **前端** `web/src/lib/graphLayout.test.ts`:`layerize` 纯函数——线性/分支/多父/
  残留环兜底。
- **前端** `projectState.contract.test.ts`:新字段与枚举。
- **前端** `GraphView` 组件测:分层渲染、态色、点击 asset 触发 `onSelectNode`、
  空态占位。
- **前端** 路由分流测:`isCustom=false` 渲染 5 段轨道、`isCustom=true` 渲染 GraphView。
- 全量回归:`GOWORK=off go test ./...` + 前端 `vitest run` 全绿。

## 8. 成功标准

跑一个分支自定义工作流(多 script / 分支依赖),中栏**如实画出该 DAG** 且各节点
live 状态正确;**默认管线项目渲染与改动前逐像素一致**(现有 5 段轨道路径不受影响)。

## 8.1 已知限制(优雅降级,非阻塞)

- **删除工作流后历史 run 回落 5 段轨道**:`plans.workflow_id` 是 `ON DELETE SET
  NULL`,而 first-class workflow 运行不置 `projects.custom_workflow_enabled`。
  故「跑过工作流 W → 删除 W」后,该历史 run 的 `workflow_id` 变 NULL 且
  custom_workflow_enabled=false → `isCustom=false` → 渲染回退到折叠的 5 段轨道
  (不崩溃,优雅降级)。这是既有数据模型属性,非本子项目引入。若将来要修:plan
  创建时持久化 per-plan `is_custom` 布尔,或由「存在非默认 depends_on 结构」推导。
- **默认 run 也会计算 nodes/edges**:`buildGraph` 对默认管线运行同样执行(其 todos
  也带 depends_on),`isCustom=false` 时前端不读。代价极小(少量计算+字节),换来
  逻辑简单,刻意保留。

## 9. 范围边界(不在本子项目 A)

- 视觉 / 动画打磨、流式 token 进度、运行控制交互升级 → **子项目 B**。
- DB schema 迁移 → 不需要(DAG 已持久化)。
- 删除后端 5-role 折叠 / 合并 `deriveStatus` 双份 → 保留,不动。
- react-flow / 画布式布局 → 未来升级,非本期。

## 10. 相关

- 前序:`docs/superpowers/specs/2026-06-15-project-workflow-state-single-source-design.md`
  (状态单源,本子项目在其权威 Compute 上扩展)。
- 记忆:`[[studio-workflow-state-singlesource]]`。
