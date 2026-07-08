package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/audit"
)

// fakeRecorder captures audit entries; recErr lets a test force a write failure.
// email 非空时 fakeRecorder 也满足 actorEmailResolver，用来验证 audited 回填 actor_email。
type fakeRecorder struct {
	entries []audit.Entry
	recErr  error
	email   string
}

func (f *fakeRecorder) Record(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return f.recErr
}

func (f *fakeRecorder) ActorEmail(_ context.Context, _ string) (string, error) {
	return f.email, nil
}

// TestAuditedRecordsOnSuccess: 2xx 的管理动作追加一条审计行，action/target 正确捕获。
func TestAuditedRecordsOnSuccess(t *testing.T) {
	rec := &fakeRecorder{}
	inner := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hasApiKey":true,"apiKey":"sk-secret"}`))
	}
	h := audited(rec, "model_key.reveal", "model_config", inner)

	req := httptest.NewRequest(http.MethodGet, "/api/orgs/org1/model-configs/cfg9/reveal", nil)
	req.SetPathValue("org", "org1")
	req.SetPathValue("id", "cfg9")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("entries: want 1, got %d", len(rec.entries))
	}
	e := rec.entries[0]
	if e.Action != "model_key.reveal" || e.TargetType != "model_config" || e.TargetID != "cfg9" || e.OrgID != "org1" {
		t.Fatalf("entry: %+v", e)
	}
	// detail 必须最小、非敏感——绝不含明文密钥。
	if e.Detail != nil {
		t.Fatalf("detail should be nil (minimal), got %v", e.Detail)
	}
}

// TestAuditedFailedRecordDoesNotBreakAction: 审计写失败绝不影响已返回给客户端的响应
// （best-effort）。
func TestAuditedFailedRecordDoesNotBreakAction(t *testing.T) {
	rec := &fakeRecorder{recErr: errors.New("db down")}
	inner := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
	h := audited(rec, "model_config.update", "model_config", inner)

	req := httptest.NewRequest(http.MethodPut, "/api/orgs/org1/model-configs/cfg9", nil)
	req.SetPathValue("org", "org1")
	req.SetPathValue("id", "cfg9")
	rr := httptest.NewRecorder()
	h(rr, req) // must not panic

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200 despite audit failure, got %d", rr.Code)
	}
	if rr.Body.String() != `{"ok":true}` {
		t.Fatalf("body corrupted by audit failure: %q", rr.Body.String())
	}
}

// TestAuditedSkipsOnNon2xx: 被拒/失败的动作（非 2xx）不写审计行。
func TestAuditedSkipsOnNon2xx(t *testing.T) {
	rec := &fakeRecorder{}
	inner := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}
	h := audited(rec, "model_config.delete", "model_config", inner)

	req := httptest.NewRequest(http.MethodDelete, "/api/orgs/org1/model-configs/cfg9", nil)
	req.SetPathValue("org", "org1")
	req.SetPathValue("id", "cfg9")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d", rr.Code)
	}
	if len(rec.entries) != 0 {
		t.Fatalf("entries: want 0 on non-2xx, got %d", len(rec.entries))
	}
}

// TestAuditedNilRecorderIsPassthrough: 未注入审计（nil）时透传、零副作用。
func TestAuditedNilRecorderIsPassthrough(t *testing.T) {
	inner := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	h := audited(nil, "model_key.reveal", "model_config", inner)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
}

// TestAuditedBackfillsActorEmail: recorder 实现 actorEmailResolver 时，audited 回填 actor_email。
func TestAuditedBackfillsActorEmail(t *testing.T) {
	rec := &fakeRecorder{email: "admin@studio.com"}
	inner := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	h := audited(rec, "member.role_change", "member", inner)

	req := httptest.NewRequest(http.MethodPut, "/api/orgs/org1/members/u9", nil)
	req.SetPathValue("org", "org1")
	req.SetPathValue("userId", "u9")
	h(httptest.NewRecorder(), req)

	if len(rec.entries) != 1 {
		t.Fatalf("entries: want 1, got %d", len(rec.entries))
	}
	if got := rec.entries[0].ActorEmail; got != "admin@studio.com" {
		t.Fatalf("actor email: want admin@studio.com, got %q", got)
	}
	if got := rec.entries[0].TargetID; got != "u9" {
		t.Fatalf("target id: want u9 (from path), got %q", got)
	}
}

// TestAuditedBackfillsCreateTargetIDFromBody: create 类 (路径无 id) 从响应体回填 target_id。
func TestAuditedBackfillsCreateTargetIDFromBody(t *testing.T) {
	rec := &fakeRecorder{}
	inner := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cfg-new-42","orgId":"org1","name":"prod"}`))
	}
	h := audited(rec, "model_config.create", "model_config", inner)

	req := httptest.NewRequest(http.MethodPost, "/api/orgs/org1/model-configs", nil)
	req.SetPathValue("org", "org1")
	h(httptest.NewRecorder(), req)

	if len(rec.entries) != 1 {
		t.Fatalf("entries: want 1, got %d", len(rec.entries))
	}
	if got := rec.entries[0].TargetID; got != "cfg-new-42" {
		t.Fatalf("target id: want cfg-new-42 (from body), got %q", got)
	}
}

// TestAuditedBackfillsMemberAddUserID: member.add 返回 OrgMember{userId}，回填其 userId 为 target。
func TestAuditedBackfillsMemberAddUserID(t *testing.T) {
	rec := &fakeRecorder{}
	inner := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"userId":"u-added-7","email":"new@x.com","role":"editor"}`))
	}
	h := audited(rec, "member.add", "member", inner)

	req := httptest.NewRequest(http.MethodPost, "/api/orgs/org1/members", nil)
	req.SetPathValue("org", "org1")
	h(httptest.NewRecorder(), req)

	if len(rec.entries) != 1 {
		t.Fatalf("entries: want 1, got %d", len(rec.entries))
	}
	if got := rec.entries[0].TargetID; got != "u-added-7" {
		t.Fatalf("target id: want u-added-7 (from body userId), got %q", got)
	}
}

// fakeAuditLister 用固定分页结果驱动 auditLogHandler 单测。
type fakeAuditLister struct {
	items []audit.Record
	next  string
	err   error
}

func (f *fakeAuditLister) List(_ context.Context, _ string, _ int, _ string) ([]audit.Record, string, error) {
	return f.items, f.next, f.err
}

// TestAuditLogHandlerReturnsItemsAndCursor: 读取 handler 返回 items + next_cursor 信封。
func TestAuditLogHandlerReturnsItemsAndCursor(t *testing.T) {
	l := &fakeAuditLister{
		items: []audit.Record{{ID: "a1", Action: "member.add", ActorEmail: "admin@studio.com", TargetID: "u9"}},
		next:  "cursor-2",
	}
	h := auditLogHandler(l)
	req := httptest.NewRequest(http.MethodGet, "/api/orgs/org1/audit-log?limit=50", nil)
	req.SetPathValue("org", "org1")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	var resp struct {
		Items      []audit.Record `json:"items"`
		NextCursor string         `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Action != "member.add" || resp.Items[0].ActorEmail != "admin@studio.com" {
		t.Fatalf("items: %+v", resp.Items)
	}
	if resp.NextCursor != "cursor-2" {
		t.Fatalf("next_cursor: want cursor-2, got %q", resp.NextCursor)
	}
}

// TestAuditLogHandlerBadCursor: 非法 cursor → 400。
func TestAuditLogHandlerBadCursor(t *testing.T) {
	h := auditLogHandler(&fakeAuditLister{err: audit.ErrBadCursor})
	req := httptest.NewRequest(http.MethodGet, "/api/orgs/org1/audit-log?cursor=bogus", nil)
	req.SetPathValue("org", "org1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rr.Code)
	}
}
