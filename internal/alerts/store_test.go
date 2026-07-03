package alerts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func randHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func openStorage(t *testing.T) *storage.Storage {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run alerts tests")
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
	return st
}

func TestAlertSettingsStore(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	store := NewStore(st.GORM())
	orgID := "alorg_" + randHex(t)

	// 1. 未配置的 org：零值默认（enabled=false），不是错误。
	got, err := store.Get(ctx, orgID)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if got.OrgID != orgID || got.Email != "" || got.Enabled {
		t.Fatalf("unexpected default settings: %+v", got)
	}

	// 2. Upsert 建行。
	got, err = store.Upsert(ctx, orgID, UpsertInput{Email: "ops@example.com", Enabled: true})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.Email != "ops@example.com" || !got.Enabled {
		t.Fatalf("unexpected settings after upsert: %+v", got)
	}

	// 3. Upsert 更新（同 org 一行，org_id 冲突走 UPDATE）。
	got, err = store.Upsert(ctx, orgID, UpsertInput{Email: "oncall@example.com", Enabled: false})
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if got.Email != "oncall@example.com" || got.Enabled {
		t.Fatalf("unexpected settings after update: %+v", got)
	}

	// 4. Get 回读与 Upsert 返回一致。
	back, err := store.Get(ctx, orgID)
	if err != nil {
		t.Fatalf("get back: %v", err)
	}
	if back != got {
		t.Fatalf("get %+v != upsert result %+v", back, got)
	}
}
