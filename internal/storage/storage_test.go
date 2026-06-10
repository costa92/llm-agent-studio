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
