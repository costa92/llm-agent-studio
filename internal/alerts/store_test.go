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

// TestAlertSettingsStore_OperationalFields 验证 m29 新增的运营告警字段
// （成本/卡顿/积压开关 + 阈值）的写读往返，且 Upsert 全字段透传。
func TestAlertSettingsStore_OperationalFields(t *testing.T) {
	st := openStorage(t)
	ctx := context.Background()
	store := NewStore(st.GORM())
	orgID := "alorg_" + randHex(t)

	// 未配置的 org：运营字段全零值默认（关闭）。
	got, err := store.Get(ctx, orgID)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if got.BudgetEnabled || got.StuckEnabled || got.BacklogEnabled {
		t.Fatalf("operational alerts must default OFF: %+v", got)
	}

	in := UpsertInput{
		Email:                 "ops@example.com",
		Enabled:               false, // run 失败告警关，仅开运营告警
		BudgetEnabled:         true,
		BudgetThresholdMicros: 50_000_000, // ¥50
		BudgetWindowHours:     12,
		StuckEnabled:          true,
		StuckThresholdMinutes: 45,
		BacklogEnabled:        true,
		BacklogThreshold:      100,
	}
	got, err = store.Upsert(ctx, orgID, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	want := Settings{
		OrgID: orgID, Email: "ops@example.com",
		BudgetEnabled: true, BudgetThresholdMicros: 50_000_000, BudgetWindowHours: 12,
		StuckEnabled: true, StuckThresholdMinutes: 45,
		BacklogEnabled: true, BacklogThreshold: 100,
	}
	if got != want {
		t.Fatalf("upsert result %+v != want %+v", got, want)
	}
	back, err := store.Get(ctx, orgID)
	if err != nil {
		t.Fatalf("get back: %v", err)
	}
	if back != want {
		t.Fatalf("get back %+v != want %+v", back, want)
	}

	// ListOperational 应包含本 org（有邮箱 + 至少一类运营告警开）。
	ops, err := store.ListOperational(ctx)
	if err != nil {
		t.Fatalf("list operational: %v", err)
	}
	found := false
	for _, s := range ops {
		if s.OrgID == orgID {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListOperational missing org %s", orgID)
	}

	// 全部运营告警关闭后应从 ListOperational 消失。
	if _, err := store.Upsert(ctx, orgID, UpsertInput{Email: "ops@example.com"}); err != nil {
		t.Fatalf("upsert disable: %v", err)
	}
	ops, err = store.ListOperational(ctx)
	if err != nil {
		t.Fatalf("list operational 2: %v", err)
	}
	for _, s := range ops {
		if s.OrgID == orgID {
			t.Fatalf("disabled org must not appear in ListOperational: %+v", s)
		}
	}
}
