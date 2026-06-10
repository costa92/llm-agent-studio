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
