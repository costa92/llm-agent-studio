// Package blob owns the BlobStore abstraction: the ONLY place that handles
// asset bytes (spec §10). Put writes bytes; SignedURL mints a short-TTL direct
// link (no proxying); Delete removes. Implementations: fake (in-memory, tests),
// localfs (dev disk + HMAC-signed backend URL), s3 (minio-go presigned GET).
package blob

import (
	"context"
	"io"
	"time"
)

// BlobStore stores and serves asset bytes (spec §10).
type BlobStore interface {
	Put(ctx context.Context, key string, r io.Reader, contentType string) error
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
}
