package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"gorm.io/gorm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/modelrouter"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// ---- shared fakes/helpers for {{input:}} pass tests ----

// inputSecretsSpy records the SET of secret names Resolve was asked for (not just
// a count), so a test can assert "only REAL was resolved, never STOLEN". Unknown
// names error (mirrors a missing org secret).
type inputSecretsSpy struct {
	mu     sync.Mutex
	values map[string]string
	calls  []string
}

func (r *inputSecretsSpy) Resolve(_ context.Context, _ string, name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
	v, ok := r.values[name]
	if !ok {
		return "", fmt.Errorf("no such secret")
	}
	return v, nil
}

func (r *inputSecretsSpy) resolvedSet() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := map[string]bool{}
	for _, n := range r.calls {
		s[n] = true
	}
	return s
}

// recordingChat captures every Generate request so the LLM-channel test can inspect
// the exact system/user text that reached the model.
type recordingChat struct {
	mu     sync.Mutex
	answer string
	reqs   []llm.Request
}

func (c *recordingChat) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, req)
	return llm.Response{Text: c.answer}, nil
}

func (c *recordingChat) Stream(_ context.Context, _ llm.Request) (llm.StreamReader, error) {
	return nil, fmt.Errorf("recordingChat: Stream not used")
}

func (c *recordingChat) Info() llm.ProviderInfo {
	return llm.ProviderInfo{Provider: "rec", Model: "rec"}
}

// lastSystem / lastUser return the system prompt and first user message from the
// most recent Generate call.
func (c *recordingChat) lastSystem() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		return ""
	}
	return c.reqs[len(c.reqs)-1].SystemPrompt
}

func (c *recordingChat) lastUser() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		return ""
	}
	r := c.reqs[len(c.reqs)-1]
	for _, m := range r.Messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
}

// variableRunInputs builds a plans.run_inputs JSON snapshot ({values, schema}) that
// declares each name as a target=="variable" text field with the given value.
func variableRunInputs(vars map[string]string) string {
	type field struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Target string `json:"target"`
	}
	values := map[string]json.RawMessage{}
	schema := []field{}
	for k, v := range vars {
		b, _ := json.Marshal(v)
		values[k] = b
		schema = append(schema, field{Name: k, Type: "text", Target: "variable"})
	}
	out, _ := json.Marshal(struct {
		Values map[string]json.RawMessage `json:"values"`
		Schema []field                    `json:"schema"`
	}{values, schema})
	return string(out)
}

// seedInputsTodo inserts a project + a plans row carrying run_inputs + a todo whose
// plan_id points at that plan, returning the projID and a claimed for the todo.
func seedInputsTodo(t *testing.T, db *gorm.DB, orgID, todoType, runInputs string) (string, claimed) {
	t.Helper()
	ctx := context.Background()
	var projID string
	if err := db.WithContext(ctx).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	planID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO plans (id, project_id, status, valid, run_inputs) VALUES ($1,$2,'created',true,$3)`,
		planID, projID, []byte(runInputs)).Error; err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	todoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, input_json) VALUES ($1,$2,$3,$4,'running','{}')`,
		todoID, projID, planID, todoType).Error; err != nil {
		t.Fatalf("seed todo: %v", err)
	}
	return projID, claimed{todoID: todoID, projectID: projID, typ: todoType, attempts: 1}
}

// llmInputsTestWorker builds an LLM-capable worker (real Router → recordingChat) and
// returns it plus the orgID its model_config is registered under. Mirrors
// customTestWorker but exposes the org so callers can seed a matching project+plan.
func llmInputsTestWorker(t *testing.T, chat llm.ChatModel) (*Worker, string) {
	t.Helper()
	db := assetTestGorm(t)
	box := testBox(t)
	ms := models.New(db, box)
	ctx := context.Background()
	orgID := "org_in_llm_" + randHex3()
	if _, err := ms.Create(ctx, models.CreateInput{
		OrgID: orgID, Kind: "text", Provider: "openai-compatible", Model: "x",
		Enabled: true, IsDefault: true, APIKey: "sk-test",
	}); err != nil {
		t.Fatalf("create text config: %v", err)
	}
	reg := generate.NewRegistry()
	router := modelrouter.New(modelrouter.Config{
		Models:   ms,
		Registry: reg,
		BuildChat: func(_, _, _, _ string) (llm.ChatModel, error) { return chat, nil },
	})
	w := New(Config{
		DB:       db,
		Todos:    todos.New(db),
		Projects: project.New(db),
		Events:   events.New(db),
		Script:   studioagents.NewScriptAgent(llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{}`}))),
		Cost:     cost.New(db),
		Models:   ms,
		Registry: reg,
		Router:   router,
		WorkerID: "in-llm-test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	return w, orgID
}

// laziness needles: the four sibling-channel syntaxes that must survive an input
// VALUE verbatim (never re-evaluated after the input pass).
var lazinessPayload = `{{secret:X}} {{upVar}} {{ $node["y"].json }} {{input:self}}`

func lazinessNeedles() []string {
	return []string{`{{secret:X}}`, `{{upVar}}`, `{{ $node["y"].json }}`, `{{input:self}}`}
}

func assertAllLiteral(t *testing.T, where, got string) {
	t.Helper()
	for _, n := range lazinessNeedles() {
		if !strings.Contains(got, n) {
			t.Fatalf("%s: missing literal %q in %q (channel re-evaluated an input value)", where, n, got)
		}
	}
}

// ---- Test #1a: HTTP mixed secret — input cannot steal a secret ----

func TestRunCustomHTTP_InputCannotStealSecret(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_in_" + randHex3()
	// input value smuggles a {{secret:STOLEN}} ref.
	runInputs := variableRunInputs(map[string]string{"tok": "{{secret:STOLEN}}"})
	_, c := seedInputsTodo(t, db, orgID, "custom:http", runInputs)

	secrets := &inputSecretsSpy{values: map[string]string{"REAL": "REALTOKEN"}}
	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	w := httpTestWorker(t, db, secrets, doer)

	if _, err := w.runCustomHTTP(ctx, c, httpParams{
		Method:  "GET",
		URL:     "https://api.example.com",
		Headers: map[string]string{"Authorization": "Bearer {{secret:REAL}} {{input:tok}}"},
	}); err != nil {
		t.Fatalf("runCustomHTTP: %v", err)
	}

	got := doer.gotReq.Headers["Authorization"]
	// The author's real secret resolved.
	if !strings.Contains(got, "REALTOKEN") {
		t.Fatalf("author {{secret:REAL}} not resolved, header=%q", got)
	}
	// The smuggled secret ref stays literal — never resolved.
	if !strings.Contains(got, "{{secret:STOLEN}}") {
		t.Fatalf("smuggled secret should be literal, header=%q", got)
	}
	// Resolve call SET must be exactly {REAL} (not a count==0, not {REAL,STOLEN}).
	set := secrets.resolvedSet()
	if len(set) != 1 || !set["REAL"] {
		t.Fatalf("Resolve set must be exactly {REAL}, got %v", secrets.calls)
	}
}

// ---- Test #1b: body residue reorder regression ----

func TestRunCustomHTTP_InputBody_ResidueGuardReorder(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_in_" + randHex3()

	// Part A: AUTHOR writes {{secret:}} in body → still rejected, no request.
	t.Run("author secret in body still rejected", func(t *testing.T) {
		_, c := seedInputsTodo(t, db, orgID, "custom:http", `{}`)
		doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
		w := httpTestWorker(t, db, &inputSecretsSpy{values: map[string]string{}}, doer)
		_, err := w.runCustomHTTP(ctx, c, httpParams{
			Method: "POST", URL: "https://api.example.com",
			BodyTemplate: `{"k":"{{secret:K}}"}`,
		})
		if err == nil || err.Error() != "request_failed" {
			t.Fatalf("author {{secret:}} in body must be request_failed, got %v", err)
		}
		if doer.callCount != 0 {
			t.Fatalf("no request must be made, got callCount=%d", doer.callCount)
		}
	})

	// Part B: input-injected {{secret:X}} into body → request sent, literal, not resolved.
	t.Run("input-injected secret in body is literal and sent", func(t *testing.T) {
		runInputs := variableRunInputs(map[string]string{"payload": "{{secret:STOLEN}}"})
		_, c := seedInputsTodo(t, db, orgID, "custom:http", runInputs)
		secrets := &inputSecretsSpy{values: map[string]string{}}
		doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
		w := httpTestWorker(t, db, secrets, doer)
		if _, err := w.runCustomHTTP(ctx, c, httpParams{
			Method: "POST", URL: "https://api.example.com", AllowResponseBody: true,
			BodyTemplate: `{"k":"{{input:payload}}"}`,
		}); err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		if doer.callCount != 1 {
			t.Fatalf("request must be sent, got callCount=%d", doer.callCount)
		}
		if !strings.Contains(string(doer.gotReq.Body), "{{secret:STOLEN}}") {
			t.Fatalf("body should carry literal smuggled secret, got %q", string(doer.gotReq.Body))
		}
		if len(secrets.resolvedSet()) != 0 {
			t.Fatalf("no secret should be resolved, got %v", secrets.calls)
		}
	})
}

// ---- Test #1c: full laziness across all four channels ----

func TestInputPass_FullLaziness_HTTP(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_in_" + randHex3()
	runInputs := variableRunInputs(map[string]string{"self": lazinessPayload})
	_, c := seedInputsTodo(t, db, orgID, "custom:http", runInputs)

	secrets := &inputSecretsSpy{values: map[string]string{}}
	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	w := httpTestWorker(t, db, secrets, doer)

	if _, err := w.runCustomHTTP(ctx, c, httpParams{
		Method: "POST", URL: "https://api.example.com", AllowResponseBody: true,
		Headers:      map[string]string{"X-Probe": "H={{input:self}}"},
		BodyTemplate: `B={{input:self}}`,
	}); err != nil {
		t.Fatalf("runCustomHTTP: %v", err)
	}
	assertAllLiteral(t, "http header", doer.gotReq.Headers["X-Probe"])
	assertAllLiteral(t, "http body", string(doer.gotReq.Body))
	if len(secrets.resolvedSet()) != 0 {
		t.Fatalf("no secret should be resolved across channels, got %v", secrets.calls)
	}
}

func TestInputPass_FullLaziness_LLM(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	rec := &recordingChat{answer: "done"}
	w, orgID := llmInputsTestWorker(t, rec)
	runInputs := variableRunInputs(map[string]string{"self": lazinessPayload})
	_, c := seedInputsTodo(t, w.cfg.DB, orgID, "custom:llm", runInputs)

	if _, err := w.runCustomLLM(ctx, c, llmParams{
		SystemPrompt: "S={{input:self}}",
		UserPrompt:   "U={{input:self}}",
		OutputFormat: "text",
	}); err != nil {
		t.Fatalf("runCustomLLM: %v", err)
	}
	assertAllLiteral(t, "llm system", rec.lastSystem())
	assertAllLiteral(t, "llm user", rec.lastUser())
}

// ---- Test #1d: script code injection — value is a read-only global, never code ----

func TestRunCustomScript_InputIsDataGlobalNotCode(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_in_" + randHex3()

	// The input value is a Starlark snippet. If it were spliced into source it would
	// either execute or fail-to-parse; as a data global it is inert and echoes back.
	snippet := `output = "HIJACKED"`
	runInputs := variableRunInputs(map[string]string{"payload": snippet})
	_, c := seedInputsTodo(t, db, orgID, "custom:script", runInputs)

	w := httpTestWorker(t, db, &inputSecretsSpy{values: map[string]string{}}, &fakeDoer{})
	out, err := w.runCustomScript(ctx, c, scriptParams{
		Code:         `output = payload`, // reads input as a predeclared global
		OutputFormat: "text",
	})
	if err != nil {
		t.Fatalf("runCustomScript: %v", err)
	}
	outID := strings.TrimPrefix(out, "custom:")
	var content string
	if err := db.WithContext(ctx).Raw(`SELECT content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&content); err != nil {
		t.Fatalf("load node_output: %v", err)
	}
	if content != snippet {
		t.Fatalf("input value must be an inert data global echoed verbatim, got %q want %q", content, snippet)
	}
}

// ---- Test: script name-variable wins over input on a name collision ----

func TestRunCustomScript_NameVariableWinsCollision(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_in_" + randHex3()
	runInputs := variableRunInputs(map[string]string{"x": "FROM_INPUT"})
	projID, c := seedInputsTodo(t, db, orgID, "custom:script", runInputs)

	// Upstream node output bound to the name variable "x".
	upOutID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
		 VALUES ($1,$2,'t-up','custom:up','FROM_NAME','text')`,
		upOutID, projID).Error; err != nil {
		t.Fatalf("seed upstream node_output: %v", err)
	}
	upTodoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:up','done',$3,'{}')`,
		upTodoID, projID, "custom:"+upOutID).Error; err != nil {
		t.Fatalf("seed upstream todo: %v", err)
	}

	w := httpTestWorker(t, db, &inputSecretsSpy{values: map[string]string{}}, &fakeDoer{})
	out, err := w.runCustomScript(ctx, c, scriptParams{
		Code:         `output = x`,
		OutputFormat: "text",
		Variables:    []customVariable{{Name: "x", SourceTodoId: upTodoID}},
	})
	if err != nil {
		t.Fatalf("runCustomScript: %v", err)
	}
	outID := strings.TrimPrefix(out, "custom:")
	var content string
	if err := db.WithContext(ctx).Raw(`SELECT content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&content); err != nil {
		t.Fatalf("load node_output: %v", err)
	}
	if content != "FROM_NAME" {
		t.Fatalf("name variable must win on collision, got %q", content)
	}
}

// ---- Test: CRLF in an input value cannot split an HTTP header ----

// TestRunCustomHTTP_InputCRLFNoHeaderSplit contractualizes a previously-implicit
// defense: an input text value carrying CRLF + a forged header line, injected into a
// header VALUE, must NOT produce a split-out header. The worker substitutes into a
// single map[string]string value, so the forged line stays embedded in the original
// header's value (no new key); the real fetch transport's stdlib header writer would
// additionally reject the illegal value at send time (→ request_failed). Either
// outcome is acceptable — both deny header injection.
func TestRunCustomHTTP_InputCRLFNoHeaderSplit(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_in_" + randHex3()
	runInputs := variableRunInputs(map[string]string{"tok": "line1\r\nX-Injected: evil"})
	_, c := seedInputsTodo(t, db, orgID, "custom:http", runInputs)

	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	w := httpTestWorker(t, db, &inputSecretsSpy{values: map[string]string{}}, doer)

	_, err := w.runCustomHTTP(ctx, c, httpParams{
		Method:  "GET",
		URL:     "https://api.example.com",
		Headers: map[string]string{"Authorization": "Bearer {{input:tok}}"},
	})
	if err != nil {
		// A real transport rejected the illegal header value before sending — also fine.
		if err.Error() != "request_failed" {
			t.Fatalf("want request_failed if the CRLF value is rejected, got %v", err)
		}
		if doer.callCount != 0 {
			t.Fatalf("a rejected request must not reach the doer, got callCount=%d", doer.callCount)
		}
		return
	}
	// No forged header split out of the CRLF value.
	if _, ok := doer.gotReq.Headers["X-Injected"]; ok {
		t.Fatalf("CRLF in input value forged a split header X-Injected=%q", doer.gotReq.Headers["X-Injected"])
	}
	// The forged line stays confined inside the Authorization value (literal text).
	if !strings.Contains(doer.gotReq.Headers["Authorization"], "X-Injected: evil") {
		t.Fatalf("forged line must stay embedded in the Authorization value, got %q", doer.gotReq.Headers["Authorization"])
	}
}

// ---- Test #5: undeclared {{input:foo}} → empty string, no error ----

func TestInputPass_UndeclaredResolvesToEmpty(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker input tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_in_" + randHex3()
	// run_inputs declares nothing.
	_, c := seedInputsTodo(t, db, orgID, "custom:http", `{}`)

	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	w := httpTestWorker(t, db, &inputSecretsSpy{values: map[string]string{}}, doer)
	if _, err := w.runCustomHTTP(ctx, c, httpParams{
		Method:  "GET",
		URL:     "https://api.example.com",
		Headers: map[string]string{"X-E": "a{{input:nope}}b"},
	}); err != nil {
		t.Fatalf("runCustomHTTP: %v", err)
	}
	if got := doer.gotReq.Headers["X-E"]; got != "ab" {
		t.Fatalf("undeclared {{input:}} must resolve to empty, header=%q want %q", got, "ab")
	}
}
