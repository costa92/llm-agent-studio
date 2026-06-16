package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authztoken "github.com/costa92/llm-agent-authz/token"

	"github.com/costa92/llm-agent-studio/internal/storageconfig"
)

// stubSCStore implements StorageConfigStore for DB-free handler tests.
type stubSCStore struct {
	listOut   []storageconfig.StorageConfig
	deleteErr error
}

func (s *stubSCStore) List(_ context.Context, _ string) ([]storageconfig.StorageConfig, error) {
	return s.listOut, nil
}
func (s *stubSCStore) Create(_ context.Context, _ string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error) {
	return storageconfig.StorageConfig{ID: "new", Mode: in.Mode, Name: in.Name}, nil
}
func (s *stubSCStore) Update(_ context.Context, _, id string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error) {
	return storageconfig.StorageConfig{ID: id, Mode: in.Mode}, nil
}
func (s *stubSCStore) Delete(_ context.Context, _, _ string) error { return s.deleteErr }
func (s *stubSCStore) SetDefault(_ context.Context, _, _ string) error { return nil }
func (s *stubSCStore) UpsertGlobal(_ context.Context, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error) {
	return storageconfig.StorageConfig{Scope: "global", Mode: in.Mode, Bucket: in.Bucket, AccessKeyID: in.AccessKeyID, HasSecret: in.Secret != ""}, nil
}
func (s *stubSCStore) GetGlobal(_ context.Context) (storageconfig.StorageConfig, bool, error) {
	return storageconfig.StorageConfig{}, false, nil
}

func storageReq(method, target, body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(method, target, nil)
	}
	return httptest.NewRequest(method, target, strings.NewReader(body))
}

func TestCreateOrgStorageConfig_RejectsLocalfs(t *testing.T) {
	h := createOrgStorageConfigHandler(&stubSCStore{})
	req := httptest.NewRequest("POST", "/api/orgs/o1/storage-configs", strings.NewReader(`{"mode":"localfs","name":"x"}`))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("localfs should 400, got %d", rr.Code)
	}
}

func TestCreateOrgStorageConfig_Happy(t *testing.T) {
	h := createOrgStorageConfigHandler(&stubSCStore{})
	req := httptest.NewRequest("POST", "/api/orgs/o1/storage-configs", strings.NewReader(`{"mode":"s3","name":"主桶","bucket":"b","endpoint":"https://e"}`))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("happy should 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateOrgStorageConfig_RejectsEmptyName(t *testing.T) {
	h := createOrgStorageConfigHandler(&stubSCStore{})
	req := httptest.NewRequest("POST", "/api/orgs/o1/storage-configs", strings.NewReader(`{"mode":"s3","bucket":"b","endpoint":"https://e"}`))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty name should 400, got %d", rr.Code)
	}
}

func TestDeleteOrgStorageConfig_InUse409(t *testing.T) {
	h := deleteOrgStorageConfigHandler(&stubSCStore{deleteErr: storageconfig.ErrInUse})
	req := httptest.NewRequest("DELETE", "/api/orgs/o1/storage-configs/c1", nil)
	req.SetPathValue("org", "o1")
	req.SetPathValue("id", "c1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("in-use delete should 409, got %d", rr.Code)
	}
}

func TestListOrgStorageConfigs_OK(t *testing.T) {
	h := listOrgStorageConfigsHandler(&stubSCStore{listOut: []storageconfig.StorageConfig{{ID: "c1", Name: "x"}}})
	req := httptest.NewRequest("GET", "/api/orgs/o1/storage-configs", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list should 200, got %d", rr.Code)
	}
}

// TestGlobalStorageConfigGet proves the global GET handler returns the DTO (no secret)
// when present and {config:null} when absent.
func TestGlobalStorageConfigGet(t *testing.T) {
	// absent
	st := &stubSCStore{}
	rr := httptest.NewRecorder()
	getGlobalStorageConfigHandler(st)(rr, storageReq("GET", "/api/platform/storage-config/global", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"config":null`) {
		t.Fatalf("absent global want 200 {config:null}, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestPutGlobalStorageConfigPassesSecret proves the global PUT forwards the secret and
// never echoes it.
func TestPutGlobalStorageConfigPassesSecret(t *testing.T) {
	st := &stubSCStore{}
	const secret = "global-secret-key-abc"
	rr := httptest.NewRecorder()
	putGlobalStorageConfigHandler(st)(rr, storageReq("PUT", "/api/platform/storage-config/global",
		`{"mode":"s3","endpoint":"https://s3.example.com","bucket":"gb","accessKeyId":"AK","secret":"`+secret+`","enabled":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), secret) {
		t.Fatalf("secret leaked in global PUT response: %s", rr.Body.String())
	}
}

// TestPutOrgStorageConfigRejectsLocalfs — global localfs is still allowed (env-default semantics).
func TestPutOrgStorageConfigRejectsLocalfs(t *testing.T) {
	t.Run("global localfs still allowed", func(t *testing.T) {
		st := &stubSCStore{}
		rr := httptest.NewRecorder()
		req := storageReq("PUT", "/api/platform/storage-config/global",
			`{"mode":"localfs","publicPrefix":"/files","useSsl":true,"enabled":true}`)
		putGlobalStorageConfigHandler(st)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("global localfs must remain 200 (env-default semantics), got %d body=%s",
				rr.Code, rr.Body.String())
		}
	})
}

// TestStorageGetResultShape pins the exact JSON envelope for absent/present so the
// frontend contract is locked.
func TestStorageGetResultShape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeStorageGetResult(rr, storageconfig.StorageConfig{}, false)
	var absent map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &absent); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(absent["config"]) != "null" {
		t.Fatalf("absent config want null, got %s", absent["config"])
	}
}

// mintToken issues a valid access token for uid against the issuer (so the mux's
// Authenticate middleware injects the user id, exercising the real gate path).
func mintToken(t *testing.T, iss *authztoken.Issuer, uid string) string {
	t.Helper()
	tok, err := iss.Issue(uid, time.Now())
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}
