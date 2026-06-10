package blob

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestFakePutGetDelete(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	if err := f.Put(ctx, "a/b.png", strings.NewReader("hello"), "image/png"); err != nil {
		t.Fatalf("put: %v", err)
	}
	data, ct, ok := f.Get("a/b.png")
	if !ok || string(data) != "hello" || ct != "image/png" {
		t.Fatalf("get mismatch: ok=%v ct=%q data=%q", ok, ct, data)
	}
	u, err := f.SignedURL(ctx, "a/b.png", time.Minute)
	if err != nil || u == "" {
		t.Fatalf("signedURL: %v %q", err, u)
	}
	if err := f.Delete(ctx, "a/b.png"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, ok := f.Get("a/b.png"); ok {
		t.Fatalf("expected gone after delete")
	}
}

func TestFakePutReadsAll(t *testing.T) {
	f := NewFake()
	_ = f.Put(context.Background(), "k", io.NopCloser(bytes.NewReader([]byte("xyz"))), "")
	if data, _, _ := f.Get("k"); string(data) != "xyz" {
		t.Fatalf("want xyz got %q", data)
	}
}
