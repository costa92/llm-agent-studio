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

// stubStorageStore 是 StorageConfigStore 的假实现，记录最近一次入参 (验证 secret 透传)。
// notFound→DeleteForOrg 返回 ErrNotFound；encUnavailable→Upsert* 返回 ErrEncUnavailable。
type stubStorageStore struct {
	notFound       bool
	encUnavailable bool
	lastUpsert     storageconfig.UpsertInput
	lastUpsertOrg  string
	global         storageconfig.StorageConfig
	globalOK       bool
	org            storageconfig.StorageConfig
	orgOK          bool
}

func (s *stubStorageStore) UpsertGlobal(_ context.Context, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error) {
	s.lastUpsert = in
	if s.encUnavailable {
		return storageconfig.StorageConfig{}, storageconfig.ErrEncUnavailable
	}
	return storageconfig.StorageConfig{Scope: "global", Mode: in.Mode, Bucket: in.Bucket, AccessKeyID: in.AccessKeyID, HasSecret: in.Secret != ""}, nil
}

func (s *stubStorageStore) UpsertForOrg(_ context.Context, orgID string, in storageconfig.UpsertInput) (storageconfig.StorageConfig, error) {
	s.lastUpsert, s.lastUpsertOrg = in, orgID
	if s.encUnavailable {
		return storageconfig.StorageConfig{}, storageconfig.ErrEncUnavailable
	}
	return storageconfig.StorageConfig{Scope: "org", OrgID: orgID, Mode: in.Mode, Bucket: in.Bucket, AccessKeyID: in.AccessKeyID, HasSecret: in.Secret != ""}, nil
}

func (s *stubStorageStore) GetGlobal(context.Context) (storageconfig.StorageConfig, bool, error) {
	return s.global, s.globalOK, nil
}

func (s *stubStorageStore) GetForOrg(context.Context, string) (storageconfig.StorageConfig, bool, error) {
	return s.org, s.orgOK, nil
}

func (s *stubStorageStore) DeleteForOrg(context.Context, string) error {
	if s.notFound {
		return storageconfig.ErrNotFound
	}
	return nil
}

func storageReq(method, target, body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(method, target, nil)
	}
	return httptest.NewRequest(method, target, strings.NewReader(body))
}

// TestGetOrgStorageConfigAbsent proves a missing per-org config → 200 {config:null}
// (not 404), so the frontend branches on config==null.
func TestGetOrgStorageConfigAbsent(t *testing.T) {
	rr := httptest.NewRecorder()
	req := storageReq("GET", "/api/orgs/org-x/storage-config", "")
	req.SetPathValue("org", "org-x")
	getOrgStorageConfigHandler(&stubStorageStore{orgOK: false})(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("absent want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"config":null`) {
		t.Fatalf("absent body want {config:null}, got %s", rr.Body.String())
	}
}

// TestGetOrgStorageConfigPresent proves a present config → 200 {config:{...}} DTO.
func TestGetOrgStorageConfigPresent(t *testing.T) {
	st := &stubStorageStore{orgOK: true, org: storageconfig.StorageConfig{
		ID: "c1", Scope: "org", OrgID: "org-x", Mode: "s3", Bucket: "b", AccessKeyID: "AK", HasSecret: true,
	}}
	rr := httptest.NewRecorder()
	req := storageReq("GET", "/api/orgs/org-x/storage-config", "")
	req.SetPathValue("org", "org-x")
	getOrgStorageConfigHandler(st)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	b := rr.Body.String()
	if !strings.Contains(b, `"hasSecret":true`) || !strings.Contains(b, `"accessKeyId":"AK"`) {
		t.Fatalf("DTO shape: %s", b)
	}
}

// TestPutOrgStorageConfigPassesSecret proves the PUT body's secret is passed through
// to the store, the org path value is scoped, and the response is the DTO (no secret).
func TestPutOrgStorageConfigPassesSecret(t *testing.T) {
	st := &stubStorageStore{}
	const secret = "s3-secret-access-key-xyz"
	rr := httptest.NewRecorder()
	req := storageReq("PUT", "/api/orgs/org-p/storage-config",
		`{"mode":"s3","endpoint":"https://s3.example.com","region":"us","bucket":"b","accessKeyId":"AK","secret":"`+secret+`","useSsl":true,"publicPrefix":"","enabled":true}`)
	req.SetPathValue("org", "org-p")
	putOrgStorageConfigHandler(st)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if st.lastUpsertOrg != "org-p" {
		t.Fatalf("upsert org scope=%q want org-p", st.lastUpsertOrg)
	}
	if st.lastUpsert.Secret != secret {
		t.Fatalf("secret not passed through: got %q", st.lastUpsert.Secret)
	}
	if st.lastUpsert.Mode != "s3" || st.lastUpsert.Bucket != "b" || !st.lastUpsert.UseSSL {
		t.Fatalf("input fields not mapped: %+v", st.lastUpsert)
	}
	if strings.Contains(rr.Body.String(), secret) {
		t.Fatalf("secret leaked in PUT response: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"hasSecret":true`) {
		t.Fatalf("response DTO missing hasSecret: %s", rr.Body.String())
	}
}

// TestPutOrgStorageConfigKeepSecret proves an empty secret is forwarded as "" (store
// keep-or-replace semantics: keep existing).
func TestPutOrgStorageConfigKeepSecret(t *testing.T) {
	st := &stubStorageStore{}
	rr := httptest.NewRecorder()
	req := storageReq("PUT", "/api/orgs/org-k/storage-config",
		`{"mode":"s3","endpoint":"https://s3.example.com","bucket":"b","accessKeyId":"AK","secret":"","enabled":true}`)
	req.SetPathValue("org", "org-k")
	putOrgStorageConfigHandler(st)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if st.lastUpsert.Secret != "" {
		t.Fatalf("empty secret must pass through as empty (keep): got %q", st.lastUpsert.Secret)
	}
	if strings.Contains(rr.Body.String(), `"hasSecret":true`) {
		t.Fatalf("keep with empty secret: stub returns hasSecret=false, got %s", rr.Body.String())
	}
}

// TestPutOrgStorageConfig400OnEncUnavailable proves a store ErrEncUnavailable → 400
// carrying the message (UI prompts to set STUDIO_CONFIG_ENC_KEY).
func TestPutOrgStorageConfig400OnEncUnavailable(t *testing.T) {
	rr := httptest.NewRecorder()
	req := storageReq("PUT", "/api/orgs/org-e/storage-config",
		`{"mode":"s3","endpoint":"https://s3.example.com","bucket":"b","accessKeyId":"AK","secret":"x","enabled":true}`)
	req.SetPathValue("org", "org-e")
	putOrgStorageConfigHandler(&stubStorageStore{encUnavailable: true})(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), storageconfig.ErrEncUnavailable.Error()) {
		t.Fatalf("400 body must carry ErrEncUnavailable: %s", rr.Body.String())
	}
}

// TestPutOrgStorageConfigRejectsLocalfs closes M8 §10 known limitation: per-org
// localfs is silently ignored by buildStorageStore (which always returns
// localfsDefault for mode=localfs to keep the single signing root). The handler
// must REJECT per-org localfs at the request boundary so admins get immediate
// feedback instead of the UX trap (write succeeds, factory ignores, all orgs
// share one root). Global localfs remains valid (env-default semantics).
func TestPutOrgStorageConfigRejectsLocalfs(t *testing.T) {
	t.Run("per-org localfs is rejected", func(t *testing.T) {
		st := &stubStorageStore{}
		rr := httptest.NewRecorder()
		req := storageReq("PUT", "/api/orgs/org-x/storage-config",
			`{"mode":"localfs","publicPrefix":"/files","useSsl":true,"enabled":true}`)
		req.SetPathValue("org", "org-x")
		putOrgStorageConfigHandler(st)(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("per-org localfs must 400, got %d body=%s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "localfs") {
			t.Fatalf("error must mention localfs for actionable UX, got %s", rr.Body.String())
		}
		// Critical: store must NOT be reached — fail-fast at handler boundary.
		if st.lastUpsertOrg != "" || st.lastUpsert.Mode != "" {
			t.Fatalf("store was reached despite handler precondition: lastOrg=%q lastMode=%q",
				st.lastUpsertOrg, st.lastUpsert.Mode)
		}
	})
	t.Run("global localfs still allowed", func(t *testing.T) {
		// Regression guard: the precondition is per-org only. Global localfs is
		// the env-default mode and must keep round-tripping the PUT.
		st := &stubStorageStore{}
		rr := httptest.NewRecorder()
		req := storageReq("PUT", "/api/platform/storage-config/global",
			`{"mode":"localfs","publicPrefix":"/files","useSsl":true,"enabled":true}`)
		putGlobalStorageConfigHandler(st)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("global localfs must remain 200 (env-default semantics), got %d body=%s",
				rr.Code, rr.Body.String())
		}
		if st.lastUpsert.Mode != "localfs" {
			t.Fatalf("global handler must forward mode=localfs to store, got %q", st.lastUpsert.Mode)
		}
	})
}

// TestDeleteOrgStorageConfig proves DELETE → 200 {ok:true}; missing → 404.
func TestDeleteOrgStorageConfig(t *testing.T) {
	rr := httptest.NewRecorder()
	req := storageReq("DELETE", "/api/orgs/org-d/storage-config", "")
	req.SetPathValue("org", "org-d")
	deleteOrgStorageConfigHandler(&stubStorageStore{})(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("delete want 200 {ok:true}, got %d body=%s", rr.Code, rr.Body.String())
	}

	rr2 := httptest.NewRecorder()
	req2 := storageReq("DELETE", "/api/orgs/org-d/storage-config", "")
	req2.SetPathValue("org", "org-d")
	deleteOrgStorageConfigHandler(&stubStorageStore{notFound: true})(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("delete missing want 404, got %d", rr2.Code)
	}
}

// TestGlobalStorageConfigGet proves the global GET handler returns the DTO (no secret)
// when present and {config:null} when absent.
func TestGlobalStorageConfigGet(t *testing.T) {
	rr := httptest.NewRecorder()
	getGlobalStorageConfigHandler(&stubStorageStore{globalOK: false})(rr, storageReq("GET", "/api/platform/storage-config/global", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"config":null`) {
		t.Fatalf("absent global want 200 {config:null}, got %d body=%s", rr.Code, rr.Body.String())
	}

	st := &stubStorageStore{globalOK: true, global: storageconfig.StorageConfig{Scope: "global", Mode: "s3", Bucket: "gb", HasSecret: true}}
	rr2 := httptest.NewRecorder()
	getGlobalStorageConfigHandler(st)(rr2, storageReq("GET", "/api/platform/storage-config/global", ""))
	if rr2.Code != http.StatusOK || !strings.Contains(rr2.Body.String(), `"bucket":"gb"`) {
		t.Fatalf("present global want DTO, got %d body=%s", rr2.Code, rr2.Body.String())
	}
}

// TestPutGlobalStorageConfigPassesSecret proves the global PUT forwards the secret and
// never echoes it.
func TestPutGlobalStorageConfigPassesSecret(t *testing.T) {
	st := &stubStorageStore{}
	const secret = "global-secret-key-abc"
	rr := httptest.NewRecorder()
	putGlobalStorageConfigHandler(st)(rr, storageReq("PUT", "/api/platform/storage-config/global",
		`{"mode":"s3","endpoint":"https://s3.example.com","bucket":"gb","accessKeyId":"AK","secret":"`+secret+`","enabled":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if st.lastUpsert.Secret != secret {
		t.Fatalf("global secret not passed through: %q", st.lastUpsert.Secret)
	}
	if strings.Contains(rr.Body.String(), secret) {
		t.Fatalf("secret leaked in global PUT response: %s", rr.Body.String())
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
