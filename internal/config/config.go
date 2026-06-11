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

	Provider string // "deepseek" | "openai" | "ollama"
	Model    string
	APIKey   string
	BaseURL  string

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
	LeaseRenewInterval time.Duration // heartbeat renewLease period; MUST be < WorkerLease
	VideoFetchMaxBytes int64         // hard cap on a pulled video/audio body (default 512MB)

	// M4 video/audio provider keys (key-gated real-adapter skeletons, spec §8).
	RunwayAPIKey string
	KlingAPIKey  string
	TTSAPIKey    string
	// Veo reuses GoogleAPIKey (already present, spec §8.2).

	BlobMode    string // "localfs" | "s3"
	BlobDir     string // localfs root
	BlobSecret  string // HMAC secret for localfs signed URLs (falls back to JWTSecret)
	BlobPublic  string // public URL prefix for blob回源, default "/api/blob/"
	S3Endpoint  string
	S3Bucket    string
	S3Region    string
	S3AccessKey string
	S3SecretKey string
	S3UseSSL    bool

	OTLPEndpoint string
	OTLPProtocol string
	OTLPInsecure bool
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
		LeaseRenewInterval: durOf("LEASE_RENEW_INTERVAL", get("LEASE_RENEW_INTERVAL", "40s"), &errs),
		VideoFetchMaxBytes: int64(intOf("VIDEO_FETCH_MAX_BYTES", get("VIDEO_FETCH_MAX_BYTES", "536870912"), &errs)),
		RunwayAPIKey:       get("RUNWAY_API_KEY", ""),
		KlingAPIKey:        get("KLING_API_KEY", ""),
		TTSAPIKey:          get("TTS_API_KEY", ""),

		BlobMode:         get("BLOB_MODE", "localfs"),
		BlobDir:          get("BLOB_DIR", "./blobdata"),
		BlobSecret:       get("BLOB_SECRET", ""),
		BlobPublic:       get("BLOB_PUBLIC_PREFIX", "/api/blob/"),
		S3Endpoint:       get("S3_ENDPOINT", ""),
		S3Bucket:         get("S3_BUCKET", ""),
		S3Region:         get("S3_REGION", ""),
		S3AccessKey:      get("S3_ACCESS_KEY", ""),
		S3SecretKey:      get("S3_SECRET_KEY", ""),
		S3UseSSL:         get("S3_USE_SSL", "true") == "true",
		OTLPEndpoint:     get("OTLP_ENDPOINT", ""),
		OTLPProtocol:     get("OTLP_PROTOCOL", ""),
		OTLPInsecure:     get("OTLP_INSECURE", "true") == "true",
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
