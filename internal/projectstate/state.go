// Package projectstate is the SINGLE authoritative computation of a project
// workflow's render state. Both the REST /state endpoint and the SSE pusher
// call Compute so the two channels can never diverge. Compute is a pure
// function (no I/O) — the DB load lives in project.Store.LoadState.
//
// Boundary: Compute produces SEMANTIC roles/statuses only — never UI concerns
// (S1-S5 numbering, colors, i18n labels). The frontend maps role→layout.
package projectstate

import (
	"fmt"
	"sort"
	"time"
)

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

// GraphNode 是一个 todo 在执行图中的节点(自定义工作流 GraphView 渲染用)。
type GraphNode struct {
	ID      string `json:"id"`                // todo id
	Label   string `json:"label"`             // type 派生(如「剧本生成 #1」)
	Type    string `json:"type"`              // script|storyboard|asset|...
	Status  string `json:"status"`            // blocked|pending(ready)|running|done|failed
	AssetID string `json:"assetId,omitempty"` // asset 节点的产物 id,供右栏预览
	// Output 是 custom 节点 (node_outputs) 的文本/JSON 产物,供运行视图选中面板渲染 (T3)。
	Output string `json:"output,omitempty"`
	// OutputFormat ∈ "text"|"json" (Output 非空时有意义)。
	OutputFormat string `json:"outputFormat,omitempty"`
}

// GraphEdge 是一条依赖边:From 依赖 To(To 先于 From 执行)。
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Assets is the authoritative asset tally (frontend no longer counts events).
type Assets struct {
	Total int `json:"total"`
	// Done counts asset todos that have at least one generated asset record
	// (not todo Status==done). An asset todo is "done" when assetByTodo[todo.ID]
	// exists, regardless of the todo's own status field.
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
	Nodes     []GraphNode   `json:"nodes"`
	Edges     []GraphEdge   `json:"edges"`
	IsCustom  bool          `json:"isCustom"`
}

// Todo is the minimal todo projection Compute needs.
type Todo struct {
	ID        string
	Type      string // script|storyboard|asset
	Status    string // ready|running|blocked|done|failed|canceled
	Error     string
	DependsOn []string
	CreatedAt time.Time
}

// Asset is the minimal asset projection Compute needs (asset→its todo).
type Asset struct {
	ID     string
	TodoID string
	Status string // e.g. pending_acceptance
}

// NodeOutput is one custom node's produced text/JSON output (joined by todo id).
type NodeOutput struct {
	TodoID  string
	Content string
	Format  string
}

// Input is everything Compute needs, loaded by project.Store.LoadState.
type Input struct {
	ProjectID             string
	Version               int64
	ProjectStatus         string // persisted project.status (used when no plan exists)
	HasPlan               bool
	Plan                  *Plan
	Todos                 []Todo
	Assets                []Asset
	WorkflowID            string
	CustomWorkflowEnabled bool
	Outputs               []NodeOutput
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
		Nodes:     []GraphNode{},
		Edges:     []GraphEdge{},
		IsCustom:  in.WorkflowID != "" || in.CustomWorkflowEnabled,
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
		// An in-flight asset with no todo_id is a HITL regenerate descendant
		// (the worker fills it via input_json.assetId, never todo_id). LoadState
		// only feeds in regenerate descendants rooted at the resolved plan's
		// assets (the latest plan when planID=="", else that historical plan), so
		// any todo-less in-flight asset here gates the run in 'review'.
		if a.TodoID == "" && (a.Status == "generating" || a.Status == "submitted" || a.Status == "pending_acceptance") {
			c.inFlightRegen++
		}
	}
	st.Status = deriveStatus(c)
	st.RunStatus = runStatusFor(st.Status)

	// Pips + asset tally from asset todos joined to their latest asset.
	assetByTodo := map[string]Asset{}
	for _, a := range in.Assets {
		assetByTodo[a.TodoID] = a // last write wins = latest (caller orders asc)
	}
	outputByTodo := map[string]NodeOutput{}
	for _, o := range in.Outputs {
		outputByTodo[o.TodoID] = o
	}
	st.Nodes, st.Edges = buildGraph(in.Todos, assetByTodo, outputByTodo)
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

// counts mirrors project.TodoCounts. Unrecognized todo statuses only increment
// total (so a new status added to project.TodoCounts must be added here too,
// otherwise deriveStatus will produce incorrect results for that status).
type counts struct {
	total, ready, running, blocked, done, failed, canceled, pendingAssets, inFlightRegen int
}

// deriveStatus mirrors project.DeriveStatus (kept in sync manually; the two Go
// implementations are intentionally NOT merged — taskboard/health/workflows
// still use project.DeriveStatus. Keep this logic identical to that function).
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
	if c.pendingAssets > 0 || c.inFlightRegen > 0 {
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
		// mixed done+failed (no running) → pending: a partially-failed batch stays
		// actionable (per-asset retry), it does not collapse the stage to failed.
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
func buildGraph(todos []Todo, assetByTodo map[string]Asset, outputByTodo map[string]NodeOutput) ([]GraphNode, []GraphEdge) {
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
		if o, ok := outputByTodo[t.ID]; ok {
			n.Output = o.Content
			n.OutputFormat = o.Format
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
