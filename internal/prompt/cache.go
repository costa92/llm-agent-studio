package prompt

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/localcache"
)

const cacheTable = "prompts"

// promptCacheRow wraps promptRow (all value fields → shallow copy is deep).
type promptCacheRow struct {
	promptRow
}

func (r *promptCacheRow) GetID() string           { return r.ID }
func (r *promptCacheRow) GetValue() *promptCacheRow { return r }
func (r *promptCacheRow) DeepCopy() *promptCacheRow { c := *r; return &c }

func loadPrompts(db *gorm.DB) func() ([]*promptCacheRow, error) {
	return func() ([]*promptCacheRow, error) {
		var rows []promptRow
		if err := db.WithContext(context.Background()).Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("prompt: cache load: %w", err)
		}
		out := make([]*promptCacheRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, &promptCacheRow{r})
		}
		return out, nil
	}
}

// NewStoreCached builds a cache-backed Store and registers it with the hub.
func NewStoreCached(db *gorm.DB, hub *localcache.Hub) *Store {
	s := &Store{db: db, hub: hub}
	s.cache = localcache.NewCustom[*promptCacheRow, string](loadPrompts(db))
	hub.Register(cacheTable, s.cache.ReloadAll)
	return s
}

func (s *Store) invalidate(ctx context.Context) {
	if s.hub == nil {
		return
	}
	if err := s.hub.Invalidate(ctx, cacheTable); err != nil {
		slog.Default().Warn("prompt: cache invalidate failed", "err", err)
	}
}

func (s *Store) listByOrgCached(orgID string) ([]Prompt, error) {
	rows := s.cache.GetAll(func(r *promptCacheRow) bool { return r.OrgID == orgID })
	// created_at DESC (matches SQL ORDER BY created_at DESC)
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].CreatedAt.After(rows[j-1].CreatedAt); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	out := make([]Prompt, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toPrompt())
	}
	return out, nil
}
