# Phase P3 — expr engine cut-over (legacy `substituteVars` → live `internal/expr`)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`. No production code before a red test. Security-critical — the irreversible sub-phase (P3e) has a ⛔ MERGE GATE.

**Goal:** Make the `internal/expr` engine the LIVE substitution channel for custom nodes (llm/http/script), replacing the legacy `substituteVars`/`resolveVariables` `{{name}}` channel, with whole-output `$node` semantics — reversible until one isolated irreversible step.

**Architecture:** Five reversible-until-the-gate sub-phases: a real-`$node` shadow probe (P3b) → planner emits the `$node` form (P3c) → worker live-read behind a new `ExprChannel` flag, legacy kept as the `else` (P3d) → ⛔ additive `m22` migration + flip the flag (P3e, irreversible, gated) → cleanup keeping shared seams (P3f). Two-round-reviewed (design + adversarial); all 6 load-bearing claims confirmed.

**Tech stack:** Go stdlib; `internal/expr` UNCHANGED (Q1=A', no engine edit). DB tests fresh PG `-p 1`.

---

## Ratified decisions

- **Q1 = A' (format-dependent rewrite, NO engine change).** Rewrite `{{name}}` → `{{ $node["<sourceTodoId>"].json.text }}` when the dep's output is **text**-format, and `{{ $node["<sourceTodoId>"].json }}` when **json/script**-format. The var NAME is only a lookup key into the binding's `sourceTodoId` (never appears in the expression — non-identifier names are a non-issue). Key-reorder + whitespace differences for object outputs are **accepted as known, semantically-equal, probe-measured** divergences (review H-3/M-1).
- **Whole-output semantics.** No field-level `$node.field` authoring. The authoring surface stays `{{name}}`; `.json.text`/`.json` is an author-invisible mechanical rewrite keyed on the dep's format.
- **New `Config.ExprChannel bool` (default false).** Distinct from `ExprParity` (the probe). `ExprParity` = probe runs + logs. `ExprChannel` = which result feeds downstream. Flip is one bool, reversible even after m22 (m22 is additive).

## Confirmed scope simplifications (verified, review Claims 4/5)

- **B4 is OFF P3's critical path.** The LLM planner (`planner.go:52`, whitelist `builtinnode.Types()` = script/storyboard/asset/prescreen) emits **no** custom (`llm`/`http`/`script`-kind) nodes; the legacy substitution channel is reached ONLY via `PlanCustom` typed custom nodes. P3 leaves `runScript`/`runStoryboard`/`runPrescreen` and the worker's project-config reads (`pictureBookConfig`/`style`) UNTOUCHED — so the B4 ordering rule does not bind here.
- **m22 migrates ONLY `todos.input_json`** (per-run rows carrying resolved `sourceTodoId`). Saved `projects.workflow_nodes`/`workflows.nodes` templates are re-planned each run by `PlanCustom`, so the P3c planner change covers them; NO destructive JSONB rewrite of saved templates. Verified: no execution/console/SSE path reads `{{name}}` out of stored templates — the worker reads only `todos.input_json` (`worker.go:1704`).

## Review fixes folded in (must follow)

- **C-1 (CRITICAL):** the shipped probe (`exprParityCheck`, expr_resolver.go) proves only `$json`-string-templating equivalence over a SYNTHETIC self-item — it NEVER exercises `$node`/`NodeByID`/stored items. "Probe green ⇒ safe to flip" is INVALID. **P3b adds a real-`$node` shadow probe; the P3e flip gates on ITS evidence, not the existing probe.**
- **H-1:** `resolveOutputText` is shared by `runPrescreen` (worker.go:1071) + storyboard script-load — P3f cleanup deletes `substituteVars` + the `resolveVariables`→`substituteVars` call-sites ONLY; `resolveOutputText` SURVIVES.
- **H-2:** m22 (migrate) and the flag flip must land in the SAME deploy (migrate-then-flip), or the worker must tolerate both dialects during the window — else a straddling deploy feeds an un-rewritten `{{name}}` to expr (missing-field → hard error).
- **H-3/M-1:** decode→re-marshal of object outputs reorders keys / normalizes whitespace. Accepted under Q1=A'. The shadow probe MUST classify these as benign (semantic-equal) vs real divergence.
- **S-2/S-3 carry-forward:** the LIVE path MUST route `$node` through the existing `exprNodeResolver` (direct-depends_on, project-scoped, fail-closed — expr_resolver.go:73-103), NOT a fresh unscoped `expr.Context`. The HTTP `{{secret:}}` pre-pass + `{status}`/SSRF guards are untouched (expr replaces only the second `substituteVars(val,nameVals)` pass).

---

## Sub-phase sequence

| Sub-phase | Scope | Reversible? | Irreversible gate? |
|---|---|---|---|
| **P3b** | Real-`$node` SHADOW probe: emit the Q1=A' rewrite, resolve via live `exprNodeResolver`, compare per-variable to legacy `resolveOutputText` whole-output; classify benign-reorder vs real divergence; log metadata only. LLM+HTTP+script. | Yes (probe only) | No |
| **P3c** | Teach `PlanCustom` (planner.go:376-412) to ALSO emit the `$node` rewrite form into `todos.input_json` (additive new field; legacy retained). New runs carry both. | Yes (additive) | No |
| **P3d** | Worker live-read behind `ExprChannel` (default false): each custom executor `if ExprChannel { expr.Resolve(rewritten, exprNodeResolver) } else { legacy }`. Script resolves globals via expr (name-keyed map preserved). | Yes (flag off ⇒ legacy live) | No |
| **P3e** ⛔ | (1) `m22` additive migration backfilling `todos.input_json` pending/historical rows with the rewrite (marker `params._exprRewritten`, detect-already-expr guard, legacy bytes retained). (2) Flip `ExprChannel` default true. **Same deploy (H-2).** | Migration: NO. Flag: YES (revert to false). | **YES — user approval + independent security review** |
| **P3f** | Cleanup after a soak release: delete `substituteVars` + the `else` legacy call-sites (KEEP `resolveOutputText` — H-1). Optional: drop legacy `input_json` fields. | No (separate gated PR) | Soft gate |

---

## Task P3b — real-`$node` shadow probe

**Files:** Modify `internal/worker/expr_resolver.go` (+ `$node`-channel probe fn), `internal/worker/worker.go` (call it from the 3 custom executors under `ExprParity`); Test `internal/worker/worker_expr_nodeprobe_test.go`.

**Design:** For each `customVariable{Name, SourceTodoId}` of the executing custom node:
1. Determine the dep's output format (`SELECT format FROM node_outputs WHERE todo_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 1`; fall back via `output_ref` prefix: `script:`→json-ish, `custom:`→read its format). `text`→accessor `.json.text`; else `.json`.
2. Build `tpl = "{{ $node[\"" + SourceTodoId + "\"]" + accessor + " }}"`.
3. `exprVal, exprErr := expr.Resolve(tpl, w.exprNodeResolver(ctx, c, nil))` — exercises the LIVE `$node`/`NodeByID`/`itemsForDep` path (S-2 enforced).
4. `legacyVal, legacyErr := w.resolveOutputText(ctx, depOutputRef)` (the whole-output legacy value; depOutputRef from `SELECT output_ref FROM todos WHERE id=$1 AND project_id=$2`).
5. Classify: `exact` (exprVal==legacyVal), `benign` (both decode to equal JSON via `reflect.DeepEqual` of `json.Unmarshal` — the H-3 key-reorder/whitespace case), or `divergent` (neither, or one errored). 
6. Log metadata ONLY (F4): `{todo_id, var_name_hash OR index, class, len_legacy, len_expr, expr_err_bool, legacy_err_bool}` — NEVER the resolved values.

Gate the whole thing on `w.cfg.ExprParity`. NEVER feed downstream.

- [ ] **Step 1 — failing test.** `internal/worker/worker_expr_nodeprobe_test.go` (DB, fresh PG `-p 1`, skip if `LLM_AGENT_STUDIO_PG_URL` unset). Seed a project + a dep todo with a `node_outputs` row (one text-format dep `[{json:{text:"hello"}}]` + output_ref `custom:<id>` with content `"hello"`; one json-format dep). Seed a custom consumer todo depending on them with `params.variables=[{name,sourceTodoId}]`. Build worker with `ExprParity:true` + captured slog. Call the probe (directly or via `runCustomLLM` with a scripted model). Assert: log lines carry `class=exact` for the text dep (`.json.text` == `"hello"` == legacy content) and `class=benign|exact` for the json dep; NO resolved value substring in the log; the probe routes `$node` through `exprNodeResolver` (an out-of-deps `sourceTodoId` ⇒ `class=divergent` + `expr_err_bool=true`, fail-closed). Run → RED.
- [ ] **Step 2 — implement** the probe fn + wire calls (gated on `ExprParity`) in `runCustomLLM`/`runCustomHTTP`/`runCustomScript` AFTER their existing `resolveVariables`. Reuse `exprNodeResolver`; do NOT add an unscoped resolver.
- [ ] **Step 3 — green.** `GOWORK=off go test ./internal/worker/ -run NodeProbe -count=1 -p 1 -v` → PASS.
- [ ] **Step 4 — flag-off regression.** Full worker suite fresh DB `-p 1` → `ok` (probe only runs under `ExprParity`, default false → zero behavior change). `go vet`/`go build ./...` clean. Leaf check: `internal/expr` still imports no starlark/goja/worker.
- [ ] **Step 5 — commit** `feat(worker): P3b — real-$node shadow probe (Q1=A' rewrite, exprNodeResolver live path, benign-reorder classified, metadata-only log)`.

## Tasks P3c–P3f

Detailed per-sub-phase as each is reached (outlines above are the contract). P3e is the ⛔ irreversible gate: do NOT execute its migration/flip without explicit user approval + an independent security review confirming S-2/S-3 hold on the LIVE path (executor-level fail-closed tests for all 3 kinds; secret-literal survival; `{status}`/SSRF guards under `ExprChannel=true`; m22 idempotency/marker/don't-clobber on a fresh DB).

## Open questions (carried, ratify before P3e)

- Missing-upstream behavior: expr errors on a missing field/empty-items dep (fail-closed); legacy leaves `{{name}}` literal. Confirm a missing dep becoming a run failure is acceptable (likely yes).
- Production soak: require the P3b shadow probe to run in PROD with a measured benign/exact rate (no `divergent`) before the P3e flip.
