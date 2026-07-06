package localcache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel is the PostgreSQL NOTIFY channel used to broadcast cache invalidations
// across studiod replicas.
const Channel = "studio_cache_invalidate"

// payload is the wire form of an invalidation message. Kept tiny (well under the
// 8000-byte pg_notify limit).
type payload struct {
	Table  string `json:"t"`
	Origin string `json:"o"`
}

// Hub coordinates cross-replica config-cache invalidation over PG LISTEN/NOTIFY.
// A store calls Invalidate after a successful write; the local replica reloads
// synchronously (read-your-writes) and emits a NOTIFY so peer replicas reload
// too. The Hub's own Listener skips messages carrying its own origin token so
// the writer replica does not reload twice.
type Hub struct {
	pool    *pgxpool.Pool
	dsn     string
	origin  string
	log     *slog.Logger
	backoff time.Duration

	mu     sync.RWMutex
	reload map[string]func() error
}

// NewHub builds a Hub. pool is used to publish NOTIFY; dsn opens the dedicated
// LISTEN connection. A per-process origin token is generated for self-skip.
func NewHub(pool *pgxpool.Pool, dsn string, log *slog.Logger) (*Hub, error) {
	if pool == nil || dsn == "" {
		return nil, errors.New("localcache: NewHub requires pool and dsn")
	}
	if log == nil {
		log = slog.Default()
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("localcache: origin token: %w", err)
	}
	return &Hub{
		pool:    pool,
		dsn:     dsn,
		origin:  hex.EncodeToString(b),
		log:     log,
		backoff: time.Second,
		reload:  map[string]func() error{},
	}, nil
}

// Register wires a table name to its cache's ReloadAll. Call once per cached
// table before starting Listen / issuing Invalidate.
func (h *Hub) Register(table string, reload func() error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reload[table] = reload
}

// PreloadAll reloads every registered cache once. Call at startup after all
// stores registered; a returned error should be treated as fatal (config is a
// hard dependency and the DB is already reachable at this point).
func (h *Hub) PreloadAll() error {
	h.mu.RLock()
	tables := make([]string, 0, len(h.reload))
	for t := range h.reload {
		tables = append(tables, t)
	}
	h.mu.RUnlock()
	for _, t := range tables {
		f, ok := h.reloadFor(t)
		if !ok {
			continue
		}
		if err := f(); err != nil {
			return fmt.Errorf("localcache: preload %q: %w", t, err)
		}
	}
	return nil
}

func (h *Hub) reloadFor(table string) (func() error, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	f, ok := h.reload[table]
	return f, ok
}

// Invalidate reloads the local cache for table (synchronous, so a write is
// immediately visible on this replica) and broadcasts a NOTIFY for peers. A
// NOTIFY failure is non-fatal: the local reload already succeeded and peers will
// self-heal on their next TTL refresh or listener reconnect.
func (h *Hub) Invalidate(ctx context.Context, table string) error {
	reload, ok := h.reloadFor(table)
	if !ok {
		return fmt.Errorf("localcache: no cache registered for table %q", table)
	}
	if err := reload(); err != nil {
		return fmt.Errorf("localcache: local reload %q: %w", table, err)
	}
	msg, _ := json.Marshal(payload{Table: table, Origin: h.origin})
	if _, err := h.pool.Exec(ctx, "SELECT pg_notify($1, $2)", Channel, string(msg)); err != nil {
		h.log.Warn("localcache: pg_notify failed (local reload ok; peers self-heal)", "table", table, "err", err)
	}
	return nil
}

// Listen runs the LISTEN loop until ctx is canceled. Intended to run in its own
// goroutine. It reconnects on connection loss with backoff, and on every
// (re)connect reloads all registered caches to catch invalidations missed while
// disconnected.
func (h *Hub) Listen(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := h.listenOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			h.log.Warn("localcache: listener disconnected; reconnecting", "backoff", h.backoff, "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(h.backoff):
			}
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, h.dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	// Catch up on anything missed while we were disconnected.
	h.reloadAll()
	h.log.Info("localcache: listening for cache invalidations", "channel", Channel)

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.handle(n.Payload)
	}
}

func (h *Hub) handle(raw string) {
	var p payload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		h.log.Warn("localcache: bad invalidation payload", "raw", raw, "err", err)
		return
	}
	if p.Origin == h.origin {
		return // our own write — already reloaded synchronously in Invalidate
	}
	reload, ok := h.reloadFor(p.Table)
	if !ok {
		h.log.Warn("localcache: invalidation for unknown table", "table", p.Table)
		return
	}
	if err := reload(); err != nil {
		h.log.Error("localcache: reload on invalidation failed", "table", p.Table, "err", err)
	}
}

func (h *Hub) reloadAll() {
	h.mu.RLock()
	tables := make([]string, 0, len(h.reload))
	for t := range h.reload {
		tables = append(tables, t)
	}
	h.mu.RUnlock()
	for _, t := range tables {
		if f, ok := h.reloadFor(t); ok {
			if err := f(); err != nil {
				h.log.Error("localcache: catch-up reload failed", "table", t, "err", err)
			}
		}
	}
}
