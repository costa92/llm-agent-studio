# Phase P3 — expr engine cut-over (legacy `substituteVars` → live `internal/expr`)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`. No production code before a red test. Security-sensitive — the flag flip (P3e) is gated on user approval + independent security review + prod soak evidence (but is REVERSIBLE — there is NO irreversible step).

**Goal:** Make the `internal/expr` engine the LIVE upstream-value resolution path for custom nodes (llm/http/script), replacing the un-scoped `resolveVariables`/`resolveOutputText` value seam, with whole-output `$node` semantics — gaining S-2 cross-tenant safety. Fully reversible (single flag).

**Architecture (REVISED — Approach R2, third-round review):** The rewrite happens at RUN TIME in the worker, NOT at plan time + migration. Under a new `ExprChannel` flag, each custom executor resolves each variable's VALUE via `expr.Resolve("{{ $node[\"<sourceTodoId>\"]<accessor> }}", exprNodeResolver)` (the P3b rewrite logic, promoted from shadow to live) to build the same `name→value` map, then keeps `substituteVars(prompt, map)` for interpolation. This swaps ONLY the value source (un-scoped `resolveOutputText` → project-scoped fail-closed `exprNodeResolver`); templating is unchanged. **No planner change, NO m22 migration, NO irreversible step** — the flip is one reversible bool. Sub-phases: real-`$node` shadow probe (P3b, done) → worker R2 seam behind `ExprChannel` default-false (P3d) → flip to default-true after soak (P3e, gated but reversible) → cleanup (P3f). Three-round-reviewed (design + adversarial + plan-vs-run fork).

**Tech stack:** Go stdlib; `internal/expr` UNCHANGED (Q1=A', no engine edit). DB tests fresh PG `-p 1`.

---

## Ratified decisions

- **Q1 = A' (format-dependent rewrite, NO engine change).** Rewrite `{{name}}` → `{{ $node["<sourceTodoId>"].json.text }}` when the dep's output is **text**-format, and `{{ $node["<sourceTodoId>"].json }}` when **json/script**-format. The var NAME is only a lookup key into the binding's `sourceTodoId` (never appears in the expression — non-identifier names are a non-issue). Key-reorder + whitespace differences for object outputs are **accepted as known, semantically-equal, probe-measured** divergences (review H-3/M-1).
- **Whole-output semantics.** No field-level `$node.field` authoring. The authoring surface stays `{{name}}`; `.json.text`/`.json` is an author-invisible mechanical rewrite keyed on the dep's format.
- **★ R2 (run-time value-resolution swap, NO migration — third-round review).** The cut-over replaces ONLY the value source: legacy `resolveVariables`→`resolveOutputText` (bare un-scoped `WHERE id=$1`) → per-variable `expr.Resolve("{{ $node[\"<sourceTodoId>\"]<accessor> }}", w.exprNodeResolver(ctx,c,nil))` (project-scoped, fail-closed). `substituteVars(prompt, map)` interpolation is UNCHANGED. Rejected **R1** (rewrite whole prompt then `expr.Resolve` it) — `expr.Resolve` parses EVERY non-`secret:` `{{…}}` span and hard-fails the run on a parse error, so a prompt with legitimate literal braces (JSON examples, `{{ role }}`) would regress; `substituteVars` only touches BOUND names and leaves other braces literal. R2 only ever parses the machine-generated `$node` template (never author text) → no such failure. Rejected **Approach P** (plan-time rewrite + m22): irreversible migration + H-2 race + declared-vs-actual-format parity gap, for a benefit (stored expr templates) that is cosmetic on throwaway per-run rows this phase.
- **New `Config.ExprChannel bool` (default false).** Distinct from `ExprParity` (the probe). `ExprParity` = probe runs + logs. `ExprChannel` = whether the expr-resolved value feeds downstream. Flip is one bool, FULLY reversible (no migration to undo).

## Confirmed scope simplifications (verified, review Claims 4/5 + R2 fork)

- **B4 is OFF P3's critical path.** The LLM planner (`planner.go:52`, whitelist `builtinnode.Types()` = script/storyboard/asset/prescreen) emits **no** custom (`llm`/`http`/`script`-kind) nodes; the legacy substitution channel is reached ONLY via `PlanCustom` typed custom nodes. P3 leaves `runScript`/`runStoryboard`/`runPrescreen` and the worker's project-config reads (`pictureBookConfig`/`style`) UNTOUCHED — so the B4 ordering rule does not bind here.
- **NO migration (R2).** The rewrite is computed at run time from the legacy `{{name}}`+`variables` always present in `todos.input_json` (the executors already unmarshal them — worker.go:1761/1827/1955). `PlanCustom` is UNCHANGED (no P3c). The accessor comes from the dep's ACTUAL stored `node_outputs.format` (tighter parity than the declared `outputFormat`, which can lie when a model returns text for a json-declared node). No `m22`, no idempotency marker, no straddling-deploy ordering.

## Review fixes folded in (must follow)

- **C-1 (CRITICAL):** the shipped probe (`exprParityCheck`, expr_resolver.go) proves only `$json`-string-templating equivalence over a SYNTHETIC self-item — it NEVER exercises `$node`/`NodeByID`/stored items. "Probe green ⇒ safe to flip" is INVALID. **P3b adds a real-`$node` shadow probe; the P3e flip gates on ITS evidence, not the existing probe.**
- **H-1:** `resolveOutputText` is shared by `runPrescreen` (worker.go:1071) + storyboard script-load — P3f cleanup deletes `substituteVars` + the `resolveVariables`→`substituteVars` call-sites ONLY; `resolveOutputText` SURVIVES.
- **H-2 — STRUCK (R2 eliminates it).** With no migration there is no migrate↔flip ordering and no straddling-deploy missing-field window: every run reads the legacy `variables` from `input_json` and rewrites in-process, so old and new workers both work regardless of flag state.
- **H-3/M-1:** decode→re-marshal of object outputs reorders keys / normalizes whitespace. Accepted under Q1=A'. The shadow probe MUST classify these as benign (semantic-equal) vs real divergence.
- **S-2/S-3 carry-forward:** the LIVE path MUST route `$node` through the existing `exprNodeResolver` (direct-depends_on, project-scoped, fail-closed — expr_resolver.go:73-103), NOT a fresh unscoped `expr.Context`. The HTTP `{{secret:}}` pre-pass + `{status}`/SSRF guards are untouched (expr replaces only the second `substituteVars(val,nameVals)` pass).

---

## Sub-phase sequence

| Sub-phase | Scope | Reversible? | Irreversible gate? |
|---|---|---|---|
| **P3b** ✅ | Real-`$node` SHADOW probe: emit the Q1=A' rewrite, resolve via live `exprNodeResolver`, compare per-variable to legacy `resolveOutputText` whole-output; classify benign-reorder vs real divergence; log metadata only. LLM+HTTP+script. DONE (commit 1db8929). | Yes (probe only) | No |
| ~~P3c~~ | **DROPPED (R2).** No planner change; the rewrite is computed at run time, not stored. | — | — |
| **P3d** | Worker R2 seam behind `ExprChannel` (default false): each custom executor builds the `name→value` map by resolving each var via `expr.Resolve("{{ $node[...]<accessor> }}", exprNodeResolver)` when `ExprChannel`, else legacy `resolveVariables`; then `substituteVars(prompt, map)` UNCHANGED. Script resolves its name-keyed globals the same way. Reuses P3b's `exprNodeAccessor`. | Yes (flag off ⇒ legacy live) | No |
| **P3e** | Flip `ExprChannel` default true after a prod soak with the P3b shadow probe showing **zero `divergent`** + missing-upstream behavior ratified. NO migration. | **YES — flag revert to false** | Gated on user approval + independent security review + soak (but REVERSIBLE) |
| **P3f** | Cleanup after a soak release: delete `substituteVars`'s legacy `resolveVariables` value-source path + the `else` branches (KEEP `resolveOutputText` — H-1, still used by prescreen/storyboard; KEEP `substituteVars` itself — still the interpolator). | No (separate gated PR) | Soft gate |

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

## Task P3d — worker R2 seam behind `ExprChannel` (default false)

**Files:** `internal/worker/worker.go` (+`Config.ExprChannel bool`; a new `resolveVariablesExpr` value-resolver; branch in `runCustomLLM`/`runCustomHTTP`/`runCustomScript`); reuse `exprNodeAccessor`/`exprNodeResolver` (expr_resolver.go). Test `internal/worker/worker_expr_channel_test.go`.

**Design:** Add `func (w *Worker) resolveVariablesExpr(ctx, c claimed, vars []customVariable) (map[string]string, error)` mirroring `resolveVariables` but resolving each var's value via the LIVE expr path:
```
for each v with v.SourceTodoId != "":
    accessor := w.exprNodeAccessor(ctx, c.projectID, v.SourceTodoId)   // P3b helper: .json.text|.json
    val, err := expr.Resolve(`{{ $node["`+v.SourceTodoId+`"]`+accessor+` }}`, w.exprNodeResolver(ctx, c, nil))
    if err != nil { return nil, err }   // fail-closed: a missing/cross-project/empty dep fails the run (the S-2 win)
    out[v.Name] = val
```
Each custom executor picks the resolver by flag: `replacer := resolveVariables(...)` becomes `if w.cfg.ExprChannel { resolveVariablesExpr(ctx,c,in.Variables) } else { resolveVariables(ctx,in.Variables) }`. Everything downstream (`substituteVars`, the HTTP secret pre-pass, `{status}`/SSRF guards, script globals) is UNCHANGED — only the value SOURCE swaps. NOTE the HTTP `{{name}}` channel is the SECOND pass (after the secret pass); the flag swaps only the `nameVals` source, never the secret pre-pass.

- [ ] **Step 1 — failing tests** (`worker_expr_channel_test.go`, DB fresh `-p 1`). With `ExprChannel:true`: (a) a custom node whose `{{draft}}` var points at a text dep resolves to the dep's content via `$node` (byte-equal to legacy for text; semantic-equal for json) and the prompt interpolates correctly; (b) **S-2 executor fail-closed**: a var whose `sourceTodoId` is NOT in the node's `depends_on`, and one in another project, make the run FAIL with an OPAQUE error (no denial detail / no cross-project data); (c) **secret survival**: an HTTP node with `{{secret:K}}` header + a `{{name}}` var — secret still resolves via the untouched pre-pass, `{{name}}` via expr, and a `{{secret:X}}` arriving through a `$node` value stays literal; (d) `{status}`-only + URL `{{`-residue guards still fire under the flag. Run → RED.
- [ ] **Step 2 — implement** `Config.ExprChannel` + `resolveVariablesExpr` + the flag branch in all 3 executors.
- [ ] **Step 3 — green** + **flag-off regression**: full worker suite fresh DB `-p 1` → `ok` (ExprChannel default false ⇒ zero behavior change). `go vet`/`build` clean; `internal/expr` leaf-clean.
- [ ] **Step 4 — commit** `feat(worker): P3d — ExprChannel R2 value-resolution seam (expr $node via exprNodeResolver, substituteVars unchanged, default off)`.

## P3e — flip (gated, REVERSIBLE) & P3f — cleanup

P3e flips `ExprChannel` default→true. NO migration. Gate: user approval + independent security review (executor-level S-2 fail-closed for all 3 kinds; secret-literal survival; `{status}`/SSRF under the flag) + a prod soak where the P3b shadow probe shows **zero `divergent`**. Reversible (flip back to false). P3f deletes the legacy `resolveVariables` value-source path + `else` branches after a soak release (KEEP `resolveOutputText` — H-1 — and `substituteVars` the interpolator).

## Open questions (ratify before P3e flip)

- **Missing-upstream behavior change:** under `ExprChannel`, a missing-field/empty-items/cross-project dep fail-closes → the run FAILS (the inherent cost of the S-2 win); legacy left `{{name}}` literal or returned text. Confirm a missing/denied dep becoming a run failure is acceptable (likely yes — it's a real wiring/permission error).
- **Production soak:** require the P3b shadow probe to run in PROD with a measured benign/exact rate (zero `divergent`) before the P3e flip.
