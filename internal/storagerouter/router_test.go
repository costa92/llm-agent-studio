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
		Build:   func(storageconfig.ResolvedStorage) (blob.BlobStore, error) { t.Fatal("Build must not run on !ok"); return nil, nil },
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
		Build:   func(storageconfig.ResolvedStorage) (blob.BlobStore, error) { t.Fatal("Build must not run on resolve error"); return nil, nil },
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
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

func TestBlobStoreForNeverNilWhenDefaultSet(t *testing.T) {
	def := &stubBlob{name: "default"}
	r := New(Config{Configs: fakeResolver{ok: false}, Default: def})
	got, err := r.BlobStoreFor(context.Background(), "org")
	if err != nil || got != def {
		t.Fatalf("want default, got %v err=%v", got, err)
	}
}
