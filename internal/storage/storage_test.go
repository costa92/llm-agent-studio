package storage

import (
	"context"
	"os"
	"testing"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run storage tests")
	}
	return dsn
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
