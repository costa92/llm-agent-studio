package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/alerts"
)

// stubAlertSettingsStore implements AlertSettingsStore for DB-free handler tests.
type stubAlertSettingsStore struct {
	settings alerts.Settings
	upserted *alerts.UpsertInput
}

func (s *stubAlertSettingsStore) Get(_ context.Context, orgID string) (alerts.Settings, error) {
	st := s.settings
	st.OrgID = orgID
	return st, nil
}

func (s *stubAlertSettingsStore) Upsert(_ context.Context, orgID string, in alerts.UpsertInput) (alerts.Settings, error) {
	s.upserted = &in
	return alerts.Settings{OrgID: orgID, Email: in.Email, Enabled: in.Enabled}, nil
}

func TestGetAlertSettings_DefaultWhenUnset(t *testing.T) {
	h := getAlertSettingsHandler(&stubAlertSettingsStore{})
	req := httptest.NewRequest("GET", "/api/orgs/o1/alert-settings", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var st alerts.Settings
	if err := json.NewDecoder(rr.Body).Decode(&st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.OrgID != "o1" || st.Email != "" || st.Enabled {
		t.Fatalf("unexpected default settings: %+v", st)
	}
}

func TestPutAlertSettings_OK(t *testing.T) {
	store := &stubAlertSettingsStore{}
	h := putAlertSettingsHandler(store)
	body := `{"email":" ops@example.com ","enabled":true}`
	req := httptest.NewRequest("PUT", "/api/orgs/o1/alert-settings", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("put should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if store.upserted == nil || store.upserted.Email != "ops@example.com" || !store.upserted.Enabled {
		t.Fatalf("unexpected upsert input: %+v", store.upserted)
	}
}

func TestPutAlertSettings_EnabledRequiresEmail400(t *testing.T) {
	store := &stubAlertSettingsStore{}
	h := putAlertSettingsHandler(store)
	for _, body := range []string{
		`{"email":"","enabled":true}`,
		`{"email":"not-an-email","enabled":true}`,
		`{"email":"a@b","enabled":true}`, // 域名无点
	} {
		req := httptest.NewRequest("PUT", "/api/orgs/o1/alert-settings", strings.NewReader(body))
		req.SetPathValue("org", "o1")
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s should 400, got %d", body, rr.Code)
		}
	}
	if store.upserted != nil {
		t.Fatalf("invalid input must not reach the store: %+v", store.upserted)
	}
}

func TestPutAlertSettings_DisableWithEmptyEmailOK(t *testing.T) {
	store := &stubAlertSettingsStore{}
	h := putAlertSettingsHandler(store)
	req := httptest.NewRequest("PUT", "/api/orgs/o1/alert-settings", strings.NewReader(`{"email":"","enabled":false}`))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable with empty email should 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
