package models

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCatalogListsImageProviders(t *testing.T) {
	cat := Catalog()
	// spec §13 R3: only openai/google/minimax/volcengine image providers.
	want := map[string]bool{"openai": false, "google": false, "minimax": false, "volcengine": false}
	for _, e := range cat {
		if _, ok := want[e.Provider]; ok {
			want[e.Provider] = true
		}
		if e.Kind != "image" {
			t.Fatalf("M2 catalog is image-only, got kind %q", e.Kind)
		}
	}
	for p, seen := range want {
		if !seen {
			t.Fatalf("catalog missing provider %q", p)
		}
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run model store tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestCreateAndListByOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	mc, err := st.Create(ctx, CreateInput{
		OrgID: "org-m", Kind: "image", Provider: "openai", Model: "gpt-image-1",
		Enabled: true, IsDefault: true,
	})
	if err != nil || mc.ID == "" {
		t.Fatalf("create: %v %+v", err, mc)
	}
	list, err := st.ListByOrg(ctx, "org-m")
	if err != nil || len(list) != 1 || list[0].Provider != "openai" {
		t.Fatalf("list: %v %+v", err, list)
	}
}

func TestDefaultForOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	_, _ = st.Create(ctx, CreateInput{OrgID: "org-d", Kind: "image", Provider: "minimax", Model: "image-01", Enabled: true, IsDefault: false})
	_, _ = st.Create(ctx, CreateInput{OrgID: "org-d", Kind: "image", Provider: "openai", Model: "gpt-image-1", Enabled: true, IsDefault: true})
	prov, model, ok, err := st.DefaultForOrg(ctx, "org-d", "image")
	if err != nil || !ok || prov != "openai" || model != "gpt-image-1" {
		t.Fatalf("default: %v ok=%v %s/%s", err, ok, prov, model)
	}
}
