package worker

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"gorm.io/gorm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// TestItemsInput_Regression is the regression battery for the per-dep items
// input channel — the ONLY upstream-input channel for runStoryboard/runPrescreen
// since the items cut-over (docs/specs/items-cutover.md §3 PR-C). It descends
// from the PR-A/PR-B differential soak; with the legacy JOIN reads deleted, each
// scenario now runs once through the authoritative path (loadInputsByDep) and
// asserts the selection semantics ("多 dep 按 updated_at 挑最新单个上游") plus
// the resolved content against the seeded data.
//
// The scripted model returns a canned answer, so the observables are (a) the
// returned output_ref (which upstream got SELECTED) and (b) the exact prompt fed
// to the agent (captured by promptCapturingModel — which upstream CONTENT the
// selection resolved).
func TestItemsInput_Regression(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run the items input regression battery")
	}
	t.Run("StoryboardInput", func(t *testing.T) { soakStoryboardInput(t) })
	t.Run("PrescreenInput", func(t *testing.T) { soakPrescreenInput(t) })
}

// ---- prompt capture ---------------------------------------------------------

// promptCapturingModel wraps a ChatModel and records every Generate request so
// the battery can assert the EXACT prompt the resolved input produced.
type promptCapturingModel struct {
	inner llm.ChatModel
	mu    sync.Mutex
	reqs  []llm.Request
}

func (m *promptCapturingModel) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	m.mu.Lock()
	m.reqs = append(m.reqs, req)
	m.mu.Unlock()
	return m.inner.Generate(ctx, req)
}

func (m *promptCapturingModel) Stream(ctx context.Context, req llm.Request) (llm.StreamReader, error) {
	return m.inner.Stream(ctx, req)
}

func (m *promptCapturingModel) Info() llm.ProviderInfo { return m.inner.Info() }

// capturedPrompt joins the last request's system prompt + message contents into
// one comparable string.
func (m *promptCapturingModel) capturedPrompt(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.reqs) == 0 {
		t.Fatalf("no Generate request captured")
	}
	req := m.reqs[len(m.reqs)-1]
	var b strings.Builder
	b.WriteString(req.SystemPrompt)
	for _, msg := range req.Messages {
		b.WriteString("\n--\n")
		b.WriteString(msg.Content)
	}
	return b.String()
}

// assertPromptJSONMiddle extracts the json span between start/end markers in the
// prompt and asserts it is semantically-equal JSON to want (the accepted
// envelope for a json upstream: JSONB round-trips may reorder keys / normalize
// whitespace; nothing else may differ).
func assertPromptJSONMiddle(t *testing.T, label, prompt, want, start, end string) {
	t.Helper()
	_, mid, _ := splitAround(t, prompt, start, end)
	assertJSONSemEqual(t, label, want, mid)
}

// splitAround splits s into (before+start, middle, end+after) around the FIRST
// occurrence of start and the LAST occurrence of end after it.
func splitAround(t *testing.T, s, start, end string) (string, string, string) {
	t.Helper()
	i := strings.Index(s, start)
	if i < 0 {
		t.Fatalf("prompt missing marker %q:\n%s", start, s)
	}
	j := strings.LastIndex(s, end)
	if j < i+len(start) {
		t.Fatalf("prompt missing end marker %q after %q:\n%s", end, start, s)
	}
	return s[:i+len(start)], s[i+len(start) : j], s[j:]
}

// ---- seed helpers -----------------------------------------------------------

// seedScriptParentAged seeds one upstream script parent: a scripts row + a done
// 'script' todo (output_ref 'script:<id>', updated_at = now()-age). When
// withItems it also seeds the runScript-shaped dual-write items row
// ([{json:<content>}], format='items'); withItems=false models a straddling dep
// that completed under pre-items code (★M-4 projection fallback).
func seedScriptParentAged(t *testing.T, db *gorm.DB, projID, contentJSON string, withItems bool, age time.Duration) (todoID, scriptID string) {
	t.Helper()
	ctx := context.Background()
	scriptID = newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
		scriptID, projID, newID(), []byte(contentJSON)).Error; err != nil {
		t.Fatalf("seed scripts row: %v", err)
	}
	todoID = newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json, updated_at)
		 VALUES ($1,$2,'plan-x','script','done',$3,'{}',$4)`,
		todoID, projID, "script:"+scriptID, time.Now().Add(-age)).Error; err != nil {
		t.Fatalf("seed script todo: %v", err)
	}
	if withItems {
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
			 VALUES ($1,$2,$3,'script','','items',$4)`,
			newID(), projID, todoID, []byte(`[{"json":`+contentJSON+`}]`)).Error; err != nil {
			t.Fatalf("seed script items row: %v", err)
		}
	}
	return todoID, scriptID
}

// seedCustomDepAged seeds a done custom dep (output_ref 'custom:<coid>',
// updated_at = now()-age) whose node_outputs row carries content+format and,
// when withItems, the dual-write items payload.
func seedCustomDepAged(t *testing.T, db *gorm.DB, projID, content, format, itemsJSON string, age time.Duration) string {
	t.Helper()
	ctx := context.Background()
	depTodo := newID()
	coid := newID()
	if itemsJSON != "" {
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
			 VALUES ($1,$2,$3,'custom:llm',$4,$5,$6)`,
			coid, projID, depTodo, content, format, []byte(itemsJSON)).Error; err != nil {
			t.Fatalf("seed custom dep node_output: %v", err)
		}
	} else {
		// Straddling row (pre-items code): items stays the '[]' column default.
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
			 VALUES ($1,$2,$3,'custom:llm',$4,$5)`,
			coid, projID, depTodo, content, format).Error; err != nil {
			t.Fatalf("seed straddling custom dep node_output: %v", err)
		}
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json, updated_at)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}',$4)`,
		depTodo, projID, "custom:"+coid, time.Now().Add(-age)).Error; err != nil {
		t.Fatalf("seed custom dep todo: %v", err)
	}
	return depTodo
}

// seedConsumerAt inserts a running consumer todo of typ with the given deps and
// returns its claimed (like seedConsumerTodo, reused via a Worker shim).
func seedConsumerAt(t *testing.T, db *gorm.DB, projID, typ string, deps ...string) claimed {
	t.Helper()
	w := &Worker{cfg: Config{DB: db}}
	return seedConsumerTodo(t, w, projID, typ, deps...)
}

// ---- storyboard input battery --------------------------------------------------

func soakStoryboardInput(t *testing.T) {
	ctx := context.Background()
	db := assetTestGorm(t)

	// StoryboardAgent's user prompt is "Script JSON:\n%s\n\nStyle: %s"; the shots
	// answer is canned. PG-canonical json carries no newlines, so the markers are
	// unambiguous.
	const sbAnswer = `{"shots":[{"shotNo":1,"camera":"wide","scene":"s1","action":"a1","prompt":"p1","duration":3}]}`
	const jsonStart = "Script JSON:\n"
	const jsonEnd = "\n\nStyle:"

	// runOne runs one storyboard execution over the seeded deps and returns
	// (output_ref, captured prompt).
	runOne := func(t *testing.T, projID string, deps []string) (string, string) {
		t.Helper()
		model := &promptCapturingModel{inner: llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: sbAnswer}))}
		w := itemsTestWorker(t, db, model)
		c := seedConsumerAt(t, db, projID, "storyboard", deps...)
		c.input = []byte(`{}`)
		ref, err := w.runStoryboard(ctx, c)
		if err != nil {
			t.Fatalf("runStoryboard: %v", err)
		}
		// runStoryboard fans out one READY asset todo per shot (AddDynamic).
		// Delete them: this battery never drains a queue, and leftover claimable
		// todos would poison later queue-draining tests sharing the DB.
		if err := db.WithContext(ctx).Exec(
			`DELETE FROM todos WHERE project_id=$1 AND type='asset'`, projID).Error; err != nil {
			t.Fatalf("cleanup fan-out asset todos: %v", err)
		}
		return ref, model.capturedPrompt(t)
	}

	// Scenario 1: TWO script parents — the selection semantics (newest updated_at
	// wins) must hold: the NEWER script is picked and embedded.
	t.Run("newest of two script parents wins", func(t *testing.T) {
		projID := seedItemsProject(t, db)
		const older = `{"title":"OLD-SCRIPT-soak","logline":"old","scenes":[{"heading":"H","description":"D","dialogue":"x"}]}`
		const newer = `{"title":"NEW-SCRIPT-soak","logline":"new","scenes":[{"heading":"H","description":"D","dialogue":"y"}]}`
		oldTodo, _ := seedScriptParentAged(t, db, projID, older, true, time.Hour)
		newTodo, newScriptID := seedScriptParentAged(t, db, projID, newer, true, 0)

		ref, prompt := runOne(t, projID, []string{oldTodo, newTodo})
		if want := "shots:" + newScriptID; ref != want {
			t.Fatalf("want the NEWER script selected (%q), got %q", want, ref)
		}
		assertPromptJSONMiddle(t, "storyboard two-parent prompt", prompt, newer, jsonStart, jsonEnd)
		if !strings.Contains(prompt, "NEW-SCRIPT-soak") || strings.Contains(prompt, "OLD-SCRIPT-soak") {
			t.Fatalf("prompt must embed the newer script only:\n%s", prompt)
		}
	})

	// Scenario 2: a straddling script parent (NO items row) — the items channel
	// must satisfy it via itemsForDep's output_ref projection fallback (★M-4).
	t.Run("straddling script parent falls back to projection", func(t *testing.T) {
		projID := seedItemsProject(t, db)
		const content = `{"title":"STRADDLE-SCRIPT-soak","logline":"L","scenes":[{"heading":"H","description":"D","dialogue":"z"}]}`
		depTodo, scriptID := seedScriptParentAged(t, db, projID, content, false, 0)

		ref, prompt := runOne(t, projID, []string{depTodo})
		if want := "shots:" + scriptID; ref != want {
			t.Fatalf("want %q, got %q", want, ref)
		}
		assertPromptJSONMiddle(t, "storyboard straddling prompt", prompt, content, jsonStart, jsonEnd)
	})

	// Scenario 3: NO script parent edge — the project-wide newest-script heuristic
	// (M1 compat) is preserved AS-IS.
	t.Run("no parent edge preserves project-wide heuristic", func(t *testing.T) {
		projID := seedItemsProject(t, db)
		const content = `{"title":"HEURISTIC-SCRIPT-soak","logline":"L","scenes":[{"heading":"H","description":"D","dialogue":"h"}]}`
		scriptID := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
			scriptID, projID, newID(), []byte(content)).Error; err != nil {
			t.Fatalf("seed heuristic scripts row: %v", err)
		}

		ref, prompt := runOne(t, projID, nil)
		if want := "shots:" + scriptID; ref != want {
			t.Fatalf("want heuristic script %q, got %q", want, ref)
		}
		assertPromptJSONMiddle(t, "storyboard heuristic prompt", prompt, content, jsonStart, jsonEnd)
	})
}

// ---- prescreen input battery ----------------------------------------------------

func soakPrescreenInput(t *testing.T) {
	ctx := context.Background()
	db := assetTestGorm(t)

	// ReviewAgent's user prompt embeds the upstream text as
	// "Generation prompt: %s\nStyle: ..."; the verdict is canned.
	const verdict = `{"score":80,"flags":[],"note":"ok"}`
	const textStart = "Generation prompt: "
	const textEnd = "\nStyle:"

	runOne := func(t *testing.T, projID string, deps []string) (string, string) {
		t.Helper()
		model := &promptCapturingModel{inner: llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: verdict}))}
		w := New(Config{
			DB:       db,
			Todos:    todos.New(db),
			Projects: project.New(db),
			Events:   events.New(db),
			Review:   studioagents.NewReviewAgent(model),
			WorkerID: "items-soak-ps", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
		})
		c := seedConsumerAt(t, db, projID, "prescreen", deps...)
		c.input = []byte(`{}`)
		ref, err := w.runPrescreen(ctx, c)
		if err != nil {
			t.Fatalf("runPrescreen: %v", err)
		}
		return ref, model.capturedPrompt(t)
	}

	// Scenario 1: two text deps + a NEWER shots dep — the newest TEXT-SOURCE dep
	// wins (selection semantics + the script:/custom: prefix filter); the text is
	// embedded byte-identically.
	t.Run("newest text dep wins and shots dep is excluded", func(t *testing.T) {
		projID := seedItemsProject(t, db)
		const oldText = "OLD-PRESCREEN-TEXT-soak"
		const newText = "NEW-PRESCREEN-TEXT-soak"
		oldDep := seedCustomDepAged(t, db, projID, oldText, "text",
			`[{"json":{"text":"`+oldText+`"}}]`, time.Hour)
		newDep := seedCustomDepAged(t, db, projID, newText, "text",
			`[{"json":{"text":"`+newText+`"}}]`, 0)
		// A shots: dep NEWER than both text deps — excluded by the prefix filter.
		shotsDep := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json, updated_at)
			 VALUES ($1,$2,'plan-x','storyboard','done','shots:none','{}',$3)`,
			shotsDep, projID, time.Now().Add(time.Minute)).Error; err != nil {
			t.Fatalf("seed shots dep: %v", err)
		}

		_, prompt := runOne(t, projID, []string{oldDep, newDep, shotsDep})
		if !strings.Contains(prompt, newText) || strings.Contains(prompt, oldText) {
			t.Fatalf("prompt must embed the newer text only:\n%s", prompt)
		}
	})

	// Scenario 2: a script upstream (json content) — the embedded json is
	// semantically equal to the seeded script content.
	t.Run("script upstream json is semantically equal", func(t *testing.T) {
		projID := seedItemsProject(t, db)
		const content = `{"title":"PS-SCRIPT-soak","logline":"L","scenes":[{"heading":"H","description":"D","dialogue":"q"}]}`
		depTodo, _ := seedScriptParentAged(t, db, projID, content, true, 0)

		_, prompt := runOne(t, projID, []string{depTodo})
		assertPromptJSONMiddle(t, "prescreen script upstream", prompt, content, textStart, textEnd)
	})

	// Scenario 3: a custom json upstream stored with NON-canonical key order —
	// the items channel reads the JSONB-normalized item; the accepted envelope is
	// json semantic equality against the seed.
	t.Run("custom json upstream is semantically equal", func(t *testing.T) {
		projID := seedItemsProject(t, db)
		const obj = `{"beta":2,"alpha":1,"nested":{"z":9,"y":8}}`
		depTodo := seedCustomDepAged(t, db, projID, obj, "json", `[{"json":`+obj+`}]`, 0)

		_, prompt := runOne(t, projID, []string{depTodo})
		assertPromptJSONMiddle(t, "prescreen custom json upstream", prompt, obj, textStart, textEnd)
	})

	// Scenario 4: a straddling custom TEXT dep (no items payload — column default
	// '[]') — itemsForDep's projection fallback must reproduce the stored content
	// byte-identically.
	t.Run("straddling custom text dep falls back byte-identically", func(t *testing.T) {
		projID := seedItemsProject(t, db)
		const content = "STRADDLE-PRESCREEN-TEXT-soak"
		depTodo := seedCustomDepAged(t, db, projID, content, "text", "", 0)

		_, prompt := runOne(t, projID, []string{depTodo})
		if !strings.Contains(prompt, content) {
			t.Fatalf("prompt must embed the straddling content:\n%s", prompt)
		}
	})
}
