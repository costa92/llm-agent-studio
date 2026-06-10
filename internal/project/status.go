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
}

// DeriveStatus computes the project status from its todo tally (spec §7.3 step
// 5). Active work (ready/running/blocked) dominates; otherwise a terminal
// failure/cancel surfaces; all-done is completed; no todos means still planning.
//
// NOTE: the 'review' status from spec §6 is intentionally deferred to M2 — it
// only appears once an asset enters pending_acceptance (HITL acceptance), which
// M1 does not build. M1's status set is {planning,running,failed,canceled,
// completed}; do NOT add asset/HITL logic here.
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
	if c.Done == c.Total {
		return "completed"
	}
	return "running"
}
