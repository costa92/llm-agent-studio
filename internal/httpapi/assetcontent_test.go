package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/project"
)

// recordingBlobRouter is a BlobRouter stub that records WHICH routing method
// was called and with what arguments. Used to assert that assetContentHandler
// routes by storage_config_id (new path) vs. storage mode (legacy fallback).
type recordingBlobRouter struct {
	// calledConfigID is set when BlobStoreForConfigID is called; holds the configID arg.
	calledConfigID string
	// calledMode is set when BlobStoreForMode is called; holds the mode arg.
	calledMode string
	// bs is the BlobStore returned for either call; must support SignedURL.
	bs blob.BlobStore
}

func (r *recordingBlobRouter) BlobStoreFor(_ context.Context, _ string) (blob.BlobStore, error) {
	return r.bs, nil
}

func (r *recordingBlobRouter) BlobStoreForMode(_ context.Context, _ string, mode string) (blob.BlobStore, error) {
	r.calledMode = mode
	return r.bs, nil
}

func (r *recordingBlobRouter) BlobStoreForConfigID(_ context.Context, _ string, configID string) (blob.BlobStore, error) {
	r.calledConfigID = configID
	return r.bs, nil
}

func (r *recordingBlobRouter) ConfigIDForMode(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func (r *recordingBlobRouter) ResolveWriteTarget(_ context.Context, _ string, _ string) (blob.BlobStore, string, error) {
	return r.bs, "builtin", nil
}

// fixedAssetLib is a minimal AssetLibrary stub that returns a pre-configured asset.
type fixedAssetLib struct {
	a   assets.Asset
	err error
}

func (l *fixedAssetLib) Get(_ context.Context, _ string) (assets.Asset, error) {
	return l.a, l.err
}
func (l *fixedAssetLib) VersionHistory(_ context.Context, _ string) ([]assets.Asset, error) {
	return nil, nil
}
func (l *fixedAssetLib) Library(_ context.Context, _ assets.LibraryFilter) ([]assets.Asset, string, error) {
	return nil, "", nil
}
func (l *fixedAssetLib) OrgIDForAsset(_ context.Context, _ string) (string, error) {
	return "org-test", nil
}

// fixedProjectReader returns a stub project with the given StorageMode.
type fixedProjectReader struct {
	proj project.Project
}

func (r *fixedProjectReader) Get(_ context.Context, _ string) (project.Project, error) {
	return r.proj, nil
}

// signedURLOnlyFake is a BlobStore that only supports SignedURL (no ctxReader
// interface), so assetContentHandler falls through to the http.Redirect branch.
// It always returns a valid URL so the 302 redirect can be asserted.
type signedURLOnlyFake struct{}

func (s *signedURLOnlyFake) Put(_ context.Context, _ string, _ io.Reader, _ string) error {
	return nil
}
func (s *signedURLOnlyFake) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://cdn.example.com/" + key, nil
}
func (s *signedURLOnlyFake) Delete(_ context.Context, _ string) error { return nil }

// TestAssetContentHandlerRoutesByConfigID verifies Fix: when an asset carries a
// non-empty StorageConfigID, assetContentHandler MUST call BlobStoreForConfigID
// with that id and MUST NOT call BlobStoreForMode.
func TestAssetContentHandlerRoutesByConfigID(t *testing.T) {
	router := &recordingBlobRouter{bs: &signedURLOnlyFake{}}
	lib := &fixedAssetLib{a: assets.Asset{
		ID:              "a1",
		ProjectID:       "p1",
		BlobKey:         "covers/a1.png",
		StorageConfigID: "cfg1",
	}}
	ps := &fixedProjectReader{proj: project.Project{ID: "p1", StorageMode: "s3"}}

	h := assetContentHandler(lib, router, ps)
	req := httptest.NewRequest(http.MethodGet, "/api/assets/a1/content", nil)
	req.SetPathValue("id", "a1")
	rec := httptest.NewRecorder()
	h(rec, req)

	// Must redirect (302) — blob key present and SignedURL succeeds.
	if rec.Code != http.StatusFound {
		t.Fatalf("Case A: want 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	// BlobStoreForConfigID MUST have been called with "cfg1".
	if router.calledConfigID != "cfg1" {
		t.Fatalf("Case A: want BlobStoreForConfigID(%q), got calledConfigID=%q", "cfg1", router.calledConfigID)
	}
	// BlobStoreForMode MUST NOT have been called.
	if router.calledMode != "" {
		t.Fatalf("Case A: BlobStoreForMode should NOT be called when StorageConfigID is set, got calledMode=%q", router.calledMode)
	}
	// Redirect URL must contain the blob key.
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "covers/a1.png") {
		t.Fatalf("Case A: redirect URL %q missing blob key", loc)
	}
}

// TestAssetContentHandlerNoBytes404 verifies that an asset with NO bytes
// (blobKey=="" AND url=="", e.g. a failed/canceled generation) yields a clean
// 404 — NOT a 302 to /api/blob/?sig=… with an empty key (which always 404s and
// makes the frontend fire a doomed redirect + console error per thumbnail).
func TestAssetContentHandlerNoBytes404(t *testing.T) {
	router := &recordingBlobRouter{bs: &signedURLOnlyFake{}}
	lib := &fixedAssetLib{a: assets.Asset{
		ID:        "a3",
		ProjectID: "p3",
		BlobKey:   "", // no blob
		URL:       "", // no external url either
		Status:    "failed",
	}}
	ps := &fixedProjectReader{proj: project.Project{ID: "p3", StorageMode: "localfs"}}

	h := assetContentHandler(lib, router, ps)
	req := httptest.NewRequest(http.MethodGet, "/api/assets/a3/content", nil)
	req.SetPathValue("id", "a3")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for a no-bytes asset, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Must NOT have signed an empty key (no redirect, no blob-store routing).
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("no-bytes asset must not 302, got Location=%q", loc)
	}
	if router.calledConfigID != "" || router.calledMode != "" {
		t.Fatalf("no-bytes asset must not resolve a blob store, got configID=%q mode=%q", router.calledConfigID, router.calledMode)
	}
}

// TestAssetContentHandlerLegacyFallback verifies that an asset with an empty
// StorageConfigID (pre-m15 row) falls back to BlobStoreForMode, NOT
// BlobStoreForConfigID.
func TestAssetContentHandlerLegacyFallback(t *testing.T) {
	router := &recordingBlobRouter{bs: &signedURLOnlyFake{}}
	lib := &fixedAssetLib{a: assets.Asset{
		ID:              "a2",
		ProjectID:       "p2",
		BlobKey:         "covers/a2.png",
		StorageConfigID: "", // empty = legacy
	}}
	ps := &fixedProjectReader{proj: project.Project{ID: "p2", StorageMode: "localfs"}}

	h := assetContentHandler(lib, router, ps)
	req := httptest.NewRequest(http.MethodGet, "/api/assets/a2/content", nil)
	req.SetPathValue("id", "a2")
	rec := httptest.NewRecorder()
	h(rec, req)

	// Must redirect.
	if rec.Code != http.StatusFound {
		t.Fatalf("Case B: want 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	// BlobStoreForMode MUST have been called (legacy fallback).
	if router.calledMode == "" {
		t.Fatalf("Case B: BlobStoreForMode should be called for legacy (empty StorageConfigID) asset")
	}
	// BlobStoreForConfigID MUST NOT have been called.
	if router.calledConfigID != "" {
		t.Fatalf("Case B: BlobStoreForConfigID should NOT be called for legacy asset, got calledConfigID=%q", router.calledConfigID)
	}
}
