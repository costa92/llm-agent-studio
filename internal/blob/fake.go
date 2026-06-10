package blob

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// Fake is an in-memory BlobStore for tests. Concurrent-safe.
type Fake struct {
	mu      sync.Mutex
	objects map[string]fakeObject
}

type fakeObject struct {
	data        []byte
	contentType string
}

// NewFake builds an empty in-memory BlobStore.
func NewFake() *Fake { return &Fake{objects: map[string]fakeObject{}} }

// Put reads r fully and stores the bytes under key.
func (f *Fake) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("blob.fake: read: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = fakeObject{data: data, contentType: contentType}
	return nil
}

// SignedURL returns a deterministic fake URL (tests assert non-empty + key).
func (f *Fake) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[key]; !ok {
		return "", fmt.Errorf("blob.fake: key %q not found", key)
	}
	return "fake://blob/" + key, nil
}

// Delete removes key (no error if absent).
func (f *Fake) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

// Get returns the stored bytes + content type (test helper, not on the interface).
func (f *Fake) Get(key string) (data []byte, contentType string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[key]
	return o.data, o.contentType, ok
}
