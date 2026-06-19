package prompt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
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

// Store persists prompt templates via GORM.
type Store struct {
	db *gorm.DB
}

// NewStore builds a Store.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

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
	// INSERT ... RETURNING（保留原生）：返回 DB 落盘后的时间戳（微秒精度），与后续
	// GET/ListByOrg 读到的值逐字一致——避免 GORM Create 回填 Go 时钟纳秒精度，造成
	// POST 响应与后续读取的 created/updatedAt 在亚微秒位漂移（与 Update/SetDefault 一致）。
	now := time.Now()
	const q = `INSERT INTO prompts (id, org_id, name, content, style, kind, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`
	var row promptRow
	res := s.db.WithContext(ctx).Raw(q, newID(), orgID, name, content, style, kind, now, now).Scan(&row)
	if res.Error != nil {
		return Prompt{}, fmt.Errorf("prompt: create: %w", res.Error)
	}
	return row.toPrompt(), nil
}

// Update updates a prompt template. Re-typing a default prompt (changing kind)
// drops its is_default flag so the per-(org,kind) partial-unique index can't see
// a stale default leaking into another kind. RETURNING + 列表达式 → 保留原生 SQL。
func (s *Store) Update(ctx context.Context, id, orgID, name, content, style, kind string) (Prompt, error) {
	if id == "" || orgID == "" || name == "" || content == "" {
		return Prompt{}, fmt.Errorf("prompt: id, orgID, name, and content are required")
	}
	const q = `UPDATE prompts SET name = $3, content = $4, style = $5, kind = $7,
			is_default = (is_default AND kind = $7), updated_at = $6
		WHERE id = $1 AND org_id = $2
		RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`
	var row promptRow
	res := s.db.WithContext(ctx).Raw(q, id, orgID, name, content, style, time.Now(), kind).Scan(&row)
	if res.Error != nil {
		return Prompt{}, fmt.Errorf("prompt: update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return Prompt{}, ErrNotFound
	}
	return row.toPrompt(), nil
}

// SetDefault marks a prompt as the per-(org,kind) default, clearing same-kind
// siblings first so the partial-unique index never sees two defaults transiently.
// 部分索引顺序敏感 + RETURNING → 在 GORM 事务内保留原生 SQL。
func (s *Store) SetDefault(ctx context.Context, id, orgID string) (Prompt, error) {
	if id == "" || orgID == "" {
		return Prompt{}, fmt.Errorf("prompt: id and orgID are required")
	}
	var out promptRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var kind string
		look := tx.Raw(`SELECT kind FROM prompts WHERE id = $1 AND org_id = $2`, id, orgID).Scan(&kind)
		if look.Error != nil {
			return fmt.Errorf("prompt: set default: lookup: %w", look.Error)
		}
		if look.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Exec(
			`UPDATE prompts SET is_default = false WHERE org_id = $2 AND kind = $3 AND id <> $1`,
			id, orgID, kind).Error; err != nil {
			return fmt.Errorf("prompt: set default: clear siblings: %w", err)
		}
		set := tx.Raw(
			`UPDATE prompts SET is_default = true, updated_at = now() WHERE id = $1 AND org_id = $2
				RETURNING id, org_id, name, content, style, kind, is_default, created_at, updated_at`,
			id, orgID).Scan(&out)
		if set.Error != nil {
			return fmt.Errorf("prompt: set default: %w", set.Error)
		}
		return nil
	})
	if err != nil {
		return Prompt{}, err
	}
	return out.toPrompt(), nil
}

// Delete removes a prompt template. 不存在 → ErrNotFound（语义 404）。
func (s *Store) Delete(ctx context.Context, id, orgID string) error {
	if id == "" || orgID == "" {
		return fmt.Errorf("prompt: id and orgID are required")
	}
	res := s.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&promptRow{})
	if res.Error != nil {
		return fmt.Errorf("prompt: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByOrg retrieves all prompt templates for an organization.
func (s *Store) ListByOrg(ctx context.Context, orgID string) ([]Prompt, error) {
	if orgID == "" {
		return nil, fmt.Errorf("prompt: orgID is required")
	}
	var rows []promptRow
	if err := s.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("prompt: list: %w", err)
	}
	prompts := make([]Prompt, 0, len(rows))
	for _, r := range rows {
		prompts = append(prompts, r.toPrompt())
	}
	return prompts, nil
}
