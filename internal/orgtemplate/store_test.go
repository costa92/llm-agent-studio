package orgtemplate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testGorm(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run org template store tests")
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

func randID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sampleInput(name string) SaveInput {
	return SaveInput{
		Name:         name,
		Description:  "desc-" + name,
		Nodes:        json.RawMessage(`[{"id":"n1","type":"script","dependsOn":[]}]`),
		InputsSchema: json.RawMessage(`[{"name":"theme","target":"brief"}]`),
		Settings:     json.RawMessage(`{"style":"anime"}`),
		CreatedBy:    "u1",
	}
}

// TestSaveGet 断言 Save→Get 往返，JSONB 三列保真。
func TestSaveGet(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	saved, err := New(db).Save(context.Background(), org, sampleInput("我的模板"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.ID == "" || saved.OrgID != org || saved.CreatedAt.IsZero() {
		t.Fatalf("bad saved row: %+v", saved)
	}
	got, err := New(db).Get(context.Background(), org, saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "我的模板" || got.Description != "desc-我的模板" || got.CreatedBy != "u1" {
		t.Fatalf("scalar mismatch: %+v", got)
	}
	// JSONB 保真（语义相等，避免空白差异）。
	assertJSONEq(t, got.Nodes, `[{"id":"n1","type":"script","dependsOn":[]}]`)
	assertJSONEq(t, got.InputsSchema, `[{"name":"theme","target":"brief"}]`)
	assertJSONEq(t, got.Settings, `{"style":"anime"}`)
}

// TestSaveNormalizesEmptyJSON 断言 nil JSON 列被兜成 '[]'/'{}'（永不 NULL）。
func TestSaveNormalizesEmptyJSON(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	saved, err := New(db).Save(context.Background(), org, SaveInput{Name: "空"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := New(db).Get(context.Background(), org, saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertJSONEq(t, got.Nodes, `[]`)
	assertJSONEq(t, got.InputsSchema, `[]`)
	assertJSONEq(t, got.Settings, `{}`)
}

// TestListByOrgOrder 断言 ListByOrg 按 updated_at DESC（最近在前）。
func TestListByOrgOrder(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	s := New(db)
	a, _ := s.Save(context.Background(), org, sampleInput("A"))
	b, _ := s.Save(context.Background(), org, sampleInput("B"))
	rows, err := s.ListByOrg(context.Background(), org)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	// 后存的 b 的 updated_at >= a，DESC 下 b 在前（相等则 id 稳定即可，这里 now() 递增）。
	if rows[0].ID != b.ID || rows[1].ID != a.ID {
		t.Fatalf("order wrong: got [%s,%s] want [%s,%s]", rows[0].ID, rows[1].ID, b.ID, a.ID)
	}
}

// TestOrgIsolation 断言严格 org 隔离：A org 存的模板对 B org 的 Get/Delete 均 ErrNotFound、
// ListByOrg(B) 不含。
func TestOrgIsolation(t *testing.T) {
	db := testGorm(t)
	orgA, orgB := randID(t), randID(t)
	s := New(db)
	saved, _ := s.Save(context.Background(), orgA, sampleInput("A 私有"))

	if _, err := s.Get(context.Background(), orgB, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Get should be ErrNotFound, got %v", err)
	}
	if err := s.Delete(context.Background(), orgB, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Delete should be ErrNotFound, got %v", err)
	}
	rowsB, err := s.ListByOrg(context.Background(), orgB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(rowsB) != 0 {
		t.Fatalf("orgB list should be empty, got %d", len(rowsB))
	}
	// A org 仍能读到（跨 org Delete 没误删）。
	if _, err := s.Get(context.Background(), orgA, saved.ID); err != nil {
		t.Fatalf("orgA Get after cross-org attempts: %v", err)
	}
}

// TestDelete 断言删除后 Get→ErrNotFound、重复 Delete→ErrNotFound。
func TestDelete(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	s := New(db)
	saved, _ := s.Save(context.Background(), org, sampleInput("待删"))

	if err := s.Delete(context.Background(), org, saved.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(context.Background(), org, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete should be ErrNotFound, got %v", err)
	}
	if err := s.Delete(context.Background(), org, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat delete should be ErrNotFound, got %v", err)
	}
}

func assertJSONEq(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("unmarshal want %s: %v", want, err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("JSON mismatch: got %s want %s", gb, wb)
	}
}
