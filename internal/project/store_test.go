package project

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run project tests")
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
	return New(st.Pool()), st.Pool()
}

func TestCreateGetListProject(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{
		OrgID: "org-x", Name: "Promo", Brief: "a promo", ContentType: "ad",
		TargetPlatform: "tiktok", Style: "cyberpunk", CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Status != "draft" {
		t.Fatalf("new project status=%q want draft", p.Status)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Promo" || got.OrgID != "org-x" {
		t.Fatalf("get mismatch: %+v", got)
	}
	items, _, err := s.ListByOrg(ctx, "org-x", 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list len=%d want 1", len(items))
	}
}

func TestOrgIDForProject(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org-y", Name: "X", CreatedBy: "u1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	org, err := s.OrgIDForProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("org lookup: %v", err)
	}
	if org != "org-y" {
		t.Fatalf("org=%q want org-y", org)
	}
}
