# Custom Nodes Phase 2C — `script` kind (Starlark) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** Add a `script` execution kind that runs author-written Starlark on upstream node outputs, secure-by-construction (no I/O, no secrets), returning a string/JSON to `node_outputs`.

**Architecture:** New `internal/scriptengine` wraps `go.starlark.net` with step/time/output limits + classified sentinel errors. `runCustom` gains a `case "script"`. No new table, no migration, no admin-gate, no secrets (D1). Mirrors the 2B `http` kind but simpler.

**Tech Stack:** Go, `go.starlark.net/starlark` + `/lib/json` + `/syntax`, GORM, React/TS.

> All `go` commands run with `GOWORK=off`. DB-gated tests use a FRESH DB with `-p 1`: `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p2c_<rand>?sslmode=disable`.

---

### Task C0.1: `internal/scriptengine` package + `Run`

**Files:**
- Create: `internal/scriptengine/engine.go`
- Create: `internal/scriptengine/engine_test.go`
- Modify: `go.mod` / `go.sum` (add `go.starlark.net`)

- [ ] **Step 1: Add the dependency**

Run: `GOWORK=off go get go.starlark.net@latest`

- [ ] **Step 2: Write `engine_test.go` (table-driven, security-first)**

Cover, as a fresh-context author, these cases (use the real `Run` signature):
- `output = "hi"` → returns exactly `hi` (NOT `"hi"` — guards `string(s)` vs `s.String()`).
- `output = upstream.upper()` with `inputs={"upstream":"ab"}` → `AB` (data-global injection).
- `output = json.encode({"k": 1})` → `{"k":1}` (json module present & pure).
- no `output` assigned → `errors.Is(err, ErrOutputMissing)`.
- `output = 5` (non-string) → `errors.Is(err, ErrFailed)`.
- huge output (`output = "x" * (300*1024)`, OutputCap 256K) → `errors.Is(err, ErrOutputTooLarge)`.
- infinite-ish loop via tiny MaxSteps (`MaxSteps:1000`, code `output = str(sorted(range(100000)))`) → `errors.Is(err, ErrTimeout)`.
- ctx already-cancelled before Run → `errors.Is(err, ErrTimeout)`.
- **sandbox escape asserts**: `open("x")`, `load("m", "x")`, `print(1)` referencing filesystem — assert each fails to compile/run (builtins absent); assert `globals` after a benign run contains no `open`/`load`.
- **error-non-leakage**: a script that fails referencing a sensitive input value → assert `err`'s surfaced sentinel is one of the four; (worker maps to bare enum — tested in C1).

Run: `GOWORK=off go test ./internal/scriptengine/... -count=1` → FAIL (package missing).

- [ ] **Step 3: Write `engine.go`**

```go
// Package scriptengine runs author-authored Starlark with step/time/output
// limits. Secure-by-construction: NO I/O builtins are granted (no open/file/
// network), so a script can only transform its given inputs into an output
// string. Errors are CLASSIFIED sentinels — the raw Starlark error (which
// embeds source lines + variable values) is wrapped for server logs only and
// MUST NOT be surfaced to the frontend (the caller maps these to a bare
// opaque enum). See spec D1/D3/D4.
package scriptengine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	starlarkjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Classified sentinels. Callers use errors.Is and surface only a bare enum.
var (
	ErrFailed         = errors.New("scriptengine: script failed")
	ErrTimeout        = errors.New("scriptengine: timed out")
	ErrOutputMissing  = errors.New("scriptengine: no output assigned")
	ErrOutputTooLarge = errors.New("scriptengine: output too large")
)

const (
	DefaultMaxSteps  uint64 = 10_000_000
	DefaultOutputCap int    = 256 * 1024
)

// Options bounds a run. Zero fields take the Default* values.
type Options struct {
	MaxSteps  uint64
	OutputCap int
}

// Run executes Starlark `code` with `inputs` injected as predeclared string
// globals (plus a pure `json` module). The script must assign a string to the
// global `output`. Returns that string. ctx cancellation / step-budget overrun
// → ErrTimeout; any other failure → ErrFailed (raw err wrapped for logs only).
func Run(ctx context.Context, code string, inputs map[string]string, opt Options) (string, error) {
	maxSteps := opt.MaxSteps
	if maxSteps == 0 {
		maxSteps = DefaultMaxSteps
	}
	outCap := opt.OutputCap
	if outCap == 0 {
		outCap = DefaultOutputCap
	}

	// Data-global injection (NOT string substitution into source): input values
	// are first-class Starlark strings, never parsed as code → no injection-
	// channel confusion (spec D2). `json` is pure (encode/decode only, no I/O).
	predeclared := starlark.StringDict{"json": starlarkjson.Module}
	for k, v := range inputs {
		predeclared[k] = starlark.String(v)
	}

	thread := &starlark.Thread{Name: "node"}
	thread.SetMaxExecutionSteps(maxSteps)
	thread.Print = func(*starlark.Thread, string) {} // swallow print; no stderr spam

	// Wall-time / cancellation: Cancel is goroutine-safe (atomic CAS); Starlark
	// checks it between steps. ctx carries the worker's CallTimeout.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel("ctx")
		case <-done:
		}
	}()

	// FileOptions: allow top-level if/for + global reassignment for author
	// ergonomics; leave Set/While/Recursion off (the unbounded constructs).
	fileOpts := &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true}
	globals, err := starlark.ExecFileOptions(fileOpts, thread, "node.star", []byte(code), predeclared)
	if err != nil {
		// Step-budget and wall-time BOTH surface as "...cancelled..." with an
		// unreadable reason — branch on ctx.Err() FIRST (spec D3 / finding #2).
		if ctx.Err() != nil || strings.Contains(err.Error(), "cancelled") {
			return "", fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return "", fmt.Errorf("%w: %v", ErrFailed, err)
	}

	out, ok := globals["output"]
	if !ok {
		return "", ErrOutputMissing
	}
	s, ok := out.(starlark.String)
	if !ok {
		return "", fmt.Errorf("%w: output must be a string (use json.encode for JSON)", ErrFailed)
	}
	str := string(s) // raw bytes; NEVER s.String() (returns a quoted repr)
	if len(str) > outCap {
		return "", ErrOutputTooLarge
	}
	return str, nil
}
```

- [ ] **Step 4: Run tests** → `GOWORK=off go test ./internal/scriptengine/... -count=1` → PASS. Run `GOWORK=off go build ./...`.
- [ ] **Step 5: Commit** `feat(scriptengine): Starlark runner — no-I/O, step/time/output limits, classified errors`

> **C0 gets an INDEPENDENT security review** (sandbox boundary: no builtins leak; error non-leakage; the OOM residual is documented, not claimed-fixed).

---

### Task C1.1: worker `scriptParams` + `scriptError` enum + `classifyScriptError`

**Files:** Modify `internal/worker/worker.go` (near `httpParams`/`httpError`, ~line 1523-1545)

- [ ] **Step 1:** Add types after `httpError` consts:

```go
// scriptParams is the "script" kind's params. No Language (v1 Starlark only),
// no secret field (D1: scripts forbid {{secret:}} — Starlark has no network so
// a secret would be a pure exfil oracle). url-free, I/O-free.
type scriptParams struct {
	Code         string           `json:"code"`
	OutputFormat string           `json:"outputFormat"` // "text" | "json"
	Variables    []customVariable `json:"variables"`
}

// scriptError is the opaque enum surfaced to the frontend — mirrors httpError.
// .Error() returns the BARE enum (never %w-wrap a scriptengine error onto the
// surfaced path: the raw Starlark error embeds source lines + variable values).
type scriptError string

func (e scriptError) Error() string { return string(e) }

const (
	errScriptFailed     scriptError = "script_failed"
	errScriptTimeout    scriptError = "script_timeout"
	errScriptOutputMiss scriptError = "script_output_missing"
	errScriptTooLarge   scriptError = "script_output_too_large"
)

// classifyScriptError maps a scriptengine sentinel to a bare opaque enum.
func classifyScriptError(err error) error {
	switch {
	case errors.Is(err, scriptengine.ErrTimeout):
		return errScriptTimeout
	case errors.Is(err, scriptengine.ErrOutputMissing):
		return errScriptOutputMiss
	case errors.Is(err, scriptengine.ErrOutputTooLarge):
		return errScriptTooLarge
	default:
		return errScriptFailed
	}
}
```

Add the import `"github.com/costa92/llm-agent-studio/internal/scriptengine"`.

- [ ] **Step 2:** Build, commit `feat(worker): scriptParams + scriptError opaque enum + classifyScriptError`.

---

### Task C1.2: Extract shared `resolveVariables` (NIT #9)

**Files:** Modify `internal/worker/worker.go` (`runCustomLLM` ~1582-1598, `runCustomHTTP` ~1652-1667)

- [ ] **Step 1:** Add helper near `runCustom`:

```go
// resolveVariables resolves each post-rewrite varBinding to its upstream node's
// text output. Shared by every custom kind (llm/http/script).
func (w *Worker) resolveVariables(ctx context.Context, vars []customVariable) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vars {
		if v.SourceTodoId == "" {
			continue
		}
		var outputRef string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT COALESCE(output_ref,'') FROM todos WHERE id=$1`, v.SourceTodoId).Row().Scan(&outputRef); err != nil {
			return nil, fmt.Errorf("worker: load variable %q source todo: %w", v.Name, err)
		}
		text, err := w.resolveOutputText(ctx, outputRef)
		if err != nil {
			return nil, fmt.Errorf("worker: resolve variable %q: %w", v.Name, err)
		}
		out[v.Name] = text
	}
	return out, nil
}
```

- [ ] **Step 2:** Replace the inline loop in `runCustomLLM` (build `replacer`) and in `runCustomHTTP` (build `nameVals`) with `m, err := w.resolveVariables(ctx, in.Variables)`. Keep http's opaque-error wrapping: http returns `errRequestFailed` on resolution error, so wrap there (`if err != nil { return "", errRequestFailed }`); llm keeps its descriptive error.
- [ ] **Step 3:** `GOWORK=off go test ./internal/worker/... -count=1` (fresh DB) → existing llm/http tests still PASS.
- [ ] **Step 4:** Commit `refactor(worker): extract shared resolveVariables (llm/http), prep for script`.

---

### Task C1.3: `runCustom` case + `runCustomScript`

**Files:** Modify `internal/worker/worker.go`

- [ ] **Step 1:** Write a worker test (fresh DB) `TestRunCustomScript_*`: a script type todo with an upstream output bound as a var; assert `node_outputs` content == the transformed text; assert format json path; assert a failing script surfaces a bare `script_*` enum (NOT containing source/values); assert `{{secret:K}}` in code → `errScriptFailed`.
- [ ] **Step 2:** Add the dispatch case in `runCustom`'s switch (after `case "http"`):

```go
	case "script":
		var p scriptParams
		if err := json.Unmarshal(env.Params, &p); err != nil {
			return "", fmt.Errorf("worker: custom script params unmarshal: %w", err)
		}
		return w.runCustomScript(ctx, c, p)
```

- [ ] **Step 3:** Add `runCustomScript` after `runCustomHTTP`:

```go
// runCustomScript executes the "script" kind: resolve {{name}} upstream
// variables, inject them as Starlark data-globals, run the sandboxed engine
// (no I/O, no secrets), and land node_outputs. ALL errors are opaque
// (scriptError enum) — never surface the raw Starlark error.
func (w *Worker) runCustomScript(ctx context.Context, c claimed, in scriptParams) (string, error) {
	// D1 runtime defense-in-depth: scripts may not reference secrets.
	if secretRefRe.MatchString(in.Code) || strings.Contains(in.Code, "{{secret:") {
		return "", errScriptFailed
	}
	inputs, err := w.resolveVariables(ctx, in.Variables)
	if err != nil {
		return "", errScriptFailed // opaque: never leak the variable/source
	}
	out, err := scriptengine.Run(ctx, in.Code, inputs, scriptengine.Options{})
	if err != nil {
		return "", classifyScriptError(err)
	}
	format := "text"
	if in.OutputFormat == "json" {
		var probe any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &probe); jerr != nil {
			return "", errScriptFailed
		}
		out = strings.TrimSpace(out)
		format = "json"
	}
	outID := newID()
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		outID, c.projectID, c.todoID, c.typ, out, format).Error; err != nil {
		return "", fmt.Errorf("worker: insert node_output: %w", err)
	}
	return "custom:" + outID, nil
}
```

- [ ] **Step 4:** `GOWORK=off go test ./internal/worker/... -count=1` (fresh DB) → PASS. Build.
- [ ] **Step 5:** Commit `feat(worker): runCustomScript (Starlark kind) + runCustom dispatch case`.

---

### Task C2: customnodetype `script` kind + save-time validation

**Files:** Modify `internal/customnodetype/store.go`

- [ ] **Step 1:** Add tests to `store_test.go`: valid script params create OK; empty `code` → error; `code` containing `{{secret:X}}` → error; bad `outputFormat` → error.
- [ ] **Step 2:** `validKinds["script"] = true`; update the `validate` error string to `(want llm|http|script)`. Add the branch in `validate`:

```go
	if in.Kind == "script" {
		return validateScriptParams(in.Params)
	}
```

- [ ] **Step 3:** Add `validateScriptParams`:

```go
// validateScriptParams enforces the script kind's save-time rules: code
// required; outputFormat ∈ text|json; {{secret:}} forbidden (D1 — Starlark has
// no network, an injected secret is a pure exfil oracle).
func validateScriptParams(raw json.RawMessage) error {
	var p struct {
		Code         string `json:"code"`
		OutputFormat string `json:"outputFormat"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("customnodetype: script params: %w", err)
	}
	if strings.TrimSpace(p.Code) == "" {
		return fmt.Errorf("customnodetype: script code required")
	}
	if secretRefRe.MatchString(p.Code) {
		return fmt.Errorf("customnodetype: {{secret:...}} not allowed in script code")
	}
	if p.OutputFormat != "" && p.OutputFormat != "text" && p.OutputFormat != "json" {
		return fmt.Errorf("customnodetype: script outputFormat %q invalid (text|json)", p.OutputFormat)
	}
	return nil
}
```

- [ ] **Step 4:** `GOWORK=off go test ./internal/customnodetype/... -count=1` (fresh DB) → PASS. Commit `feat(customnodetype): script kind + save-time validation (code/outputFormat/no-secret)`.

> **httpapi: NO CHANGE.** `script` carries no secret → `requireAdminForSecret`/`bodyBearsSecret` (http-specific) don't fire; editor creates script types via existing CRUD. Confirm by reading `customnodetypehandlers.go` — do not edit.

---

### Task C3: Frontend — types + ScriptParamForm + manager + canvas

**Files:** Modify `web/src/lib/types.ts`; Create `web/src/features/custom-node-types/ScriptParamForm.tsx`; Modify `CustomNodeTypeManager.tsx`; canvas typed-node + run-view (mirror the llm/http wiring).

- [ ] **Step 1:** `types.ts`: add `ScriptParams { code: string; outputFormat: 'text'|'json'; variables: {name:string; sourceNodeId?:string}[] }`; extend kind union to `'llm'|'http'|'script'`.
- [ ] **Step 2:** `ScriptParamForm.tsx`: a monospace `<textarea>` for `code` (placeholder `output = upstream_text.upper()`), an outputFormat select, and the same `{{name}}`→varBinding rows the llm form uses (extract the upstream-var UI if shared). NO secret picker, NO allowResponseBody (D1).
- [ ] **Step 3:** `CustomNodeTypeManager.tsx`: add the `script` branch to the kind switch (form selection + default params).
- [ ] **Step 4:** Canvas: register the `script` typed node like `llm`/`http` (reuse 2A typed rendering + varBindings; no http-specific machinery). Run view shows output directly (no suppression).
- [ ] **Step 5:** `cd web && npx vitest run` + `npx tsc --noEmit` → green. Commit `feat(web): script kind — ScriptParamForm + manager + canvas typed node`.

---

### Task C4: go.mod tidy + full sweep + smoke

- [ ] **Step 1:** `GOWORK=off go mod tidy`; confirm `go.starlark.net` + transitive (`starlarkstruct`, `lib/json`) pinned. Commit if go.sum changed.
- [ ] **Step 2:** Full Go sweep on a FRESH DB: `GOWORK=off go build ./... && GOWORK=off go vet ./... && LLM_AGENT_STUDIO_PG_URL=...studio_p2c_<rand>... GOWORK=off go test ./internal/... -count=1 -p 1` → all packages ok.
- [ ] **Step 3:** Web: `cd web && npx vitest run && npx tsc --noEmit`.
- [ ] **Step 4 (optional, if dev instance available):** real-server smoke mirroring 2A/2B — create a `script` type (editor, no admin needed), run a workflow node, confirm `node_outputs` content; confirm a `{{secret:}}` code → 400; confirm a runaway script → `script_timeout` SSE without leaking source.
- [ ] **Step 5:** Final whole-branch code review. Then `superpowers:finishing-a-development-branch` (user opens the PR — house rule; do not push main / open PR).

## Self-review notes
- Spec coverage: C0(engine)→D2/D3/D4; C1(worker)→execution+opaque errors+shared resolve; C2(validation)→D1 save-time; C3(frontend); C4(deps+sweep). ✓
- All literal API calls verified against go.starlark.net by the design-validation agent (ExecFileOptions, SetMaxExecutionSteps, Cancel, json.Module, string(s)). ✓
- Residual OOM risk: documented in spec D4, NOT claimed fixed; wall-time conservative via worker CallTimeout. ✓
