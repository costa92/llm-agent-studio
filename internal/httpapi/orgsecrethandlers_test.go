package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/orgsecret"
)

// stubOrgSecretStore implements OrgSecretStore for DB-free handler tests.
type stubOrgSecretStore struct {
	createErr error
	updateErr error
	deleteErr error
}

func (s *stubOrgSecretStore) List(_ context.Context, _ string) ([]orgsecret.OrgSecret, error) {
	return []orgsecret.OrgSecret{}, nil
}
func (s *stubOrgSecretStore) Create(_ context.Context, org string, in orgsecret.UpsertInput) (orgsecret.OrgSecret, error) {
	if s.createErr != nil {
		return orgsecret.OrgSecret{}, s.createErr
	}
	// Mirror the store: DTO carries only {id, orgId, name, hasValue}. The value
	// MUST NOT appear in the returned DTO (struct has no Value field).
	return orgsecret.OrgSecret{ID: "new", OrgID: org, Name: in.Name, HasValue: in.Value != ""}, nil
}
func (s *stubOrgSecretStore) Update(_ context.Context, org, name string, in orgsecret.UpsertInput) (orgsecret.OrgSecret, error) {
	if s.updateErr != nil {
		return orgsecret.OrgSecret{}, s.updateErr
	}
	return orgsecret.OrgSecret{ID: "id1", OrgID: org, Name: name, HasValue: true}, nil
}
func (s *stubOrgSecretStore) Delete(_ context.Context, _, _ string) error {
	return s.deleteErr
}

func TestCreateOrgSecret_NoValueInResponse(t *testing.T) {
	h := createOrgSecretHandler(&stubOrgSecretStore{})
	body := `{"name":"PARTNER_KEY","value":"topsecret"}`
	req := httptest.NewRequest("POST", "/api/orgs/o1/secrets", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	respBody := rr.Body.String()
	// The submitted value must never be echoed back in any HTTP response.
	if strings.Contains(respBody, "topsecret") {
		t.Fatalf("response leaked the secret value: %s", respBody)
	}
	var sec orgsecret.OrgSecret
	if err := json.NewDecoder(rr.Body).Decode(&sec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sec.ID != "new" || sec.Name != "PARTNER_KEY" || !sec.HasValue || sec.OrgID != "o1" {
		t.Fatalf("bad DTO: %+v", sec)
	}
}

func TestCreateOrgSecret_EncUnavailable400(t *testing.T) {
	h := createOrgSecretHandler(&stubOrgSecretStore{createErr: orgsecret.ErrEncUnavailable})
	body := `{"name":"K","value":"v"}`
	req := httptest.NewRequest("POST", "/api/orgs/o1/secrets", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("enc-unavailable create should 400, got %d", rr.Code)
	}
}

func TestUpdateOrgSecret_NotFound404(t *testing.T) {
	h := updateOrgSecretHandler(&stubOrgSecretStore{updateErr: orgsecret.ErrNotFound})
	body := `{"name":"K","value":""}`
	req := httptest.NewRequest("PUT", "/api/orgs/o2/secrets/K", strings.NewReader(body))
	req.SetPathValue("org", "o2")
	req.SetPathValue("name", "K")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("not-found update should 404, got %d", rr.Code)
	}
}

func TestUpdateOrgSecret_EncUnavailable400(t *testing.T) {
	h := updateOrgSecretHandler(&stubOrgSecretStore{updateErr: orgsecret.ErrEncUnavailable})
	body := `{"name":"K","value":"v"}`
	req := httptest.NewRequest("PUT", "/api/orgs/o1/secrets/K", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	req.SetPathValue("name", "K")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("enc-unavailable update should 400, got %d", rr.Code)
	}
}

func TestDeleteOrgSecret_NotFound404(t *testing.T) {
	h := deleteOrgSecretHandler(&stubOrgSecretStore{deleteErr: orgsecret.ErrNotFound})
	req := httptest.NewRequest("DELETE", "/api/orgs/o1/secrets/K", nil)
	req.SetPathValue("org", "o1")
	req.SetPathValue("name", "K")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("not-found delete should 404, got %d", rr.Code)
	}
}

func TestDeleteOrgSecret_OK200(t *testing.T) {
	h := deleteOrgSecretHandler(&stubOrgSecretStore{})
	req := httptest.NewRequest("DELETE", "/api/orgs/o1/secrets/K", nil)
	req.SetPathValue("org", "o1")
	req.SetPathValue("name", "K")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete should 200, got %d", rr.Code)
	}
}
