# 工作流运行期输入 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development。设计真源见 `docs/superpowers/specs/2026-06-29-runtime-inputs-design.md`——本计划每个任务都假设你已读该 spec 对应章节。Steps 用 checkbox 跟踪。

**Goal:** 让工作流（含绘本）run 时填类型化输入，注入 `{{input:}}` 自由变量 + 覆盖 brief + 覆盖 PictureBookConfig，一次 run 一份不可变快照、可复现、可出变体。

**Architecture:** `workflows.inputs_schema`（设计期声明）+ `plans.run_inputs`（运行期快照）双列；`internal/runinputs` 统一校验/解析；worker `{{input:}}` pass 严格在 secret pass 之后、script 并入 globals；绘本经 `pictureBookConfig` overlay。

**Tech Stack:** Go（GORM 混合层，纯 `$N` 查询，`INSERT...RETURNING`，不 AutoMigrate）；React 19 + TanStack + zod；DB-backed 测试用 fresh PG + `GOWORK=off ... -count=1 -p 1`。

**铁律**：写一律 `INSERT...RETURNING`（勿 gorm.Create）；不 AutoMigrate；NULL/JSONB 列用 `[]byte` 中转；DB-backed 测试 fresh DB（脏数据撞唯一索引）；`pool.Exec` 种子失败要断言（勿吞错）。

---

## Task 1: 迁移 m20 + workflows store inputs_schema

**Files:**
- Modify: `internal/storage/storage.go`（紧随 `m19Migrations` 注册 `m20`）
- Modify: `internal/workflows/store.go`（`Workflow` struct + CRUD）
- Modify: `internal/httpapi/workflowhandlers.go`（`WorkflowStore` 接口 + `workflowReq` + 存时校验）
- Test: `internal/workflows/store_test.go`、`internal/httpapi/workflowhandlers_test.go`

- [ ] **Step 1: 写失败测试** —— store CRUD 往返 `inputs_schema`（建 workflow 带 schema → Get 读回一致）；handler 存非法 schema（name 非 `^[A-Za-z_]\w*$` / select 无 options / 未知 type / 未知 target / multiselect×非pbConfig）→ 400。
- [ ] **Step 2: 跑测试确认失败**（列不存在 / 字段缺失）。
- [ ] **Step 3: 迁移** —— `m20Migrations = []string{ ALTER TABLE workflows ADD COLUMN IF NOT EXISTS inputs_schema JSONB NOT NULL DEFAULT '[]'; ALTER TABLE plans ADD COLUMN IF NOT EXISTS run_inputs JSONB NOT NULL DEFAULT '{}' }`，`all = append(all, m20Migrations...)`。
- [ ] **Step 4: store** —— `Workflow.InputsSchema json.RawMessage`；`Create`/`Update` 增列（`INSERT...RETURNING`，`[]byte` 中转）；`Get`/`ListByProject` SELECT 增 `inputs_schema`。
- [ ] **Step 5: handler** —— `WorkflowStore` 接口 + `workflowReq.InputsSchema`；存前调用 `runinputs.ValidateSchema`（T2 提供；本任务可先内联一个轻校验，T2 完成后改为复用——或调整任务序让 T2 先行）。
- [ ] **Step 6: 跑测试确认通过**：`GOWORK=off go test ./internal/workflows/... ./internal/httpapi/... -run InputsSchema -count=1`。
- [ ] **Step 7: commit** `feat(runtime-inputs): m20 迁移 + workflows.inputs_schema CRUD`

> 注：存时校验依赖 T2 的 `ValidateSchema`。**执行序建议 T2 先于 T1 的 Step 5**，或 T1 先内联占位、T2 后回填。

## Task 2: internal/runinputs 包

**Files:**
- Create: `internal/runinputs/runinputs.go`、`internal/runinputs/picturebook.go`
- Test: `internal/runinputs/runinputs_test.go`

- [ ] **Step 1: 写失败表测** —— `Validate(schema, values)`：required 缺失 → err；type 不符（number 非数字、select/multiselect 越界）→ err；单值 >8KB / 字段数 >64 → err；`multiselect` × target≠`pbConfig` → err；合法 → `Resolved{Variables, BriefOverride, PBOverride}` 正确分流；空 schema+空 values → ok 空 Resolved。`PictureBookSchema(cfg)` → 7 字段全 `target:"pbConfig"`、字符串字段全 `type:"select"`（断言无 text）、default 取 cfg 当前值。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现** —— 类型 `Field/Option/Resolved`；`ValidateSchema(schema)`（存时用：name 正则 `^[A-Za-z_]\w*$`、select/multiselect 必 options、type/target allowlist、multiselect 组合）；`Validate(schema, values)`（运行时用：上面全部 + 值校验 + 分流）；`PictureBookSchema(cfg project.PictureBookConfig)`（复用 pbconfig 枚举）。number/select→variable 统一 JSON 字面量 stringify。
- [ ] **Step 4: 跑测试确认通过** `GOWORK=off go test ./internal/runinputs/... -count=1`。
- [ ] **Step 5: commit** `feat(runtime-inputs): internal/runinputs 校验+绘本派生 schema`

## Task 3: planner 落库 run_inputs

**Files:**
- Modify: `internal/planner/planner.go`（`Plan:70`/`PlanWith:108`/`PlanCustom:293,INSERT:337`）
- Modify: `internal/httpapi/handlers.go`（`PlannerPort` 接口 :76 + 调用点 :497/505/507）
- Modify: `internal/httpapi/workflowhandlers.go`（调用点 :276）
- Test: `internal/planner/planner_test.go`

- [ ] **Step 1: 写失败测试** —— `PlanWith`/`PlanCustom` 传入 `runInputs` JSON → 查 `plans.run_inputs` 列等于传入值；不传（nil）→ 列为 `'{}'`。
- [ ] **Step 2: 跑测试确认失败**（签名不接受参数）。
- [ ] **Step 3: 实现** —— 三函数加 `runInputs json.RawMessage` 形参；两处 `INSERT INTO plans` 增 `run_inputs` 列（nil → `'{}'`，用 `NULLIF`-风格或显式默认）；`PlannerPort` 接口 + 4 调用点同步（先传 `nil` 占位，T4/T5 再填真值）。
- [ ] **Step 4: 跑测试确认通过** + 全包构建 `GOWORK=off go build ./...`。
- [ ] **Step 5: commit** `feat(runtime-inputs): planner 落 plans.run_inputs`

## Task 4: 自定义工作流 run 注入

**Files:**
- Modify: `internal/httpapi/workflowhandlers.go`（`runWorkflowHandler:216`）
- Test: `internal/httpapi/workflowhandlers_test.go`

- [ ] **Step 1: 写失败测试** —— POST run 带合法 inputs → brief 被 override（断言 PlanCustom 收到的 brief）+ run_inputs 落库；带非法 inputs → 400 且 `SetStatus("planning")`/`planner_started` 未触发；body >64KB → 400（MaxBytesReader）；空 body → 行为同今日。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现** —— `http.MaxBytesReader(w,r.Body,64<<10)` + 解析可选 `{"inputs":...}`；在 SetStatus 前 `runinputs.Validate(wf.InputsSchema, inputs)`（400）；brief 叠 `BriefOverride`；`PlanCustom(...,runInputsJSON)`。
- [ ] **Step 4: 跑测试确认通过**。
- [ ] **Step 5: commit** `feat(runtime-inputs): 自定义工作流 run 注入输入`

## Task 5: 绘本/标准 run 注入 + pictureBookConfig overlay

**Files:**
- Modify: `internal/httpapi/handlers.go`（`runHandler:489` 三分支）
- Modify: `internal/worker/worker.go`（`pictureBookConfig:495` 签名 + overlay；PageCount 接 storyboard）
- Test: `internal/httpapi/handlers_test.go`、`internal/worker/worker_test.go`

- [ ] **Step 1: 写失败测试** —— ① 绘本带 inputs(改 voice/ageBand) → run_inputs 落 pbConfig 项 + `projects.picturebook_config` 未变；② worker `pictureBookConfig` 对带 pbConfig override 的 plan 返回覆盖值、对无 override 的 plan 返回基线；③ 并发两 plan 不同 voice 交错执行 → 各自不串台；④ 标准项目带 inputs → 400；⑤ legacy 自定义带 inputs → 忽略不报错；⑥ PB override 枚举越界 voice → 400。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现 handler** —— 按 `CustomWorkflowEnabled`/`Kind` 三分支（见 spec）；绘本派生 `PictureBookSchema`+Validate+落 run_inputs；标准带 inputs→400；legacy 忽略。
- [ ] **Step 4: 实现 worker overlay** —— `pictureBookConfig` 改为接 todo 上下文，按 `c.todoID` 反查 `plans.run_inputs`（带 project scope），`ParsePictureBookConfig` 后叠加 `target=="pbConfig"`。PageCount 接 storyboard agent 输入（min/max 4–24）——**若过侵入则 escalate**。
- [ ] **Step 5: 跑测试确认通过**。
- [ ] **Step 6: commit** `feat(runtime-inputs): 绘本运行期覆盖 PictureBookConfig`

## Task 6: worker {{input:}} pass（三通道，安全核心）

**Files:**
- Modify: `internal/worker/worker.go`（`runCustomLLM:1818`/`runCustomHTTP:1887`/`runCustomScript:2025`）
- Test: `internal/worker/worker_test.go`

- [ ] **Step 1: 写失败安全测试**（见 spec 测试 #1 全部子case）—— HTTP 混合 secret（Resolve 集合=={REAL}、STOLEN 字面量）；body 残留重排正向回归；全惰性跨通道字面留存；script 代码注入（源码字节不变、片段不执行、值仅 global 可读）；未声明 `{{input:foo}}`→空串。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现** —— `inputRefRe`；按 `c.todoID` 反查 run_inputs（带 project scope）取 `target=="variable"` 项；LLM：substituteVars 后 input pass；HTTP：header（secret→name→input）+ body（name→残留检查→input，残留检查夹位）；script：input 值并入 `scriptengine.Run` 的 globals，**不替换 `in.Code`**。
- [ ] **Step 4: 跑测试确认通过** `GOWORK=off go test ./internal/worker/... -count=1 -p 1`。
- [ ] **Step 5: commit** `feat(runtime-inputs): worker {{input:}} 三通道注入（安全）`

## Task 7: 前端设计期 InputsSchemaPanel

**Files:**
- Create: `web/src/features/workflow-canvas/InputsSchemaPanel.tsx`
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx`、`web/src/features/projects/workflowApi.ts`、`WorkflowDialog.schema.ts`
- Test: `web/src/features/workflow-canvas/InputsSchemaPanel.test.tsx`

- [ ] **Step 1: 写失败 vitest** —— 增删 InputField、type/target/options 编辑、name 正则校验提示；保存 mutation body 含 `inputsSchema`。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现** —— 画布级面板（与 PropertiesPanel 并列），复用 property 渲染；zod schema 校验（复用 WorkflowDialog.schema.ts 同款）；保存接入 create/update。
- [ ] **Step 4: 验证** —— 改动文件 `npx eslint` 零新 error + vitest 全绿 + `npm run build`（见 [[studio-web-lint-baseline]]）。
- [ ] **Step 5: commit** `feat(runtime-inputs): 工作流编辑器输入 schema 面板`

## Task 8: 前端运行期 RunInputsDialog

**Files:**
- Create: `web/src/features/workflow/RunInputsDialog.tsx`
- Modify: `web/src/features/projects/workflowApi.ts`（`useRunWorkflow`）、`web/src/features/workflow/api.ts`（`useRun`）、绘本 run 触发处
- Test: `web/src/features/workflow/RunInputsDialog.test.tsx`

- [ ] **Step 1: 写失败 vitest** —— schema 非空弹表单 + required 前端校验拦截；提交把 `{inputs}` 进 POST body；空 schema 跳过弹窗直跑；绘本表单预填当前 config 全字段并提交全字段。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现** —— `RunInputsDialog` 按 schema 渲染；`useRunWorkflow` `mutationFn` → `({wfId,inputs})`；`useRun` → `(inputs?)`；绘本 schema 前端用 `pbConfig.ts` 枚举派生（与后端 `PictureBookSchema` 对称），复用 `PictureBookConfigForm` 控件。
- [ ] **Step 4: 验证** —— 改动文件零新 eslint error + vitest 全绿 + build。
- [ ] **Step 5: commit** `feat(runtime-inputs): 运行期输入表单（含绘本变体）`

---

## 执行序

T2 → T1 → T3 → {T4, T5, T6}（T6 依赖 T3 落库；T5 worker overlay 依赖 T3）→ {T7, T8}。
每任务：implementer → spec 合规审 → 代码质量审 → 过。全绿后整体终审 → push → PR（**不直推 main**）。
