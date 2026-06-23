package fetch

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP_Matrix(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},             // loopback
		{"10.0.0.5", true},              // RFC1918
		{"192.168.1.1", true},           // RFC1918
		{"169.254.169.254", true},       // link-local metadata
		{"100.64.0.1", true},            // CGNAT
		{"::1", true},                   // IPv6 loopback
		{"64:ff9b::a9fe:a9fe", true},    // NAT64 of 169.254.169.254 (a9fe:a9fe)
		{"64:ff9b::0a00:0005", true},    // NAT64 of 10.0.0.5
		{"::ffff:127.0.0.1", true},      // IPv4-mapped loopback
		{"::ffff:10.0.0.5", true},       // IPv4-mapped RFC1918
		{"8.8.8.8", false},              // public
		{"2606:4700:4700::1111", false}, // public IPv6 (Cloudflare)
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("bad test ip %q", c.ip)
		}
		if got := isBlockedIP(ip); got != c.blocked {
			t.Errorf("isBlockedIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}

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
