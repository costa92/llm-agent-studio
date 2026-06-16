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
