package prompt

import (
	"context"
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testDB(t *testing.T) *gorm.DB {
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
	return st.GORM()
}

func TestStoreCRUD(t *testing.T) {
	db := testDB(t)
	s := NewStore(db)
	ctx := context.Background()
	orgID := "org-test-123"

	// Create
	p1, err := s.Create(ctx, orgID, "Test Prompt 1", "draw a cat", "日漫", "script")
	if err != nil {
		t.Fatalf("create error: %v", err)
	}
	if p1.Name != "Test Prompt 1" || p1.Content != "draw a cat" || p1.Style != "日漫" || p1.Kind != "script" {
		t.Errorf("created prompt mismatch: %+v", p1)
	}
	if p1.IsDefault {
		t.Errorf("created prompt should not be default by default: %+v", p1)
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
	p2, err := s.Update(ctx, p1.ID, orgID, "Updated Name", "draw a dog", "吉卜力", "storyboard")
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	if p2.Name != "Updated Name" || p2.Content != "draw a dog" || p2.Style != "吉卜力" || p2.Kind != "storyboard" {
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

func TestStoreSetDefault(t *testing.T) {
	db := testDB(t)
	s := NewStore(db)
	ctx := context.Background()
	orgID := "org-setdefault-456"

	script1, err := s.Create(ctx, orgID, "Script 1", "c1", "", "script")
	if err != nil {
		t.Fatalf("create script1: %v", err)
	}
	script2, err := s.Create(ctx, orgID, "Script 2", "c2", "", "script")
	if err != nil {
		t.Fatalf("create script2: %v", err)
	}
	story, err := s.Create(ctx, orgID, "Storyboard 1", "c3", "", "storyboard")
	if err != nil {
		t.Fatalf("create storyboard: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Delete(ctx, script1.ID, orgID)
		_ = s.Delete(ctx, script2.ID, orgID)
		_ = s.Delete(ctx, story.ID, orgID)
	})

	defaults := func() map[string]bool {
		t.Helper()
		list, err := s.ListByOrg(ctx, orgID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		m := map[string]bool{}
		for _, p := range list {
			m[p.ID] = p.IsDefault
		}
		return m
	}

	// SetDefault(script1) → script1 default, script2 not, storyboard untouched.
	got, err := s.SetDefault(ctx, script1.ID, orgID)
	if err != nil {
		t.Fatalf("set default script1: %v", err)
	}
	if !got.IsDefault {
		t.Fatalf("returned prompt should be default: %+v", got)
	}
	d := defaults()
	if !d[script1.ID] || d[script2.ID] || d[story.ID] {
		t.Fatalf("after SetDefault(script1) defaults wrong: %+v", d)
	}

	// SetDefault(script2) → script1 cleared (one default per (org,kind)),
	// script2 default, storyboard still untouched.
	if _, err := s.SetDefault(ctx, script2.ID, orgID); err != nil {
		t.Fatalf("set default script2: %v", err)
	}
	d = defaults()
	if d[script1.ID] || !d[script2.ID] || d[story.ID] {
		t.Fatalf("after SetDefault(script2) defaults wrong: %+v", d)
	}

	// Not-found → ErrNotFound.
	if _, err := s.SetDefault(ctx, "does-not-exist", orgID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}
