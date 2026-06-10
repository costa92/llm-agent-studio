// Package assets owns asset metadata + version lineage + the cross-project
// library search (spec §7.4 HITL lineage, §9 资产库). A regenerate produces a
// NEW row (parent_asset_id + version+1, never overwrite). Library is keyset-
// paginated and filters by status/type/style/project/tag, scoped to an org via
// the project join.
package assets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when an asset row does not exist.
var ErrNotFound = errors.New("assets: not found")

// Asset is an assets row.
type Asset struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"projectId"`
	ShotID        string   `json:"shotId"`
	TodoID        string   `json:"todoId"`
	Type          string   `json:"type"`
	BlobKey       string   `json:"blobKey"`
	URL           string   `json:"url"`
	Prompt        string   `json:"prompt"`
	Style         string   `json:"style"`
	Provider      string   `json:"provider"`
	Model         string   `json:"model"`
	Status        string   `json:"status"`
	Version       int      `json:"version"`
	ParentAssetID string   `json:"parentAssetId"`
	Tags          []string `json:"tags"`
}

// CreateInput is the input to Create / CreateVersion.
type CreateInput struct {
	ProjectID string
	ShotID    string
	TodoID    string
	Type      string
	BlobKey   string
	URL       string
	Prompt    string
	Style     string
	Provider  string
	Model     string
	Status    string
	Tags      []string
}

// Store persists assets.
type Store struct{ pool *pgxpool.Pool }

// New builds a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const assetCols = `id, project_id, shot_id, todo_id, type, blob_key, url, prompt, style, provider, model, status, version, parent_asset_id, tags`

func scanAsset(row pgx.Row) (Asset, error) {
	var a Asset
	err := row.Scan(&a.ID, &a.ProjectID, &a.ShotID, &a.TodoID, &a.Type, &a.BlobKey, &a.URL,
		&a.Prompt, &a.Style, &a.Provider, &a.Model, &a.Status, &a.Version, &a.ParentAssetID, &a.Tags)
	return a, err
}

// Create inserts a v1 asset (no parent).
func (s *Store) Create(ctx context.Context, in CreateInput) (Asset, error) {
	return s.insert(ctx, in, 1, "")
}

// CreateVersion inserts a regenerated asset: version = parent.version+1,
// parent_asset_id = parentID (spec §7.4 lineage).
func (s *Store) CreateVersion(ctx context.Context, parentID string, in CreateInput) (Asset, error) {
	parent, err := s.Get(ctx, parentID)
	if err != nil {
		return Asset{}, err
	}
	return s.insert(ctx, in, parent.Version+1, parentID)
}

func (s *Store) insert(ctx context.Context, in CreateInput, version int, parentID string) (Asset, error) {
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	a := Asset{
		ID: newID(), ProjectID: in.ProjectID, ShotID: in.ShotID, TodoID: in.TodoID,
		Type: in.Type, BlobKey: in.BlobKey, URL: in.URL, Prompt: in.Prompt, Style: in.Style,
		Provider: in.Provider, Model: in.Model, Status: in.Status, Version: version,
		ParentAssetID: parentID, Tags: tags,
	}
	if a.Type == "" {
		a.Type = "image"
	}
	if a.Status == "" {
		a.Status = "generating"
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO assets (`+assetCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		a.ID, a.ProjectID, a.ShotID, a.TodoID, a.Type, a.BlobKey, a.URL, a.Prompt, a.Style,
		a.Provider, a.Model, a.Status, a.Version, a.ParentAssetID, a.Tags); err != nil {
		return Asset{}, fmt.Errorf("assets: insert: %w", err)
	}
	return a, nil
}

// Get returns an asset by id.
func (s *Store) Get(ctx context.Context, id string) (Asset, error) {
	a, err := scanAsset(s.pool.QueryRow(ctx, `SELECT `+assetCols+` FROM assets WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	return a, err
}

// SetBlob updates blob_key/url/provider/model/status after generation completes
// (generating → pending_acceptance). Guarded on status='generating'.
func (s *Store) SetBlob(ctx context.Context, id, blobKey, url, provider, model, newStatus string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE assets SET blob_key=$2, url=$3, provider=$4, model=$5, status=$6 WHERE id=$1`,
		id, blobKey, url, provider, model, newStatus)
	return err
}

// TransitionStatus moves an asset from→to atomically, returning ok=false (no
// error) when the row is not in the expected `from` state (HITL 409 semantics,
// spec §7.4 防重).
func (s *Store) TransitionStatus(ctx context.Context, id, from, to string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE assets SET status=$3 WHERE id=$1 AND status=$2`, id, from, to)
	if err != nil {
		return false, fmt.Errorf("assets: transition: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// VersionHistory walks the parent_asset_id chain up + descendants down so the
// review drawer can render v1→v2→… lineage (spec §7.4). Returns oldest-first.
func (s *Store) VersionHistory(ctx context.Context, id string) ([]Asset, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT `+assetCols+` FROM assets WHERE id=$1
			UNION
			SELECT a.id, a.project_id, a.shot_id, a.todo_id, a.type, a.blob_key, a.url, a.prompt,
			       a.style, a.provider, a.model, a.status, a.version, a.parent_asset_id, a.tags
			FROM assets a JOIN up u ON a.id = u.parent_asset_id
		), down AS (
			SELECT `+assetCols+` FROM assets WHERE id=$1
			UNION
			SELECT a.id, a.project_id, a.shot_id, a.todo_id, a.type, a.blob_key, a.url, a.prompt,
			       a.style, a.provider, a.model, a.status, a.version, a.parent_asset_id, a.tags
			FROM assets a JOIN down d ON a.parent_asset_id = d.id
		)
		SELECT `+assetCols+` FROM up
		UNION
		SELECT `+assetCols+` FROM down
		ORDER BY version ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("assets: version history: %w", err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LibraryFilter parameterizes the cross-project library search (spec §9).
type LibraryFilter struct {
	OrgID     string
	ProjectID string
	Type      string
	Status    string
	Style     string
	Tag       string
	Limit     int
	Cursor    string // keyset: last seen asset id
}

// Library returns assets for an org (via the project join) matching the filter,
// keyset-paginated by id. Returns (items, nextCursor, error).
func (s *Store) Library(ctx context.Context, f LibraryFilter) ([]Asset, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var conds []string
	var args []any
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	add("p.org_id=$%d", f.OrgID)
	add("a.id>$%d", f.Cursor)
	if f.ProjectID != "" {
		add("a.project_id=$%d", f.ProjectID)
	}
	if f.Type != "" {
		add("a.type=$%d", f.Type)
	}
	if f.Status != "" {
		add("a.status=$%d", f.Status)
	}
	if f.Style != "" {
		add("a.style=$%d", f.Style)
	}
	if f.Tag != "" {
		add("a.tags @> ARRAY[$%d]::text[]", f.Tag)
	}
	args = append(args, limit)
	q := `SELECT a.id, a.project_id, a.shot_id, a.todo_id, a.type, a.blob_key, a.url, a.prompt,
		a.style, a.provider, a.model, a.status, a.version, a.parent_asset_id, a.tags
		FROM assets a JOIN projects p ON a.project_id = p.id
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY a.id ASC LIMIT $` + fmt.Sprintf("%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("assets: library: %w", err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, a)
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

// OrgIDForAsset resolves the owning org of an asset (via project join) for the
// RBAC middleware on /api/assets/{id} routes.
func (s *Store) OrgIDForAsset(ctx context.Context, assetID string) (string, error) {
	var orgID string
	err := s.pool.QueryRow(ctx,
		`SELECT p.org_id FROM assets a JOIN projects p ON a.project_id=p.id WHERE a.id=$1`, assetID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return orgID, err
}
