package prompt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a prompt row does not exist.
var ErrNotFound = errors.New("prompt: not found")

// Prompt is a database-persisted prompt template.
type Prompt struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Style     string    `json:"style"`
	Kind      string    `json:"kind"`
	IsDefault bool      `json:"isDefault"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store persists prompt templates.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create inserts a prompt template.
func (s *Store) Create(ctx context.Context, orgID, name, content, style, kind string) (Prompt, error) {
	if orgID == "" || name == "" || content == "" {
		return Prompt{}, fmt.Errorf("prompt: orgID, name, and content are required")
	}
	id := newID()
	now := time.Now()
	query := `INSERT INTO prompts (id, org_id, name, content, style, kind, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`

	var p Prompt
	err := s.pool.QueryRow(ctx, query, id, orgID, name, content, style, kind, now, now).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Content, &p.Style, &p.Kind, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt: create: %w", err)
	}
	return p, nil
}

// Update updates a prompt template. Re-typing a default prompt (changing kind)
// drops its is_default flag so the per-(org,kind) partial-unique index can't
// see a stale default leaking into another kind.
func (s *Store) Update(ctx context.Context, id, orgID, name, content, style, kind string) (Prompt, error) {
	if id == "" || orgID == "" || name == "" || content == "" {
		return Prompt{}, fmt.Errorf("prompt: id, orgID, name, and content are required")
	}
	now := time.Now()
	query := `UPDATE prompts SET name = $3, content = $4, style = $5, kind = $7,
			is_default = (is_default AND kind = $7), updated_at = $6
		WHERE id = $1 AND org_id = $2
		RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`

	var p Prompt
	err := s.pool.QueryRow(ctx, query, id, orgID, name, content, style, now, kind).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Content, &p.Style, &p.Kind, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt: update: %w", err)
	}
	return p, nil
}

// SetDefault marks a prompt as the per-(org,kind) default, clearing the default
// flag on its same-kind siblings first so the partial-unique index never sees
// two defaults transiently. Runs in a single transaction.
func (s *Store) SetDefault(ctx context.Context, id, orgID string) (Prompt, error) {
	if id == "" || orgID == "" {
		return Prompt{}, fmt.Errorf("prompt: id and orgID are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt: set default: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM prompts WHERE id = $1 AND org_id = $2`, id, orgID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return Prompt{}, ErrNotFound
	}
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt: set default: lookup: %w", err)
	}

	// Clear siblings of the same kind BEFORE setting the target so the partial
	// unique index never sees two defaults at once.
	if _, err := tx.Exec(ctx,
		`UPDATE prompts SET is_default = false WHERE org_id = $2 AND kind = $3 AND id <> $1`,
		id, orgID, kind); err != nil {
		return Prompt{}, fmt.Errorf("prompt: set default: clear siblings: %w", err)
	}

	var p Prompt
	err = tx.QueryRow(ctx,
		`UPDATE prompts SET is_default = true, updated_at = now() WHERE id = $1 AND org_id = $2
			RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`,
		id, orgID).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Content, &p.Style, &p.Kind, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt: set default: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Prompt{}, fmt.Errorf("prompt: set default: commit: %w", err)
	}
	return p, nil
}

// Delete removes a prompt template.
func (s *Store) Delete(ctx context.Context, id, orgID string) error {
	if id == "" || orgID == "" {
		return fmt.Errorf("prompt: id and orgID are required")
	}
	query := `DELETE FROM prompts WHERE id = $1 AND org_id = $2`
	res, err := s.pool.Exec(ctx, query, id, orgID)
	if err != nil {
		return fmt.Errorf("prompt: delete: %w", err)
	}
	if res.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListByOrg retrieves all prompt templates for an organization.
func (s *Store) ListByOrg(ctx context.Context, orgID string) ([]Prompt, error) {
	if orgID == "" {
		return nil, fmt.Errorf("prompt: orgID is required")
	}
	query := `SELECT id, org_id, name, content, style, kind, is_default, created_at, updated_at
		FROM prompts WHERE org_id = $1 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("prompt: list: %w", err)
	}
	defer rows.Close()

	var prompts []Prompt
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Content, &p.Style, &p.Kind, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("prompt: list scan: %w", err)
		}
		prompts = append(prompts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("prompt: list rows: %w", err)
	}
	return prompts, nil
}
