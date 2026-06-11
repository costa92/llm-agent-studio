package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func spaTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>studio</title>")},
		"assets/app.abc123.js": {Data: []byte("console.log('hi')")},
	}
}

func TestSPA_ServesIndexAtRoot(t *testing.T) {
	h := spaHandler(spaTestFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct == "" || ct[:9] != "text/html" {
		t.Fatalf("content-type=%q want text/html", ct)
	}
	if body := rr.Body.String(); body == "" || body[:9] != "<!doctype" {
		t.Fatalf("body=%q want index.html", body)
	}
}

func TestSPA_ServesRealAsset(t *testing.T) {
	h := spaHandler(spaTestFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/assets/app.abc123.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "console.log('hi')" {
		t.Fatalf("body=%q want js content", got)
	}
}

func TestSPA_DeepLinkFallsBackToIndex(t *testing.T) {
	// Client-side route (no such file) must serve index.html, not 404,
	// so the SPA router can render it on reload / direct navigation.
	h := spaHandler(spaTestFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/projects/abc-123/workflow", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 (index fallback)", rr.Code)
	}
	if body := rr.Body.String(); body == "" || body[:9] != "<!doctype" {
		t.Fatalf("body=%q want index.html fallback", body)
	}
}

func TestSPA_UnknownAPIPathIs404(t *testing.T) {
	// /api/* must never fall through to index.html — an unrouted API call is a
	// 404, not an HTML page (clients parse JSON / rely on status).
	h := spaHandler(spaTestFS())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/does-not-exist", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404", rr.Code)
	}
}

func TestSPA_MountedOnMuxBehindAPIRoutes(t *testing.T) {
	// Wired through NewMux: the SPA catch-all must not shadow /api/* routes.
	// /api/projects/x without auth still returns 401 (route matched), while a
	// non-API path returns the SPA index.
	mux := NewMux(Deps{WebFS: spaTestFS()})

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("root code=%d want 200 (SPA index)", rr.Code)
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/login", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/login code=%d want 200 (SPA fallback)", rr.Code)
	}
}

func TestSPA_ServesFromRealDirFS(t *testing.T) {
	// studiod wires os.DirFS(cfg.WebDir); prove the handler works over a real
	// filesystem (nested asset dir + deep-link fallback), not just MapFS.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "main.js"), []byte("REAL_JS"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := spaHandler(os.DirFS(dir))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/assets/main.js", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "REAL_JS" {
		t.Fatalf("asset: code=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/library", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "<!doctype html>root" {
		t.Fatalf("deep-link fallback: code=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestSPA_DisabledWhenNoWebFS(t *testing.T) {
	// Default deployment (no WebFS): no catch-all mounted, so a non-API path
	// is a plain 404 — backend-only mode is unchanged.
	mux := NewMux(Deps{})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/some-ui-route", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404 (SPA disabled)", rr.Code)
	}
}
