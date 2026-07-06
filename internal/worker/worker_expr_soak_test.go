package worker

import (
	"context"
	"encoding/json"
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

// TestExprValueSource_Regression is the regression battery for the expr $node
// {{name}} value source — the ONLY value channel since the items cut-over
// (docs/specs/items-cutover.md §3 PR-C). It descends from the P3 differential
// soak that gated the flip; with the legacy channel deleted, each scenario now
// runs once through the authoritative path and asserts the observable output
// against the seeded content:
//   - text-format deps interpolate BYTE-IDENTICAL to the seeded value.
//   - json-object deps interpolate SEMANTICALLY EQUAL (decode + DeepEqual) —
//     the engine decodes→re-marshals so key order / whitespace may differ.
func TestExprValueSource_Regression(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the expr value-source regression battery")
	}

	t.Run("HTTP", func(t *testing.T) { soakHTTP(t) })
	t.Run("LLM", func(t *testing.T) { soakLLM(t) })
	t.Run("Script", func(t *testing.T) { soakScript(t) })
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
// done dep todo whose output_ref is "custom:<coid>". The expr channel resolves
// $node[depTodo].json (re-marshaled object) → semantically equal to objJSON but
// possibly key-reordered. Returns the dep todo id.
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
// exprNodeAccessor output_ref-prefix inference (.json). Returns the dep todo id.
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
// deps (required so the S-2 direct-deps gate admits each dep). Returns the claimed.
func seedHTTPConsumer(t *testing.T, db *gorm.DB, projID string, deps ...string) claimed {
	t.Helper()
	w := &Worker{cfg: Config{DB: db}}
	return seedConsumerTodo(t, w, projID, "custom:http", deps...)
}

// httpSoakWorker builds an http-path worker over db with the given fake fetcher.
func httpSoakWorker(t *testing.T, db *gorm.DB, doer HTTPDoer) *Worker {
	t.Helper()
	return New(Config{
		DB:          db,
		Todos:       todos.New(db),
		Projects:    project.New(db),
		Events:      events.New(db),
		Secrets:     &fakeSecrets{value: "tok"},
		HTTPFetcher: doer,
		WorkerID:    "soak-http", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
}

// assertJSONSemEqual asserts a and b decode to equal JSON values (benign reorder /
// whitespace allowed; nothing else).
func assertJSONSemEqual(t *testing.T, label, a, b string) {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		t.Fatalf("%s: value not valid JSON (a=%q): %v", label, a, err)
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		t.Fatalf("%s: value not valid JSON (b=%q): %v", label, b, err)
	}
	if !reflect.DeepEqual(av, bv) {
		t.Fatalf("%s: JSON values not semantically equal:\n a=%q\n b=%q", label, a, b)
	}
}

// ---- HTTP battery (strongest — byte assertions on the wire) -----------------

func soakHTTP(t *testing.T) {
	ctx := context.Background()
	db := assetTestGorm(t)

	// Recognizable seed content per format.
	const textVal = "UPSTREAM-TEXT-VALUE-soak"
	const textVal2 = "ANOTHER-TEXT-VALUE-soak"
	const jsonObj = `{"b":2,"a":1,"nested":{"z":9,"y":8}}`

	// runOne seeds fresh data, runs the http request through the authoritative
	// channel, and returns the outgoing request captured on the wire.
	runOne := func(t *testing.T, seed func(projID string) (deps []string), params func(deps []string) httpParams) fetch.Request {
		t.Helper()
		org := "org_soak_http_" + randHex3()
		projID := seedSoakProject(t, db, org)
		deps := seed(projID)

		doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
		w := httpSoakWorker(t, db, doer)
		c := seedHTTPConsumer(t, db, projID, deps...)
		if _, err := w.runCustomHTTP(ctx, c, params(deps)); err != nil {
			t.Fatalf("runCustomHTTP: %v", err)
		}
		return doer.gotReq
	}

	// Scenario 1: a single text-dep var in a header → byte-identical interpolation.
	t.Run("single text dep var in header", func(t *testing.T) {
		req := runOne(t,
			func(projID string) []string {
				w := &Worker{cfg: Config{DB: db}}
				return []string{seedTextDep(t, w, projID, textVal)}
			},
			func(deps []string) httpParams {
				return httpParams{
					Method:    "GET",
					URL:       "https://api.example.com",
					Headers:   map[string]string{"X-Text": "PRE[{{t}}]POST"},
					Variables: []customVariable{{Name: "t", SourceTodoId: deps[0]}},
				}
			})
		if want := "PRE[" + textVal + "]POST"; req.Headers["X-Text"] != want {
			t.Fatalf("text dep header = %q, want byte-identical %q", req.Headers["X-Text"], want)
		}
	})

	// Scenario 2: two vars (text + json) across headers + body.
	// text var → byte-identical; json var → semantically equal to the seed.
	t.Run("two vars text+json in headers and body", func(t *testing.T) {
		req := runOne(t,
			func(projID string) []string {
				w := &Worker{cfg: Config{DB: db}}
				textDep := seedTextDep(t, w, projID, textVal2)
				jsonDep := seedJSONDep(t, db, projID, jsonObj)
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
		// text header: byte-identical to the seed.
		if req.Headers["X-Text"] != textVal2 {
			t.Fatalf("text header = %q, want %q", req.Headers["X-Text"], textVal2)
		}
		// json header: semantically equal to the seeded object.
		assertJSONSemEqual(t, "X-Json header", jsonObj, req.Headers["X-Json"])
		// body: the embedded text part is byte-identical; the wrapped json is
		// semantically equal (may reorder through the engine).
		var body map[string]any
		if err := json.Unmarshal(req.Body, &body); err != nil {
			t.Fatalf("request body not valid JSON: %q (%v)", req.Body, err)
		}
		if body["wrapped_text"] != textVal2 {
			t.Fatalf("body text part = %v, want %q", body["wrapped_text"], textVal2)
		}
		wrapped, err := json.Marshal(body["wrapped_json"])
		if err != nil {
			t.Fatalf("re-marshal wrapped_json: %v", err)
		}
		assertJSONSemEqual(t, "body wrapped_json", jsonObj, string(wrapped))
	})

	// Scenario 3: a dep that ran via the script/fallback path (scripts row only, no
	// node_outputs.items) — exercises the exprNodeAccessor + itemsForDep fallback.
	// content_json is a JSON object → semantically equal to the seed.
	t.Run("script-fallback json dep in header", func(t *testing.T) {
		const fallbackJSON = `{"title":"Draft Title","logline":"A short film.","characterSheet":"a teal fox"}`
		req := runOne(t,
			func(projID string) []string {
				return []string{seedScriptFallbackDep(t, db, projID, fallbackJSON)}
			},
			func(deps []string) httpParams {
				return httpParams{
					Method:    "GET",
					URL:       "https://api.example.com",
					Headers:   map[string]string{"X-Fallback": "{{f}}"},
					Variables: []customVariable{{Name: "f", SourceTodoId: deps[0]}},
				}
			})
		assertJSONSemEqual(t, "script-fallback header", fallbackJSON, req.Headers["X-Fallback"])
	})
}

// ---- LLM battery -------------------------------------------------------------

func soakLLM(t *testing.T) {
	ctx := context.Background()

	// The scripted model returns a canned answer (it cannot observe the interpolated
	// prompt), so the observable is that the run completes through the authoritative
	// resolver and lands the canned node_outputs.content.
	t.Run("text dep into system+user prompt", func(t *testing.T) {
		const depText = "LLM-UPSTREAM-DRAFT-soak"
		const canned = "MODEL-ANSWER-soak"
		w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: canned})))
		projID := seedSoakProject(t, w.cfg.DB, os.Getenv("_CUSTOM_TEST_ORG_ID"))
		dep := seedTextDep(t, w, projID, depText)
		c := seedConsumerTodo(t, w, projID, "custom:translate", dep)
		ref, err := w.runCustomLLM(ctx, c, llmParams{
			SystemPrompt: "Context: {{ctx}}",
			UserPrompt:   "Use the upstream: {{ctx}}",
			Variables:    []customVariable{{Name: "ctx", SourceTodoId: dep}},
		})
		if err != nil {
			t.Fatalf("runCustomLLM: %v", err)
		}
		outID := strings.TrimPrefix(ref, "custom:")
		var out string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&out); err != nil {
			t.Fatalf("load node_output: %v", err)
		}
		if out != canned {
			t.Fatalf("LLM node_outputs.content = %q, want %q", out, canned)
		}
	})
}

// ---- Script battery ------------------------------------------------------------

func soakScript(t *testing.T) {
	ctx := context.Background()
	db := assetTestGorm(t)

	// A custom script node injects the resolved {{name}} value as a Starlark global,
	// then echoes it back as output. The output node_outputs.content is the observable.
	t.Run("global var resolves byte-identically", func(t *testing.T) {
		const depText = "SCRIPT-GLOBAL-VALUE-soak"
		org := "org_soak_script_" + randHex3()
		projID, depTodo := seedScriptUpstream(t, db, org, depText)
		w := scriptTestWorker(t, db)
		// Consumer must depend on depTodo so the S-2 gate admits it.
		c := seedConsumerTodo(t, w, projID, "custom:script", depTodo)
		ref, err := w.runCustomScript(ctx, c, scriptParams{
			Code:      `output = g`,
			Variables: []customVariable{{Name: "g", SourceTodoId: depTodo}},
		})
		if err != nil {
			t.Fatalf("runCustomScript: %v", err)
		}
		_, content := readNodeOutput(t, db, ref)
		if content != depText {
			t.Fatalf("script did not echo the global, got %q want %q", content, depText)
		}
	})
}
