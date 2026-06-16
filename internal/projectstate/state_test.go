package projectstate

import (
	"testing"
	"time"
)

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
	if stageByRole(t, got, "planner").Status != "done" {
		t.Fatalf("planner = %q, want done (todos exist)", stageByRole(t, got, "planner").Status)
	}
	if s := stageByRole(t, got, "script"); s.Status != "running" || s.TodoID != "t-s" {
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
	if stageByRole(t, got, "asset").Status != "done" {
		t.Fatalf("asset stage = %q, want done", stageByRole(t, got, "asset").Status)
	}
	if stageByRole(t, got, "review").Status != "pending" {
		t.Fatalf("review stage = %q, want pending", stageByRole(t, got, "review").Status)
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

func stageByRole(t *testing.T, s ProjectState, role string) Stage {
	t.Helper()
	for _, st := range s.Stages {
		if st.Role == role {
			return st
		}
	}
	t.Fatalf("stageByRole: role %q not found in stages %+v", role, s.Stages)
	return Stage{} // unreachable
}

func TestCompute_AssetStage_MixedDoneFailedIsPending(t *testing.T) {
	in := Input{
		ProjectID: "p1", ProjectStatus: "running", HasPlan: true,
		Plan: &Plan{PlanID: "pl1"},
		Todos: []Todo{
			{ID: "a1", Type: "asset", Status: "done"},
			{ID: "a2", Type: "asset", Status: "failed"},
		},
		Assets: []Asset{
			{ID: "as1", TodoID: "a1", Status: "pending_acceptance"},
			// a2 has no asset record — it failed before producing one
		},
	}
	got := Compute(in)
	if s := stageByRole(t, got, "asset"); s.Status != "pending" {
		t.Fatalf("asset stage = %q, want pending (mixed done+failed, no running)", s.Status)
	}
}

func TestCompute_PlanButNoTodos_Planning(t *testing.T) {
	in := Input{
		ProjectID: "p1", ProjectStatus: "planning", HasPlan: true,
		Plan: &Plan{PlanID: "pl1"},
	}
	got := Compute(in)
	if got.Status != "planning" {
		t.Fatalf("status = %q, want planning", got.Status)
	}
	if got.RunStatus != "running" {
		t.Fatalf("runStatus = %q, want running", got.RunStatus)
	}
	if s := stageByRole(t, got, "planner"); s.Status != "running" {
		t.Fatalf("planner stage = %q, want running (plan exists but no todos yet)", s.Status)
	}
}

func TestCompute_AssetDone_CountsAssetRecordNotTodoStatus(t *testing.T) {
	in := Input{
		ProjectID: "p1", ProjectStatus: "running", HasPlan: true,
		Plan: &Plan{PlanID: "pl1"},
		Todos: []Todo{
			{ID: "a1", Type: "asset", Status: "running"},
		},
		Assets: []Asset{
			{ID: "as1", TodoID: "a1", Status: "pending_acceptance"},
		},
	}
	got := Compute(in)
	if got.Assets.Done != 1 {
		t.Fatalf("Assets.Done = %d, want 1 (asset record exists even though todo status is running)", got.Assets.Done)
	}
	if len(got.Pips) != 1 || got.Pips[0].Status != "running" {
		t.Fatalf("pip = %+v, want status=running", got.Pips)
	}
}

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
	got := Compute(Input{ProjectID: "p", ProjectStatus: "draft", WorkflowID: "wf1"})
	if !got.IsCustom {
		t.Fatalf("WorkflowID set → IsCustom must be true")
	}
	got = Compute(Input{ProjectID: "p", ProjectStatus: "draft", CustomWorkflowEnabled: true})
	if !got.IsCustom {
		t.Fatalf("CustomWorkflowEnabled → IsCustom must be true")
	}
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
