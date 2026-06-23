package customnodetype

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
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run custom node type store tests")
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

func llmInput(label string) UpsertInput {
	params, _ := json.Marshal(map[string]any{"systemPrompt": "sys", "userPrompt": "{{x}}", "outputFormat": "text"})
	return UpsertInput{Slug: "", Label: label, Color: "#7c93ff", Kind: "llm", Params: params}
}

func TestCreateListGet(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	ct, err := New(db).Create(context.Background(), org, llmInput("翻译"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ct.Slug == "" || ct.Kind != "llm" || ct.OrgID != org {
		t.Fatalf("bad row: %+v", ct)
	}
	got, err := New(db).Get(context.Background(), ct.ID, org)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != ct.ID {
		t.Fatalf("get mismatch")
	}
	items, err := New(db).List(context.Background(), org)
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v len=%d", err, len(items))
	}
}

func TestOrgIsolation(t *testing.T) {
	db := testGorm(t)
	orgA, orgB := randID(t), randID(t)
	ct, _ := New(db).Create(context.Background(), orgA, llmInput("A 类型"))
	if _, err := New(db).Get(context.Background(), ct.ID, orgB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Get should be ErrNotFound, got %v", err)
	}
	if _, err := New(db).Update(context.Background(), ct.ID, orgB, llmInput("hijack")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Update should be ErrNotFound, got %v", err)
	}
	if err := New(db).Delete(context.Background(), ct.ID, orgB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Delete should be ErrNotFound, got %v", err)
	}
}

func TestSlugUnique(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	if _, err := New(db).Create(context.Background(), org, llmInput("同名")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := New(db).Create(context.Background(), org, llmInput("同名")); err == nil {
		t.Fatalf("duplicate slug should fail")
	}
}

func TestDeleteInUse(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	ct, err := New(db).Create(context.Background(), org, llmInput("删除测试类型"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Insert a project + workflow that references this type via typeId in nodes JSONB.
	projID := randID(t)
	if res := db.Exec(
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1, $2, 'test', 'test')`,
		projID, org); res.Error != nil {
		t.Fatalf("insert project: %v", res.Error)
	}
	wfID := randID(t)
	nodesJSON := `[{"id":"n1","type":"custom:llm","typeId":"` + ct.ID + `"}]`
	if res := db.Exec(
		`INSERT INTO workflows (id, project_id, name, nodes) VALUES ($1, $2, 'wf', $3::jsonb)`,
		wfID, projID, nodesJSON); res.Error != nil {
		t.Fatalf("insert workflow: %v", res.Error)
	}

	// Delete should return ErrInUse.
	if err := New(db).Delete(context.Background(), ct.ID, org); !errors.Is(err, ErrInUse) {
		t.Fatalf("Delete with in-use ref should be ErrInUse, got %v", err)
	}

	// Remove the workflow and now delete should succeed.
	if res := db.Exec(`DELETE FROM workflows WHERE id=$1`, wfID); res.Error != nil {
		t.Fatalf("cleanup workflow: %v", res.Error)
	}
	if err := New(db).Delete(context.Background(), ct.ID, org); err != nil {
		t.Fatalf("Delete after removing ref should succeed, got %v", err)
	}
}

// TestValidate_HTTP exercises http kind save-time validation directly (no DB):
// method enum, static url (no {{}}), {{secret:}} only in headers, outputFormat enum.
func TestValidate_HTTP(t *testing.T) {
	mk := func(params string) UpsertInput {
		return UpsertInput{Label: "调接口", Kind: "http", Params: json.RawMessage(params)}
	}
	ok := `{"method":"POST","url":"https://api.example.com","headers":{"Authorization":"Bearer {{secret:K}}","X-Q":"{{draft}}"},"bodyTemplate":"{\"q\":\"{{draft}}\"}","outputFormat":"json"}`
	if err := validate(mk(ok)); err != nil {
		t.Fatalf("valid http params rejected: %v", err)
	}
	bad := map[string]string{
		"bad method":       `{"method":"TRACE","url":"https://x.com"}`,
		"templated url":    `{"method":"GET","url":"https://{{host}}/x"}`,
		"secret in url":    `{"method":"GET","url":"https://x.com/{{secret:K}}"}`,
		"secret in body":   `{"method":"POST","url":"https://x.com","bodyTemplate":"{{secret:K}}"}`,
		"bad outputFormat": `{"method":"GET","url":"https://x.com","outputFormat":"xml"}`,
		"missing url":      `{"method":"GET"}`,
	}
	for name, p := range bad {
		if err := validate(mk(p)); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}
