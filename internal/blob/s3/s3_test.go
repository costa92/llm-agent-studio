package s3

import (
	"testing"

	"github.com/costa92/llm-agent-studio/internal/blob"
)

func TestSatisfiesBlobStore(t *testing.T) {
	var _ blob.BlobStore = (*Store)(nil)
}

func TestNewRequiresBucket(t *testing.T) {
	if _, err := New(Config{Endpoint: "localhost:9000", AccessKey: "a", SecretKey: "b"}); err == nil {
		t.Fatalf("expected error when bucket is empty")
	}
}
