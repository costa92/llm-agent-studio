// Package config loads studiod configuration from the environment. LoadFromLookup
// takes an injectable lookup so tests can drive build() deterministically
// (mirrors llm-agent-kb config).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the studiod runtime configuration.
type Config struct {
	HTTPAddr        string
	PGURL           string
	JWTSecret       string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	ShutdownTimeout time.Duration

	Provider string // "deepseek" | "openai" | "ollama" | "fake"
	Model    string
	APIKey   string
	BaseURL  string

	// FakeGen turns on keyless dev/demo mode: buildModel returns a deterministic
	// fake ChatModel and buildGenerator returns a placeholder-PNG MediaGenerator,
	// so a deployment with NO provider API keys still runs the whole pipeline
	// (Run → asset → pending_acceptance → review → library). Enabled by
	// PROVIDER=fake OR STUDIO_FAKE_GEN=1. Never use in production keyed runs.
	FakeGen bool

	Workers          int
	WorkerLease      time.Duration
	WorkerPoll       time.Duration
	WorkerMaxAttempt int
	WorkerBackoff    time.Duration

	PerUserLimit int

	// M3 横切 (spec §12/§15).
	WorkerCallTimeout time.Duration // per-agent/generator-call ctx timeout; MUST be < WorkerLease
	OrgDailyGenQuota  int           // rolling-24h generations per org; 0 = unlimited
	MaxConcurrentGen  int           // global concurrent asset-todo cap; 0 = unlimited
	ReviewPrescreen   bool          // run the ReviewAgent prescreen after generation

	// Per-provider image keys (M3 模型路由): a key registers that provider's
	// catalog models as real generators in the registry. Empty = not registered
	// (org defaults pointing there resolve to the env default generator).
	OpenAIAPIKey     string
	GoogleAPIKey     string
	MinimaxAPIKey    string
	VolcengineAPIKey string

	// M4 异步引擎 (spec §5.6/§9.4).
	PollBackoff        time.Duration // async poll base backoff (env POLL_BACKOFF, 5s)
	MaxPollBackoff     time.Duration // poll backoff cap (env MAX_POLL_BACKOFF, 30s)
	MaxPollAttempts    int           // per-asset poll budget (env MAX_POLL_ATTEMPTS, 60)
	MaxConcurrentVideo int           // video submit-admission + fetch cap; 0 = unlimited
	MaxConcurrentAudio int           // audio submit-admission + fetch cap; 0 = unlimited
	// 双层 submit-admission 上限 (issue #21)：MaxConcurrentVideo/Audio 是跨 org 全局软
	// 兜底 (OOM/容量维度)；下面两个是叠加其上的 per-org 软上限 (noisy-neighbor 公平性)。
	// 任一层达限即 hold submit。0 = 该层不限。
	MaxConcurrentVideoPerOrg int           // per-org video submit-admission cap; 0 = unlimited
	MaxConcurrentAudioPerOrg int           // per-org audio submit-admission cap; 0 = unlimited
	LeaseRenewInterval       time.Duration // heartbeat renewLease period; MUST be < WorkerLease
	VideoFetchMaxBytes       int64         // hard cap on a pulled video/audio body (default 512MB)

	// CachePricingTTL is the background refresh interval for the in-memory
	// pricing cache (env CACHE_PRICING_TTL, 5m). Pricing has no application write
	// path (ops edit via SQL), so it relies on periodic reload rather than
	// LISTEN/NOTIFY. 0 disables refresh (preload-only).
	CachePricingTTL time.Duration

	// BlobDir/BlobSecret/BlobPublic configure the BUILT-IN localfs default store +
	// the single回源 server. 远端对象存储 (s3/oss/cos) 改由 DB-only storage_configs
	// 配置 + StorageRouter 路由 (Phase 3 决策: 删除 env 存储配置)。
	BlobDir    string // localfs root
	BlobSecret string // HMAC secret for localfs signed URLs (falls back to JWTSecret)
	BlobPublic string // public URL prefix for blob回源, default "/api/blob/"

	OTLPEndpoint string
	OTLPProtocol string
	OTLPInsecure bool

	WebDir string // built SPA dir to serve (e.g. "web/dist"); "" = backend-only

	// PlatformAdminEmails 是平台超级管理员的 env 种子名单（PLATFORM_ADMIN_EMAILS，
	// 逗号分隔）。启动时对名单内已注册用户授予平台管理员 (SeedFromEmails)；尚未注册
	// 的，注册时再 top-up (见 studiosvc.Register)。每项已 trim + 转小写以匹配落库形态。
	PlatformAdminEmails []string

	// PublicURL 是控制台对外可达的 base URL（env STUDIO_PUBLIC_URL，如
	// https://studio.example.com）。目前仅用于 run 失败告警邮件里的控制台链接；
	// 留空则邮件不带链接（既有部署无外链约定，纯文本定位信息足够）。
	PublicURL string

	// SMTP settings for email verification
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string
}

// Load reads from the process environment.
func Load() (Config, error) { return LoadFromLookup(os.LookupEnv) }

// LoadFromLookup builds a Config from an injectable lookup.
func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	get := func(k, def string) string {
		if v, ok := lookup(k); ok && v != "" {
			return v
		}
		return def
	}
	var errs []string
	cfg := Config{
		HTTPAddr:         get("HTTP_ADDR", ":8083"),
		PGURL:            get("PG_URL", ""),
		JWTSecret:        get("JWT_SECRET", ""),
		AccessTTL:        durOf("ACCESS_TTL", get("ACCESS_TTL", "15m"), &errs),
		RefreshTTL:       durOf("REFRESH_TTL", get("REFRESH_TTL", "720h"), &errs),
		ShutdownTimeout:  durOf("SHUTDOWN_TIMEOUT", get("SHUTDOWN_TIMEOUT", "20s"), &errs),
		Provider:         get("PROVIDER", "deepseek"),
		Model:            get("MODEL", "deepseek-chat"),
		APIKey:           get("API_KEY", ""),
		BaseURL:          get("BASE_URL", ""),
		FakeGen:          get("PROVIDER", "deepseek") == "fake" || get("STUDIO_FAKE_GEN", "") == "1",
		Workers:          intOf("WORKERS", get("WORKERS", "2"), &errs),
		WorkerLease:      durOf("WORKER_LEASE", get("WORKER_LEASE", "120s"), &errs),
		WorkerPoll:       durOf("WORKER_POLL", get("WORKER_POLL", "1s"), &errs),
		WorkerMaxAttempt: intOf("WORKER_MAX_ATTEMPTS", get("WORKER_MAX_ATTEMPTS", "3"), &errs),
		WorkerBackoff:    durOf("WORKER_BACKOFF", get("WORKER_BACKOFF", "2s"), &errs),
		PerUserLimit:     intOf("PER_USER_LIMIT", get("PER_USER_LIMIT", "120"), &errs),

		WorkerCallTimeout: durOf("WORKER_CALL_TIMEOUT", get("WORKER_CALL_TIMEOUT", "90s"), &errs),
		OrgDailyGenQuota:  intOf("ORG_DAILY_GEN_QUOTA", get("ORG_DAILY_GEN_QUOTA", "0"), &errs),
		MaxConcurrentGen:  intOf("MAX_CONCURRENT_GENERATIONS", get("MAX_CONCURRENT_GENERATIONS", "0"), &errs),
		ReviewPrescreen:   get("REVIEW_PRESCREEN", "true") == "true",
		OpenAIAPIKey:      get("OPENAI_API_KEY", ""),
		GoogleAPIKey:      get("GOOGLE_API_KEY", ""),
		MinimaxAPIKey:     get("MINIMAX_API_KEY", ""),
		VolcengineAPIKey:  get("VOLCENGINE_API_KEY", ""),

		PollBackoff:        durOf("POLL_BACKOFF", get("POLL_BACKOFF", "5s"), &errs),
		MaxPollBackoff:     durOf("MAX_POLL_BACKOFF", get("MAX_POLL_BACKOFF", "30s"), &errs),
		MaxPollAttempts:    intOf("MAX_POLL_ATTEMPTS", get("MAX_POLL_ATTEMPTS", "60"), &errs),
		MaxConcurrentVideo: intOf("MAX_CONCURRENT_VIDEO", get("MAX_CONCURRENT_VIDEO", "0"), &errs),
		MaxConcurrentAudio: intOf("MAX_CONCURRENT_AUDIO", get("MAX_CONCURRENT_AUDIO", "0"), &errs),

		MaxConcurrentVideoPerOrg: intOf("MAX_CONCURRENT_VIDEO_PER_ORG", get("MAX_CONCURRENT_VIDEO_PER_ORG", "0"), &errs),
		MaxConcurrentAudioPerOrg: intOf("MAX_CONCURRENT_AUDIO_PER_ORG", get("MAX_CONCURRENT_AUDIO_PER_ORG", "0"), &errs),
		LeaseRenewInterval:       durOf("LEASE_RENEW_INTERVAL", get("LEASE_RENEW_INTERVAL", "40s"), &errs),
		CachePricingTTL:          durOf("CACHE_PRICING_TTL", get("CACHE_PRICING_TTL", "5m"), &errs),
		VideoFetchMaxBytes:       int64(intOf("VIDEO_FETCH_MAX_BYTES", get("VIDEO_FETCH_MAX_BYTES", "536870912"), &errs)),

		BlobDir:    get("BLOB_DIR", "./blobdata"),
		BlobSecret: get("BLOB_SECRET", ""),
		BlobPublic: get("BLOB_PUBLIC_PREFIX", "/api/blob/"),

		OTLPEndpoint: get("OTLP_ENDPOINT", ""),
		OTLPProtocol: get("OTLP_PROTOCOL", ""),
		OTLPInsecure: get("OTLP_INSECURE", "true") == "true",
		WebDir:       get("WEB_DIR", ""),

		PlatformAdminEmails: splitEmails(get("PLATFORM_ADMIN_EMAILS", "")),

		PublicURL: get("STUDIO_PUBLIC_URL", ""),

		SMTPHost: get("SMTP_HOST", ""),
		SMTPPort: intOf("SMTP_PORT", get("SMTP_PORT", "587"), &errs),
		SMTPUser: get("SMTP_USER", ""),
		SMTPPass: get("SMTP_PASS", ""),
		SMTPFrom: get("SMTP_FROM", "no-reply@studio.com"),
	}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: invalid values: %s", strings.Join(errs, "; "))
	}
	if cfg.WorkerCallTimeout > 0 && cfg.WorkerCallTimeout >= cfg.WorkerLease {
		return Config{}, fmt.Errorf("config: WORKER_CALL_TIMEOUT (%s) must be strictly shorter than WORKER_LEASE (%s)",
			cfg.WorkerCallTimeout, cfg.WorkerLease)
	}
	if cfg.LeaseRenewInterval > 0 && cfg.LeaseRenewInterval >= cfg.WorkerLease {
		return Config{}, fmt.Errorf("config: LEASE_RENEW_INTERVAL (%s) must be strictly shorter than WORKER_LEASE (%s)",
			cfg.LeaseRenewInterval, cfg.WorkerLease)
	}
	if cfg.PGURL == "" {
		return Config{}, fmt.Errorf("config: PG_URL is required")
	}
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("config: JWT_SECRET is required")
	}
	if cfg.BlobSecret == "" {
		cfg.BlobSecret = cfg.JWTSecret
	}
	return cfg, nil
}

// splitEmails 解析逗号分隔的邮箱名单：trim + 转小写，丢弃空项。空入参 → nil。
func splitEmails(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		e := strings.ToLower(strings.TrimSpace(part))
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// durOf parses a duration, recording a load error naming the env key on
// malformed input (M1 carry: parse errors must not silently become 0).
func durOf(key, s string, errs *[]string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s=%q: %v", key, s, err))
		return 0
	}
	return d
}

// intOf parses an int, recording a load error naming the env key.
func intOf(key, s string, errs *[]string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s=%q: %v", key, s, err))
		return 0
	}
	return n
}
