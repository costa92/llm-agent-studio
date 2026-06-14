package prompt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Prompt is a database-persisted prompt template.
type Prompt struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Style     string    `json:"style"`
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
func (s *Store) Create(ctx context.Context, orgID, name, content, style string) (Prompt, error) {
	if orgID == "" || name == "" || content == "" {
		return Prompt{}, fmt.Errorf("prompt: orgID, name, and content are required")
	}
	id := newID()
	now := time.Now()
	query := `INSERT INTO prompts (id, org_id, name, content, style, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, org_id, name, content, style, created_at, updated_at`

	var p Prompt
	err := s.pool.QueryRow(ctx, query, id, orgID, name, content, style, now, now).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Content, &p.Style, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt: create: %w", err)
	}
	return p, nil
}

// Update updates a prompt template.
func (s *Store) Update(ctx context.Context, id, orgID, name, content, style string) (Prompt, error) {
	if id == "" || orgID == "" || name == "" || content == "" {
		return Prompt{}, fmt.Errorf("prompt: id, orgID, name, and content are required")
	}
	now := time.Now()
	query := `UPDATE prompts SET name = $3, content = $4, style = $5, updated_at = $6
		WHERE id = $1 AND org_id = $2
		RETURNING id, org_id, name, content, style, created_at, updated_at`

	var p Prompt
	err := s.pool.QueryRow(ctx, query, id, orgID, name, content, style, now).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Content, &p.Style, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Prompt{}, fmt.Errorf("prompt: update: %w", err)
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
	query := `SELECT id, org_id, name, content, style, created_at, updated_at
		FROM prompts WHERE org_id = $1 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("prompt: list: %w", err)
	}
	defer rows.Close()

	var prompts []Prompt
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Content, &p.Style, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("prompt: list scan: %w", err)
		}
		prompts = append(prompts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("prompt: list rows: %w", err)
	}
	return prompts, nil
}
