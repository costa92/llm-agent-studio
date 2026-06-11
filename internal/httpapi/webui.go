package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves a built single-page app from fsys: real files are served
// with their content-type (hashed assets are immutable-cacheable by the
// FileServer), and any other non-/api path falls back to index.html so the
// client router can render deep links on reload / direct navigation.
//
// Mounted under the mux's "GET /" catch-all, which Go's ServeMux ranks below
// every more-specific "/api/..." pattern — so this never shadows an API route.
// Unknown "/api/..." paths still reach here (no API pattern matched); those
// must 404 rather than return an HTML page, since clients parse JSON.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			serveIndex(w, fsys)
			return
		}
		if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, fsys)
	})
}

// serveIndex writes index.html with no-cache so a fresh deploy's asset hashes
// are always picked up (the hashed assets themselves stay cacheable).
func serveIndex(w http.ResponseWriter, fsys fs.FS) {
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "web UI not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
