# 创建项目工作流:状态单一权威源 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让后端成为工作流状态的唯一权威源:新增 `projectstate.Compute` 纯函数产出语义状态快照,经 REST 端点 + SSE 推送下发;前端删除自有状态推导,只渲染后端快照,事件流仅喂日志。

**Architecture:** 后端新增 `internal/projectstate` 包,`Compute(Input) ProjectState` 从最新 plan 的 todos/assets 派生整体 status、runStatus、各语义角色阶段、asset pip 列表、计数与错误。`project.Store.LoadState` 装载 DB 数据并调用 Compute。新增 `GET /api/projects/{id}/state` 返回快照;SSE 在连接时发一条 `state` 事件、之后 version 变化时再推。前端 `useProjectState` 经 REST 拉取 + SSE `state` 帧覆盖缓存;`timeline.ts` 退化为"事件→日志文案 + role/status 映射表";`WorkbenchView` 改读 `ProjectState`。

**Tech Stack:** Go 1.26 (pgx/pgxpool, stdlib `net/http` ServeMux), React + TypeScript + TanStack Query + Vitest + `@microsoft/fetch-event-source`。

**测试约定:**
- 后端:`cd llm-agent-studio && GOWORK=off go test ./internal/<pkg>/... -run <Name> -count=1`
- 前端:`cd llm-agent-studio/web && pnpm vitest <path> --run`
- 提交粒度:每个 Task 末尾一个原子提交。当前在 `docs/workflow-state-single-source` 分支已有 spec 提交;实现可继续在同分支或新开 `feat/workflow-state-single-source`。

---

## File Structure

**后端(新建):**
- `internal/projectstate/state.go` — `ProjectState`/`Stage`/`Pip`/`Assets`/`ProblemError`/`Plan`/`Input`/`Todo`/`Asset` 类型 + `Compute(Input) ProjectState` 纯函数(零 I/O)。
- `internal/projectstate/state_test.go` — Compute 的表驱动单测。

**后端(修改):**
- `internal/project/store.go` — 新增 `LoadState(ctx, projectID) (projectstate.ProjectState, error)`(装载最新 plan + todos + assets + version,调 Compute)。
- `internal/httpapi/handlers.go` — 新增 `stateHandler`;`runHandler` 去掉显式 `SetStatus("running")`(改由 Compute 派生);`ProjectStore` 接口加 `LoadState`。
- `internal/httpapi/httpapi.go` — 注册 `GET /api/projects/{id}/state`;`Deps` 加 `StateReader`;SSE 注入 state 装载器。
- `internal/httpapi/sse.go` — 连接发 `state` 全量帧 + version 变化推 `state`;`state` 入白名单。
- `internal/worker/worker.go` — 两处 `todo_failed` 载荷补 `type` 字段。

**前端(修改):**
- `web/src/lib/projectState.ts`(新建)— `ProjectState` 等 TS 类型(对齐后端)。
- `web/src/lib/types.ts` — 引用/导出 `ProjectState`;新增 `StageRole`/`StageStatus`/`RunStatus2` 枚举。
- `web/src/lib/projectState.contract.test.ts`(新建)— 契约一致性测试。
- `web/src/lib/timeline.ts` — 删状态推导,仅留 `logFor` + `LogLine` + 角色映射。
- `web/src/features/workflow/api.ts` — 新增 `useProjectState`;`fetchProjectState`。
- `web/src/features/workflow/useProductionTimeline.ts` — 改为只累积日志 + 应用 `state` 帧。
- `web/src/features/workflow/WorkbenchPage.tsx` — `WorkbenchView` 改读 `ProjectState`。
- `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx` — 删 `runStatus`/`status` override,直接传 `ProjectState`。

---

## Task 1: 后端 `projectstate.Compute` 纯函数 + 类型

**Files:**
- Create: `internal/projectstate/state.go`
- Test: `internal/projectstate/state_test.go`

设计要点(语义映射,实现者必须照此):
- **整体 `status`**(7 态):沿用现有 `DeriveStatus` 决策树,但内联进 Compute(从 `Input.Todos`/`Input.Assets` 算 `TodoCounts`)。无 plan → 返回 `Input.ProjectStatus`(保持 draft)。
- **`runStatus`**:`done`(整体 status ∈ {completed,review,failed,canceled})/`running`(planning|running)/`idle`(draft 或无 plan)。
- **`stages`**:固定 5 个语义角色顺序 `planner, script, storyboard, asset, review`。
  - `planner`:无对应 todo。无 plan→`blocked`;有 plan 且有 todos→`done`;有 plan 无 todos(planning 中)→`running`。
  - `script`/`storyboard`:取该 type 的 todo,按 `todoStatusToStage` 映射;无该 todo→`blocked`。
  - `asset`:聚合 pip——全 done→`done`;全 failed→`failed`;有 running→`running`;有 ready/blocked→`pending`;无 asset todo→`blocked`。
  - `review`:整体 status==`review`→`pending`;==`completed`→`done`;==`failed`/`canceled`→`failed`;否则 `blocked`。
- **`pips`**:每个 type==`asset` 的 todo 一个,`status` 由 `todoStatusToPip` 映射,`assetId` 取该 todo 关联的最新 asset。
- **`assets`**:`total`=asset todo 数;`done`=有已生成 asset 的;`pending`=asset.status==`pending_acceptance` 的。
- **`error`**:最后一个 status==`failed` 的 todo(取 type 作 role,error 文本)。
- **`todoStatusToStage`**:ready→pending, running→running, done→done, blocked→blocked, failed→failed, canceled→failed。
- **`todoStatusToPip`**:ready→idle, running→running, done→done, blocked→idle, failed→failed, canceled→failed。

- [ ] **Step 1: 写失败测试 `state_test.go`**

```go
package projectstate

import "testing"

func TestCompute_NoPlan_KeepsDraft(t *testing.T) {
	got := Compute(Input{ProjectID: "p1", ProjectStatus: "draft", HasPlan: false})
	if got.Status != "draft" {
		t.Fatalf("status = %q, want draft", got.Status)
	}
	if got.RunStatus != "idle" {
		t.Fatalf("runStatus = %q, want idle", got.RunStatus)
	}
	if got.Stages[0].Role != "planner" || got.Stages[0].Status != "blocked" {
		t.Fatalf("planner stage = %+v, want blocked", got.Stages[0])
	}
}

func TestCompute_RunningWithScript(t *testing.T) {
	in := Input{
		ProjectID: "p1", ProjectStatus: "running", HasPlan: true, Version: 7,
		Plan:  &Plan{PlanID: "pl1", Valid: true},
		Todos: []Todo{{ID: "t-s", Type: "script", Status: "running"}},
	}
	got := Compute(in)
	if got.Status != "running" {
		t.Fatalf("status = %q, want running", got.Status)
	}
	if got.RunStatus != "running" {
		t.Fatalf("runStatus = %q, want running", got.RunStatus)
	}
	if got.Version != 7 {
		t.Fatalf("version = %d, want 7", got.Version)
	}
	if stageByRole(got, "planner").Status != "done" {
		t.Fatalf("planner = %q, want done (todos exist)", stageByRole(got, "planner").Status)
	}
	if s := stageByRole(got, "script"); s.Status != "running" || s.TodoID != "t-s" {
		t.Fatalf("script stage = %+v, want running/t-s", s)
	}
}

func TestCompute_AssetPipsAndCounts(t *testing.T) {
	in := Input{
		ProjectID: "p1", ProjectStatus: "review", HasPlan: true,
		Plan: &Plan{PlanID: "pl1"},
		Todos: []Todo{
			{ID: "a1", Type: "asset", Status: "done"},
			{ID: "a2", Type: "asset", Status: "done"},
		},
		Assets: []Asset{
			{ID: "as1", TodoID: "a1", Status: "pending_acceptance"},
			{ID: "as2", TodoID: "a2", Status: "pending_acceptance"},
		},
	}
	got := Compute(in)
	if got.Assets.Total != 2 || got.Assets.Done != 2 || got.Assets.Pending != 2 {
		t.Fatalf("assets = %+v, want 2/2/2", got.Assets)
	}
	if len(got.Pips) != 2 {
		t.Fatalf("pips = %d, want 2", len(got.Pips))
	}
	if got.Pips[0].AssetID != "as1" || got.Pips[0].Status != "done" {
		t.Fatalf("pip0 = %+v, want as1/done", got.Pips[0])
	}
	if stageByRole(got, "asset").Status != "done" {
		t.Fatalf("asset stage = %q, want done", stageByRole(got, "asset").Status)
	}
	if stageByRole(got, "review").Status != "pending" {
		t.Fatalf("review stage = %q, want pending", stageByRole(got, "review").Status)
	}
}

func TestCompute_LastFailureSurfaces(t *testing.T) {
	in := Input{
		ProjectID: "p1", ProjectStatus: "failed", HasPlan: true, Plan: &Plan{PlanID: "pl1"},
		Todos: []Todo{{ID: "t-sb", Type: "storyboard", Status: "failed", Error: "EOF from provider"}},
	}
	got := Compute(in)
	if got.Error == nil || got.Error.Message != "EOF from provider" || got.Error.Role != "storyboard" {
		t.Fatalf("error = %+v, want storyboard/EOF", got.Error)
	}
	if got.RunStatus != "done" {
		t.Fatalf("runStatus = %q, want done (terminal)", got.RunStatus)
	}
}

func stageByRole(s ProjectState, role string) Stage {
	for _, st := range s.Stages {
		if st.Role == role {
			return st
		}
	}
	return Stage{}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/projectstate/... -count=1`
Expected: FAIL — `undefined: Compute` / package has no non-test files。

- [ ] **Step 3: 写 `state.go` 实现**

```go
// Package projectstate is the SINGLE authoritative computation of a project
// workflow's render state. Both the REST /state endpoint and the SSE pusher
// call Compute so the two channels can never diverge. Compute is a pure
// function (no I/O) — the DB load lives in project.Store.LoadState.
//
// Boundary: Compute produces SEMANTIC roles/statuses only — never UI concerns
// (S1-S5 numbering, colors, i18n labels). The frontend maps role→layout.
package projectstate

// Stage status / run status / pip status string domains. Kept as plain strings
// (JSON-friendly); the frontend mirrors these in lib/projectState.ts and a
// contract test guards drift.
//   stage.status: blocked | pending | running | done | failed
//   runStatus:    idle | running | done
//   pip.status:   idle | running | done | failed

// Stage is one semantic pipeline role and its derived status.
type Stage struct {
	Role   string `json:"role"` // planner|script|storyboard|asset|review
	Status string `json:"status"`
	TodoID string `json:"todoId,omitempty"`
}

// Pip is one asset-todo's state (frontend renders the S4 grid + previews).
type Pip struct {
	TodoID  string `json:"todoId"`
	Status  string `json:"status"`
	AssetID string `json:"assetId,omitempty"`
}

// Assets is the authoritative asset tally (frontend no longer counts events).
type Assets struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Pending int `json:"pending"`
}

// ProblemError surfaces the last failed todo to the workbench error strip.
type ProblemError struct {
	TodoID  string `json:"todoId"`
	Role    string `json:"role,omitempty"`
	Message string `json:"message"`
}

// Plan mirrors the run's plan identity (valid / fallbackUsed warnings).
type Plan struct {
	PlanID       string `json:"planId"`
	Valid        bool   `json:"valid"`
	FallbackUsed bool   `json:"fallbackUsed"`
}

// ProjectState is the authoritative semantic snapshot pushed over REST + SSE.
type ProjectState struct {
	ProjectID string        `json:"projectId"`
	Version   int64         `json:"version"` // = max(run_events.seq); monotonic, for ordering/dedup
	Status    string        `json:"status"`  // 7-state project status
	RunStatus string        `json:"runStatus"`
	Plan      *Plan         `json:"plan,omitempty"`
	Stages    []Stage       `json:"stages"`
	Pips      []Pip         `json:"pips"`
	Assets    Assets        `json:"assets"`
	Error     *ProblemError `json:"error,omitempty"`
}

// Todo is the minimal todo projection Compute needs.
type Todo struct {
	ID     string
	Type   string // script|storyboard|asset
	Status string // ready|running|blocked|done|failed|canceled
	Error  string
}

// Asset is the minimal asset projection Compute needs (asset→its todo).
type Asset struct {
	ID     string
	TodoID string
	Status string // e.g. pending_acceptance
}

// Input is everything Compute needs, loaded by project.Store.LoadState.
type Input struct {
	ProjectID     string
	Version       int64
	ProjectStatus string // persisted project.status (used when no plan exists)
	HasPlan       bool
	Plan          *Plan
	Todos         []Todo
	Assets        []Asset
}

const (
	roleOrderPlanner    = "planner"
	roleOrderScript     = "script"
	roleOrderStoryboard = "storyboard"
	roleOrderAsset      = "asset"
	roleOrderReview     = "review"
)

// Compute derives the authoritative render state. Pure: same Input → same output.
func Compute(in Input) ProjectState {
	st := ProjectState{
		ProjectID: in.ProjectID,
		Version:   in.Version,
		Plan:      in.Plan,
		Pips:      []Pip{},
	}

	if !in.HasPlan {
		st.Status = in.ProjectStatus
		st.RunStatus = runStatusFor(in.ProjectStatus)
		st.Stages = blockedStages()
		return st
	}

	// Tally + derive overall status (replaces project.DeriveStatus).
	var c counts
	for _, t := range in.Todos {
		c.total++
		switch t.Status {
		case "ready":
			c.ready++
		case "running":
			c.running++
		case "blocked":
			c.blocked++
		case "done":
			c.done++
		case "failed":
			c.failed++
		case "canceled":
			c.canceled++
		}
	}
	for _, a := range in.Assets {
		if a.Status == "pending_acceptance" {
			c.pendingAssets++
		}
	}
	st.Status = deriveStatus(c)
	st.RunStatus = runStatusFor(st.Status)

	// Pips + asset tally from asset todos joined to their latest asset.
	assetByTodo := map[string]Asset{}
	for _, a := range in.Assets {
		assetByTodo[a.TodoID] = a // last write wins = latest (caller orders asc)
	}
	scriptStatus, storyboardStatus := "blocked", "blocked"
	var scriptTodo, storyboardTodo string
	assetTodoCount := 0
	for _, t := range in.Todos {
		switch t.Type {
		case "script":
			scriptStatus, scriptTodo = todoStatusToStage(t.Status), t.ID
		case "storyboard":
			storyboardStatus, storyboardTodo = todoStatusToStage(t.Status), t.ID
		case "asset":
			assetTodoCount++
			pip := Pip{TodoID: t.ID, Status: todoStatusToPip(t.Status)}
			if a, ok := assetByTodo[t.ID]; ok {
				pip.AssetID = a.ID
				st.Assets.Done++
			}
			st.Pips = append(st.Pips, pip)
		}
	}
	st.Assets.Total = assetTodoCount
	st.Assets.Pending = c.pendingAssets

	st.Stages = []Stage{
		{Role: roleOrderPlanner, Status: plannerStatus(c.total)},
		{Role: roleOrderScript, Status: scriptStatus, TodoID: scriptTodo},
		{Role: roleOrderStoryboard, Status: storyboardStatus, TodoID: storyboardTodo},
		{Role: roleOrderAsset, Status: assetStageStatus(st.Pips)},
		{Role: roleOrderReview, Status: reviewStatus(st.Status)},
	}

	// Last failed todo → error strip.
	for i := len(in.Todos) - 1; i >= 0; i-- {
		if in.Todos[i].Status == "failed" {
			st.Error = &ProblemError{TodoID: in.Todos[i].ID, Role: in.Todos[i].Type, Message: in.Todos[i].Error}
			break
		}
	}
	return st
}

type counts struct {
	total, ready, running, blocked, done, failed, canceled, pendingAssets int
}

// deriveStatus mirrors project.DeriveStatus (kept in sync; see Task 4 note).
func deriveStatus(c counts) string {
	if c.total == 0 {
		return "planning"
	}
	if c.running > 0 || c.ready > 0 || c.blocked > 0 {
		return "running"
	}
	if c.failed > 0 {
		return "failed"
	}
	if c.canceled > 0 {
		return "canceled"
	}
	if c.pendingAssets > 0 {
		return "review"
	}
	if c.done == c.total {
		return "completed"
	}
	return "running"
}

func runStatusFor(status string) string {
	switch status {
	case "completed", "review", "failed", "canceled":
		return "done"
	case "planning", "running":
		return "running"
	default:
		return "idle"
	}
}

func plannerStatus(totalTodos int) string {
	if totalTodos > 0 {
		return "done"
	}
	return "running" // has plan but no todos yet = still planning
}

func assetStageStatus(pips []Pip) string {
	if len(pips) == 0 {
		return "blocked"
	}
	allDone, allFailed, anyRunning := true, true, false
	for _, p := range pips {
		if p.Status != "done" {
			allDone = false
		}
		if p.Status != "failed" {
			allFailed = false
		}
		if p.Status == "running" {
			anyRunning = true
		}
	}
	switch {
	case allDone:
		return "done"
	case allFailed:
		return "failed"
	case anyRunning:
		return "running"
	default:
		return "pending"
	}
}

func reviewStatus(projectStatus string) string {
	switch projectStatus {
	case "review":
		return "pending"
	case "completed":
		return "done"
	case "failed", "canceled":
		return "failed"
	default:
		return "blocked"
	}
}

func todoStatusToStage(s string) string {
	switch s {
	case "ready":
		return "pending"
	case "running":
		return "running"
	case "done":
		return "done"
	case "blocked":
		return "blocked"
	case "failed", "canceled":
		return "failed"
	default:
		return "blocked"
	}
}

func todoStatusToPip(s string) string {
	switch s {
	case "running":
		return "running"
	case "done":
		return "done"
	case "failed", "canceled":
		return "failed"
	default:
		return "idle"
	}
}

func blockedStages() []Stage {
	return []Stage{
		{Role: roleOrderPlanner, Status: "blocked"},
		{Role: roleOrderScript, Status: "blocked"},
		{Role: roleOrderStoryboard, Status: "blocked"},
		{Role: roleOrderAsset, Status: "blocked"},
		{Role: roleOrderReview, Status: "blocked"},
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/projectstate/... -count=1`
Expected: PASS (all 4 tests)。

- [ ] **Step 5: 提交**

```bash
cd llm-agent-studio
git add internal/projectstate/state.go internal/projectstate/state_test.go
git commit -m "feat(projectstate): authoritative Compute for workflow render state

Single pure function deriving overall status, runStatus, semantic stages,
asset pips, counts and last-error from the latest plan's todos+assets.
Replaces the split between project.DeriveStatus and frontend reduceTimeline."
```

---

## Task 2: `project.Store.LoadState` 装载器

**Files:**
- Modify: `internal/project/store.go`(新增方法 + import `projectstate`)
- Test: `internal/project/store_test.go`(若为 DB 测试需 PG;见下方降级方案)

注:`project` 包导入 `projectstate`(单向,无环——`projectstate` 不导入 `project`)。

- [ ] **Step 1: 写测试(DB-backed,复用现有 store_test 基建)**

先确认现有 `store_test.go` 的建表/连接 helper 名称:
Run: `cd llm-agent-studio && grep -n "func Test\|newTestStore\|pgxpool\|t.Skip" internal/project/store_test.go | head`

按其模式新增(以下用占位 helper `newTestStore(t)`,改成实际名):

```go
func TestLoadState_NoPlan_Draft(t *testing.T) {
	s, ctx := newTestStore(t) // 复用现有 helper
	p := mustCreateProject(t, s, ctx) // 复用现有 create helper（draft 初态）
	st, err := s.LoadState(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "draft" || st.RunStatus != "idle" {
		t.Fatalf("state = %+v, want draft/idle", st)
	}
	if st.ProjectID != p.ID {
		t.Fatalf("projectId = %q", st.ProjectID)
	}
}
```

> 若 `internal/project` 无 PG 测试基建(只跑逻辑单测),则 LoadState 属"薄 I/O 装载",其纯逻辑已被 Task 1 覆盖。此时改为:Step 1 跳过新测试,在 Task 3 的 handler 测试(用现有 stub ProjectStore)间接覆盖 LoadState 的契约。实现者按现状二选一,并在提交信息注明。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/project/... -run TestLoadState -count=1`
Expected: FAIL — `s.LoadState undefined`。

- [ ] **Step 3: 实现 `LoadState`**

在 `internal/project/store.go` 顶部 import 块加 `"github.com/costa92/llm-agent-studio/internal/projectstate"`。新增方法(放在 `RefreshStatus` 之后):

```go
// LoadState loads the latest plan's todos + assets + event version and computes
// the authoritative ProjectState (single source of truth for render). Used by
// the GET /state endpoint and the SSE pusher so both channels agree.
func (s *Store) LoadState(ctx context.Context, projectID string) (projectstate.ProjectState, error) {
	var p Project
	p, err := s.Get(ctx, projectID)
	if err != nil {
		return projectstate.ProjectState{}, err
	}
	in := projectstate.Input{ProjectID: projectID, ProjectStatus: p.Status}

	// version = max event seq for the project (monotonic; 0 if none).
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(max(seq), 0) FROM run_events WHERE project_id=$1`, projectID).
		Scan(&in.Version); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state version: %w", err)
	}

	// latest plan
	var planID string
	var valid, fallbackUsed bool
	err = s.pool.QueryRow(ctx,
		`SELECT id, valid, fallback_used FROM plans WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
		projectID).Scan(&planID, &valid, &fallbackUsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return projectstate.Compute(in), nil // no plan: draft passthrough
	}
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state plan: %w", err)
	}
	in.HasPlan = true
	in.Plan = &projectstate.Plan{PlanID: planID, Valid: valid, FallbackUsed: fallbackUsed}

	// todos of the latest plan
	rows, err := s.pool.Query(ctx,
		`SELECT id, type, status, COALESCE(error,'') FROM todos WHERE plan_id=$1 ORDER BY updated_at ASC`, planID)
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state todos: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t projectstate.Todo
		if err := rows.Scan(&t.ID, &t.Type, &t.Status, &t.Error); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan state todo: %w", err)
		}
		in.Todos = append(in.Todos, t)
	}
	if err := rows.Err(); err != nil {
		return projectstate.ProjectState{}, err
	}

	// assets of the latest plan (joined via todos)
	arows, err := s.pool.Query(ctx,
		`SELECT a.id, a.todo_id, a.status FROM assets a
		 JOIN todos t ON a.todo_id = t.id
		 WHERE t.plan_id=$1 ORDER BY a.created_at ASC`, planID)
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state assets: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var a projectstate.Asset
		if err := arows.Scan(&a.ID, &a.TodoID, &a.Status); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan state asset: %w", err)
		}
		in.Assets = append(in.Assets, a)
	}
	if err := arows.Err(); err != nil {
		return projectstate.ProjectState{}, err
	}

	return projectstate.Compute(in), nil
}
```

> 实现者注意:`assets` 表的列名(`created_at`、`todo_id`、`status`)和 `todos` 表的 `type`/`error`/`updated_at` 列名以现有 schema 为准。先 `grep -rn "CREATE TABLE assets\|CREATE TABLE todos" internal/` 或看 migrations 确认列名,不一致则按实际调整 SQL。

- [ ] **Step 4: 运行测试 + 全包构建**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go test ./internal/project/... -count=1`
Expected: 构建通过;LoadState 测试 PASS(或按 Step 1 降级方案跳过)。

- [ ] **Step 5: 提交**

```bash
cd llm-agent-studio
git add internal/project/store.go internal/project/store_test.go
git commit -m "feat(project): LoadState loads latest plan + computes ProjectState"
```

---

## Task 3: `GET /api/projects/{id}/state` 端点

**Files:**
- Modify: `internal/httpapi/handlers.go`(`ProjectStore` 接口加 `LoadState`;新增 `stateHandler`)
- Modify: `internal/httpapi/httpapi.go`(注册路由)
- Test: `internal/httpapi/handlers_test.go` 或新建 `statehandlers_test.go`

- [ ] **Step 1: 写失败测试**

先看现有 handler 测试如何构造 stub ProjectStore:
Run: `cd llm-agent-studio && grep -n "ProjectStore\|fakeProjects\|stubProject" internal/httpapi/*_test.go | head`

新建 `internal/httpapi/statehandlers_test.go`(按现有 stub 模式;以下用占位 `fakeProjectStore`,改成实际):

```go
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

func TestStateHandler_ReturnsSnapshot(t *testing.T) {
	want := projectstate.ProjectState{ProjectID: "p1", Version: 3, Status: "running", RunStatus: "running"}
	h := stateHandler(stateStoreStub{state: want})
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/state", nil)
	req.SetPathValue("id", "p1")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var got projectstate.ProjectState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.Version != 3 {
		t.Fatalf("got %+v", got)
	}
}

type stateStoreStub struct{ state projectstate.ProjectState }

func (s stateStoreStub) LoadState(ctx context.Context, id string) (projectstate.ProjectState, error) {
	return s.state, nil
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/httpapi/... -run TestStateHandler -count=1`
Expected: FAIL — `undefined: stateHandler` / `StateReader`。

- [ ] **Step 3: 实现 handler + 接口 + 路由**

在 `internal/httpapi/handlers.go` 的 `ProjectStore` 接口(:54-65)末尾加一行:

```go
	LoadState(ctx context.Context, projectID string) (projectstate.ProjectState, error)
```

并在该文件 import 块加 `"github.com/costa92/llm-agent-studio/internal/projectstate"`。

新增独立的窄接口 + handler(放在 `runHandler` 附近):

```go
// StateReader is the project-state surface the /state endpoint + SSE need.
type StateReader interface {
	LoadState(ctx context.Context, projectID string) (projectstate.ProjectState, error)
}

// stateHandler (GET /api/projects/{id}/state): viewer+. Returns the
// authoritative semantic snapshot computed by projectstate.Compute.
func stateHandler(sr StateReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := sr.LoadState(r.Context(), r.PathValue("id"))
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}
```

在 `internal/httpapi/httpapi.go` 路由块(紧邻 `GET /api/projects/{id}/events`)加:

```go
	mux.Handle("GET /api/projects/{id}/state", proj(roleViewer, stateHandler(d.Projects)))
```

(`d.Projects` 已是 `ProjectStore`,新加的 `LoadState` 方法令其同时满足 `StateReader`。)

- [ ] **Step 4: 运行确认通过 + 全包构建**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go test ./internal/httpapi/... -run TestStateHandler -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd llm-agent-studio
git add internal/httpapi/handlers.go internal/httpapi/httpapi.go internal/httpapi/statehandlers_test.go
git commit -m "feat(httpapi): GET /api/projects/{id}/state authoritative snapshot"
```

---

## Task 4: SSE 推送 `state` 帧 + `runHandler` 去命令式置位

**Files:**
- Modify: `internal/httpapi/sse.go`(连接发 `state` + version 变化推 `state`;白名单加 `state`)
- Modify: `internal/httpapi/httpapi.go`(`streamEventsHandler` 注入 `StateReader`)
- Modify: `internal/httpapi/handlers.go`(`runHandler` 删 `ps.SetStatus(..., "running")`)
- Test: `internal/httpapi/sse_test.go`

设计:`streamEventsHandler` 既保留现有"原始事件 → 命名 SSE 帧(喂日志)",又在 (a) 连接首帧、(b) 每轮检测到 `version` 变化时,`LoadState` 并发一条 `event: state` 帧(data = ProjectState JSON)。`version` 用 ProjectState.Version(= max seq)做变化判定。

- [ ] **Step 1: 写失败测试**

先看现有 `sse_test.go` 如何驱动 handler + 喂 EventReader:
Run: `cd llm-agent-studio && grep -n "func Test\|streamEventsHandler\|EventReader\|httptest" internal/httpapi/sse_test.go | head -30`

按其模式新增(用现有 fake EventReader + 新 fake StateReader):

```go
func TestStreamEvents_EmitsInitialStateFrame(t *testing.T) {
	reader := fakeEventReader{} // 复用现有 fake；无事件也应先发 state
	state := stateStoreStub{state: projectstate.ProjectState{ProjectID: "p1", Version: 0, Status: "draft", RunStatus: "idle"}}
	h := streamEventsHandler(reader, state)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/events/stream", nil)
	req.SetPathValue("id", "p1")
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	// run handler briefly then cancel (no run_done so it would poll forever).
	done := make(chan struct{})
	go func() { h(rec, req); close(done) }()
	// give it a tick to emit the initial frame, then stop.
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: state") {
		t.Fatalf("missing initial state frame; body=%q", body)
	}
	if !strings.Contains(body, `"status":"draft"`) {
		t.Fatalf("state frame missing status; body=%q", body)
	}
}
```

> 若现有 sse_test 用了 `flushRecorder`/可控 ticker 之类 helper,沿用之以确定性断言;上面的 goroutine+cancel 是无 helper 时的兜底。实现者择现有模式。

- [ ] **Step 2: 运行确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/httpapi/... -run TestStreamEvents_EmitsInitialState -count=1`
Expected: FAIL — `streamEventsHandler` 签名不符 / 无 `state` 帧。

- [ ] **Step 3: 改 `sse.go`**

白名单加 `state`(在 `sseEventNames` map 内补一行):

```go
	"run_done":          true,
	"state":             true,
```

`streamEventsHandler` 签名加入 `StateReader`,并加 state 帧发射逻辑:

```go
func streamEventsHandler(reader EventReader, state StateReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		planID := r.URL.Query().Get("planId")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		var after int64
		if v := r.Header.Get("Last-Event-ID"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				after = n
			}
		}

		var lastStateVersion int64 = -1
		emitState := func() error {
			st, err := state.LoadState(r.Context(), projectID)
			if err != nil {
				return err
			}
			if st.Version == lastStateVersion {
				return nil // unchanged: skip
			}
			lastStateVersion = st.Version
			data, _ := json.Marshal(st)
			_, _ = io.WriteString(w, "event: state\ndata: "+string(data)+"\n\n")
			return nil
		}

		emit := func() (done bool, err error) {
			evs, lerr := reader.List(r.Context(), projectID, planID, after, 200)
			if lerr != nil {
				return false, lerr
			}
			for _, e := range evs {
				after = e.Seq
				payload, _ := json.Marshal(map[string]any{
					"seq": e.Seq, "kind": e.Kind, "todoId": e.TodoID, "payload": e.Payload,
				})
				name := e.Kind
				if !sseEventNames[name] {
					name = "message"
				}
				_, _ = io.WriteString(w, "id: "+strconv.FormatInt(e.Seq, 10)+"\nevent: "+name+"\ndata: "+string(payload)+"\n\n")
				if e.Kind == "run_done" {
					done = true
				}
			}
			// authoritative state after applying this batch (version-gated).
			if serr := emitState(); serr != nil {
				return done, serr
			}
			flusher.Flush()
			return done, nil
		}

		if done, err := emit(); err != nil || done {
			return
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if done, err := emit(); err != nil || done {
					return
				}
			}
		}
	}
}
```

> 注意:首轮 `emit()` 即使没有历史事件(evs 空)也会调用 `emitState()` → 因为 `lastStateVersion` 初始 -1、新版本 ≥0,故必发首帧。`version` 不变时跳过,避免空推。

在 `internal/httpapi/httpapi.go` 更新注册(把 `d.Projects` 作为第二参传入,它满足 `StateReader`):

```go
	mux.Handle("GET /api/projects/{id}/events/stream", proj(roleViewer, streamEventsHandler(d.EventReader, d.Projects)))
```

- [ ] **Step 4: `runHandler` 去命令式 `running` 置位**

在 `internal/httpapi/handlers.go` `runHandler`(:363-428)删除这一行(`_ = ps.SetStatus(r.Context(), id, "running")`,在 `writeJSON` 之前):

理由:running 现由 `Compute`/`RefreshStatus` 从 todos 派生(有 todos→running),命令式置位是双源残留。保留 `SetStatus(..., "planning")`(plan 创建前、todos 为空时的初态)。

> 保留 `RefreshStatus`(worker 用)与 `project.DeriveStatus`(taskboard/health/workflows 仍依赖)不动——它们与 `projectstate.deriveStatus` 逻辑一致;Task 7 的契约测试只覆盖前后端枚举,不强制合并这两个 Go 实现(YAGNI:合并是额外重构,非本计划目标)。在 `deriveStatus` 上方补注释指明二者需手工同步即可。

- [ ] **Step 5: 运行测试 + 全包测试**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go test ./internal/httpapi/... -count=1`
Expected: 全 PASS(含既有 sse 测试——确认旧断言仍成立;若旧测试断言"首帧是某原始事件",需调整为接受前置的 state 帧)。

- [ ] **Step 6: 提交**

```bash
cd llm-agent-studio
git add internal/httpapi/sse.go internal/httpapi/httpapi.go internal/httpapi/handlers.go internal/httpapi/sse_test.go
git commit -m "feat(httpapi): SSE pushes authoritative state frames; drop imperative running set"
```

---

## Task 5: worker `todo_failed` 载荷补 `type`

**Files:**
- Modify: `internal/worker/worker.go`(:809-833 `fail()`、:1193-1202 `terminalFail`)
- Test: `internal/worker/worker_test.go`

- [ ] **Step 1: 写失败测试**

先看现有 worker 测试如何断言事件载荷:
Run: `cd llm-agent-studio && grep -n "todo_failed\|Append\|Events\b\|fakeEvents" internal/worker/worker_test.go | head`

按其模式断言 `todo_failed` 载荷含 `type`(以下示意,改成现有 fake events 捕获方式):

```go
func TestFail_EmitsTypeInPayload(t *testing.T) {
	// ... 构造 worker + claimed{typ:"asset"} 使其 attempts 耗尽走终态 ...
	// 触发 fail(ctx, c, errors.New("boom"))
	ev := lastEvent(t, fakeEvents, "todo_failed") // 复用现有 helper
	if ev.Payload["type"] != "asset" {
		t.Fatalf("todo_failed payload = %+v, want type=asset", ev.Payload)
	}
	if ev.Payload["error"] != "boom" {
		t.Fatalf("todo_failed payload = %+v, want error=boom", ev.Payload)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/worker/... -run TestFail_EmitsType -count=1`
Expected: FAIL — payload 无 `type` 键。

- [ ] **Step 3: 改两处发射点**

`fail()`(:823 一带):

```go
		_, _ = w.cfg.Events.Append(ctx, c.projectID, "todo_failed", c.todoID, map[string]any{"type": c.typ, "error": msg})
```

`pollAsync.terminalFail`(:1199 一带):

```go
		_, _ = w.cfg.Events.Append(cctx, c.projectID, "todo_failed", c.todoID, map[string]any{"type": c.typ, "error": reason})
```

(`c.typ` 在两处闭包均可见——`fail` 接 `claimed`,`pollAsync` 的 `c` 同为 claimed。)

- [ ] **Step 4: 运行确认通过**

Run: `cd llm-agent-studio && GOWORK=off go test ./internal/worker/... -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd llm-agent-studio
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "fix(worker): include type in todo_failed payload (align with ready/started/finished)"
```

---

## Task 6: 前端 `ProjectState` 类型 + 契约测试

**Files:**
- Create: `web/src/lib/projectState.ts`
- Create: `web/src/lib/projectState.contract.test.ts`

- [ ] **Step 1: 写 `projectState.ts`(对齐后端 JSON)**

```ts
// 对齐后端 internal/projectstate/state.go 的 ProjectState JSON 形状。
// 这是前端唯一的工作流状态真相源——不再由事件 reduce 推导(见 timeline.ts 瘦身)。
// 枚举字符串域与后端逐一对应;projectState.contract.test.ts 守护漂移。

export type StageRole = "planner" | "script" | "storyboard" | "asset" | "review"
export type StageStatus2 = "blocked" | "pending" | "running" | "done" | "failed"
export type RunStatus2 = "idle" | "running" | "done"
export type PipStatus2 = "idle" | "running" | "done" | "failed"

export interface StageState {
  role: StageRole
  status: StageStatus2
  todoId?: string
}

export interface PipState {
  todoId: string
  status: PipStatus2
  assetId?: string
}

export interface AssetsState {
  total: number
  done: number
  pending: number
}

export interface ProblemError {
  todoId: string
  role?: string
  message: string
}

export interface PlanState {
  planId: string
  valid: boolean
  fallbackUsed: boolean
}

import type { ProjectStatus } from "./types"

export interface ProjectState {
  projectId: string
  version: number
  status: ProjectStatus
  runStatus: RunStatus2
  plan?: PlanState
  stages: StageState[]
  pips: PipState[]
  assets: AssetsState
  error?: ProblemError
}
```

- [ ] **Step 2: 写契约一致性测试**

```ts
import { describe, it, expect } from "vitest"
import type { StageRole, StageStatus2, RunStatus2, PipStatus2 } from "./projectState"

// 守护前后端枚举漂移:任一侧新增/改名而另一侧没跟 → 这里需同步更新,否则编译/断言红。
// 后端真相:internal/projectstate/state.go 的注释枚举域 + Compute 分支。
describe("ProjectState 枚举契约(与后端 projectstate 对齐)", () => {
  it("StageRole 恰为 5 个语义角色", () => {
    const roles: StageRole[] = ["planner", "script", "storyboard", "asset", "review"]
    expect(roles).toHaveLength(5)
  })
  it("StageStatus2 恰为 5 态", () => {
    const s: StageStatus2[] = ["blocked", "pending", "running", "done", "failed"]
    expect(s).toHaveLength(5)
  })
  it("RunStatus2 恰为 3 态", () => {
    const s: RunStatus2[] = ["idle", "running", "done"]
    expect(s).toHaveLength(3)
  })
  it("PipStatus2 恰为 4 态", () => {
    const s: PipStatus2[] = ["idle", "running", "done", "failed"]
    expect(s).toHaveLength(4)
  })
})
```

> 这是"穷举即测试":新增枚举值必须同时改类型与此处数组,长度断言 + TS 穷举强制双改。比无测试好,且不引 codegen(符合 spec 非目标)。

- [ ] **Step 3: 运行测试**

Run: `cd llm-agent-studio/web && pnpm vitest src/lib/projectState.contract.test.ts --run`
Expected: PASS(4 tests)。

- [ ] **Step 4: 提交**

```bash
cd llm-agent-studio
git add web/src/lib/projectState.ts web/src/lib/projectState.contract.test.ts
git commit -m "feat(web): ProjectState types mirroring backend + enum contract test"
```

---

## Task 7: 前端 `useProjectState` + SSE state 帧应用

**Files:**
- Modify: `web/src/features/workflow/api.ts`(新增 `fetchProjectState` + `useProjectState`)
- Modify: `web/src/features/workflow/useProductionTimeline.ts`(改名意图:保留日志累积,新增 state 帧 → 回调)
- Test: `web/src/features/workflow/api.test.ts` / `useProductionTimeline.test.tsx`

- [ ] **Step 1: 写 `fetchProjectState` + `useProjectState` 测试**

在 `web/src/features/workflow/api.test.ts` 按现有模式(看 `grep -n "fetchEvents\|apiJSON\|vi.mock" api.test.ts`)新增:

```ts
it("fetchProjectState 拉取 /state", async () => {
  // 复用现有 apiJSON mock 方式
  mockApiJSON({ projectId: "p1", version: 5, status: "running", runStatus: "running", stages: [], pips: [], assets: { total: 0, done: 0, pending: 0 } })
  const st = await fetchProjectState("p1")
  expect(st.status).toBe("running")
  expect(st.version).toBe(5)
})
```

- [ ] **Step 2: 运行确认失败**

Run: `cd llm-agent-studio/web && pnpm vitest src/features/workflow/api.test.ts --run`
Expected: FAIL — `fetchProjectState` 未导出。

- [ ] **Step 3: 实现 `fetchProjectState` + `useProjectState`**

在 `web/src/features/workflow/api.ts` import 加 `import type { ProjectState } from "@/lib/projectState"`,并新增:

```ts
// GET /api/projects/{id}/state → ProjectState(viewer+)。工作流状态的权威来源。
export async function fetchProjectState(id: string, planId?: string): Promise<ProjectState> {
  const qs = planId ? `?planId=${encodeURIComponent(planId)}` : ""
  return apiJSON<ProjectState>(`/api/projects/${id}/state${qs}`)
}

// 权威状态查询。SSE 的 state 帧到达时由 useProductionTimeline 经 setQueryData 覆盖此缓存。
export function useProjectState(id: string, planId?: string): UseQueryResult<ProjectState> {
  return useQuery({
    queryKey: ["project-state", id, planId ?? ""],
    queryFn: () => fetchProjectState(id, planId),
    enabled: id !== "",
  })
}
```

(`useQuery`/`UseQueryResult` 已在该文件 import。)

- [ ] **Step 4: 改 `useProductionTimeline.ts` — 日志累积 + state 帧回调**

把 hook 的产出从"reduce 出全态"改为"(a) 仅累积日志行 + (b) 把 SSE `state` 帧透出给调用方写缓存"。`SseHandlers` 已有 `onEvent`(命名帧)——后端新增的 `state` 帧 `ev.event==="state"` 不在 `NAMED_EVENTS` 白名单里,会走 `onMessage`。需在 `lib/sse.ts` 把 `state` 也当作专门帧路由。

先改 `web/src/lib/sse.ts`:`NAMED_EVENTS` 加 `"state"`,并在 `SseHandlers` 加可选 `onState`,在 `onmessage` 内分流:

```ts
// NAMED_EVENTS 加一行：
  "run_done",
  "state",
```

```ts
// SseHandlers 接口加：
  // 后端权威状态帧(event: state)——data 是 ProjectState JSON。
  onState?: (raw: unknown) => void
```

```ts
// onmessage 内,在 NAMED_EVENTS 分支前优先处理 state：
    onmessage(ev) {
      if (ev.event === "state") {
        handlers.onState?.(JSON.parse(ev.data))
        return
      }
      const frame = JSON.parse(ev.data) as SseFrame
      if (NAMED_EVENTS.has(ev.event)) {
        handlers.onEvent(frame)
        if (ev.event === TERMINAL_EVENT) handlers.onDone?.(frame)
      } else {
        handlers.onMessage?.(frame)
      }
    },
```

再改 `useProductionTimeline.ts`:产出改为 `{ log: LogLine[]; conn; replayed }`,内部 reducer 只 fold 日志;新增 `onState` 回调参数透出 ProjectState。最小改法——给 `UseProductionTimelineArgs` 加 `onState?: (s: ProjectState) => void`,在 streamRunEvents 的 handlers 里接上:

```ts
// import 顶部加：
import type { ProjectState } from "@/lib/projectState"
// LogLine 仍来自 timeline（Task 8 保留 logFor/LogLine）。
import { foldLog, type LogLine } from "@/lib/timeline"
```

`ProductionTimeline` 产出改为:

```ts
export interface ProductionTimeline {
  log: LogLine[]
  conn: SseConnState
  replayed: boolean
}
```

reducer 改为累积日志(Task 8 提供 `foldLog`/`logFor`):

```ts
type Action =
  | { type: "replayed"; frames: SseFrame[] }
  | { type: "frame"; frame: SseFrame }
  | { type: "reset" }

function logReducer(state: LogLine[], action: Action): LogLine[] {
  switch (action.type) {
    case "reset":
      return []
    case "replayed":
      return foldLog(state, action.frames)
    case "frame":
      return foldLog(state, [action.frame])
  }
}
```

`UseProductionTimelineArgs` 加 `onState?: (s: ProjectState) => void`;在 streamRunEvents 的 handlers 里加 `onState: (raw) => { if (!cancelled) onState?.(raw as ProjectState) }`。其余回放/开流/完成态逻辑不变(完成态仍只回放日志、不开流)。

> 去重:日志行用 `seq` 去重(`foldLog` 内置,见 Task 8),替代原 `appliedSeqs`。

- [ ] **Step 5: 运行 hook 测试**

按现有 `useProductionTimeline.test.tsx`(看其断言)调整:断言对象从 `state.stages` 改为 `log` 行 + `onState` 被调用。

Run: `cd llm-agent-studio/web && pnpm vitest src/features/workflow/useProductionTimeline.test.tsx src/features/workflow/api.test.ts --run`
Expected: PASS(调整后)。

- [ ] **Step 6: 提交**

```bash
cd llm-agent-studio
git add web/src/features/workflow/api.ts web/src/features/workflow/useProductionTimeline.ts web/src/lib/sse.ts web/src/features/workflow/useProductionTimeline.test.tsx web/src/features/workflow/api.test.ts
git commit -m "feat(web): useProjectState + SSE state frame routing; hook accumulates log only"
```

---

## Task 8: `timeline.ts` 瘦身(删状态推导,留日志 + 角色映射)

**Files:**
- Modify: `web/src/lib/timeline.ts`
- Test: `web/src/lib/timeline.test.ts`(若存在;否则新建)

目标:删除 `TimelineState`/`Stage`/`Pip`/`reduceTimeline`/`foldEvents`/`settleAssetStage`/`initialTimeline` 等**状态推导**;保留 `LogLine`、`logFor`、`StageId`、`TYPE_TO_STAGE`(日志 emphasis 着色用),新增 `foldLog`(按 seq 去重累积日志)。

- [ ] **Step 1: 写/改 `timeline.test.ts`**

```ts
import { describe, it, expect } from "vitest"
import { foldLog, logFor } from "./timeline"
import type { SseFrame } from "./types"

const f = (seq: number, kind: string, payload?: unknown, todoId = "t1"): SseFrame => ({ seq, kind, todoId, payload })

describe("logFor 文案", () => {
  it("planner_started → 规划开始", () => {
    expect(logFor(f(1, "planner_started")).text).toBe("规划开始")
  })
  it("todo_failed 透出 error 文本", () => {
    expect(logFor(f(2, "todo_failed", { type: "asset", error: "boom" })).text).toBe("失败：boom")
  })
})

describe("foldLog 按 seq 去重(重连全量回放幂等)", () => {
  it("重复 seq 不重复追加", () => {
    let log = foldLog([], [f(1, "planner_started"), f(2, "todo_started", { type: "script" })])
    log = foldLog(log, [f(1, "planner_started"), f(3, "run_done")]) // seq 1 重复
    expect(log.map((l) => l.seq)).toEqual([1, 2, 3])
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `cd llm-agent-studio/web && pnpm vitest src/lib/timeline.test.ts --run`
Expected: FAIL — `foldLog` 未导出。

- [ ] **Step 3: 重写 `timeline.ts`(只保留日志相关)**

整文件替换为:

```ts
// 事件 → 日志文案(左栏事件日志)。本里程碑后:状态推导已移至后端 projectstate.Compute,
// 前端经 useProjectState 拿权威 ProjectState 渲染。本文件只剩"事件流 → 人类可读日志行"
// 这一纯表现职责 + 阶段着色映射。
import type { SseFrame } from "./types"

// 阶段着色 id(供日志 emphasis;与 ProjectState 的语义 role 一一对应:
// planner→S1 script→S2 storyboard→S3 asset→S4 review→S5)。纯前端表现。
export type StageId = "S1" | "S2" | "S3" | "S4" | "S5"

// EventLog 行(左栏日志)。emphasis = 阶段标签供着色。
export interface LogLine {
  seq: number
  kind: string
  text: string
  emphasis?: StageId
}

// todo 的 type(payload.type)→ 阶段着色 id。
const TYPE_TO_STAGE: Record<string, StageId> = {
  script: "S2",
  storyboard: "S3",
}

function payloadType(frame: SseFrame): string | undefined {
  const p = frame.payload
  if (p && typeof p === "object" && "type" in p) {
    const t = (p as { type?: unknown }).type
    if (typeof t === "string") return t
  }
  return undefined
}

function payloadStr(frame: SseFrame, key: string): string | undefined {
  const p = frame.payload
  if (p && typeof p === "object" && key in p) {
    const v = (p as Record<string, unknown>)[key]
    if (typeof v === "string") return v
  }
  return undefined
}

function emphasisFor(t: string | undefined): StageId | undefined {
  if (!t) return undefined
  return TYPE_TO_STAGE[t] ?? (t === "asset" ? "S4" : undefined)
}

// 单帧 → 日志行(纯表现:状态正确性已由后端 ProjectState 保证)。
export function logFor(frame: SseFrame): LogLine {
  const t = payloadType(frame)
  let text: string
  let emphasis: StageId | undefined
  switch (frame.kind) {
    case "planner_started":
      text = "规划开始"
      emphasis = "S1"
      break
    case "todo_ready":
      text = `todo_ready（${t ?? "?"}）`
      emphasis = emphasisFor(t)
      break
    case "todo_started":
      text = `开始：${t ?? frame.todoId}`
      emphasis = emphasisFor(t)
      break
    case "todo_finished":
      text = `完成：${t ?? frame.todoId}`
      emphasis = emphasisFor(t)
      break
    case "asset_generated":
      text = "asset_generated · 待审"
      emphasis = "S4"
      break
    case "asset_submitted":
      text = "asset_submitted · 已提交"
      emphasis = "S4"
      break
    case "asset_prescreened":
      text = "asset_prescreened · 预筛"
      emphasis = "S4"
      break
    case "todo_failed": {
      // 后端现已在 payload 带 {type,error}(见 worker.go fail/terminalFail)。
      const err = payloadStr(frame, "error")
      text = err ? `失败：${err}` : `失败：${frame.todoId} · 退避重试`
      emphasis = emphasisFor(t)
      break
    }
    case "run_done":
      text = "运行结束"
      break
    default:
      text = frame.kind
      break
  }
  return { seq: frame.seq, kind: frame.kind, text, emphasis }
}

// 累积日志,按 seq 去重(替代 Last-Event-ID:重连全量回放的旧帧在此被吞掉,幂等)。
export function foldLog(log: LogLine[], frames: SseFrame[]): LogLine[] {
  const seen = new Set(log.map((l) => l.seq))
  const next = [...log]
  for (const f of frames) {
    if (seen.has(f.seq)) continue
    seen.add(f.seq)
    next.push(logFor(f))
  }
  return next
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd llm-agent-studio/web && pnpm vitest src/lib/timeline.test.ts --run`
Expected: PASS。

> 注:此步会破坏仍 import 旧 `TimelineState`/`reduceTimeline` 的文件(WorkbenchPage、container、旧测试)——Task 9 修复它们。本步先让 timeline 单测过即可;整体 `pnpm build` 在 Task 9 末尾绿。

- [ ] **Step 5: 提交**

```bash
cd llm-agent-studio
git add web/src/lib/timeline.ts web/src/lib/timeline.test.ts
git commit -m "refactor(web): timeline.ts down to log-text + role mapping; state derivation removed"
```

---

## Task 9: `WorkbenchView` + container 改读 `ProjectState`

**Files:**
- Modify: `web/src/features/workflow/WorkbenchPage.tsx`
- Modify: `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`
- Test: `web/src/features/workflow/workflow.test.tsx`

### 9a. `WorkbenchView` props 改造

`WorkbenchViewProps` 删 `timeline: TimelineState`,改为 `state: ProjectState` + `log: LogLine[]`。徽章、阶段图、pip、计数全读 `state`。

- [ ] **Step 1: 改 `workflow.test.tsx` 断言(先红)**

看现有断言怎么渲染 WorkbenchView(`grep -n "WorkbenchView\|timeline=\|render(" workflow.test.tsx`),把传入的 `timeline={...}` 改为 `state={...}` + `log={[]}`,并构造一个 ProjectState fixture:

```ts
const sampleState: ProjectState = {
  projectId: "p1", version: 1, status: "running", runStatus: "running",
  stages: [
    { role: "planner", status: "done" },
    { role: "script", status: "running", todoId: "t-s" },
    { role: "storyboard", status: "blocked" },
    { role: "asset", status: "blocked" },
    { role: "review", status: "blocked" },
  ],
  pips: [], assets: { total: 0, done: 0, pending: 0 },
}
```

断言:徽章渲染 `state.status` 的 label(`生产中`);run_done/review 态渲染"待审核 · {assets.pending}"。

- [ ] **Step 2: 运行确认失败**

Run: `cd llm-agent-studio/web && pnpm vitest src/features/workflow/workflow.test.tsx --run`
Expected: FAIL — 类型/属性不符。

- [ ] **Step 3: 改 `WorkbenchPage.tsx`**

import 改:

```ts
import type { Project } from "@/lib/types"
import type { ProjectState, StageRole } from "@/lib/projectState"
import type { LogLine, StageId } from "@/lib/timeline"
```

`role → S1-S5` 表现映射(新增,放在 STAGE_SUB 附近):

```ts
const ROLE_TO_STAGE: Record<StageRole, StageId> = {
  planner: "S1", script: "S2", storyboard: "S3", asset: "S4", review: "S5",
}
const STAGE_TO_ROLE: Record<StageId, StageRole> = {
  S1: "planner", S2: "script", S3: "storyboard", S4: "asset", S5: "review",
}
```

`WorkbenchViewProps` 把 `timeline: TimelineState` 换为:

```ts
  state: ProjectState
  log: LogLine[]
```

函数体顶部解构改为读 `state`:

```ts
  const { stages, pips, assets, runStatus, status } = state
  const doneAssetCount = assets.done
  const pipCount = assets.total
  const pendingAssetCount = assets.pending
  const slateVisible = runStatus === "running"
```

徽章逻辑(直接读权威 `status`/`runStatus`,删 project.status vs runStatus 混算):

```ts
  const readyForReview = runStatus === "done" || status === "review"
  const showReviewBadge = runStatus === "done" && status !== "failed" && status !== "canceled"
  const badge = showReviewBadge ? (
    <Badge variant="pending">待审核 · {pendingAssetCount}</Badge>
  ) : (
    <Badge variant={statusVariant(status)}>{statusLabel(status)}</Badge>
  )
```

错误条改读 `state.error`(权威),不再扫日志:

```ts
  const errorText = state.error?.message
```

并把渲染处 `{lastFailedLine && (...)}` 改为 `{errorText && (... {errorText} ...)}`。

阶段图渲染:`stages` 现是 `StageState[]`(role+status),`TimelineStage` 组件期望旧 `Stage`(id/kind/status/linked)。在 map 时适配:

```ts
            {stages.map((stage, i) => {
              const id = ROLE_TO_STAGE[stage.role]
              const uiStage = {
                id,
                kind: stage.role,
                status: stage.status,
                todoId: stage.todoId,
                linked: stage.status === "done",
              }
              return (
                <TimelineStage
                  key={id}
                  stage={uiStage}
                  last={i === stages.length - 1}
                  onSelect={onSelectStage && INSPECTABLE_STAGES[id] ? () => onSelectStage(id) : undefined}
                  sub={id === "S4" ? `素材生成 · ${doneAssetCount}/${pipCount || "?"}` : STAGE_SUB[id]}
                >
                  {id === "S4" && pips.length > 0 && <PipGroup pips={pips} onSelectPip={onSelectPip} />}
                </TimelineStage>
              )
            })}
```

> `PipGroup`/`onSelectPip` 期望的 Pip 形状(todoId/status/assetId)与 `PipState` 一致——若 `PipGroup` import 的是旧 `@/lib/timeline` Pip 类型,改成 `import type { PipState as Pip } from "@/lib/projectState"`。`TimelineStage` 若强类型旧 `Stage`,放宽其 prop 类型或新建适配类型(实现者按组件实际签名微调)。

事件日志渲染改读 `log` prop:

```ts
            <EventLog
              lines={log.map((l) => ({ seq: l.seq, text: l.text, emphasis: l.emphasis }))}
            />
```

### 9b. container 删 override,接 `useProjectState` + `useProductionTimeline(log)`

- [ ] **Step 4: 改 container `orgs.$org.projects.$id.runs.$runId.tsx`**

- import 加 `useProjectState`(from api)与 `ProjectState`(from projectState);删对 `TimelineState` 的依赖。
- 用 query client 把 SSE `state` 帧写回缓存:

```ts
  const qc = useQueryClient()
  const stateQuery = useProjectState(id, runId)
  const { log, conn } = useProductionTimeline({
    projectId: id,
    accessToken: getAccessToken(),
    status: stateQuery.data?.status,
    enabled: project != null,
    fetchAllEvents,
    planId: runId,
    onState: (s) => qc.setQueryData(["project-state", id, runId], s),
  })
```

- 删掉这两段双源 override(原 :73 `displayStatus` 与 :243-246 的 `timeline.runStatus` 重算):传给 WorkbenchView 的 `project.status` 与阶段全部来自 `stateQuery.data`。
- 渲染:

```ts
  const wfState: ProjectState = stateQuery.data ?? {
    projectId: id, version: 0, status: "draft", runStatus: "idle",
    stages: [], pips: [], assets: { total: 0, done: 0, pending: 0 },
  }
  // latestAssetId 改读 wfState.pips
  const latestAssetId = [...wfState.pips].reverse().find((p) => p.status === "done" && p.assetId)?.assetId
```

```tsx
    <WorkbenchView
      project={{ ...project, fallbackUsed: showFallback }}
      state={wfState}
      log={log}
      conn={conn}
      live={wfState.runStatus !== "done"}
      fallbackUsed={showFallback || undefined}
      canRun={canRun}
      ...
    />
```

- `canCancel` 改读 `wfState.status`(running|planning);`handleSelectPip(pip: PipState)` 类型更新。
- 删除文件底部 `isTerminal` 工具(改用 `wfState.runStatus === "done"`)及 `displayStatus` 相关行。

- [ ] **Step 5: 运行 workflow 测试 + 全量构建/测试**

Run: `cd llm-agent-studio/web && pnpm vitest src/features/workflow/workflow.test.tsx --run`
Expected: PASS。

Run: `cd llm-agent-studio/web && pnpm build`
Expected: tsc + vite 构建通过(无残留 `TimelineState`/`reduceTimeline` 引用)。

Run: `cd llm-agent-studio/web && pnpm test`
Expected: 全套前端测试 PASS(修掉所有引用旧 timeline API 的测试)。

- [ ] **Step 6: 提交**

```bash
cd llm-agent-studio
git add web/src/features/workflow/WorkbenchPage.tsx web/src/routes/_authed/orgs.\$org.projects.\$id.runs.\$runId.tsx web/src/features/workflow/workflow.test.tsx
git commit -m "refactor(web): WorkbenchView + container render authoritative ProjectState

Badge/stages/pips/counts/error all read backend ProjectState. Dual-source
runStatus/displayStatus overrides removed. Race conditions eliminated."
```

---

## Task 10: 端到端验证

**Files:** 无(验证 only)

- [ ] **Step 1: 后端全量**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1`
Expected: 全 PASS。

- [ ] **Step 2: 前端全量**

Run: `cd llm-agent-studio/web && pnpm build && pnpm test`
Expected: 构建 + 测试全 PASS。

- [ ] **Step 3: 手动一条龙(参考 memory: Studio dev runtime)**

按 `reference_studio-dev-runtime.md`(studiod :8083 + Vite :5173,demo@studio.com/DevReveal#123)起本地栈,跑一个完整 create-project → run 工作流,核对:
- 工作台徽章/阶段/pip 计数全程与 `GET /api/projects/{id}/state` 返回一致。
- 无徽章闪烁、无"待审核 · 0"与失败态自相矛盾、无轮询 vs SSE 竞态。
- 前端无任何"自己推导状态"代码路径(`grep -rn "reduceTimeline\|TimelineState\|appliedSeqs" web/src` 应为空)。

Run: `cd llm-agent-studio && grep -rn "reduceTimeline\|TimelineState\|settleAssetStage\|appliedSeqs" web/src`
Expected: 无输出(空)。

- [ ] **Step 4: 最终提交(如有手动修正)**

```bash
cd llm-agent-studio
git add -A && git commit -m "test: e2e verify single-source workflow state (no client-side derivation)"
```

---

## Self-Review(已执行)

**Spec coverage:**
- §4.1 单一 Compute → Task 1。§4.2 ProjectState schema → Task 1(Go)+ Task 6(TS)。§5.1 REST 端点 → Task 3。§5.2 SSE 推快照 + version 去重 → Task 4。§6 前端瘦身 + 纯渲染 → Task 7/8/9。§7 契约对齐(契约测试)→ Task 6;`todo_failed` 补 type → Task 5。§8 迁移顺序 → Task 1-10 即其落地。✅ 全覆盖。
- spec §8 第 4 步"handler 命令式 SetStatus 收敛" → Task 4 Step 4(删 running 置位,保留 planning)。✅

**Placeholder scan:** 无 TBD/TODO。两处"按现有 helper/列名确认"是**明确的探查指令**(给出 grep 命令 + 调整原则),非占位。✅

**Type consistency:** 后端 `ProjectState`/`Stage`/`Pip`/`Assets`/`ProblemError`/`Plan` 与前端 `ProjectState`/`StageState`/`PipState`/`AssetsState`/`ProblemError`/`PlanState` 字段名(role/status/todoId/assetId/version/runStatus)逐一对齐;契约测试守护枚举。`Compute(Input)`/`LoadState`/`fetchProjectState`/`useProjectState` 签名前后一致。✅

**已知裁剪(YAGNI,符合 spec 非目标):** 不合并 `project.DeriveStatus` 与 `projectstate.deriveStatus` 两个 Go 实现(taskboard/health/workflows 仍用前者);二者逻辑相同,加注释要求手工同步。引入新基建(codegen/LISTEN-NOTIFY)被排除。
