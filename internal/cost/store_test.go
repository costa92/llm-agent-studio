package cost

import (
	"context"
	"errors"
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
	// Recent ledger entries, newest first. Under-limit page → next cursor 空串.
	recent, next, err := s.RecentByOrg(ctx, orgID, 10, "")
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent = %d err=%v, want 2", len(recent), err)
	}
	if next != "" {
		t.Fatalf("under-limit page must return empty next cursor, got %q", next)
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

// TestCountByOrgSinceExcludesText 锁住配额口径：CountByOrgSince 只算媒体生成，
// kind='text'（script/storyboard/custom-llm 中间文本节点）不吃媒体配额。回归守卫——
// 文本节点开始记账（PR#188）后这条不变式一度被打破，导致内置工作流首个 asset 未产出前
// 就被自身的文本账本行顶到配额上限（cmd/studiod TestEndToEndGenerationQuota429）。
func TestCountByOrgSinceExcludesText(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	orgID := "org_qtext_" + time.Now().Format("150405.000000000")
	pid := seedProject(t, db, orgID)
	// 2 个文本中间节点 + 1 个媒体生成，全在窗口内。
	for i := 0; i < 2; i++ {
		if err := s.Record(ctx, Generation{ProjectID: pid, Kind: "text", Provider: "p", Model: "m", Tokens: 100, CostMicros: 50}); err != nil {
			t.Fatalf("record text: %v", err)
		}
	}
	if err := s.Record(ctx, Generation{ProjectID: pid, Kind: "image", Provider: "p", Model: "m", ImageCount: 1, CostMicros: 2000}); err != nil {
		t.Fatalf("record image: %v", err)
	}
	// 配额只数媒体：3 条账本行里只有 1 条媒体。
	n, err := s.CountByOrgSince(ctx, orgID, time.Now().Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("CountByOrgSince = %d err=%v, want 1 (media only, text excluded)", n, err)
	}
}

// TestByPlan aggregates one run's ledger via the generations→todos join:
// multi-todo multi-kind seed data → totals + per-kind counts + per-todo
// breakdown; other-plan rows, other-project rows and todo-less rows (empty todo_id)
// stay excluded; an unknown plan returns the zero rollup with an empty slice.
func TestByPlan(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	orgID := "org_plan_" + time.Now().Format("150405.000000000")
	pid := seedProject(t, db, orgID)
	planID := "plan-" + time.Now().Format("150405.000000000")
	seedTodo := func(id, typ, plan string) {
		t.Helper()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type) VALUES ($1,$2,$3,$4)`,
			id, pid, plan, typ).Error; err != nil {
			t.Fatalf("seed todo %s: %v", id, err)
		}
	}
	tdScript := planID + "-t-script"
	tdImage := planID + "-t-image"
	tdOther := planID + "-t-other"
	seedTodo(tdScript, "script", planID)
	seedTodo(tdImage, "storyboard", planID)
	seedTodo(tdOther, "script", planID+"-other") // 另一个 plan 的 todo
	rec := func(g Generation) {
		t.Helper()
		if err := s.Record(ctx, g); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	// 目标 plan：1 次 chat（script 节点）+ 2 次 image（storyboard 节点，重生成 2 次）。
	rec(Generation{ProjectID: pid, TodoID: tdScript, Kind: "chat", Provider: "openai", Model: "gpt", Tokens: 800, CostMicros: 1600})
	rec(Generation{ProjectID: pid, TodoID: tdImage, Kind: "image", Provider: "fake", Model: "img", Tokens: 50, ImageCount: 1, CostMicros: 5000})
	rec(Generation{ProjectID: pid, TodoID: tdImage, Kind: "image", Provider: "fake", Model: "img", Tokens: 50, ImageCount: 1, CostMicros: 5000})
	// 噪音：另一 plan 的行、无 todo 的行（封面/朗读）、另一项目同 todo id 的行。
	rec(Generation{ProjectID: pid, TodoID: tdOther, Kind: "chat", Provider: "openai", Model: "gpt", Tokens: 999, CostMicros: 999})
	rec(Generation{ProjectID: pid, TodoID: "", Kind: "image", Provider: "fake", Model: "img", ImageCount: 1, CostMicros: 777})
	otherPid := seedProject(t, db, orgID)
	rec(Generation{ProjectID: otherPid, TodoID: tdScript, Kind: "chat", Provider: "openai", Model: "gpt", Tokens: 5, CostMicros: 5})

	pc, err := s.ByPlan(ctx, pid, planID)
	if err != nil {
		t.Fatalf("ByPlan: %v", err)
	}
	if pc.Generations != 3 || pc.Tokens != 900 || pc.ImageCount != 2 || pc.CostMicros != 11600 {
		t.Fatalf("totals mismatch: %+v", pc.Aggregate)
	}
	if pc.KindCounts["chat"] != 1 || pc.KindCounts["image"] != 2 || len(pc.KindCounts) != 2 {
		t.Fatalf("kind counts mismatch: %+v", pc.KindCounts)
	}
	if len(pc.Todos) != 2 {
		t.Fatalf("breakdown rows = %d, want 2: %+v", len(pc.Todos), pc.Todos)
	}
	// 按首次 created_at 排序：script 先于 storyboard。
	if pc.Todos[0].TodoID != tdScript || pc.Todos[0].TodoType != "script" ||
		pc.Todos[0].Kind != "chat" || pc.Todos[0].Tokens != 800 || pc.Todos[0].CostMicros != 1600 {
		t.Fatalf("script row mismatch: %+v", pc.Todos[0])
	}
	if pc.Todos[1].TodoID != tdImage || pc.Todos[1].TodoType != "storyboard" ||
		pc.Todos[1].Generations != 2 || pc.Todos[1].ImageCount != 2 || pc.Todos[1].CostMicros != 10000 {
		t.Fatalf("storyboard row mismatch: %+v", pc.Todos[1])
	}
	// 未知 plan → 零总计 + 空（非 nil）分解。
	empty, err := s.ByPlan(ctx, pid, "no-such-plan")
	if err != nil {
		t.Fatalf("ByPlan empty: %v", err)
	}
	if empty.Generations != 0 || empty.CostMicros != 0 || empty.Todos == nil || len(empty.Todos) != 0 {
		t.Fatalf("empty plan rollup mismatch: %+v", empty)
	}
}

// TestTextGenerationTokensSurfaceInPlanCost 复现并锁定「运行成本有费用但 Tokens=0」
// 的修复：文本生成节点（script/storyboard/custom-llm）现在会按 provider/model 定价落
// 一条 kind=text 账本行，token 来自模型响应 usage。此处用与 worker 相同的写入方式
// (RecordPriced + Kind=text) 落库，再经 ByPlan 断言 tokens 非零地汇总出来 —— 修复前
// 这些节点不落任何账本行，plan 汇总的 Tokens 恒为 0。
func TestTextGenerationTokensSurfaceInPlanCost(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	orgID := "org_text_" + time.Now().Format("150405.000000000")
	pid := seedProject(t, db, orgID)
	planID := "plan-text-" + time.Now().Format("150405.000000000")
	// deepseek chat 按 token 计费（无 per-image）。
	if err := db.WithContext(ctx).Exec(`INSERT INTO pricing (provider, model, kind, micros_per_1k_tokens)
		VALUES ('deepseek','deepseek-chat','text',2000) ON CONFLICT (provider, model) DO NOTHING`).Error; err != nil {
		t.Fatalf("seed pricing: %v", err)
	}
	tdText := planID + "-t-llm"
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type) VALUES ($1,$2,$3,$4)`,
		tdText, pid, planID, "custom:llm").Error; err != nil {
		t.Fatalf("seed todo: %v", err)
	}
	// worker 写入方式：无 image、无 asset，仅 token 用量。RecordPriced 依定价补 cost_micros。
	if err := s.RecordPriced(ctx, Generation{
		ProjectID: pid, TodoID: tdText, Kind: "text",
		Provider: "deepseek", Model: "deepseek-chat", Tokens: 1500,
	}); err != nil {
		t.Fatalf("RecordPriced text: %v", err)
	}
	pc, err := s.ByPlan(ctx, pid, planID)
	if err != nil {
		t.Fatalf("ByPlan: %v", err)
	}
	// 关键断言：Tokens 非零地汇总到 plan 成本；cost 按 token 定价 = 1500 * 2000/1000。
	if pc.Tokens != 1500 {
		t.Fatalf("plan tokens = %d, want 1500 (text gen tokens must surface)", pc.Tokens)
	}
	if pc.CostMicros != 1500*2000/1000 {
		t.Fatalf("plan cost_micros = %d, want %d", pc.CostMicros, 1500*2000/1000)
	}
	if pc.KindCounts["text"] != 1 || pc.ImageCount != 0 {
		t.Fatalf("kind/image mismatch: kinds=%+v imageCount=%d", pc.KindCounts, pc.ImageCount)
	}
	if len(pc.Todos) != 1 || pc.Todos[0].Kind != "text" || pc.Todos[0].Tokens != 1500 {
		t.Fatalf("per-todo breakdown mismatch: %+v", pc.Todos)
	}
}

// TestRecentByOrgPagination pages the ledger with a keyset cursor: 5 rows,
// 3 of them sharing the exact same created_at (tie broken by id DESC), limit=2
// → 3 pages, no duplicate, no omission, no drift across the tie boundary.
func TestRecentByOrgPagination(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	orgID := "org_page_" + time.Now().Format("150405.000000000")
	pid := seedProject(t, db, orgID)
	// Run-unique id prefix (re-runs must not hit the generations pkey); the
	// suffix alone decides id DESC ordering inside a created_at tie.
	prefix := "pg" + time.Now().Format("150405.000000000") + "-"
	// 2 rows at distinct times + 3 rows at the SAME timestamp (same-second drift risk).
	seed := func(suffix string, createdAt string) {
		t.Helper()
		if err := db.WithContext(ctx).Exec(`INSERT INTO generations
			(id, project_id, kind, provider, model, image_count, cost_micros, created_at)
			VALUES ($1, $2, 'image', 'p', 'm', 1, 100, $3::timestamptz)`, prefix+suffix, pid, createdAt).Error; err != nil {
			t.Fatalf("seed %s: %v", suffix, err)
		}
	}
	seed("newest", "2026-06-10T10:00:05Z")
	seed("tie-c", "2026-06-10T10:00:03Z")
	seed("tie-b", "2026-06-10T10:00:03Z")
	seed("tie-a", "2026-06-10T10:00:03Z")
	seed("oldest", "2026-06-10T10:00:01Z")

	var got []LedgerEntry
	cursor := ""
	pages := 0
	for {
		items, next, err := s.RecentByOrg(ctx, orgID, 2, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		got = append(got, items...)
		pages++
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatalf("cursor did not terminate")
		}
	}
	// Deterministic order: created_at DESC then id DESC inside the tie.
	wantIDs := []string{
		prefix + "newest", prefix + "tie-c", prefix + "tie-b", prefix + "tie-a", prefix + "oldest",
	}
	if len(got) != len(wantIDs) {
		t.Fatalf("paged rows = %d, want %d (dup or omission)", len(got), len(wantIDs))
	}
	seen := map[string]bool{}
	for i, e := range got {
		if seen[e.ID] {
			t.Fatalf("duplicate row %s across pages", e.ID)
		}
		seen[e.ID] = true
		if e.ID != wantIDs[i] {
			t.Fatalf("row %d = %s, want %s (order drift)", i, e.ID, wantIDs[i])
		}
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3 (2+2+1)", pages)
	}
	// Malformed cursor → ErrBadCursor.
	if _, _, err := s.RecentByOrg(ctx, orgID, 2, "not-a-cursor"); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("malformed cursor: err = %v, want ErrBadCursor", err)
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

// TestPerActorByOrg 验证成本「按成员」聚合：按 actor_user_id 分桶、贵者在前，空 actor
// 自成「未归属」桶；跨 org 的行不串桶；时间范围过滤生效。
func TestPerActorByOrg(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := New(db)
	orgID := "org_actor_" + time.Now().Format("150405.000000000")
	pid := seedProject(t, db, orgID)
	// 成员 A：两条（合计 300 micros / 30 tokens / 2 gen）。
	if err := s.Record(ctx, Generation{ProjectID: pid, Provider: "p", Model: "m", ImageCount: 1, CostMicros: 100, Tokens: 10, ActorUserID: "user-A"}); err != nil {
		t.Fatalf("record A1: %v", err)
	}
	if err := s.Record(ctx, Generation{ProjectID: pid, Provider: "p", Model: "m", ImageCount: 1, CostMicros: 200, Tokens: 20, ActorUserID: "user-A"}); err != nil {
		t.Fatalf("record A2: %v", err)
	}
	// 成员 B：一条（50 micros）。
	if err := s.Record(ctx, Generation{ProjectID: pid, Provider: "p", Model: "m", ImageCount: 1, CostMicros: 50, ActorUserID: "user-B"}); err != nil {
		t.Fatalf("record B: %v", err)
	}
	// 未归属（空 actor）：一条（70 micros）。
	if err := s.Record(ctx, Generation{ProjectID: pid, Provider: "p", Model: "m", ImageCount: 1, CostMicros: 70}); err != nil {
		t.Fatalf("record unattributed: %v", err)
	}
	// 另一个 org 的行不得串入本 org 的分桶。
	otherPid := seedProject(t, db, "org_actor_other_"+time.Now().Format("150405.000000000"))
	if err := s.Record(ctx, Generation{ProjectID: otherPid, Provider: "p", Model: "m", ImageCount: 1, CostMicros: 999, ActorUserID: "user-A"}); err != nil {
		t.Fatalf("record other-org: %v", err)
	}

	rows, err := s.PerActorByOrg(ctx, orgID, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("PerActorByOrg: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 buckets (A/B/未归属), got %d: %+v", len(rows), rows)
	}
	// 贵者在前：A(300) > 未归属(70) > B(50)。
	if rows[0].ActorUserID != "user-A" || rows[0].CostMicros != 300 || rows[0].Tokens != 30 || rows[0].Generations != 2 {
		t.Fatalf("row[0] = %+v, want user-A 300/30/2", rows[0])
	}
	if rows[1].ActorUserID != "" || rows[1].CostMicros != 70 {
		t.Fatalf("row[1] = %+v, want unattributed(空 actor)/70", rows[1])
	}
	if rows[2].ActorUserID != "user-B" || rows[2].CostMicros != 50 {
		t.Fatalf("row[2] = %+v, want user-B/50", rows[2])
	}
}
