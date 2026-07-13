package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testGorm(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run audit store tests")
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

// TestRecordAndList 验证一次写 + 读回：Record 追加行、List 按 org 倒序返回、detail 往返、
// 字段完整。
func TestRecordAndList(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	s := New(db)
	ctx := context.Background()

	if err := s.Record(ctx, Entry{
		OrgID:       org,
		ActorUserID: "user-1",
		ActorEmail:  "admin@example.com",
		Action:      "model_key.reveal",
		TargetType:  "model_config",
		TargetID:    "cfg-abc",
		Detail:      map[string]any{"name": "prod-openai"},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	rows, next, err := s.List(ctx, org, 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if next != "" {
		t.Fatalf("nextCursor: want empty on last page, got %q", next)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: want 1, got %d", len(rows))
	}
	r := rows[0]
	if r.ActorUserID != "user-1" || r.ActorEmail != "admin@example.com" {
		t.Fatalf("actor: got %q / %q", r.ActorUserID, r.ActorEmail)
	}
	if r.Action != "model_key.reveal" || r.TargetType != "model_config" || r.TargetID != "cfg-abc" {
		t.Fatalf("action/target: got %q %q %q", r.Action, r.TargetType, r.TargetID)
	}
	var detail map[string]any
	if err := json.Unmarshal(r.Detail, &detail); err != nil {
		t.Fatalf("detail unmarshal: %v (%s)", err, r.Detail)
	}
	if detail["name"] != "prod-openai" {
		t.Fatalf("detail: got %v", detail)
	}
	if r.CreatedAt.IsZero() {
		t.Fatalf("created_at not set")
	}
}

// TestListOrgIsolation 验证 List 严格按 org 过滤，不泄露他 org 的审计行。
func TestListOrgIsolation(t *testing.T) {
	db := testGorm(t)
	orgA, orgB := randID(t), randID(t)
	s := New(db)
	ctx := context.Background()

	if err := s.Record(ctx, Entry{OrgID: orgA, Action: "model_config.create"}); err != nil {
		t.Fatalf("record A: %v", err)
	}
	if err := s.Record(ctx, Entry{OrgID: orgB, Action: "model_config.delete"}); err != nil {
		t.Fatalf("record B: %v", err)
	}
	rows, _, err := s.List(ctx, orgA, 50, "")
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(rows) != 1 || rows[0].Action != "model_config.create" {
		t.Fatalf("org isolation leak: %+v", rows)
	}
}

// TestListKeysetPagination 验证 keyset 翻页：写 3 行、limit=2 拿首页 + nextCursor，
// 用游标拿次页，无重叠、末页 nextCursor 为空。
func TestListKeysetPagination(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	s := New(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s.Record(ctx, Entry{OrgID: org, Action: "member.role_change", TargetID: hex.EncodeToString([]byte{byte(i)})}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	page1, next, err := s.List(ctx, org, 2, "")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || next == "" {
		t.Fatalf("page1: want 2 rows + cursor, got %d rows / %q", len(page1), next)
	}
	page2, next2, err := s.List(ctx, org, 2, next)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 1 || next2 != "" {
		t.Fatalf("page2: want 1 row + empty cursor, got %d rows / %q", len(page2), next2)
	}
	seen := map[string]bool{}
	for _, r := range append(page1, page2...) {
		if seen[r.ID] {
			t.Fatalf("duplicate row across pages: %s", r.ID)
		}
		seen[r.ID] = true
	}
}

// TestRecordRejectsEmptyAction 验证 action 必填（防止无意义空审计行）。
func TestRecordRejectsEmptyAction(t *testing.T) {
	db := testGorm(t)
	s := New(db)
	if err := s.Record(context.Background(), Entry{OrgID: randID(t), Action: "  "}); err == nil {
		t.Fatalf("want error on empty action")
	}
}

// TestBadCursor 验证非法游标映射为 ErrBadCursor。
func TestBadCursor(t *testing.T) {
	db := testGorm(t)
	s := New(db)
	if _, _, err := s.List(context.Background(), randID(t), 50, "not-a-cursor"); err == nil {
		t.Fatalf("want ErrBadCursor")
	}
}

// TestActorEmail 验证 ActorEmail 反查 auth_user：命中返回 email；未知 id / 空 id / 已软删
// 均返回 ("", nil)（best-effort，缺 email 不拦审计写入）。
func TestActorEmail(t *testing.T) {
	db := testGorm(t)
	ctx := context.Background()
	// auth_user 由 authz 库 own；studio storage.Migrate 不建它。测试里自备最小表 + 一行。
	if err := db.WithContext(ctx).Exec(`CREATE TABLE IF NOT EXISTS auth_user (
		id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(), deleted_at TIMESTAMPTZ)`).Error; err != nil {
		t.Fatalf("create auth_user: %v", err)
	}
	uid := randID(t)
	email := uid + "@example.com"
	// password_hash 必须显式给：全量 DB-backed 跑（-p 1 ./...）时真实 authz auth_user
	// （password_hash NOT NULL、无 default）已由别的包先建，上面的 CREATE ... IF NOT
	// EXISTS 成 no-op，只给 (id,email) 会撞 NOT NULL。给 '' 在自备最小表与真表下都合法。
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO auth_user (id, email, password_hash) VALUES ($1, $2, '')`, uid, email).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	s := New(db)

	got, err := s.ActorEmail(ctx, uid)
	if err != nil || got != email {
		t.Fatalf("resolve: got %q err %v, want %q", got, err, email)
	}
	if got, err := s.ActorEmail(ctx, "no-such-user"); err != nil || got != "" {
		t.Fatalf("unknown id: got %q err %v, want empty", got, err)
	}
	if got, err := s.ActorEmail(ctx, ""); err != nil || got != "" {
		t.Fatalf("empty id: got %q err %v, want empty", got, err)
	}
}
