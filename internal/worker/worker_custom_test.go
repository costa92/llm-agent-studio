package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"gorm.io/gorm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/modelrouter"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// TestSubstituteVars is a pure unit test — no DB required.
func TestSubstituteVars(t *testing.T) {
	out := substituteVars("Hello {{draft}}! No {{unknown}} here.", map[string]string{"draft": "world"})
	if !strings.Contains(out, "world") {
		t.Fatalf("want 'world' in output, got %q", out)
	}
	// unknown placeholder must survive unchanged
	if !strings.Contains(out, "{{unknown}}") {
		t.Fatalf("unknown placeholder should be left intact, got %q", out)
	}
	// known placeholder must be replaced
	if strings.Contains(out, "{{draft}}") {
		t.Fatalf("{{draft}} should be replaced, got %q", out)
	}

	// Whitespace-tolerant: frontend extractTemplateVars trims token names, so
	// "{{ draft }}" authored in a template must resolve to the same binding as
	// "{{draft}}". Verify BOTH forms in a single template are replaced (Blocker 2).
	tpl := "A={{draft}}, B={{ draft }}, C={{ draft   }}, X={{other}}"
	out2 := substituteVars(tpl, map[string]string{"draft": "VALUE"})
	// All three {{draft}} variants must be replaced.
	if strings.Contains(out2, "{{draft}}") || strings.Contains(out2, "{{ draft }}") || strings.Contains(out2, "{{ draft   }}") {
		t.Fatalf("whitespace variants not replaced, got %q", out2)
	}
	// The replacement must appear 3 times.
	if strings.Count(out2, "VALUE") != 3 {
		t.Fatalf("want 3 replacements, got output %q", out2)
	}
	// {{other}} must survive (unbound variable left intact).
	if !strings.Contains(out2, "{{other}}") {
		t.Fatalf("unbound {{other}} should be intact, got %q", out2)
	}
}

// customTestWorker builds a Worker wired with a real Router (so routedChatModel
// works) whose BuildChat always returns the provided chatModel. Mirrors the
// pattern in router_test.go:TestWorkerRoutesChatModelViaRouter.
func customTestWorker(t *testing.T, chatModel llm.ChatModel) *Worker {
	t.Helper()
	db := assetTestGorm(t)
	box := testBox(t)
	ms := models.New(db, box)
	ctx := context.Background()
	// A text model_config with a per-config key so BuildChat fires on resolution.
	orgID := "org_custom_" + randHex3()
	if _, err := ms.Create(ctx, models.CreateInput{
		OrgID: orgID, Kind: "text", Provider: "openai-compatible", Model: "x",
		Enabled: true, IsDefault: true, APIKey: "sk-test",
	}); err != nil {
		t.Fatalf("create text config: %v", err)
	}

	reg := generate.NewRegistry()
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("FAKEPNG"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
	})
	reg.SetDefault(fakeGen)

	router := modelrouter.New(modelrouter.Config{
		Models:   ms,
		Registry: reg,
		BuildChat: func(_, _, _, _ string) (llm.ChatModel, error) {
			return chatModel, nil
		},
	})

	// Stash the orgID for test callers to use when inserting projects.
	t.Setenv("_CUSTOM_TEST_ORG_ID", orgID)

	bound := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{}`}))
	return New(Config{
		DB:       db,
		Todos:    todos.New(db),
		Projects: project.New(db),
		Events:   events.New(db),
		Script:   studioagents.NewScriptAgent(bound),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:  testStorage(),
		Assets:   assets.New(db),
		Cost:     cost.New(db),
		Models:   ms,
		Registry: reg,
		Router:   router,
		WorkerID: "custom-test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
}

// TestRunCustomLLM_TextAndJSON is the core executor test:
//  1. Seeds a script todo (status=done, output_ref="script:<id>") + a matching
//     scripts row.
//  2. Seeds a typed custom todo (depends_on the script todo — the expr value
//     channel is project-scoped + direct-deps gated) whose input_json has
//     kind=llm, params with {{draft}} template, and
//     variables:[{name:"draft",sourceTodoId:<scriptTodo>}].
//  3. Runs runCustom with a mock chat model.
//  4. Asserts a node_outputs row is written with format="text".
//  5. Re-runs with outputFormat="json" and a non-JSON model response → error.
//  6. Asserts unbound (sourceTodoId="") variable does NOT crash.
func TestRunCustomLLM_TextAndJSON(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	pool := assetTestPool(t)

	// mock chat model returns a fixed string
	const mockAnswer = "Here is the translated text."
	mockModel := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: mockAnswer},
		llm.Response{Text: mockAnswer},
		llm.Response{Text: "not-json-at-all"},
	))

	w := customTestWorker(t, mockModel)
	orgID := os.Getenv("_CUSTOM_TEST_ORG_ID")

	// Insert a project owned by the org that was configured in customTestWorker.
	var projID string
	_ = pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID,
	).Scan(&projID)

	// Seed a script row + its done todo.
	scriptID := newID()
	scriptContent := `{"title":"Draft Title","logline":"A short film."}`
	_, err := pool.Exec(ctx,
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,'t-dummy-s',$3,1)`,
		scriptID, projID, []byte(scriptContent))
	if err != nil {
		t.Fatalf("seed script: %v", err)
	}
	scriptTodoID := newID()
	_, err = pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','script','done',$3,'{}')`,
		scriptTodoID, projID, "script:"+scriptID)
	if err != nil {
		t.Fatalf("seed script todo: %v", err)
	}

	// Build input_json for the typed custom todo (text output).
	buildInput := func(outputFormat string) []byte {
		in := map[string]any{
			"kind": "llm",
			"params": map[string]any{
				"systemPrompt": "You are a translator.",
				"userPrompt":   "Translate: {{draft}}",
				"outputFormat": outputFormat,
				"variables": []map[string]any{
					{"name": "draft", "sourceTodoId": scriptTodoID},
				},
			},
		}
		b, _ := json.Marshal(in)
		return b
	}

	// Sub-test 1: text output → writes a node_outputs row.
	t.Run("text output writes node_outputs row", func(t *testing.T) {
		customTodoID := newID()
		_, err = pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,'plan-x','custom:translate','running',ARRAY[$3]::text[],$4)`,
			customTodoID, projID, scriptTodoID, buildInput("text"))
		if err != nil {
			t.Fatalf("seed custom todo: %v", err)
		}

		outputRef, err := w.runCustom(ctx, claimed{
			todoID:    customTodoID,
			projectID: projID,
			typ:       "custom:translate",
			attempts:  1,
			input:     buildInput("text"),
		})
		if err != nil {
			t.Fatalf("runCustom text: %v", err)
		}
		if !strings.HasPrefix(outputRef, "custom:") {
			t.Fatalf("want outputRef to start with 'custom:', got %q", outputRef)
		}

		// Verify the node_outputs row exists with format=text.
		outID := strings.TrimPrefix(outputRef, "custom:")
		var format, content string
		err = pool.QueryRow(ctx,
			`SELECT format, content FROM node_outputs WHERE id=$1`, outID).Scan(&format, &content)
		if err != nil {
			t.Fatalf("load node_output: %v", err)
		}
		if format != "text" {
			t.Fatalf("want format=text, got %q", format)
		}
		if content != mockAnswer {
			t.Fatalf("want content=%q, got %q", mockAnswer, content)
		}
	})

	// Sub-test 2: outputFormat=json + non-JSON response → error.
	t.Run("json outputFormat with non-JSON model response returns error", func(t *testing.T) {
		customTodoID := newID()
		_, err = pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,'plan-x','custom:translate','running',ARRAY[$3]::text[],$4)`,
			customTodoID, projID, scriptTodoID, buildInput("json"))
		if err != nil {
			t.Fatalf("seed custom todo: %v", err)
		}

		_, err := w.runCustom(ctx, claimed{
			todoID:    customTodoID,
			projectID: projID,
			typ:       "custom:translate",
			attempts:  1,
			input:     buildInput("json"),
		})
		if err == nil {
			t.Fatalf("expected error for non-JSON model response, got nil")
		}
		if !strings.Contains(err.Error(), "JSON") {
			t.Fatalf("expected JSON-related error, got: %v", err)
		}
	})

	// Sub-test 3: unbound variable (sourceTodoId="") must not crash.
	t.Run("unbound variable (empty sourceTodoId) does not crash", func(t *testing.T) {
		inUnbound := map[string]any{
			"kind": "llm",
			"params": map[string]any{
				"systemPrompt": "You are a helper.",
				"userPrompt":   "Process: {{x}} and {{y}}",
				"outputFormat": "text",
				"variables": []map[string]any{
					{"name": "x", "sourceTodoId": ""},  // unbound
					{"name": "y", "sourceTodoId": ""},  // unbound
				},
			},
		}
		inputBytes, _ := json.Marshal(inUnbound)

		customTodoID := newID()
		_, err = pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status, input_json)
			 VALUES ($1,$2,'plan-x','custom:helper','running',$3)`,
			customTodoID, projID, inputBytes)
		if err != nil {
			t.Fatalf("seed unbound custom todo: %v", err)
		}

		// We need one more mock response for the unbound case.
		mockModel2 := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "unbound result"}))
		w2 := customTestWorker(t, mockModel2)

		outputRef, err := w2.runCustom(ctx, claimed{
			todoID:    customTodoID,
			projectID: projID,
			typ:       "custom:helper",
			attempts:  1,
			input:     inputBytes,
		})
		if err != nil {
			t.Fatalf("runCustom with unbound variables: %v", err)
		}
		if !strings.HasPrefix(outputRef, "custom:") {
			t.Fatalf("want outputRef to start with 'custom:', got %q", outputRef)
		}
	})
}

// ---- B2.3: http custom node tests ----

// fakeDoer is a scripted HTTPDoer. callCount lets tests assert "no request made";
// gotReq captures the last request so tests can assert what was sent on the wire.
type fakeDoer struct {
	resp      fetch.Response
	err       error
	callCount int
	gotReq    fetch.Request
}

func (f *fakeDoer) Do(ctx context.Context, in fetch.Request) (fetch.Response, error) {
	f.callCount++
	f.gotReq = in
	return f.resp, f.err
}

// fakeSecrets always resolves to a fixed value, recording the orgID it was asked
// for (used by TestRunCustomHTTP_OrgScopedSecret).
type fakeSecrets struct {
	value      string
	calledOrg  string
	calledName string
}

func (f *fakeSecrets) Resolve(ctx context.Context, orgID, name string) (string, error) {
	f.calledOrg = orgID
	f.calledName = name
	return f.value, nil
}

// httpTestWorker builds a Worker wired for the http path: real DB + Projects store,
// injected Secrets + HTTPFetcher. No Router needed (http doesn't call models).
func httpTestWorker(t *testing.T, db *gorm.DB, secrets SecretResolver, doer HTTPDoer) *Worker {
	t.Helper()
	return New(Config{
		DB:       db,
		Todos:    todos.New(db),
		Projects: project.New(db),
		Events:   events.New(db),
		Secrets:  secrets,
		HTTPFetcher: doer,
		WorkerID: "http-test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
}

// seedHTTPProjectTodo inserts a project (under orgID) + a typed http todo, returning
// projID + the claimed for that todo.
func seedHTTPProjectTodo(t *testing.T, db *gorm.DB, orgID string) (string, claimed) {
	t.Helper()
	ctx := context.Background()
	var projID string
	if err := db.WithContext(ctx).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID,
	).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	todoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, input_json)
		 VALUES ($1,$2,'plan-x','custom:http','running','{}')`,
		todoID, projID).Error; err != nil {
		t.Fatalf("seed http todo: %v", err)
	}
	return projID, claimed{todoID: todoID, projectID: projID, typ: "custom:http", attempts: 1}
}

// TestRunCustomHTTP_SecretNeverLeaks is the keystone security test: under every
// forced failure the resolved secret must NOT appear in the returned error or any
// node_outputs row, and the error must be one of the opaque httpError enum values.
func TestRunCustomHTTP_SecretNeverLeaks(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	const sentinel = "SUPER-SECRET-SENTINEL-9z8y7x"
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_http_" + randHex3()
	_, c := seedHTTPProjectTodo(t, db, orgID)

	secretHeaders := map[string]string{"Authorization": "Bearer {{secret:K}}"}

	failures := []struct {
		name   string
		doer   *fakeDoer
		params httpParams
	}{
		{"non-2xx", &fakeDoer{resp: fetch.Response{Status: 500, Body: []byte("err")}}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: secretHeaders}},
		{"dial-error", &fakeDoer{err: fmt.Errorf("fetch: do: dial https://api.example.com: connection refused")}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: secretHeaders}},
		{"timeout", &fakeDoer{err: context.DeadlineExceeded}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: secretHeaders}},
		{"body-cap", &fakeDoer{err: fmt.Errorf("fetch: body exceeds 1048576 byte cap")}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: secretHeaders}},
		{"json-parse", &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("not json")}}, httpParams{Method: "GET", URL: "https://api.example.com", OutputFormat: "json", AllowResponseBody: true, Headers: secretHeaders}},
	}
	for _, f := range failures {
		t.Run(f.name, func(t *testing.T) {
			w := httpTestWorker(t, db, &fakeSecrets{value: sentinel}, f.doer)
			ref, err := w.runCustomHTTP(ctx, c, f.params)
			if err == nil {
				t.Fatalf("expected failure for %s (ref=%q)", f.name, ref)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Fatalf("secret leaked into error: %q", err.Error())
			}
			// Returned error must be one of the opaque enum values.
			switch err.Error() {
			case "request_failed", "host_not_allowed", "timeout", "body_too_large", "blocked_destination", "bad_response_format":
			default:
				t.Fatalf("non-opaque error: %q", err.Error())
			}
			// 200 + 非声明格式（outputFormat=json 却非 JSON 正文）须单独报 bad_response_format，
			// 与传输故障（request_failed/timeout）区分。
			if f.name == "json-parse" && err.Error() != "bad_response_format" {
				t.Fatalf("json-parse: want bad_response_format, got %q", err.Error())
			}
			// No node_outputs row should carry the sentinel.
			var leaked int
			if err := db.WithContext(ctx).Raw(
				`SELECT count(*) FROM node_outputs WHERE content LIKE '%' || $1 || '%'`, sentinel).Row().Scan(&leaked); err != nil {
				t.Fatalf("count leaked: %v", err)
			}
			if leaked != 0 {
				t.Fatalf("secret leaked into node_outputs (%d rows)", leaked)
			}
		})
	}
}

// TestRunCustomHTTP_BodyPolicy verifies the secret-bearing body-suppression policy.
func TestRunCustomHTTP_BodyPolicy(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_http_" + randHex3()

	readOutput := func(ref string) (string, string) {
		outID := strings.TrimPrefix(ref, "custom:")
		var format, content string
		if err := db.WithContext(ctx).Raw(
			`SELECT format, content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&format, &content); err != nil {
			t.Fatalf("load node_output: %v", err)
		}
		return format, content
	}

	const body = `{"data":"value"}`

	// 1. secret-bearing + allowResponseBody:false → only {status}.
	t.Run("secret-bearing suppressed", func(t *testing.T) {
		_, c := seedHTTPProjectTodo(t, db, orgID)
		w := httpTestWorker(t, db, &fakeSecrets{value: "tok"}, &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte(body)}})
		ref, err := w.runCustomHTTP(ctx, c, httpParams{
			Method: "GET", URL: "https://api.example.com",
			Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"},
		})
		if err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		format, content := readOutput(ref)
		if format != "http-status" {
			t.Fatalf("want format=http-status, got %q", format)
		}
		if content != `{"status":200}` {
			t.Fatalf("want suppressed body, got %q", content)
		}
	})

	// 2. secret-bearing + allowResponseBody:true → body lands.
	t.Run("secret-bearing attested allows body", func(t *testing.T) {
		_, c := seedHTTPProjectTodo(t, db, orgID)
		w := httpTestWorker(t, db, &fakeSecrets{value: "tok"}, &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte(body)}})
		ref, err := w.runCustomHTTP(ctx, c, httpParams{
			Method: "GET", URL: "https://api.example.com", OutputFormat: "json", AllowResponseBody: true,
			Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"},
		})
		if err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		format, content := readOutput(ref)
		if format != "json" {
			t.Fatalf("want format=json, got %q", format)
		}
		if content != body {
			t.Fatalf("want body %q, got %q", body, content)
		}
	})

	// 3. non-secret request → body always lands.
	t.Run("non-secret body lands", func(t *testing.T) {
		_, c := seedHTTPProjectTodo(t, db, orgID)
		w := httpTestWorker(t, db, &fakeSecrets{value: "tok"}, &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("plain text")}})
		ref, err := w.runCustomHTTP(ctx, c, httpParams{
			Method: "GET", URL: "https://api.example.com",
			Headers: map[string]string{"Accept": "text/plain"},
		})
		if err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		format, content := readOutput(ref)
		if format != "text" {
			t.Fatalf("want format=text, got %q", format)
		}
		if content != "plain text" {
			t.Fatalf("want body, got %q", content)
		}
	})
}

// TestRunCustomHTTP_SecretForbiddenInBody verifies {{secret:NAME}} in the body
// template is rejected opaquely BEFORE any request is made.
func TestRunCustomHTTP_SecretForbiddenInBody(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_http_" + randHex3()
	_, c := seedHTTPProjectTodo(t, db, orgID)

	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	w := httpTestWorker(t, db, &fakeSecrets{value: "tok"}, doer)
	_, err := w.runCustomHTTP(ctx, c, httpParams{
		Method: "POST", URL: "https://api.example.com",
		BodyTemplate: `{"key":"{{secret:K}}"}`,
	})
	if err == nil {
		t.Fatalf("expected opaque error for secret-in-body")
	}
	if err.Error() != "request_failed" {
		t.Fatalf("want request_failed, got %q", err.Error())
	}
	if doer.callCount != 0 {
		t.Fatalf("expected NO request made, got callCount=%d", doer.callCount)
	}
}

// TestRunCustomHTTP_OrgScopedSecret proves secret resolution uses the project's
// trusted org (from OrgIDForProject), not anything from input_json.
func TestRunCustomHTTP_OrgScopedSecret(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_http_" + randHex3()
	_, c := seedHTTPProjectTodo(t, db, orgID)

	secrets := &fakeSecrets{value: "tok"}
	w := httpTestWorker(t, db, secrets, &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}})
	if _, err := w.runCustomHTTP(ctx, c, httpParams{
		Method: "GET", URL: "https://api.example.com",
		Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"},
	}); err != nil {
		t.Fatalf("runCustomHTTP: %v", err)
	}
	if secrets.calledOrg != orgID {
		t.Fatalf("secret resolved with org %q, want project's org %q", secrets.calledOrg, orgID)
	}
	if secrets.calledName != "K" {
		t.Fatalf("secret resolved with name %q, want K", secrets.calledName)
	}
}

// TestRunCustomHTTP_NameChannelCannotSmuggleSecret is the F1 regression test:
// an upstream node whose output text is the LITERAL string "{{secret:LEAKME}}"
// is bound to a header via the {{name}} channel (X-Test: {{up}}). Because the
// secret channel resolves on the author template FIRST, the {{name}} value is
// substituted AFTER the secret pass and its embedded {{secret:...}} stays literal.
// The org secret value must NEVER appear on the wire; the header must carry the
// literal "{{secret:LEAKME}}". This proves the editor-influenced {{name}} channel
// cannot smuggle an admin-only secret (defeating the editor→admin admin-gate).
//
// WITHOUT the reorder (name-channel-first), substituteVars would expand {{up}} to
// "{{secret:LEAKME}}" and the secret pass would then resolve it to the secret value
// and send it on the wire — this test would fail.
func TestRunCustomHTTP_NameChannelCannotSmuggleSecret(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	const secretValue = "supersecretvalue"
	const smuggle = "{{secret:LEAKME}}"
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_http_" + randHex3()
	projID, c := seedHTTPProjectTodo(t, db, orgID)

	// Seed an upstream custom node whose text output is the literal {{secret:LEAKME}}.
	upOutID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
		 VALUES ($1,$2,'t-up','custom:up',$3,'text')`,
		upOutID, projID, smuggle).Error; err != nil {
		t.Fatalf("seed upstream node_output: %v", err)
	}
	upTodoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:up','done',$3,'{}')`,
		upTodoID, projID, "custom:"+upOutID).Error; err != nil {
		t.Fatalf("seed upstream todo: %v", err)
	}
	// Wire the consumer's depends_on to the upstream (the expr value channel is
	// direct-deps gated; an unwired var would fail closed before the smuggle).
	if err := db.WithContext(ctx).Exec(
		`UPDATE todos SET depends_on=ARRAY[$1]::text[] WHERE id=$2`,
		upTodoID, c.todoID).Error; err != nil {
		t.Fatalf("wire consumer depends_on: %v", err)
	}

	// Org secret LEAKME resolves to the sentinel value. fakeSecrets records the
	// name it was asked for, so we can assert it was NEVER asked for LEAKME.
	secrets := &fakeSecrets{value: secretValue}
	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	w := httpTestWorker(t, db, secrets, doer)

	// Header binds {{up}} (a {{name}} binding, NOT an authored {{secret:}}).
	_, err := w.runCustomHTTP(ctx, c, httpParams{
		Method:  "GET",
		URL:     "https://api.example.com",
		Headers: map[string]string{"X-Test": "{{up}}"},
		Variables: []customVariable{
			{Name: "up", SourceTodoId: upTodoID},
		},
	})
	if err != nil {
		t.Fatalf("runCustomHTTP: %v", err)
	}

	// The received X-Test header must be the LITERAL {{secret:LEAKME}}, not resolved.
	got := doer.gotReq.Headers["X-Test"]
	if got != smuggle {
		t.Fatalf("X-Test header = %q, want literal %q (name channel must not smuggle a secret)", got, smuggle)
	}

	// The secret value must NOT appear anywhere in the outgoing request.
	for hk, hv := range doer.gotReq.Headers {
		if strings.Contains(hv, secretValue) {
			t.Fatalf("secret value leaked into header %q: %q", hk, hv)
		}
	}
	if strings.Contains(string(doer.gotReq.Body), secretValue) {
		t.Fatalf("secret value leaked into request body")
	}

	// The secret resolver must never have been asked for the smuggled name.
	if secrets.calledName == "LEAKME" {
		t.Fatalf("secret resolver was asked for LEAKME via the name channel — smuggle succeeded")
	}
}

func TestRevalidateCustomParamsRejectsDirty(t *testing.T) {
	cases := []struct {
		name string
		env  customEnvelope
	}{
		{"http url template", customEnvelope{Kind: "http", Params: json.RawMessage(`{"method":"GET","url":"http://x/{{y}}"}`)}},
		{"http body secret", customEnvelope{Kind: "http", Params: json.RawMessage(`{"method":"POST","url":"https://x","bodyTemplate":"{{secret:K}}"}`)}},
		{"script code secret", customEnvelope{Kind: "script", Params: json.RawMessage(`{"code":"x={{secret:K}}"}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := revalidateCustomParams(tc.env); err == nil {
				t.Fatalf("dirty %s params must be rejected at run-time", tc.name)
			}
		})
	}
}

func TestRevalidateCustomParamsAcceptsClean(t *testing.T) {
	clean := customEnvelope{Kind: "http", Params: json.RawMessage(`{"method":"GET","url":"https://api.example.com","outputFormat":"text"}`)}
	if err := revalidateCustomParams(clean); err != nil {
		t.Fatalf("clean params rejected: %v", err)
	}
}

// TestRunCustomHTTPDirtyURLNoOutbound (security, P-write-4) [DB]: a backfilled /
// directly-written dirty http envelope (templated url — bypasses save validation)
// must be rejected by the run-time gate BEFORE any outbound request — zero calls
// to the HTTPDoer, opaque error (no url leak).
func TestRunCustomHTTPDirtyURLNoOutbound(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_dirty_" + randHex3()
	_, c := seedHTTPProjectTodo(t, db, orgID)
	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte(`{}`)}}
	w := httpTestWorker(t, db, &fakeSecrets{value: "S"}, doer)

	c.input = json.RawMessage(`{"kind":"http","params":{"method":"GET","url":"http://attacker/{{x}}"}}`)
	_, err := w.runCustom(ctx, c)
	if err == nil {
		t.Fatal("dirty url must be rejected before dispatch")
	}
	if doer.callCount != 0 {
		t.Fatalf("no outbound request must be made, got %d", doer.callCount)
	}
	if strings.Contains(err.Error(), "attacker") {
		t.Fatalf("run-time error leaked dirty url: %v", err)
	}
}

// TestRunCustomScriptDirtySecretRejected (security, P-write-4) [DB]: a dirty
// script envelope carrying a {{secret:}} ref in code is rejected at the run-time
// gate (opaque errScriptFailed), proving the worker holds independently of save.
func TestRunCustomScriptDirtySecretRejected(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_dirty_s_" + randHex3()
	_, c := seedHTTPProjectTodo(t, db, orgID)
	doer := &fakeDoer{}
	w := httpTestWorker(t, db, &fakeSecrets{value: "S"}, doer)

	c.input = json.RawMessage(`{"kind":"script","params":{"code":"x = {{secret:K}}"}}`)
	_, err := w.runCustom(ctx, c)
	if err == nil {
		t.Fatal("dirty script secret must be rejected before dispatch")
	}
}

// TestIsPermanentFailure 锁定「不可重试」分类（fix B）：SSRF 拦截（blocked_destination）
// 与响应格式不符（bad_response_format）是确定性失败，须走终态分支；传输类失败（timeout /
// request_failed / body_too_large 等 5xx/网络抖动）仍走正常重试。纯函数，无需 DB。
func TestIsPermanentFailure(t *testing.T) {
	permanent := []error{errBlockedDest, errBadResponseFormat}
	for _, e := range permanent {
		if !isPermanentFailure(e) {
			t.Errorf("isPermanentFailure(%q) = false, want true", e)
		}
	}
	transient := []error{errRequestFailed, errTimeout, errBodyTooLarge, errScriptFailed, nil}
	for _, e := range transient {
		if isPermanentFailure(e) {
			t.Errorf("isPermanentFailure(%v) = true, want false (must keep retrying)", e)
		}
	}
}
