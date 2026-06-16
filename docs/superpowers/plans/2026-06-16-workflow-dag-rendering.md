# Workflow DAG Rendering Implementation Plan (子项目 A)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 web 端「agent 执行」如实渲染自定义工作流的真实 DAG(多 script / 分支 / 多父),每个节点带独立 live 状态;默认管线项目渲染与改动前逐像素一致。

**Architecture:** 后端唯一权威纯函数 `projectstate.Compute` 额外吐 `nodes/edges/isCustom`(保留现有 5-role 折叠与 status 派生);`project.Store.LoadState` 多 SELECT 几列喂进去;前端 `WorkbenchView` 按 `isCustom` 在表现层路由到「现有 5 段轨道」或「新 `GraphView`(分层竖向)」,两者读同一份 `ProjectState`。零 schema 迁移、数据层零双源。

**Tech Stack:** Go(pgx / 纯函数 + 表驱动测试)、React + TypeScript(Vitest + Testing Library)。自定义 DAG 用分层竖向布局,不引入 react-flow。

**Spec:** `docs/superpowers/specs/2026-06-16-workflow-dag-rendering-design.md`(必读,含 Plan-agent 审核修正的 2 个 load-bearing 假设:isCustom 需含 `custom_workflow_enabled`;节点按 `(created_at,id)` 稳定排序)。

**测试约定:** 后端 `cd llm-agent-studio && GOWORK=off go test ./pkg... -run X -count=1`;DB-backed 测试需先导出 `LLM_AGENT_STUDIO_PG_URL` 指向一个干净 PG 库并 `-p 1`(见 memory `reference_studio-authz-test-release`)。前端 `cd web && npx vitest run path -t "name"`。

---

## File Structure

**后端(改 2 文件 + 1 测试):**
- `internal/projectstate/state.go` — 加 `GraphNode`/`GraphEdge` 类型、`Input`/`Todo`/`ProjectState` 新字段、纯函数 `buildGraph`、`graphLabel`、`Compute` 接线。
- `internal/projectstate/state_test.go` — `buildGraph` 表驱动 + `Compute` 的 `isCustom`/nodes/edges 断言。
- `internal/project/store.go` — `LoadState` 多取 `workflow_id`、`custom_workflow_enabled`、`depends_on`、`created_at`。
- `internal/project/store_test.go` — DB-backed:自定义 plan → `LoadState` 返回正确 nodes/edges/isCustom。

**前端(新 2 文件 + 改 4 文件 + 测试):**
- `web/src/lib/projectState.ts` — 加 `GraphNode`/`GraphEdge` 类型 + `ProjectState.nodes/edges/isCustom`。
- `web/src/lib/projectState.contract.test.ts` — 新增 GraphNode.status 枚举断言。
- `web/src/lib/graphLayout.ts`(新)— 纯函数 `layerize` 拓扑分层。
- `web/src/lib/graphLayout.test.ts`(新)— `layerize` 表驱动。
- `web/src/features/workflow/GraphView.tsx`(新)— 分层竖向 DAG 渲染。
- `web/src/features/workflow/GraphView.test.tsx`(新)— 组件测。
- `web/src/features/workflow/WorkbenchPage.tsx` — 中栏按 `isCustom` 路由 + 新 `onSelectNode` prop。
- `web/src/features/workflow/workflow.test.tsx` — 路由分流测 + 补字面量字段。
- `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx` — 容器 `onSelectNode` 接线 + draft 回落补字段。
- `web/src/features/workflow/{useProductionTimeline.test,api.test}.tsx` — 仅补 ProjectState 字面量新字段(编译修复)。

---

## Task 1: 后端 projectstate 类型 + buildGraph 纯函数

**Files:**
- Modify: `internal/projectstate/state.go`
- Test: `internal/projectstate/state_test.go`

- [ ] **Step 1: 写失败测试 —— buildGraph 线性/分支/多父/悬挂边/稳定序**

追加到 `internal/projectstate/state_test.go` 末尾(同包,可直接调未导出函数):

```go
import (
	"testing"
	"time"
)

// 注:文件首已有 `import "testing"`,改成上面的分组 import(加 "time")。

func tAt(sec int) time.Time { return time.Unix(int64(sec), 0).UTC() }

func TestBuildGraph_LinearChain(t *testing.T) {
	todos := []Todo{
		{ID: "a", Type: "script", Status: "done", CreatedAt: tAt(1)},
		{ID: "b", Type: "storyboard", Status: "running", DependsOn: []string{"a"}, CreatedAt: tAt(2)},
	}
	nodes, edges := buildGraph(todos, map[string]Asset{})
	if len(nodes) != 2 || nodes[0].ID != "a" || nodes[1].ID != "b" {
		t.Fatalf("nodes = %+v", nodes)
	}
	if nodes[0].Label != "剧本生成 #1" || nodes[1].Label != "分镜拆解 #1" {
		t.Fatalf("labels = %q,%q", nodes[0].Label, nodes[1].Label)
	}
	if nodes[0].Status != "done" || nodes[1].Status != "running" {
		t.Fatalf("status = %q,%q", nodes[0].Status, nodes[1].Status)
	}
	if len(edges) != 1 || edges[0].From != "b" || edges[0].To != "a" {
		t.Fatalf("edges = %+v", edges)
	}
}

func TestBuildGraph_PerTypeSequence(t *testing.T) {
	todos := []Todo{
		{ID: "s1", Type: "script", Status: "done", CreatedAt: tAt(1)},
		{ID: "s2", Type: "script", Status: "ready", CreatedAt: tAt(2)},
	}
	nodes, _ := buildGraph(todos, map[string]Asset{})
	if nodes[0].Label != "剧本生成 #1" || nodes[1].Label != "剧本生成 #2" {
		t.Fatalf("labels = %q,%q", nodes[0].Label, nodes[1].Label)
	}
}

func TestBuildGraph_StableOrderIgnoresInputOrder(t *testing.T) {
	// 输入乱序(模拟 updated_at 重排)→ 仍按 (CreatedAt, ID) 稳定输出。
	todos := []Todo{
		{ID: "s2", Type: "script", Status: "ready", CreatedAt: tAt(2)},
		{ID: "s1", Type: "script", Status: "done", CreatedAt: tAt(1)},
	}
	nodes, _ := buildGraph(todos, map[string]Asset{})
	if nodes[0].ID != "s1" || nodes[1].ID != "s2" {
		t.Fatalf("order = %s,%s want s1,s2", nodes[0].ID, nodes[1].ID)
	}
	if nodes[0].Label != "剧本生成 #1" || nodes[1].Label != "剧本生成 #2" {
		t.Fatalf("seq not stable: %q,%q", nodes[0].Label, nodes[1].Label)
	}
}

func TestBuildGraph_TieBreakByID(t *testing.T) {
	// created_at 并列(同 tx 批量插)→ ID 字典序 tiebreak。
	todos := []Todo{
		{ID: "b", Type: "asset", Status: "ready", CreatedAt: tAt(5)},
		{ID: "a", Type: "asset", Status: "ready", CreatedAt: tAt(5)},
	}
	nodes, _ := buildGraph(todos, map[string]Asset{})
	if nodes[0].ID != "a" || nodes[1].ID != "b" {
		t.Fatalf("tiebreak order = %s,%s want a,b", nodes[0].ID, nodes[1].ID)
	}
}

func TestBuildGraph_FanInMultiParent(t *testing.T) {
	todos := []Todo{
		{ID: "a", Type: "script", Status: "done", CreatedAt: tAt(1)},
		{ID: "b", Type: "script", Status: "done", CreatedAt: tAt(2)},
		{ID: "c", Type: "storyboard", Status: "ready", DependsOn: []string{"a", "b"}, CreatedAt: tAt(3)},
	}
	_, edges := buildGraph(todos, map[string]Asset{})
	if len(edges) != 2 {
		t.Fatalf("edges = %+v want 2", edges)
	}
}

func TestBuildGraph_DropsDanglingEdge(t *testing.T) {
	todos := []Todo{
		{ID: "a", Type: "asset", Status: "ready", DependsOn: []string{"ghost"}, CreatedAt: tAt(1)},
	}
	_, edges := buildGraph(todos, map[string]Asset{})
	if len(edges) != 0 {
		t.Fatalf("dangling edge not dropped: %+v", edges)
	}
}

func TestBuildGraph_AssetIDPassthrough(t *testing.T) {
	todos := []Todo{{ID: "a", Type: "asset", Status: "done", CreatedAt: tAt(1)}}
	nodes, _ := buildGraph(todos, map[string]Asset{"a": {ID: "as1", TodoID: "a"}})
	if nodes[0].AssetID != "as1" {
		t.Fatalf("assetId = %q want as1", nodes[0].AssetID)
	}
}

func TestBuildGraph_Empty(t *testing.T) {
	nodes, edges := buildGraph(nil, map[string]Asset{})
	if nodes == nil || edges == nil {
		t.Fatalf("must return non-nil slices: nodes=%v edges=%v", nodes, edges)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("want empty, got %d/%d", len(nodes), len(edges))
	}
}

func TestCompute_IsCustom(t *testing.T) {
	// workflow_id 非空 → custom
	got := Compute(Input{ProjectID: "p", ProjectStatus: "draft", WorkflowID: "wf1"})
	if !got.IsCustom {
		t.Fatalf("WorkflowID set → IsCustom must be true")
	}
	// workflow_id 空但 custom_workflow_enabled → custom(legacy 项目级自定义)
	got = Compute(Input{ProjectID: "p", ProjectStatus: "draft", CustomWorkflowEnabled: true})
	if !got.IsCustom {
		t.Fatalf("CustomWorkflowEnabled → IsCustom must be true")
	}
	// 都没有 → 默认
	got = Compute(Input{ProjectID: "p", ProjectStatus: "draft"})
	if got.IsCustom {
		t.Fatalf("neither set → IsCustom must be false")
	}
	if got.Nodes == nil || got.Edges == nil {
		t.Fatalf("Nodes/Edges must be non-nil even with no plan")
	}
}

func TestCompute_PopulatesGraph(t *testing.T) {
	in := Input{
		ProjectID: "p", ProjectStatus: "running", HasPlan: true,
		Plan:  &Plan{PlanID: "pl"},
		Todos: []Todo{{ID: "a", Type: "script", Status: "running", CreatedAt: tAt(1)}},
	}
	got := Compute(in)
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "a" {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
}
```

- [ ] **Step 2: 跑测试,确认编译失败(类型/函数未定义)**

Run: `GOWORK=off go test ./internal/projectstate/ -run TestBuildGraph -count=1`
Expected: 编译失败 —— `undefined: buildGraph`、`Todo.CreatedAt`、`Todo.DependsOn`、`ProjectState.IsCustom`、`Input.WorkflowID` 等。

- [ ] **Step 3: 加类型字段(state.go)**

`internal/projectstate/state.go` 顶部 `package projectstate` 之后加 import:

```go
import (
	"fmt"
	"sort"
	"time"
)
```

在 `Pip` 类型之后加:

```go
// GraphNode 是一个 todo 在执行图中的节点(自定义工作流 GraphView 渲染用)。
type GraphNode struct {
	ID      string `json:"id"`                // todo id
	Label   string `json:"label"`             // type 派生(如「剧本生成 #1」)
	Type    string `json:"type"`              // script|storyboard|asset|...
	Status  string `json:"status"`            // blocked|pending|running|done|failed
	AssetID string `json:"assetId,omitempty"` // asset 节点的产物 id,供右栏预览
}

// GraphEdge 是一条依赖边:From 依赖 To(To 先于 From 执行)。
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
```

`ProjectState` 结构体加三个字段(放在 `Error` 之前/之后均可,这里放末尾):

```go
	Error     *ProblemError `json:"error,omitempty"`
	Nodes     []GraphNode   `json:"nodes"`
	Edges     []GraphEdge   `json:"edges"`
	IsCustom  bool          `json:"isCustom"`
```

`Todo` 结构体加两字段:

```go
type Todo struct {
	ID        string
	Type      string // script|storyboard|asset
	Status    string // ready|running|blocked|done|failed|canceled
	Error     string
	DependsOn []string
	CreatedAt time.Time
}
```

`Input` 结构体加两字段:

```go
type Input struct {
	ProjectID             string
	Version               int64
	ProjectStatus         string
	HasPlan               bool
	Plan                  *Plan
	Todos                 []Todo
	Assets                []Asset
	WorkflowID            string
	CustomWorkflowEnabled bool
}
```

- [ ] **Step 4: 加 buildGraph + graphLabel(state.go)**

在 `blockedStages()` 之后加:

```go
var graphLabelBase = map[string]string{
	"script":     "剧本生成",
	"storyboard": "分镜拆解",
	"asset":      "素材生成",
	"planner":    "规划",
	"review":     "人工审核",
}

func graphLabel(typ string, n int) string {
	base, ok := graphLabelBase[typ]
	if !ok {
		base = typ
	}
	return fmt.Sprintf("%s #%d", base, n)
}

// buildGraph 把一批 todo 投影成执行图的节点 + 边。节点按 (CreatedAt, ID) 稳定排序
// ——不能用 LoadState 主查询的 updated_at 序,因为 worker 在 run 过程中持续改 todo
// 的 updated_at 会让节点位置与 #N 序号在两次快照间漂移。悬挂边(指向不存在 todo)
// 被丢弃。返回非 nil 空切片(对齐 Pips: []Pip{},JSON 出 [] 而非 null)。
func buildGraph(todos []Todo, assetByTodo map[string]Asset) ([]GraphNode, []GraphEdge) {
	nodes := make([]GraphNode, 0, len(todos))
	edges := make([]GraphEdge, 0)

	sorted := make([]Todo, len(todos))
	copy(sorted, todos)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
		}
		return sorted[i].ID < sorted[j].ID
	})

	ids := make(map[string]bool, len(sorted))
	for _, t := range sorted {
		ids[t.ID] = true
	}

	typeSeq := map[string]int{}
	for _, t := range sorted {
		typeSeq[t.Type]++
		n := GraphNode{
			ID:     t.ID,
			Label:  graphLabel(t.Type, typeSeq[t.Type]),
			Type:   t.Type,
			Status: todoStatusToStage(t.Status),
		}
		if a, ok := assetByTodo[t.ID]; ok {
			n.AssetID = a.ID
		}
		nodes = append(nodes, n)
	}

	for _, t := range sorted {
		for _, dep := range t.DependsOn {
			if ids[dep] {
				edges = append(edges, GraphEdge{From: t.ID, To: dep})
			}
		}
	}
	return nodes, edges
}
```

- [ ] **Step 5: Compute 接线(state.go)**

把 `Compute` 开头的初始化改为(加 Nodes/Edges/IsCustom):

```go
	st := ProjectState{
		ProjectID: in.ProjectID,
		Version:   in.Version,
		Plan:      in.Plan,
		Pips:      []Pip{},
		Nodes:     []GraphNode{},
		Edges:     []GraphEdge{},
		IsCustom:  in.WorkflowID != "" || in.CustomWorkflowEnabled,
	}
```

在 plan 分支里 `assetByTodo` 构建完成之后(现 state.go:149 `}` 之后、`scriptStatus,...` 之前)插入:

```go
	st.Nodes, st.Edges = buildGraph(in.Todos, assetByTodo)
```

- [ ] **Step 6: 跑测试,确认通过**

Run: `GOWORK=off go test ./internal/projectstate/ -count=1`
Expected: PASS(新测试 + 原有 state_test 全过)。

- [ ] **Step 7: Commit**

```bash
git add internal/projectstate/state.go internal/projectstate/state_test.go
git commit -m "feat(projectstate): emit nodes/edges/isCustom from Compute

buildGraph 把 todos 投影成执行图节点+边,按 (created_at,id) 稳定排序避免
run 中 #N 序号漂移;保留现有 5-role 折叠。isCustom = workflow_id 非空 ||
custom_workflow_enabled。"
```

---

## Task 2: LoadState 多取 workflow_id / custom_workflow_enabled / depends_on / created_at

**Files:**
- Modify: `internal/project/store.go:404-484`(`LoadState`)
- Test: `internal/project/store_test.go`

- [ ] **Step 1: 写失败测试 —— DB-backed 自定义工作流图**

追加到 `internal/project/store_test.go` 末尾:

```go
// TestLoadState_CustomGraph: 自定义工作流(plan.workflow_id 非空 + 两 todo 带
// depends_on)→ LoadState 返回 isCustom + 正确 nodes/edges。
func TestLoadState_CustomGraph(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_graph_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-Graph", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// first-class workflow row(满足 plans.workflow_id 的外键)。
	wfID := "wf_ls_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO workflows (id, project_id, name, nodes) VALUES ($1,$2,'wf','[]'::jsonb)`,
		wfID, p.ID); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	planID := "pln_lsg_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, workflow_id, created_at)
		 VALUES ($1,$2,'running',true,false,$3, now())`, planID, p.ID, wfID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	scriptID := "todo_s_" + p.ID
	boardID := "todo_b_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on)
		 VALUES ($1,$2,$3,'script','done','{}'),
		        ($4,$2,$3,'storyboard','running',ARRAY[$1])`,
		scriptID, p.ID, planID, boardID); err != nil {
		t.Fatalf("insert todos: %v", err)
	}
	st, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !st.IsCustom {
		t.Fatalf("IsCustom = false, want true (workflow_id set)")
	}
	if len(st.Nodes) != 2 {
		t.Fatalf("nodes = %+v want 2", st.Nodes)
	}
	if len(st.Edges) != 1 || st.Edges[0].From != boardID || st.Edges[0].To != scriptID {
		t.Fatalf("edges = %+v want one board→script", st.Edges)
	}
}

// TestLoadState_LegacyCustomEnabled: custom_workflow_enabled=true 但 plan
// 的 workflow_id 为 NULL(经 runHandler 的项目级自定义路径)→ 仍判 isCustom。
func TestLoadState_LegacyCustomEnabled(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_legacy_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "LS-Legacy", CreatedBy: "u", CustomWorkflowEnabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	planID := "pln_lsl_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1,$2,'running',true,false, now())`, planID, p.ID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	st, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !st.IsCustom {
		t.Fatalf("IsCustom = false, want true (custom_workflow_enabled)")
	}
}
```

> 注:`CreateInput` 已有 `CustomWorkflowEnabled bool`(`store.go:69`)。若 `workflows` 表列名/必填项与此 INSERT 不符,以 `internal/storage/storage.go` 的 `workflows` DDL 为准微调 INSERT(只调 SQL,不改断言)。

- [ ] **Step 2: 跑测试,确认失败**

Run: `GOWORK=off go test ./internal/project/ -run 'TestLoadState_CustomGraph|TestLoadState_LegacyCustomEnabled' -count=1 -p 1`
Expected: FAIL —— `IsCustom = false`(LoadState 尚未填新字段)、nodes/edges 为空。

- [ ] **Step 3: 改 LoadState plan 查询取 workflow_id + custom_workflow_enabled**

`internal/project/store.go`,`in := projectstate.Input{...}` 那行(:409)改为附带 custom 标志:

```go
	in := projectstate.Input{
		ProjectID:             projectID,
		ProjectStatus:         p.Status,
		CustomWorkflowEnabled: p.CustomWorkflowEnabled,
	}
```

plan 解析块(:425-435)改为多取 `workflow_id`:

```go
	var planRowID, workflowID string
	var valid, fallbackUsed bool
	if planID == "" {
		err = s.pool.QueryRow(ctx,
			`SELECT id, valid, fallback_used, COALESCE(workflow_id,'') FROM plans WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
			projectID).Scan(&planRowID, &valid, &fallbackUsed, &workflowID)
	} else {
		err = s.pool.QueryRow(ctx,
			`SELECT id, valid, fallback_used, COALESCE(workflow_id,'') FROM plans WHERE id=$1 AND project_id=$2`,
			planID, projectID).Scan(&planRowID, &valid, &fallbackUsed, &workflowID)
	}
```

`in.HasPlan = true` 那行(:442)之后加:

```go
	in.WorkflowID = workflowID
```

> 注意:`errors.Is(err, pgx.ErrNoRows)` 的 draft 直通分支(:436-438)在 plan 不存在时返回 `Compute(in)` —— 此时 `in.CustomWorkflowEnabled` 已设好,所以「自定义但还没 plan」也会 isCustom=true(符合 spec §6 占位)。无需额外改动。

- [ ] **Step 4: 改 todos 查询取 depends_on + created_at**

todos 查询(:446-447)改为:

```go
	rows, err := s.pool.Query(ctx,
		`SELECT id, type, status, COALESCE(error,''), depends_on, created_at FROM todos WHERE plan_id=$1 ORDER BY updated_at ASC`, planRowID)
```

scan 循环(:452-457)改为:

```go
	for rows.Next() {
		var t projectstate.Todo
		if err := rows.Scan(&t.ID, &t.Type, &t.Status, &t.Error, &t.DependsOn, &t.CreatedAt); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan state todo: %w", err)
		}
		in.Todos = append(in.Todos, t)
	}
```

> `depends_on TEXT[]` → pgx v5 直扫 `*[]string`;`created_at TIMESTAMPTZ` → `*time.Time`。主查询保持 `ORDER BY updated_at ASC`(现有折叠/last-failed 语义依赖它);稳定排序由 buildGraph 内部完成。

- [ ] **Step 5: 跑测试,确认通过**

Run: `GOWORK=off go test ./internal/project/ -run 'TestLoadState' -count=1 -p 1`
Expected: PASS(新两个 + 原有 LoadState 测试)。

- [ ] **Step 6: 全后端回归**

Run: `GOWORK=off go build ./... && GOWORK=off go test ./internal/projectstate/ ./internal/project/ -count=1 -p 1`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/project/store.go internal/project/store_test.go
git commit -m "feat(project): LoadState feeds workflow_id/custom_enabled/depends_on/created_at

供 Compute 产出 isCustom + 执行图节点边。isCustom 含 custom_workflow_enabled
以覆盖 runHandler 的 NULL-workflow_id 项目级自定义运行。"
```

---

## Task 3: 前端 projectState 类型 + 契约测试 + 补齐字面量

**Files:**
- Modify: `web/src/lib/projectState.ts`
- Modify: `web/src/lib/projectState.contract.test.ts`
- Modify: `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx:106-114`
- Modify: `web/src/features/workflow/workflow.test.tsx`、`useProductionTimeline.test.tsx`、`api.test.ts`(补字面量字段)
- Test: `web/src/lib/projectState.contract.test.ts`

- [ ] **Step 1: 写失败测试 —— GraphNode.status 枚举契约**

`web/src/lib/projectState.contract.test.ts` 顶部 import 加 `GraphNodeStatus`(下一步定义),并在 `describe` 内追加:

```ts
  it("GraphNode.status 复用 StageStatus2 的 5 态", () => {
    const s: GraphNodeStatus[] = ["blocked", "pending", "running", "done", "failed"]
    expect(s).toHaveLength(5)
  })
```

把首行 import 改为:

```ts
import type { StageRole, StageStatus2, RunStatus2, PipStatus2, GraphNodeStatus } from "./projectState"
```

- [ ] **Step 2: 跑测试,确认失败**

Run: `cd web && npx vitest run src/lib/projectState.contract.test.ts`
Expected: FAIL —— `GraphNodeStatus` 未导出(类型错误)。

- [ ] **Step 3: 加类型(projectState.ts)**

`web/src/lib/projectState.ts` 在 `PipState` 之后加:

```ts
// GraphNode.status 与 StageStatus2 同域(后端 buildGraph 用 todoStatusToStage)。
export type GraphNodeStatus = StageStatus2

export interface GraphNode {
  id: string
  label: string
  type: string
  status: GraphNodeStatus
  assetId?: string
}

export interface GraphEdge {
  from: string
  to: string
}
```

`ProjectState` 接口加三字段:

```ts
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
  nodes: GraphNode[]
  edges: GraphEdge[]
  isCustom: boolean
}
```

- [ ] **Step 4: 补齐所有 ProjectState 字面量(编译修复)**

新增 3 个必填字段后,以下字面量会 tsc 报错,各补 `nodes: [], edges: [], isCustom: false`:

`web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx:106-114` 的 draft 回落:

```ts
  const wfState: ProjectState = stateQuery.data ?? {
    projectId: id,
    version: 0,
    status: "draft",
    runStatus: "idle",
    stages: [],
    pips: [],
    assets: { total: 0, done: 0, pending: 0 },
    nodes: [],
    edges: [],
    isCustom: false,
  }
```

对 `web/src/features/workflow/workflow.test.tsx`、`web/src/features/workflow/useProductionTimeline.test.tsx`、`web/src/features/workflow/api.test.ts` 里每个构造 `ProjectState`(含 `stages:` 字段)的对象字面量,同样补这三个字段。用 `npx tsc -b` 的报错行定位。

- [ ] **Step 5: 跑契约测试 + 类型检查,确认通过**

Run: `cd web && npx vitest run src/lib/projectState.contract.test.ts && npx tsc -b`
Expected: 契约测试 PASS;tsc 无 ProjectState 缺字段错误。

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/projectState.ts web/src/lib/projectState.contract.test.ts \
  web/src/routes/_authed/orgs.\$org.projects.\$id.runs.\$runId.tsx \
  web/src/features/workflow/workflow.test.tsx \
  web/src/features/workflow/useProductionTimeline.test.tsx \
  web/src/features/workflow/api.test.ts
git commit -m "feat(web): mirror GraphNode/GraphEdge/isCustom in projectState"
```

---

## Task 4: graphLayout.layerize 纯函数

**Files:**
- Create: `web/src/lib/graphLayout.ts`
- Test: `web/src/lib/graphLayout.test.ts`

- [ ] **Step 1: 写失败测试**

`web/src/lib/graphLayout.test.ts`:

```ts
import { describe, it, expect } from "vitest"
import { layerize } from "./graphLayout"
import type { GraphNode, GraphEdge } from "./projectState"

function n(id: string): GraphNode {
  return { id, label: id, type: "script", status: "done" }
}

describe("layerize", () => {
  it("空图返回空数组", () => {
    expect(layerize([], [])).toEqual([])
  })

  it("线性链分成逐层", () => {
    const nodes = [n("a"), n("b"), n("c")]
    const edges: GraphEdge[] = [
      { from: "b", to: "a" },
      { from: "c", to: "b" },
    ]
    const layers = layerize(nodes, edges)
    expect(layers.map((l) => l.map((x) => x.id))).toEqual([["a"], ["b"], ["c"]])
  })

  it("一父多子:子节点同层并排", () => {
    const nodes = [n("a"), n("b"), n("c")]
    const edges: GraphEdge[] = [
      { from: "b", to: "a" },
      { from: "c", to: "a" },
    ]
    const layers = layerize(nodes, edges)
    expect(layers[0].map((x) => x.id)).toEqual(["a"])
    expect(layers[1].map((x) => x.id).sort()).toEqual(["b", "c"])
  })

  it("多父汇聚:汇聚点落在最深父之后", () => {
    const nodes = [n("a"), n("b"), n("c"), n("d")]
    const edges: GraphEdge[] = [
      { from: "b", to: "a" }, // b 在 a 之后
      { from: "d", to: "a" }, // d 依赖 a
      { from: "d", to: "b" }, // d 依赖 b → d 必须在 b 之后
    ]
    const layers = layerize(nodes, edges)
    const layerOf = (id: string) => layers.findIndex((l) => l.some((x) => x.id === id))
    expect(layerOf("d")).toBeGreaterThan(layerOf("b"))
    expect(layerOf("b")).toBeGreaterThan(layerOf("a"))
  })

  it("残留环不死循环(兜底返回有限层)", () => {
    const nodes = [n("a"), n("b")]
    const edges: GraphEdge[] = [
      { from: "a", to: "b" },
      { from: "b", to: "a" },
    ]
    const layers = layerize(nodes, edges)
    // 仅要求:终止 + 两个节点都出现一次。
    const ids = layers.flat().map((x) => x.id).sort()
    expect(ids).toEqual(["a", "b"])
  })
})
```

- [ ] **Step 2: 跑测试,确认失败**

Run: `cd web && npx vitest run src/lib/graphLayout.test.ts`
Expected: FAIL —— 找不到模块 `./graphLayout`。

- [ ] **Step 3: 实现 layerize**

`web/src/lib/graphLayout.ts`:

```ts
import type { GraphNode, GraphEdge } from "./projectState"

// layerize 把 DAG 拓扑分层:无依赖的节点在第 0 层,其余按「最长依赖路径」下沉。
// 同层节点保持输入顺序(后端已按 created_at,id 稳定排序)。
// 残留环防御:迭代次数上限 = 节点数 + 1,环内节点停在已达到的层,不死循环。
export function layerize(nodes: GraphNode[], edges: GraphEdge[]): GraphNode[][] {
  if (nodes.length === 0) return []
  const has = new Set(nodes.map((x) => x.id))
  // prereqs: 节点 → 它依赖的节点(edge.from 依赖 edge.to)。
  const prereqs = new Map<string, string[]>()
  for (const x of nodes) prereqs.set(x.id, [])
  for (const e of edges) {
    if (has.has(e.from) && has.has(e.to)) prereqs.get(e.from)!.push(e.to)
  }
  const layer = new Map<string, number>()
  for (const x of nodes) layer.set(x.id, 0)

  let changed = true
  let guard = nodes.length + 1
  while (changed && guard-- > 0) {
    changed = false
    for (const x of nodes) {
      let want = 0
      for (const d of prereqs.get(x.id)!) {
        want = Math.max(want, (layer.get(d) ?? 0) + 1)
      }
      if (want !== layer.get(x.id)) {
        layer.set(x.id, want)
        changed = true
      }
    }
  }

  const maxLayer = Math.max(0, ...nodes.map((x) => layer.get(x.id) ?? 0))
  const out: GraphNode[][] = Array.from({ length: maxLayer + 1 }, () => [])
  for (const x of nodes) out[layer.get(x.id) ?? 0].push(x)
  return out.filter((l) => l.length > 0)
}
```

- [ ] **Step 4: 跑测试,确认通过**

Run: `cd web && npx vitest run src/lib/graphLayout.test.ts`
Expected: PASS(5 个测试)。

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/graphLayout.ts web/src/lib/graphLayout.test.ts
git commit -m "feat(web): layerize — topological layering for DAG view"
```

---

## Task 5: GraphView 组件(分层竖向)

**Files:**
- Create: `web/src/features/workflow/GraphView.tsx`
- Test: `web/src/features/workflow/GraphView.test.tsx`

- [ ] **Step 1: 写失败测试**

`web/src/features/workflow/GraphView.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { GraphView } from "./GraphView"
import type { GraphNode, GraphEdge } from "@/lib/projectState"

const nodes: GraphNode[] = [
  { id: "a", label: "剧本生成 #1", type: "script", status: "done" },
  { id: "b", label: "分镜拆解 #1", type: "storyboard", status: "running" },
  { id: "c", label: "素材生成 #1", type: "asset", status: "done", assetId: "as1" },
]
const edges: GraphEdge[] = [
  { from: "b", to: "a" },
  { from: "c", to: "b" },
]

describe("GraphView", () => {
  it("空 nodes 显占位", () => {
    render(<GraphView nodes={[]} edges={[]} />)
    expect(screen.getByText(/等待规划/)).toBeInTheDocument()
  })

  it("渲染每个节点的 label 与 data-status", () => {
    render(<GraphView nodes={nodes} edges={edges} />)
    expect(screen.getByText("剧本生成 #1")).toBeInTheDocument()
    expect(screen.getByText("分镜拆解 #1")).toBeInTheDocument()
    const cards = document.querySelectorAll('[data-slot="graph-node"]')
    expect(cards).toHaveLength(3)
    expect(cards[1].getAttribute("data-status")).toBe("running")
  })

  it("点击带 assetId 的节点触发 onSelectNode", () => {
    const onSelect = vi.fn()
    render(<GraphView nodes={nodes} edges={edges} onSelectNode={onSelect} />)
    fireEvent.click(screen.getByText("素材生成 #1"))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "c", assetId: "as1" }))
  })

  it("无 assetId 的节点点击不触发", () => {
    const onSelect = vi.fn()
    render(<GraphView nodes={nodes} edges={edges} onSelectNode={onSelect} />)
    fireEvent.click(screen.getByText("剧本生成 #1"))
    expect(onSelect).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 跑测试,确认失败**

Run: `cd web && npx vitest run src/features/workflow/GraphView.test.tsx`
Expected: FAIL —— 找不到 `./GraphView`。

- [ ] **Step 3: 实现 GraphView**

`web/src/features/workflow/GraphView.tsx`:

```tsx
import { cn } from "@/lib/utils"
import { layerize } from "@/lib/graphLayout"
import type { GraphNode, GraphEdge } from "@/lib/projectState"

// 节点 agent 语义色(CSS 变量,见 src/index.css)。未知 type 用中性线色。
const NODE_COLOR: Record<string, string> = {
  planner: "var(--amber)",
  script: "var(--script)",
  storyboard: "var(--board)",
  asset: "var(--asset)",
  review: "var(--review)",
}

export interface GraphViewProps {
  nodes: GraphNode[]
  edges: GraphEdge[]
  // asset 节点(带 assetId)点击 → 容器把右栏预览切到该工件。
  onSelectNode?: (node: GraphNode) => void
}

// 分层竖向 DAG:每层一行、同层节点并排;层间竖向连接线表达依赖方向。
// 复用 TimelineStage 的节点视觉语言(done 填色 / running 琥珀旋转环 / failed 红)。
export function GraphView({ nodes, edges, onSelectNode }: GraphViewProps) {
  if (nodes.length === 0) {
    return (
      <div
        data-slot="graph-empty"
        className="flex flex-col items-center justify-center gap-1.5 py-16 text-center"
      >
        <p className="text-[13px] text-text-2">等待规划…</p>
        <p className="text-[12px] text-text-3">工作流节点产出后在此渲染</p>
      </div>
    )
  }
  const layers = layerize(nodes, edges)
  return (
    <div data-slot="graph" className="mx-auto max-w-[560px]">
      {layers.map((layer, li) => (
        <div key={li} data-slot="graph-layer" className="relative pb-[30px]">
          {/* 层间连接线(非首层画上行连线)。 */}
          {li > 0 && (
            <span
              aria-hidden
              className="absolute left-1/2 -top-[30px] h-[30px] w-0.5 -translate-x-1/2 bg-line"
            />
          )}
          <div className="flex flex-wrap items-start justify-center gap-3">
            {layer.map((node) => (
              <GraphNodeCard key={node.id} node={node} onSelectNode={onSelectNode} />
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

function GraphNodeCard({
  node,
  onSelectNode,
}: {
  node: GraphNode
  onSelectNode?: (node: GraphNode) => void
}) {
  const color = NODE_COLOR[node.type] ?? "var(--line)"
  const isDone = node.status === "done"
  const isRunning = node.status === "running"
  const isFailed = node.status === "failed"
  const clickable = !!node.assetId && !!onSelectNode

  const inner = (
    <>
      <div
        className={cn(
          "relative grid h-7 w-7 place-items-center rounded-full border-2 bg-bg-base",
          isDone && "border-[var(--cur)] bg-[var(--cur)]",
          isRunning && "border-amber",
          isFailed && "border-danger bg-danger/15",
          !isDone && !isRunning && !isFailed && "border-line",
        )}
        style={{ ["--cur" as string]: color }}
      >
        {isRunning && (
          <span
            aria-hidden
            className="absolute -inset-1.5 rounded-full border-2 border-dashed border-amber motion-safe:animate-[spin_3s_linear_infinite]"
          />
        )}
        <span
          className={cn(
            "text-[10px] font-bold",
            isDone ? "text-[#14161a]" : isFailed ? "text-danger" : "text-text-3",
          )}
        >
          {isDone ? "✓" : ""}
        </span>
      </div>
      <span className="mt-1 max-w-[110px] truncate text-center text-[11.5px] text-text-2">
        {node.label}
      </span>
    </>
  )

  if (clickable) {
    return (
      <button
        type="button"
        data-slot="graph-node"
        data-status={node.status}
        onClick={() => onSelectNode!(node)}
        className="flex flex-col items-center rounded-md p-1 transition-colors hover:bg-bg-raised"
      >
        {inner}
      </button>
    )
  }
  return (
    <div
      data-slot="graph-node"
      data-status={node.status}
      className="flex flex-col items-center p-1"
    >
      {inner}
    </div>
  )
}
```

- [ ] **Step 4: 跑测试,确认通过**

Run: `cd web && npx vitest run src/features/workflow/GraphView.test.tsx`
Expected: PASS(4 个测试)。

- [ ] **Step 5: Commit**

```bash
git add web/src/features/workflow/GraphView.tsx web/src/features/workflow/GraphView.test.tsx
git commit -m "feat(web): GraphView — layered vertical DAG renderer for custom workflows"
```

---

## Task 6: WorkbenchView 按 isCustom 路由 + 容器接线

**Files:**
- Modify: `web/src/features/workflow/WorkbenchPage.tsx`
- Modify: `web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`
- Test: `web/src/features/workflow/workflow.test.tsx`

- [ ] **Step 1: 写失败测试 —— 路由分流**

`web/src/features/workflow/workflow.test.tsx` 追加(复用文件内现有 `WorkbenchView` 渲染 helper / fixture;若无 helper,用一个最小 ProjectState 字面量构造,记得带 nodes/edges/isCustom)。新增:

```tsx
import { GraphView } from "./GraphView" // 若已 import 可略

it("isCustom=true 渲染 GraphView 而非 5 段轨道", () => {
  renderWorkbench({
    state: makeState({
      isCustom: true,
      nodes: [{ id: "a", label: "剧本生成 #1", type: "script", status: "done" }],
      edges: [],
    }),
  })
  expect(document.querySelector('[data-slot="graph"]')).not.toBeNull()
  expect(document.querySelector('[data-slot="stage"]')).toBeNull()
})

it("isCustom=false 渲染 5 段轨道而非 GraphView", () => {
  renderWorkbench({ state: makeState({ isCustom: false }) })
  expect(document.querySelector('[data-slot="stage"]')).not.toBeNull()
  expect(document.querySelector('[data-slot="graph"]')).toBeNull()
})
```

> 实施提示:`renderWorkbench`/`makeState` 用文件里已有的渲染/构造工具;若现有 fixture 没有 `isCustom` 字段,先在 helper 默认值里补 `isCustom: false, nodes: [], edges: []`。`makeState(over)` 浅合并 over。

- [ ] **Step 2: 跑测试,确认失败**

Run: `cd web && npx vitest run src/features/workflow/workflow.test.tsx -t "isCustom"`
Expected: FAIL —— `[data-slot="graph"]` 为 null(WorkbenchView 还没接 GraphView)。

- [ ] **Step 3: WorkbenchView 加 onSelectNode prop + 中栏路由**

`web/src/features/workflow/WorkbenchPage.tsx`:

顶部 import 加:

```tsx
import { GraphView } from "./GraphView"
import type { ProjectState, PipState, StageRole, GraphNode } from "@/lib/projectState"
```

(把现有 `import type { ProjectState, PipState, StageRole } from "@/lib/projectState"` 替换为上面这行。)

`WorkbenchViewProps` 接口加:

```tsx
  // 自定义 DAG 节点(asset 节点带 assetId)点击 → 右栏预览。
  onSelectNode?: (node: GraphNode) => void
```

函数签名解构里加 `onSelectNode`:

```tsx
export function WorkbenchView({
  project,
  state,
  log,
  conn,
  live,
  fallbackUsed,
  canRun,
  onRun,
  onCancel,
  isRunning,
  preview,
  onSelectStage,
  onSelectPip,
  onSelectNode,
  drawer,
  onOpenReview,
  onBack,
  plannerModelNode,
}: WorkbenchViewProps) {
```

中栏(现 :203-239 的 `<div className="order-first ...">` 内部)按 isCustom 路由。把内层 `<div className="relative mx-auto max-w-[560px] pl-2">...stages.map...</div>` 包成条件:

```tsx
        {/* 中：制片轨道(默认管线)/ DAG 图(自定义工作流)。 */}
        <div className="order-first p-[18px] lg:order-none lg:overflow-y-auto">
          {state.isCustom ? (
            <GraphView
              nodes={state.nodes}
              edges={state.edges}
              onSelectNode={onSelectNode}
            />
          ) : (
            <div className="relative mx-auto max-w-[560px] pl-2">
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
                    onSelect={
                      onSelectStage && INSPECTABLE_STAGES[id]
                        ? () => onSelectStage(id)
                        : undefined
                    }
                    sub={
                      id === "S4"
                        ? `素材生成 · ${doneAssetCount}/${pipCount || "?"}`
                        : STAGE_SUB[id]
                    }
                  >
                    {id === "S4" && pips.length > 0 && (
                      <PipGroup pips={pips} onSelectPip={onSelectPip} />
                    )}
                  </TimelineStage>
                )
              })}
            </div>
          )}
        </div>
```

- [ ] **Step 4: 跑分流测试,确认通过**

Run: `cd web && npx vitest run src/features/workflow/workflow.test.tsx -t "isCustom"`
Expected: PASS。

- [ ] **Step 5: 容器接 onSelectNode(runs.$runId.tsx)**

`web/src/routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`:

顶部类型 import 加 `GraphNode`:

```tsx
import type { ProjectState, PipState, GraphNode } from "@/lib/projectState"
```

在 `handleSelectPip`(:170-172)之后加(用 `node.assetId` 直接驱动,**不复用 PipState 形参**):

```tsx
  function handleSelectNode(node: GraphNode) {
    if (node.assetId) setSelection({ kind: "asset", assetId: node.assetId })
  }
```

在 `<WorkbenchView>` 的 props 里(`onSelectPip={handleSelectPip}` 之后)加:

```tsx
      onSelectNode={handleSelectNode}
```

- [ ] **Step 6: 前端全量回归**

Run: `cd web && npx tsc -b && npx vitest run`
Expected: tsc 无错;vitest 全绿(含新增测试)。

- [ ] **Step 7: Commit**

```bash
git add web/src/features/workflow/WorkbenchPage.tsx \
  web/src/routes/_authed/orgs.\$org.projects.\$id.runs.\$runId.tsx \
  web/src/features/workflow/workflow.test.tsx
git commit -m "feat(web): route Workbench middle column to GraphView when isCustom"
```

---

## Task 7: 端到端回归 + 手测

**Files:** 无(验证)

- [ ] **Step 1: 后端全量**

Run: `cd llm-agent-studio && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1 -p 1`
Expected: 全绿(DB-backed 测试需 `LLM_AGENT_STUDIO_PG_URL` 指向干净库)。

- [ ] **Step 2: 前端全量**

Run: `cd web && npm test`
Expected: 全绿。

- [ ] **Step 3: 手测(成功标准)**

按 memory `reference_studio-dev-runtime` 起 studiod :8083 + Vite :5173,登录后:
1. 跑一个**自定义工作流**(多 script / 分支依赖)→ 中栏出现 `GraphView`,分层渲染、各节点 live 状态正确、asset 节点可点开右栏预览。
2. 打开一个**默认管线项目**的 run 页 → 中栏仍是原 5 段轨道,与改动前视觉一致。

- [ ] **Step 4: 用 finishing-a-development-branch 收尾**

Run sub-skill: `superpowers:finishing-a-development-branch`(验证测试 → 选 merge/PR)。

---

## Self-Review

**Spec coverage:**
- §4.1 LoadState(workflow_id/custom_enabled/depends_on/created_at)→ Task 2 ✓
- §4.2 Compute 新字段 + buildGraph + isCustom 公式 + 非 nil 切片 → Task 1 ✓
- §5.1 projectState.ts 镜像 + 契约测试新断言 → Task 3 ✓
- §5.2 GraphView 分层竖向 + 5 态视觉 + 空态占位 → Task 5;layerize → Task 4 ✓
- §5.3 WorkbenchView 路由 + onSelectNode 用 node.assetId(不复用 PipState)→ Task 6 ✓
- §6 错误/空态(节点 failed 红、isCustom 真但空 → 占位、悬挂边丢弃、残留环兜底)→ Task 1(丢悬挂边)+ Task 4(环兜底)+ Task 5(占位)✓
- §7 测试矩阵(线性/分支/多父/悬挂/空/fan-out 稳定序/NULL-workflow_id-custom/路由分流/契约)→ Task 1-6 ✓
- §8 成功标准(自定义画 DAG / 默认逐像素一致)→ Task 7 手测 ✓
- §9 边界(不动 5-role 折叠、无迁移、不引 react-flow)→ 全程遵守 ✓

**Placeholder scan:** 无 TBD/TODO;每个改代码步骤均含完整代码块与确切命令。Task 3 Step 4 与 Task 6 Step 1 的「用 tsc 报错定位字面量 / 复用文件内 helper」是机械的编译器驱动修复,非模糊占位(已给出要补的确切字段值)。

**Type consistency:** 全程一致 —— `buildGraph(todos, assetByTodo)`、`GraphNode{id,label,type,status,assetId}`、`GraphEdge{from,to}`、`layerize(nodes,edges): GraphNode[][]`、`GraphNodeStatus = StageStatus2`、`onSelectNode(node: GraphNode)`、`isCustom`/`nodes`/`edges` 前后端字段名对齐。
