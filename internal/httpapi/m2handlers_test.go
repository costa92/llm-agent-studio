package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob/localfs"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

func TestPromptStylesHandler(t *testing.T) {
	h := promptStylesHandler()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/prompt-styles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "国风") {
		t.Fatalf("styles body missing 国风: %s", rec.Body.String())
	}
}

func TestBuiltinNodeTypesHandler(t *testing.T) {
	h := builtinNodeTypesHandler()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/node-types/builtin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 4 {
		t.Fatalf("items len=%d, want 4", len(body.Items))
	}
	seen := map[string]bool{}
	for i, it := range body.Items {
		for _, field := range []string{"type", "label", "description"} {
			if s, _ := it[field].(string); s == "" {
				t.Errorf("items[%d] field %q empty", i, field)
			}
		}
		if _, ok := it["color"]; ok {
			t.Errorf("items[%d] unexpectedly has color field", i)
		}
		if typ, _ := it["type"].(string); typ != "" {
			seen[typ] = true
		}
	}
	for _, want := range []string{"script", "storyboard", "asset", "prescreen"} {
		if !seen[want] {
			t.Errorf("builtin catalog missing type %q", want)
		}
	}
}

func TestPromptBuildHandler(t *testing.T) {
	h := promptBuildHandler(prompt.NewBuilder())
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/api/prompt/build", strings.NewReader(`{"prompt":"a cat","style":"国风"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "guofeng") {
		t.Fatalf("build did not inject style: %s", rec.Body.String())
	}
}

func TestModelCatalogHandler(t *testing.T) {
	h := modelCatalogHandler(nil) // nil avail → every entry available
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/model-catalog", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "minimax") {
		t.Fatalf("catalog: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"available":true`) {
		t.Fatalf("nil avail should mark every entry available: %s", rec.Body.String())
	}
}

// TestModelCatalogHandlerAvailability proves the injected ModelAvailable func
// drives the per-entry `available` flag: only volcengine image is keyed here, so
// that entry is available:true while openai image is available:false; fake
// entries are always available:true.
func TestModelCatalogHandlerAvailability(t *testing.T) {
	avail := func(provider, kind string) bool {
		if provider == "fake" {
			return true
		}
		return provider == "volcengine" && kind == "image"
	}
	h := modelCatalogHandler(avail)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/model-catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Catalog []struct {
			Provider, Model, Kind, Label string
			Available                    bool
		} `json:"catalog"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	seen := map[string]bool{} // "provider/model" → available
	for _, e := range got.Catalog {
		seen[e.Provider+"/"+e.Model] = e.Available
	}
	if !seen["volcengine/doubao-seedream-3-0-t2i"] {
		t.Errorf("volcengine image should be available:true")
	}
	if seen["openai/gpt-image-1"] {
		t.Errorf("openai image (unkeyed) should be available:false")
	}
	if !seen["fake/fake-video-async"] {
		t.Errorf("fake entries should always be available:true")
	}
}

// stubReview implements ReviewPort.
type stubReview struct{ conflict bool }

func (s stubReview) Accept(_ context.Context, _ string) error {
	if s.conflict {
		return errReviewConflict
	}
	return nil
}
func (s stubReview) Reject(_ context.Context, _ string) error { return nil }
func (s stubReview) Regenerate(_ context.Context, _, _ string) (string, string, error) {
	return "newAsset", "newTodo", nil
}

// narrationCalled records whether RegenerateNarration ran (and with what text),
// so the handler test can assert the body's text reached the service.
type recordingReview struct {
	stubReview
	calledText string
}

func (s *recordingReview) RegenerateNarration(_ context.Context, _, text string) (string, string, error) {
	s.calledText = text
	return "newAudio", "newTodo", nil
}

func (s stubReview) RegenerateNarration(_ context.Context, _, _ string) (string, string, error) {
	return "newAudio", "newTodo", nil
}

// stubAssetLib is a no-op AssetLibrary: OrgIDForAsset errors so the handler's
// quota gate is skipped (quota=0 also means unlimited). The narration/regenerate
// handlers only touch OrgIDForAsset from this surface.
type stubAssetLib struct{ AssetLibrary }

func (stubAssetLib) OrgIDForAsset(_ context.Context, _ string) (string, error) {
	return "", errors.New("no org")
}

func TestNarrationHandlerOK(t *testing.T) {
	rv := &recordingReview{}
	h := narrationHandler(rv, stubAssetLib{}, nil, 0)
	req := httptest.NewRequest("POST", "/api/assets/abc/narration", strings.NewReader(`{"text":"新旁白"}`))
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rv.calledText != "新旁白" {
		t.Fatalf("want RegenerateNarration called with 新旁白, got %q", rv.calledText)
	}
}

func TestNarrationHandlerEmptyText400(t *testing.T) {
	rv := &recordingReview{}
	h := narrationHandler(rv, stubAssetLib{}, nil, 0)
	req := httptest.NewRequest("POST", "/api/assets/abc/narration", strings.NewReader(`{"text":""}`))
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	if rv.calledText != "" {
		t.Fatalf("RegenerateNarration must not be called on empty text")
	}
}

func TestAcceptHandler409OnConflict(t *testing.T) {
	h := acceptHandler(stubReview{conflict: true})
	req := httptest.NewRequest("POST", "/api/assets/abc/accept", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
}

func TestAcceptHandlerOK(t *testing.T) {
	h := acceptHandler(stubReview{})
	req := httptest.NewRequest("POST", "/api/assets/abc/accept", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// TestBlobHandlerVerifiesSignature confirms the blob回源 handler serves bytes
// only on a valid HMAC sig (403 otherwise). Non-gated: uses the real localfs
// store on a temp dir, no DB.
func TestBlobHandlerVerifiesSignature(t *testing.T) {
	st := localfs.New(t.TempDir(), []byte("test-secret"), "/api/blob/")
	if err := st.Put(context.Background(), "img/x.png", strings.NewReader("PNGBYTES"), "image/png"); err != nil {
		t.Fatalf("put: %v", err)
	}
	h := blobHandler(st)

	// Bad signature → 403.
	bad := httptest.NewRequest("GET", "/api/blob/img/x.png?exp=9999999999&sig=deadbeef", nil)
	recBad := httptest.NewRecorder()
	h(recBad, bad)
	if recBad.Code != http.StatusForbidden {
		t.Fatalf("bad sig: want 403, got %d", recBad.Code)
	}

	// Valid signature → 200 + bytes.
	signed, err := st.SignedURL(context.Background(), "img/x.png", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	u, _ := url.Parse(signed)
	good := httptest.NewRequest("GET", u.String(), nil)
	recGood := httptest.NewRecorder()
	h(recGood, good)
	if recGood.Code != http.StatusOK {
		t.Fatalf("valid sig: want 200, got %d (%s)", recGood.Code, recGood.Body.String())
	}
	if recGood.Body.String() != "PNGBYTES" {
		t.Fatalf("body=%q", recGood.Body.String())
	}
	if ct := recGood.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type=%q", ct)
	}
}

var _ assets.Store // keep import (library handler uses *assets.Store via AssetLibrary port)
var _ models.Store

// modelTestPool opens the live PG store (LLM_AGENT_STUDIO_PG_URL), migrating the
// schema. Skips when the env var is unset (mirrors models.testPool).
func modelTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run model-config HTTP store tests")
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
	return st.Pool()
}

// modelTestGorm 与 modelTestPool 同源，但返回 *gorm.DB，供已迁到 GORM 的 store（prompt）用。
func modelTestGorm(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run prompt HTTP store tests")
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

// modelTestBox builds an enabled secretbox.Box from a fixed base64 32-byte key.
func modelTestBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatalf("test box: %v", err)
	}
	return b
}

// createModelConfigReq builds the POST request for an org with a JSON body.
func createModelConfigReq(org, body string) *http.Request {
	req := httptest.NewRequest("POST", "/api/orgs/"+org+"/model-configs", strings.NewReader(body))
	req.SetPathValue("org", org)
	return req
}

// TestCreateModelConfigBYOKHidesKey proves the HTTP layer (real *models.Store +
// enabled box): a POST with apiKey+baseUrl returns 200 with baseUrl + hasApiKey
// and NEVER echoes the apiKey; the list endpoint shows the same; and the stored
// key is recoverable only via the store's ResolveForOrg (never over HTTP).
func TestCreateModelConfigBYOKHidesKey(t *testing.T) {
	st := models.New(modelTestGorm(t), modelTestBox(t))
	const org = "org-byok-http"
	const rawKey = "sk-http-byok-secret-123456789"
	const baseURL = "https://api.example.com/v1"

	rr := httptest.NewRecorder()
	createModelConfigHandler(st)(rr, createModelConfigReq(org,
		`{"kind":"text","provider":"openai-compatible","model":"gpt-4o-mini","baseUrl":"`+baseURL+`","apiKey":"`+rawKey+`","enabled":true,"isDefault":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("create: code=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, rawKey) {
		t.Fatalf("apiKey LEAKED in create response (must never echo): %s", body)
	}
	if !strings.Contains(body, `"hasApiKey":true`) || !strings.Contains(body, `"baseUrl":"`+baseURL+`"`) {
		t.Fatalf("create response missing hasApiKey/baseUrl: %s", body)
	}
	var created models.ModelConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	// List shows the same config — still no key.
	lr := httptest.NewRecorder()
	lreq := httptest.NewRequest("GET", "/api/orgs/"+org+"/model-configs", nil)
	lreq.SetPathValue("org", org)
	listModelConfigsHandler(st)(lr, lreq)
	if lr.Code != http.StatusOK {
		t.Fatalf("list: code=%d body=%s", lr.Code, lr.Body.String())
	}
	lbody := lr.Body.String()
	if strings.Contains(lbody, rawKey) {
		t.Fatalf("apiKey LEAKED in list response (must never echo): %s", lbody)
	}
	if !strings.Contains(lbody, `"hasApiKey":true`) || !strings.Contains(lbody, created.ID) {
		t.Fatalf("list missing created config / hasApiKey: %s", lbody)
	}

	// The plaintext key is recoverable only server-side via ResolveForOrg.
	rm, ok, err := st.ResolveForOrg(context.Background(), org, "text")
	if err != nil || !ok || rm.APIKey != rawKey {
		t.Fatalf("resolve: ok=%v err=%v key=%q", ok, err, rm.APIKey)
	}
}

// TestCreateModelConfig400OnDisabledBox proves a POST carrying an apiKey when the
// store's box is disabled returns 400 with the ErrEncUnavailable message (so the
// UI can tell the admin to set STUDIO_CONFIG_ENC_KEY).
func TestCreateModelConfig400OnDisabledBox(t *testing.T) {
	disabled, _ := secretbox.New("") // disabled box
	st := models.New(modelTestGorm(t), disabled)

	rr := httptest.NewRecorder()
	createModelConfigHandler(st)(rr, createModelConfigReq("org-nobox",
		`{"kind":"text","provider":"openai","model":"gpt-4o-mini","apiKey":"sk-nope","enabled":true}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("apiKey with disabled box must 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), models.ErrEncUnavailable.Error()) {
		t.Fatalf("400 body must carry ErrEncUnavailable message, got: %s", rr.Body.String())
	}
}

// stubModelStore is a fake ModelStore for non-gated handler mapping tests
// (404 / delete-ok). Update/Delete return notFound when set.
type stubModelStore struct {
	notFound   bool
	updated    models.ModelConfig
	deletedID  string
	deletedOrg string
}

func (s *stubModelStore) Create(context.Context, models.CreateInput) (models.ModelConfig, error) {
	return models.ModelConfig{}, nil
}
func (s *stubModelStore) ListByOrg(context.Context, string) ([]models.ModelConfig, error) {
	return nil, nil
}
func (s *stubModelStore) Update(_ context.Context, id, orgID string, _ models.UpdateInput) (models.ModelConfig, error) {
	if s.notFound {
		return models.ModelConfig{}, models.ErrNotFound
	}
	mc := s.updated
	mc.ID, mc.OrgID = id, orgID
	return mc, nil
}
func (s *stubModelStore) Delete(_ context.Context, id, orgID string) error {
	if s.notFound {
		return models.ErrNotFound
	}
	s.deletedID, s.deletedOrg = id, orgID
	return nil
}

// modelConfigReq builds a {PUT,DELETE} request for org/id with path values set.
func modelConfigReq(method, org, id, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/api/orgs/"+org+"/model-configs/"+id, nil)
	} else {
		r = httptest.NewRequest(method, "/api/orgs/"+org+"/model-configs/"+id, strings.NewReader(body))
	}
	r.SetPathValue("org", org)
	r.SetPathValue("id", id)
	return r
}

// TestUpdateModelConfigHandler404 proves a missing/cross-org config → 404.
func TestUpdateModelConfigHandler404(t *testing.T) {
	rr := httptest.NewRecorder()
	updateModelConfigHandler(&stubModelStore{notFound: true})(rr,
		modelConfigReq("PUT", "org-x", "missing", `{"provider":"openai","model":"gpt-4o-mini","enabled":true}`))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUpdateModelConfigHandlerBadRequest proves missing provider/model → 400.
func TestUpdateModelConfigHandlerBadRequest(t *testing.T) {
	rr := httptest.NewRecorder()
	updateModelConfigHandler(&stubModelStore{})(rr, modelConfigReq("PUT", "org-x", "id1", `{"enabled":true}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// TestDeleteModelConfigHandler proves DELETE → 200 {ok:true}, scoped by (id,org),
// and a missing config → 404.
func TestDeleteModelConfigHandler(t *testing.T) {
	st := &stubModelStore{}
	rr := httptest.NewRecorder()
	deleteModelConfigHandler(st)(rr, modelConfigReq("DELETE", "org-d", "cfg1", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("delete want 200 {ok:true}, got %d body=%s", rr.Code, rr.Body.String())
	}
	if st.deletedID != "cfg1" || st.deletedOrg != "org-d" {
		t.Fatalf("delete must be scoped by (id,org): got id=%q org=%q", st.deletedID, st.deletedOrg)
	}

	rr2 := httptest.NewRecorder()
	deleteModelConfigHandler(&stubModelStore{notFound: true})(rr2, modelConfigReq("DELETE", "org-d", "missing", ""))
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("delete missing want 404, got %d", rr2.Code)
	}
}

// TestUpdateModelConfigBYOKHidesKey proves the HTTP update layer (real
// *models.Store + enabled box): a PUT with a blank apiKey keeps the existing key
// (recoverable only via ResolveForOrg), a PUT with a new apiKey replaces it, and
// NO response ever echoes a raw key.
func TestUpdateModelConfigBYOKHidesKey(t *testing.T) {
	st := models.New(modelTestGorm(t), modelTestBox(t))
	const org = "org-update-http"
	const origKey = "sk-http-orig-111"
	const newKey = "sk-http-new-222"

	mc, err := st.Create(context.Background(), models.CreateInput{
		OrgID: org, Kind: "text", Provider: "openai-compatible", Model: "gpt-4o-mini",
		Enabled: true, IsDefault: true, BaseURL: "https://a.example.com/v1", APIKey: origKey,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// PUT with blank apiKey → keeps the key; changes model/base_url.
	rr := httptest.NewRecorder()
	updateModelConfigHandler(st)(rr, modelConfigReq("PUT", org, mc.ID,
		`{"kind":"text","provider":"openai-compatible","model":"gpt-4o","baseUrl":"https://b.example.com/v1","enabled":true,"isDefault":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("update keep: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if b := rr.Body.String(); strings.Contains(b, origKey) {
		t.Fatalf("raw key LEAKED in update response (must never echo): %s", b)
	}
	if !strings.Contains(rr.Body.String(), `"hasApiKey":true`) || !strings.Contains(rr.Body.String(), `"model":"gpt-4o"`) {
		t.Fatalf("update response shape: %s", rr.Body.String())
	}
	if rm, ok, err := st.ResolveForOrg(context.Background(), org, "text"); err != nil || !ok || rm.APIKey != origKey {
		t.Fatalf("keep: resolved key=%q (want %q) ok=%v err=%v", rm.APIKey, origKey, ok, err)
	}

	// PUT with a new apiKey → replaces it.
	rr2 := httptest.NewRecorder()
	updateModelConfigHandler(st)(rr2, modelConfigReq("PUT", org, mc.ID,
		`{"kind":"text","provider":"openai-compatible","model":"gpt-4o","baseUrl":"https://b.example.com/v1","apiKey":"`+newKey+`","enabled":true,"isDefault":true}`))
	if rr2.Code != http.StatusOK || strings.Contains(rr2.Body.String(), newKey) {
		t.Fatalf("update replace: code=%d leaked=%v body=%s", rr2.Code, strings.Contains(rr2.Body.String(), newKey), rr2.Body.String())
	}
	if rm, ok, err := st.ResolveForOrg(context.Background(), org, "text"); err != nil || !ok || rm.APIKey != newKey {
		t.Fatalf("replace: resolved key=%q (want %q) ok=%v err=%v", rm.APIKey, newKey, ok, err)
	}
}

// TestModelCatalogHandlerTextAvailability proves the catalog includes text-kind
// entries and the injected ModelAvailable func drives their `available` flag.
func TestModelCatalogHandlerTextAvailability(t *testing.T) {
	// deepseek text keyed, openai text un-keyed.
	avail := func(provider, kind string) bool {
		return kind == "text" && provider == "deepseek"
	}
	rr := httptest.NewRecorder()
	modelCatalogHandler(avail)(rr, httptest.NewRequest("GET", "/api/model-catalog", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Catalog []catalogEntryView `json:"catalog"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	avl := map[string]bool{}
	var sawText bool
	for _, e := range got.Catalog {
		if e.Kind == "text" {
			sawText = true
		}
		avl[e.Provider+"/"+e.Model] = e.Available
	}
	if !sawText {
		t.Fatalf("catalog missing text entries: %s", rr.Body.String())
	}
	if !avl["deepseek/deepseek-chat"] {
		t.Errorf("deepseek text (keyed) should be available:true")
	}
	if avl["openai/gpt-4o-mini"] {
		t.Errorf("openai text (un-keyed) should be available:false")
	}
}
