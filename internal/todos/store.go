// Package todos persists the planner's todo graph as the worker job queue:
// dependency edges (depends_on[]) + lease columns (locked_by/locked_until/
// next_run_at/attempts). A todo IS a job (spec §6 merged design).
package todos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NodeSpec is one todo to create. LocalID is the planner-local node id; the
// store maps it to a generated todo id and rewrites DependsOn accordingly.
type NodeSpec struct {
	LocalID   string
	Type      string
	DependsOn []string // planner-local ids
	InputJSON []byte
}

// Store persists todos.
type Store struct {
	pool *pgxpool.Pool
}

// New builds a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateGraph inserts all nodes in one tx. A node with no dependencies starts
// 'ready'; a node with dependencies starts 'blocked'. Returns localID→todoID.
func (s *Store) CreateGraph(ctx context.Context, projectID, planID string, specs []NodeSpec) (map[string]string, error) {
	idMap := make(map[string]string, len(specs))
	for _, sp := range specs {
		idMap[sp.LocalID] = newID()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, sp := range specs {
		deps := make([]string, 0, len(sp.DependsOn))
		for _, d := range sp.DependsOn {
			id, ok := idMap[d]
			if !ok {
				return nil, fmt.Errorf("todos: node %q depends on unknown local id %q", sp.LocalID, d)
			}
			deps = append(deps, id)
		}
		status := "ready"
		if len(deps) > 0 {
			status = "blocked"
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			idMap[sp.LocalID], projectID, planID, sp.Type, status, deps, sp.InputJSON); err != nil {
			return nil, fmt.Errorf("todos: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return idMap, nil
}

// MarkDone marks a todo done with its output ref, then promotes any dependent
// 'blocked' todo whose dependencies are ALL done to 'ready'. The done write is
// guarded with status='running' so a user-canceled todo isn't resurrected by an
// in-flight worker; it returns false (no error) when no running row matched
// (already canceled). One tx so the unblock is atomic with the done write
// (worker §7.3 step 3).
func (s *Store) MarkDone(ctx context.Context, todoID, outputRef string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var projectID string
	tag, err := tx.Exec(ctx,
		`UPDATE todos SET status='done', output_ref=$2, locked_by='', locked_until=NULL, updated_at=now()
		 WHERE id=$1 AND status='running'`, todoID, outputRef)
	if err != nil {
		return false, fmt.Errorf("todos: mark done: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Todo no longer 'running' (e.g. canceled): commit cleanly, no unblock.
		return false, tx.Commit(ctx)
	}
	if err := tx.QueryRow(ctx, `SELECT project_id FROM todos WHERE id=$1`, todoID).Scan(&projectID); err != nil {
		return false, fmt.Errorf("todos: load project: %w", err)
	}
	// Promote blocked dependents whose deps are now all done.
	if _, err := tx.Exec(ctx,
		`UPDATE todos t SET status='ready', updated_at=now()
		 WHERE t.project_id=$1 AND t.status='blocked' AND $2 = ANY(t.depends_on)
		   AND NOT EXISTS (
		     SELECT 1 FROM todos d
		     WHERE d.id = ANY(t.depends_on) AND d.status <> 'done'
		   )`, projectID, todoID); err != nil {
		return false, fmt.Errorf("todos: unblock dependents: %w", err)
	}
	return true, tx.Commit(ctx)
}

// MarkFailed marks a todo terminally 'failed' AND transitively cancels every
// todo that (recursively) depends on it. Without the cancel, those dependents
// stay 'blocked' forever — DeriveStatus would see Blocked>0 and wedge the
// project in 'running' (spec §7.3 step 4: 耗尽 → failed + 阻断后继). The cancel
// turns them into a terminal 'canceled' state so DeriveStatus resolves to
// 'failed'. Caller invokes this ONLY after retry attempts are exhausted; a
// retryable failure still reschedules (see worker.fail) and never lands here.
// One tx so the fail + cancel are atomic.
func (s *Store) MarkFailed(ctx context.Context, todoID, errMsg string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE todos SET status='failed', error=$2, locked_by='', locked_until=NULL, updated_at=now()
		 WHERE id=$1`, todoID, errMsg); err != nil {
		return fmt.Errorf("todos: mark failed: %w", err)
	}
	if err := cancelDependents(ctx, tx, todoID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DynamicSpec is one dynamically-added todo (fan-out): a type + its input. The
// dependency on the producing parent is passed once to AddDynamic.
type DynamicSpec struct {
	Type      string
	InputJSON []byte
}

// AddDynamic inserts fan-out todos within an EXISTING tx (spec M2 fan-out): when
// a storyboard todo completes, the worker creates one 'asset' todo per shot in
// the SAME tx that writes the shots rows, so shots + their asset todos become
// visible atomically. Each new todo starts 'ready' (its sole dependency — the
// storyboard parent — is about to be marked done by the same worker) with
// depends_on=[parentTodoID] recorded for lineage/audit. Returns the new todo ids
// so the caller can emit todo_ready after commit. allDone/DeriveStatus need no
// change: they tally all todos for the project, so the run won't finish until
// these asset todos terminate.
//
// Schema-default reliance (C2): this INSERT intentionally omits next_run_at and
// relies on the column's DEFAULT now(), so the todo is immediately claimable by
// the worker's `next_run_at <= now()` claim. plan_id may be the producing run's
// plan id; todos.plan_id is NOT NULL but has NO FK. A future schema change that
// drops the next_run_at default (or adds a plan_id FK) would break fan-out
// claimability — keep the default.
func (s *Store) AddDynamic(ctx context.Context, tx pgx.Tx, projectID, planID, parentTodoID string, specs []DynamicSpec) ([]string, error) {
	ids := make([]string, 0, len(specs))
	for _, sp := range specs {
		id := newID()
		if _, err := tx.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,$3,$4,'ready',$5,$6)`,
			id, projectID, planID, sp.Type, []string{parentTodoID}, sp.InputJSON); err != nil {
			return nil, fmt.Errorf("todos: add dynamic: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// cancelDependents transitively cancels every non-terminal todo reachable via
// depends_on edges from the failed todo, using a recursive CTE over
// depends_on @> ARRAY[id]. Already-terminal todos (done/failed/canceled) are
// left untouched.
func cancelDependents(ctx context.Context, tx pgx.Tx, failedID string) error {
	if _, err := tx.Exec(ctx, `
		WITH RECURSIVE blocked AS (
		  SELECT id FROM todos WHERE depends_on @> ARRAY[$1]::text[]
		  UNION
		  SELECT t.id FROM todos t
		  JOIN blocked b ON t.depends_on @> ARRAY[b.id]::text[]
		)
		UPDATE todos SET status='canceled', locked_by='', locked_until=NULL, updated_at=now()
		WHERE id IN (SELECT id FROM blocked)
		  AND status NOT IN ('done','failed','canceled')`, failedID); err != nil {
		return fmt.Errorf("todos: cancel dependents: %w", err)
	}
	return nil
}
