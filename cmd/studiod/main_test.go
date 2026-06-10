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
)

// loadCfg builds a deterministic config for the e2e (Workers=1 so the single
// injected ScriptedLLM cursor advances in a predictable order).
func loadCfg(t *testing.T, dsn string) config.Config {
	t.Helper()
	cfg, err := config.LoadFromLookup(func(k string) (string, bool) {
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
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func newHarness(t *testing.T, dsn string, responses ...llm.Response) (*httptest.Server, func()) {
	t.Helper()
	ctx := context.Background()
	providerOverride = func(config.Config) (llm.ChatModel, error) {
		return llm.NewScriptedLLM(llm.WithResponses(responses...)), nil
	}
	t.Cleanup(func() { providerOverride = nil })
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
