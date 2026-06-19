package storagerouter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/storageconfig"
)

// stubBlob 是记录身份的 blob.BlobStore 桩 (按 name 区分实例)。
type stubBlob struct{ name string }

func (s *stubBlob) Put(context.Context, string, io.Reader, string) error { return nil }
func (s *stubBlob) SignedURL(context.Context, string, time.Duration) (string, error) {
	return s.name, nil
}
func (s *stubBlob) Delete(context.Context, string) error { return nil }

// fakeResolver fakes storageconfig.Store.ResolveForOrg。
type fakeResolver struct {
	rs  storageconfig.ResolvedStorage
	ok  bool
	err error
}

func (f fakeResolver) ResolveForOrg(context.Context, string) (storageconfig.ResolvedStorage, bool, error) {
	return f.rs, f.ok, f.err
}

func (f fakeResolver) ResolveForOrgAndMode(context.Context, string, string) (storageconfig.ResolvedStorage, bool, error) {
	return f.rs, f.ok, f.err
}

func (f fakeResolver) ResolveByID(context.Context, string) (storageconfig.ResolvedStorage, bool, error) {
	return f.rs, f.ok, f.err
}

func (f fakeResolver) ResolveByIDForServe(context.Context, string) (storageconfig.ResolvedStorage, bool, error) {
	return f.rs, f.ok, f.err
}

func (f fakeResolver) ConfigIDForOrgAndMode(context.Context, string, string) (string, bool, error) {
	return "", f.ok, f.err
}
func (f fakeResolver) DefaultConfigID(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func TestBlobStoreForBuildsFromConfig(t *testing.T) {
	def := &stubBlob{name: "default"}
	built := &stubBlob{name: "org"}
	r := New(Config{
		Configs: fakeResolver{rs: storageconfig.ResolvedStorage{Mode: "s3", Bucket: "b"}, ok: true},
		Default: def,
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			if rs.Mode != "s3" || rs.Bucket != "b" {
				t.Fatalf("Build got wrong rs: %+v", rs)
			}
			return built, nil
		},
	})
	got, err := r.BlobStoreFor(context.Background(), "org")
	if err != nil || got != built {
		t.Fatalf("want built store, got %v err=%v", got, err)
	}
}

func TestBlobStoreForFallsBackWhenNotOK(t *testing.T) {
	def := &stubBlob{name: "default"}
	r := New(Config{
		Configs: fakeResolver{ok: false},
		Default: def,
		Build: func(storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			t.Fatal("Build must not run on !ok")
			return nil, nil
		},
	})
	got, err := r.BlobStoreFor(context.Background(), "org")
	if err != nil || got != def {
		t.Fatalf("want default on !ok, got %v err=%v", got, err)
	}
}

func TestBlobStoreForFallsBackOnResolveError(t *testing.T) {
	def := &stubBlob{name: "default"}
	var buf bytes.Buffer
	r := New(Config{
		Configs: fakeResolver{err: errors.New("db down")},
		Default: def,
		Build: func(storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			t.Fatal("Build must not run on resolve error")
			return nil, nil
		},
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	got, err := r.BlobStoreFor(context.Background(), "org")
	if err != nil || got != def {
		t.Fatalf("want default on resolve error, got %v err=%v", got, err)
	}
}

func TestBlobStoreForFallsBackOnBuildError(t *testing.T) {
	def := &stubBlob{name: "default"}
	var buf bytes.Buffer
	r := New(Config{
		Configs: fakeResolver{rs: storageconfig.ResolvedStorage{Mode: "s3", Bucket: "b"}, ok: true},
		Default: def,
		Build:   func(storageconfig.ResolvedStorage) (blob.BlobStore, error) { return nil, errors.New("boom") },
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
	})
	got, err := r.BlobStoreFor(context.Background(), "org")
	if err != nil || got != def {
		t.Fatalf("want default on build error, got %v err=%v", got, err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("build")) {
		t.Fatalf("want a warning logged on build failure, got: %s", buf.String())
	}
}

func TestBlobStoreForCachesByIdentity(t *testing.T) {
	def := &stubBlob{name: "default"}
	var mu sync.Mutex
	var builds int
	r := New(Config{
		Configs: fakeResolver{rs: storageconfig.ResolvedStorage{Mode: "s3", Bucket: "b", SecretKey: "sek"}, ok: true},
		Default: def,
		Build: func(storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			mu.Lock()
			builds++
			mu.Unlock()
			return &stubBlob{name: "built"}, nil
		},
	})
	for i := 0; i < 5; i++ {
		if _, err := r.BlobStoreFor(context.Background(), "org"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if builds != 1 {
		t.Fatalf("Build should run once per identity, ran %d times", builds)
	}
}

func TestBlobStoreForRebuildsOnIdentityChange(t *testing.T) {
	def := &stubBlob{name: "default"}
	var builds int
	// resolver 返回随 orgID 变化的配置：两个不同 org → 两次 Build。
	r := New(Config{
		Configs: orgVaryingResolver{},
		Default: def,
		Build: func(storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			builds++
			return &stubBlob{name: "built"}, nil
		},
	})
	ctx := context.Background()
	_, _ = r.BlobStoreFor(ctx, "org-a") // bucket=org-a
	_, _ = r.BlobStoreFor(ctx, "org-a") // cache hit
	_, _ = r.BlobStoreFor(ctx, "org-b") // bucket=org-b → new identity
	if builds != 2 {
		t.Fatalf("distinct identities should rebuild: want 2 builds, got %d", builds)
	}
}

// orgVaryingResolver 把 orgID 当 bucket，制造不同配置身份。
type orgVaryingResolver struct{}

func (orgVaryingResolver) ResolveForOrg(_ context.Context, orgID string) (storageconfig.ResolvedStorage, bool, error) {
	return storageconfig.ResolvedStorage{Mode: "s3", Bucket: orgID}, true, nil
}

func (orgVaryingResolver) ResolveForOrgAndMode(_ context.Context, orgID, mode string) (storageconfig.ResolvedStorage, bool, error) {
	return storageconfig.ResolvedStorage{Mode: "s3", Bucket: orgID}, true, nil
}

func (orgVaryingResolver) ResolveByID(_ context.Context, id string) (storageconfig.ResolvedStorage, bool, error) {
	return storageconfig.ResolvedStorage{Mode: "s3", Bucket: id}, true, nil
}

func (orgVaryingResolver) ResolveByIDForServe(_ context.Context, id string) (storageconfig.ResolvedStorage, bool, error) {
	return storageconfig.ResolvedStorage{Mode: "s3", Bucket: id}, true, nil
}

func (orgVaryingResolver) ConfigIDForOrgAndMode(_ context.Context, orgID, mode string) (string, bool, error) {
	return orgID, true, nil
}
func (orgVaryingResolver) DefaultConfigID(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func TestBlobStoreForNeverNilWhenDefaultSet(t *testing.T) {
	def := &stubBlob{name: "default"}
	r := New(Config{Configs: fakeResolver{ok: false}, Default: def})
	got, err := r.BlobStoreFor(context.Background(), "org")
	if err != nil || got != def {
		t.Fatalf("want default, got %v err=%v", got, err)
	}
}

// idResolver fakes the by-id + config-id resolution surface the serve/write path
// needs. Keyed by config id so a single fake covers both ResolveByID and
// ConfigIDForOrgAndMode independently of the org's current mode.
type idResolver struct {
	byID    map[string]storageconfig.ResolvedStorage
	idByOrg map[string]string // key: orgID+"|"+mode
	// byIDServe models the serve-path (enabled-agnostic) resolution. If set, an id
	// here resolves for serve even when absent from byID (i.e. a disabled config).
	byIDServe map[string]storageconfig.ResolvedStorage
}

func (r idResolver) ResolveForOrg(context.Context, string) (storageconfig.ResolvedStorage, bool, error) {
	return storageconfig.ResolvedStorage{}, false, nil
}
func (r idResolver) ResolveForOrgAndMode(_ context.Context, orgID, mode string) (storageconfig.ResolvedStorage, bool, error) {
	id, ok := r.idByOrg[orgID+"|"+mode]
	if !ok {
		return storageconfig.ResolvedStorage{}, false, nil
	}
	rs, ok := r.byID[id]
	return rs, ok, nil
}
func (r idResolver) ResolveByID(_ context.Context, id string) (storageconfig.ResolvedStorage, bool, error) {
	rs, ok := r.byID[id]
	return rs, ok, nil
}
func (r idResolver) ResolveByIDForServe(_ context.Context, id string) (storageconfig.ResolvedStorage, bool, error) {
	if rs, ok := r.byIDServe[id]; ok {
		return rs, true, nil
	}
	rs, ok := r.byID[id]
	return rs, ok, nil
}
func (r idResolver) ConfigIDForOrgAndMode(_ context.Context, orgID, mode string) (string, bool, error) {
	id, ok := r.idByOrg[orgID+"|"+mode]
	return id, ok, nil
}
func (r idResolver) DefaultConfigID(_ context.Context, orgID string) (string, bool, error) {
	id, ok := r.idByOrg["default|"+orgID]
	return id, ok, nil
}

func TestConfigIDForMode(t *testing.T) {
	r := New(Config{
		Configs: idResolver{
			byID:    map[string]storageconfig.ResolvedStorage{"cfg-s3": {Mode: "s3", Bucket: "b"}},
			idByOrg: map[string]string{"org|s3": "cfg-s3"},
		},
		Default: &stubBlob{name: "default"},
		Build:   func(storageconfig.ResolvedStorage) (blob.BlobStore, error) { return &stubBlob{name: "built"}, nil },
	})
	ctx := context.Background()
	// configured mode → returns the config id.
	if id, err := r.ConfigIDForMode(ctx, "org", "s3"); err != nil || id != "cfg-s3" {
		t.Fatalf("configured mode: id=%q err=%v", id, err)
	}
	// no config row → "builtin" sentinel.
	if id, err := r.ConfigIDForMode(ctx, "org", "localfs"); err != nil || id != "builtin" {
		t.Fatalf("builtin sentinel: id=%q err=%v", id, err)
	}
}

func TestBlobStoreForConfigID(t *testing.T) {
	def := &stubBlob{name: "default"}
	built := &stubBlob{name: "built-s3"}
	r := New(Config{
		Configs: idResolver{
			byID:    map[string]storageconfig.ResolvedStorage{"cfg-s3": {Mode: "s3", Bucket: "b"}},
			idByOrg: map[string]string{"org|s3": "cfg-s3"},
		},
		Default: def,
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			if rs.Mode != "s3" {
				t.Fatalf("build got %+v", rs)
			}
			return built, nil
		},
	})
	ctx := context.Background()
	// "builtin" → the Default store.
	if bs, err := r.BlobStoreForConfigID(ctx, "org", "builtin"); err != nil || bs != def {
		t.Fatalf("builtin token: bs=%v err=%v", bs, err)
	}
	// real config id → its built store (independent of org current mode).
	if bs, err := r.BlobStoreForConfigID(ctx, "org", "cfg-s3"); err != nil || bs != built {
		t.Fatalf("config id token: bs=%v err=%v", bs, err)
	}
	// unknown id → falls back to Default (never nil-meaningfully).
	if bs, err := r.BlobStoreForConfigID(ctx, "org", "nope"); err != nil || bs != def {
		t.Fatalf("unknown id should fall back to default: bs=%v err=%v", bs, err)
	}
}

// TestBlobStoreForConfigID_ServesDisabledConfig is the regression for the
// disable-breaks-serve bug: a DISABLED config (ResolveByID → ok=false, here
// modeled by living only in byIDServe, not byID) must still serve historical
// assets from its real backend — NOT silently fall back to the builtin Default
// store (which lacks the bytes → 404). The serve path resolves via
// ResolveByIDForServe (enabled-agnostic); disable only stops new writes.
func TestBlobStoreForConfigID_ServesDisabledConfig(t *testing.T) {
	def := &stubBlob{name: "default"}
	built := &stubBlob{name: "built-s3"}
	r := New(Config{
		Configs: idResolver{
			// disabled: absent from byID (write/enabled view), present for serve.
			byIDServe: map[string]storageconfig.ResolvedStorage{"cfg-disabled": {Mode: "s3", Bucket: "b"}},
		},
		Default: def,
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			if rs.Mode != "s3" {
				t.Fatalf("build got %+v", rs)
			}
			return built, nil
		},
	})
	ctx := context.Background()
	bs, err := r.BlobStoreForConfigID(ctx, "org", "cfg-disabled")
	if err != nil || bs != built {
		t.Fatalf("disabled config must serve from its real backend, not default: bs=%v err=%v", bs, err)
	}
}

// TestServeIndependentOfCurrentMode is the key regression: an asset written under
// config X must resolve to X by its stored config id even after the org switches
// to a different mode Y. Resolution by config id is independent of current mode.
func TestServeIndependentOfCurrentMode(t *testing.T) {
	storeX := &stubBlob{name: "backend-X"}
	storeY := &stubBlob{name: "backend-Y"}
	r := New(Config{
		Configs: idResolver{
			byID: map[string]storageconfig.ResolvedStorage{
				"cfg-X": {Mode: "s3", Bucket: "X"},
				"cfg-Y": {Mode: "oss", Bucket: "Y"},
			},
			// org has SWITCHED to oss/cfg-Y now (current mode).
			idByOrg: map[string]string{"org|oss": "cfg-Y", "org|s3": "cfg-X"},
		},
		Default: &stubBlob{name: "default"},
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			switch rs.Bucket {
			case "X":
				return storeX, nil
			case "Y":
				return storeY, nil
			}
			return nil, errors.New("unexpected")
		},
	})
	ctx := context.Background()
	// Asset was written under cfg-X. Even though current mode is oss (cfg-Y),
	// serving by the asset's stored config id must pick backend X.
	bs, err := r.BlobStoreForConfigID(ctx, "org", "cfg-X")
	if err != nil || bs != storeX {
		t.Fatalf("serve must pick the write-time backend X, got %v err=%v", bs, err)
	}
	// Sanity: resolving by current mode would (wrongly, for a historical asset) pick Y.
	byMode, _ := r.BlobStoreForMode(ctx, "org", "oss")
	if byMode != storeY {
		t.Fatalf("current mode resolves to Y as expected, got %v", byMode)
	}
}

func TestResolveWriteTarget_ProjectOverrideWinsOverDefault(t *testing.T) {
	def := &stubBlob{name: "default"}
	r := New(Config{
		Configs: idResolver{
			byID: map[string]storageconfig.ResolvedStorage{
				"cfgX": {Mode: "s3", Bucket: "X"},
				"cfgD": {Mode: "s3", Bucket: "D"},
			},
			idByOrg: map[string]string{"default|org1": "cfgD"},
		},
		Default: def,
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			return &stubBlob{name: rs.Bucket}, nil
		},
	})
	bs, id, err := r.ResolveWriteTarget(context.Background(), "org1", "cfgX")
	if err != nil || id != "cfgX" {
		t.Fatalf("override: id=%q err=%v want cfgX", id, err)
	}
	url, _ := bs.SignedURL(context.Background(), "", 0)
	if url != "X" {
		t.Fatalf("override: store bucket=%q want X", url)
	}
}

func TestResolveWriteTarget_FallsBackToDefault(t *testing.T) {
	def := &stubBlob{name: "default"}
	r := New(Config{
		Configs: idResolver{
			byID:    map[string]storageconfig.ResolvedStorage{"cfgD": {Mode: "s3", Bucket: "D"}},
			idByOrg: map[string]string{"default|org1": "cfgD"},
		},
		Default: def,
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			return &stubBlob{name: rs.Bucket}, nil
		},
	})
	bs, id, err := r.ResolveWriteTarget(context.Background(), "org1", "")
	if err != nil || id != "cfgD" {
		t.Fatalf("default: id=%q err=%v want cfgD", id, err)
	}
	url, _ := bs.SignedURL(context.Background(), "", 0)
	if url != "D" {
		t.Fatalf("default: store bucket=%q want D", url)
	}
}

func TestResolveWriteTarget_BuiltinWhenNoDefault(t *testing.T) {
	def := &stubBlob{name: "default"}
	r := New(Config{
		Configs: idResolver{
			byID:    map[string]storageconfig.ResolvedStorage{},
			idByOrg: map[string]string{}, // no default entry
		},
		Default: def,
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			return &stubBlob{name: rs.Bucket}, nil
		},
	})
	bs, id, err := r.ResolveWriteTarget(context.Background(), "org1", "")
	if err != nil || id != builtinConfigID {
		t.Fatalf("builtin: id=%q err=%v want builtin", id, err)
	}
	if bs != def {
		t.Fatalf("builtin: store=%v want def", bs)
	}
}

// TestResolveWriteTarget_OverrideMissingFallsToDefault verifies that when the
// project-override config id is set but unknown (ResolveByID returns ok=false),
// ResolveWriteTarget falls through and returns the org DEFAULT config, not
// builtin and not an error.
func TestResolveWriteTarget_OverrideMissingFallsToDefault(t *testing.T) {
	def := &stubBlob{name: "default"}
	var buf bytes.Buffer
	r := New(Config{
		Configs: idResolver{
			byID: map[string]storageconfig.ResolvedStorage{
				"cfgD": {Mode: "s3", Bucket: "D"},
				// "cfgX" is intentionally absent — override is unknown.
			},
			idByOrg: map[string]string{"default|org1": "cfgD"},
		},
		Default: def,
		Build: func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
			return &stubBlob{name: rs.Bucket}, nil
		},
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	bs, id, err := r.ResolveWriteTarget(context.Background(), "org1", "cfgX")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if id != "cfgD" {
		t.Fatalf("want default config id cfgD, got %q", id)
	}
	url, _ := bs.SignedURL(context.Background(), "", 0)
	if url != "D" {
		t.Fatalf("want default store (bucket D), got %q", url)
	}
}
