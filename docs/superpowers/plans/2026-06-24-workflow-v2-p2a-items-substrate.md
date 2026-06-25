# Workflow v2 — Phase P2a: items substrate (migration-runner hardening + node_outputs.items dual-write + loadInputs) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lay the data-layer foundation for the items model WITHOUT the expression engine: (1) harden `storage.Migrate` into a versioned, transactional, advisory-locked runner that can run **Go-coded** migration steps idempotently; (2) add `node_outputs.items JSONB` via a Go-coded, format-aware backfill (`m21`); (3) make built-in + custom executors emit **type-aware** `node_outputs.items` while STILL writing legacy `content`/`format` + `scripts`/`shots` (dual-write window); (4) add `loadInputs(ctx, todoID)` that reads upstream `items` with a fallback to the `scripts`/`shots`/`output_ref` projection — exercised by tests, not yet load-bearing.

**Architecture:** The migration runner gains a `schema_migrations(version TEXT PRIMARY KEY, applied_at timestamptz)` table and a parallel registry of **Go-coded** steps (`migrationStep{version, run func(tx) error}`). Legacy idempotent DDL (`m1…m19`) keeps running unconditionally each boot (self-skipping via `IF NOT EXISTS`); the new Go steps consult `schema_migrations`, run once each inside an explicit transaction, and record themselves. The whole sequence runs under a boot-time `pg_advisory_lock` so concurrent `studiod` replicas serialize. `m21` adds `node_outputs.items` and backfills format-aware (`json`→`[{json:content::jsonb}]` with a Go-coded parse-error fallback; `text`/`http-status`→`[{json:{text}}]`). A new `items.go` defines the `Item` wire type (`{json, binary}`) shared by emission + `loadInputs`. Each executor gains an item-emission INSERT (net-new for script/storyboard; in-statement column add for prescreen + the 3 custom executors). `loadInputs` reads each dependency todo's `node_outputs.items`, falling back to a `scripts`/`shots`/`output_ref`-derived item when empty.

**Tech Stack:** Go (pgx/pgxpool for the runner; GORM `*gorm.DB` for executors — matching existing code), `encoding/json`, table + DB-backed tests (`-p 1`, fresh DB via `internal/dbtest`). No frontend, no expr engine, no new third-party deps.

> All `go` commands use `GOWORK=off`. DB-backed tests need a FRESH Postgres DB each run, serialized with `-p 1`, at `172.17.0.3:5432` (`postgres`/`pw`). The worker package self-provisions a fresh DB in `TestMain` from `LLM_AGENT_STUDIO_PG_URL`; the storage package derives fresh DBs per-test. Run with e.g.
> `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p2a_$RANDOM?sslmode=disable GOWORK=off go test ./internal/storage/... ./internal/worker/... -count=1 -p 1`
> Never reuse a DB (dirty-data false failures bite partial-unique indexes like `assets_todo_uniq`).

---

## Prerequisites (hard blockers)

- **On `main`, which already contains P1** (`internal/nodedesc` + `GET /api/orgs/{org}/node-types` + `<PropertiesForm>`, all merged). Branch off `main`: `git switch -c feat/workflow-v2-p2a-items` off main (or use a worktree).
- **Confirm the baseline before Task 1:** `internal/storage/storage.go` `Migrate` is the flat `[]string` loop through `m1Migrations…m19Migrations` (no version table); `node_outputs` is `content TEXT + format TEXT` (DDL at `m18Migrations`); `internal/worker/worker.go` has `runScript`(~405)/`runStoryboard`(~476)/`runPrescreen`(~1006)/`runCustomLLM`(~1707)/`runCustomHTTP`(~1761)/`runCustomScript`(~1876), and `loadInputs` does NOT exist. If any differ (e.g. another phase landed first), STOP and re-ground.
- **HIGHEST RISK — the migration-runner change (Task 1).** It touches the boot path of every `studiod` and every DB test in the repo. You MUST test the **existing-DB upgrade path**, not just fresh-DB: a DB already migrated through `m19` under the OLD runner must, on first run of the NEW runner, gain `schema_migrations`, run `m21` exactly once, and be re-runnable (second `Migrate` is a no-op). Task 1 + Task 3 include explicit "simulate a pre-P2a DB" tests for this. Do not collapse them into a fresh-DB-only assertion.
- **Scope discipline (do NOT do in P2a):** no expr engine; no change to `substituteVars`/`varBindings`/`resolveVariables`/`resolveOutputText` behavior; do not delete `output_ref`/`content`/`format`; do not demote the planner; do not touch project-config reads (`pictureBookConfig`, B1 project-style). `loadInputs` is added + tested but NOT routed as the sole execution channel.
- House rules: studio changes go branch→push→PR→rebase-merge (no direct push to `main`; no CI/auto-merge on studio). Do not open the PR yourself — hand off via `superpowers:finishing-a-development-branch`.

## File Structure

**Backend (Go) — storage:**
- Modify `internal/storage/storage.go` — add `schema_migrations` DDL to the legacy list; add `migrationStep` type + a `goSteps()` registry (initially `m21`); rewrite `Migrate` to (a) take `pg_advisory_lock`, (b) run legacy DDL, (c) run each Go step once under a transaction guarded by `schema_migrations`. Add `m21` as a Go-coded, format-aware backfill.
- Modify `internal/storage/storage_test.go` — add runner-hardening tests (version table, idempotent re-run, existing-DB upgrade simulation, m21 format-aware backfill).

**Backend (Go) — worker:**
- Create `internal/worker/items.go` — the `Item` wire type (`{json json.RawMessage, binary map[string]BinaryRef}`) + helpers `itemsJSON(items []Item) ([]byte, error)` and `textItem`/`jsonItem` constructors. Leaf within the worker package (stdlib-only imports).
- Create `internal/worker/items_test.go` — JSON-shape table tests for `Item` (no DB).
- Modify `internal/worker/worker.go`:
  - `runScript` (~443): after the `scripts` INSERT, net-new INSERT of `[{json: ScriptOutput}]` into `node_outputs.items`.
  - `runStoryboard` (~568, inside `tx`): net-new INSERT of one item per shot `[{json: Shot}, …]`.
  - `runPrescreen` (~1045): extend the existing INSERT to also write `items = [{json: ReviewOutput}]`.
  - `runCustomLLM`/`runCustomHTTP`/`runCustomScript` (~1748/~1864/~1906): extend each INSERT to also write `items` — `{json: <parsed>}` for `format='json'`, `{json:{text: content}}` for text/http-status.
  - Add `loadInputs(ctx, todoID) ([]Item, error)`.
- Modify `internal/worker/worker_items_test.go` (new file) — DB-backed emission + `loadInputs`-fallback tests.

> NOTE: the `node_outputs` table gains its `items` column via `m21` (Task 3), so every worker emission test depends on Task 3 having landed (the fresh-DB helper runs the full migration chain). Leaf-first ordering below enforces this.

---

## Task summaries (full per-task TDD steps with verbatim code are dispatched to implementers from this plan + the planning transcript)

### Task 1: harden the migration runner — `schema_migrations` + transactions + advisory lock + Go-step registry (★M-1)
Files: `internal/storage/storage.go`, `internal/storage/storage_test.go`. Add the runner machinery with an EMPTY Go-step registry (m21 lands in Task 3). The legacy DDL path must be preserved byte-for-byte; the version table must appear without breaking any existing DB. Tests: `TestMigrateCreatesSchemaMigrationsTable`, `TestMigrateLegacyDDLStillApplied`, `TestGoStepRunsOnceAndIsRecorded` (test-injected Go step via overridable `st.testGoSteps`). `Migrate` rewritten to: acquire a connection, take `pg_advisory_lock(hashtext("studio_schema_migrate"))` (same idiom as worker.go:1279), run legacy DDL (schema_migrations DDL first), then for each Go step: skip if recorded, else run in an explicit tx + record version + commit.

### Task 2: `Item` wire type + JSON helpers (no DB)
Files: `internal/worker/items.go`, `internal/worker/items_test.go`. `Item{JSON json.RawMessage, Binary map[string]BinaryRef}` mirrors n8n INodeExecutionData (§3.2). NO `pairedItem` (★D-3). `json` is the canonical structured object, NEVER `{text:"<json string>"}` for structured output (★D-6). `BinaryRef{AssetID,MimeType,Kind,Status}` (§3.3 + ★D-4 status) — P2a never emits binary; type exists so loadInputs round-trips it. Helpers: `jsonItem(payload)`, `textItem(text)` → `{json:{text}}`, `itemsJSON(items)` → JSON array, never null (★D-5).

### Task 3: `m21` — `node_outputs.items` column + format-aware Go-coded backfill (★M-2, ★D-5)
Files: `internal/storage/storage.go` (register `m21` in `goSteps()`), `internal/storage/storage_test.go`. DDL: `ALTER TABLE node_outputs ADD COLUMN IF NOT EXISTS items JSONB NOT NULL DEFAULT '[]'`. Go-coded backfill (parse can fail → must be Go): `format='json'` valid → `[{json: content::jsonb}]`, invalid → `[{json:{text:content,_parseError:true}}]` (never half-fail); `text`/`http-status` → `[{json:{text:content}}]`. Backfill only rows still at `items='[]'::jsonb` (re-run safe). Test `TestM21AddsItemsColumnAndBackfillsFormatAware` seeds legacy rows (existing-DB upgrade simulation) then asserts all three branches + idempotent re-run.

### Task 4: built-in executors emit typed `node_outputs.items` (★B2/D-6) — `runScript` + `runStoryboard`
Files: `internal/worker/worker.go`, `internal/worker/worker_items_test.go` (new). Net-new emission (neither writes node_outputs today). `runScript` → `emitItems([{json: ScriptOutput}])` after the scripts INSERT (characterSheet reachable). `runStoryboard` → `emitItemsTx(tx, [{json: Shot}] per shot)` INSIDE the existing tx, after the per-shot loop, BEFORE `return nil` (skipped on the `earlyExisting` re-run path). Helpers `emitItems`/`emitItemsTx` INSERT a node_outputs row with `content=''`, `format='items'`, `items=payload`. Do NOT change return values (`script:<id>`/`shots:<scriptID>`) or legacy writes. Tests assert legacy scripts/shots rows preserved AND typed items emitted.

### Task 5: prescreen + custom executors dual-write `items` (★B2/D-6)
Files: `internal/worker/worker.go` (`runPrescreen` ~1045, `runCustomLLM` ~1748, `runCustomHTTP` ~1864, `runCustomScript` ~1906), `internal/worker/worker_items_test.go`. These already write `node_outputs(content,format)` — add `items` to the SAME INSERT (true dual-write). prescreen → `[{json: ReviewOutput}]`. Custom: shared helper `itemsForContent(content, format)` = `format='json'`&&`json.Valid` → `[jsonItem(content)]` else `[textItem(content)]` (mirrors m21 so live + backfilled rows are shape-identical). runCustomHTTP's `{status}`-only secret guard flows through unchanged (★S-4 preserved within additive scope — do NOT special-case/widen). Tests assert legacy content/format preserved AND typed items.

### Task 6: `loadInputs(ctx, todoID)` with scripts/shots/output_ref fallback (★M-4)
Files: `internal/worker/worker.go`, `internal/worker/worker_items_test.go`. `loadInputs` reads each dep todo's newest `node_outputs.items`; when empty (straddling-deploy run under old code) falls back to projecting `output_ref`: `script:` → `scripts.content_json` as one item; `shots:` → one Shot item per shots row; `custom:` → `textItem(resolveOutputText(ref))`; `asset:`/empty → nil (binary consumption is post-P2a). **ADDITIVE** — execution NOT routed through it; existing depends_on/output_ref/resolveVariables resolution stays live. Tests: `TestLoadInputsReadsItems` + `TestLoadInputsFallsBackToScriptProjection` (asserts legacy script's characterSheet reachable via fallback). Match the existing `depends_on TEXT[]` scan idiom (grep — don't assume `lib/pq`). `resolveOutputText` reused read-only.

### Task 7: full-suite regression + hand-off
Verification only. `GOWORK=off go build ./... && go vet ./...`; full DB-backed pass on a fresh DB `-p 1` over `./internal/storage/... ./internal/worker/...`; confirm the existing-DB-upgrade tests (`TestM21...`, `TestGoStepRunsOnce...`) green (the highest-risk gate). Hand off via `superpowers:finishing-a-development-branch` (do not push main / open PR yourself).

---

## Stated ambiguity-calls (from planning)

- **A1 — Legacy `m1…m19` are NOT retro-registered as tracked versions.** Legacy idempotent DDL keeps running unconditionally every boot (current contract; `IF NOT EXISTS` self-skip is harmless); only the new Go steps (`m21`, future `m20`) are version-tracked. Avoids inventing 19 version names + a fragile "mark applied" heuristic (CLAUDE.md §2). Strictly safer + minimal; no regression (Migrate already re-runs every boot).
- **A2 — items-only rows use `format='items'` sentinel** (`content=''`). Distinguishes net-new script/storyboard items rows from legacy text/json rows; never matched by `output_ref`/`resolveOutputText` (which only read `custom:`). Keeps the NOT-NULL column happy.
- **A3 — `loadInputs` flattens all deps' items into one slice** (dependency order). No consumer needs per-node addressing yet (that's `$node["X"]` in P2b's expr engine). Avoids over-building.
- **A4 — runCustomHTTP `{status}`-only secret guard flows through items unchanged.** `itemsForContent` wraps whatever `content` the executor already landed; the secret-body guard already reduced `content` to `{"status":N}` before the INSERT, so the items payload inherits the restriction automatically. No separate items-side guard in P2a (★S-4's full treatment is a P3 security-reviewed concern). Flagged so an implementer doesn't "helpfully" widen it.

## Self-Review (writing-plans)

Spec coverage: ★M-1 (Task 1, incl. existing-DB upgrade tested), ★M-2 (Task 3, Go-coded format-aware + `_parseError` fallback), ★M-4 (Task 6, additive fallback), ★D-5 (NOT NULL DEFAULT '[]' + `[]byte`→RawMessage reads + in-statement writes), ★D-6 (typed emission, never text-wrap structured). Dual-write preserved (every executor still writes legacy content/format + scripts/shots + unchanged output_ref; tests assert). Out-of-scope (expr engine, substituteVars, output_ref deletion, planner, project-config) explicitly NOT touched. Leaf-first: runner → Item type → m21 → emission → loadInputs → regression.

Read-before-edit grounding points (not placeholders): `claimed.typ` field (used worker.go:1047); runCustomHTTP content/format locals (read ~1820-1870); `depends_on TEXT[]` scan idiom (match existing reader). Typed payloads: `agents.ScriptOutput` (script.go:37), `agents.Shot` (storyboard.go:28), `agents.ReviewOutput` (review.go:32). Test helpers: `assetTestPool`/`assetTestGorm` (asset_test.go), `TestMain`→`dbtest.CreateFresh`.
