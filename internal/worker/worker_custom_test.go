package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
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

// TestResolveOutputText_ScriptAndCustom verifies that resolveOutputText correctly
// reads from scripts.content_json and node_outputs.content, and errors on asset refs.
func TestResolveOutputText_ScriptAndCustom(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	pool := assetTestPool(t)

	// Insert a project for foreign-key constraints.
	var projID string
	_ = pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'orgX','p','u') RETURNING id`,
	).Scan(&projID)

	// Seed a scripts row. scripts.content_json is JSONB, so postgres will
	// normalize the stored value (e.g. add spaces). We read back what postgres
	// stored and compare against that, not the raw input string.
	scriptID := newID()
	scriptContent := `{"title":"hello"}`
	_, err := pool.Exec(ctx,
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,'todo-x',$3,1)`,
		scriptID, projID, []byte(scriptContent))
	if err != nil {
		t.Fatalf("seed script: %v", err)
	}
	// Read back what postgres actually stored (JSONB normalized).
	var storedScriptContent string
	_ = pool.QueryRow(ctx, `SELECT content_json::text FROM scripts WHERE id=$1`, scriptID).Scan(&storedScriptContent)

	// Seed a node_outputs row.
	outID := newID()
	outContent := "resolved custom output"
	_, err = pool.Exec(ctx,
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format) VALUES ($1,$2,'todo-y','custom:llm',$3,'text')`,
		outID, projID, outContent)
	if err != nil {
		t.Fatalf("seed node_output: %v", err)
	}

	// Build a worker just to exercise resolveOutputText.
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "unused"})))

	// script: ref returns content_json blob (as stored by postgres JSONB).
	got, err := w.resolveOutputText(ctx, "script:"+scriptID)
	if err != nil {
		t.Fatalf("resolveOutputText script: %v", err)
	}
	if got != storedScriptContent {
		t.Fatalf("script: want %q got %q", storedScriptContent, got)
	}

	// custom: ref returns node_outputs.content.
	got, err = w.resolveOutputText(ctx, "custom:"+outID)
	if err != nil {
		t.Fatalf("resolveOutputText custom: %v", err)
	}
	if got != outContent {
		t.Fatalf("custom: want %q got %q", outContent, got)
	}

	// asset: ref must error.
	if _, err := w.resolveOutputText(ctx, "asset:someID"); err == nil {
		t.Fatalf("expected error for asset: ref, got nil")
	}
}

// TestRunCustomLLM_TextAndJSON is the core executor test:
//  1. Seeds a script todo (status=done, output_ref="script:<id>") + a matching
//     scripts row.
//  2. Seeds a typed custom todo whose input_json has kind=llm, params with
//     {{draft}} template, and variables:[{name:"draft",sourceTodoId:<scriptTodo>}].
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
			`INSERT INTO todos (id, project_id, plan_id, type, status, input_json)
			 VALUES ($1,$2,'plan-x','custom:translate','running',$3)`,
			customTodoID, projID, buildInput("text"))
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
			`INSERT INTO todos (id, project_id, plan_id, type, status, input_json)
			 VALUES ($1,$2,'plan-x','custom:translate','running',$3)`,
			customTodoID, projID, buildInput("json"))
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
