package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-authz/password"
	authzstore "github.com/costa92/llm-agent-authz/store"
	"github.com/costa92/llm-agent-contract/llm"

	"github.com/costa92/llm-agent-studio/internal/config"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/obs"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// loadCfgWith builds a deterministic config (Workers=1 keeps scripted-LLM
// cursors ordered) with per-test env overrides layered on top.
func loadCfgWith(t *testing.T, dsn string, extra map[string]string) config.Config {
	t.Helper()
	cfg, err := config.LoadFromLookup(func(k string) (string, bool) {
		if v, ok := extra[k]; ok {
			return v, true
		}
		switch k {
		case "PG_URL":
			return dsn, true
		case "JWT_SECRET":
			return "test-secret", true
		case "WORKERS":
			return "1", true
		case "WORKER_POLL":
			return "50ms", true
		case "WORKER_BACKOFF":
			return "1ms", true
		case "REVIEW_PRESCREEN":
			// Baseline OFF (T9/评审修复 M1): the M1/M2 e2e scripted sequences
			// carry no review responses; build() would otherwise construct a
			// ReviewAgent and burn responses. M3 e2e re-enable via extra.
			return "false", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

// loadCfg builds a deterministic config for the e2e (Workers=1 so the single
// injected ScriptedLLM cursor advances in a predictable order).
func loadCfg(t *testing.T, dsn string) config.Config { return loadCfgWith(t, dsn, nil) }

func newHarness(t *testing.T, dsn string, responses ...llm.Response) (*httptest.Server, func()) {
	t.Helper()
	ctx := context.Background()
	providerOverride = func(config.Config) (llm.ChatModel, error) {
		return llm.NewScriptedLLM(llm.WithResponses(responses...)), nil
	}
	// M2 fans out an asset todo per shot, so even the text-pipeline e2e now hits
	// the asset path. Inject a fake generator (same canned bytes as the image
	// harness) so the fanned-out asset todo succeeds → run_done via the success
	// path; without it the asset todo would fail with no generator configured.
	generatorOverride = func(config.Config) (generate.MediaGenerator, error) {
		return generate.NewFakeLooping(generate.GenResult{
			Bytes: []byte("FAKEPNG"), MimeType: "image/png", Provider: "fake", Model: "fake-img",
			Tokens: 5, ImageCount: 1, LatencyMS: 10,
		}), nil
	}
	t.Cleanup(func() { providerOverride = nil; generatorOverride = nil })
	handler, cleanup, err := build(ctx, loadCfg(t, dsn))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	srv := httptest.NewServer(handler)
	return srv, func() { srv.Close(); cleanup() }
}

func seedUser(t *testing.T, dsn, email string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	hash, err := password.Hash("pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authzstore.New(pool).CreateUser(ctx, email, hash); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestEndToEndRegister exercises the self-serve registration endpoint: a fresh
// email + valid password → 200 with an access_token that authorizes a follow-up
// request, and a refresh cookie that powers POST /api/auth/refresh; the same
// email again → 409; bad inputs → 400.
func TestEndToEndRegister(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio register e2e")
	}
	srv, done := newHarness(t, dsn) // no scripted LLM responses needed (no pipeline run)
	defer done()

	client := srv.Client()
	do := func(method, path, bearer, body string) (int, map[string]any, *http.Response) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{"_raw": string(raw)}
		}
		return resp.StatusCode, m, resp
	}

	email := "newuser_" + time.Now().Format("20060102150405000000000") + "@studio.com"
	// 1. Register a brand-new email → 200 + {"verified":false,"email":"..."}
	code, body, resp := do("POST", "/api/auth/register", "", `{"email":"`+email+`","password":"password123"}`)
	if code != http.StatusOK {
		t.Fatalf("register code=%d body=%v", code, body)
	}
	verified, _ := body["verified"].(bool)
	if verified {
		t.Fatalf("expected user to be unverified initially")
	}

	// 1b. Retrieve verification code from DB
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect DB: %v", err)
	}
	defer db.Close()
	var codeStr string
	err = db.QueryRow(context.Background(), `SELECT verification_code FROM auth_user WHERE email=$1`, email).Scan(&codeStr)
	if err != nil {
		t.Fatalf("query verification code: %v", err)
	}
	if codeStr == "" {
		t.Fatalf("verification code not generated in DB")
	}

	// 1c. Submit correct code to verify → 200 + access_token + refresh cookie.
	code, body, resp = do("POST", "/api/auth/verify", "", `{"email":"`+email+`","code":"`+codeStr+`"}`)
	if code != http.StatusOK {
		t.Fatalf("verify code=%d body=%v", code, body)
	}
	token, _ := body["access_token"].(string)
	if token == "" {
		t.Fatalf("no access_token: %v", body)
	}
	// Capture the refresh cookie set by register (Secure cookie won't be auto-sent
	// back over httptest's plain HTTP, so we replay it manually below).
	var refreshCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "authz_refresh" {
			refreshCookie = c
		}
	}
	if refreshCookie == nil || refreshCookie.Value == "" {
		t.Fatalf("register did not set the authz_refresh cookie: %v", resp.Cookies())
	}

	// 2. The access token authorizes a follow-up request (create an org).
	code, ob, _ := do("POST", "/api/orgs", token, `{"name":"New Co"}`)
	if code != http.StatusOK {
		t.Fatalf("create org with register token code=%d body=%v", code, ob)
	}

	// 3. The refresh cookie powers POST /api/auth/refresh (needs the CSRF header).
	refReq, _ := http.NewRequest("POST", srv.URL+"/api/auth/refresh", nil)
	refReq.Header.Set("X-CSRF", "1")
	refReq.AddCookie(refreshCookie)
	refResp, err := client.Do(refReq)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	raw, _ := io.ReadAll(refResp.Body)
	refResp.Body.Close()
	if refResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh code=%d body=%s", refResp.StatusCode, raw)
	}
	var refBody map[string]any
	_ = json.Unmarshal(raw, &refBody)
	if tok, _ := refBody["access_token"].(string); tok == "" {
		t.Fatalf("refresh returned no access_token: %s", raw)
	}

	// 4. Same email again → 409.
	code, body, _ = do("POST", "/api/auth/register", "", `{"email":"`+email+`","password":"password123"}`)
	if code != http.StatusConflict {
		t.Fatalf("duplicate register code=%d body=%v, want 409", code, body)
	}

	// 5. Bad email (no "@") → 400.
	if code, body, _ := do("POST", "/api/auth/register", "", `{"email":"nope","password":"password123"}`); code != http.StatusBadRequest {
		t.Fatalf("bad-email register code=%d body=%v, want 400", code, body)
	}
	// 6. Weak password (<8 chars) → 400.
	if code, body, _ := do("POST", "/api/auth/register", "", `{"email":"short@studio.com","password":"short"}`); code != http.StatusBadRequest {
		t.Fatalf("weak-password register code=%d body=%v, want 400", code, body)
	}
}

func TestEndToEndTextPipeline(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio e2e")
	}
	// Script order consumed by the single ScriptedLLM cursor:
	//   1) planner → valid graph (script→storyboard)
	//   2) script agent → script JSON
	//   3) storyboard agent → shots JSON
	srv, done := newHarness(t, dsn,
		llm.Response{Text: `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`},
		llm.Response{Text: `{"title":"Coffee","logline":"a cup","scenes":[{"heading":"INT. CAFE","description":"steam","dialogue":"hi"}]}`},
		llm.Response{Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"cafe","action":"open","prompt":"a cafe","duration":3}]}`},
	)
	defer done()
	seedUser(t, dsn, "e2e@studio.com")

	client := srv.Client()
	do := func(method, path, bearer, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{"_raw": string(raw)}
		}
		return resp.StatusCode, m
	}

	// 1. Login.
	code, body := do("POST", "/api/auth/login", "", `{"Email":"e2e@studio.com","Password":"pw"}`)
	if code != http.StatusOK {
		t.Fatalf("login code=%d body=%v", code, body)
	}
	token, _ := body["access_token"].(string)
	if token == "" {
		t.Fatalf("no access_token: %v", body)
	}

	// 2. Create org (creator → org_admin).
	code, body = do("POST", "/api/orgs", token, `{"name":"Studio Co"}`)
	if code != http.StatusOK {
		t.Fatalf("create org code=%d body=%v", code, body)
	}
	orgID, _ := body["id"].(string)

	// 3. Create project (editor+; org_admin satisfies).
	code, body = do("POST", "/api/orgs/"+orgID+"/projects", token,
		`{"name":"Promo","brief":"a coffee ad","contentType":"ad","targetPlatform":"web","style":"realistic"}`)
	if code != http.StatusOK {
		t.Fatalf("create project code=%d body=%v", code, body)
	}
	projID, _ := body["id"].(string)
	if projID == "" {
		t.Fatalf("no project id: %v", body)
	}

	// 4. Run (kicks planner + enqueues todos).
	code, body = do("POST", "/api/projects/"+projID+"/run", token, "")
	if code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}
	if v, _ := body["valid"].(bool); !v {
		t.Fatalf("planner not valid: %v", body)
	}

	// 5+6. Poll the events timeline until the terminal run_done arrives (the
	// worker emits run_done asynchronously AFTER inserting the shots row, so
	// polling for shots and reading events once would race the run_done append).
	// run_done implies both todos finished → script + shots are persisted.
	kinds := map[string]bool{}
	firstReadySeq, firstStartedSeq := -1.0, -1.0
	for i := 0; i < 100; i++ {
		_, evBody := do("GET", "/api/projects/"+projID+"/events", token, "")
		items, _ := evBody["items"].([]any)
		kinds = map[string]bool{}
		firstReadySeq, firstStartedSeq = -1.0, -1.0
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			k, _ := m["kind"].(string)
			if k == "" {
				continue
			}
			kinds[k] = true
			seq, _ := m["seq"].(float64)
			if k == "todo_ready" && firstReadySeq < 0 {
				firstReadySeq = seq
			}
			if k == "todo_started" && firstStartedSeq < 0 {
				firstStartedSeq = seq
			}
		}
		if kinds["run_done"] {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Timeline includes the expected kinds, and todo_ready precedes todo_started
	// for the first (script) node (spec §9 per-node transitions).
	for _, want := range []string{"planner_started", "todo_ready", "todo_started", "todo_finished", "run_done"} {
		if !kinds[want] {
			t.Fatalf("missing timeline event %q; got %v", want, kinds)
		}
	}
	if firstReadySeq < 0 || firstStartedSeq < 0 || firstReadySeq >= firstStartedSeq {
		t.Fatalf("todo_ready (seq %v) must precede todo_started (seq %v) for the script node", firstReadySeq, firstStartedSeq)
	}

	// Artifacts persisted (run_done implies the storyboard todo finished).
	sc, _ := do("GET", "/api/projects/"+projID+"/script", token, "")
	shCode, shBody := do("GET", "/api/projects/"+projID+"/shots", token, "")
	shotItems, _ := shBody["items"].([]any)
	if sc != http.StatusOK || shCode != http.StatusOK || len(shotItems) != 1 {
		t.Fatalf("script+shots not produced: scriptCode=%d shotsCode=%d shots=%d", sc, shCode, len(shotItems))
	}
}

func TestEndToEndMalformedPlanFallback(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio e2e")
	}
	// Planner returns garbage → fallback default pipeline (script→storyboard).
	// Then script + storyboard agents get valid JSON (cursor positions 2,3).
	srv, done := newHarness(t, dsn,
		llm.Response{Text: "I cannot produce a plan."},
		llm.Response{Text: `{"title":"X","logline":"y","scenes":[{"heading":"INT","description":"d","dialogue":""}]}`},
		llm.Response{Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"s","action":"a","prompt":"p","duration":2}]}`},
	)
	defer done()
	seedUser(t, dsn, "fb@studio.com")

	client := srv.Client()
	do := func(method, path, bearer, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{"_raw": string(raw)}
		}
		return resp.StatusCode, m
	}

	_, lb := do("POST", "/api/auth/login", "", `{"Email":"fb@studio.com","Password":"pw"}`)
	token, _ := lb["access_token"].(string)
	_, ob := do("POST", "/api/orgs", token, `{"name":"FB Co"}`)
	orgID, _ := ob["id"].(string)
	_, pb := do("POST", "/api/orgs/"+orgID+"/projects", token, `{"name":"P","brief":"b"}`)
	projID, _ := pb["id"].(string)

	code, body := do("POST", "/api/projects/"+projID+"/run", token, "")
	if code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}
	if fb, _ := body["fallbackUsed"].(bool); !fb {
		t.Fatalf("want fallbackUsed=true, got %v", body)
	}
	// Fallback pipeline still produces script + shots.
	ok := false
	for i := 0; i < 100; i++ {
		sc, _ := do("GET", "/api/projects/"+projID+"/script", token, "")
		shCode, shBody := do("GET", "/api/projects/"+projID+"/shots", token, "")
		if sc == http.StatusOK && shCode == http.StatusOK {
			if items, _ := shBody["items"].([]any); len(items) >= 1 {
				ok = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("fallback pipeline produced no artifacts")
	}
}

// newImageHarness builds a server with BOTH a scripted LLM (planner/script/
// storyboard) and a fake looping MediaGenerator (every asset todo gets canned
// bytes). Workers=1 keeps the scripted-LLM cursor deterministic.
func newImageHarness(t *testing.T, dsn string, responses ...llm.Response) (*httptest.Server, func()) {
	t.Helper()
	ctx := context.Background()
	providerOverride = func(config.Config) (llm.ChatModel, error) {
		return llm.NewScriptedLLM(llm.WithResponses(responses...)), nil
	}
	generatorOverride = func(config.Config) (generate.MediaGenerator, error) {
		return generate.NewFakeLooping(generate.GenResult{
			Bytes: []byte("FAKEPNG"), MimeType: "image/png", Provider: "fake", Model: "fake-img",
			Tokens: 5, ImageCount: 1, LatencyMS: 10,
		}), nil
	}
	t.Cleanup(func() { providerOverride = nil; generatorOverride = nil })
	handler, cleanup, err := build(ctx, loadCfg(t, dsn))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	srv := httptest.NewServer(handler)
	return srv, func() { srv.Close(); cleanup() }
}

// grantAdmin promotes a user to org_admin already covers admin (rank 4 ≥ admin);
// the org creator IS org_admin, so HITL (admin) is satisfied with the creator
// token. No extra grant needed.

func TestEndToEndImagePipeline(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio image e2e")
	}
	// Scripted LLM cursor: planner(valid script→storyboard) → script → storyboard(2 shots).
	srv, done := newImageHarness(t, dsn,
		llm.Response{Text: `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`},
		llm.Response{Text: `{"title":"Tea","logline":"x","scenes":[{"heading":"INT","description":"d","dialogue":""}]}`},
		llm.Response{Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"s1","action":"a","prompt":"shot1","duration":2},{"shotNo":2,"camera":"close","scene":"s2","action":"b","prompt":"shot2","duration":2}]}`},
	)
	defer done()
	seedUser(t, dsn, "img@studio.com")

	client := srv.Client()
	do := func(method, path, bearer, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{"_raw": string(raw)}
		}
		return resp.StatusCode, m
	}

	_, lb := do("POST", "/api/auth/login", "", `{"Email":"img@studio.com","Password":"pw"}`)
	token, _ := lb["access_token"].(string)
	_, ob := do("POST", "/api/orgs", token, `{"name":"Img Co"}`)
	orgID, _ := ob["id"].(string)
	_, pb := do("POST", "/api/orgs/"+orgID+"/projects", token,
		`{"name":"Tea Ad","brief":"a tea ad","contentType":"ad","targetPlatform":"web","style":"国风"}`)
	projID, _ := pb["id"].(string)

	code, body := do("POST", "/api/projects/"+projID+"/run", token, "")
	if code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}

	// Poll project assets until 2 are pending_acceptance (fan-out → 2 asset todos
	// → 2 generated assets). The looping fake generator serves both.
	var assetIDs []string
	for i := 0; i < 150; i++ {
		_, ab := do("GET", "/api/projects/"+projID+"/assets?status=pending_acceptance", token, "")
		items, _ := ab["items"].([]any)
		assetIDs = assetIDs[:0]
		for _, it := range items {
			m, _ := it.(map[string]any)
			if id, _ := m["id"].(string); id != "" {
				assetIDs = append(assetIDs, id)
			}
		}
		if len(assetIDs) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(assetIDs) != 2 {
		t.Fatalf("fan-out: want 2 pending assets, got %d", len(assetIDs))
	}

	// Admin accept the first, reject the second.
	if code, _ := do("POST", "/api/assets/"+assetIDs[0]+"/accept", token, ""); code != http.StatusOK {
		t.Fatalf("accept code=%d", code)
	}
	if code, _ := do("POST", "/api/assets/"+assetIDs[1]+"/reject", token, ""); code != http.StatusOK {
		t.Fatalf("reject code=%d", code)
	}
	// Re-accept the already-accepted asset → 409.
	if code, _ := do("POST", "/api/assets/"+assetIDs[0]+"/accept", token, ""); code != http.StatusConflict {
		t.Fatalf("re-accept should 409, got %d", code)
	}

	// Regenerate the accepted one is NOT allowed (it's not pending) → 409. So
	// regenerate must target a fresh pending asset: regenerate works on pending.
	// Create that situation by regenerating from a pending asset is impossible
	// now (both terminal). Instead assert regenerate's 409 guard on a terminal
	// asset, then accept the regenerate child below via a new pending asset.
	if code, _ := do("POST", "/api/assets/"+assetIDs[1]+"/regenerate", token, `{"prompt":"edited"}`); code != http.StatusConflict {
		t.Fatalf("regenerate on rejected (terminal) should 409, got %d", code)
	}

	// Library search returns the accepted asset (filter status=accepted).
	_, lib := do("GET", "/api/orgs/"+orgID+"/assets?status=accepted", token, "")
	items, _ := lib["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("library accepted: want 1, got %d", len(items))
	}

	// Asset content 302-redirects to a signed URL.
	req, _ := http.NewRequest("GET", srv.URL+"/api/assets/"+assetIDs[0]+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cr, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	cr.Body.Close()
	if cr.StatusCode != http.StatusFound || cr.Header.Get("Location") == "" {
		t.Fatalf("content should 302 to signed URL, got %d loc=%q", cr.StatusCode, cr.Header.Get("Location"))
	}
}

func TestEndToEndRegenerateLineage(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio regenerate e2e")
	}
	// One shot → one pending asset; regenerate it → v2 child + new pending asset.
	srv, done := newImageHarness(t, dsn,
		llm.Response{Text: `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`},
		llm.Response{Text: `{"title":"T","logline":"x","scenes":[{"heading":"INT","description":"d","dialogue":""}]}`},
		llm.Response{Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"s1","action":"a","prompt":"only-shot","duration":2}]}`},
	)
	defer done()
	seedUser(t, dsn, "regen@studio.com")

	client := srv.Client()
	do := func(method, path, bearer, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{"_raw": string(raw)}
		}
		return resp.StatusCode, m
	}
	_, lb := do("POST", "/api/auth/login", "", `{"Email":"regen@studio.com","Password":"pw"}`)
	token, _ := lb["access_token"].(string)
	_, ob := do("POST", "/api/orgs", token, `{"name":"Regen Co"}`)
	orgID, _ := ob["id"].(string)
	_, pb := do("POST", "/api/orgs/"+orgID+"/projects", token, `{"name":"P","brief":"b","style":"国风"}`)
	projID, _ := pb["id"].(string)
	_, _ = do("POST", "/api/projects/"+projID+"/run", token, "")

	var v1ID string
	for i := 0; i < 150; i++ {
		_, ab := do("GET", "/api/projects/"+projID+"/assets?status=pending_acceptance", token, "")
		items, _ := ab["items"].([]any)
		if len(items) == 1 {
			m, _ := items[0].(map[string]any)
			v1ID, _ = m["id"].(string)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if v1ID == "" {
		t.Fatalf("no pending v1 asset")
	}
	// Regenerate the pending v1 → rejects v1, spawns v2 + a new asset todo.
	code, rb := do("POST", "/api/assets/"+v1ID+"/regenerate", token, `{"prompt":"edited prompt"}`)
	if code != http.StatusOK {
		t.Fatalf("regenerate code=%d body=%v", code, rb)
	}
	v2ID, _ := rb["newAssetId"].(string)
	if v2ID == "" {
		t.Fatalf("no v2 asset id: %v", rb)
	}
	// The v2 asset (after the worker runs the regenerate todo) reports version 2
	// + parent = v1, and lands pending_acceptance again.
	for i := 0; i < 150; i++ {
		_, av := do("GET", "/api/assets/"+v2ID, token, "")
		asset, _ := av["asset"].(map[string]any)
		ver, _ := asset["version"].(float64)
		parent, _ := asset["parentAssetId"].(string)
		status, _ := asset["status"].(string)
		if ver == 2 && parent == v1ID && status == "pending_acceptance" {
			versions, _ := av["versions"].([]any)
			if len(versions) == 2 {
				return // success: v1→v2 lineage with both versions in history
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("v2 lineage/regeneration did not converge")
}

// newDoer returns the standard JSON request helper used by the M3 e2e tests.
func newDoer(t *testing.T, srv *httptest.Server) func(method, path, bearer, body string) (int, map[string]any) {
	t.Helper()
	client := srv.Client()
	return func(method, path, bearer, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{"_raw": string(raw)}
		}
		return resp.StatusCode, m
	}
}

// newHarnessWith builds a server with an explicit MediaGenerator + extra env.
func newHarnessWith(t *testing.T, dsn string, gen generate.MediaGenerator, extraEnv map[string]string, responses ...llm.Response) (*httptest.Server, func()) {
	t.Helper()
	ctx := context.Background()
	providerOverride = func(config.Config) (llm.ChatModel, error) {
		return llm.NewScriptedLLM(llm.WithResponses(responses...)), nil
	}
	generatorOverride = func(config.Config) (generate.MediaGenerator, error) { return gen, nil }
	t.Cleanup(func() { providerOverride = nil; generatorOverride = nil })
	handler, cleanup, err := build(ctx, loadCfgWith(t, dsn, extraEnv))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	srv := httptest.NewServer(handler)
	return srv, func() { srv.Close(); cleanup() }
}

// setupOrgProject logs in, creates an org + project, and returns (token, orgID, projectID).
func setupOrgProject(t *testing.T, do func(string, string, string, string) (int, map[string]any), email, style string) (string, string, string) {
	t.Helper()
	seedUser(t, os.Getenv("LLM_AGENT_STUDIO_PG_URL"), email)
	_, lb := do("POST", "/api/auth/login", "", `{"Email":"`+email+`","Password":"pw"}`)
	token, _ := lb["access_token"].(string)
	if token == "" {
		t.Fatalf("no token: %v", lb)
	}
	_, ob := do("POST", "/api/orgs", token, `{"name":"M3 Co"}`)
	orgID, _ := ob["id"].(string)
	_, pb := do("POST", "/api/orgs/"+orgID+"/projects", token,
		`{"name":"P","brief":"a tea ad","contentType":"ad","targetPlatform":"web","style":"`+style+`"}`)
	projID, _ := pb["id"].(string)
	if orgID == "" || projID == "" {
		t.Fatalf("org/project bootstrap failed: %v %v", ob, pb)
	}
	return token, orgID, projID
}

// m3PipelineResponses is the canonical 1-shot pipeline script for the M3 e2e:
// planner(valid) → script → storyboard(1 shot) → review(prescreen, score 87).
func m3PipelineResponses() []llm.Response {
	return []llm.Response{
		{Text: `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`},
		{Text: `{"title":"Tea","logline":"x","scenes":[{"heading":"INT","description":"d","dialogue":""}]}`},
		{Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"s1","action":"a","prompt":"shot1","duration":2}]}`},
		{Text: `{"score":87,"flags":["minor_blur"],"note":"prompt-consistent"}`},
	}
}

// pollAssetWithStatus polls the project's assets until one reaches the wanted
// status, returning its id ("" on timeout).
func pollAssetWithStatus(do func(string, string, string, string) (int, map[string]any), token, projID, status string) string {
	for i := 0; i < 150; i++ {
		_, ab := do("GET", "/api/projects/"+projID+"/assets?status="+status, token, "")
		items, _ := ab["items"].([]any)
		if len(items) >= 1 {
			m, _ := items[0].(map[string]any)
			id, _ := m["id"].(string)
			return id
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ""
}

func TestEndToEndModelRoutingTakesEffect(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio e2e")
	}
	// Register a distinguishable generator under fakeB/mB via the e2e seam.
	registryHook = func(r *generate.Registry) {
		r.Register("fakeB", "mB", generate.NewFakeLooping(generate.GenResult{
			Bytes: []byte("B"), MimeType: "image/png", Provider: "fakeB", Model: "mB", ImageCount: 1,
		}))
	}
	defer func() { registryHook = nil }()
	defGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("D"), MimeType: "image/png", Provider: "default", Model: "d", ImageCount: 1,
	})
	srv, done := newHarnessWith(t, dsn, defGen, map[string]string{"REVIEW_PRESCREEN": "true"}, m3PipelineResponses()...)
	defer done()
	do := newDoer(t, srv)
	token, orgID, projID := setupOrgProject(t, do, "route@studio.com", "")

	// Admin sets the org default model BEFORE running.
	code, mc := do("POST", "/api/orgs/"+orgID+"/model-configs", token,
		`{"kind":"image","provider":"fakeB","model":"mB","enabled":true,"isDefault":true}`)
	if code != http.StatusOK {
		t.Fatalf("model-config code=%d body=%v", code, mc)
	}
	if code, body := do("POST", "/api/projects/"+projID+"/run", token, ""); code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}
	assetID := pollAssetWithStatus(do, token, projID, "pending_acceptance")
	if assetID == "" {
		t.Fatalf("no pending asset produced")
	}
	_, av := do("GET", "/api/assets/"+assetID, token, "")
	asset, _ := av["asset"].(map[string]any)
	if provider, _ := asset["provider"].(string); provider != "fakeB" {
		t.Fatalf("org default model did not route: asset provider = %q, want fakeB (asset=%v)", provider, asset)
	}
}

// TestEndToEndFakeModeKeylessPipeline proves FIX D end-to-end: with PROVIDER=fake
// and NO provider/generator overrides and NO API keys, build() wires the fake
// chat model + dev fake generator so the whole pipeline (Run → script/storyboard
// → asset) completes to a pending_acceptance asset with stored bytes.
func TestEndToEndFakeModeKeylessPipeline(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio fake-mode e2e")
	}
	// No providerOverride / generatorOverride: build() must construct the fake
	// chat model + dev fake generator purely from PROVIDER=fake.
	ctx := context.Background()
	cfg := loadCfgWith(t, dsn, map[string]string{"PROVIDER": "fake"})
	if !cfg.FakeGen {
		t.Fatalf("PROVIDER=fake did not enable FakeGen")
	}
	handler, cleanup, err := build(ctx, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer func() { srv.Close(); cleanup() }()
	do := newDoer(t, srv)
	token, _, projID := setupOrgProject(t, do, "fake@studio.com", "国风")

	if code, body := do("POST", "/api/projects/"+projID+"/run", token, ""); code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}
	assetID := pollAssetWithStatus(do, token, projID, "pending_acceptance")
	if assetID == "" {
		t.Fatalf("keyless fake-mode pipeline produced no pending_acceptance asset")
	}
	_, av := do("GET", "/api/assets/"+assetID, token, "")
	asset, _ := av["asset"].(map[string]any)
	if provider, _ := asset["provider"].(string); provider != "fake" {
		t.Fatalf("fake-mode asset provider = %q, want fake", provider)
	}
	// The asset content 302-redirects to a signed URL (stored bytes exist).
	req, _ := http.NewRequest("GET", srv.URL+"/api/assets/"+assetID+"/content", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cr, err := client.Do(req)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	cr.Body.Close()
	if cr.StatusCode != http.StatusFound || cr.Header.Get("Location") == "" {
		t.Fatalf("fake-mode asset content should 302 to a signed URL, got %d", cr.StatusCode)
	}
}

// TestBuildGeneratorNeverUsesChatModel proves the model-config fix: in non-fake
// mode with NO image provider keys, buildGenerator returns the SAFE placeholder
// (DevFakeGenerator, provider "fake") instead of binding an image generator to
// the chat model (which yielded capability-not-supported on every asset). No
// Postgres required — pure unit test of the wiring decision.
func TestBuildGeneratorNeverUsesChatModel(t *testing.T) {
	// Non-fake chat config (the deepseek-chat default) with no image keys: the
	// default generator must NOT be an openai/minimax image gen built from cfg.Model.
	nonFake := config.Config{Provider: "deepseek", Model: "deepseek-chat", APIKey: "chat-key"}
	g, err := buildGenerator(nonFake)
	if err != nil {
		t.Fatalf("buildGenerator(non-fake): %v", err)
	}
	if _, ok := g.(*generate.DevFakeGenerator); !ok {
		t.Fatalf("non-fake default generator = %T, want *generate.DevFakeGenerator (placeholder)", g)
	}
	res, gerr := g.Generate(context.Background(), generate.GenRequest{Prompt: "x"})
	if gerr != nil {
		t.Fatalf("placeholder Generate: %v", gerr)
	}
	if res.Provider != "fake" {
		t.Fatalf("placeholder provider = %q, want fake", res.Provider)
	}

	// Fake mode: also the placeholder (unchanged behavior).
	fake, err := buildGenerator(config.Config{FakeGen: true})
	if err != nil {
		t.Fatalf("buildGenerator(fake): %v", err)
	}
	if _, ok := fake.(*generate.DevFakeGenerator); !ok {
		t.Fatalf("fake-mode generator = %T, want *generate.DevFakeGenerator", fake)
	}
}

// TestOllamaProviderWired proves Ollama is a first-class chat provider: the env
// default (PROVIDER=ollama) and the per-org chat factory both construct a usable
// ChatModel with no API key (Ollama is local/keyless; base_url缺省 localhost:11434).
// Construction is lazy (no network), so this is a pure unit test.
func TestOllamaProviderWired(t *testing.T) {
	// Env default chat model.
	m, err := buildModel(config.Config{Provider: "ollama", Model: "llama3"})
	if err != nil || m == nil {
		t.Fatalf("buildModel(ollama) = (%v, %v), want non-nil model, nil err", m, err)
	}
	// Per-org chat factory (BYOK path): no apiKey required; custom base_url honored.
	f := buildChatFactory(sdktrace.NewTracerProvider())
	cm, err := f("ollama", "qwen2.5", "", "http://localhost:11434")
	if err != nil || cm == nil {
		t.Fatalf("buildChatFactory(ollama) = (%v, %v), want non-nil model, nil err", cm, err)
	}
}

// gatedGen blocks Generate until released — lets the e2e freeze an asset in
// 'generating' to exercise the cancel path.
type gatedGen struct{ release chan struct{} }

func (g *gatedGen) Kind() string { return "image" }
func (g *gatedGen) Generate(ctx context.Context, _ generate.GenRequest) (generate.GenResult, error) {
	select {
	case <-g.release:
	case <-ctx.Done():
		return generate.GenResult{}, ctx.Err()
	}
	return generate.GenResult{Bytes: []byte("X"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1}, nil
}

func TestEndToEndCancelTerminalStatesAssets(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio e2e")
	}
	gen := &gatedGen{release: make(chan struct{})}
	srv, done := newHarnessWith(t, dsn, gen, map[string]string{"REVIEW_PRESCREEN": "true"}, m3PipelineResponses()...)
	defer done()
	do := newDoer(t, srv)
	token, _, projID := setupOrgProject(t, do, "cancel@studio.com", "")
	if code, body := do("POST", "/api/projects/"+projID+"/run", token, ""); code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}
	// Wait until the asset is in-flight ('generating', frozen inside gatedGen).
	if id := pollAssetWithStatus(do, token, projID, "generating"); id == "" {
		t.Fatalf("asset never entered generating")
	}
	if code, body := do("POST", "/api/projects/"+projID+"/cancel", token, ""); code != http.StatusOK {
		t.Fatalf("cancel code=%d body=%v", code, body)
	}
	// Cancel sweeps the in-flight asset to terminal 'canceled' (M2 carry #3)…
	if id := pollAssetWithStatus(do, token, projID, "canceled"); id == "" {
		t.Fatalf("generating asset was not terminal-stated on cancel")
	}
	_, pj := do("GET", "/api/projects/"+projID, token, "")
	if status, _ := pj["status"].(string); status != "canceled" {
		t.Fatalf("project status = %q, want canceled", status)
	}
	// …and the in-flight result is DISCARDED on arrival (SetBlob guards on
	// status='generating'): release the generator and verify the asset never
	// resurfaces as pending_acceptance.
	close(gen.release)
	for i := 0; i < 10; i++ {
		_, ab := do("GET", "/api/projects/"+projID+"/assets?status=pending_acceptance", token, "")
		if items, _ := ab["items"].([]any); len(items) != 0 {
			t.Fatalf("discarded in-flight result resurfaced as pending_acceptance")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestEndToEndPrescreenScoreSurfaces(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio e2e")
	}
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("P"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1,
	})
	srv, done := newHarnessWith(t, dsn, fakeGen, map[string]string{"REVIEW_PRESCREEN": "true"}, m3PipelineResponses()...)
	defer done()
	do := newDoer(t, srv)
	token, _, projID := setupOrgProject(t, do, "prescreen@studio.com", "")
	if code, body := do("POST", "/api/projects/"+projID+"/run", token, ""); code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}
	assetID := pollAssetWithStatus(do, token, projID, "pending_acceptance")
	if assetID == "" {
		t.Fatalf("no pending asset produced")
	}
	// The prescreen verdict (scripted: score 87) surfaces on the asset.
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, av := do("GET", "/api/assets/"+assetID, token, "")
		asset, _ := av["asset"].(map[string]any)
		if score, _ := asset["prescreenScore"].(float64); score == 87 {
			flags, _ := asset["prescreenFlags"].([]any)
			if len(flags) != 1 {
				t.Fatalf("prescreenFlags = %v, want 1 flag", flags)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("prescreen score never surfaced: %v", asset)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestEndToEndGenerationQuota429(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio e2e")
	}
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("P"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1,
	})
	srv, done := newHarnessWith(t, dsn, fakeGen,
		map[string]string{"ORG_DAILY_GEN_QUOTA": "1", "REVIEW_PRESCREEN": "true"}, m3PipelineResponses()...)
	defer done()
	do := newDoer(t, srv)
	token, orgID, projID := setupOrgProject(t, do, "quota@studio.com", "")
	// First run passes (0 generations so far) and records 1 generation.
	if code, body := do("POST", "/api/projects/"+projID+"/run", token, ""); code != http.StatusAccepted {
		t.Fatalf("first run code=%d body=%v", code, body)
	}
	if id := pollAssetWithStatus(do, token, projID, "pending_acceptance"); id == "" {
		t.Fatalf("first run produced no asset")
	}
	// The asset flips to 'pending_acceptance' (SetBlob) one step BEFORE its
	// generation row is written to the cost ledger (RecordPriced); the quota
	// gate counts ledger rows, so wait for the ledger to reflect the generation
	// before the over-quota run (else we race the worker and see 0/1).
	for i := 0; i < 150; i++ {
		_, cb := do("GET", "/api/orgs/"+orgID+"/cost", token, "")
		if gens, _ := cb["generations"].(float64); gens >= 1 {
			break
		}
		if i == 149 {
			t.Fatalf("first run's generation never reached the cost ledger")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Second run: the org is at quota (1/1 in the rolling window) → 429.
	code, body := do("POST", "/api/projects/"+projID+"/run", token, "")
	if code != http.StatusTooManyRequests {
		t.Fatalf("over-quota run should 429, got %d body=%v", code, body)
	}
	// spec §12 E2E ⑥ 成本账本累计 (评审修复 M8): the cost center shows the
	// first run's generation in the org aggregate.
	code, costBody := do("GET", "/api/orgs/"+orgID+"/cost", token, "")
	if code != http.StatusOK {
		t.Fatalf("org cost code=%d body=%v", code, costBody)
	}
	if gens, _ := costBody["generations"].(float64); gens < 1 {
		t.Fatalf("cost ledger should show >= 1 generation after the first run, got %v", costBody)
	}
}

// TestEndToEndFakeAsyncVideoSubmitPoll exercises the M4 async engine end-to-end
// via the FakeAsync generator (zero network, deterministic submit→poll). The
// FakeAsync is injected through the registryHook seam — WRAPPED with
// obs.WrapGenerator exactly like production, so this also proves the otel wrapper
// preserves the AsyncGenerator interface in the live assembly chain (B1): if the
// wrapper stripped Submit/Poll the worker's routed.(AsyncGenerator) assertion
// would be false and the asset would never advance past 'generating'.
//
// Routing note: the storyboard fan-out hardcodes the asset todo kind to "image"
// (worker.go), and DefaultForOrg keys on (org,kind). So the org's IMAGE default
// is pointed at fake/fake-video-async; the per-second pricing/ledger key on
// provider+model, so per-second billing still applies, and the async path is
// taken purely because the resolved generator is an AsyncGenerator (spec §4.2).
func TestEndToEndFakeAsyncVideoSubmitPoll(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio async e2e")
	}
	// FakeAsync: 2 polls (poll#1 pending → poll#2 done). Bytes-only done result
	// (no URL) so completeAsync stores the bytes directly without the SSRF-safe
	// fetcher (production build() uses a non-loopback fetcher; URL+loopback pull
	// is covered by worker/fetch unit tests). EstSeconds echoes the request
	// duration (6s from the storyboard shot below) for per-second billing.
	doneResult := generate.GenResult{
		Bytes: []byte("FAKEMP4"), MimeType: "video/mp4", Provider: "fake", Model: "fake-video-async",
	}
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	registryHook = func(r *generate.Registry) {
		fa := generate.NewFakeAsync("video", 2, doneResult)
		wrapped := obs.WrapGenerator(fa, tp) // mirror production wrapping (B1)
		if _, ok := wrapped.(generate.AsyncGenerator); !ok {
			t.Fatalf("B1: obs.WrapGenerator(FakeAsync) lost the AsyncGenerator seam in the assembly chain")
		}
		r.Register("fake", "fake-video-async", wrapped)
	}
	defer func() { registryHook = nil }()

	// Default (env) generator is a plain sync image fake — unused once the org
	// default routes to fake-video-async, but build() requires one.
	defGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("D"), MimeType: "image/png", Provider: "default", Model: "d", ImageCount: 1,
	})
	// One shot, duration 6 → DurationSeconds=6 → 6 * 500000 micros/sec = 3000000.
	srv, done := newHarnessWith(t, dsn, defGen,
		map[string]string{"REVIEW_PRESCREEN": "false", "POLL_BACKOFF": "50ms", "MAX_POLL_BACKOFF": "200ms"},
		llm.Response{Text: `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`},
		llm.Response{Text: `{"title":"V","logline":"x","scenes":[{"heading":"INT","description":"d","dialogue":""}]}`},
		llm.Response{Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"s1","action":"a","prompt":"a city flythrough","duration":6}]}`},
	)
	defer done()
	do := newDoer(t, srv)
	token, orgID, projID := setupOrgProject(t, do, "async@studio.com", "")

	// Point the org's IMAGE default at the async fake (routing keys on kind=image
	// from fan-out; the AsyncGenerator interface drives the submit→poll engine).
	if code, mc := do("POST", "/api/orgs/"+orgID+"/model-configs", token,
		`{"kind":"image","provider":"fake","model":"fake-video-async","enabled":true,"isDefault":true}`); code != http.StatusOK {
		t.Fatalf("model-config code=%d body=%v", code, mc)
	}
	if code, body := do("POST", "/api/projects/"+projID+"/run", token, ""); code != http.StatusAccepted {
		t.Fatalf("run code=%d body=%v", code, body)
	}

	// The async asset advances submitted → pending_acceptance across multiple
	// short poll dispatches (the worker re-claims the rescheduled todo each poll).
	assetID := pollAssetWithStatus(do, token, projID, "pending_acceptance")
	if assetID == "" {
		t.Fatalf("async asset never reached pending_acceptance (submit→poll engine did not complete)")
	}
	_, av := do("GET", "/api/assets/"+assetID, token, "")
	asset, _ := av["asset"].(map[string]any)
	if provider, _ := asset["provider"].(string); provider != "fake" {
		t.Fatalf("async asset provider = %q, want fake (routing did not take effect): %v", provider, asset)
	}

	// Per-second billing: 6s * 500000 micros/sec = 3,000,000 micros in the ledger.
	for i := 0; i < 150; i++ {
		_, cb := do("GET", "/api/orgs/"+orgID+"/cost", token, "")
		gens, _ := cb["generations"].(float64)
		micros, _ := cb["costMicros"].(float64)
		if gens >= 1 && micros >= 3000000 {
			break
		}
		if i == 149 {
			t.Fatalf("per-second billing did not land: cost=%v", cb)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Timeline (spec §9.5 M2): asset_submitted + asset_generated are emitted;
	// asset_polling is NEVER emitted (deliberately not whitelisted — poll noise).
	_, eb := do("GET", "/api/projects/"+projID+"/events", token, "")
	items, _ := eb["items"].([]any)
	var sawSubmitted, sawGenerated, sawPolling bool
	for _, it := range items {
		m, _ := it.(map[string]any)
		switch m["kind"] {
		case "asset_submitted":
			sawSubmitted = true
		case "asset_generated":
			sawGenerated = true
		case "asset_polling":
			sawPolling = true
		}
	}
	if !sawSubmitted {
		t.Fatalf("timeline missing asset_submitted: %v", items)
	}
	if !sawGenerated {
		t.Fatalf("timeline missing asset_generated: %v", items)
	}
	if sawPolling {
		t.Fatalf("asset_polling must never be emitted (M4 DEFER)")
	}
}

func TestEndToEndCustomWorkflow(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the studio custom workflow e2e")
	}

	// We define 2 scripted responses:
	//   1) script agent → custom script JSON
	//   2) storyboard agent → custom shots JSON
	// The planner response is omitted because custom workflow bypasses it.
	srv, done := newHarness(t, dsn,
		llm.Response{Text: `{"title":"Custom Tea","logline":"custom x","scenes":[{"heading":"INT","description":"d","dialogue":""}]}`},
		llm.Response{Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"s1","action":"a","prompt":"custom shot1","duration":2}]}`},
	)
	defer done()

	seedUser(t, dsn, "custom_e2e@studio.com")
	client := srv.Client()
	do := func(method, path, bearer, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{"_raw": string(raw)}
		}
		return resp.StatusCode, m
	}

	// 1. Login.
	code, body := do("POST", "/api/auth/login", "", `{"Email":"custom_e2e@studio.com","Password":"pw"}`)
	if code != http.StatusOK {
		t.Fatalf("login code=%d body=%v", code, body)
	}
	token, _ := body["access_token"].(string)

	// 2. Create org.
	_, ob := do("POST", "/api/orgs", token, `{"name":"Custom Org"}`)
	orgID, _ := ob["id"].(string)

	// 3. Create prompt override.
	pCode, pb := do("POST", "/api/orgs/"+orgID+"/prompts", token,
		`{"name":"custom_prompt","content":"custom template","style":"anime"}`)
	if pCode != http.StatusCreated {
		t.Fatalf("create prompt code=%d body=%v", pCode, pb)
	}
	promptID, _ := pb["id"].(string)

	// 4. Create project.
	_, projb := do("POST", "/api/orgs/"+orgID+"/projects", token,
		`{"name":"Promo","brief":"a coffee ad","contentType":"ad","targetPlatform":"web","style":"realistic"}`)
	projID, _ := projb["id"].(string)

	// 5. Create a workflow on the project with the builtin script→storyboard graph.
	// (The legacy PUT /api/projects/{id} with customWorkflowEnabled/workflowNodes
	// no longer persists those fields — custom workflows moved to the workflows-API.)
	wfNodes := `[
		{"id": "node-script", "type": "script", "promptId": "` + promptID + `", "dependsOn": []},
		{"id": "node-storyboard", "type": "storyboard", "dependsOn": ["node-script"]}
	]`
	wfCode, wfb := do("POST", "/api/projects/"+projID+"/workflows", token,
		`{"name":"custom-flow","nodes":`+wfNodes+`}`)
	if wfCode != http.StatusOK {
		t.Fatalf("create workflow code=%d body=%v", wfCode, wfb)
	}
	wfID, _ := wfb["id"].(string)
	if wfID == "" {
		t.Fatalf("no workflow id in response: %v", wfb)
	}

	// 6. Run the workflow (PlanCustom tagged with workflow_id; enqueues custom todos).
	runCode, runb := do("POST", "/api/projects/"+projID+"/workflows/"+wfID+"/run", token, "")
	if runCode != http.StatusAccepted {
		t.Fatalf("run workflow code=%d body=%v", runCode, runb)
	}

	// 7. Verify the plans row was created and fallback_used is false, valid is true.
	valid, _ := runb["valid"].(bool)
	fallback, _ := runb["fallbackUsed"].(bool)
	if !valid || fallback {
		t.Fatalf("run result invalid/fallback: %+v", runb)
	}

	// 8. Wait/Poll for todos to check that they run and reach review status.
	var status string
	for i := 0; i < 50; i++ {
		pCode, pb = do("GET", "/api/projects/"+projID, token, "")
		status, _ = pb["status"].(string)
		if status == "review" || status == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if status != "review" {
		t.Fatalf("expected project status to be review, got %q", status)
	}
}

// TestFakeChatModelPicturebookPrompts guards the fake (keyless dev/demo) ChatModel
// against the BUG that picturebook generation could never run: the 绘本 script /
// storyboard system prompts are Chinese (agents.pictureBookSystemPrompt /
// pictureBookStoryboardSystemPrompt) and do NOT contain the English "screenwriter"
// / "storyboard" markers, so before the fix they fell through to the default review
// JSON and ScriptAgent/StoryboardAgent failed ("empty script" / "no shots produced").
// These markers must keep routing to structured script/storyboard output.
func TestFakeChatModelPicturebookPrompts(t *testing.T) {
	m := &fakeChatModel{}
	gen := func(sys string) map[string]any {
		resp, err := m.Generate(context.Background(), llm.Request{SystemPrompt: sys})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
			t.Fatalf("response not JSON: %v (%q)", err, resp.Text)
		}
		return out
	}

	// 绘本脚本：必须拿到 title + scenes（非默认评审 JSON 的 score 字段）。
	script := gen("你是一名儿童绘本作家。请为 3-6 岁儿童写一个故事…")
	if _, hasScore := script["score"]; hasScore {
		t.Fatalf("picturebook script prompt fell through to default review JSON: %v", script)
	}
	if title, _ := script["title"].(string); title == "" {
		t.Fatalf("picturebook script missing title: %v", script)
	}
	if scenes, _ := script["scenes"].([]any); len(scenes) == 0 {
		t.Fatalf("picturebook script missing scenes: %v", script)
	}

	// 绘本分镜：必须拿到 shots，且第一页（封面）action 留空。
	board := gen("你是一名儿童绘本分镜师。请把脚本拆成「跨页」…")
	shots, _ := board["shots"].([]any)
	if len(shots) == 0 {
		t.Fatalf("picturebook storyboard produced no shots: %v", board)
	}
	if first, _ := shots[0].(map[string]any); first != nil {
		if action, _ := first["action"].(string); action != "" {
			t.Fatalf("cover shot action must be empty, got %q", action)
		}
	}
}
