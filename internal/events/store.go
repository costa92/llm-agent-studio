// Package events owns the run_events ledger: the SSE timeline source of truth
// (spec §9). Events are appended by the worker and replayed/streamed by httpapi.
package events

import (
	"context"
	"encoding/json"
	"fmt"

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

// List returns events for a project with seq > afterSeq, oldest first, up to
// limit. afterSeq=0 returns from the beginning (paging + SSE catch-up).
func (s *Store) List(ctx context.Context, projectID string, afterSeq int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
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
