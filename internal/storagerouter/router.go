// Package storagerouter centralizes per-org 对象存储路由: it resolves an org's
// stored storage_config (per-org → global) and constructs the matching
// blob.BlobStore, falling back to a built-in Default when the org has no usable
// config. It does NOT import concrete adapter packages (localfs/oss/s3/cos) —
// construction is injected via the Build factory func (that lives in cmd/studiod),
// so this package stays adapter-agnostic and unit-testable. Built stores are
// cached by config identity (RWMutex-guarded) to avoid rebuilding a client per call.
package storagerouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/storageconfig"
)

// resolver is the slice of *storageconfig.Store the router needs (extracted so
// router unit tests can fake resolution without a live PG — *storageconfig.Store
// satisfies it). Resolution already encodes per-org → global precedence.
type resolver interface {
	ResolveForOrg(ctx context.Context, orgID string) (storageconfig.ResolvedStorage, bool, error)
	ResolveForOrgAndMode(ctx context.Context, orgID string, mode string) (storageconfig.ResolvedStorage, bool, error)
}

// Config configures a Router. Configs/Build are required for routing; Default is
// the built-in localfs store (always usable) returned on any miss/error.
type Config struct {
	Configs resolver
	Default blob.BlobStore                                                // 内置 localfs 默认，永远可用
	Build   func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) // 由 cmd/studiod 注入
	Logger  *slog.Logger                                                  // nil → slog.Default()
}

// Router resolves+constructs per-org blob stores, caching by config identity.
type Router struct {
	configs resolver
	def     blob.BlobStore
	build   func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error)
	log     *slog.Logger

	mu    sync.RWMutex
	cache map[string]blob.BlobStore
}

// New builds a Router.
func New(cfg Config) *Router {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Router{
		configs: cfg.Configs,
		def:     cfg.Default,
		build:   cfg.Build,
		log:     log,
		cache:   make(map[string]blob.BlobStore),
	}
}

// BlobStoreFor returns the org's configured blob store, else Default. NEVER
// returns nil-meaningfully: on any miss/error/build-failure it returns Default
// (callers depend on a usable store; if Default is also nil the caller handles
// that — the router does not invent one). Resolution already encodes per-org →
// global; Default is the third layer.
func (r *Router) BlobStoreFor(ctx context.Context, orgID string) (blob.BlobStore, error) {
	return r.BlobStoreForMode(ctx, orgID, "")
}

// BlobStoreForMode returns the org's configured blob store for a specific mode, else Default.
func (r *Router) BlobStoreForMode(ctx context.Context, orgID string, mode string) (blob.BlobStore, error) {
	if r.configs == nil || r.build == nil {
		return r.def, nil
	}
	rs, ok, err := r.configs.ResolveForOrgAndMode(ctx, orgID, mode)
	if err != nil {
		r.log.Warn("storagerouter: resolve storage config failed; using default store", "org", orgID, "mode", mode, "err", err)
		return r.def, nil
	}
	if !ok {
		return r.def, nil
	}
	key := identity(rs)
	// 缓存命中。
	r.mu.RLock()
	if bs, hit := r.cache[key]; hit {
		r.mu.RUnlock()
		return bs, nil
	}
	r.mu.RUnlock()
	// 缓存未命中 → 构造。
	bs, berr := r.build(rs)
	if berr != nil {
		r.log.Warn("storagerouter: build org blob store failed; using default store",
			"org", orgID, "mode", rs.Mode, "bucket", rs.Bucket, "err", berr)
		return r.def, nil
	}
	r.mu.Lock()
	// double-check：并发下别人可能已建好同 key。
	if existing, hit := r.cache[key]; hit {
		r.mu.Unlock()
		return existing, nil
	}
	r.cache[key] = bs
	r.mu.Unlock()
	return bs, nil
}

// identity 是缓存键：mode|endpoint|region|bucket|accessKeyID|sha256(secret)|useSSL|publicPrefix。
// secret 不进明文键 (取 sha256)；不同 secret/bucket 等字段变化 → 不同身份 → 重建。
func identity(rs storageconfig.ResolvedStorage) string {
	sum := sha256.Sum256([]byte(rs.SecretKey))
	return strings.Join([]string{
		rs.Mode, rs.Endpoint, rs.Region, rs.Bucket, rs.AccessKeyID,
		hex.EncodeToString(sum[:]), strconv.FormatBool(rs.UseSSL), rs.PublicPrefix,
	}, "|")
}
