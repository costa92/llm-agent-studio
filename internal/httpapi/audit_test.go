package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/audit"
)

// fakeRecorder captures audit entries; recErr lets a test force a write failure.
type fakeRecorder struct {
	entries []audit.Entry
	recErr  error
}

func (f *fakeRecorder) Record(_ context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return f.recErr
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
