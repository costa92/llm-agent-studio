// Package workflows owns the first-class 1:N project→workflow model: a project
// can have many named workflows, each an independent execution unit (its own DAG
// of nodes, its own runs via plans.workflow_id, its own produced assets). It
// mirrors the internal/project store conventions (pgxpool, hex newID, ErrNotFound).
package workflows

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/project"
)

// ErrNotFound is returned when a workflow row does not exist (or belongs to a
// different project — Get/Update/Delete are scoped by (id AND project_id)).
var ErrNotFound = errors.New("workflows: not found")

// Workflow is a workflows row. Nodes is the DAG definition (a JSON array of
// planner.WorkflowNode {id,type,promptId,dependsOn}); kept as RawMessage so the
// store stays decoupled from the planner type. LatestRunStatus / LatestPlanID
// are derived (from the workflow's most-recent plans row) and only populated by
// ListByProject — they are zero on Create/Get/Update.
type Workflow struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"projectId"`
	Name            string          `json:"name"`
	Nodes           json.RawMessage `json:"nodes"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	LatestRunStatus string          `json:"latestRunStatus,omitempty"`
	LatestPlanID    string          `json:"latestPlanId,omitempty"`
}

// Store persists workflows.
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

// normalizeNodes defaults a nil/empty definition to an empty JSON array so the
// NOT NULL nodes column always holds valid JSON.
func normalizeNodes(nodes json.RawMessage) json.RawMessage {
	if len(nodes) == 0 {
		return json.RawMessage("[]")
	}
	return nodes
}

// Create inserts a workflow under a project.
func (s *Store) Create(ctx context.Context, projectID, name string, nodes json.RawMessage) (Workflow, error) {
	if projectID == "" || name == "" {
		return Workflow{}, fmt.Errorf("workflows: projectID and name required")
	}
	w := Workflow{ID: newID(), ProjectID: projectID, Name: name, Nodes: normalizeNodes(nodes)}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO workflows (id, project_id, name, nodes)
		 VALUES ($1,$2,$3,$4)
		 RETURNING created_at, updated_at`,
		w.ID, w.ProjectID, w.Name, w.Nodes).Scan(&w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Workflow{}, fmt.Errorf("workflows: insert: %w", err)
	}
	return w, nil
}

// Get returns a workflow scoped by (id AND project_id) — a cross-project id is
// ErrNotFound, not a leak.
func (s *Store) Get(ctx context.Context, projectID, id string) (Workflow, error) {
	var w Workflow
	err := s.pool.QueryRow(ctx,
		`SELECT id, project_id, name, nodes, created_at, updated_at
		 FROM workflows WHERE id=$1 AND project_id=$2`, id, projectID).
		Scan(&w.ID, &w.ProjectID, &w.Name, &w.Nodes, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workflow{}, ErrNotFound
	}
	if err != nil {
		return Workflow{}, fmt.Errorf("workflows: get: %w", err)
	}
	return w, nil
}

// ListByProject returns a project's workflows, newest first, each annotated with
// the status of its most-recent run (plans row with this workflow_id, tallied
// like project.ListPlans). Workflows that have never run get an empty status.
func (s *Store) ListByProject(ctx context.Context, projectID string) ([]Workflow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.project_id, w.name, w.nodes, w.created_at, w.updated_at,
		       COALESCE(lp.plan_id, ''),
		       COALESCE(t.total, 0),
		       COALESCE(t.ready, 0),
		       COALESCE(t.running, 0),
		       COALESCE(t.blocked, 0),
		       COALESCE(t.done, 0),
		       COALESCE(t.failed, 0),
		       COALESCE(t.canceled, 0),
		       COALESCE(a.pending_assets, 0)
		FROM workflows w
		LEFT JOIN (
		    SELECT DISTINCT ON (workflow_id) workflow_id, id AS plan_id
		    FROM plans
		    WHERE workflow_id IS NOT NULL
		    ORDER BY workflow_id, created_at DESC
		) lp ON lp.workflow_id = w.id
		LEFT JOIN (
		    SELECT plan_id,
		           count(*) as total,
		           count(*) FILTER (WHERE status='ready') as ready,
		           count(*) FILTER (WHERE status='running') as running,
		           count(*) FILTER (WHERE status='blocked') as blocked,
		           count(*) FILTER (WHERE status='done') as done,
		           count(*) FILTER (WHERE status='failed') as failed,
		           count(*) FILTER (WHERE status='canceled') as canceled
		    FROM todos GROUP BY plan_id
		) t ON t.plan_id = lp.plan_id
		LEFT JOIN (
		    SELECT t.plan_id, count(*) as pending_assets
		    FROM assets a JOIN todos t ON a.todo_id = t.id
		    WHERE a.status = 'pending_acceptance'
		    GROUP BY t.plan_id
		) a ON a.plan_id = lp.plan_id
		WHERE w.project_id = $1
		ORDER BY w.created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("workflows: list: %w", err)
	}
	defer rows.Close()

	var out []Workflow
	for rows.Next() {
		var w Workflow
		var c project.TodoCounts
		if err := rows.Scan(&w.ID, &w.ProjectID, &w.Name, &w.Nodes, &w.CreatedAt, &w.UpdatedAt,
			&w.LatestPlanID,
			&c.Total, &c.Ready, &c.Running, &c.Blocked, &c.Done, &c.Failed, &c.Canceled, &c.PendingAssets); err != nil {
			return nil, fmt.Errorf("workflows: list: scan: %w", err)
		}
		// No run yet → leave status empty (the UI shows "未运行"); a run with todos
		// derives the same status as project.ListPlans.
		if w.LatestPlanID != "" {
			w.LatestRunStatus = project.DeriveStatus(c)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflows: list: rows: %w", err)
	}
	return out, nil
}

// Update changes a workflow's name + nodes, scoped by (id AND project_id).
// 0 rows affected → ErrNotFound.
func (s *Store) Update(ctx context.Context, projectID, id, name string, nodes json.RawMessage) (Workflow, error) {
	if name == "" {
		return Workflow{}, fmt.Errorf("workflows: name required")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE workflows SET name=$3, nodes=$4, updated_at=now()
		 WHERE id=$1 AND project_id=$2`, id, projectID, name, normalizeNodes(nodes))
	if err != nil {
		return Workflow{}, fmt.Errorf("workflows: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Workflow{}, ErrNotFound
	}
	return s.Get(ctx, projectID, id)
}

// Delete removes a workflow scoped by (id AND project_id). Its past runs (plans)
// are kept but orphaned (plans.workflow_id ON DELETE SET NULL). 0 rows → ErrNotFound.
func (s *Store) Delete(ctx context.Context, projectID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM workflows WHERE id=$1 AND project_id=$2`, id, projectID)
	if err != nil {
		return fmt.Errorf("workflows: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
