// Package events owns the run_events ledger: the SSE timeline source of truth
// (spec §9). Events are appended by the worker and replayed/streamed by httpapi.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event is one run_events row.
type Event struct {
	Seq     int64           `json:"seq"`
	Kind    string          `json:"kind"`
	TodoID  string          `json:"todoId,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Store persists run events.
type Store struct {
	pool *pgxpool.Pool
}

// New builds a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append writes one event and returns its assigned seq. payload may be nil.
func (s *Store) Append(ctx context.Context, projectID, kind, todoID string, payload any) (int64, error) {
	var raw []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("events: marshal payload: %w", err)
		}
		raw = b
	}
	var seq int64
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO run_events (project_id, kind, todo_id, payload) VALUES ($1,$2,$3,$4) RETURNING seq`,
		projectID, kind, todoID, raw).Scan(&seq); err != nil {
		return 0, fmt.Errorf("events: append: %w", err)
	}
	return seq, nil
}

// AppendRunDone appends run_done at most once per run (M1 carry: two workers
// can both observe allDone and double-emit). The run boundary is the latest
// planner_started seq — a re-run (重跑=重规划) emits planner_started again,
// opening a fresh dedup window. Returns ok=false when this run already has a
// run_done. The NOT-EXISTS check alone is NOT atomic under READ COMMITTED
// (two workers can both pass it before either insert commits — 评审修复 I2),
// so the insert runs in a transaction that first takes a per-project advisory
// xact lock: the second worker blocks until the first commits, then its
// NOT EXISTS sees the fresh run_done and skips.
func (s *Store) AppendRunDone(ctx context.Context, projectID string) (int64, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("events: append run_done: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, projectID); err != nil {
		return 0, false, fmt.Errorf("events: append run_done: lock: %w", err)
	}
	var seq int64
	err = tx.QueryRow(ctx, `
		INSERT INTO run_events (project_id, kind, todo_id, payload)
		SELECT $1, 'run_done', '', NULL
		WHERE NOT EXISTS (
			SELECT 1 FROM run_events
			WHERE project_id=$1 AND kind='run_done'
			  AND seq > COALESCE((SELECT max(seq) FROM run_events WHERE project_id=$1 AND kind='planner_started'), 0)
		)
		RETURNING seq`, projectID).Scan(&seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil // deferred rollback releases the lock
	}
	if err != nil {
		return 0, false, fmt.Errorf("events: append run_done: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("events: append run_done: commit: %w", err)
	}
	return seq, true, nil
}

// List returns events for a project with seq > afterSeq, oldest first, up to
// limit. afterSeq=0 returns from the beginning (paging + SSE catch-up).
// If planID is non-empty, only returns events belonging to that plan's run window.
func (s *Store) List(ctx context.Context, projectID string, planID string, afterSeq int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if planID == "" {
		rows, err := s.pool.Query(ctx,
			`SELECT seq, kind, todo_id, payload FROM run_events
			 WHERE project_id=$1 AND seq>$2 ORDER BY seq ASC LIMIT $3`,
			projectID, afterSeq, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Event
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.Seq, &e.Kind, &e.TodoID, &e.Payload); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, rows.Err()
	}

	q := `
		WITH bounds AS (
		  SELECT created_at FROM plans WHERE id = $2
		),
		start_event AS (
		  SELECT seq FROM run_events
		  WHERE project_id = $1 AND kind = 'planner_started'
		    AND ts <= (SELECT created_at FROM bounds)
		  ORDER BY ts DESC LIMIT 1
		),
		end_event AS (
		  SELECT seq FROM run_events
		  WHERE project_id = $1 AND kind = 'planner_started'
		    AND seq > (SELECT seq FROM start_event)
		  ORDER BY seq ASC LIMIT 1
		)
		SELECT seq, kind, todo_id, payload FROM run_events
		WHERE project_id = $1
		  AND seq >= COALESCE((SELECT seq FROM start_event), 0)
		  AND seq < COALESCE((SELECT seq FROM end_event), 9223372036854775807)
		  AND seq > $3
		ORDER BY seq ASC LIMIT $4`

	rows, err := s.pool.Query(ctx, q, projectID, planID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.Kind, &e.TodoID, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
