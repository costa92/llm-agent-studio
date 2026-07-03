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
	if cfg.WebDir != "" {
		t.Fatalf("WebDir default = %q, want empty (backend-only)", cfg.WebDir)
	}
}

func TestLoadFakeGenMode(t *testing.T) {
	base := func(extra map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if v, ok := extra[k]; ok {
				return v, true
			}
			switch k {
			case "PG_URL":
				return "postgres://x", true
			case "JWT_SECRET":
				return "s", true
			}
			return "", false
		}
	}
	// Default: fake mode OFF.
	cfg, err := LoadFromLookup(base(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FakeGen {
		t.Fatalf("FakeGen must default off")
	}
	// PROVIDER=fake turns it on.
	cfg, err = LoadFromLookup(base(map[string]string{"PROVIDER": "fake"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.FakeGen {
		t.Fatalf("PROVIDER=fake must enable FakeGen")
	}
	// STUDIO_FAKE_GEN=1 also turns it on (even with a real provider name).
	cfg, err = LoadFromLookup(base(map[string]string{"STUDIO_FAKE_GEN": "1"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.FakeGen {
		t.Fatalf("STUDIO_FAKE_GEN=1 must enable FakeGen")
	}
}

func TestLoadExprFlags(t *testing.T) {
	base := func(extra map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if v, ok := extra[k]; ok {
				return v, true
			}
			switch k {
			case "PG_URL":
				return "postgres://x", true
			case "JWT_SECRET":
				return "s", true
			}
			return "", false
		}
	}
	// Default (P3e flip): ExprChannel ON, ExprParity OFF. The expr engine is the
	// live custom-node value channel by default.
	cfg, err := LoadFromLookup(base(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ExprParity {
		t.Fatalf("ExprParity must default off, got %v", cfg.ExprParity)
	}
	if !cfg.ExprChannel {
		t.Fatalf("ExprChannel must default ON after P3e flip, got %v", cfg.ExprChannel)
	}
	// STUDIO_EXPR_CHANNEL=0 is the REVERSIBLE kill-switch (revert to legacy resolver).
	cfg, err = LoadFromLookup(base(map[string]string{"STUDIO_EXPR_CHANNEL": "0"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ExprChannel {
		t.Fatalf("STUDIO_EXPR_CHANNEL=0 should disable ExprChannel (kill-switch)")
	}
	// STUDIO_EXPR_PARITY=1 enables the shadow probe.
	cfg, err = LoadFromLookup(base(map[string]string{"STUDIO_EXPR_PARITY": "1"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.ExprParity {
		t.Fatalf("STUDIO_EXPR_PARITY=1 should set ExprParity, got %v", cfg.ExprParity)
	}
	// STUDIO_EXPR_CHANNEL=1 keeps the live channel on (explicit).
	cfg, err = LoadFromLookup(base(map[string]string{"STUDIO_EXPR_CHANNEL": "1"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.ExprChannel {
		t.Fatalf("STUDIO_EXPR_CHANNEL=1 should enable ExprChannel")
	}
	// ItemsCanonical (items cut-over PR-A) defaults OFF until the PR-B soak.
	if cfg.ItemsCanonical {
		t.Fatalf("ItemsCanonical must default off, got %v", cfg.ItemsCanonical)
	}
	// STUDIO_ITEMS_CANONICAL=1 enables the items canonical input channel.
	cfg, err = LoadFromLookup(base(map[string]string{"STUDIO_ITEMS_CANONICAL": "1"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.ItemsCanonical {
		t.Fatalf("STUDIO_ITEMS_CANONICAL=1 should set ItemsCanonical")
	}
	// Fail-closed combination (spec items-cutover §3): items canonical structurally
	// depends on the expr channel; =1 with the expr kill-switch thrown must ERROR,
	// never silently degrade.
	if _, err = LoadFromLookup(base(map[string]string{
		"STUDIO_ITEMS_CANONICAL": "1",
		"STUDIO_EXPR_CHANNEL":    "0",
	})); err == nil {
		t.Fatalf("STUDIO_ITEMS_CANONICAL=1 + STUDIO_EXPR_CHANNEL=0 must fail config.Load (fail-closed)")
	}
}

func TestLoadWebDir(t *testing.T) {
	cfg, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		case "WEB_DIR":
			return "web/dist", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.WebDir != "web/dist" {
		t.Fatalf("WebDir = %q, want web/dist", cfg.WebDir)
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
	if cfg.MaxConcurrentVideoPerOrg != 0 || cfg.MaxConcurrentAudioPerOrg != 0 {
		t.Fatalf("per-org concurrency caps should default 0 (disabled): %+v", cfg)
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

// TestLoadPerOrgConcurrencyCaps verifies the issue #21 dual-layer knobs parse
// independently of the global caps.
func TestLoadPerOrgConcurrencyCaps(t *testing.T) {
	cfg, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		case "MAX_CONCURRENT_VIDEO_PER_ORG":
			return "2", true
		case "MAX_CONCURRENT_AUDIO_PER_ORG":
			return "3", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MaxConcurrentVideoPerOrg != 2 || cfg.MaxConcurrentAudioPerOrg != 3 {
		t.Fatalf("per-org caps = %d/%d, want 2/3", cfg.MaxConcurrentVideoPerOrg, cfg.MaxConcurrentAudioPerOrg)
	}
	// Global caps stay at their default (0) — the layers are independent.
	if cfg.MaxConcurrentVideo != 0 || cfg.MaxConcurrentAudio != 0 {
		t.Fatalf("global caps should remain 0 when only per-org set: %d/%d", cfg.MaxConcurrentVideo, cfg.MaxConcurrentAudio)
	}
}
