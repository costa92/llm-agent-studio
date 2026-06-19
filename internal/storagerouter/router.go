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
	// ResolveByID (enabled-only) backs the WRITE-target path (project override /
	// org default re-resolve): a disabled config must not be a new write landing.
	// ResolveByIDForServe (enabled-agnostic) backs the SERVE path: write-time
	// persists the backend identity (config id / "builtin"); serve reads that EXACT
	// backend regardless of the org's current storage_mode OR whether the config is
	// still enabled — disabling only stops new writes, historical bytes keep serving.
	ResolveByID(ctx context.Context, id string) (storageconfig.ResolvedStorage, bool, error)
	ResolveByIDForServe(ctx context.Context, id string) (storageconfig.ResolvedStorage, bool, error)
	ConfigIDForOrgAndMode(ctx context.Context, orgID string, mode string) (string, bool, error)
	// DefaultConfigID returns the org's default storage config id (if any).
	// Used by ResolveWriteTarget to fall back from project override to org default.
	DefaultConfigID(ctx context.Context, orgID string) (string, bool, error)
}

// builtinConfigID is the sentinel persisted on an asset whose bytes landed in the
// built-in default localfs store (no storage_configs row). Serve resolves it back
// to Default. Distinct from "" (legacy, pre-fix rows → fall back to current mode).
const builtinConfigID = "builtin"

// Config configures a Router. Configs/Build are required for routing; Default is
// the built-in localfs store (always usable) returned on any miss/error.
type Config struct {
	Configs resolver
	Default blob.BlobStore                                                 // 内置 localfs 默认，永远可用
	Build   func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) // 由 cmd/studiod 注入
	Logger  *slog.Logger                                                   // nil → slog.Default()
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
	return r.buildCached(orgID, rs), nil
}

// ConfigIDForMode returns the backend-identity token to PERSIST on an asset at
// write time for an (org,mode): the resolved storage_configs.id, or the
// "builtin" sentinel when the (org,mode) resolves to the built-in default store
// (no config row). Worker / cover handlers call this alongside BlobStoreForMode
// so the serve path can later re-resolve EXACTLY this backend by id, independent
// of the org's current storage_mode. NEVER errors-out the caller: on a resolve
// error it returns "builtin" (the safe write-time default the bytes land in when
// resolution fails) so write paths keep working.
func (r *Router) ConfigIDForMode(ctx context.Context, orgID, mode string) (string, error) {
	if r.configs == nil {
		return builtinConfigID, nil
	}
	id, ok, err := r.configs.ConfigIDForOrgAndMode(ctx, orgID, mode)
	if err != nil {
		r.log.Warn("storagerouter: resolve config id failed; persisting builtin sentinel", "org", orgID, "mode", mode, "err", err)
		return builtinConfigID, nil
	}
	if !ok || id == "" {
		return builtinConfigID, nil
	}
	return id, nil
}

// BlobStoreForConfigID resolves the blob store by an asset's persisted backend
// token (the serve path). "builtin" → the built-in Default store; a real config
// id → that EXACTLY (independent of the org's current mode); unknown/disabled id
// or any error → falls back to Default (never returns nil-meaningfully). The ""
// (legacy) token is NOT handled here — the handler routes "" to BlobStoreForMode
// (current-mode fallback) so un-backfilled rows keep working.
func (r *Router) BlobStoreForConfigID(ctx context.Context, orgID, configID string) (blob.BlobStore, error) {
	if configID == builtinConfigID || r.configs == nil || r.build == nil {
		return r.def, nil
	}
	// ResolveByIDForServe (不过滤 enabled)：禁用一个存储配置只阻止新写入，已落在该
	// 后端的历史 asset 必须继续可读——否则禁用会静默回落到 builtin default (无字节→404)。
	rs, ok, err := r.configs.ResolveByIDForServe(ctx, configID)
	if err != nil {
		r.log.Warn("storagerouter: resolve config by id failed; using default store", "org", orgID, "configID", configID, "err", err)
		return r.def, nil
	}
	if !ok {
		r.log.Warn("storagerouter: config id not found; using default store", "org", orgID, "configID", configID)
		return r.def, nil
	}
	return r.buildCached(orgID, rs), nil
}

// ResolveWriteTarget 决定一次写入落到哪个后端 + 要持久化的 config id token。
// 优先级：项目覆盖(projConfigID 非空且 enabled) → org 默认 → builtin。
// 返回 (store, configID)；configID 写进 asset.storage_config_id。
//
// 该函数当前永不返回非 nil 的 error——任何查询失败或 miss 都会静默回落到
// builtin，error 返回值预留给未来需要向调用方传递错误的场景（前向兼容）。
func (r *Router) ResolveWriteTarget(ctx context.Context, orgID, projConfigID string) (blob.BlobStore, string, error) {
	if r.configs == nil || r.build == nil {
		return r.def, builtinConfigID, nil
	}
	if projConfigID != "" {
		rs, ok, err := r.configs.ResolveByID(ctx, projConfigID)
		if err != nil {
			r.log.Warn("storagerouter: resolve project override config failed; falling through to org default",
				"org", orgID, "projConfigID", projConfigID, "err", err)
		} else if ok {
			return r.buildCached(orgID, rs), projConfigID, nil
		}
	}
	id, ok, err := r.configs.DefaultConfigID(ctx, orgID)
	if err != nil {
		r.log.Warn("storagerouter: resolve default config id failed; falling through to builtin",
			"org", orgID, "err", err)
	} else if ok {
		rs, ok2, err2 := r.configs.ResolveByID(ctx, id)
		if err2 != nil {
			r.log.Warn("storagerouter: resolve default config by id failed; falling through to builtin",
				"org", orgID, "defaultConfigID", id, "err", err2)
		} else if ok2 {
			return r.buildCached(orgID, rs), id, nil
		}
	}
	return r.def, builtinConfigID, nil
}

// buildCached builds (or returns a cached) blob store for a resolved config,
// keyed by config identity. On build failure it logs and returns Default.
func (r *Router) buildCached(orgID string, rs storageconfig.ResolvedStorage) blob.BlobStore {
	key := identity(rs)
	// 缓存命中。
	r.mu.RLock()
	if bs, hit := r.cache[key]; hit {
		r.mu.RUnlock()
		return bs
	}
	r.mu.RUnlock()
	// 缓存未命中 → 构造。
	bs, berr := r.build(rs)
	if berr != nil {
		r.log.Warn("storagerouter: build org blob store failed; using default store",
			"org", orgID, "mode", rs.Mode, "bucket", rs.Bucket, "err", berr)
		return r.def
	}
	r.mu.Lock()
	// double-check：并发下别人可能已建好同 key。
	if existing, hit := r.cache[key]; hit {
		r.mu.Unlock()
		return existing
	}
	r.cache[key] = bs
	r.mu.Unlock()
	return bs
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
