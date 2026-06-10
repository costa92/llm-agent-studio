package localfs

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPutThenReadBytes(t *testing.T) {
	s := New(t.TempDir(), []byte("secret"), "/api/blob/")
	ctx := context.Background()
	if err := s.Put(ctx, "proj/asset1.png", strings.NewReader("PNGDATA"), "image/png"); err != nil {
		t.Fatalf("put: %v", err)
	}
	data, ct, err := s.ReadKey("proj/asset1.png")
	if err != nil || string(data) != "PNGDATA" || ct != "image/png" {
		t.Fatalf("read mismatch: %v ct=%q data=%q", err, ct, data)
	}
}

func TestSignedURLVerifies(t *testing.T) {
	s := New(t.TempDir(), []byte("secret"), "/api/blob/")
	ctx := context.Background()
	_ = s.Put(ctx, "k.png", strings.NewReader("x"), "image/png")
	raw, err := s.SignedURL(ctx, "k.png", time.Hour)
	if err != nil {
		t.Fatalf("signedURL: %v", err)
	}
	u, _ := url.Parse(raw)
	if !strings.HasPrefix(u.Path, "/api/blob/k.png") {
		t.Fatalf("bad path %q", u.Path)
	}
	sig := u.Query().Get("sig")
	exp := u.Query().Get("exp")
	if err := s.Verify("k.png", exp, sig); err != nil {
		t.Fatalf("verify good sig: %v", err)
	}
	if err := s.Verify("k.png", exp, "deadbeef"); err == nil {
		t.Fatalf("expected bad-sig rejection")
	}
}

func TestPutBlocksPathTraversal(t *testing.T) {
	root := t.TempDir()
	s := New(root, []byte("secret"), "/api/blob/")
	ctx := context.Background()
	// A traversal key must be neutralized (Clean strips leading ../), so the
	// write stays under root and cannot escape to a sibling/parent directory.
	if err := s.Put(ctx, "../../escape.png", strings.NewReader("X"), "image/png"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.png")); err == nil {
		t.Fatalf("path traversal escaped root: file written to parent dir")
	}
	// Round-trips via the same cleaned-key mapping (stays inside root).
	if data, _, err := s.ReadKey("../../escape.png"); err != nil || string(data) != "X" {
		t.Fatalf("read back: err=%v data=%q", err, data)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	s := New(t.TempDir(), []byte("secret"), "/api/blob/")
	// exp in the past, signed correctly.
	exp := "1"
	sig := s.sign("k.png", exp)
	if err := s.Verify("k.png", exp, sig); err == nil {
		t.Fatalf("expected expiry rejection")
	}
}
