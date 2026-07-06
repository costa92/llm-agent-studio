package worker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"

	"github.com/costa92/llm-agent-studio/internal/fetch"
)

// seedTextDep seeds a text-format dep under projID: a node_outputs row
// (format='text', items=[{json:{text:<content>}}], id==coid AND todo_id==depTodo)
// + a done dep todo whose output_ref is "custom:<coid>". Returns the dep todo id.
// The expr channel resolves $node[depTodo].json.text to <content> (byte-equal to
// the stored node_outputs.content).
func seedTextDep(t *testing.T, w *Worker, projID, content string) string {
	t.Helper()
	ctx := context.Background()
	depTodo := newID()
	coid := newID()
	items := []byte(`[{"json":{"text":` + jsonQuote(content) + `}}]`)
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,'custom:llm',$4,'text',$5)`,
		coid, projID, depTodo, content, items).Error; err != nil {
		t.Fatalf("seed text dep node_output: %v", err)
	}
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}')`,
		depTodo, projID, "custom:"+coid).Error; err != nil {
		t.Fatalf("seed text dep todo: %v", err)
	}
	return depTodo
}

// jsonQuote is a tiny JSON string-quoter for the seed helper (avoids importing
// encoding/json just to wrap one literal).
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// seedConsumerTodo inserts a running consumer custom todo with the given
// depends_on set (a slice of dep todo ids). Returns its claimed.
func seedConsumerTodo(t *testing.T, w *Worker, projID, typ string, deps ...string) claimed {
	t.Helper()
	ctx := context.Background()
	id := newID()
	if len(deps) == 0 {
		if err := w.cfg.DB.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,'plan-x',$3,'running',ARRAY[]::text[],'{}')`,
			id, projID, typ).Error; err != nil {
			t.Fatalf("seed consumer todo: %v", err)
		}
	} else {
		var arr strings.Builder
		args := []any{id, projID, typ}
		arr.WriteString("ARRAY[")
		for i, d := range deps {
			if i > 0 {
				arr.WriteByte(',')
			}
			arr.WriteString("$")
			arr.WriteString(itoa(len(args) + 1))
			args = append(args, d)
		}
		arr.WriteString("]::text[]")
		sql := `INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,'plan-x',$3,'running',` + arr.String() + `,'{}')`
		if err := w.cfg.DB.WithContext(ctx).Exec(sql, args...).Error; err != nil {
			t.Fatalf("seed consumer todo: %v", err)
		}
	}
	return claimed{todoID: id, projectID: projID, typ: typ, attempts: 1}
}

// TestExprValueSource_LLM_ResolvesViaNode proves the {{name}} value source
// resolves a {{draft}} var via the live $node path (project-scoped, fail-closed)
// end-to-end: the run completes (the dep value resolved through the S-2 resolver
// without error) for a node whose var is a properly-wired dep. The scripted model
// returns a canned answer (it cannot observe the prompt), so the byte-exact
// interpolated-value assertion lives in the HTTP test (doer.gotReq).
func TestExprValueSource_LLM_ResolvesViaNode(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker expr value-source tests")
	}
	ctx := context.Background()

	const canned = "MODEL_ANSWER"
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: canned})))
	orgID := os.Getenv("_CUSTOM_TEST_ORG_ID")
	var projID string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	dep := seedTextDep(t, w, projID, "DRAFT_BODY")
	c := seedConsumerTodo(t, w, projID, "custom:translate", dep)
	ref, err := w.runCustomLLM(ctx, c, llmParams{
		UserPrompt: "Use: {{draft}}",
		Variables:  []customVariable{{Name: "draft", SourceTodoId: dep}},
	})
	if err != nil {
		t.Fatalf("runCustomLLM: %v", err)
	}
	outID := strings.TrimPrefix(ref, "custom:")
	var content string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&content); err != nil {
		t.Fatalf("load node_output: %v", err)
	}
	if content != canned {
		t.Fatalf("node_outputs.content = %q, want %q", content, canned)
	}
}

// TestExprValueSource_S2_FailClosed is the core security test: a var pointing at
// a todo NOT in the consumer's depends_on, or in a different project, must make
// the run FAIL with an opaque error and surface no cross-data.
func TestExprValueSource_S2_FailClosed(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker expr value-source tests")
	}
	ctx := context.Background()

	// (a) out-of-deps: a real text dep exists but is NOT in the consumer's
	// depends_on. The expr resolver (exprNodeResolver) denies it → resolve errors →
	// the executor returns its opaque error.
	t.Run("out-of-deps LLM fails closed", func(t *testing.T) {
		echo := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "SHOULD_NOT_RUN"}))
		w := customTestWorker(t, echo)
		orgID := os.Getenv("_CUSTOM_TEST_ORG_ID")
		var projID string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
			orgID).Row().Scan(&projID); err != nil {
			t.Fatalf("seed project: %v", err)
		}
		dep := seedTextDep(t, w, projID, "OUT_OF_DEPS_SECRET")
		// Consumer depends on NOTHING (dep is out-of-set).
		c := seedConsumerTodo(t, w, projID, "custom:translate")
		_, err := w.runCustomLLM(ctx, c, llmParams{
			UserPrompt: "Use: {{x}}",
			Variables:  []customVariable{{Name: "x", SourceTodoId: dep}},
		})
		if err == nil {
			t.Fatalf("expected fail-closed error for out-of-deps var, got nil")
		}
		if strings.Contains(err.Error(), "OUT_OF_DEPS_SECRET") {
			t.Fatalf("dep content leaked into error: %q", err.Error())
		}
	})

	// (b) cross-project: a var points at a todo in project B (with recognizable
	// seeded content). The consumer in project A lists it in depends_on, but
	// exprNodeResolver project-scopes the read to project A → the forged id reads
	// zero rows → fail-closed. Project-B content must NOT surface.
	t.Run("cross-project HTTP fails closed opaquely", func(t *testing.T) {
		db := assetTestGorm(t)
		secrets := &fakeSecrets{value: "tok"}
		doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
		w := httpTestWorker(t, db, secrets, doer)

		// Project B with recognizable content.
		var projB string
		if err := db.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'orgB','pB','u') RETURNING id`,
		).Row().Scan(&projB); err != nil {
			t.Fatalf("seed project B: %v", err)
		}
		depB := seedTextDep(t, w, projB, "PROJECT_B_SECRET")

		// Project A consumer that (forged) lists depB in its depends_on.
		orgA := "org_http_" + randHex3()
		var projA string
		if err := db.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'pA','u') RETURNING id`,
			orgA).Row().Scan(&projA); err != nil {
			t.Fatalf("seed project A: %v", err)
		}
		c := seedConsumerTodo(t, w, projA, "custom:http", depB)

		ref, err := w.runCustomHTTP(ctx, c, httpParams{
			Method:    "GET",
			URL:       "https://api.example.com",
			Headers:   map[string]string{"X-Up": "{{up}}"},
			Variables: []customVariable{{Name: "up", SourceTodoId: depB}},
		})
		if err == nil {
			t.Fatalf("expected fail-closed error for cross-project var, got ref=%q", ref)
		}
		// HTTP maps any resolve error to the opaque errRequestFailed.
		if err.Error() != "request_failed" {
			t.Fatalf("want opaque request_failed, got %q", err.Error())
		}
		if strings.Contains(err.Error(), "PROJECT_B_SECRET") {
			t.Fatalf("project-B content leaked into error: %q", err.Error())
		}
		// No request should have carried project-B content.
		for hk, hv := range doer.gotReq.Headers {
			if strings.Contains(hv, "PROJECT_B_SECRET") {
				t.Fatalf("project-B content leaked into header %q: %q", hk, hv)
			}
		}
	})

	// (c) out-of-deps for script: opaque errScriptFailed.
	t.Run("out-of-deps script fails closed opaquely", func(t *testing.T) {
		db := assetTestGorm(t)
		w := scriptTestWorker(t, db)
		var projID string
		if err := db.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'orgS','pS','u') RETURNING id`,
		).Row().Scan(&projID); err != nil {
			t.Fatalf("seed project: %v", err)
		}
		dep := seedTextDep(t, w, projID, "SCRIPT_OUT_SECRET")
		c := seedConsumerTodo(t, w, projID, "custom:script") // no deps
		_, err := w.runCustomScript(ctx, c, scriptParams{
			Code:      `def main(inputs):\n    return ""`,
			Variables: []customVariable{{Name: "x", SourceTodoId: dep}},
		})
		if err == nil {
			t.Fatalf("expected fail-closed error for out-of-deps script var, got nil")
		}
		if err.Error() != "script_failed" {
			t.Fatalf("want opaque script_failed, got %q", err.Error())
		}
		if strings.Contains(err.Error(), "SCRIPT_OUT_SECRET") {
			t.Fatalf("dep content leaked into error: %q", err.Error())
		}
	})
}

// TestExprValueSource_HTTP_SecretSurvivesAndGuards proves the untouched secret
// pre-pass, the {{name}} expr channel, and the {status} body-suppression guard
// all work together on the authoritative channel.
func TestExprValueSource_HTTP_SecretSurvivesAndGuards(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker expr value-source tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)

	// (a) secret pre-pass still resolves + (b) {{up}} resolves via expr.
	t.Run("secret prepass + expr name channel", func(t *testing.T) {
		secrets := &fakeSecrets{value: "RESOLVED_SECRET"}
		doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
		w := httpTestWorker(t, db, secrets, doer)

		orgA := "org_http_" + randHex3()
		var projA string
		if err := db.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'pA','u') RETURNING id`,
			orgA).Row().Scan(&projA); err != nil {
			t.Fatalf("seed project A: %v", err)
		}
		dep := seedTextDep(t, w, projA, "UPSTREAM_VAL")
		c := seedConsumerTodo(t, w, projA, "custom:http", dep)

		// AllowResponseBody:true so the body lands (proves the run completed).
		ref, err := w.runCustomHTTP(ctx, c, httpParams{
			Method:            "GET",
			URL:               "https://api.example.com",
			OutputFormat:      "text",
			AllowResponseBody: true,
			Headers: map[string]string{
				"Authorization": "Bearer {{secret:K}}",
				"X-Up":          "{{up}}",
			},
			Variables: []customVariable{{Name: "up", SourceTodoId: dep}},
		})
		if err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		_ = ref
		// (a) secret pre-pass ran (spy called for K) and resolved on the wire.
		if secrets.calledName != "K" {
			t.Fatalf("secret pre-pass not invoked for K, calledName=%q", secrets.calledName)
		}
		if got := doer.gotReq.Headers["Authorization"]; got != "Bearer RESOLVED_SECRET" {
			t.Fatalf("secret not resolved on wire, Authorization=%q", got)
		}
		// (b) {{up}} resolved via expr to the dep's text value.
		if got := doer.gotReq.Headers["X-Up"]; got != "UPSTREAM_VAL" {
			t.Fatalf("expr name channel did not resolve {{up}}, X-Up=%q", got)
		}
	})

	// (c) a {{secret:X}} arriving THROUGH the $node upstream value stays literal:
	// the expr name channel substitutes AFTER the secret pre-pass, so the embedded
	// {{secret:LEAKME}} is never resolved.
	t.Run("secret smuggled via node value stays literal", func(t *testing.T) {
		const smuggle = "{{secret:LEAKME}}"
		secrets := &fakeSecrets{value: "LEAKED"}
		doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
		w := httpTestWorker(t, db, secrets, doer)

		orgA := "org_http_" + randHex3()
		var projA string
		if err := db.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'pA','u') RETURNING id`,
			orgA).Row().Scan(&projA); err != nil {
			t.Fatalf("seed project A: %v", err)
		}
		dep := seedTextDep(t, w, projA, smuggle)
		c := seedConsumerTodo(t, w, projA, "custom:http", dep)

		if _, err := w.runCustomHTTP(ctx, c, httpParams{
			Method:    "GET",
			URL:       "https://api.example.com",
			Headers:   map[string]string{"X-Test": "{{up}}"},
			Variables: []customVariable{{Name: "up", SourceTodoId: dep}},
		}); err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		if got := doer.gotReq.Headers["X-Test"]; got != smuggle {
			t.Fatalf("X-Test = %q, want literal %q (node value must not smuggle a secret)", got, smuggle)
		}
		for hk, hv := range doer.gotReq.Headers {
			if strings.Contains(hv, "LEAKED") {
				t.Fatalf("secret value leaked into header %q: %q", hk, hv)
			}
		}
		if secrets.calledName == "LEAKME" {
			t.Fatalf("secret resolver asked for LEAKME via the node value — smuggle succeeded")
		}
	})

	// (d) secret-bearing + AllowResponseBody:false still stores {status}-only.
	t.Run("status-only guard", func(t *testing.T) {
		secrets := &fakeSecrets{value: "tok"}
		doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte(`{"data":"x"}`)}}
		w := httpTestWorker(t, db, secrets, doer)

		orgA := "org_http_" + randHex3()
		var projA string
		if err := db.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'pA','u') RETURNING id`,
			orgA).Row().Scan(&projA); err != nil {
			t.Fatalf("seed project A: %v", err)
		}
		dep := seedTextDep(t, w, projA, "UP")
		c := seedConsumerTodo(t, w, projA, "custom:http", dep)

		ref, err := w.runCustomHTTP(ctx, c, httpParams{
			Method:    "GET",
			URL:       "https://api.example.com",
			Headers:   map[string]string{"Authorization": "Bearer {{secret:K}}", "X-Up": "{{up}}"},
			Variables: []customVariable{{Name: "up", SourceTodoId: dep}},
		})
		if err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		outID := strings.TrimPrefix(ref, "custom:")
		var format, content string
		if err := db.WithContext(ctx).Raw(
			`SELECT format, content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&format, &content); err != nil {
			t.Fatalf("load node_output: %v", err)
		}
		if format != "http-status" {
			t.Fatalf("want format=http-status (secret-bearing suppressed), got %q", format)
		}
		if content != `{"status":200}` {
			t.Fatalf("want {status}-only, got %q", content)
		}
	})
}
