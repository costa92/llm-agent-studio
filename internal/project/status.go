package project

// TodoCounts is the per-status tally of a project's todos.
type TodoCounts struct {
	Total    int
	Ready    int
	Running  int
	Blocked  int
	Done     int
	Failed   int
	Canceled int
	// PendingAssets is the number of assets in pending_acceptance (M2 HITL). It
	// is NOT a todo status — it's joined in by RefreshStatus so DeriveStatus can
	// surface 'review' once todos finish but acceptance is outstanding.
	PendingAssets int
}

// DeriveStatus computes the project status from its todo tally (spec §7.3 step
// 5). Active work (ready/running/blocked) dominates; otherwise a terminal
// failure/cancel surfaces; pending-acceptance assets rest the run in review;
// all-done is completed; no todos means still planning.
func DeriveStatus(c TodoCounts) string {
	if c.Total == 0 {
		return "planning"
	}
	if c.Running > 0 || c.Ready > 0 || c.Blocked > 0 {
		return "running"
	}
	if c.Failed > 0 {
		return "failed"
	}
	if c.Canceled > 0 {
		return "canceled"
	}
	// Todos all done: if assets await HITL acceptance, the run rests in 'review'
	// (spec §6 status set; §7.3 step 5: review→completed once all accepted).
	if c.PendingAssets > 0 {
		return "review"
	}
	if c.Done == c.Total {
		return "completed"
	}
	return "running"
}
