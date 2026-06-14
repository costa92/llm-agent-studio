package prompt

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run prompt store tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st.Pool()
}

func TestStoreCRUD(t *testing.T) {
	pool := testPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	orgID := "org-test-123"

	// Create
	p1, err := s.Create(ctx, orgID, "Test Prompt 1", "draw a cat", "日漫")
	if err != nil {
		t.Fatalf("create error: %v", err)
	}
	if p1.Name != "Test Prompt 1" || p1.Content != "draw a cat" || p1.Style != "日漫" {
		t.Errorf("created prompt mismatch: %+v", p1)
	}

	// List
	list, err := s.ListByOrg(ctx, orgID)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 prompt, got %d", len(list))
	}

	// Update
	p2, err := s.Update(ctx, p1.ID, orgID, "Updated Name", "draw a dog", "吉卜力")
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	if p2.Name != "Updated Name" || p2.Content != "draw a dog" || p2.Style != "吉卜力" {
		t.Errorf("updated prompt mismatch: %+v", p2)
	}

	// Delete
	err = s.Delete(ctx, p1.ID, orgID)
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}

	// List again
	list, err = s.ListByOrg(ctx, orgID)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 prompts, got %d", len(list))
	}
}
