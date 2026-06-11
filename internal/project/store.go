// Package project owns project CRUD + the project status machine derived from
// the project's todos (spec §5). It mirrors orgkb's resource pattern but the
// org membership bootstrap lives in httpapi (POST /api/orgs) like kb.
package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a project row does not exist.
var ErrNotFound = errors.New("project: not found")

// Project is a projects row.
type Project struct {
	ID             string `json:"id"`
	OrgID          string `json:"orgId"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ContentType    string `json:"contentType"`
	TargetPlatform string `json:"targetPlatform"`
	Style          string `json:"style"`
	Status         string `json:"status"`
	CreatedBy      string `json:"createdBy"`
}

// CreateInput is the input to Create. Brief maps to the description column
// (the creative brief the planner/ScriptAgent consume).
type CreateInput struct {
	OrgID          string
	Name           string
	Brief          string
	ContentType    string
	TargetPlatform string
	Style          string
	CreatedBy      string
}

// Store persists projects.
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

// Create inserts a project (status='draft').
func (s *Store) Create(ctx context.Context, in CreateInput) (Project, error) {
	if in.OrgID == "" || in.Name == "" || in.CreatedBy == "" {
		return Project{}, fmt.Errorf("project: OrgID, Name, CreatedBy required")
	}
	p := Project{
		ID: newID(), OrgID: in.OrgID, Name: in.Name, Description: in.Brief,
		ContentType: in.ContentType, TargetPlatform: in.TargetPlatform,
		Style: in.Style, Status: "draft", CreatedBy: in.CreatedBy,
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, description, content_type, target_platform, style, status, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.ID, p.OrgID, p.Name, p.Description, p.ContentType, p.TargetPlatform, p.Style, p.Status, p.CreatedBy); err != nil {
		return Project{}, fmt.Errorf("project: insert: %w", err)
	}
	return p, nil
}

// Get returns a project by id.
func (s *Store) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, name, description, content_type, target_platform, style, status, created_by
		 FROM projects WHERE id=$1`, id).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.ContentType, &p.TargetPlatform, &p.Style, &p.Status, &p.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

// OrgIDForProject resolves the org for a project (used by the RBAC middleware,
// which only has the project id from the path). Mirrors orgkb.OrgIDForKB.
func (s *Store) OrgIDForProject(ctx context.Context, projectID string) (string, error) {
	var orgID string
	err := s.pool.QueryRow(ctx, `SELECT org_id FROM projects WHERE id=$1`, projectID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return orgID, err
}

// ListByOrg returns up to limit projects for an org, keyset-paginated by id.
func (s *Store) ListByOrg(ctx context.Context, orgID string, limit int, cursor string) ([]Project, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, name, description, content_type, target_platform, style, status, created_by
		 FROM projects WHERE org_id=$1 AND id>$2 ORDER BY id ASC LIMIT $3`,
		orgID, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.ContentType, &p.TargetPlatform, &p.Style, &p.Status, &p.CreatedBy); err != nil {
			return nil, "", err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// SetStatus writes the project status directly (used on run kickoff: planning).
func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

// RefreshStatus recomputes the project status from its todo tally and persists
// it. Called by the worker after each todo transition (spec §7.3 step 5).
func (s *Store) RefreshStatus(ctx context.Context, projectID string) (string, error) {
	var c TodoCounts
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status='ready'),
		       count(*) FILTER (WHERE status='running'),
		       count(*) FILTER (WHERE status='blocked'),
		       count(*) FILTER (WHERE status='done'),
		       count(*) FILTER (WHERE status='failed'),
		       count(*) FILTER (WHERE status='canceled')
		FROM todos WHERE project_id=$1`, projectID).
		Scan(&c.Total, &c.Ready, &c.Running, &c.Blocked, &c.Done, &c.Failed, &c.Canceled)
	if err != nil {
		return "", fmt.Errorf("project: tally todos: %w", err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM assets WHERE project_id=$1 AND status='pending_acceptance'`,
		projectID).Scan(&c.PendingAssets); err != nil {
		return "", fmt.Errorf("project: tally pending assets: %w", err)
	}
	status := DeriveStatus(c)
	if err := s.SetStatus(ctx, projectID, status); err != nil {
		return "", err
	}
	return status, nil
}

// Cancel marks all non-terminal todos canceled, sweeps in-flight ('generating')
// assets to a terminal 'canceled' (M3 取消语义 — in-flight generation results
// then no-op on arrival because assets.SetBlob is guarded on
// status='generating'), and sets the project canceled. pending_acceptance
// assets are deliberately KEPT reviewable: the generation already cost real
// money and HITL accept/reject still applies; DeriveStatus's Canceled branch
// outranks the review branch so the project status stays canceled.
func (s *Store) Cancel(ctx context.Context, projectID string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE todos SET status='canceled', locked_by='', locked_until=NULL, updated_at=now()
		 WHERE project_id=$1 AND status IN ('pending','ready','blocked','running')`, projectID); err != nil {
		return fmt.Errorf("project: cancel todos: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE assets SET status='canceled' WHERE project_id=$1 AND status='generating'`, projectID); err != nil {
		return fmt.Errorf("project: cancel assets: %w", err)
	}
	return s.SetStatus(ctx, projectID, "canceled")
}
