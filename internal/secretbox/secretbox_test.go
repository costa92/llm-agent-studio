package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

// testKeyB64 是固定的 base64 32 字节密钥，供测试构造 enabled box。
const testKeyB64 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func TestRoundTrip(t *testing.T) {
	b, err := New(testKeyB64)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if !b.Enabled() {
		t.Fatal("box should be enabled")
	}
	pt := []byte("sk-secret-api-key-12345")
	ct, err := b.Encrypt(pt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ct, pt) {
		t.Fatal("ciphertext must not contain plaintext")
	}
	got, err := b.Decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("round-trip mismatch: %q != %q", got, pt)
	}
}

func TestTamperFails(t *testing.T) {
	b, _ := New(testKeyB64)
	ct, _ := b.Encrypt([]byte("payload"))
	ct[len(ct)-1] ^= 0xff // 篡改最后一字节
	if _, err := b.Decrypt(ct); err == nil {
		t.Fatal("tampered ciphertext must fail GCM auth")
	}
}

func TestNonceIsRandom(t *testing.T) {
	b, _ := New(testKeyB64)
	a, _ := b.Encrypt([]byte("x"))
	c, _ := b.Encrypt([]byte("x"))
	if bytes.Equal(a, c) {
		t.Fatal("two encryptions of same plaintext must differ (random nonce)")
	}
}

func TestDisabledBox(t *testing.T) {
	b, err := New("")
	if err != nil {
		t.Fatalf("empty key must not error (disabled): %v", err)
	}
	if b.Enabled() {
		t.Fatal("empty-key box must be disabled")
	}
	if _, err := b.Encrypt([]byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("disabled Encrypt want ErrNoKey, got %v", err)
	}
	if _, err := b.Decrypt([]byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("disabled Decrypt want ErrNoKey, got %v", err)
	}
}

func TestBadKeyLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	if _, err := New(short); err == nil {
		t.Fatal("16/9-byte key must error (need 32)")
	}
	if _, err := New("!!!not-base64!!!"); err == nil {
		t.Fatal("invalid base64 must error")
	}
}

func TestRandomKeyRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	b, err := New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("new random key: %v", err)
	}
	ct, _ := b.Encrypt([]byte("hello"))
	got, err := b.Decrypt(ct)
	if err != nil || string(got) != "hello" {
		t.Fatalf("round-trip: %v %q", err, got)
	}
}
