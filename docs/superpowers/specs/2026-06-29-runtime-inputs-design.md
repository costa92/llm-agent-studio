# 工作流运行期输入（Run-time Inputs）设计

**日期**：2026-06-29
**状态**：设计已批准，待两轮评审 → 写计划
**分支**：`feat/runtime-inputs`

## 目标

让同一个工作流（含绘本项目）在**不改节点定义/不改项目行**的前提下，run 时填一组类型化输入，既能注入自由变量（`{{input:name}}`），又能临时覆盖 brief 字段与绘本 PictureBookConfig——一次 run 一份输入、不可变、可复现。一个绘本项目由此可出多个变体（换音色/主题/页数）。

## 关键架构事实（带证据）

- **runHandler 分支判别字段是 `CustomWorkflowEnabled`，不是 `kind`**（评审修正）：`runHandler`（`internal/httpapi/handlers.go:489`）`if p.CustomWorkflowEnabled { → PlanCustom(:497) } else { → PlanWith/Plan(:505/507) }`。**else 分支被「标准项目」与「绘本项目」共享，不读 `p.Kind`**。绘本注入必须挂在 **`!CustomWorkflowEnabled && p.Kind=='picturebook'`** 子分支；标准项目（else 分支但非绘本）**无 schema 来源 → body.inputs 非空则 400，空则忽略**；绘本+`CustomWorkflowEnabled=true` 组合走 `PlanCustom`（不进 else），其 PB 覆盖**不生效**（worker 端 run_inputs 为 `{}`，pictureBookConfig overlay 为空，行为同今日）——此组合 v1 不支持绘本运行期覆盖，需在文档与测试中显式声明。
- **绘本字段消费汇聚于单一函数**：所有绘本参数在 worker 执行期经 `pictureBookConfig(...)`（`internal/worker/worker.go:495`）从 `projects.kind/picturebook_config` 解析（全 `internal/` 仅此一处为 run 消费解析，无旁路直读，已核实），5 个消费点（runScript 的 ageBand/bookType/themes `:464-466`、runStoryboard 的 illustrationStyle/MaxWordsPerSpread `:573-574`、fan-out 的 voice `:652`/旁白校验 `:638`）全部经此。**在这一处叠加"本 run 覆盖"，5 处零改动自动生效。** 两个调用点 `runScript:458`/`runStoryboard:563` 都持有 `c.todoID`，签名改为按 todo 反查 plan_id 可行。
- **三条 run 路径，plans 行真 INSERT 在两处**（评审修正）：`Plan`（`planner.go:70`）是单行委派 `return p.PlanWith(...)`，**无 INSERT**；标准/绘本路径真正的 `INSERT INTO plans` 在 **`PlanWith`（`planner.go:108`）**；自定义工作流路径在 **`PlanCustom`（`planner.go:337`）**。`run_inputs` 在这**两处 INSERT** 写入，但**三个函数**（`Plan`/`PlanWith`/`PlanCustom`）都要加 `runInputs` 形参穿透，且 `PlannerPort` 接口（`handlers.go:76-78`）与全部调用点（`handlers.go:497/505/507`、`workflowhandlers.go:276`）随签名改。
- **worker 反查通道**：worker 已有 `SELECT plan_id FROM todos WHERE id=$1`（`worker.go:660`）模式，可按 todo 反查 plan 的 run_inputs。
- **变量替换链**：`substituteVars`（`worker.go:2102`）纯字符串替换；`runCustomHTTP` 的 secret pass 顺序是「secret 先、name 后」（`worker.go:1925-1958`，注释明确是安全关键顺序）；`secretRefRe`（`worker.go:1707`）。

## 数据模型

### `workflows.inputs_schema JSONB`（设计期声明，DEFAULT `'[]'`）

`InputField` 数组，复用 `nodedesc.Property` 形态：

```json
[{
  "name": "heroName",            // ^[A-Za-z_]\w*$ 双校验（存时+运行时）
  "label": "主角名字",
  "type": "text",                // text | textarea | number | select | multiselect
  "options": [{"value":"warm","label":"温暖"}],  // 仅 select/multiselect
  "default": "小熊",             // json.RawMessage，可选
  "required": true,
  "target": "variable"           // variable | brief | contentType | targetPlatform | style | pbConfig
}]
```

`type` 5 种：`text`/`textarea`/`number`/`select`/`multiselect`（`multiselect` 为支持绘本 themes 多选而设）。

### `plans.run_inputs JSONB`（本次 run 值快照，不可变，DEFAULT `'{}'`）

```json
{
  "values": { "heroName": "阿力", "voice": "warm", "pageCount": 12 },
  "schema": [ /* run 时刻的 InputField[] 快照，保证可复现 */ ]
}
```

带 schema 快照是刻意的：回放/审计不依赖可变的 `workflows.inputs_schema`。

### 统一抽象：`internal/runinputs`（新包，纯函数 + store 读）

```go
type Field struct { Name, Label, Type, Target string; Options []Option; Default json.RawMessage; Required bool }
type Resolved struct {
    Variables     map[string]string          // target=="variable" → {{input:name}}
    BriefOverride map[string]string           // target∈{brief,contentType,targetPlatform,style}
    PBOverride    map[string]json.RawMessage  // target=="pbConfig"
}
func Validate(schema []Field, values map[string]json.RawMessage) (Resolved, error)  // 400 来源
func PictureBookSchema(cfg project.PictureBookConfig) []Field                       // 绘本派生 schema（纯函数）
```

- **自定义工作流**：schema 取自 `workflows.inputs_schema`。
- **绘本**：无 workflows 行——schema 由 `PictureBookSchema(cfg)` 从当前 `picturebook_config` 派生（不落 `workflows.inputs_schema`），run_inputs 仍按统一结构落 `plans.run_inputs`。

`plans.run_inputs` 是唯一真源，worker 端三类覆盖都从这一列读。

## 后端集成点

### 迁移（新增 m20，`internal/storage/storage.go`，紧随现状最新 m19 注册）

```sql
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS inputs_schema JSONB NOT NULL DEFAULT '[]';
ALTER TABLE plans     ADD COLUMN IF NOT EXISTS run_inputs   JSONB NOT NULL DEFAULT '{}';
```

`DEFAULT '[]'/'{}'` 保证旧行零回归。不 AutoMigrate，纯幂等 DDL（GORM 铁律）。

### workflows store（`internal/workflows/store.go`）

`Workflow` struct 加 `InputsSchema json.RawMessage`；`Create`/`Update` 增形参 + 列（写一律 `INSERT...RETURNING`，NULL 列 `[]byte` 中转）；`Get`/`ListByProject` SELECT 增 `inputs_schema`。`httpapi.WorkflowStore` 接口 + `workflowReq` 同步加 `InputsSchema`；create/update handler 保存前做**存时 schema 校验**（name 正则、type allowlist、select/multiselect 必带 options、target 合法）。

### body 读取上限（所有 run 路径，评审新增）

`runHandler`/`runWorkflowHandler` 今天**完全不读 body**。新增 body 解码前**必须**先套 `http.MaxBytesReader(w, r.Body, maxRunInputsBody)` 再 `json.Decode`，否则巨大 body 在解码阶段即可打爆内存。限额（钉死）：
- `maxRunInputsBody = 64 KB`（读取层强制）
- 单字段值长度 ≤ 8 KB，字段数 ≤ 64（`Validate` 校验，超限 → 400）

### 自定义工作流 run 注入（`runWorkflowHandler`，`workflowhandlers.go:216`）

1. `MaxBytesReader` + 解析可选 body `{"inputs": {name:value,...}}`（当前完全不读 body）。
2. `runinputs.Validate(wf.InputsSchema, inputs)` —— 必须在 `SetStatus("planning")`（:262）与 `planner_started`（:266）**之前**，失败 **400**（避免悬挂状态）。
3. brief 构造（:267-270）叠加 `Resolved.BriefOverride`（仅本 run，不写回 projects）。
4. `PlanCustom` 增形参 `runInputs json.RawMessage`，传 `{values, schema}` 快照。

### run 注入（`runHandler`，`handlers.go:489` 的分支结构，评审修正）

`runHandler` 服务 标准 / 绘本 / `CustomWorkflowEnabled` legacy 自定义三类，分支如下：

- **`p.CustomWorkflowEnabled`（legacy 自定义）→ `PlanCustom`**：v1 不支持其 inputs_schema（无 workflows 行）。**body.inputs 非空 → 忽略**（不落 run_inputs，不报错），传空 `runInputs`。
- **`!CustomWorkflowEnabled && p.Kind=='picturebook'`（绘本）**：
  1. `MaxBytesReader` + 读 body 可选 `inputs`。
  2. `runinputs.PictureBookSchema(cfg)` 从当前 `picturebook_config` 派生 schema → `Validate` → 400（在 SetStatus(planning) 之前）。
  3. brief 叠加 `BriefOverride`。
  4. 绘本覆盖值进 `run_inputs.values` 的 `target=="pbConfig"` 项，随 `PlanWith` 落 plans 行。
- **标准项目（else 且非绘本）**：无 schema 来源。**body.inputs 非空 → 400**，空则照常 run，传空 `runInputs`。

### planner（`internal/planner/planner.go`）

`Plan`（:70，单行委派无 INSERT）、`PlanWith`（:108，真 INSERT）、`PlanCustom`（:293，INSERT 在 :337）三个函数签名都加 `runInputs json.RawMessage` 形参穿透；两处 `INSERT INTO plans` 增 `run_inputs` 列。`PlannerPort` 接口（`handlers.go:76-78`）与全部调用点（`handlers.go:497/505/507`、`workflowhandlers.go:276`）随签名改。planner **不解析** `{{input:}}`、不把 variable 值塞进 todo input_json——variable 解析延迟到 worker（与 secret 同构）。

### worker `{{input:}}` pass（核心安全设计）

新增 `inputRefRe`（`\{\{\s*input:([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`，name 用 `safeFieldRe` 同款字符集，定义在 `internal/worker/expr_resolver.go:25`，同包可直接复用）。worker 执行 todo 时反查（**带 project scope，防御纵深**）：

```sql
SELECT p.run_inputs FROM plans p JOIN todos t ON t.plan_id = p.id WHERE t.id = $1 AND t.project_id = $2
```

只取 `target=="variable"` 项构成 `inputVals map[string]string`（number/select 值统一 JSON 字面量 stringify；**`multiselect` 与非 `pbConfig` target 的组合在 Validate 阶段拒绝**，故 variable 通道不会遇到 multiselect）。

**三通道注入机制（安全关键，input 值绝不被二次求值/不进可执行体）**：
- `runCustomLLM`（`worker.go:1818`）：`substituteVars`（name）之后对 system/user 跑 input pass（末位、单次、literal `ReplaceAllStringFunc`）。LLM 无 secret pass。
- `runCustomHTTP`（`worker.go:1887`）：input pass 在**两个独立代码位置**各加一次：
  - **header**：每项现有顺序「secret pass（:1939）→ name pass（:1958）」之后追加 input pass。
  - **body**：`substituteVars`（:1964）→ **body 的 `{{secret:` 残留检查（:1965，只校验作者模板+name 通道）** → 然后才 input pass。残留检查必须严格夹在 name pass 与 input pass 之间，确保作者写 secret 仍 → `errRequestFailed`，而 input 注入的 `{{secret:X}}` 字面量在检查之后注入、安全随请求发出、不被解析。
- `runCustomScript`（`worker.go:2025`，**评审修正——绝不替换源码**）：`{{input:}}` **不对 `in.Code` 做字符串替换**（那是 Starlark 代码注入）。改为把 `target=="variable"` 的 input 值**并入 `scriptengine.Run` 的 `inputs` globals**（`StringDict`，与 name 变量同为只读数据全局，见 `internal/scriptengine/engine.go:38-64`），脚本经全局名读取，源码字节全程不被 input 污染。

**未声明的 `{{input:foo}}` → 空串不报错**（`inputRefRe.ReplaceAllStringFunc` 查不到返回 `""`）。`input:` 命名空间含冒号，与 name 变量名空间（`safeFieldRe`=`^[A-Za-z_]\w*$`，不含冒号）天然不相交，不与 name pass 串号。

**绘本 PB 覆盖**：`pictureBookConfig` 签名改为接受 todo 上下文（按 `c.todoID` 反查 plan_id → run_inputs），在 `ParsePictureBookConfig` 之后叠加 `target=="pbConfig"` 覆盖层。**这是唯一改点**，5 个绘本消费点零改动。projects 表全程只读。

> **ageBand 派生默认值 / 覆盖语义**（评审 + 终审修正）：`ParsePictureBookConfig` 按 `ageBand` 给未提供的字段填默认。实际实现是**增量覆盖 over baseline**（worker `overlayPBConfig` 只替换 present 的 key，缺省字段保留 baseline cfg 值）。绘本运行期表单**用当前 config 预填所有字段**，所以表单即 baseline 的可见快照：用户在表单里看到 pageCount=16，改 ageBand 时 pageCount 字段不自动级联重算（停在 baseline，仍可见可手改）——这是 WYSIWYG，无隐藏错位，非正确性/安全问题。注：number 字段（pageCount）只要有值总会提交；空标量 select 省略提交（等价"不覆盖=用 baseline"）。

## 绘本覆盖具体方案

### 可被 run 覆盖的字段（取自 `internal/project/pbconfig.go`）

| 字段 | json key | type | v1 | 消费点 |
|---|---|---|---|---|
| 音色 Voice | `voice` | select | ✅ | audio fan-out `:652` |
| 主题 Themes | `themes` | multiselect | ✅ | runScript `:466` |
| 年龄段 AgeBand | `ageBand` | select | ✅ | runScript `:464`；派生 MaxWordsPerSpread |
| 书籍类型 BookType | `bookType` | select | ✅ | runScript `:465` |
| 插画风格 IllustrationStyle | `illustrationStyle` | select | ✅ | runStoryboard `:573` |
| 旁白风格 NarrationStyle | `narrationStyle` | select | ✅ | storyboard |
| 页数 PageCount | `pageCount` | number | ✅（需接线，见下） | **当前无消费者** |

> **🔴 注入边界不变量**：`PictureBookSchema` 的所有字符串型字段**必须是 `select`（options=完整枚举），永不为 `text`**。PB override 值不走 `{{input:}}` pass，而是经 overlay 合并进 `PictureBookConfig` 后**直达 LLM prompt**（`:464-466`/`:573`），该路径无任何 secret/input pass 兜底——枚举越界拒绝（`Validate`）是其唯一注入防线。测试：PB override 传不在枚举内的 voice/theme → 400（不可落 run_inputs、不可达 prompt）。

> **🟡 PageCount 接线**（评审：当前 PageCount 无任何 worker/planner 消费者，页数由 storyboard agent 的 LLM 输出决定）。v1 要让 PageCount 真生效，须把覆盖后的 pageCount 接进 storyboard agent 输入并加 min/max 边界（合理区间，如 4–24）。**这是本功能最易膨胀的子项**——实现时若发现接线过于侵入 storyboard agent，implementer 须 escalate，由主线决定「接线 vs 砍到 v2」，不得静默留假控件。

### schema 派生

`runinputs.PictureBookSchema(cfg)` 纯函数把每个可覆盖字段映射成 `Field{name:<json key>, type, target:"pbConfig", default:<当前 cfg 值>, options:<枚举>}`。前端复用同一份枚举（`web/src/features/projects/pbConfig.ts`）。绘本 schema **不落库**，是 `(kind, picturebook_config)` 的确定性函数，run 时合成并作为 `run_inputs.schema` 快照落 plans。

### 覆盖值旁路链路

```
runHandler 读 body.inputs → Validate(派生schema) → run_inputs[target=pbConfig]
  → Plan/PlanWith 写 plans.run_inputs（仅 plans 行，projects 不动）
  ──（worker 执行期）──
worker.pictureBookConfig(ctx, todo) → ParsePictureBookConfig(基线) → overlay run_inputs[pbConfig] → 合并 cfg → 5 消费点自动用上
```

一个绘本项目连续 run 不同 inputs → 多 plan → 多变体，互不干扰，完全可复现（每 plan 自带 run_inputs 快照）。

## 前端组件

### 设计期 schema 编辑器（自定义工作流）

`web/src/features/workflow-canvas/InputsSchemaPanel.tsx`（画布级，与 `PropertiesPanel.tsx` 并列），复用 property 渲染编辑每个 InputField。保存并入 `WorkflowCanvas.tsx` 的保存 mutation（`workflowApi.ts` create/update body 增 `inputsSchema`）。schema 校验复用 `WorkflowDialog.schema.ts` 同款 zod。

### 运行期表单（自定义工作流）

`RunInputsDialog.tsx`（新）：run 前若 `wf.inputsSchema` 非空 → 弹表单按 schema 渲染 + required 前端校验（后端 400 兜底），提交把 `{inputs}` 作为 POST body。`useRunWorkflow` 的 `mutationFn` 从 `(wfId)` 改为 `({wfId, inputs})`。空 schema → 跳过弹窗（零回归）。

### 绘本运行期表单

绘本 run 触发处弹 `RunInputsDialog`，schema 由前端用 `pbConfig.ts` 枚举 + 当前 `picturebook_config` 派生（与后端 `PictureBookSchema` 对称），默认值预填，用户改后提交。可复用 `PictureBookConfigForm.tsx` 的控件。`useRun`（`web/src/features/workflow/api.ts:80`）`mutationFn` 改为 `(inputs?)`。

## 边界与错误处理

| 场景 | 行为 |
|---|---|
| 旧工作流（`inputs_schema='[]'`） | 不弹表单，run 与今日一致 |
| 旧 plan（`run_inputs='{}'`） | worker 无覆盖、无 variable |
| required 字段缺失 | **400**，planning 之前 |
| type 不符（number 非数字 / select 越界 / multiselect 越界） | **400** |
| 存时 name 不匹配 `^[A-Za-z_]\w*$` / select 无 options / 未知 type/target | 保存 **400** |
| run 时 name 二次校验失败 | 跳过该项 |
| 未声明的 `{{input:foo}}` | 空串，不报错 |
| input 值含 `{{secret:X}}` | 字面量，绝不解析（input pass 永在 secret pass 之后） |
| 绘本 config 解析失败 | 退化非绘本（worker.go:505），覆盖一并失效 |
| number/select 值注入 variable 通道 | 统一 JSON 字面量 stringify |
| run_inputs 体积/字段数 | 加保守上限校验（防 DoS） |

## 权限不变量（评审新增）

`runWorkflowHandler` 标 "editor+"（`workflowhandlers.go:211`）。`{{input:}}`/brief/PB 覆盖注入 LLM prompt 本质是 prompt injection——**只要 run-with-inputs 的权限门槛 == 编辑 workflow/绘本配置的门槛**，能 run 的人本就能编辑 prompt，注入不带来新越权（产品固有风险）。spec 断言此不变量并加权限测试：不存在「能 run 却不能 edit」的更低角色；若存在则必须收紧到 editor+。

## 测试策略

DB-backed Go 测试一律 **fresh DB**（铁律：脏数据撞唯一索引），`GOWORK=off ... -count=1`。

1. **注入回归（最重要，三通道 + 混合 case）**：
   - **HTTP 混合**：同一 header 同时含作者真 `{{secret:REAL}}` + input 值携带 `{{secret:STOLEN}}` → 断言 `Secrets.Resolve` 调用集合 == {REAL}（不是计数=0）、最终串里 STOLEN 仍字面量。
   - **body 残留重排正向回归**：作者在 body 写 `{{secret:}}` → 仍 `errRequestFailed`（`:1965` 守卫不因重排失效）；input 注入的 `{{secret:}}` 进 body → 请求照常发出、body 字面含 `{{secret:X}}`、Resolve 不解它。
   - **全惰性跨通道**：input 值 = `{{secret:X}} {{upVar}} {{ $node["y"].json }} {{input:self}}` 注入 LLM system/user、HTTP header/body → 四种语法在最终输出全部字面留存、均不二次求值。
   - **script 代码注入**：input 值含 Starlark 片段（如 `load(...)`/赋值）→ 断言脚本**源码字节不变**、片段不执行、值仅作为只读 global 可读。
2. **绘本覆盖只作用本 run + 并发不串台**：① 同绘本项目顺序跑两次不同 voice/ageBand → 两 plan run_inputs 不同、`projects.picturebook_config` 未变、worker 对各 plan 返回各自覆盖值；② **两个 plan 的 todo 交错执行** → 每个 todo 反查到自己 plan 的 voice，不串台。
3. **`Validate` 表测**：required/type/select/multiselect 越界 → error；单值 >8KB / 字段数 >64 → error；`multiselect` × 非 `pbConfig` target → error；空 schema+空 inputs → ok。
4. **body 上限**：超 64KB body → `MaxBytesReader` 在读取层 400（不进解码）。
5. **存时 schema 校验**：非法 name/无 options/未知 type/target → 400。
6. **planning 之前 400**：非法 inputs，断言 `SetStatus("planning")` 与 `planner_started` 均未触发。
7. **分支行为**：标准项目带 body.inputs → 400；legacy 自定义带 body.inputs → 忽略不报错；绘本+CustomWorkflowEnabled → PB 覆盖不生效（行为同今日）。
8. **未声明 `{{input:foo}}` → 空串**；PB override 枚举越界 → 400。
9. **migration 幂等**：m20 重跑不报错；旧行 DEFAULT 生效。
10. **权限**：run-with-inputs 门槛 == edit 门槛（无更低 run 角色）。
11. **前端**：`RunInputsDialog` required 校验 vitest；`useRun`/`useRunWorkflow` body 携带 inputs 断言；空 schema 跳过弹窗；绘本表单预填并提交全字段。

## 任务拆分

依赖序：T1→T2→{T3,T4,T5,T6}→{T7,T8}。T6 依赖 T3（plans 有 run_inputs）。

- **T1** 迁移 m20（紧随 m19 注册）+ workflows store（inputs_schema CRUD，`INSERT...RETURNING`/`[]byte` 中转）+ WorkflowStore 接口 + workflowReq 字段 + **存时 schema 校验**（name 正则/options/type/target；multiselect×非pbConfig 拒绝）
- **T2** `internal/runinputs` 包：`Field`/`Resolved`/`Validate`（含 8KB/64 字段限额、枚举越界、multiselect 组合拒绝）+ `PictureBookSchema`（**全字符串字段 select**）+ 单元表测
- **T3** planner 落库：`Plan`(:70)/`PlanWith`(:108 真 INSERT)/`PlanCustom`(:337 INSERT) 三签名加 `runInputs` + `PlannerPort` 接口（`handlers.go:76`）+ 4 调用点（`handlers.go:497/505/507`、`workflowhandlers.go:276`）随改
- **T4** 自定义工作流 run 注入：`runWorkflowHandler` `MaxBytesReader`+读 body + Validate(400, 在 SetStatus/planner_started 之前) + brief override + 传 run_inputs
- **T5** 绘本/标准 run 注入：`runHandler` 按 `CustomWorkflowEnabled` + `Kind` 三分支（绘本派生 schema+Validate+run_inputs；标准带 inputs→400；legacy 忽略）；`pictureBookConfig` 按 todo 反查叠加 pbConfig override（PB override 须完整字段集）；**PageCount 接 storyboard agent（min/max，过侵入则 escalate）**；注入回归测试 #2（含并发不串台）
- **T6** worker `{{input:}}` pass：`inputRefRe` + plan_id→run_inputs 反查（带 project scope）+ LLM/HTTP(header+body 两处，残留检查夹位)/**script(并入 globals 不替换源码)** + 安全测试 #1（含混合 case + 代码注入）
- **T7** 前端设计期：`InputsSchemaPanel` + 保存接 inputsSchema
- **T8** 前端运行期：`RunInputsDialog` + `useRun`/`useRunWorkflow` body + 绘本派生表单（预填+提交全字段）

## 范围排除（YAGNI）

- 项目级 legacy 自定义工作流（`custom_workflow_enabled`+`workflow_nodes`）的 inputs_schema 支持
- 跨工作流共享输入库、输入历史版本管理
- 富类型（文件上传/资产引用/日期/表达式默认值）
- run_inputs 的服务端长度上限之外的高级校验
