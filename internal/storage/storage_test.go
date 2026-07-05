package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run storage tests")
	}
	return dsn
}

func TestOpenExposesGORM(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run storage tests")
	}
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if st.GORM() == nil {
		t.Fatal("GORM() returned nil")
	}
	var n int
	if err := st.GORM().Raw("SELECT 1").Scan(&n).Error; err != nil {
		t.Fatalf("gorm raw select: %v", err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", n)
	}
	var m int
	if err := st.Pool().QueryRow(ctx, "SELECT 1").Scan(&m); err != nil || m != 1 {
		t.Fatalf("pool select: m=%d err=%v", m, err)
	}
}

func TestMigrateIsIdempotentAndCreatesM1Tables(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 1: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 2 (idempotent): %v", err)
	}
	for _, tbl := range []string{"projects", "plans", "todos", "scripts", "shots", "run_events"} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name=$1)`, tbl).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("table %s not created", tbl)
		}
	}
}

func TestMigrateCreatesM2Tables(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"assets", "generations", "model_configs"} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name=$1)`, tbl).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("table %s not created", tbl)
		}
	}
	// assets carries the lineage + library-search columns.
	for _, col := range []string{"parent_asset_id", "version", "blob_key", "tags", "status"} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='assets' AND column_name=$1)`, col).Scan(&exists); err != nil {
			t.Fatalf("check assets.%s: %v", col, err)
		}
		if !exists {
			t.Fatalf("assets.%s missing", col)
		}
	}
}

func TestMigrateCreatesM3Surfaces(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// pricing table exists and is seeded with the catalog unit prices.
	var n int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM pricing`).Scan(&n); err != nil {
		t.Fatalf("pricing table missing: %v", err)
	}
	if n < 5 {
		t.Fatalf("pricing not seeded: %d rows, want >= 5", n)
	}
	// assets carries the ReviewAgent prescreen columns.
	for _, col := range []string{"prescreen_score", "prescreen_flags", "prescreen_note"} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='assets' AND column_name=$1)`, col).Scan(&exists); err != nil {
			t.Fatalf("check assets.%s: %v", col, err)
		}
		if !exists {
			t.Fatalf("assets.%s missing", col)
		}
	}
}

func TestMigrateCreatesM5ByokColumns(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// BYOK per-config credential columns on model_configs.
	for _, col := range []string{"base_url", "api_key_enc"} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='model_configs' AND column_name=$1)`, col).Scan(&exists); err != nil {
			t.Fatalf("check model_configs.%s: %v", col, err)
		}
		if !exists {
			t.Fatalf("model_configs.%s missing", col)
		}
	}
}

func TestMigrateCreatesM4Surfaces(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// todos.poll_attempts (async poll budget, separate from claim attempts).
	for _, c := range []struct{ table, col string }{
		{"todos", "poll_attempts"},
		{"assets", "external_job_id"},
		{"assets", "submitted_at"},
		{"pricing", "micros_per_second"},
	} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`,
			c.table, c.col).Scan(&exists); err != nil {
			t.Fatalf("check %s.%s: %v", c.table, c.col, err)
		}
		if !exists {
			t.Fatalf("%s.%s missing", c.table, c.col)
		}
	}
	// Partial unique indexes (B1 assets_todo_uniq + B3 generations_asset_todo_uniq).
	for _, idx := range []string{"assets_todo_uniq", "generations_asset_todo_uniq"} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname=$1)`, idx).Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", idx, err)
		}
		if !exists {
			t.Fatalf("index %s missing", idx)
		}
	}
	// assets_todo_uniq rejects a duplicate non-empty todo_id (and allows blanks).
	// M1: generate ids inline via md5(random()::text) — no dependency on a
	// randHex3() helper (which is NOT in the storage package; copying it would
	// fail to compile). Insert the project letting Postgres mint both ids, then
	// read back the todo id we need to assert the unique-index collision.
	var pid string
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'o','n','u') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var tid string
	if err := st.Pool().QueryRow(ctx, `SELECT md5(random()::text)`).Scan(&tid); err != nil {
		t.Fatalf("mint todo id: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, status) VALUES (md5(random()::text),$1,$2,'generating')`, pid, tid); err != nil {
		t.Fatalf("seed asset 1: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, status) VALUES (md5(random()::text),$1,$2,'generating')`, pid, tid); err == nil {
		t.Fatalf("duplicate non-empty todo_id must violate assets_todo_uniq")
	}
	// Two empty-todo_id assets coexist (regenerate v2 carries todo_id='').
	for i := 0; i < 2; i++ {
		if _, err := st.Pool().Exec(ctx,
			`INSERT INTO assets (id, project_id, todo_id, status) VALUES (md5(random()::text),$1,'','generating')`, pid); err != nil {
			t.Fatalf("empty todo_id asset %d must be allowed: %v", i, err)
		}
	}
	// Video/audio prices seeded.
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pricing WHERE kind IN ('video','audio') AND micros_per_second > 0`).Scan(&n); err != nil {
		t.Fatalf("pricing per-second: %v", err)
	}
	if n < 4 {
		t.Fatalf("per-second pricing not seeded: %d rows, want >= 4", n)
	}
}

// TestM15BackfillStorageConfigID covers the m15 backfill: legacy assets
// (storage_config_id=”) get stamped with the matching enabled storage_configs.id
// for their project's (org,mode); rows with no matching config (builtin/no-config
// orgs) get the "builtin" sentinel. The backfill is guarded on =” so re-running
// migrate over freshly-inserted legacy rows applies it (idempotent).
func TestM15BackfillStorageConfigID(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()

	suf := func() string { return randHex(6) }
	cfgOrg := "org-cfg-" + suf()
	builtinOrg := "org-builtin-" + suf()
	cfgID := "cfg-" + suf()

	// Configured org: an enabled s3 storage_configs row + a project on mode s3.
	if _, err := pool.Exec(ctx,
		`INSERT INTO storage_configs (id, scope, org_id, mode, bucket, endpoint, enabled)
		 VALUES ($1,'org',$2,'s3','b','https://e',true)`, cfgID, cfgOrg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfgProj := "proj-cfg-" + suf()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, storage_mode) VALUES ($1,$2,'p','u','s3')`,
		cfgProj, cfgOrg); err != nil {
		t.Fatalf("seed cfg project: %v", err)
	}
	// Builtin org: a project with no storage_configs row (storage_mode '').
	builtinProj := "proj-builtin-" + suf()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, storage_mode) VALUES ($1,$2,'p','u','')`,
		builtinProj, builtinOrg); err != nil {
		t.Fatalf("seed builtin project: %v", err)
	}

	// Legacy assets: storage_config_id='' (simulate pre-m15 rows).
	cfgAsset := "asset-cfg-" + suf()
	builtinAsset := "asset-builtin-" + suf()
	for _, row := range []struct{ id, proj string }{{cfgAsset, cfgProj}, {builtinAsset, builtinProj}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO assets (id, project_id, type, status, blob_key, storage_config_id)
			 VALUES ($1,$2,'image','accepted','k.png','')`, row.id, row.proj); err != nil {
			t.Fatalf("seed asset %s: %v", row.id, err)
		}
	}

	// Re-run migrate: the guarded backfill claims the freshly-inserted legacy rows.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate (backfill): %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx, `SELECT storage_config_id FROM assets WHERE id=$1`, cfgAsset).Scan(&got); err != nil {
		t.Fatalf("read cfg asset: %v", err)
	}
	if got != cfgID {
		t.Fatalf("configured-org asset should get config id %q, got %q", cfgID, got)
	}
	if err := pool.QueryRow(ctx, `SELECT storage_config_id FROM assets WHERE id=$1`, builtinAsset).Scan(&got); err != nil {
		t.Fatalf("read builtin asset: %v", err)
	}
	if got != "builtin" {
		t.Fatalf("builtin-org asset should get 'builtin' sentinel, got %q", got)
	}
}

func TestMigrateCreatesSchemaMigrationsTable(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 1: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 2 (idempotent): %v", err)
	}
	var exists bool
	if err := st.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='schema_migrations')`).Scan(&exists); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if !exists {
		t.Fatal("schema_migrations table not created")
	}
}

func TestMigrateLegacyDDLStillApplied(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"node_outputs", "custom_node_types", "org_secrets", "workflows"} {
		var exists bool
		if err := st.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name=$1)`, tbl).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", tbl, err)
		}
		if !exists {
			t.Fatalf("legacy table %s missing after runner rewrite", tbl)
		}
	}
}

func TestGoStepRunsOnceAndIsRecorded(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	var calls int
	st.testGoSteps = []migrationStep{{
		version: "test_counter_step",
		run: func(ctx context.Context, tx pgx.Tx) error {
			calls++
			_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS _p2a_probe (n INT)`)
			return err
		},
	}}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 1: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 2: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Go step ran %d times, want exactly 1", calls)
	}
	var recorded bool
	if err := st.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version='test_counter_step')`).Scan(&recorded); err != nil {
		t.Fatalf("check recorded: %v", err)
	}
	if !recorded {
		t.Fatal("Go step not recorded in schema_migrations")
	}
}

func TestM21AddsItemsColumnAndBackfillsFormatAware(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Simulate a pre-P2a DB: legacy DDL only, NO Go steps → node_outputs has no items column.
	st.testGoSteps = []migrationStep{}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("pre-migrate: %v", err)
	}
	var pid string
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org','p','u') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rows := []struct{ id, content, format string }{
		{"no_json_ok", `{"score":80,"flags":[]}`, "json"},
		{"no_json_bad", `{not valid json`, "json"},
		{"no_text", `hello world`, "text"},
	}
	for _, r := range rows {
		if _, err := st.Pool().Exec(ctx,
			`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format) VALUES ($1,$2,'t','custom:x',$3,$4)`,
			r.id, pid, r.content, r.format); err != nil {
			t.Fatalf("seed %s: %v", r.id, err)
		}
	}

	// Enable the real m21 (production goSteps) and re-migrate. In the shared-DB
	// suite an earlier test may already have applied m21; clear its record so the
	// step re-runs against THESE freshly-seeded legacy rows (re-run is safe — m21
	// only touches rows still at the '[]' default).
	if _, err := st.Pool().Exec(ctx, `DELETE FROM schema_migrations WHERE version='m21'`); err != nil {
		t.Fatalf("reset m21 record: %v", err)
	}
	st.testGoSteps = nil
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate with m21: %v", err)
	}

	get := func(id string) string {
		var items string
		if err := st.Pool().QueryRow(ctx, `SELECT items::text FROM node_outputs WHERE id=$1`, id).Scan(&items); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return items
	}
	var ok []struct {
		JSON struct {
			Score int `json:"score"`
		} `json:"json"`
	}
	if err := json.Unmarshal([]byte(get("no_json_ok")), &ok); err != nil {
		t.Fatalf("decode no_json_ok items: %v", err)
	}
	if len(ok) != 1 || ok[0].JSON.Score != 80 {
		t.Fatalf("json backfill lost $json.score: %v", ok)
	}
	if !strings.Contains(get("no_json_bad"), "_parseError") {
		t.Errorf("invalid json row missing _parseError fallback: %s", get("no_json_bad"))
	}
	var tx []struct {
		JSON struct {
			Text string `json:"text"`
		} `json:"json"`
	}
	if err := json.Unmarshal([]byte(get("no_text")), &tx); err != nil {
		t.Fatalf("decode no_text items: %v", err)
	}
	if len(tx) != 1 || tx[0].JSON.Text != "hello world" {
		t.Fatalf("text backfill wrong: %v", tx)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 3 (idempotent): %v", err)
	}
}

// TestM27StripsPBConfigInputs covers the m27 cleanup: existing workflows whose
// inputs_schema carries retired target='pbConfig' fields get those fields
// stripped (other fields preserved verbatim), an all-pbConfig schema collapses
// to '[]', and pbConfig-free rows are left untouched. Re-running migrate is a
// no-op (idempotent).
func TestM27StripsPBConfigInputs(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, Config{PGURL: testDSN(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()

	var pid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org','p','u') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seed := func(id, schema string) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO workflows (id, project_id, name, nodes, inputs_schema) VALUES ($1,$2,'wf','[]',$3::jsonb)`,
			id, pid, schema); err != nil {
			t.Fatalf("seed workflow %s: %v", id, err)
		}
	}
	mixed := "wf-mixed-" + randHex(6)
	allPB := "wf-allpb-" + randHex(6)
	clean := "wf-clean-" + randHex(6)
	seed(mixed, `[{"name":"heroName","type":"text","target":"variable"},{"name":"themes","type":"multiselect","target":"pbConfig","options":[{"value":"a"}]},{"name":"brief","type":"textarea","target":"brief"}]`)
	seed(allPB, `[{"name":"voice","type":"select","target":"pbConfig","options":[{"value":"warm"}]}]`)
	seed(clean, `[{"name":"tone","type":"select","target":"variable","options":[{"value":"warm"}]}]`)

	// In the shared-DB suite an earlier test run may already have recorded m27;
	// clear its record so the step re-runs against THESE freshly-seeded rows
	// (re-run is safe — the UPDATE only matches rows still carrying pbConfig).
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version='m27'`); err != nil {
		t.Fatalf("reset m27 record: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate with m27: %v", err)
	}

	get := func(id string) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT inputs_schema::text FROM workflows WHERE id=$1`, id).Scan(&s); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return s
	}
	var fields []struct {
		Name   string `json:"name"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal([]byte(get(mixed)), &fields); err != nil {
		t.Fatalf("decode mixed schema: %v", err)
	}
	if len(fields) != 2 || fields[0].Name != "heroName" || fields[1].Name != "brief" {
		t.Fatalf("mixed schema should keep exactly heroName+brief in order, got %s", get(mixed))
	}
	for _, f := range fields {
		if f.Target == "pbConfig" {
			t.Fatalf("pbConfig field survived m27: %s", get(mixed))
		}
	}
	if got := get(allPB); got != "[]" {
		t.Fatalf("all-pbConfig schema should collapse to [], got %s", got)
	}
	if !strings.Contains(get(clean), `"tone"`) {
		t.Fatalf("pbConfig-free schema should be untouched, got %s", get(clean))
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 2 (idempotent): %v", err)
	}
}
