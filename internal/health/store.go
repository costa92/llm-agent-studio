// Package health owns the platform monitoring + data-integrity surface: a set of
// read-only invariant checks over the studio tables (stuck todos, stranded
// assets, status divergence, orphans) plus repair routines that drive the
// offending rows back to a consistent state. It composes *pgxpool.Pool with
// *project.Store so the status_divergence repair reuses the authoritative
// project.RefreshStatus (no import cycle: project does not import health).
package health

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/project"
)

// stuckAssetCutoff is how long an asset may sit in an in-flight status
// (generating / submitted) before the stuck_assets check flags it. Aligns with
// the M4 orphan reaper window.
const stuckAssetCutoff = time.Hour

// Store runs the health checks + repairs. It reuses project.Store.RefreshStatus
// for the status_divergence repair.
type Store struct {
	pool     *pgxpool.Pool
	projects *project.Store
}

// New builds a Store.
func New(pool *pgxpool.Pool, projects *project.Store) *Store {
	return &Store{pool: pool, projects: projects}
}

// Check is one data-integrity invariant result.
type Check struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Severity   string   `json:"severity"`
	Count      int      `json:"count"`
	Samples    []string `json:"samples"`
	Repairable bool     `json:"repairable"`
}

// System is the live system-health snapshot.
type System struct {
	DBLatencyMs   int64     `json:"dbLatencyMs"`
	DBOK          bool      `json:"dbOk"`
	StuckTodos    int       `json:"stuckTodos"`
	LastEventAt   time.Time `json:"lastEventAt"`
	WorkerHealthy bool      `json:"workerHealthy"`
}

// Report is the full health report: system snapshot + all checks.
type Report struct {
	System System  `json:"system"`
	Checks []Check `json:"checks"`
}

// RepairResult is the outcome of a repair dispatch.
type RepairResult struct {
	CheckID  string `json:"checkId"`
	Repaired int    `json:"repaired"`
}

// Failure is a recently-failed todo, joined to its project for context.
type Failure struct {
	TodoID      string    `json:"todoId"`
	ProjectID   string    `json:"projectId"`
	ProjectName string    `json:"projectName"`
	OrgID       string    `json:"orgId"`
	Type        string    `json:"type"`
	Agent       string    `json:"agent"`
	Error       string    `json:"error"`
	At          time.Time `json:"at"`
}

// Report runs every check and assembles the system snapshot.
func (s *Store) Report(ctx context.Context) (Report, error) {
	var rep Report

	// System: DB latency + reachability.
	start := time.Now()
	if _, err := s.pool.Exec(ctx, "SELECT 1"); err != nil {
		rep.System.DBOK = false
		rep.System.DBLatencyMs = time.Since(start).Milliseconds()
		return rep, fmt.Errorf("health: ping: %w", err)
	}
	rep.System.DBOK = true
	rep.System.DBLatencyMs = time.Since(start).Milliseconds()

	// Last run event (NULL/no rows → zero time).
	var lastEvent *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(ts) FROM run_events`).Scan(&lastEvent); err != nil {
		return rep, fmt.Errorf("health: last event: %w", err)
	}
	if lastEvent != nil {
		rep.System.LastEventAt = *lastEvent
	}

	c1, err := s.checkStuckTodos(ctx)
	if err != nil {
		return rep, err
	}
	c2, err := s.checkStuckAssets(ctx)
	if err != nil {
		return rep, err
	}
	c3, err := s.checkFailedTodoLiveAssets(ctx)
	if err != nil {
		return rep, err
	}
	c4, err := s.checkStatusDivergence(ctx)
	if err != nil {
		return rep, err
	}
	c5, err := s.checkOrphanAssets(ctx)
	if err != nil {
		return rep, err
	}
	rep.Checks = []Check{c1, c2, c3, c4, c5}

	// WorkerHealthy is derived from stuck todos: a healthy worker should not let
	// running todos outlive their lock.
	rep.System.StuckTodos = c1.Count
	rep.System.WorkerHealthy = c1.Count == 0
	return rep, nil
}

// aggCheck runs a read-only aggregate that yields (count, sample ids) and wraps
// it in a Check with the supplied metadata.
func (s *Store) aggCheck(ctx context.Context, id, title, severity string, repairable bool, query string, args ...any) (Check, error) {
	c := Check{ID: id, Title: title, Severity: severity, Repairable: repairable, Samples: []string{}}
	var samples []string
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&c.Count, &samples); err != nil {
		return Check{}, fmt.Errorf("health: check %s: %w", id, err)
	}
	if samples != nil {
		c.Samples = samples
	}
	return c, nil
}

func (s *Store) checkStuckTodos(ctx context.Context) (Check, error) {
	return s.aggCheck(ctx, "stuck_todos", "运行中超时未完成的任务", "error", true,
		`SELECT count(*), COALESCE((array_agg(id))[1:5], '{}')
		 FROM todos
		 WHERE status='running' AND (locked_until IS NULL OR locked_until < now())`)
}

func (s *Store) checkStuckAssets(ctx context.Context) (Check, error) {
	cutoff := time.Now().Add(-stuckAssetCutoff)
	return s.aggCheck(ctx, "stuck_assets", "长时间停留在生成中的资产", "warn", true,
		`SELECT count(*), COALESCE((array_agg(id))[1:5], '{}')
		 FROM assets
		 WHERE (status='generating' AND created_at < $1)
		    OR (status='submitted' AND submitted_at IS NOT NULL AND submitted_at < $1)`, cutoff)
}

func (s *Store) checkFailedTodoLiveAssets(ctx context.Context) (Check, error) {
	return s.aggCheck(ctx, "failed_todo_live_assets", "任务已失败但资产仍在进行中", "error", true,
		`SELECT count(*), COALESCE((array_agg(a.id))[1:5], '{}')
		 FROM assets a JOIN todos t ON a.todo_id = t.id
		 WHERE a.status IN ('generating','submitted','pending_acceptance') AND t.status='failed'`)
}

func (s *Store) checkOrphanAssets(ctx context.Context) (Check, error) {
	return s.aggCheck(ctx, "orphan_assets", "引用了不存在任务的孤儿资产", "warn", false,
		`SELECT count(*), COALESCE((array_agg(a.id))[1:5], '{}')
		 FROM assets a
		 WHERE a.todo_id <> '' AND NOT EXISTS (SELECT 1 FROM todos t WHERE t.id = a.todo_id)`)
}

// divergentProjectQuery is the shared logic between the status_divergence check
// and its repair: per project, derive the status from the latest plan's todo
// tally (+ pending-acceptance assets) and compare it to the stored status. It
// returns the ids of projects whose stored status disagrees with the derived
// one. Mirrors taskboard.Board()'s latest-plan LATERAL join + project.DeriveStatus.
const divergentProjectQuery = `
	SELECT p.id, p.status,
	       COALESCE(t.total, 0),
	       COALESCE(t.ready, 0),
	       COALESCE(t.running, 0),
	       COALESCE(t.blocked, 0),
	       COALESCE(t.done, 0),
	       COALESCE(t.failed, 0),
	       COALESCE(t.canceled, 0),
	       COALESCE(a.pending, 0),
	       lp.id IS NOT NULL AS has_plan
	FROM projects p
	LEFT JOIN LATERAL (
	  SELECT id FROM plans WHERE project_id = p.id ORDER BY created_at DESC LIMIT 1
	) lp ON true
	LEFT JOIN LATERAL (
	  SELECT count(*) AS total,
	         count(*) FILTER (WHERE status='ready')    AS ready,
	         count(*) FILTER (WHERE status='running')  AS running,
	         count(*) FILTER (WHERE status='blocked')  AS blocked,
	         count(*) FILTER (WHERE status='done')     AS done,
	         count(*) FILTER (WHERE status='failed')   AS failed,
	         count(*) FILTER (WHERE status='canceled') AS canceled
	  FROM todos WHERE plan_id = lp.id
	) t ON true
	LEFT JOIN LATERAL (
	  SELECT count(*) AS pending
	  FROM assets a JOIN todos t ON a.todo_id = t.id
	  WHERE t.plan_id = lp.id AND a.status = 'pending_acceptance'
	) a ON true`

// divergentProjectIDs returns the project ids whose stored status differs from
// the status derived off their latest plan. Projects with no plan are skipped
// (RefreshStatus leaves them at their create-time 'draft' — see its docstring).
func (s *Store) divergentProjectIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, divergentProjectQuery)
	if err != nil {
		return nil, fmt.Errorf("health: status_divergence query: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, stored string
		var c project.TodoCounts
		var hasPlan bool
		if err := rows.Scan(&id, &stored,
			&c.Total, &c.Ready, &c.Running, &c.Blocked, &c.Done, &c.Failed, &c.Canceled,
			&c.PendingAssets, &hasPlan); err != nil {
			return nil, fmt.Errorf("health: status_divergence scan: %w", err)
		}
		if !hasPlan {
			continue
		}
		if project.DeriveStatus(c) != stored {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("health: status_divergence rows: %w", err)
	}
	return ids, nil
}

func (s *Store) checkStatusDivergence(ctx context.Context) (Check, error) {
	ids, err := s.divergentProjectIDs(ctx)
	if err != nil {
		return Check{}, err
	}
	samples := ids
	if len(samples) > 5 {
		samples = samples[:5]
	}
	if samples == nil {
		samples = []string{}
	}
	return Check{
		ID: "status_divergence", Title: "项目状态与最新计划不一致",
		Severity: "warn", Repairable: true,
		Count: len(ids), Samples: samples,
	}, nil
}

// Repair drives the rows flagged by checkID back to a consistent state. Unknown
// or non-repairable checks return an error.
func (s *Store) Repair(ctx context.Context, checkID string) (RepairResult, error) {
	switch checkID {
	case "stuck_todos":
		tag, err := s.pool.Exec(ctx,
			`UPDATE todos SET status='ready', locked_by='', locked_until=NULL, next_run_at=now(), updated_at=now()
			 WHERE status='running' AND (locked_until IS NULL OR locked_until < now())`)
		if err != nil {
			return RepairResult{}, fmt.Errorf("health: repair stuck_todos: %w", err)
		}
		return RepairResult{CheckID: checkID, Repaired: int(tag.RowsAffected())}, nil

	case "stuck_assets":
		// Terminal = 'failed' (aligns with the orphan reaper + assets.SetAsyncFailed;
		// intentionally NOT 'canceled').
		cutoff := time.Now().Add(-stuckAssetCutoff)
		tag, err := s.pool.Exec(ctx,
			`UPDATE assets SET status='failed'
			 WHERE (status='generating' AND created_at < $1)
			    OR (status='submitted' AND submitted_at IS NOT NULL AND submitted_at < $1)`, cutoff)
		if err != nil {
			return RepairResult{}, fmt.Errorf("health: repair stuck_assets: %w", err)
		}
		return RepairResult{CheckID: checkID, Repaired: int(tag.RowsAffected())}, nil

	case "failed_todo_live_assets":
		tag, err := s.pool.Exec(ctx,
			`UPDATE assets a SET status='failed'
			 FROM todos t
			 WHERE a.todo_id = t.id
			   AND a.status IN ('generating','submitted','pending_acceptance')
			   AND t.status='failed'`)
		if err != nil {
			return RepairResult{}, fmt.Errorf("health: repair failed_todo_live_assets: %w", err)
		}
		return RepairResult{CheckID: checkID, Repaired: int(tag.RowsAffected())}, nil

	case "status_divergence":
		ids, err := s.divergentProjectIDs(ctx)
		if err != nil {
			return RepairResult{}, err
		}
		// Deliberately NOT wrapped in an outer tx: RefreshStatus does its own
		// SetStatus and is per-project idempotent, so a partial run still leaves
		// every processed project consistent.
		for _, id := range ids {
			if _, err := s.projects.RefreshStatus(ctx, id); err != nil {
				return RepairResult{}, fmt.Errorf("health: repair status_divergence %s: %w", id, err)
			}
		}
		return RepairResult{CheckID: checkID, Repaired: len(ids)}, nil

	case "orphan_assets":
		return RepairResult{}, fmt.Errorf("health: orphan_assets is report-only (manual review required)")

	default:
		return RepairResult{}, fmt.Errorf("health: unknown check %q", checkID)
	}
}

// Ping verifies DB reachability with a trivial query.
func (s *Store) Ping(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, "SELECT 1"); err != nil {
		return fmt.Errorf("health: ping: %w", err)
	}
	return nil
}

// RecentFailures lists the most recently failed todos, joined to their project.
func (s *Store) RecentFailures(ctx context.Context, limit int) ([]Failure, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx,
		`SELECT t.id, t.project_id, p.name, p.org_id, t.type, t.agent, t.error, t.updated_at
		 FROM todos t JOIN projects p ON t.project_id = p.id
		 WHERE t.status='failed'
		 ORDER BY t.updated_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("health: recent failures: %w", err)
	}
	defer rows.Close()
	out := make([]Failure, 0)
	for rows.Next() {
		var f Failure
		if err := rows.Scan(&f.TodoID, &f.ProjectID, &f.ProjectName, &f.OrgID, &f.Type, &f.Agent, &f.Error, &f.At); err != nil {
			return nil, fmt.Errorf("health: recent failures scan: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("health: recent failures rows: %w", err)
	}
	return out, nil
}
