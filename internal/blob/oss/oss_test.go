package oss

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/blob"
)

func TestSatisfiesBlobStore(t *testing.T) {
	var _ blob.BlobStore = (*Store)(nil)
}

func TestNewRequiresBucket(t *testing.T) {
	if _, err := New(Config{Endpoint: "oss-cn-hangzhou.aliyuncs.com", AccessKeyID: "a", AccessKeySecret: "b"}); err == nil {
		t.Fatalf("expected error when bucket is empty")
	}
}

func TestNewRequiresEndpoint(t *testing.T) {
	if _, err := New(Config{Bucket: "bkt", AccessKeyID: "a", AccessKeySecret: "b"}); err == nil {
		t.Fatalf("expected error when endpoint is empty")
	}
}

// SignedURL is a local signing operation (no network) — it must mint a GET URL
// carrying the object key and OSS signature query params. This is the only
// production path we can verify offline (Put/Delete need a live bucket).
func TestSignedURLIsOfflineAndSigned(t *testing.T) {
	s, err := New(Config{
		Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: "my-bucket",
		AccessKeyID: "AKID", AccessKeySecret: "SECRET",
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	raw, err := s.SignedURL(context.Background(), "assets/x/y.png", 10*time.Minute)
	if err != nil {
		t.Fatalf("signed url: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if u.Scheme != "https" {
		t.Fatalf("scheme=%q want https (secure by default)", u.Scheme)
	}
	if !strings.Contains(u.Host, "my-bucket") || !strings.Contains(u.Host, "oss-cn-hangzhou.aliyuncs.com") {
		t.Fatalf("host=%q want virtual-hosted bucket on OSS endpoint", u.Host)
	}
	if !strings.Contains(u.Path, "assets/x/y.png") {
		t.Fatalf("path=%q want object key", u.Path)
	}
	q := u.Query()
	for _, k := range []string{"OSSAccessKeyId", "Expires", "Signature"} {
		if q.Get(k) == "" {
			t.Fatalf("missing signature param %q in %q", k, raw)
		}
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"oss-cn-hangzhou.aliyuncs.com":         "https://oss-cn-hangzhou.aliyuncs.com",
		"https://oss-cn-hangzhou.aliyuncs.com": "https://oss-cn-hangzhou.aliyuncs.com",
		"http://minio.internal:9000":           "http://minio.internal:9000",
	}
	for in, want := range cases {
		if got := normalizeEndpoint(in); got != want {
			t.Fatalf("normalizeEndpoint(%q)=%q want %q", in, got, want)
		}
	}
}
