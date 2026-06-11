package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetBlocksNonPublicIPs(t *testing.T) {
	f := New(Config{Timeout: time.Second, MaxBytes: 1024})
	for _, u := range []string{
		"http://127.0.0.1/x",
		"http://169.254.169.254/latest/meta-data", // cloud metadata
		"http://10.0.0.8/internal",
		"http://[::1]/x",
	} {
		if _, _, err := f.Get(context.Background(), u); err == nil {
			t.Fatalf("%s must be blocked", u)
		}
	}
}

func TestGetRejectsNonHTTPSchemes(t *testing.T) {
	f := New(Config{Timeout: time.Second, MaxBytes: 1024})
	if _, _, err := f.Get(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatalf("file scheme must be rejected")
	}
}

func TestLoopbackForTestFetchesAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNGBYTES"))
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		case "/big":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
		}
	}))
	defer srv.Close()
	f := NewLoopbackForTest(time.Second, 1024, []string{"image/"})
	body, ct, err := f.Get(context.Background(), srv.URL+"/img")
	if err != nil || string(body) != "PNGBYTES" || ct == "" {
		t.Fatalf("fetch image: %v body=%q ct=%q", err, body, ct)
	}
	if _, _, err := f.Get(context.Background(), srv.URL+"/html"); err == nil {
		t.Fatalf("disallowed content-type must be rejected")
	}
	if _, _, err := f.Get(context.Background(), srv.URL+"/big"); err == nil {
		t.Fatalf("over-cap body must be rejected, not truncated")
	}
}
