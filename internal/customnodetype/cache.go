package customnodetype

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/localcache"
)

const cacheTable = "custom_node_types"

// ntCacheRow wraps a CustomNodeType plus created_at (needed for List ordering).
type ntCacheRow struct {
	CustomNodeType
	CreatedAt time.Time
}

func (r *ntCacheRow) GetID() string        { return r.ID }
func (r *ntCacheRow) GetValue() *ntCacheRow { return r }
func (r *ntCacheRow) DeepCopy() *ntCacheRow {
	c := *r
	c.Params = append(json.RawMessage(nil), r.Params...)
	return &c
}

func loadNodeTypes(db *gorm.DB) func() ([]*ntCacheRow, error) {
	return func() ([]*ntCacheRow, error) {
		rows, err := db.WithContext(context.Background()).Raw(
			`SELECT id, org_id, slug, label, color, kind, params, created_at FROM custom_node_types`).Rows()
		if err != nil {
			return nil, fmt.Errorf("customnodetype: cache load: %w", err)
		}
		defer rows.Close()
		out := make([]*ntCacheRow, 0)
		for rows.Next() {
			var r ntCacheRow
			var params []byte
			if err := rows.Scan(&r.ID, &r.OrgID, &r.Slug, &r.Label, &r.Color, &r.Kind, &params, &r.CreatedAt); err != nil {
				return nil, fmt.Errorf("customnodetype: cache scan: %w", err)
			}
			r.Params = json.RawMessage(params)
			out = append(out, &r)
		}
		return out, rows.Err()
	}
}

// NewCached builds a cache-backed Store and registers it with the hub.
func NewCached(db *gorm.DB, hub *localcache.Hub) *Store {
	s := &Store{db: db}
	s.cache = localcache.NewCustom[*ntCacheRow, string](loadNodeTypes(db))
	hub.Register(cacheTable, s.cache.ReloadAll)
	s.hub = hub
	return s
}

func (s *Store) invalidate(ctx context.Context) {
	if s.hub == nil {
		return
	}
	if err := s.hub.Invalidate(ctx, cacheTable); err != nil {
		slog.Default().Warn("customnodetype: cache invalidate failed", "err", err)
	}
}

func (s *Store) listCached(orgID string) ([]CustomNodeType, error) {
	rows := s.cache.GetAll(func(r *ntCacheRow) bool { return r.OrgID == orgID })
	// created_at ASC (matches SQL ORDER BY created_at ASC)
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].CreatedAt.Before(rows[j-1].CreatedAt); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	out := make([]CustomNodeType, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.CustomNodeType)
	}
	return out, nil
}

func (s *Store) getCached(id, orgID string) (CustomNodeType, error) {
	r, err := s.cache.Get(id)
	if err != nil || r.OrgID != orgID {
		return CustomNodeType{}, ErrNotFound
	}
	return r.CustomNodeType, nil
}
