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

func TestCancelSweepsGeneratingAssets(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org_cancel", Name: "P", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// One in-flight sync asset ('generating'), one in-flight async asset
	// ('submitted' — external job running), one already awaiting review.
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, project_id, status) VALUES
		(md5(random()::text), $1, 'generating'),
		(md5(random()::text), $1, 'submitted'),
		(md5(random()::text), $1, 'pending_acceptance')`, p.ID); err != nil {
		t.Fatalf("seed assets: %v", err)
	}
	if err := s.Cancel(ctx, p.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var canceled, pending int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='canceled'`, p.ID).Scan(&canceled)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='pending_acceptance'`, p.ID).Scan(&pending)
	// F1: BOTH 'generating' (sync) AND 'submitted' (async) in-flight assets must be
	// swept to 'canceled' — a submitted asset stranded otherwise (spec §5.4 必修).
	if canceled != 2 {
		t.Fatalf("generating + submitted assets should be canceled, got %d canceled (want 2)", canceled)
	}
	// Decision: pending_acceptance assets stay reviewable (real money was spent;
	// HITL accept/reject still applies after a cancel).
	if pending != 1 {
		t.Fatalf("pending_acceptance asset must survive cancel, got %d pending", pending)
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

func TestCancelSweepsSubmittedAssets(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org_cancel_sub", Name: "P", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// One submitted (external job in flight), one generating, one pending.
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, project_id, status) VALUES
		(md5(random()::text), $1, 'submitted'),
		(md5(random()::text), $1, 'generating'),
		(md5(random()::text), $1, 'pending_acceptance')`, p.ID); err != nil {
		t.Fatalf("seed assets: %v", err)
	}
	if err := s.Cancel(ctx, p.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var canceled, pending int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='canceled'`, p.ID).Scan(&canceled)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='pending_acceptance'`, p.ID).Scan(&pending)
	if canceled != 2 {
		t.Fatalf("submitted + generating must both be canceled, got %d canceled", canceled)
	}
	if pending != 1 {
		t.Fatalf("pending_acceptance must survive cancel, got %d", pending)
	}
}
