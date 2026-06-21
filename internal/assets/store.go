// Package assets owns asset metadata + version lineage + the cross-project
// library search (spec §7.4 HITL lineage, §9 资产库). A regenerate produces a
// NEW row (parent_asset_id + version+1, never overwrite). Library is keyset-
// paginated and filters by status/type/style/project/tag, scoped to an org via
// the project join.
package assets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
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

	PrescreenScore int      `json:"prescreenScore"` // -1 = not screened (M3 ReviewAgent)
	PrescreenFlags []string `json:"prescreenFlags"`
	PrescreenNote  string   `json:"prescreenNote"`

	ExternalJobID string `json:"externalJobId"` // M4 async: provider job handle

	// StorageConfigID records WHICH storage backend the blob bytes were written
	// to: a storage_configs.id, the "builtin" sentinel (built-in default localfs),
	// or "" for legacy rows written before this column existed. The serve path
	// resolves the blob store by THIS token so a later storage_mode switch can't
	// re-point a historical asset at the wrong (now-empty) backend.
	StorageConfigID string `json:"storageConfigId"`
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
type Store struct{ db *gorm.DB }

// New builds a Store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const assetCols = `id, project_id, shot_id, todo_id, type, blob_key, url, prompt, style, provider, model, status, version, parent_asset_id, tags, prescreen_score, prescreen_flags, prescreen_note, external_job_id, storage_config_id`

func scanAsset(row interface{ Scan(...any) error }) (Asset, error) {
	var a Asset
	var tags, flags pq.StringArray
	err := row.Scan(&a.ID, &a.ProjectID, &a.ShotID, &a.TodoID, &a.Type, &a.BlobKey, &a.URL,
		&a.Prompt, &a.Style, &a.Provider, &a.Model, &a.Status, &a.Version, &a.ParentAssetID, &tags,
		&a.PrescreenScore, &flags, &a.PrescreenNote, &a.ExternalJobID, &a.StorageConfigID)
	a.Tags = []string(tags)
	a.PrescreenFlags = []string(flags)
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
		PrescreenScore: -1, PrescreenFlags: []string{}, // -1 = not screened (M3)
	}
	if a.Type == "" {
		a.Type = "image"
	}
	if a.Status == "" {
		a.Status = "generating"
	}
	if err := s.db.WithContext(ctx).Exec(
		`INSERT INTO assets (`+assetCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		a.ID, a.ProjectID, a.ShotID, a.TodoID, a.Type, a.BlobKey, a.URL, a.Prompt, a.Style,
		a.Provider, a.Model, a.Status, a.Version, a.ParentAssetID, pq.StringArray(a.Tags),
		a.PrescreenScore, pq.StringArray(a.PrescreenFlags), a.PrescreenNote, a.ExternalJobID, a.StorageConfigID).Error; err != nil {
		return Asset{}, fmt.Errorf("assets: insert: %w", err)
	}
	return a, nil
}

// Get returns an asset by id.
func (s *Store) Get(ctx context.Context, id string) (Asset, error) {
	a, err := scanAsset(s.db.WithContext(ctx).Raw(`SELECT `+assetCols+` FROM assets WHERE id=$1`, id).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	return a, err
}

// SetBlob updates blob_key/url/provider/model/status after generation completes.
// Guarded on status IN ('generating','submitted') so it advances both the sync
// image path (generating→pending_acceptance) AND the async poll-done completion
// (submitted→pending_acceptance, M4 §5.4); a row that already left those states
// (concurrent reclaim/cancel) is a no-op.
//
// Returns won=(rowsAffected==1): the caller learns whether THIS transition
// actually advanced the row. The async poll-done completion (F-INT-1) uses won
// as the TOCTOU-free won/lost arbiter under cross-worker reclaim — only the
// worker whose SetBlob flips submitted→pending_acceptance emits asset_generated
// + books the ledger; a loser (won=false) bows out benignly.
// storageConfigID records WHICH backend the bytes landed in (a storage_configs.id
// or the "builtin" sentinel) so the serve path re-resolves THAT backend regardless
// of the project's current storage_mode. URL-only/failed transitions pass "".
func (s *Store) SetBlob(ctx context.Context, id, blobKey, url, provider, model, storageConfigID, newStatus string) (bool, error) {
	res := s.db.WithContext(ctx).Exec(
		`UPDATE assets SET blob_key=$2, url=$3, provider=$4, model=$5, storage_config_id=$6, status=$7
		 WHERE id=$1 AND status IN ('generating','submitted')`,
		id, blobKey, url, provider, model, storageConfigID, newStatus)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// SetCoverBlob writes blob_key/url unconditionally (M14 cover assets). Unlike
// SetBlob there is NO status guard: a cover asset is created status='accepted',
// so SetBlob's generating/submitted-only guard would no-op. Wraps errors.
func (s *Store) SetCoverBlob(ctx context.Context, id, blobKey, url, storageConfigID string) error {
	if err := s.db.WithContext(ctx).Exec(
		`UPDATE assets SET blob_key=$2, url=$3, storage_config_id=$4 WHERE id=$1`,
		id, blobKey, url, storageConfigID).Error; err != nil {
		return fmt.Errorf("assets: set cover blob: %w", err)
	}
	return nil
}

// TransitionStatus moves an asset from→to atomically, returning ok=false (no
// error) when the row is not in the expected `from` state (HITL 409 semantics,
// spec §7.4 防重).
func (s *Store) TransitionStatus(ctx context.Context, id, from, to string) (bool, error) {
	res := s.db.WithContext(ctx).Exec(`UPDATE assets SET status=$3 WHERE id=$1 AND status=$2`, id, from, to)
	if res.Error != nil {
		return false, fmt.Errorf("assets: transition: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// VersionHistory walks the parent_asset_id chain up + descendants down so the
// review drawer can render v1→v2→… lineage (spec §7.4). Returns oldest-first.
func (s *Store) VersionHistory(ctx context.Context, id string) ([]Asset, error) {
	rows, err := s.db.WithContext(ctx).Raw(`
		WITH RECURSIVE up AS (
			SELECT `+assetCols+` FROM assets WHERE id=$1
			UNION
			SELECT a.id, a.project_id, a.shot_id, a.todo_id, a.type, a.blob_key, a.url, a.prompt,
			       a.style, a.provider, a.model, a.status, a.version, a.parent_asset_id, a.tags,
			       a.prescreen_score, a.prescreen_flags, a.prescreen_note, a.external_job_id, a.storage_config_id
			FROM assets a JOIN up u ON a.id = u.parent_asset_id
		), down AS (
			SELECT `+assetCols+` FROM assets WHERE id=$1
			UNION
			SELECT a.id, a.project_id, a.shot_id, a.todo_id, a.type, a.blob_key, a.url, a.prompt,
			       a.style, a.provider, a.model, a.status, a.version, a.parent_asset_id, a.tags,
			       a.prescreen_score, a.prescreen_flags, a.prescreen_note, a.external_job_id, a.storage_config_id
			FROM assets a JOIN down d ON a.parent_asset_id = d.id
		)
		SELECT `+assetCols+` FROM up
		UNION
		SELECT `+assetCols+` FROM down
		ORDER BY version ASC`, id).Rows()
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
	// Placeholders use GORM's native `?` bindvar (translated to $N for pgx):
	// GORM's $N scanner mis-binds args nested inside ARRAY[$N]::text[], so the
	// dynamic builder emits `?` and lets the driver number them positionally.
	var conds []string
	var args []any
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, cond)
	}
	add("p.org_id=?", f.OrgID)
	add("a.id>?", f.Cursor)
	if f.ProjectID != "" {
		add("a.project_id=?", f.ProjectID)
	}
	if f.Type != "" {
		add("a.type=?", f.Type)
	}
	if f.Status != "" {
		add("a.status=?", f.Status)
	}
	if f.Style != "" {
		add("a.style=?", f.Style)
	}
	if f.Tag != "" {
		add("a.tags @> ARRAY[?]::text[]", f.Tag)
	}
	args = append(args, limit)
	q := `SELECT a.id, a.project_id, a.shot_id, a.todo_id, a.type, a.blob_key, a.url, a.prompt,
		a.style, a.provider, a.model, a.status, a.version, a.parent_asset_id, a.tags,
		a.prescreen_score, a.prescreen_flags, a.prescreen_note, a.external_job_id, a.storage_config_id
		FROM assets a JOIN projects p ON a.project_id = p.id
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY a.id ASC LIMIT ?`
	rows, err := s.db.WithContext(ctx).Raw(q, args...).Rows()
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
	err := s.db.WithContext(ctx).Raw(
		`SELECT p.org_id FROM assets a JOIN projects p ON a.project_id=p.id WHERE a.id=$1`, assetID).Row().Scan(&orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return orgID, err
}

// SetPrescreen records the advisory ReviewAgent verdict (M3). Unconditional on
// status: the verdict is metadata, valid whatever HITL later decides.
func (s *Store) SetPrescreen(ctx context.Context, id string, score int, flags []string, note string) error {
	if flags == nil {
		flags = []string{}
	}
	if err := s.db.WithContext(ctx).Exec(
		`UPDATE assets SET prescreen_score=$2, prescreen_flags=$3, prescreen_note=$4 WHERE id=$1`,
		id, score, pq.StringArray(flags), note).Error; err != nil {
		return fmt.Errorf("assets: set prescreen: %w", err)
	}
	return nil
}

// GetOrCreateForTodo returns the existing asset for a todo_id, or inserts a fresh
// one (B1 crash-idempotency: a reclaimed submit dispatch reuses the same row
// rather than creating a duplicate). Relies on the assets_todo_uniq partial
// unique index; the ON CONFLICT + re-read closes the concurrent-insert window.
// TodoID MUST be non-empty (fan-out/async submit only — regenerate carries
// todo_id=” and uses the fill-in-place path, never this).
func (s *Store) GetOrCreateForTodo(ctx context.Context, in CreateInput) (Asset, error) {
	if in.TodoID == "" {
		return Asset{}, fmt.Errorf("assets: GetOrCreateForTodo requires a non-empty todo_id")
	}
	existing, err := scanAsset(s.db.WithContext(ctx).Raw(
		`SELECT `+assetCols+` FROM assets WHERE todo_id=$1`, in.TodoID).Row())
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Asset{}, fmt.Errorf("assets: get for todo: %w", err)
	}
	created, err := s.Create(ctx, in)
	if err == nil {
		return created, nil
	}
	// Lost an insert race: re-read the winner.
	got, rerr := scanAsset(s.db.WithContext(ctx).Raw(`SELECT `+assetCols+` FROM assets WHERE todo_id=$1`, in.TodoID).Row())
	if rerr == nil {
		return got, nil
	}
	return Asset{}, fmt.Errorf("assets: get-or-create for todo: %w", err)
}

// SetSubmitted advances an async asset generating→submitted, recording the
// provider job handle + submit timestamp (the timestamp feeds the orphan
// reaper, M4 §5.4). Guarded on status='generating' so a re-submit after
// reclaim doesn't reset an already-submitted row.
func (s *Store) SetSubmitted(ctx context.Context, id, externalJobID string) error {
	err := s.db.WithContext(ctx).Exec(
		`UPDATE assets SET status='submitted', external_job_id=$2, submitted_at=now()
		 WHERE id=$1 AND status='generating'`, id, externalJobID).Error
	if err != nil {
		return fmt.Errorf("assets: set submitted: %w", err)
	}
	return nil
}

// SetAsyncFailed terminal-states an async asset from generating OR submitted
// (B2: every async failure/cancel path uses this — SetBlob's generating-only
// guard would silently strand a submitted asset). image's sync path keeps using
// SetBlob(...,'failed').
func (s *Store) SetAsyncFailed(ctx context.Context, id string) error {
	err := s.db.WithContext(ctx).Exec(
		`UPDATE assets SET status='failed' WHERE id=$1 AND status IN ('generating','submitted')`, id).Error
	if err != nil {
		return fmt.Errorf("assets: set async failed: %w", err)
	}
	return nil
}

// CountInFlightByKind counts external jobs currently in flight for a kind (B2
// submit-admission cap: limits PROVIDER-SIDE in-flight jobs, not local running
// todos — submitted spans the whole external job lifetime). Reuses assets_status_idx.
func (s *Store) CountInFlightByKind(ctx context.Context, kind string) (int, error) {
	var n int
	if err := s.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM assets WHERE status='submitted' AND type=$1`, kind).Row().Scan(&n); err != nil {
		return 0, fmt.Errorf("assets: count in-flight: %w", err)
	}
	return n, nil
}

// CountInFlightByKindOrg is CountInFlightByKind scoped to one org (issue #21
// per-org admission layer): counts submitted assets of a kind whose project
// belongs to orgID. Joins projects for org_id (assets carry org only transitively
// via project_id). Reuses assets_status_idx + projects_org_idx.
func (s *Store) CountInFlightByKindOrg(ctx context.Context, kind, orgID string) (int, error) {
	var n int
	if err := s.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM assets a JOIN projects p ON a.project_id=p.id
		 WHERE a.status='submitted' AND a.type=$1 AND p.org_id=$2`, kind, orgID).Row().Scan(&n); err != nil {
		return 0, fmt.Errorf("assets: count in-flight by org: %w", err)
	}
	return n, nil
}

// ReapStaleSubmitted terminal-states submitted assets older than the cutoff
// (orphan reaper, M4 §5.4 M1: a provider that never returns would strand the
// asset forever). Returns the number reaped.
func (s *Store) ReapStaleSubmitted(ctx context.Context, olderThan time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Exec(
		`UPDATE assets SET status='failed' WHERE status='submitted' AND submitted_at IS NOT NULL AND submitted_at < $1`,
		olderThan)
	if res.Error != nil {
		return 0, fmt.Errorf("assets: reap stale submitted: %w", res.Error)
	}
	return res.RowsAffected, nil
}
