// Package config loads studiod configuration from the environment. LoadFromLookup
// takes an injectable lookup so tests can drive build() deterministically
// (mirrors llm-agent-kb config).
package config

import (
	"fmt"
	"os"
	"strconv"
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
	cfg := Config{
		HTTPAddr:         get("HTTP_ADDR", ":8083"),
		PGURL:            get("PG_URL", ""),
		JWTSecret:        get("JWT_SECRET", ""),
		AccessTTL:        durOf(get("ACCESS_TTL", "15m")),
		RefreshTTL:       durOf(get("REFRESH_TTL", "720h")),
		ShutdownTimeout:  durOf(get("SHUTDOWN_TIMEOUT", "20s")),
		Provider:         get("PROVIDER", "deepseek"),
		Model:            get("MODEL", "deepseek-chat"),
		APIKey:           get("API_KEY", ""),
		BaseURL:          get("BASE_URL", ""),
		Workers:          intOf(get("WORKERS", "2")),
		WorkerLease:      durOf(get("WORKER_LEASE", "120s")),
		WorkerPoll:       durOf(get("WORKER_POLL", "1s")),
		WorkerMaxAttempt: intOf(get("WORKER_MAX_ATTEMPTS", "3")),
		WorkerBackoff:    durOf(get("WORKER_BACKOFF", "2s")),
		PerUserLimit:     intOf(get("PER_USER_LIMIT", "120")),
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

func durOf(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func intOf(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
