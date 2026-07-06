// Package localcache is a small in-memory, full-reload config cache. It mirrors
// the design of code.hellotalk.com/im/config-center-v2/local_cache (generic
// Record[DATA,ID] + ReloadAll + Get/GetAll/GetMap), trimmed to the two sources
// studio needs — a GORM table loader and a custom query func — with no mongo
// dependency and stdlib-only errors.
//
// Semantics: after a ReloadAll the cache is authoritative — Get miss means the
// key genuinely does not exist (callers do NOT fall back to the DB). Reads
// return DeepCopy'd values so callers cannot mutate cached state. Concurrency is
// guarded by an RWMutex; ReloadAll swaps in fresh slices/maps atomically.
package localcache

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
)

// ErrNotInCache is returned by Get when the id is absent from the loaded set.
var ErrNotInCache = errors.New("localcache: key not in cache")

// Record is the contract a cached row type must satisfy. DATA is the row type
// itself (typically a pointer, e.g. *priceRow) and ID its cache key.
type Record[DATA any, ID comparable] interface {
	DeepCopy() DATA
	GetID() ID
	GetValue() DATA
}

// Cache is the read/reload surface exposed to consumers.
type Cache[DATA any, ID comparable] interface {
	ReloadAll() error
	Get(id ID) (DATA, error)
	GetAll(filter func(DATA) bool) []DATA
	GetMap(filter func(DATA) bool) map[ID]DATA
	GetAllRaw() []DATA
	GetUpdateTs() int64
	GetTotal() int
}

// OrderBy selects the reload sort direction for the GORM loader.
type OrderBy string

const (
	OrderByAsc  OrderBy = "asc"
	OrderByDesc OrderBy = "desc"
)

type options struct {
	where       any
	order       OrderBy
	orderColumn string
}

// Option configures a GORM-backed cache.
type Option func(*options)

// WithWhere adds a GORM Where clause to the reload query (map/struct/string).
func WithWhere(w any) Option { return func(o *options) { o.where = w } }

// WithOrderBy sets the reload sort direction (default asc).
func WithOrderBy(b OrderBy) Option { return func(o *options) { o.order = b } }

// WithOrderColumn sets the reload sort column (default "id").
func WithOrderColumn(c string) Option { return func(o *options) { o.orderColumn = c } }

type cache[DATA Record[DATA, ID], ID comparable] struct {
	mu       sync.RWMutex
	arr      []DATA
	byID     map[ID]DATA
	updateTs int64

	// gorm source
	db    *gorm.DB
	table string
	opts  options

	// custom source (mutually exclusive with gorm)
	load func() ([]DATA, error)
}

// NewGORM builds a cache that reloads by paging the whole table via GORM. Kept
// primarily for the unit-test shape; studio's config tables use NewCustom.
func NewGORM[DATA Record[DATA, ID], ID comparable](db *gorm.DB, table string, opts ...Option) Cache[DATA, ID] {
	o := options{}
	for _, f := range opts {
		f(&o)
	}
	return &cache[DATA, ID]{db: db, table: table, opts: o, byID: map[ID]DATA{}}
}

// NewCustom builds a cache whose reload runs an arbitrary loader func — used for
// rows needing hand-written SQL (bytea columns, composite keys, decrypt-at-read).
func NewCustom[DATA Record[DATA, ID], ID comparable](load func() ([]DATA, error)) Cache[DATA, ID] {
	return &cache[DATA, ID]{load: load, byID: map[ID]DATA{}}
}

func (c *cache[DATA, ID]) ReloadAll() error {
	var (
		rows []DATA
		err  error
	)
	if c.load != nil {
		rows, err = c.load()
	} else {
		rows, err = c.gormLoad()
	}
	if err != nil {
		return err
	}
	m := make(map[ID]DATA, len(rows))
	for _, r := range rows {
		m[r.GetID()] = r
	}
	c.mu.Lock()
	c.arr = rows
	c.byID = m
	c.updateTs = time.Now().Unix()
	c.mu.Unlock()
	return nil
}

func (c *cache[DATA, ID]) gormLoad() ([]DATA, error) {
	ctx := context.Background()
	const limit = 1000
	offset := 0
	out := make([]DATA, 0)
	for {
		var batch []DATA
		q := c.db.WithContext(ctx).Table(c.table)
		if c.opts.where != nil {
			q = q.Where(c.opts.where)
		}
		col := c.opts.orderColumn
		if col == "" {
			col = "id"
		}
		if c.opts.order == OrderByDesc {
			q = q.Order(col + " desc")
		} else {
			q = q.Order(col + " asc")
		}
		if err := q.Limit(limit).Offset(offset).Find(&batch).Error; err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < limit {
			break
		}
		offset += limit
	}
	return out, nil
}

func (c *cache[DATA, ID]) Get(id ID) (DATA, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.byID[id]
	if !ok {
		var zero DATA
		return zero, ErrNotInCache
	}
	return v.DeepCopy(), nil
}

func (c *cache[DATA, ID]) GetAll(filter func(DATA) bool) []DATA {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]DATA, 0, len(c.arr))
	for _, v := range c.arr {
		if filter == nil || filter(v.GetValue()) {
			out = append(out, v.DeepCopy())
		}
	}
	return out
}

func (c *cache[DATA, ID]) GetMap(filter func(DATA) bool) map[ID]DATA {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[ID]DATA, len(c.byID))
	for k, v := range c.byID {
		if filter == nil || filter(v.GetValue()) {
			out[k] = v.DeepCopy()
		}
	}
	return out
}

// GetAllRaw returns the underlying slice WITHOUT copying — read-only. Callers
// must not mutate the returned values.
func (c *cache[DATA, ID]) GetAllRaw() []DATA {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.arr
}

func (c *cache[DATA, ID]) GetUpdateTs() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.updateTs
}

func (c *cache[DATA, ID]) GetTotal() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.arr)
}
