package cost

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/localcache"
)

// pricingTable is the pg_notify / hub registration key.
const pricingTable = "pricing"

// priceKey is the pricing cache key: (provider, model).
type priceKey struct {
	Provider string
	Model    string
}

// priceRow is a cached pricing row.
type priceRow struct {
	Provider          string
	Model             string
	MicrosPerImage    int64
	MicrosPer1kTokens int64
	MicrosPerSecond   int64
}

func (r *priceRow) GetID() priceKey    { return priceKey{r.Provider, r.Model} }
func (r *priceRow) GetValue() *priceRow { return r }
func (r *priceRow) DeepCopy() *priceRow { c := *r; return &c }

func loadPricing(db *gorm.DB) func() ([]*priceRow, error) {
	return func() ([]*priceRow, error) {
		rows, err := db.WithContext(context.Background()).Raw(
			`SELECT provider, model, micros_per_image, micros_per_1k_tokens, micros_per_second FROM pricing`).Rows()
		if err != nil {
			return nil, fmt.Errorf("cost: pricing cache load: %w", err)
		}
		defer rows.Close()
		out := make([]*priceRow, 0)
		for rows.Next() {
			var r priceRow
			if err := rows.Scan(&r.Provider, &r.Model, &r.MicrosPerImage, &r.MicrosPer1kTokens, &r.MicrosPerSecond); err != nil {
				return nil, fmt.Errorf("cost: pricing cache scan: %w", err)
			}
			out = append(out, &r)
		}
		return out, rows.Err()
	}
}

// NewCached builds a Store whose PriceFor is served from an in-memory pricing
// cache. Pricing has no application write path (rows are migration-seeded and
// edited via ops SQL), so freshness relies on PreloadAll + RunRefresh (TTL), not
// write-time invalidation.
func NewCached(db *gorm.DB, hub *localcache.Hub) *Store {
	s := &Store{db: db}
	s.cache = localcache.NewCustom[*priceRow, priceKey](loadPricing(db))
	hub.Register(pricingTable, s.cache.ReloadAll)
	return s
}

// RunRefresh periodically reloads the pricing cache until ctx is canceled.
// Intended to run in its own goroutine. A zero/negative ttl disables refresh.
func (s *Store) RunRefresh(ctx context.Context, ttl time.Duration) {
	if s.cache == nil || ttl <= 0 {
		return
	}
	t := time.NewTicker(ttl)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.cache.ReloadAll(); err != nil {
				slog.Default().Warn("cost: pricing cache refresh failed", "err", err)
			}
		}
	}
}
