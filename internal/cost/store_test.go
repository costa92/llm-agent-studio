package cost

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run cost store tests")
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

func seedProject(t *testing.T, db *gorm.DB, orgID string) string {
	t.Helper()
	var id string
	if err := db.WithContext(context.Background()).Raw(
		`INSERT INTO projects (id, org_id, name, created_by) VALUES (md5(random()::text), $1, 'p', 'u') RETURNING id`,
		orgID).Row().Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

func TestRecordAndAggregateByProject(t *testing.T) {
	db := testDB(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, db, "org-cost")
	if err := st.Record(ctx, Generation{
		ProjectID: pid, TodoID: "t", Kind: "image", Provider: "fake", Model: "m",
		Tokens: 100, ImageCount: 1, CostMicros: 2000, LatencyMS: 350,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	_ = st.Record(ctx, Generation{ProjectID: pid, Kind: "image", Provider: "fake", Model: "m", ImageCount: 1, CostMicros: 3000})
	agg, err := st.ByProject(ctx, pid)
	if err != nil {
		t.Fatalf("byProject: %v", err)
	}
	if agg.Generations != 2 || agg.CostMicros != 5000 || agg.Tokens != 100 || agg.ImageCount != 2 {
		t.Fatalf("aggregate mismatch: %+v", agg)
	}
}

func TestAggregateByOrg(t *testing.T) {
	db := testDB(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, db, "org-only")
	_ = st.Record(ctx, Generation{ProjectID: pid, Kind: "image", Provider: "fake", Model: "m", CostMicros: 1500, ImageCount: 1})
	agg, err := st.ByOrg(ctx, "org-only")
	if err != nil {
		t.Fatalf("byOrg: %v", err)
	}
	if agg.CostMicros != 1500 || agg.Generations != 1 {
		t.Fatalf("org aggregate mismatch: %+v", agg)
	}
}

func TestPriceForAndRecordPriced(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	// Seed a price for a test provider/model (idempotent for re-runs).
	if err := db.WithContext(ctx).Exec(`INSERT INTO pricing (provider, model, kind, micros_per_image, micros_per_1k_tokens)
		VALUES ('testprov','testmodel','image',5000,1000) ON CONFLICT (provider, model) DO NOTHING`).Error; err != nil {
		t.Fatalf("seed pricing: %v", err)
	}
	p, ok, err := s.PriceFor(ctx, "testprov", "testmodel")
	if err != nil || !ok {
		t.Fatalf("PriceFor: ok=%v err=%v", ok, err)
	}
	if p.MicrosPerImage != 5000 || p.MicrosPer1kTokens != 1000 {
		t.Fatalf("price mismatch: %+v", p)
	}
	if _, ok, _ := s.PriceFor(ctx, "nope", "nope"); ok {
		t.Fatalf("unknown model should not be priced")
	}
	// RecordPriced fills cost_micros: 2 images * 5000 + 2000 tokens * 1000/1k.
	pid := seedProject(t, db, "org_priced")
	if err := s.RecordPriced(ctx, Generation{
		ProjectID: pid, Kind: "image", Provider: "testprov", Model: "testmodel",
		Tokens: 2000, ImageCount: 2,
	}); err != nil {
		t.Fatalf("RecordPriced: %v", err)
	}
	var micros int64
	if err := db.WithContext(ctx).Raw(`SELECT cost_micros FROM generations WHERE project_id=$1`, pid).Row().Scan(&micros); err != nil {
		t.Fatalf("load generation: %v", err)
	}
	if micros != 2*5000+2000*1000/1000 {
		t.Fatalf("cost_micros = %d, want 12000", micros)
	}
}

func TestAggregatesByRangePerProjectAndRecent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	orgID := "org_range_" + time.Now().Format("150405.000000000")
	pid := seedProject(t, db, orgID)
	// One row inside the window, one well in the past (created_at backdated).
	if err := s.Record(ctx, Generation{ProjectID: pid, Provider: "p", Model: "m", ImageCount: 1, CostMicros: 100}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := db.WithContext(ctx).Exec(`INSERT INTO generations
		(id, project_id, kind, provider, model, image_count, cost_micros, created_at)
		VALUES (md5(random()::text), $1, 'image', 'p', 'm', 1, 100, now() - interval '48 hours')`, pid).Error; err != nil {
		t.Fatalf("seed old generation: %v", err)
	}
	// Range covering only the last 24h sees 1 generation.
	agg, err := s.ByOrgBetween(ctx, orgID, time.Now().Add(-24*time.Hour), time.Time{})
	if err != nil {
		t.Fatalf("ByOrgBetween: %v", err)
	}
	if agg.Generations != 1 || agg.CostMicros != 100 {
		t.Fatalf("ranged org agg = %+v, want 1 generation / 100 micros", agg)
	}
	// Zero bounds = unbounded: sees both.
	all, err := s.ByProjectBetween(ctx, pid, time.Time{}, time.Time{})
	if err != nil || all.Generations != 2 {
		t.Fatalf("unbounded project agg = %+v err=%v, want 2", all, err)
	}
	// Per-project rollup carries the project id + name.
	per, err := s.PerProjectByOrg(ctx, orgID, time.Time{}, time.Time{})
	if err != nil || len(per) != 1 || per[0].ProjectID != pid || per[0].Generations != 2 {
		t.Fatalf("per-project rollup = %+v err=%v", per, err)
	}
	// Recent ledger entries, newest first.
	recent, err := s.RecentByOrg(ctx, orgID, 10)
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent = %d err=%v, want 2", len(recent), err)
	}
	if recent[0].CreatedAt.Before(recent[1].CreatedAt) {
		t.Fatalf("recent must be newest-first")
	}
	// Rolling count for the quota check.
	n, err := s.CountByOrgSince(ctx, orgID, time.Now().Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("CountByOrgSince = %d err=%v, want 1", n, err)
	}
}

func TestPerSecondPricingAndUpsertFlow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	if err := db.WithContext(ctx).Exec(`INSERT INTO pricing (provider, model, kind, micros_per_second)
		VALUES ('fake','fake-video-async','video',500000) ON CONFLICT (provider, model) DO NOTHING`).Error; err != nil {
		t.Fatalf("seed pricing: %v", err)
	}
	p, ok, err := s.PriceFor(ctx, "fake", "fake-video-async")
	if err != nil || !ok || p.MicrosPerSecond != 500000 {
		t.Fatalf("PriceFor per-second: %+v ok=%v err=%v", p, ok, err)
	}
	if got := ComputeCostMicros(p, 0, 0, 6); got != 6*500000 {
		t.Fatalf("ComputeCostMicros seconds = %d, want %d", got, 6*500000)
	}
	pid := seedProject(t, db, "org_upsert")
	g := Generation{ProjectID: pid, AssetID: "asset-1", TodoID: "todo-1", Kind: "video",
		Provider: "fake", Model: "fake-video-async", VideoSeconds: 6, CostMicros: 6 * 500000}
	id1, err := s.UpsertSubmittedGeneration(ctx, g)
	if err != nil || id1 == "" {
		t.Fatalf("upsert#1: id=%q err=%v", id1, err)
	}
	// Crash-retry: a second upsert on the same (asset_id, todo_id) returns the
	// SAME row id and does NOT double-insert (B3 DB-enforced dedup).
	id2, err := s.UpsertSubmittedGeneration(ctx, g)
	if err != nil || id2 != id1 {
		t.Fatalf("upsert#2 must return same id: %q vs %q err=%v", id2, id1, err)
	}
	var nRows int
	_ = db.WithContext(ctx).Raw(`SELECT count(*) FROM generations WHERE asset_id='asset-1' AND todo_id='todo-1'`).Row().Scan(&nRows)
	if nRows != 1 {
		t.Fatalf("ledger must have exactly 1 row, got %d", nRows)
	}
	// poll-done backfill: real seconds/cost overwrite the estimate in place.
	if err := s.UpdateGenerationByAssetTodo(ctx, "asset-1", "todo-1", 8, 8*500000); err != nil {
		t.Fatalf("update: %v", err)
	}
	var sec int
	var micros int64
	_ = db.WithContext(ctx).Raw(`SELECT video_seconds, cost_micros FROM generations WHERE asset_id='asset-1' AND todo_id='todo-1'`).Row().Scan(&sec, &micros)
	if sec != 8 || micros != 8*500000 {
		t.Fatalf("backfill = %ds / %d micros, want 8s / %d", sec, micros, 8*500000)
	}
}
