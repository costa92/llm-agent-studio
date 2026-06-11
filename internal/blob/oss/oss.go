// Package oss is the production BlobStore over Alibaba Cloud OSS via the
// official aliyun-oss-go-sdk (spec §10). OSS uses its own request signature
// (not AWS SigV4), so unlike COS it is NOT served through the S3/minio-go
// adapter — minio-go presigned URLs do not validate against OSS. SignedURL =
// OSS SignURL, a local signing operation (no round-trip) producing a
// short-TTL virtual-hosted GET link; no回源 handler (direct object-store link).
package oss

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	alioss "github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// Config configures New.
type Config struct {
	Endpoint        string // e.g. oss-cn-hangzhou.aliyuncs.com; scheme optional (https default)
	Bucket          string
	AccessKeyID     string
	AccessKeySecret string
}

// Store is an Alibaba Cloud OSS BlobStore.
type Store struct {
	bucket *alioss.Bucket
}

// New builds a Store. Bucket and endpoint are required; the bucket is NOT
// auto-created (ops provisions it).
func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("blob.oss: bucket is required")
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("blob.oss: endpoint is required")
	}
	client, err := alioss.New(normalizeEndpoint(cfg.Endpoint), cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("blob.oss: new client: %w", err)
	}
	bkt, err := client.Bucket(cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("blob.oss: bucket: %w", err)
	}
	return &Store{bucket: bkt}, nil
}

// Put uploads bytes under key.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	if err := s.bucket.PutObject(key, r, alioss.ContentType(contentType), alioss.WithContext(ctx)); err != nil {
		return fmt.Errorf("blob.oss: put: %w", err)
	}
	return nil
}

// SignedURL mints a presigned GET URL valid for ttl (local signing, no I/O).
func (s *Store) SignedURL(_ context.Context, key string, ttl time.Duration) (string, error) {
	u, err := s.bucket.SignURL(key, alioss.HTTPGet, int64(ttl.Seconds()))
	if err != nil {
		return "", fmt.Errorf("blob.oss: presign: %w", err)
	}
	return u, nil
}

// Delete removes key.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.bucket.DeleteObject(key, alioss.WithContext(ctx)); err != nil {
		return fmt.Errorf("blob.oss: delete: %w", err)
	}
	return nil
}

// normalizeEndpoint defaults a scheme-less endpoint to https (secure by
// default) so signed URLs are https; an explicit scheme is left intact.
func normalizeEndpoint(ep string) string {
	if strings.Contains(ep, "://") {
		return ep
	}
	return "https://" + ep
}
