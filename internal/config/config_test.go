package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresPGAndJWT(t *testing.T) {
	_, err := LoadFromLookup(func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatalf("want error when PG_URL/JWT_SECRET missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTPAddr == "" || cfg.Workers <= 0 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadRejectsMalformedNumbers(t *testing.T) {
	// M1 carry: WORKERS=two used to silently become 0 workers (intOf swallowed
	// the error). M3: parse errors must fail Load loudly, naming the key.
	_, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		case "WORKERS":
			return "two", true
		}
		return "", false
	})
	if err == nil {
		t.Fatalf("want error for WORKERS=two")
	}
	if got := err.Error(); !strings.Contains(got, "WORKERS") {
		t.Fatalf("error should name the offending key, got %q", got)
	}
}

func TestLoadRejectsMalformedDuration(t *testing.T) {
	_, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		case "WORKER_LEASE":
			return "fast", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "WORKER_LEASE") {
		t.Fatalf("want WORKER_LEASE parse error, got %v", err)
	}
}

func TestLoadRejectsCallTimeoutNotBelowLease(t *testing.T) {
	// The per-LLM-call timeout mitigates the missing lease renewal (M1 carry):
	// it MUST be strictly shorter than the lease or a slow call can outlive the
	// lease and get double-claimed.
	_, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		case "WORKER_LEASE":
			return "60s", true
		case "WORKER_CALL_TIMEOUT":
			return "60s", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "WORKER_CALL_TIMEOUT") {
		t.Fatalf("want WORKER_CALL_TIMEOUT >= lease error, got %v", err)
	}
}

func TestLoadM3Defaults(t *testing.T) {
	cfg, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.WorkerCallTimeout != 90*time.Second {
		t.Fatalf("WorkerCallTimeout default = %v, want 90s", cfg.WorkerCallTimeout)
	}
	if cfg.OrgDailyGenQuota != 0 || cfg.MaxConcurrentGen != 0 {
		t.Fatalf("quota/concurrency defaults should be 0 (disabled): %+v", cfg)
	}
	if !cfg.ReviewPrescreen {
		t.Fatalf("ReviewPrescreen should default true")
	}
}

func TestLoadM4Defaults(t *testing.T) {
	cfg, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PollBackoff != 5*time.Second || cfg.MaxPollBackoff != 30*time.Second {
		t.Fatalf("poll backoff defaults = %v/%v, want 5s/30s", cfg.PollBackoff, cfg.MaxPollBackoff)
	}
	if cfg.MaxPollAttempts != 60 {
		t.Fatalf("MaxPollAttempts default = %d, want 60", cfg.MaxPollAttempts)
	}
	if cfg.MaxConcurrentVideo != 0 || cfg.MaxConcurrentAudio != 0 {
		t.Fatalf("concurrency caps should default 0 (disabled): %+v", cfg)
	}
	if cfg.LeaseRenewInterval <= 0 || cfg.LeaseRenewInterval >= cfg.WorkerLease {
		t.Fatalf("LeaseRenewInterval = %v must be >0 and < WorkerLease %v", cfg.LeaseRenewInterval, cfg.WorkerLease)
	}
	if cfg.VideoFetchMaxBytes != 512<<20 {
		t.Fatalf("VideoFetchMaxBytes default = %d, want 512MB", cfg.VideoFetchMaxBytes)
	}
}

func TestLoadM4RejectsMalformedPollKnob(t *testing.T) {
	_, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		case "MAX_POLL_ATTEMPTS":
			return "lots", true
		}
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "MAX_POLL_ATTEMPTS") {
		t.Fatalf("want MAX_POLL_ATTEMPTS parse error, got %v", err)
	}
}
