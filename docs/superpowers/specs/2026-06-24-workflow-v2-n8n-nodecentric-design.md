# 工作流 v2：n8n 式「节点中心」重设 设计

> 状态：设计稿，第一轮 Plan agent 评审已纳（见 §10）。多 agent 设计收敛产物（n8n 参考 + 高复用/全保真两份独立提案）。用户选型：**全保真方案 F**（items 数组 + 表达式引擎 + 二进制 items）、**原地迁移**（不留两套引擎，接受改变现有绘本/自动规划行为）。

> **依赖前提**：本 spec 以 **PR #107（`internal/builtinnode` 目录 + `GET /api/node-types/builtin`）+ #108（`prescreen` 内置节点 + `runPrescreen`）合入 main 后**的状态为基线。实现分支须 rebase 到含 #107/#108 的 main。§3.1/§7 中"取代 builtinnode.Catalog/builtin 端点"指的是这个合并后状态——若评审时在不含 #107/#108 的分支看，这些工件尚不存在（属正常）。

## 1. 概述与目标

把 studio 工作流从**「项目中心 + 薄节点」**改成 n8n 式**「节点中心 + 自配置」**：

- **每个节点自带完整配置**（要执行的内容/参数都在节点上），加节点时即在节点上配好它。
- **边只表达数据依赖**（上游 items 流向下游）。
- **项目不再持有隐藏的执行配置**，降为「容器 + 默认值」。
- **节点类型 = 声明式数据**（n8n `INodeTypeDescription`）：后端单源声明字段 schema，前端一个通用表单自动渲染，新增节点类型 = 一份 schema + 一个 execute 函数，零新 UI 代码。

**关键判断（读码确认）**：n8n 模型在本仓**已存在雏形**——自定义节点路径（`custom_node_types` → `{kind,params}` envelope → `runCustom` dispatch → `node_outputs` + `{{name}}`/varBindings）就是半个 n8n。全保真重设 = 把这套**泛化到所有节点 + 真 items/表达式引擎 + 二进制以引用进 items + 解耦项目配置**。

## 2. 现状（读码核实，作为迁移基线）

- **内置节点 project-coupled**：`worker.go:runScript` 从 `projects` 行读 `picturebook_config`/`kind`（`pictureBookConfig`，456-472）；`runStoryboard` 从项目行读 `style` + `picturebook_config`（508-537，B1 注释明确忽略 storyboard todo 的 input_json style）。这正是要消除的「隐藏执行配置」。
- **两套数据传递并存**：① 内置节点经**计划结构 `depends_on` + `output_ref` 字符串前缀**（`script:`/`shots:`/`custom:`）解析上游（`runStoryboard` JOIN 找父 script 485-489，`runPrescreen` 找最新文本父 1012-1019）；② 自定义节点用 **`varBindings` → `{{name}}` 替换**（`substituteVars` 1942，经 `resolveVariables`/`resolveOutputText` 1955-1978，`resolveOutputText` 显式拒二进制 `asset:`/`shots:`）。
- **`node_outputs` 仅文本**（`content TEXT`+`format` ∈ text/json/http-status，storage.go:467-475）。二进制走独立 `assets` 表 + 自己的状态机（generating→submitted→pending_acceptance）、blob 路由、成本账、HITL 验收、异步提交/轮询（worker.go 650-1508，约 850 行）。
- **两个 planner**：`Plan`（LLM 自动出图，仅 script+storyboard，带 `DefaultPipeline` 兜底）与 `PlanCustom`（用户显式图，n8n 式路径，已做 local→todo id 两遍改写 379-412）。
- **迁移机制**：`internal/storage/storage.go` 里有序幂等 DDL 字符串数组 `m1…m19`，`Migrate`（497），追加式 `IF NOT EXISTS`/`ALTER … ADD COLUMN IF NOT EXISTS`，无迁移框架。
- **前端** `PropertiesPanel.tsx` 硬编码 `showPrompt = type==='script'||'storyboard'`（158）+ `isTypedLlm/Http/Script` 分支；三个 per-kind 表单在 `web/src/features/custom-node-types/{Llm,Http,Script}ParamForm.tsx`。手搓、非 schema 驱动。

架构债清晰：**两套数据模型（node_outputs 文本 vs assets 二进制）、两套上游解析（depends_on/output_ref vs varBindings）、两个 planner、项目即隐藏配置**。全保真 n8n 模型统一这一切。

## 3. 目标架构

### 3.1 节点类型描述 + 参数 schema 框架（核心，唯一值得新建的基建）

新叶子包 `internal/nodedesc`（sibling 于 `builtinnode`，不 import studio 树任何包，避免成环）：

```go
type NodeTypeDescription struct {
    Type        string     `json:"type"`        // "studio.script"/"studio.storyboard"/"studio.asset"/"studio.prescreen"/"llm"/"http"/"script" + custom slug
    Version     int        `json:"version"`     // 节点类型版本（n8n typeVersion，老 workflow 前向兼容）
    Label       string     `json:"label"`
    Description string     `json:"description"`
    Group       string     `json:"group"`       // "generation"|"transform"|"io"|"trigger"
    Inputs      []PortSpec `json:"inputs"`      // 连接端口（main；未来命名端口）
    Outputs     []PortSpec `json:"outputs"`
    Properties  []Property `json:"properties"`  // 参数 schema —— 核心
}

type Property struct {
    Name           string          `json:"name"`
    Label          string          `json:"label"`
    Type           PropertyType    `json:"type"` // string|textarea|number|boolean|options|collection|fixedCollection|json|prompt|template|code
    Default        json.RawMessage `json:"default,omitempty"`
    Required       bool            `json:"required,omitempty"`
    Options        []OptionItem    `json:"options,omitempty"`        // type=options
    DisplayOptions *DisplayOptions `json:"displayOptions,omitempty"` // 条件显隐
    TypeOptions    *TypeOptions    `json:"typeOptions,omitempty"`    // rows/editor=starlark/password/templatable…
    Placeholder    string          `json:"placeholder,omitempty"`
}

// 条件显隐：键=同节点其它参数 name，值=允许值数组（数组内 OR、跨键 AND），show/hide 可组合
type DisplayOptions struct {
    Show map[string][]json.RawMessage `json:"show,omitempty"`
    Hide map[string][]json.RawMessage `json:"hide,omitempty"`
}
```

**注册表**合并两类描述：① 编译进 `nodedesc` 的静态内置描述（script/storyboard/asset/prescreen + 泛化的 llm/http/script kind），**取代 `builtinnode.Catalog()`**；② `custom_node_types` 的 org 注册行——每行 `kind` 映射到一个**基描述**（其 properties 为该 kind 的 schema），行的 `params` 成为放置节点时的**默认值**。

**端点** `GET /api/node-types` 返回合并后的 `[]NodeTypeDescription`（静态内置 + org 自定义），**取代** `GET /api/node-types/builtin` 与 custom-node-types 列表。前端一个通用 `<PropertiesForm description value onChange>` 走 `properties`、遵守 `displayOptions` 渲染，**取代** `showPrompt`/`isTypedLlm/Http/Script` 分支与三个表单。

### 3.2 数据-item 模型（node_outputs → items 数组）

把 `node_outputs` 从「每 todo 一条文本」泛化为「每次节点执行一组有序 typed items」（镜像 n8n `INodeExecutionData`）：

```
node_outputs（重写）:
  id, project_id, todo_id, type,
  items JSONB NOT NULL DEFAULT '[]',   -- [{json:{...}, binary:{<port>:BinaryRef}, pairedItem?:{item:int}}]
  created_at
```

每 item = `{ "json": <object>, "binary": { <portName>: <BinaryRef> }, "pairedItem": {item:int} }`。`pairedItem` 记录血缘（哪个上游 item 派生而来，n8n 跨分支链路）。

### 3.3 二进制 items —— 引用 `assets` 表，绝不内联字节（载重决策）

```go
type BinaryRef struct {
    AssetID  string `json:"assetId"`  // 指向既有 assets 表
    MimeType string `json:"mimeType"`
    Kind     string `json:"kind"`     // image|video|audio
}
```

`assets` 全生命周期（blob 路由 / 异步提交轮询 / 成本账 / HITL 验收 / `storage_config_id` 解析）**原样不动**。二进制 item 是指向该机器的**薄指针**——图像/音频/视频以 item 流动而**无需重写 850 行异步生成**。这与 n8n 生产模式的二进制外置一致。

### 3.4 表达式引擎（替换窄 `{{name}}`+varBindings）

新叶子包 `internal/expr`，n8n 式 per-item 解析 `{{ $json.x }}`、`{{ $node["NodeId"].json.y }}`、`{{ $items(...) }}`、`{{ $binary.image }}`：

```go
type Context struct {
    Self     []Item                          // 当前节点输入 items
    NodeByID func(id string) ([]Item, error) // 按节点 id 惰性取上游输出
    ItemIdx  int                             // per-item
}
func Resolve(template string, ctx Context) (string, error)
```

**作用域决策（载重）**：**不嵌 JS VM**。实现**受限文法**：成员访问（`$json.a.b`）、数组下标、`$node["X"]`、`$binary`、字符串拼接、小白名单 helper（`$now`、`.toLowerCase()`、`JSON.stringify`）。覆盖 n8n 90% 用法、确定性、沙箱安全（无 `eval`）、与既有 Starlark `scriptengine` 组合处理重变换。`{{secret:NAME}}` 仍是**独立通道、表达式之前先解析**（保住 `runCustomHTTP` 的 editor→admin admin-gate，worker.go:1779-1814）。

### 3.5 连接驱动执行 + 统一执行器契约

结构上已成立：`depends_on[]` 是边集，worker 认领 deps 已 `done` 的 `ready` todo。改的是**边上流什么**：每个节点执行器收到统一**输入捆**=各上游节点发出的 items（按节点 id 寻址）。

- 新 `loadInputs(ctx, todoID)`：读每个 dep 的 `node_outputs.items`。
- 执行器签名统一：`(inputItems []Item, params ResolvedParams) → (outputItems []Item, err)`。`runCustom` 的 kind-switch 泛化，内置类型同自定义 kind 一样注册。
- `runStoryboard` 扇出保留（合法的 n8n「一 item 进多 item 出」+ 动态生成节点），但 `style` 从**节点参数**读，不读项目。
- `output_ref` 字符串前缀逻辑 + `resolveOutputText` **删除**；`resolveVariables`/`substituteVars` **替换**为 `expr` 引擎 over `loadInputs`。
- `scripts`/`shots` 表**留作投影**（内置执行器仍写，供素材库/运行视图），但**节点间正典通道是 `node_outputs.items`**。

### 3.6 每个节点统一为 schema 驱动模型

| 节点 | 描述来源 | Properties（schema） | 产出 items |
|---|---|---|---|
| `studio.script` | 静态 | `brief`/`contentType`/`targetPlatform`/`style`/`pictureBook`(bool→`displayOptions` 揭示 `ageBand`/`bookType`/`themes`)/`systemPrompt` | `[{json:ScriptOutput}]`；另写 `scripts` 投影 |
| `studio.storyboard` | 静态 | `style`/`pictureBook`→`maxWordsPerSpread`/`illustrationStyle`/`systemPrompt` | 每镜一 item `[{json:Shot}]`；写 `shots` 投影；扇出 asset |
| `studio.asset` | 静态 | `kind`(options image/video/audio)/`prompt`/`style`/`voice`/`duration` | `[{json:{}, binary:{out:BinaryRef→assets}}]` |
| `studio.prescreen` | 静态 | `outputFormat` | `[{json:ReviewOutput}]` |
| `llm`/`http`/`script`（自定义 kind） | 基描述 + org 行 params 作默认 | 现有 Llm/Http/ScriptParams → Properties | `[{json:…}]` |

### 3.7 项目配置降为节点默认值

`projects` 行保留 storage/model/org 配置（org/项目基建，正确地 project-scoped）。但 `brief`/`style`/`contentType`/`targetPlatform`/`picturebook_config`/`kind` 的**工作流执行含义移到节点**。迁移（§4）读项目当前配置，**烘焙进该项目工作流的 script/storyboard 节点参数**。新项目：放置节点时描述的 `default` 可用表达式默认引用项目字段（`{{ $project.style }}`），但一旦放置，值**在节点上**，执行期不再读项目。`runStoryboard` 的 B1 项目-style 读 + `pictureBookConfig` 删除。

## 4. 原地迁移策略（无并行引擎）

### 4.1 什么破、怎么转

**绘本流程会破**（runScript/runStoryboard 不再读项目 picturebook/style）。靠**回填节点参数**迁移：

- **`m20`**：遍历每个 `workflows` 行，改写 `nodes` JSONB——每节点加 `parameters` 对象；script 节点拷所属项目 `brief`(description)/`contentType`/`targetPlatform`/`style`，若 `kind='picturebook'` 拷解析后的 `picturebook_config` 字段；storyboard 节点拷项目 `style` + 绘本跨页/插画参数；`varBindings` `{name,sourceNodeId}` → 表达式 `{{ $node["<sourceNodeId>"].json.text }}`（机械改写 `{{name}}` token）；每节点加 `version:1`。
- **`m21`**：`ALTER TABLE node_outputs ADD COLUMN items JSONB NOT NULL DEFAULT '[]'`；回填 `items = jsonb_build_array(jsonb_build_object('json', jsonb_build_object('text', content, 'format', format)))`。`content`/`format` 留一版（双写）后续迁移再删——追加优先，合仓 `IF NOT EXISTS` 姿态。

两者纯前向、幂等，追加进 `m1…m19` 链。**无并行 v2 schema**。

### 4.2 planner/worker 改造

- **`PlanCustom` 泛化**：不再特判 script/storyboard 注入 brief（已在节点 parameters），不再 varBindings 两遍——统一把每节点 `parameters`（含引用 local 节点 id 的 `$node[...]` 表达式）写进 `todos.input_json`；local→todo id 改写仍做，但统一扫所有表达式 `$node["localId"]` token（复用既有 idMap）。
- **`Plan`（LLM planner）降级不删**（用户许可改变自动规划行为）：正典创作路径 = 画布显式节点图；LLM planner 变成**「生成起始图」动作**，产出 `workflows.nodes`（带 parameters）供用户编辑——即 planner 成为**喂同一节点模型的图生成器**，而非独立执行路径。删 `DefaultPipeline` 兜底执行路径。
- **worker 执行器统一**：签名改为 `(inputItems, params)→outputItems`；新 `loadInputs` 读上游 items；built-in 同 custom kind 注册；storyboard 扇出保留但 `style` 从节点参数读。**异步 asset 机器（提交/轮询/成本/HITL）原样不动**——二进制 item 引用 asset id，asset 执行器仍驱动状态机（刻意的复用边界）。

### 4.3 用户可感知的行为变化

迁移后老绘本/自动规划项目把配置**带在节点上**、跑出一致结果。可接受的行为变化：重跑迁移后的项目从节点读配置（故改项目 `style` 不再追溯改既有工作流渲染——这正是用户要的 n8n 语义）；自动规划按钮产出**可编辑的图**而非不透明 LLM 运行。

## 5. 分阶段路线（leaf-first，每阶段独立可发布可测，branch→PR，fresh DB `-p 1`）

- **P1 — 去风险：节点类型描述框架，只读（不改执行）**。建 `nodedesc` 包 + 全 7 类静态描述；`GET /api/node-types` 返回合并描述；前端建通用 `<PropertiesForm>` 驱动渲染并切 `PropertiesPanel` 到它（初期产出同样的 promptId/promptText/typed-param 值）。无 DB/无 worker 改动。纯追加、可测；回归只触及 UI 渲染。*验证：既有 canvas/run 测试绿 + 新 schema 渲染测试。*
- **P2 — items 模型（双写）**。`m21`（加 items、回填）。worker 同时写 legacy `content/format` 与 `items`。加 `loadInputs`。`internal/expr` 引擎（受限文法），作为**备选解析器**behind flag，用既有 `substituteVars` 测试语料（worker_custom_test.go）做 parity。*验证：`{{name}}`↔`{{ $node[...].json }}` parity；items 往返。*
- **P3 — 节点参数 + 项目配置迁移**。`m20`（回填节点 parameters + varBindings→表达式）。`PlanCustom` 持久化 parameters；worker 执行器经 expr over `loadInputs` 读参数，删 `pictureBookConfig`/B1 项目-style/varBindings 两遍。二进制 item：asset 执行器产 `{binary:{out:BinaryRef}}`，storyboard 扇出与 prescreen 消费 items。*验证：迁移后绘本项目产同样 script/shots/assets；DB-backed worker 测试 fresh DB `-p 1`。*
- **P4 — 退役 legacy 路径 + planner 降级**。删 `output_ref` 前缀解析 / `resolveOutputText` / `substituteVars` / `builtinnode.Catalog`（被 `nodedesc` 取代）；删 `node_outputs.content/format`（迁移）。`Plan` 降为「生成起始图」端点产 `workflows.nodes`；删 `DefaultPipeline` 兜底。*验证：画布端到端 run；LLM 生成图可编辑可跑。*
- **P5 — n8n 创作打磨**。运行画布 per-item 检视器、`displayOptions` 条件 UI 全接、表达式自动补全引用上游输出、`resourceLocator` 选 prompt/secret/storage。纯追加 UX。

## 6. 关键风险 + 载重决策

1. **表达式引擎作用域 — 决策：受限文法，非 JS VM**。全 JS（goja）最保真但加重依赖 + 沙箱逃逸面 + 非确定性。受限文法（`$json`/`$node`/`$binary`/成员访问/白名单 helper）覆盖主流用法、近 stdlib、与 Starlark `scriptengine` 组合处理重变换、`{{secret:}}` admin-gate 作独立前置 pass 保留。**欠作用域风险**：用户期望任意 JS。缓解：`script`（Starlark）节点是文档化逃生舱。**最重要的作用域抉择。**
2. **二进制 item — 决策：引用 `assets` 表、绝不内联**。重写 n8n 二进制存储会复制 ~850 行异步/成本/HITL/blob 机器。薄 `BinaryRef`→asset 指针保住全部。**风险**：item 非自包含（二进制 item 仅在有 DB 时有意义）。可接受——n8n 生产模式二进制也外置。
3. **删 LLM planner — 决策：降级不删**。最干净单执行路径且不丢 brief→图 能力。**风险**：留比删多写码。低改造、高产品价值。
4. **数据迁移风险（最高操作风险）**。`m20` 改写每个 `workflows.nodes`，`m21` 回填 `node_outputs`。bug 会搁浅既有绘本项目。缓解：迁移幂等追加（新列/键，老的留一版）；双写窗口（P2）先验 items-parity 再退役 legacy；用真项目行快照在 fresh DB 测。
5. **`displayOptions` 前后端语义 parity**。schema 在 Go、须 TS 渲染一致（同 `nodeColor.parity.test.ts` 模式）。缓解：parity 测试断言 `/api/node-types` 载荷驱动所有可见配置。
6. **安全规则不可丢**：schema 驱动校验须保住 `customnodetype/store.go` 现有规则（http url 必静态字面量、`{{secret:}}` 仅 header、script 禁 secret）——作 schema 字段约束移植，不删。`customnodetype` 按仓规须**独立安全评审**。`{{secret:}}` 通道 + admin attestation 原样作表达式前置 pass。
7. **`HasUnboundCustomNode`（注解 vs 可运行）须续работа**：内置节点恒「已绑」。schema 框架须把内置视为恒可运行，使 `runWorkflowHandler`（166）run-gate 不受影响。

## 7. 复用图

| 既有件 | 去向 | 备注 |
|---|---|---|
| `assets` 表 + 异步提交/轮询/成本/HITL/blob（worker.go:650-1508） | **原样复用** | 二进制 item 锚点；`BinaryRef`→`assetId` |
| `todos` 队列 + `depends_on[]` + claim/lease/retry | **原样复用** | 边已驱动执行顺序 |
| `node_outputs` 表 | **泛化** | `content/format` → `items JSONB`；成节点间正典通道 |
| `custom_node_types`（注册表 kind+params + org 隔离） | **泛化** | 每 kind → 基 `NodeTypeDescription`；行 params → 节点默认 |
| `builtinnode.Catalog()` | **替换** | 被 `nodedesc` 静态描述吸收 |
| `varBindings`+`substituteVars`+`resolveOutputText`+`output_ref` 前缀 | **替换** | 被 items + `expr` 引擎 + `loadInputs` |
| `PlanCustom` 两遍 id 改写（379-412） | **泛化** | 一遍扫 `$node["localId"]` 表达式 token |
| `Plan` LLM planner + `DefaultPipeline` 兜底 | **降级** | 成起始图生成器；兜底执行路径删 |
| worker per-type 执行器（runScript/Storyboard/Prescreen/Custom*） | **重构为统一签名** | `(inputItems,params)→outputItems`；删项目 JOIN，参数走表达式 |
| `scripts`/`shots` 表 | **留作投影** | 内置执行器仍写供素材库/运行视图；非节点间通道 |
| `pictureBookConfig`/项目-style 读（worker.go:456-537） | **删除** | 配置移到节点参数（`m20`） |
| Llm/Http/ScriptParamForm + PropertiesPanel 分支 | **替换** | 通用 `<PropertiesForm>` 走 `properties` |
| `scriptengine`（Starlark） | **复用 + 抬升** | 受限表达式之外重变换的逃生舱 |
| `{{secret:}}` 通道 + `secretRefRe` + admin attestation | **原样复用** | 表达式前置 pass，保 editor→admin gate |
| 迁移链 `m1…m19` | **扩展** | 追加 `m20`（节点参数回填）+ `m21`（items 回填），幂等/追加 |

## 8. 非目标

- **不嵌 JS 表达式 VM**（受限文法 + Starlark 逃生舱；见决策 1）。
- **不重写异步 asset 状态机**（二进制以引用进 items，复用边界）。
- **不删 LLM planner**（降级为起始图生成器）。
- **不改 BYOK 模型路由 / Keystone K5**（capabilities per provider×model 不动；per-node `model` 覆盖是 n8n 式逃生舱，已支持）。
- **不引入 K8s/Helm 打包**（仓级非目标）。

## 9. 自检

- 覆盖用户诉求：节点自配置（§3.1/3.6/3.7）、边只数据依赖（§3.5）、n8n 节点设计参考（§3 全，A agent 校准）。✓
- 全保真选择落地：items（§3.2）+ 表达式引擎（§3.4）+ 二进制 items（§3.3）。✓
- 原地迁移：§4，零并行引擎，m20/m21 幂等追加。✓
- 风险载重决策显式（§6），安全规则不丢（决策 6）。✓
- 复用边界清楚（§7），异步 asset 机器不动。✓

## 10. 第一轮评审修订（Plan agent 对抗式评审纳入）

评审验证了 §3-§9 对真实代码的假设，确认 **§3.2/§3.5 把 `node_outputs` 当"现有节点间通道"是错的**（内置节点输出在 `scripts`/`shots`、经 `output_ref` 解析，从不写 `node_outputs`），以及多处"删 legacy"步骤排序过早。逐条修订（B=阻断、I=重要）：

- **B2（修正核心认知）**：`node_outputs` **不是**内置节点的节点间通道。`runScript` 只写 `scripts` 行返 `script:<id>`、`runStoryboard` 写 `shots`、二进制走 `assets`；`resolveOutputText` 从 `scripts.content_json`(script:) / `node_outputs.content`(custom:) 取。**修订**：让内置节点产 `node_outputs.items` 是**净新增的发射逻辑**（不是 P2 描述的"双写"——双写只对自定义路径）。P2 范围扩大：给 script/storyboard/asset 执行器加 `node_outputs.items` 发射；`m21` **无法**回填历史内置输出（它们从不在 node_outputs）——接受老运行的内置输出不入 items，或额外从 `scripts`/`shots` 回填。

- **B3（output_ref 不可删，只退役 custom:-text 分支）**：`output_ref` 是 `todos` 列，每个执行器经 `MarkDone` 写，被多处用：storyboard 父 script 解析（`WHERE t.output_ref LIKE 'script:%'`）、`discardCanceledAsset` 解析 `asset:<id>`、`todo_finished` 时间线事件载荷、自定义变量解析 seam。**修订**：`output_ref` **保留**作 todo/asset 结果指针；只在所有消费者迁到 items 后退役 `resolveOutputText` 的 **custom:-text 分支**。删除前须枚举并先迁移：storyboard 父解析 + `discardCanceledAsset`。

- **B4（自动规划-only 绘本项目排序陷阱）**：`CustomWorkflowEnabled=false` 项目**没有 `workflows.nodes` 行**（图由 `Plan`/`DefaultPipeline` 运行期 LLM 生成）；`m20` 只改 `workflows` 行 → 对它们回填不了；而 worker 一旦停读 `picturebook_config` → 绘本生成静默破。**修订**：`Plan`→"生成可编辑带 parameters 图"的降级**必须先于**worker 停读项目配置落地；过渡期对 NULL-parameter 节点保留 `pictureBookConfig` 兜底。把"删项目配置读"从 P3/P4 推后到 planner 降级之后。

- **B5（characterSheet 是运行期跨节点流，非项目配置）**：characterSheet 由 ScriptAgent 运行期生成、序列化进 `scripts.content_json`、`runStoryboard` 从上游 script 内容再解析——**不在** `picturebook_config`、运行前不存在。**修订**：m20 无从烘焙它；它须留作 **script→storyboard 的 inter-node item 数据**（storyboard 消费 `$node["script"].json.characterSheet`），这**依赖 B2（内置节点发 items）先完成**。删去"移到节点参数"对 characterSheet 的暗示。

- **I1（/state 运行视图直读 node_outputs.content/format）**：`project/store.go` 的 state 查询 select `no.content, no.format` → `projectstate.GraphNode.Output/OutputFormat` → SSE `/state` 契约 → 前端运行面板渲染。**修订**：P4 删 content/format 列前，先把该查询迁成从 `items` JSONB 抽文本并调整投影。补全读者枚举：写者=runCustomLLM/HTTP/Script；读者=state 查询 + resolveOutputText custom 分支。注意 `format='json'` 行回填成 `{json:{text,format}}` 会丢 `$json.field` 访问——决定是否把 JSON-format 内容解析进 `json` 对象。

- **I2（storyboard 扇出是运行期动态 todo + 资产就绪缺口）**：扇出经 `AddDynamic` 运行期建 N 个 asset todo（`status='ready'`, `depends_on=[storyboardTodoID]`）；asset 行/字节要等异步机器（generating→…→pending_acceptance→HITL），**todo 在 submit 即 done、非 accept**。下游消费 BinaryRef 会拿到无已接受字节的资产。**修订**：定义 asset-item 发射时机（accept 时？submit 时？）；明确扇出 asset 节点**非静态可寻址**（不能 `$node["assetNodeId"]`）；下游消费二进制 items 是否在 P1-P4 范围**先搁置/后置**。

- **I3（`{{ }}` 与 `{{secret:}}` 分隔符冲突，须可测规则）**：表达式引擎与 secret 通道都用 `{{ }}`。**修订**：① secret pass **只在作者模板**上跑、且**只跑一次**；② 表达式引擎把 `secret:` 当**非表达式字面量**跳过（或两通道用不同分隔符）；③ 经 `$node` 解析进来的值含字面 `{{secret:}}` 不得触发第二次 secret pass。把 B2 课（可信通道先于不可信）重述为可测不变量。

- **I4（安全校验从命令式→schema 约束须设计，非断言）**：`validateHTTPParams`/`validateScriptParams` 是命令式跨字段检查（url 必静态字面量、`{{secret:}}` 仅 header、script 禁 secret、method/outputFormat 枚举），简单 `Property` schema 表达不了。**修订**：定义约束词汇（`noTemplate`/`noSecret`/`secretAllowed`），**命令式校验器留作 schema 背后的执行层、不删只前置**；落这步的阶段须**独立安全评审**门禁；http/script 保存校验器不删。

- **I5（迁移机制是 DDL-only 字符串列表，装不下 m20 数据迁移）**：`Migrate` 跑 `[]string` DDL 经 `pool.Exec`；m20（逐 `workflows.nodes` JSONB 解析 + 解析 TEXT 编码 picturebook_config + `{{name}}`→`$node[...]` token 改写 + 加 version）不是单条幂等 SQL。**修订**：m20 须是 **Go 编码的迁移步骤**（现链无此框架）——决定是给 Migrate 加"Go 迁移步"扩展，还是单独迁移命令；这是 spec 自承"最高操作风险"项，且迁移 runner 本身需扩展。

- **N2/勘误**：`prescreen` 作独立内置节点 + `runPrescreen` 在 #108 引入（基线前提已声明）；line 引用须实现期对含 #107/#108 的代码重新核对（评审指出多处行号是对不含两 PR 的快照所写）。`HasUnboundCustomNode`：内置已返 false，§6.7 的 run-gate 担忧**已自然满足、无需改动**。

**修订后排序铁律（覆盖 §5 phasing）**：
1. 任何"删 legacy"前，先让内置节点发 `node_outputs.items`（B2）+ 迁移每个 `output_ref`/`content/format` 消费者（B3/I1）。
2. `Plan` 降级为"生成带 parameters 图"**先于** worker 停读项目配置（B4）；过渡期保 `pictureBookConfig` 兜底。
3. characterSheet 作 inter-node item，依赖 B2（B5）。
4. m20 走 Go 编码迁移步，先扩展 Migrate runner（I5）。
5. 安全约束词汇 + 独立安全评审门禁落在引入 schema 驱动校验的阶段（I4）；secret/表达式分隔符规则可测化（I3）。

**结论**：spec 方向成立，但**修订前不可直接转 P1 实现计划**——P1（nodedesc + `GET /api/node-types` + `PropertiesForm`）仍是最可先发的薄片（纯 UI 渲染、产出同样的 promptId/promptText/typed 值），但其 schema 须先编码现有安全约束（I4）才能驱动真正的参数保存（"产出同样值"仅对只读渲染成立）。"删 legacy"步骤按上述铁律重排。

## 11. 第二轮评审修订（4-agent 多镜头对抗式评审，逐条核对真实代码）

四个并行评审镜头（迁移安全 / 安全 / 数据模型 / API+前端）各自对 HEAD 真实代码核实。**一致结论：四镜头全判"按当前 spec 不可直接实现"**。§10 抓对了大方向，但把三处载重问题留成"待决"而非"已决"，且 §5 phasing 未与自己的铁律对齐。新增阻断按主题归并（标注哪个镜头发现）：

### 11.1 迁移机制（多镜头汇聚的最高风险）

- **M-1（迁移 runner 无版本表/无事务/每次启动重跑）[阻断｜迁移+数据双镜头独立命中]**：`storage.Migrate` 把所有 `mN` 摊成单个 `[]string` 经 `pool.Exec` 逐条跑，**无包裹事务、无 `schema_migrations` 版本表**，每次 studiod 启动全量重跑（`main.go` 启动期无锁调用）。现有幂等全靠各 DDL 的 `IF NOT EXISTS`。Go 编码的 m20（改写每行 `workflows.nodes` JSONB）**没有这种天然幂等**：第二次启动会重跑——若 `{{name}}`→`$node[...]` token 改写非纯函数，会把已改写文本再改写成乱码，或把 parameters 重盖到用户两次启动间的编辑上。**修订**：m20 落地前先给 runner 加 (a) `schema_migrations(version TEXT PK, applied_at)` 表 + 每个 Go 步插入成功标记/已存在则跳过；(b) 每步包裹显式事务；(c) 启动期 advisory lock 防多副本并发同一 in-place UPDATE；(d) m20 用每节点 `version` 哨兵幂等（已迁过的节点跳过）。"加一个 Go 迁移步"远不够。

- **M-2（m21 对 `format='json'` 行回填有损，且决策仍开放）[阻断｜迁移+数据双镜头命中]**：m21 统一把 `node_outputs` 回填成 `{json:{text:content, format:format}}`。但 `runCustomLLM` 的 `format='json'` 行 `content` 本就是序列化 JSON 值；包成 `{text:"<json字符串>"}` 后，下游 `{{ $json.field }}`（items 模型存在的全部理由）只看到字符串、取不到字段。`format='http-status'` 同病。**这恰恰对要服务的那批结构化行失效**。§10 I1 标了却以"决定是否…"收尾。**修订**：m21 按 format 分支——`format='json'`→`json = content::jsonb`（解析失败回退 `{text:content, _parseError:true}`，绝不让迁移半路失败）；`format='text'/'http-status'`→`{text:content}`。此分支即 m20/m21 必须走 Go 编码步的另一理由（SQL `jsonb_build_object` 装不下"解析可能失败"）。

- **M-3（§5 phasing 与 §10 B4 铁律自相矛盾）[阻断｜迁移镜头]**：§10 铁律 #2 明令 planner 降级须先于停读项目配置，但 §5 仍把"删 `pictureBookConfig`/项目-style"放 P3、"Plan 降为生成起始图"放 P4——正是 §10 禁止的倒序。spec 正文从未与自己的评审响应对账。**修订**：重写 §5，使 planner 降级 + LLM 图落成可被 m20 回填的 `workflows` 行**先于或同阶段于**移除 `pictureBookConfig`；在 LLM-planner 项目产出 `workflows` 行前，`runScript`/`runStoryboard` 必须保留 `pictureBookConfig` 兜底。

- **M-4（跨部署边界的在途运行无人处理）[阻断｜迁移镜头]**：部署瞬间处于 `running`/`ready` 的 todo：其 `script` 可能在旧代码下完成（写了 `scripts`、返 `script:<id>`、**无 `node_outputs.items`**），随后 `storyboard` 被新 worker 接管、`loadInputs` 期望上游 `node_outputs.items` → 拿到空 → storyboard 无输入跑或报错。§10 B2 的"接受老运行不入 items"punt 实际等于**每个跨部署的运行下游全破**。**修订**：要么 (a) 部署 worker 变更前排空/静默 todo 队列（运维 runbook，当前缺）；要么 (b) `loadInputs` 在上游 `node_outputs.items` 空时回退到 `scripts`/`shots`/`output_ref` 投影——这意味着 legacy 读路径在队列完全循环前不可删，收紧 P4 退役门禁。

- **M-5（无回滚路径；m20 in-place 改写是单向门）[阻断｜迁移镜头]**：`Migrate` 纯前向（无 down、无版本）。m20 一旦原地改写 `workflows.nodes` + m21 改 `node_outputs` 形状，坏部署无法回滚：旧二进制期望 `pictureBookConfig` 读 + 干净 `WorkflowNode`（无 `parameters` 字段），但数据已带新形状。§10 P2 的"双写窗口"只覆盖自定义路径的 content/format，**不覆盖 m20 的 in-place 节点改写**。**修订**：m20 改为**只增不改**——写进**新键**（如 `node.parametersV2`），旧字段原样保留，旧二进制照常工作、回滚=部署旧二进制；新二进制验证通过后再收敛到 canonical `parameters`。

- **M-6（m20 须从 project↔workflow join 合成内置节点 params，非 1:1）[次要｜迁移镜头]**：内置 `WorkflowNode` 今天零配置，brief/style 来自 project 行 + plan 期 `Brief` 参数。m20 须从每个 `workflows` 行 reach 到 `projects` 行把 brief/style/picturebook_config 烘焙到 script/storyboard 节点。但 m12 起是 1:N（一项目多 workflow）。**修订**：显式规定 m20 的 join 语义 + 对无 script/storyboard 节点的 workflow（草稿/部分图）定义行为（跳过不报错）。

### 11.2 安全（安全镜头，载重——§3.7 把危险参数移上节点摧毁了现有不变量）

> 现状安全模型（已核实）：编辑者**从不**控制危险字段。worker 绑定的参数来自 **org-scoped `custom_node_types` 注册表**（保存期 `validate*` 校验一次），节点只供 `TypeId`(引用) + `VarBindings`(且 `sourceNodeId` 必在 `dependsOn`)。worker 运行期**从不**重跑校验器。secret 在 **worker 运行期**对**注册表作者模板串**解析、`orgID` 来自可信 run 上下文、**从不落库**。输出读取（`resolveOutputText`/`resolveVariables`）全是 **bare-id `WHERE id=$1`、零 org/project scope**——今天安全纯因 id 只来自同项目 plan 图的 `idMap`，从不由攻击者选。

- **S-1（危险参数移上编辑者可控节点，无运行期重校验）[阻断]**：§3.7 把 `url`/`headers`(含 secret)/`code` 移进 `workflows.nodes[].parameters`（编辑者可控 JSON），validated-registry 不变量消失。I4 说"校验器前置"=**仅保存期**；spec 从未要求 worker 运行期对**节点参数**重跑 `validateHTTPParams`/`validateScriptParams`。**攻击**：org 编辑者（能改 workflow 的最低权限）把 http 节点 `url` 设 `http://attacker/collect`、header `Authorization: {{secret:STRIPE_KEY}}`、`allowResponseBody:true`，或直接 `PUT` workflows.nodes JSON 绕过画布——worker 执行即外泄密钥。**修订**：spec 须明确威胁模型行——"编辑者可直接写 workflows.nodes JSON，每条危险约束须在保存**与**运行两处对不可信输入成立"；决定危险字段是 registry-only（节点只给 typeId 引用 + 安全 override）还是上节点（则 worker 运行期必重校验 + 保存端校验 per-node override 而非 base description）。

- **S-2（`$node["id"]` + `NodeByID` 是无 scope 的跨租户/跨项目读原语）[阻断]**：§3.4 引入 `$node["NodeId"].json.y`、`NodeByID func(id) ([]Item,error)`，表达式串编辑者自由文本，spec 对 scope **只字未提**。若 `NodeByID` 像现 `resolveOutputText` 那样 bare-`WHERE id=$1`，则 org A 编辑者写 `{{ $node["<org-B-id>"].json.text }}` 读到 org B 输出（含 S-3 落库的 secret）。id 虽 128-bit 随机但经时间线/SSE `/state`/错误信息泄漏；即便同 org，`$node["X"]` X 不在 `dependsOn` 也读到不该见的兄弟分支。**本仓有跨租户写历史**（见 memory bug-backlog）。**修订**：硬不变量写进 expr 引擎契约——`NodeByID` (a) 只解析当前 run plan 图内、按 `project_id`(传递 `org_id`) scope 的 id；(b) 拒绝任何不在执行节点传递 `dependsOn` 集的 id（复用 planner 的 depSet 检查）。加跨租户 parity 测试进安全门禁。

- **S-3（I3"secret 作者期解析一次"措辞要么与代码矛盾、要么把明文密钥落库）[阻断]**：现实是 secret **运行期在 worker** 解析、对**注册表作者模板**、orgID 来自可信上下文、**从不落库**。I3 若字面理解为"保存期解析"→解析后明文 secret 写进 `workflows.nodes`/`node_outputs`，**静止明文存在 PG + 备份 + 每帧 `/state` SSE + 时间线**——比现状严重得多。**修订**：spec 须逐字声明——secret **只在运行期、worker 内、对注册表作者模板串、按可信 run-context orgID 解析；解析后明文绝不落 `node_outputs.items`/`workflows.nodes`/时间线/`/state`**；不变量="已解析 secret 只在 worker 进程内存活一次出站请求时长"。items 模型若让 header/body 流经 `node_outputs.items`，存的须是**替换前**含 `{{secret:}}` 的模板、非替换后的值。

- **S-4（即便 I3 完好，secret 仍可经 items 下游外流——`{status}`-only 守卫不随 items 重构存活）[阻断]**：现守卫 `secretBearing && !allowResponseBody` → http 节点只存 `{"status":N}`，防 secret 经响应体落库/下游读。§3.2/§3.5 让 `node_outputs.items` 成 canonical 通道、执行器统一 `(inputItems,params)→outputItems`，spec **未把该 body-restriction 政策带进 items 模型**。**攻击**：header `X-Probe:{{secret:KEY}}` 指向 `https://attacker-requestbin.com`(公网 IP 过 SSRF 过滤)、`allowResponseBody:true`、下游 script 节点把 `$json` 拷进输出——secret (a) 发攻击者 (b) 落 items (c) 渲染运行面板。**修订**：`secretBearing`/`allowResponseBody` body 限制须显式带进统一执行器契约（secret-bearing 且 `!allowResponseBody` 的节点 items 只含 `{status}`），`secretBearing` 追踪是强制项非 per-kind 细节。

- **S-5（SSRF：I4 说 url 须静态字面量，但 §5 P5 引入 resourceLocator/表达式 url，无运行期静态校验）[主要]**：现 url 运行期防御仅 `strings.Contains(in.URL,"{{")` 字符串检查 + fetch 传输层禁非公网 IP（**但无 host 允许名单，任何公网 host 可达**）。P5 让 url 可 resourceLocator/模板，若 expr 引擎在该检查**前**解析 `{{ }}`，动态 URL 直达 fetch + S-4 secret-header → 任意公网 host SSRF + 外泄。**修订**：secret-bearing 节点 url 永远静态字面量，运行期解析后再断言无模板残留 + host 非表达式派生；resourceLocator 仅解析到固定允许集；考虑 per-org 出站 host 允许名单。

- **S-6（Starlark"逃生舱"沙箱配置只断言未规定，而 §6.1"非 JS VM"安全论证全押在它）[主要]**：现 `scriptengine` 锁死（禁 `{{secret:}}`、5s wall-timeout、无网无文件、错误不漏源码行）。§7"复用+抬升"Starlark 为通用重变换路径，若"抬升"放宽其可触达面（`$node[...]` 任意输出 / binary refs / 更大输入更长预算），现有保证须**作硬性要求重新声明**，否则未沙箱化逃生舱击穿整个"为安全避开 goja"论证。**修订**：§3.4/§6.1 加显式 Starlark 沙箱契约（无文件/无网/无 secret 注入/保留 wall-timeout/堆+输出大小有界/错误不透明），声明"抬升"不放宽任何一条；script 节点纳入 I4 独立安全评审门禁。

- **S-7（m20 改写每行 workflows.nodes 无校验 pass，迁移产出的节点绕过全部 save-time 校验器）[主要]**：m20 把 parameters（含 url/style/picturebook，S-1 未修则含 secret-bearing 字段）直写 `workflows.nodes`，绕过每个校验器。buggy m20 误编码或 token 改写失手 → worker 执行从未被任何校验器审过的参数。**修订**：m20 须对每个改写后节点跑它正在回填进的同一组参数校验器（失败则 fail 该行、不静默写非法参数）；这也逼校验器可对 node-parameter 形状调用（S-1 本就需要）。

### 11.3 数据模型（数据镜头）

- **D-1（typeVersion 声明在 description 上、却从不持久化到 node；schema 变更静默损坏老图）[阻断]**：spec 给 `NodeTypeDescription` 加 `Version int`、m20"每节点加 version:1"，但持久化的 `planner.WorkflowNode` **无 version 字段**。n8n 按 `type+typeVersion` 解释参数正是为了 type v1→v2 后老节点仍按 v1 语义读。spec 把 `Version` 带在 type 目录、却没给 node 实例钉版本字段——`studio.script` v1→v2 改一个 property 含义，每个老图按 v2 语义读 → 静默参数损坏。round-trip 是非严格 unmarshal（无 `DisallowUnknownFields`），故加的 `typeVersion` 键能存活——但无写者无读者。**修订**：`WorkflowNode` 加 `TypeVersion int`；`PropertiesForm` 保存须写它；执行器按 `(type,typeVersion)` 选 description。否则"version:1"是无人读的死元数据。

- **D-2（PropertiesForm 保存路径不 round-trip parameters/typeVersion，值搁浅 JSONB 跨版本无人消费）[主要]**：见 §11.5 B-A1（前端 whitelist 丢弃）+ 后端 `PlanCustom` 读 `VarBindings`/registry params 而非节点 `parameters`。**修订**：把 schema 字段（`Parameters json.RawMessage`、`TypeVersion int`）落到 `WorkflowNode` + PlanCustom 读路径，**先于**前端写它们，否则值跨发布搁浅。把新 `WorkflowNode` JSON schema 钉成显式交付物。

- **D-3（pairedItem 是 cargo-cult，无消费者，每行死重）[主要]**：§3.2 给每行加 `pairedItem:{item:int}`"记录血缘"。n8n 里它只因引擎用它做 `$items()` 回溯 + fan-in 配对才有意义；本 spec 无 merge 节点、无走 pairedItem 的 `$items()` 消费者、storyboard 扇出是唯一 fan 节点。每行持久化 pairedItem = 纯存储膨胀 + 误导后人的"维护中"假象。**修订**：按本仓 CLAUDE.md §2"不加未被要求的功能"——**先删 pairedItem**，有消费者再加（additive JSONB 键零迁移成本）；或显式规定哪个读者走它 + storyboard 扇出语义。

- **D-4（BinaryRef 指向 `pending_acceptance`、非 accepted 的资产；下游解引用未接受/已拒绝字节）[主要]**：asset todo 到 `done` 时 asset 行在 `pending_acceptance`，HITL 接受**更晚且可能拒绝**；asset 生命周期**无 accepted 终态之外的"done"**（见 memory asset-status）。若 asset-item 在 todo-done 发射，`BinaryRef{assetId}` 流向下游指向用户随后可能拒的字节。**修订**：BinaryRef 物化时带 asset `status`，或下游消费 gate 在 `status='accepted'`，或（P1-P4 最简）后置下游二进制 item 消费——则 item 里 BinaryRef 是 write-only 元数据须声明，读者勿信。钉死 `$binary.image` 在资产 pending/rejected 时解析成什么。

- **D-5（items 列 NULL/空纪律未对齐本仓 GORM 硬规则）[主要]**：本仓规则——NULL 列须 `[]byte`/`pq.StringArray` 中转、写用 `INSERT...RETURNING`。spec DDL `items JSONB NOT NULL DEFAULT '[]'` 方向对，但未规定 scan 路径；现有 JSONB scan 都走 `[]byte`→`json.RawMessage`。P2 双写窗口三处 INSERT（worker.go 旧 6 列 INSERT）须**新增 items 列**而非依赖 DEFAULT（DEFAULT 只为 m21 回填的历史行）。**修订**：显式规定 items 读经 `[]byte`→`RawMessage`、NULL/空当 `[]`；三处 INSERT 同语句写 items；加 CHECK/应用层守 items 恒为 JSON 数组。

- **D-6（characterSheet 在 items 模型里的落点未钉，撞 M-2）[次要]**：§10 B5 对，但 `$node["script"].json.characterSheet` 要求 script 执行器发的 item `json` 是完整 ScriptOutput 对象、非 `{text:<序列化json>}`——这与 M-2 的统一 text 回填冲突。**修订**：script 执行器新 items 发射须 `json = <ScriptOutput 对象>`（类型化契约 `studio.script` 发 `[{json: ScriptOutput}]`），即内置 item 发射从第一天起**类型感知**而非 text-wrap。

### 11.4 API + 前端渲染契约（前端镜头）

- **B-A1（`toStudioNodes` 字段 whitelist，每次保存静默丢 `parameters` + 任何未识别 Property）[阻断]**：`canvasModel.ts` 的 `toStudioNodes` 逐字段重建节点、只拷固定 whitelist（`id/type/promptId/dependsOn/position`+条件 `promptText/label/color/typeId/varBindings`），**无 `parameters`、无 preserve-unknown**。每次画布编辑→保存会剥掉新 params；老 web 客户端不识别的新 Property 也在保存时丢失=数据损坏。代码注释已记录他们被同款 bug 咬过（typeId/varBindings 曾丢失直到显式拷贝）。**修订**：spec 须强制 (a) round-trip 完整节点对象（`{...n, id, type, dependsOn, position}`）而非字段 whitelist；(b) parity 测试断言未知 Property 经 load→save→reload 存活。

- **B-A2（`displayOptions`+静态 `Default` 表达不了 picturebook age-band 级联默认）[阻断]**：`pbconfig.go` 里 `ageBand="0-3"` 派生 pageCount=8/maxWords=10/narration=repetition/bookType=concept 等；`displayOptions.show/hide` 只切可见性、**不能由一字段取值设另一字段值**。手搭 picturebook 节点无法复现级联。**修订**：要么给 schema 加派生默认机制（`Property.DefaultFrom`/按另一字段取值的 option-scoped 默认映射），要么把 `ParsePictureBookConfig` 级联留作后端解析步、表单不复刻并把 picturebook 记为非通用节点。

- **B-A3（Property 类型枚举漏掉现表单实际渲染的 widget）[阻断]**：现三表单含枚举无成员的载重 widget——① **prompt 选择器**（`__default__`/`__custom__`/`__create__` 哨兵 + 按 node kind 过滤库 prompt + basics vs org library 合并 + 内联"新建 prompt"调 `useCreatePrompt` 副作用 mutation）；② **HTTP headers key-value 编辑器**（动态增删行 + 每行"插入 secret"下拉源自 `useOrgSecrets`）；③ **`secretBearing` 条件 checkbox**（`allowResponseBody` 仅当任一 header 值匹配 `/\{\{secret:/` 才显——是对兄弟字段内容的计算谓词，`displayOptions.show` 表达不了）；④ **model 字段**（今 free-text，§3.6 暗示 catalog 选择器，`useModelCatalog` 存在但枚举无 `resourceLocator`）。**修订**：实现前逐 widget→Property-type 映射；定义 `prompt` 类型数据源+create-mutation 契约；加 `keyValue`/header 类型或规定 secret-insert 子 widget；把 `secretBearing` 正则谓词 reveal 换成显式 schema 规则或挪后端；钉 `model` 在 P1 是 string 还是 catalog resourceLocator。

- **B-A4（picturebook `characterSheet` 无表单表达，整流程 P1 无法手搭）[阻断｜与 D-6/B5 汇聚]**：手搭 picturebook 须 (1) script 节点设 pictureBook+ageBand/themes（B-A2 级联阻断）；(2) storyboard 节点经 `{{ $node["script"].json.characterSheet }}` 引用——依赖 B2（内置发 items），**不在 P1**；(3) maxWords/illustrationStyle 默认来自 age-band 级联。P1 只读表单全表达不了。**修订**：任何移除 picturebook-config-read 须 gate 在 B2 + 具体 characterSheet 表达式路径双满足；给出复现 3-6 age-band picturebook 的确切节点图+字段值范例；在此路径示范前 P1 不降级 picturebook 配置。

- **B-A5（`GET /api/node-types` 合并契约无冲突规则；自定义节点可遮蔽内置 type id）[主要]**：endpoint 合并内置静态 description + per-org `custom_node_types`，但未钉 `type`-名冲突谁胜。自定义类型 org 可写、只校验 kind/params 形状，无"不撞内置 `studio.*`"检查；前端现按 `type` 去重"typed 胜 annotation"，朴素套用合并则**自定义可胜内置** → 劫持画布渲染/执行。响应形状也未钉（无 version 信封/etag/缓存/排序/命名空间）。**修订**：`studio.*` 是保留命名空间；自定义强制 `custom:` 前缀且冲突时**内置永远胜**（create 期拒绝遮蔽）；钉响应 `{version, nodeTypes:[...]}` + 缓存头 + 稳定排序 + 前端 query-key/失效。

- **B-A6（表达式自动补全无 schema 驱动源；输出今天无类型）[主要]**：§3.4/P5 要 `{{ $node["id"].json.y }}` 自动补全，但前端无从知上游节点发什么字段——`NodeTypeDescription` 描述**输入** Properties 非**输出** JSON 形状。现唯一"上游"UI 是 varBindings Select（给 node id+label、零字段名）。无输出 schema → 表达式退化成盲打。**修订**：`NodeTypeDescription` 加输出 schema（per-output 声明 json 字段，如 `studio.script` 输出 `{title,logline,characterSheet,scenes}`），`PropertiesForm` 表达式编辑器从 `dependsOn` 上游 description 的输出 schema 取补全源；否则把表达式移出 P1-P4、保留 varBindings。

- **B-A7（命令式跨字段安全校验器无 schema 词汇；须 round-trip 到 run-gate）[主要｜与 S-1/I4 汇聚]**：见 S-1。前端镜头补充：前端镜像（`HttpParamForm` urlInvalid/secretBearing 内联错误）须可从 schema 复现否则丢失内联错误 UX。**修订**：`Property`/`TypeOptions` 定义约束词汇（`noTemplate`/`noSecret`/`secretAllowedIn:[headers]`），命令式校验器留服务端作执行层（schema 仅 UX 提示），schema 驱动保存阶段 gate 在 `customnodetype` 须的独立安全评审。

- **B-A8（`studio.asset` 节点今天无表单；P1"产出同样值"对它是空集）[次要]**：asset 节点今 `showPrompt=false`、非 typed、prompt/style/voice/duration 由 storyboard 扇出+项目填非表单。P1"产出同样值"对 asset 空洞——无现值可保 + 把这些暴露成可编辑字段是改变 asset 取参方式的净新增行为（撞 I2 扇出非静态可寻址）。**修订**：spec 标 `studio.asset` 为行为变更（非 like-for-like 迁移），澄清手配 asset 节点如何与 storyboard 动态扇出 asset todo 共存。

### 11.5 第二轮总结论

四镜头独立判"**当前 spec 不可直接转实现**"，方向仍成立。**P1 唯一安全可起的薄片 = nodedesc + `GET /api/node-types` + `PropertiesForm` 纯只读渲染**（不拥有保存路径）。转 P1 实现计划前，spec 须补：① 迁移 runner 加 `schema_migrations` + 事务 + 启动 advisory lock，m20 改"只增新键"+ 每节点 version 哨兵幂等（M-1/M-5）；② m21 按 format 分支回填 + 内置 item 发射类型感知（M-2/D-6）；③ 重排 §5 使 planner 降级先于停读项目配置（M-3），定义 `loadInputs` 回退 + 队列排空 runbook（M-4）；④ 加显式威胁模型节：危险约束在保存**与**运行双处对编辑者可控 JSON 成立、`NodeByID`/expr 读 project+org scope + dependsOn-gate、secret 仅运行期解析永不落库、`{status}` 守卫随 items 存活、Starlark 沙箱重声明（S-1~S-6）；⑤ `WorkflowNode` 加 `TypeVersion`+`Parameters` 并钉 on-disk 节点信封（D-1/D-2）；⑥ 前端 `toStudioNodes` round-trip 完整节点 + preserve-unknown + parity 测试（B-A1）；⑦ 逐 widget 补 Property 类型枚举（B-A3），picturebook 级联/characterSheet 留特例（B-A2/B-A4）；⑧ 删 pairedItem（D-3）。`studio.*` 命名空间保留 + 内置胜冲突（B-A5）。
