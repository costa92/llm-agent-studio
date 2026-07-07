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
	return alerts.Settings{
		OrgID:                 orgID,
		Email:                 in.Email,
		Enabled:               in.Enabled,
		BudgetEnabled:         in.BudgetEnabled,
		BudgetThresholdMicros: in.BudgetThresholdMicros,
		BudgetWindowHours:     in.BudgetWindowHours,
		StuckEnabled:          in.StuckEnabled,
		StuckThresholdMinutes: in.StuckThresholdMinutes,
		BacklogEnabled:        in.BacklogEnabled,
		BacklogThreshold:      in.BacklogThreshold,
	}, nil
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

func TestPutAlertSettings_OperationalAlertsRoundTrip(t *testing.T) {
	store := &stubAlertSettingsStore{}
	h := putAlertSettingsHandler(store)
	body := `{"email":"ops@example.com","enabled":false,
		"budgetEnabled":true,"budgetThresholdMicros":50000000,"budgetWindowHours":24,
		"stuckEnabled":true,"stuckThresholdMinutes":45,
		"backlogEnabled":true,"backlogThreshold":100}`
	req := httptest.NewRequest("PUT", "/api/orgs/o1/alert-settings", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("put should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	in := store.upserted
	if in == nil || !in.BudgetEnabled || in.BudgetThresholdMicros != 50_000_000 || in.BudgetWindowHours != 24 {
		t.Fatalf("budget fields not carried through: %+v", in)
	}
	if !in.StuckEnabled || in.StuckThresholdMinutes != 45 || !in.BacklogEnabled || in.BacklogThreshold != 100 {
		t.Fatalf("stuck/backlog fields not carried through: %+v", in)
	}
}

func TestPutAlertSettings_OperationalAlertValidation400(t *testing.T) {
	store := &stubAlertSettingsStore{}
	h := putAlertSettingsHandler(store)
	for _, body := range []string{
		// 开启运营告警但无邮箱。
		`{"email":"","budgetEnabled":true,"budgetThresholdMicros":1000000}`,
		// 开启但阈值 <= 0。
		`{"email":"ops@example.com","budgetEnabled":true,"budgetThresholdMicros":0}`,
		`{"email":"ops@example.com","stuckEnabled":true,"stuckThresholdMinutes":0}`,
		`{"email":"ops@example.com","backlogEnabled":true,"backlogThreshold":0}`,
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
