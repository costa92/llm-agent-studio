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
