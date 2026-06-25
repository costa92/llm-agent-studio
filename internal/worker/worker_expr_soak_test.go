package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// TestExprChannel_DifferentialSoak is the reproducible local proxy for the
// production soak that gates the ExprChannel flip (workflow-v2 P3 cut-over).
//
// For each realistic custom-node scenario it runs the SAME execution twice — once
// under ExprChannel:false (legacy resolveVariables/resolveOutputText) and once under
// ExprChannel:true (the expr engine $node path) — against freshly-seeded, identical
// data, and asserts the observable output is EQUIVALENT. "Equivalent" is the soak's
// acceptance bar:
//   - text-format deps: BYTE-IDENTICAL across flags.
//   - json-object deps: SEMANTICALLY EQUAL (json.Unmarshal both + reflect.DeepEqual) —
//     the engine decodes→re-marshals so key order / whitespace may differ; nothing
//     else may.
//
// Anything outside that envelope is a real divergence that must block the flip; the
// assertions below do NOT tolerate it.
//
// The probe half re-runs the same battery under ExprParity:true with a captured slog
// buffer and asserts EVERY "$node shadow probe" log line is class=exact or
// class=benign — zero class=divergent — and that no resolved value or var name leaks
// into the buffer.
func TestExprChannel_DifferentialSoak(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the P3 differential soak")
	}

	t.Run("HTTP", func(t *testing.T) { soakHTTP(t) })
	t.Run("LLM", func(t *testing.T) { soakLLM(t) })
	t.Run("Script", func(t *testing.T) { soakScript(t) })
	t.Run("ShadowProbe", func(t *testing.T) { soakShadowProbe(t) })
}

// ---- seed helpers (soak-local; recognizable content) -----------------------

// seedSoakProject inserts a bare project under a unique org and returns its id.
func seedSoakProject(t *testing.T, db *gorm.DB, org string) string {
	t.Helper()
	var projID string
	if err := db.WithContext(context.Background()).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		org).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// seedJSONDep seeds a json-object dep: a node_outputs row (format='json',
// content=<obj string>, items=[{json:<obj>}], id==coid AND todo_id==depTodo) + a
// done dep todo whose output_ref is "custom:<coid>". Legacy resolves the content
// string; expr resolves $node[depTodo].json (re-marshaled object) → semantically
// equal but possibly key-reordered. Returns the dep todo id.
func seedJSONDep(t *testing.T, db *gorm.DB, projID, objJSON string) string {
	t.Helper()
	ctx := context.Background()
	depTodo := newID()
	coid := newID()
	items := []byte(`[{"json":` + objJSON + `}]`)
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,'custom:llm',$4,'json',$5)`,
		coid, projID, depTodo, objJSON, items).Error; err != nil {
		t.Fatalf("seed json dep node_output: %v", err)
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}')`,
		depTodo, projID, "custom:"+coid).Error; err != nil {
		t.Fatalf("seed json dep todo: %v", err)
	}
	return depTodo
}

// seedScriptFallbackDep seeds a STRADDLING dep that completed under old code: a
// scripts row (output_ref 'script:<id>') with NO node_outputs.items row. The expr
// side must satisfy it via itemsForDep's script-projection fallback + the
// exprNodeAccessor output_ref-prefix inference (.json). Legacy resolves the
// JSONB-normalized content_json text. Returns the dep todo id.
func seedScriptFallbackDep(t *testing.T, db *gorm.DB, projID, contentJSON string) string {
	t.Helper()
	ctx := context.Background()
	scriptID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
		scriptID, projID, newID(), []byte(contentJSON)).Error; err != nil {
		t.Fatalf("seed script fallback row: %v", err)
	}
	depTodo := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','script','done',$3,'{}')`,
		depTodo, projID, "script:"+scriptID).Error; err != nil {
		t.Fatalf("seed script fallback todo: %v", err)
	}
	return depTodo
}

// seedHTTPConsumer inserts a running custom:http todo whose depends_on is exactly
// deps (required so the ExprChannel S-2 direct-deps gate admits each dep). Returns
// the claimed.
func seedHTTPConsumer(t *testing.T, db *gorm.DB, projID string, deps ...string) claimed {
	t.Helper()
	w := &Worker{cfg: Config{DB: db}}
	return seedConsumerTodo(t, w, projID, "custom:http", deps...)
}

// httpSoakWorker builds an http-path worker over db with the given fake fetcher.
func httpSoakWorker(t *testing.T, db *gorm.DB, doer HTTPDoer, exprChannel bool) *Worker {
	t.Helper()
	w := New(Config{
		DB:          db,
		Todos:       todos.New(db),
		Projects:    project.New(db),
		Events:      events.New(db),
		Secrets:     &fakeSecrets{value: "tok"},
		HTTPFetcher: doer,
		WorkerID:    "soak-http", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	w.cfg.ExprChannel = exprChannel
	return w
}

// assertJSONSemEqual asserts a and b decode to equal JSON values (benign reorder /
// whitespace allowed; nothing else).
func assertJSONSemEqual(t *testing.T, label, a, b string) {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		t.Fatalf("%s: legacy/expr value not valid JSON (a=%q): %v", label, a, err)
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		t.Fatalf("%s: legacy/expr value not valid JSON (b=%q): %v", label, b, err)
	}
	if !reflect.DeepEqual(av, bv) {
		t.Fatalf("%s: JSON deps not semantically equal:\n legacy=%q\n expr  =%q", label, a, b)
	}
}

// ---- HTTP soak (strongest — byte differential on the wire) ------------------

func soakHTTP(t *testing.T) {
	ctx := context.Background()
	db := assetTestGorm(t)

	// Recognizable seed content per format.
	const textVal = "UPSTREAM-TEXT-VALUE-soak"
	const textVal2 = "ANOTHER-TEXT-VALUE-soak"
	const jsonObj = `{"b":2,"a":1,"nested":{"z":9,"y":8}}`

	// runScenario seeds fresh, identical data, then runs the SAME http request under
	// both flags via two workers sharing the db, capturing the outgoing request for
	// each. It returns (legacyReq, exprReq).
	type capture struct{ req fetch.Request }
	runBoth := func(t *testing.T, seed func(projID string) (deps []string), params func(deps []string) httpParams) (fetch.Request, fetch.Request) {
		t.Helper()
		org := "org_soak_http_" + randHex3()
		projID := seedSoakProject(t, db, org)
		deps := seed(projID)

		var caps [2]capture
		for i, exprChannel := range []bool{false, true} {
			doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
			w := httpSoakWorker(t, db, doer, exprChannel)
			c := seedHTTPConsumer(t, db, projID, deps...)
			if _, err := w.runCustomHTTP(ctx, c, params(deps)); err != nil {
				t.Fatalf("runCustomHTTP (exprChannel=%v): %v", exprChannel, err)
			}
			caps[i].req = doer.gotReq
		}
		return caps[0].req, caps[1].req
	}

	// Scenario 1: a single text-dep var in a header → byte-identical.
	t.Run("single text dep var in header", func(t *testing.T) {
		var depID string
		legacy, expr := runBoth(t,
			func(projID string) []string {
				w := &Worker{cfg: Config{DB: db}}
				depID = seedTextDep(t, w, projID, textVal)
				return []string{depID}
			},
			func(deps []string) httpParams {
				return httpParams{
					Method:    "GET",
					URL:       "https://api.example.com",
					Headers:   map[string]string{"X-Text": "PRE[{{t}}]POST"},
					Variables: []customVariable{{Name: "t", SourceTodoId: deps[0]}},
				}
			})
		if legacy.Headers["X-Text"] != expr.Headers["X-Text"] {
			t.Fatalf("text dep header not byte-identical:\n legacy=%q\n expr  =%q",
				legacy.Headers["X-Text"], expr.Headers["X-Text"])
		}
		if !strings.Contains(legacy.Headers["X-Text"], textVal) {
			t.Fatalf("sanity: text value not interpolated, got %q", legacy.Headers["X-Text"])
		}
	})

	// Scenario 2: two vars (text + json) across headers + body.
	// text var → byte-identical; json var → semantically equal.
	t.Run("two vars text+json in headers and body", func(t *testing.T) {
		var textDep, jsonDep string
		legacy, expr := runBoth(t,
			func(projID string) []string {
				w := &Worker{cfg: Config{DB: db}}
				textDep = seedTextDep(t, w, projID, textVal2)
				jsonDep = seedJSONDep(t, db, projID, jsonObj)
				return []string{textDep, jsonDep}
			},
			func(deps []string) httpParams {
				return httpParams{
					Method: "POST",
					URL:    "https://api.example.com",
					Headers: map[string]string{
						"X-Text": "{{t}}",
						"X-Json": "{{j}}",
					},
					BodyTemplate: `{"wrapped_text":"{{t}}","wrapped_json":{{j}}}`,
					Variables: []customVariable{
						{Name: "t", SourceTodoId: deps[0]},
						{Name: "j", SourceTodoId: deps[1]},
					},
				}
			})
		// text header: byte-identical.
		if legacy.Headers["X-Text"] != expr.Headers["X-Text"] {
			t.Fatalf("text header not byte-identical:\n legacy=%q\n expr=%q",
				legacy.Headers["X-Text"], expr.Headers["X-Text"])
		}
		if legacy.Headers["X-Text"] != textVal2 {
			t.Fatalf("sanity: text header = %q want %q", legacy.Headers["X-Text"], textVal2)
		}
		// json header: semantically equal (the var value is itself the json-object string).
		assertJSONSemEqual(t, "X-Json header", legacy.Headers["X-Json"], expr.Headers["X-Json"])
		// body: the embedded text part is byte-identical, and the whole body is
		// semantically equal as JSON (wrapped_json may reorder under expr).
		assertJSONSemEqual(t, "request body", string(legacy.Body), string(expr.Body))
		// Cross-check the text portion landed byte-identical inside the body too.
		var lb, eb map[string]any
		_ = json.Unmarshal(legacy.Body, &lb)
		_ = json.Unmarshal(expr.Body, &eb)
		if lb["wrapped_text"] != eb["wrapped_text"] || lb["wrapped_text"] != textVal2 {
			t.Fatalf("body text part diverged: legacy=%v expr=%v want %q",
				lb["wrapped_text"], eb["wrapped_text"], textVal2)
		}
	})

	// Scenario 3: a dep that ran via the script/fallback path (scripts row only, no
	// node_outputs.items) — exercises the exprNodeAccessor + itemsForDep fallback.
	// content_json is a JSON object → semantically equal across flags.
	t.Run("script-fallback json dep in header", func(t *testing.T) {
		const fallbackJSON = `{"title":"Draft Title","logline":"A short film.","characterSheet":"a teal fox"}`
		var depID string
		legacy, expr := runBoth(t,
			func(projID string) []string {
				depID = seedScriptFallbackDep(t, db, projID, fallbackJSON)
				return []string{depID}
			},
			func(deps []string) httpParams {
				return httpParams{
					Method:    "GET",
					URL:       "https://api.example.com",
					Headers:   map[string]string{"X-Fallback": "{{f}}"},
					Variables: []customVariable{{Name: "f", SourceTodoId: deps[0]}},
				}
			})
		assertJSONSemEqual(t, "script-fallback header", legacy.Headers["X-Fallback"], expr.Headers["X-Fallback"])
	})
}

// ---- LLM soak --------------------------------------------------------------

func soakLLM(t *testing.T) {
	ctx := context.Background()

	// The scripted model returns a canned answer (it cannot observe the interpolated
	// prompt), so the observable is the stored node_outputs.content. Both flag states
	// must complete and write equivalent node_outputs.
	run := func(t *testing.T, exprChannel bool, content string) string {
		t.Helper()
		const canned = "MODEL-ANSWER-soak"
		w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: canned})))
		w.cfg.ExprChannel = exprChannel
		projID := seedSoakProject(t, w.cfg.DB, os.Getenv("_CUSTOM_TEST_ORG_ID"))
		dep := seedTextDep(t, w, projID, content)
		c := seedConsumerTodo(t, w, projID, "custom:translate", dep)
		ref, err := w.runCustomLLM(ctx, c, llmParams{
			SystemPrompt: "Context: {{ctx}}",
			UserPrompt:   "Use the upstream: {{ctx}}",
			Variables:    []customVariable{{Name: "ctx", SourceTodoId: dep}},
		})
		if err != nil {
			t.Fatalf("runCustomLLM (exprChannel=%v): %v", exprChannel, err)
		}
		outID := strings.TrimPrefix(ref, "custom:")
		var out string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&out); err != nil {
			t.Fatalf("load node_output: %v", err)
		}
		return out
	}

	t.Run("text dep into system+user prompt", func(t *testing.T) {
		const depText = "LLM-UPSTREAM-DRAFT-soak"
		legacy := run(t, false, depText)
		expr := run(t, true, depText)
		if legacy != expr {
			t.Fatalf("LLM node_outputs.content diverged across flags:\n legacy=%q\n expr  =%q", legacy, expr)
		}
	})
}

// ---- Script soak -----------------------------------------------------------

func soakScript(t *testing.T) {
	ctx := context.Background()
	db := assetTestGorm(t)

	// A custom script node injects the resolved {{name}} value as a Starlark global,
	// then echoes it back as output. The output node_outputs.content is the observable.
	run := func(t *testing.T, exprChannel bool, depText string) string {
		t.Helper()
		org := "org_soak_script_" + randHex3()
		projID, depTodo := seedScriptUpstream(t, db, org, depText)
		w := scriptTestWorker(t, db)
		w.cfg.ExprChannel = exprChannel
		// Consumer must depend on depTodo so the ExprChannel S-2 gate admits it.
		c := seedConsumerTodo(t, w, projID, "custom:script", depTodo)
		// runCustomScript reads params; build them directly (the global is named "g").
		ref, err := w.runCustomScript(ctx, c, scriptParams{
			Code:      `output = g`,
			Variables: []customVariable{{Name: "g", SourceTodoId: depTodo}},
		})
		if err != nil {
			t.Fatalf("runCustomScript (exprChannel=%v): %v", exprChannel, err)
		}
		_, content := readNodeOutput(t, db, ref)
		return content
	}

	t.Run("global var resolves identically across flags", func(t *testing.T) {
		const depText = "SCRIPT-GLOBAL-VALUE-soak"
		legacy := run(t, false, depText)
		expr := run(t, true, depText)
		if legacy != expr {
			t.Fatalf("script global resolution diverged:\n legacy=%q\n expr  =%q", legacy, expr)
		}
		if legacy != depText {
			t.Fatalf("sanity: script did not echo the global, got %q want %q", legacy, depText)
		}
	})
}

// ---- Shadow probe half -----------------------------------------------------

// soakShadowProbe runs the same battery of dep shapes through the live $node shadow
// probe (exprNodeProbe under ExprParity:true) with a captured slog buffer and asserts
// EVERY probe line is class=exact or class=benign — zero class=divergent — and that
// no resolved value or var name leaks into the buffer. A single divergent on a
// legitimate scenario is a real finding and fails the test (the assertion is NOT
// weakened).
func soakShadowProbe(t *testing.T) {
	ctx := context.Background()
	db := assetTestGorm(t)
	projID := seedSoakProject(t, db, "org_soak_probe_"+randHex3())

	const textVal = "PROBE-TEXT-VALUE-soak"
	const jsonObj = `{"b":2,"a":1,"nested":{"z":9}}`
	const fallbackJSON = `{"title":"Probe Title","logline":"L"}`

	w0 := &Worker{cfg: Config{DB: db}}
	textDep := seedTextDep(t, w0, projID, textVal)
	jsonDep := seedJSONDep(t, db, projID, jsonObj)
	fallbackDep := seedScriptFallbackDep(t, db, projID, fallbackJSON)

	// Executing custom todo depends on ALL three (so the probe's live S-2 resolver
	// admits each — every line must classify exact/benign, never denied/divergent).
	exec := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
		 VALUES ($1,$2,'plan-x','custom:next','running',ARRAY[$3,$4,$5]::text[],'{}')`,
		exec, projID, textDep, jsonDep, fallbackDep).Error; err != nil {
		t.Fatalf("seed exec todo: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	w := New(Config{
		DB:         db,
		Todos:      todos.New(db),
		Projects:   project.New(db),
		Events:     events.New(db),
		Logger:     logger,
		ExprParity: true,
		WorkerID:   "soak-probe", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})

	// Distinctive var names so the no-leak check can prove they never appear.
	vars := []customVariable{
		{Name: "probeTEXT_secret", SourceTodoId: textDep},
		{Name: "probeJSON_secret", SourceTodoId: jsonDep},
		{Name: "probeFALLBACK_secret", SourceTodoId: fallbackDep},
	}
	w.exprNodeProbe(ctx, claimed{todoID: exec, projectID: projID}, vars)

	out := buf.String()
	if out == "" {
		t.Fatalf("expected probe log lines, got empty buffer")
	}

	// Parse every "$node shadow probe" line; tally classes; assert zero divergent.
	exact, benign, divergent := 0, 0, 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "expr $node shadow probe") {
			continue
		}
		switch fieldVal(line, "class") {
		case "exact":
			exact++
		case "benign":
			benign++
		case "divergent":
			divergent++
			t.Errorf("FINDING: shadow probe reported class=divergent on a legitimate scenario:\n%s", line)
		default:
			t.Errorf("unexpected probe class on line:\n%s", line)
		}
	}
	if divergent != 0 {
		t.Fatalf("soak FINDING: %d divergent probe line(s) — flip is NOT sound (exact=%d benign=%d)", divergent, exact, benign)
	}
	if exact+benign != len(vars) {
		t.Fatalf("expected %d classified probe lines, got exact=%d benign=%d", len(vars), exact, benign)
	}
	t.Logf("shadow probe classification: exact=%d benign=%d divergent=%d", exact, benign, divergent)

	// No value / var-name leak (F4).
	for _, leak := range []string{
		textVal, jsonObj, fallbackJSON, "Probe Title",
		"probeTEXT_secret", "probeJSON_secret", "probeFALLBACK_secret",
	} {
		if strings.Contains(out, leak) {
			t.Fatalf("F4 violation: probe buffer contains forbidden token %q:\n%s", leak, out)
		}
	}
}
