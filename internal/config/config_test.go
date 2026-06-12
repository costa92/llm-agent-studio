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

func TestLoadOSSAndCOS(t *testing.T) {
	cfg, err := LoadFromLookup(func(k string) (string, bool) {
		m := map[string]string{
			"PG_URL": "postgres://x", "JWT_SECRET": "s",
			"OSS_ENDPOINT": "oss-cn-hangzhou.aliyuncs.com", "OSS_BUCKET": "b",
			"OSS_ACCESS_KEY_ID": "id", "OSS_ACCESS_KEY_SECRET": "sec",
			"COS_REGION": "ap-guangzhou", "COS_BUCKET": "name-123",
			"COS_SECRET_ID": "sid", "COS_SECRET_KEY": "sk",
		}
		v, ok := m[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.OSSEndpoint != "oss-cn-hangzhou.aliyuncs.com" || cfg.OSSBucket != "b" ||
		cfg.OSSAccessKeyID != "id" || cfg.OSSAccessKeySecret != "sec" {
		t.Fatalf("OSS not plumbed: %+v", cfg)
	}
	if cfg.COSBucket != "name-123" || cfg.COSSecretID != "sid" || cfg.COSSecretKey != "sk" {
		t.Fatalf("COS not plumbed: %+v", cfg)
	}
}

func TestCOSEndpointHost(t *testing.T) {
	// Derived from region when no explicit endpoint.
	if got := (Config{COSRegion: "ap-guangzhou"}).COSEndpointHost(); got != "cos.ap-guangzhou.myqcloud.com" {
		t.Fatalf("derived = %q", got)
	}
	// Explicit endpoint overrides derivation.
	if got := (Config{COSRegion: "ap-guangzhou", COSEndpoint: "cos.internal:443"}).COSEndpointHost(); got != "cos.internal:443" {
		t.Fatalf("override = %q", got)
	}
	// Neither set → empty (New will reject downstream).
	if got := (Config{}).COSEndpointHost(); got != "" {
		t.Fatalf("empty = %q", got)
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
