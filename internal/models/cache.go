package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/localcache"
	"github.com/costa92/llm-agent-studio/internal/secretbox"
)

// cacheTable is the pg_notify table key and NOTIFY payload discriminator.
const cacheTable = "model_configs"

// modelCacheRow mirrors a full model_configs row for the in-memory cache. It
// holds the api_key as CIPHERTEXT (APIKeyEnc) — decryption happens at read time
// in resolveFromRow so plaintext keys never persist in the cache (keystone K8).
type modelCacheRow struct {
	ID        string
	OrgID     string
	Kind      string
	Provider  string
	Model     string
	Enabled   bool
	IsDefault bool
	BaseURL   string
	APIKeyEnc []byte
	Params    []byte
	CreatedAt time.Time
}

func (r *modelCacheRow) GetID() string      { return r.ID }
func (r *modelCacheRow) GetValue() *modelCacheRow { return r }
func (r *modelCacheRow) DeepCopy() *modelCacheRow {
	c := *r
	c.APIKeyEnc = append([]byte(nil), r.APIKeyEnc...)
	c.Params = append([]byte(nil), r.Params...)
	return &c
}

// loadModelConfigs is the cache reloader: it pulls every model_configs row
// (ciphertext key preserved).
func loadModelConfigs(db *gorm.DB) func() ([]*modelCacheRow, error) {
	return func() ([]*modelCacheRow, error) {
		rows, err := db.WithContext(context.Background()).Raw(
			`SELECT id, org_id, kind, provider, model, enabled, is_default,
			        COALESCE(base_url,''), api_key_enc, params_json, created_at
			 FROM model_configs`).Rows()
		if err != nil {
			return nil, fmt.Errorf("models: cache load: %w", err)
		}
		defer rows.Close()
		out := make([]*modelCacheRow, 0)
		for rows.Next() {
			var r modelCacheRow
			if err := rows.Scan(&r.ID, &r.OrgID, &r.Kind, &r.Provider, &r.Model,
				&r.Enabled, &r.IsDefault, &r.BaseURL, &r.APIKeyEnc, &r.Params, &r.CreatedAt); err != nil {
				return nil, fmt.Errorf("models: cache scan: %w", err)
			}
			out = append(out, &r)
		}
		return out, rows.Err()
	}
}

// NewCached builds a Store backed by an in-memory model_configs cache and
// registers it with the invalidation hub. Reads are served from the cache
// (authoritative after PreloadAll); writes call hub.Invalidate.
func NewCached(db *gorm.DB, box *secretbox.Box, hub *localcache.Hub) *Store {
	s := &Store{db: db, box: box, hub: hub}
	s.cache = localcache.NewCustom[*modelCacheRow, string](loadModelConfigs(db))
	hub.Register(cacheTable, s.cache.ReloadAll)
	return s
}

// invalidate reloads the local cache and broadcasts to peers after a write. The
// write is already committed; a reload failure is logged, not surfaced (a failed
// request on committed data would be worse than a briefly-stale cache that the
// TTL/listener will heal).
func (s *Store) invalidate(ctx context.Context) {
	if s.hub == nil {
		return
	}
	if err := s.hub.Invalidate(ctx, cacheTable); err != nil {
		slog.Default().Warn("models: cache invalidate failed", "err", err)
	}
}

// pickCachedDefault returns the enabled+default row for (org,kind), newest first.
func (s *Store) pickCachedDefault(org, kind string) *modelCacheRow {
	var best *modelCacheRow
	for _, r := range s.cache.GetAllRaw() {
		if r.OrgID != org || r.Kind != kind || !r.Enabled || !r.IsDefault {
			continue
		}
		if best == nil || r.CreatedAt.After(best.CreatedAt) {
			best = r
		}
	}
	return best
}

// pickCachedNamed mirrors ResolveForOrgNamed's ORDER BY is_default DESC,
// created_at DESC over the enabled rows matching (org,kind,provider,model).
func (s *Store) pickCachedNamed(org, kind, provider, model string) *modelCacheRow {
	var best *modelCacheRow
	for _, r := range s.cache.GetAllRaw() {
		if r.OrgID != org || r.Kind != kind || r.Provider != provider || r.Model != model || !r.Enabled {
			continue
		}
		if best == nil || namedBetter(r, best) {
			best = r
		}
	}
	return best
}

func namedBetter(a, b *modelCacheRow) bool {
	if a.IsDefault != b.IsDefault {
		return a.IsDefault
	}
	return a.CreatedAt.After(b.CreatedAt)
}

// resolveFromRow builds a ResolvedModel, decrypting the api key at read time.
func (s *Store) resolveFromRow(r *modelCacheRow) (ResolvedModel, bool, error) {
	rm := ResolvedModel{Provider: r.Provider, Model: r.Model, BaseURL: r.BaseURL, Params: json.RawMessage(r.Params)}
	if len(r.APIKeyEnc) > 0 {
		if !s.box.Enabled() {
			return ResolvedModel{}, false, ErrEncUnavailable
		}
		pt, err := s.box.Decrypt(r.APIKeyEnc)
		if err != nil {
			return ResolvedModel{}, false, fmt.Errorf("models: decrypt api key: %w", err)
		}
		rm.APIKey = string(pt)
	}
	return rm, true, nil
}

func (s *Store) resolveForOrgCached(orgID, kind string) (ResolvedModel, bool, error) {
	r := s.pickCachedDefault(orgID, kind)
	if r == nil {
		return ResolvedModel{}, false, nil
	}
	return s.resolveFromRow(r)
}

func (s *Store) resolveForOrgNamedCached(orgID, kind, provider, modelName string) (ResolvedModel, bool, error) {
	r := s.pickCachedNamed(orgID, kind, provider, modelName)
	if r == nil {
		return ResolvedModel{}, false, nil
	}
	return s.resolveFromRow(r)
}

func (s *Store) defaultForOrgCached(orgID, kind string) (string, string, bool, error) {
	r := s.pickCachedDefault(orgID, kind)
	if r == nil {
		return "", "", false, nil
	}
	return r.Provider, r.Model, true, nil
}

func (s *Store) listByOrgCached(orgID string) ([]ModelConfig, error) {
	rows := s.cache.GetAllRaw()
	out := make([]*modelCacheRow, 0)
	for _, r := range rows {
		if r.OrgID == orgID {
			out = append(out, r)
		}
	}
	// newest first (matches SQL ORDER BY created_at DESC)
	sortByCreatedDesc(out)
	res := make([]ModelConfig, 0, len(out))
	for _, r := range out {
		res = append(res, ModelConfig{
			ID: r.ID, OrgID: r.OrgID, Kind: r.Kind, Provider: r.Provider, Model: r.Model,
			Enabled: r.Enabled, IsDefault: r.IsDefault, BaseURL: r.BaseURL,
			Params: json.RawMessage(r.Params), HasAPIKey: len(r.APIKeyEnc) > 0,
		})
	}
	return res, nil
}

func sortByCreatedDesc(rows []*modelCacheRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].CreatedAt.After(rows[j-1].CreatedAt); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}
