// Package s3 is the production BlobStore over any S3-compatible store (MinIO,
// AWS S3) via minio-go (spec §10). SignedURL = presigned GET (direct object-store
// link, no proxying). There is no回源 handler: presigned URLs hit the store
// directly, so this type does NOT implement httpapi.BlobServer.
package s3

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config configures New.
type Config struct {
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// Store is an S3-compatible BlobStore.
type Store struct {
	client *minio.Client
	bucket string
}

// New builds a Store. Bucket is required; the bucket is NOT auto-created (ops
// provisions it).
func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("blob.s3: bucket is required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("blob.s3: new client: %w", err)
	}
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// Put uploads bytes under key. Size is unknown (streaming) so -1 is passed.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, -1, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("blob.s3: put: %w", err)
	}
	return nil
}

// SignedURL mints a presigned GET URL valid for ttl.
func (s *Store) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("blob.s3: presign: %w", err)
	}
	return u.String(), nil
}

// Delete removes key.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("blob.s3: delete: %w", err)
	}
	return nil
}
