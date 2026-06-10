// Package localfs is the dev BlobStore: bytes to disk + a backend-signed
//回源 URL (spec §10). SignedURL mints /api/blob/{key}?exp=&sig= where sig is
// HMAC-SHA256(key+"\n"+exp); the studiod blob handler calls Verify before
// serving the bytes, so no credentials leak to the browser.
package localfs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Store is a disk-backed BlobStore.
type Store struct {
	root      string
	secret    []byte
	urlPrefix string // e.g. "/api/blob/"
}

// New builds a Store rooted at dir, signing URLs with secret under urlPrefix.
func New(dir string, secret []byte, urlPrefix string) *Store {
	return &Store{root: dir, secret: secret, urlPrefix: urlPrefix}
}

func (s *Store) pathFor(key string) string {
	// key may contain '/'; map to nested dirs under root. Clean to block ../.
	clean := filepath.Clean("/" + key)
	return filepath.Join(s.root, clean)
}

// Put writes r's bytes to disk and records the content type in a sidecar file.
func (s *Store) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	p := s.pathFor(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("localfs: mkdir: %w", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("localfs: read: %w", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("localfs: write: %w", err)
	}
	if err := os.WriteFile(p+".ct", []byte(contentType), 0o644); err != nil {
		return fmt.Errorf("localfs: write ct: %w", err)
	}
	return nil
}

// ReadKey returns the stored bytes + content type (used by the blob handler).
func (s *Store) ReadKey(key string) ([]byte, string, error) {
	p := s.pathFor(key)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, "", fmt.Errorf("localfs: read: %w", err)
	}
	ct, _ := os.ReadFile(p + ".ct")
	return data, string(ct), nil
}

// Delete removes the object + sidecar (no error if absent).
func (s *Store) Delete(_ context.Context, key string) error {
	p := s.pathFor(key)
	_ = os.Remove(p + ".ct")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("localfs: delete: %w", err)
	}
	return nil
}

func (s *Store) sign(key, exp string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(key + "\n" + exp))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignedURL mints a backend回源 URL with an HMAC signature + expiry.
func (s *Store) SignedURL(_ context.Context, key string, ttl time.Duration) (string, error) {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	sig := s.sign(key, exp)
	q := url.Values{"exp": {exp}, "sig": {sig}}
	return s.urlPrefix + key + "?" + q.Encode(), nil
}

// Verify checks the signature and expiry for a key (called by the blob handler).
func (s *Store) Verify(key, exp, sig string) error {
	want := s.sign(key, exp)
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return fmt.Errorf("localfs: bad signature")
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return fmt.Errorf("localfs: bad exp")
	}
	if time.Now().Unix() > n {
		return fmt.Errorf("localfs: expired")
	}
	return nil
}

// KeyFromPath strips the urlPrefix from a request path to recover the blob key.
func (s *Store) KeyFromPath(path string) string {
	return strings.TrimPrefix(path, s.urlPrefix)
}
