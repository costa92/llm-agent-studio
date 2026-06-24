# Custom Nodes Phase 2A Implementation Plan

**For agentic workers: READ AND FOLLOW THE REQUIRED SUB-SKILL `superpowers:executing-plans` BEFORE STARTING. Execute tasks in order; one commit per task; run the verification command and confirm its output before moving on.**

## Goal

Make `custom:<slug>` workflow nodes **executable** (Phase 2 子项目 A) via a **kinds + instances** model. Users create org-scoped "typed" custom node types in a registry (binding kind=`llm` + Rich params). Typed nodes (carrying a `typeId`) run through a generic `runCustom` worker dispatch that resolves variables from upstream text outputs, calls the routed chat model, and lands results in a new `node_outputs` table. Annotation custom nodes (no `typeId`, Phase 1) stay non-runnable; the run gate flips from "any custom node → 400" to "any UNBOUND custom node → 400".

Authoritative design: `docs/superpowers/specs/2026-06-23-custom-nodes-phase2a-execution-design.md`. This plan turns it into executable tasks; it does NOT redesign.

## Architecture

- **Two coexisting node concepts**, discriminated by an explicit `typeId` field on the node JSON:
  - **Annotation** (Phase 1): node JSON has `label`+`color`, no `typeId`, per-workflow, non-runnable.
  - **Typed** (new): node JSON has `typeId` referencing an org registry entry (kind+params) + cached `label`/`color`; runnable.
- **Org-scoped resolution happens in the RUN HANDLER** (which holds `p.OrgID`), NOT the planner. The handler reads the registry `WHERE id=$1 AND org_id=$2`, builds a `map[nodeID]planner.ResolvedType{Kind, Params}`, and passes it into `PlanCustom`. The planner stays store-thin (no registry store, no org context). **Do NOT** replicate the existing unscoped `SELECT content FROM prompts WHERE id=$1` (planner.go:277).
- **Variable BINDINGS live on the NODE, not the registry.** A `custom_node_types` registry entry is org-level and reused across many workflows / placed nodes; a variable's `sourceNodeId` is a workflow-LOCAL node id, so it CANNOT live in registry `params`. Two layers:
  - **Registry `params` (org-level, `LlmParams`):** `{ systemPrompt?, userPrompt, model?, temperature?, outputFormat? }`. NO `variables`. Variable NAMES are *implicit* in the `{{name}}` template (the template is the single source of names).
  - **Node instance (per-node, in workflow JSON):** a `varBindings: [{ name, sourceNodeId }]` carry field, riding the same raw-JSONB passthrough as `typeId` (so it ALSO needs T1 preservation in `toStudioNodes`).
- **Variable resolution is two-pass** because `varBindings[].sourceNodeId` is a workflow-LOCAL node id and the local→todo `idMap` only exists after `todos.CreateGraph`. At PlanCustom, `input_json = {kind, params}` where `params` = the registry params PLUS an injected `variables: [{name, sourceTodoId}]` built from the NODE's `varBindings`. PlanCustom writes local ids into `input_json`, calls CreateGraph, then does a SECOND pass that rewrites each binding's `sourceNodeId(local)`→`sourceTodoId(idMap[local])` and `UPDATE todos SET input_json=$1 WHERE id=$2`. The executor reads only `params.variables[].sourceTodoId` (post-rewrite) and is unaware of the registry/node split.
- **Worker dispatch fallback**: `process()` — if no exact executor AND `strings.HasPrefix(c.typ, "custom:")` → `runCustom`, which unmarshals `input_json`, switches on `kind`; only `"llm"` in A. The llm executor resolves variables (`sourceTodoId`→`output_ref`→`resolveOutputText`), substitutes `{{name}}` in prompts, calls `routedChatModel` like `runScript`, validates JSON if `outputFormat=="json"`, writes a `node_outputs` row, returns `custom:<id>`.
- **Two new tables** (`custom_node_types`, `node_outputs`) added as the next migration slice, following GORM house rules (INSERT…RETURNING, no AutoMigrate, pure `$N` Raw, NULL-column marshaling, `db.Transaction` for multi-statement).
- **Registry store** `internal/customnodetype/store.go` mirrors `internal/storageconfig/store.go` (org-scoped CRUD + `ErrInUse` delete guard).
- **Run-view minimal** (T3): `projectstate.GraphNode` gains an `Output` field; `LoadState`/`Compute` join `node_outputs` by todo id; frontend surfaces text/JSON in the existing selected-node panel.

## Tech Stack

- Backend: Go (stdlib `net/http` mux, GORM over pgxpool), Postgres. `GOWORK=off` for all go commands.
- Worker LLM: `coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{...})` + `agent.Run(ctx, userPrompt)` (same as `internal/planner/planner.go:76-88`), model from `w.routedChatModel`.
- Frontend: React + TypeScript, ReactFlow (`@xyflow/react`), react-query, vitest. Web tests: `npm test` (== `tsr generate && vitest run`) or `npx vitest run <file>`.
- DB-gated Go tests: skip unless `LLM_AGENT_STUDIO_PG_URL` is set; use a FRESH DB per the repo convention (stale data trips transient uniqueness indices).

---

## File Structure

| File | Create/Modify | Responsibility |
|------|---------------|----------------|
| `internal/storage/storage.go` | Modify | Add `m18Migrations` (custom_node_types + node_outputs); append to `Migrate`. |
| `internal/customnodetype/store.go` | Create | Org-scoped registry CRUD + `ErrInUse`/`ErrNotFound`; mirrors storageconfig. |
| `internal/customnodetype/store_test.go` | Create | DB-gated CRUD / org-isolation / slug-uniqueness / ErrInUse tests. |
| `web/src/lib/types.ts` | Modify | Add `typeId?: string` + `varBindings?` to `WorkflowNode`; add `CustomNodeType`/`UpsertCustomNodeTypeInput`/`LlmParams` types (NO `variables` in `LlmParams`); add `output?` to `GraphNode` (in projectState.ts). |
| `web/src/features/workflow-canvas/canvasModel.ts` | Modify | `toStudioNodes`: preserve `typeId` + `varBindings` (T1). |
| `web/src/features/workflow-canvas/canvasModel.test.ts` | Modify | Regression: `toStudioNodes` round-trips `typeId` + `varBindings`. |
| `internal/planner/planner.go` | Modify | `WorkflowNode.TypeId` + `WorkflowNode.VarBindings`; `ResolvedType`/`CustomVariable`; `HasUnboundCustomNode`; `PlanCustom` signature + variable two-pass (reads node `VarBindings`); validation of `varBindings[].sourceNodeId ∈ dependsOn`. |
| `internal/planner/planner_test.go` | Modify | PlanCustom typed input_json shape + variable rewrite; HasUnboundCustomNode. |
| `internal/worker/worker.go` | Modify | `process()` custom: fallback → `runCustom`; `runCustom` + `runCustomLLM`; `resolveOutputText`; `node_outputs` write. |
| `internal/worker/worker_custom_test.go` | Create | runCustom/llm executor + resolveOutputText (mock chat model). |
| `internal/httpapi/handlers.go` | Modify | `runHandler`: flip gate to `HasUnboundCustomNode`; resolve typed nodes org-scoped; pass `resolved` into `PlanCustom`. Update `PlannerPort` + interface. |
| `internal/httpapi/workflowhandlers.go` | Modify | `runWorkflowHandler`: same gate flip + resolution. |
| `internal/httpapi/customnodetypehandlers.go` | Create | Registry CRUD HTTP handlers + `CustomNodeTypeStore` interface. |
| `internal/httpapi/httpapi.go` | Modify | Add `CustomNodeType` dep + register registry routes; pass `CustomNodeTypeResolver` to run handlers. |
| `internal/httpapi/customnodetypehandlers_test.go` | Create | Handler-level CRUD + run-gate (annotation→400 / typed→202) tests. |
| `cmd/studiod/main.go` | Modify | Construct `customnodetype.Store`; wire into `httpapi.Deps`. |
| `internal/projectstate/state.go` | Modify | `GraphNode.Output`; `Todo`/`Asset`-style `Output` plumbing through `buildGraph`. |
| `internal/project/store.go` | Modify | `LoadState`: join `node_outputs` by todo id into `Input`. |
| `web/src/features/custom-node-types/api.ts` | Create | react-query hooks for registry CRUD. |
| `web/src/features/custom-node-types/CustomNodeTypeManager.tsx` | Create | List/create/edit/delete typed types + Rich llm param form. |
| `web/src/features/custom-node-types/LlmParamForm.tsx` | Create | Org-level form: systemPrompt/userPrompt/model/temperature/outputFormat (NO variable binding — that's per-node in PropertiesPanel). |
| `web/src/features/custom-node-types/*.test.tsx` | Create | Rich form + manager tests. |
| `web/src/features/workflow-canvas/canvasModel.ts` (+ palette/picker/panel) | Modify | Dual-type entry (annotation + typed); typed node sets `typeId`; PropertiesPanel shows typed kind+params; run-view output panel; run-gating uses unbound predicate. |

---

### Task 1: Migration — `custom_node_types` + `node_outputs` tables

**Files:**
- Modify: `internal/storage/storage.go` (add `m18Migrations` after `m17Migrations` ~line 448; extend `Migrate` ~line 452)
- Test: covered by the registry store test in Task 2 (the migration runs in `testPool`/`testGorm`).

- [ ] In `internal/storage/storage.go`, immediately after the `m17Migrations` slice (line ~448) add:
  ```go
  // m18Migrations 建 custom_node_types (组织级 typed 自定义节点注册表) + node_outputs
  // (通用产物表，custom 执行结果落地，下游变量读取 + 运行视图消费)。
  // custom_node_types: 唯一索引 (org_id, slug)；编辑只改 label/color/params，不改 slug/kind。
  // node_outputs: project scope；content 存文本或 JSON，format 标注。additive only。
  var m18Migrations = []string{
  	`CREATE TABLE IF NOT EXISTS custom_node_types (
  		id TEXT PRIMARY KEY,
  		org_id TEXT NOT NULL,
  		slug TEXT NOT NULL,
  		label TEXT NOT NULL,
  		color TEXT NOT NULL DEFAULT '',
  		kind TEXT NOT NULL,
  		params JSONB NOT NULL DEFAULT '{}',
  		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
  	)`,
  	`CREATE UNIQUE INDEX IF NOT EXISTS custom_node_types_org_slug_uniq ON custom_node_types (org_id, slug)`,
  	`CREATE TABLE IF NOT EXISTS node_outputs (
  		id TEXT PRIMARY KEY,
  		project_id TEXT NOT NULL,
  		todo_id TEXT NOT NULL,
  		type TEXT NOT NULL,
  		content TEXT NOT NULL DEFAULT '',
  		format TEXT NOT NULL DEFAULT 'text',
  		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
  	)`,
  	`CREATE INDEX IF NOT EXISTS node_outputs_todo_idx ON node_outputs (todo_id)`,
  	`CREATE INDEX IF NOT EXISTS node_outputs_project_idx ON node_outputs (project_id)`,
  }
  ```
  (Note: `content` is TEXT not JSONB — text and stringified-JSON both fit; the `format` column discriminates. This avoids a JSONB-cast on plain text writes.)
- [ ] Update the `Migrate` doc comment (line ~450) to append `+ M18`.
- [ ] In `Migrate` (line ~452), append `m18Migrations...` to the `all` chain:
  ```go
  	all := append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append([]string{},
  		m1Migrations...), m2Migrations...), m3Migrations...), m4Migrations...), m5Migrations...), m6Migrations...), m7Migrations...), m8Migrations...), m9Migrations...), m10Migrations...), m11Migrations...), m12Migrations...), m13Migrations...), m14Migrations...), m15Migrations...), m16Migrations...), m17Migrations...), m18Migrations...)
  ```
- [ ] Run: `GOWORK=off go build ./internal/storage/...`
  - Expected: builds clean, no output.
- [ ] Commit: `feat(storage): add custom_node_types + node_outputs migration (m18)`

---

### Task 2: Registry store `internal/customnodetype/store.go`

TDD: write the failing DB-gated store test first, then the store.

**Files:**
- Create: `internal/customnodetype/store.go`
- Create: `internal/customnodetype/store_test.go`

- [ ] Create `internal/customnodetype/store_test.go` with the DB-gated harness mirroring `internal/storageconfig/store_test.go:28-66` (skip if `LLM_AGENT_STUDIO_PG_URL` unset; open + migrate; return GORM handle). Use a unique org id per test (`randID(t)`) so a shared DB doesn't collide on the `(org_id, slug)` unique index:
  ```go
  package customnodetype

  import (
  	"context"
  	"crypto/rand"
  	"encoding/hex"
  	"encoding/json"
  	"errors"
  	"os"
  	"testing"

  	"gorm.io/gorm"

  	"github.com/costa92/llm-agent-studio/internal/storage"
  )

  func testGorm(t *testing.T) *gorm.DB {
  	t.Helper()
  	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
  	if dsn == "" {
  		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run custom node type store tests")
  	}
  	ctx := context.Background()
  	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
  	if err != nil {
  		t.Fatalf("open: %v", err)
  	}
  	t.Cleanup(st.Close)
  	if err := st.Migrate(ctx); err != nil {
  		t.Fatalf("migrate: %v", err)
  	}
  	return st.GORM()
  }

  func randID(t *testing.T) string {
  	t.Helper()
  	b := make([]byte, 8)
  	_, _ = rand.Read(b)
  	return hex.EncodeToString(b)
  }

  func llmInput(label string) UpsertInput {
  	params, _ := json.Marshal(map[string]any{"systemPrompt": "sys", "userPrompt": "{{x}}", "outputFormat": "text"})
  	return UpsertInput{Slug: "", Label: label, Color: "#7c93ff", Kind: "llm", Params: params}
  }

  func TestCreateListGet(t *testing.T) {
  	db := testGorm(t)
  	org := randID(t)
  	ct, err := New(db).Create(context.Background(), org, llmInput("翻译"))
  	if err != nil {
  		t.Fatalf("create: %v", err)
  	}
  	if ct.Slug == "" || ct.Kind != "llm" || ct.OrgID != org {
  		t.Fatalf("bad row: %+v", ct)
  	}
  	got, err := New(db).Get(context.Background(), ct.ID, org)
  	if err != nil {
  		t.Fatalf("get: %v", err)
  	}
  	if got.ID != ct.ID {
  		t.Fatalf("get mismatch")
  	}
  	items, err := New(db).List(context.Background(), org)
  	if err != nil || len(items) != 1 {
  		t.Fatalf("list: %v len=%d", err, len(items))
  	}
  }

  func TestOrgIsolation(t *testing.T) {
  	db := testGorm(t)
  	orgA, orgB := randID(t), randID(t)
  	ct, _ := New(db).Create(context.Background(), orgA, llmInput("A 类型"))
  	if _, err := New(db).Get(context.Background(), ct.ID, orgB); !errors.Is(err, ErrNotFound) {
  		t.Fatalf("cross-org Get should be ErrNotFound, got %v", err)
  	}
  	if _, err := New(db).Update(context.Background(), ct.ID, orgB, llmInput("hijack")); !errors.Is(err, ErrNotFound) {
  		t.Fatalf("cross-org Update should be ErrNotFound, got %v", err)
  	}
  	if err := New(db).Delete(context.Background(), ct.ID, orgB); !errors.Is(err, ErrNotFound) {
  		t.Fatalf("cross-org Delete should be ErrNotFound, got %v", err)
  	}
  }

  func TestSlugUnique(t *testing.T) {
  	db := testGorm(t)
  	org := randID(t)
  	if _, err := New(db).Create(context.Background(), org, llmInput("同名")); err != nil {
  		t.Fatalf("first create: %v", err)
  	}
  	if _, err := New(db).Create(context.Background(), org, llmInput("同名")); err == nil {
  		t.Fatalf("duplicate slug should fail")
  	}
  }
  ```
- [ ] Run (expect FAIL — package doesn't compile yet): `GOWORK=off go test ./internal/customnodetype/... -run TestCreateListGet -count=1`
  - Expected: build error `no Go files` / undefined `New`.
- [ ] Create `internal/customnodetype/store.go` mirroring storageconfig:
  ```go
  // Package customnodetype owns custom_node_types CRUD: 组织级 typed 自定义节点注册表
  // (绑定一个 kind + params)。A 只支持 kind="llm"。slug 由 label 规范化、创建后不可改；
  // 编辑只改 label/color/params。删除有占用守卫 (任意 workflow 节点引用该 id → ErrInUse)。
  // 组织隔离贯穿全部读写 (WHERE org_id=$N)，与 storageconfig 同范式；标记为需独立安全评审。
  package customnodetype

  import (
  	"context"
  	"crypto/rand"
  	"database/sql"
  	"encoding/hex"
  	"encoding/json"
  	"errors"
  	"fmt"
  	"regexp"
  	"strings"

  	"gorm.io/gorm"
  )

  // ErrNotFound 表示按 org 定位的类型不存在 (含跨租户访问被拒)。
  var ErrNotFound = errors.New("customnodetype: type not found")

  // ErrInUse 表示该类型被某 workflow 节点 (typeId) 引用，不可删除 (best-effort: 见 Delete)。
  var ErrInUse = errors.New("customnodetype: type in use by workflow nodes")

  // validKinds 是 A 支持的 kind 集合 (后续 B/C 扩展 http/script/python)。
  var validKinds = map[string]bool{"llm": true}

  var slugStrip = regexp.MustCompile(`[^a-z0-9\-_\x{4e00}-\x{9fa5}]`)

  // CustomNodeType 是 custom_node_types 行的公开 DTO。
  type CustomNodeType struct {
  	ID        string          `json:"id"`
  	OrgID     string          `json:"orgId"`
  	Slug      string          `json:"slug"`
  	Label     string          `json:"label"`
  	Color     string          `json:"color"`
  	Kind      string          `json:"kind"`
  	Params    json.RawMessage `json:"params"`
  }

  // UpsertInput 是 Create/Update 入参。Create 用 Label 派生 slug；Update 忽略 Slug/Kind。
  type UpsertInput struct {
  	Slug   string // Create 内部派生；外部传空
  	Label  string
  	Color  string
  	Kind   string
  	Params json.RawMessage
  }

  // Store persists custom_node_types.
  type Store struct{ db *gorm.DB }

  // New builds a Store.
  func New(db *gorm.DB) *Store { return &Store{db: db} }

  func newID() string {
  	b := make([]byte, 16)
  	_, _ = rand.Read(b)
  	return hex.EncodeToString(b)
  }

  // slugify 把 label 规范化为 slug：小写、空白转 -、去非法字符 (保留中日韩)；空则 "type"。
  // 与前端 nodeColor.slugify 同语义 (两侧需对齐，但 slug 服务端权威)。
  func slugify(label string) string {
  	s := strings.ToLower(strings.TrimSpace(label))
  	s = strings.Join(strings.Fields(s), "-")
  	s = slugStrip.ReplaceAllString(s, "")
  	if s == "" {
  		return "type"
  	}
  	return s
  }

  func validate(in UpsertInput) error {
  	if strings.TrimSpace(in.Label) == "" {
  		return fmt.Errorf("customnodetype: label required")
  	}
  	if !validKinds[in.Kind] {
  		return fmt.Errorf("customnodetype: invalid kind %q (want llm)", in.Kind)
  	}
  	if len(in.Params) == 0 || !json.Valid(in.Params) {
  		return fmt.Errorf("customnodetype: params must be valid JSON")
  	}
  	return nil
  }

  func scanType(row interface{ Scan(...any) error }) (CustomNodeType, error) {
  	var ct CustomNodeType
  	var params []byte
  	if err := row.Scan(&ct.ID, &ct.OrgID, &ct.Slug, &ct.Label, &ct.Color, &ct.Kind, &params); err != nil {
  		return CustomNodeType{}, err
  	}
  	ct.Params = json.RawMessage(params)
  	return ct, nil
  }

  // Create 插入一条新 org 类型 (INSERT…RETURNING，纯 $N)。slug 由 label 派生。
  func (s *Store) Create(ctx context.Context, orgID string, in UpsertInput) (CustomNodeType, error) {
  	if orgID == "" {
  		return CustomNodeType{}, fmt.Errorf("customnodetype: orgID required")
  	}
  	if err := validate(in); err != nil {
  		return CustomNodeType{}, err
  	}
  	const q = `
  		INSERT INTO custom_node_types (id, org_id, slug, label, color, kind, params)
  		VALUES ($1,$2,$3,$4,$5,$6,$7)
  		RETURNING id, org_id, slug, label, color, kind, params`
  	row := s.db.WithContext(ctx).Raw(q,
  		newID(), orgID, slugify(in.Label), in.Label, in.Color, in.Kind, []byte(in.Params)).Row()
  	ct, err := scanType(row)
  	if err != nil {
  		return CustomNodeType{}, fmt.Errorf("customnodetype: create: %w", err)
  	}
  	return ct, nil
  }

  // List 返回 org 的全部类型 (创建序)。
  func (s *Store) List(ctx context.Context, orgID string) ([]CustomNodeType, error) {
  	rows, err := s.db.WithContext(ctx).Raw(
  		`SELECT id, org_id, slug, label, color, kind, params
  		 FROM custom_node_types WHERE org_id=$1 ORDER BY created_at ASC`, orgID).Rows()
  	if err != nil {
  		return nil, fmt.Errorf("customnodetype: list: %w", err)
  	}
  	defer rows.Close()
  	out := []CustomNodeType{}
  	for rows.Next() {
  		ct, err := scanType(rows)
  		if err != nil {
  			return nil, err
  		}
  		out = append(out, ct)
  	}
  	return out, rows.Err()
  }

  // Get 按 (id, org) 读一条；跨租户/不存在 → ErrNotFound。
  func (s *Store) Get(ctx context.Context, id, orgID string) (CustomNodeType, error) {
  	row := s.db.WithContext(ctx).Raw(
  		`SELECT id, org_id, slug, label, color, kind, params
  		 FROM custom_node_types WHERE id=$1 AND org_id=$2`, id, orgID).Row()
  	ct, err := scanType(row)
  	if errors.Is(err, sql.ErrNoRows) {
  		return CustomNodeType{}, ErrNotFound
  	}
  	if err != nil {
  		return CustomNodeType{}, fmt.Errorf("customnodetype: get: %w", err)
  	}
  	return ct, nil
  }

  // Update 改 label/color/params (不改 slug/kind)；跨租户/不存在 → ErrNotFound。
  func (s *Store) Update(ctx context.Context, id, orgID string, in UpsertInput) (CustomNodeType, error) {
  	if orgID == "" || id == "" {
  		return CustomNodeType{}, fmt.Errorf("customnodetype: orgID+id required")
  	}
  	if err := validate(in); err != nil {
  		return CustomNodeType{}, err
  	}
  	const q = `
  		UPDATE custom_node_types SET label=$3, color=$4, params=$5, updated_at=now()
  		WHERE id=$1 AND org_id=$2
  		RETURNING id, org_id, slug, label, color, kind, params`
  	row := s.db.WithContext(ctx).Raw(q, id, orgID, in.Label, in.Color, []byte(in.Params)).Row()
  	ct, err := scanType(row)
  	if errors.Is(err, sql.ErrNoRows) {
  		return CustomNodeType{}, ErrNotFound
  	}
  	if err != nil {
  		return CustomNodeType{}, fmt.Errorf("customnodetype: update: %w", err)
  	}
  	return ct, nil
  }

  // Delete 按 (id, org) 删除。占用守卫：扫描该 org 全部 workflows.nodes JSONB，
  // 若任一节点 typeId == id → ErrInUse。ref-check 与 DELETE 同一事务避免 TOCTOU。
  // 占用检测是 best-effort：只查 workflows 表 (典型保存载体)；不扫 projects.workflow_nodes
  // 旧内嵌列 (m12 已 backfill 进 workflows，旧列仅 legacy 残留)。
  func (s *Store) Delete(ctx context.Context, id, orgID string) error {
  	if orgID == "" || id == "" {
  		return fmt.Errorf("customnodetype: orgID+id required")
  	}
  	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
  		var refs int
  		if err := tx.Raw(`
  			SELECT count(*) FROM workflows w
  			JOIN projects p ON w.project_id = p.id
  			WHERE p.org_id=$1
  			  AND EXISTS (
  			    SELECT 1 FROM jsonb_array_elements(w.nodes) n
  			    WHERE n->>'typeId' = $2
  			  )`, orgID, id).Row().Scan(&refs); err != nil {
  			return fmt.Errorf("customnodetype: ref check: %w", err)
  		}
  		if refs > 0 {
  			return ErrInUse
  		}
  		res := tx.Exec(`DELETE FROM custom_node_types WHERE id=$1 AND org_id=$2`, id, orgID)
  		if res.Error != nil {
  			return res.Error
  		}
  		if res.RowsAffected == 0 {
  			return ErrNotFound
  		}
  		return nil
  	})
  }
  ```
- [ ] Add a `TestDeleteInUse` to the test file: create a project + workflow whose nodes JSON contains `{"typeId": ct.ID, ...}`, then assert `Delete` returns `ErrInUse`; assert an unreferenced type deletes cleanly. (Insert the project/workflow with raw `db.Exec` INSERTs matching the `projects`/`workflows` schemas — set `org_id` to the type's org.)
- [ ] Run: `GOWORK=off go test ./internal/customnodetype/... -count=1` (against a fresh DB:
  `LLM_AGENT_STUDIO_PG_URL=postgres://... GOWORK=off go test ./internal/customnodetype/... -count=1 -p 1`)
  - Expected: `ok ... internal/customnodetype` (or `SKIP` without the env var; ensure it passes with a real fresh DB).
- [ ] Commit: `feat(customnodetype): org-scoped typed registry store`

---

### Task 3: Frontend `typeId` + `varBindings` passthrough (T1)

TDD: regression test first. TWO fields ride the raw-JSONB passthrough: `typeId` (org registry ref) and `varBindings` (per-node variable bindings). Both must be explicitly copied in `toStudioNodes`.

**Files:**
- Modify: `web/src/lib/types.ts` (`WorkflowNode` ~line 106)
- Modify: `web/src/features/workflow-canvas/canvasModel.ts` (`toStudioNodes` ~line 94-108)
- Test: `web/src/features/workflow-canvas/canvasModel.test.ts`

- [ ] In `web/src/features/workflow-canvas/canvasModel.test.ts`, add a regression test inside the existing `describe("toStudioNodes", ...)` block (or a new describe). Assert BOTH `typeId` and `varBindings` round-trip:
  ```ts
  it("preserves typeId + varBindings on a typed custom node round-trip (T1)", () => {
    const typed: WorkflowNode[] = [
      {
        id: "n1",
        type: "custom:translate",
        typeId: "reg-123",
        varBindings: [{ name: "draft", sourceNodeId: "script-1" }],
        promptId: "",
        dependsOn: ["script-1"],
        label: "翻译",
        color: "#7c93ff",
      },
    ]
    const { nodes, edges } = toReactFlow(typed)
    const out = toStudioNodes(nodes as RFNode[], edges as RFEdge[])
    expect(out[0].typeId).toBe("reg-123")
    expect(out[0].varBindings).toEqual([{ name: "draft", sourceNodeId: "script-1" }])
    expect(out[0].label).toBe("翻译")
  })
  ```
- [ ] Run (expect FAIL): `cd web && npx vitest run src/features/workflow-canvas/canvasModel.test.ts`
  - Expected: assertion fails (`out[0].typeId` / `out[0].varBindings` are `undefined`).
- [ ] In `web/src/lib/types.ts`, add to `WorkflowNode` (after `color?: string` ~line 116):
  ```ts
    // typed 自定义节点：引用组织级注册表条目 id (custom_node_types.id)。
    // 有 typeId = typed (可运行)；无 = annotation (Phase 1 草图，不可运行)。判别器。
    typeId?: string
    // 每节点变量绑定：把模板里的 {{name}} 绑到上游 workflow-local 节点 id。
    // sourceNodeId 是 workflow-local，所以必须存在节点实例上 (而非组织级 registry params)。
    varBindings?: { name: string; sourceNodeId: string }[]
  ```
- [ ] In `web/src/features/workflow-canvas/canvasModel.ts` `toStudioNodes`, after the `if (n.color) out.color = n.color` line (~line 107) add (right after the typeId copy):
  ```ts
      // typeId 不是免费透传：toStudioNodes 逐字段重建 WorkflowNode，必须显式拷贝，
      // 否则首次画布编辑保存即丢 typeId (T1)。
      if (n.typeId) out.typeId = n.typeId
      // varBindings 同理：每节点变量绑定也必须显式拷贝，否则编辑保存即丢绑定 (T1)。
      if (n.varBindings && n.varBindings.length) out.varBindings = n.varBindings
  ```
- [ ] Run (expect PASS): `cd web && npx vitest run src/features/workflow-canvas/canvasModel.test.ts`
  - Expected: all green.
- [ ] Commit: `feat(web): preserve typeId + varBindings through canvas toStudioNodes (T1)`

---

### Task 4: Planner — `TypeId`, `HasUnboundCustomNode`, `ResolvedType`, validation

TDD: extend `planner_test.go` for the new predicate + validation.

**Files:**
- Modify: `internal/planner/planner.go` (`WorkflowNode` ~147; new `ResolvedType`, `HasUnboundCustomNode`; validation)
- Test: `internal/planner/planner_test.go`

- [ ] In `internal/planner/planner.go`, add `TypeId` AND `VarBindings` to `WorkflowNode` (after `DependsOn` ~line 156). `VarBindings` carries the per-node variable bindings (workflow-LOCAL `sourceNodeId`), the SOURCE OF TRUTH for PlanCustom's two-pass — NOT resolved registry params:
  ```go
  	DependsOn  []string `json:"dependsOn"`
  	// TypeId references a custom_node_types.id (org registry). Non-empty ⇒ a
  	// runnable "typed" custom node; empty on a custom:* node ⇒ Phase 1 annotation
  	// (non-runnable). Discriminator for HasUnboundCustomNode + run resolution.
  	TypeId string `json:"typeId"`
  	// VarBindings binds the template's {{name}} tokens to upstream workflow-LOCAL
  	// node ids. Lives on the node instance (NOT registry params) because sourceNodeId
  	// is workflow-local. PlanCustom reads THIS to inject params.variables + two-pass
  	// rewrite local→todo. Empty for annotation nodes.
  	VarBindings []CustomVariable `json:"varBindings"`
  ```
- [ ] Add a `ResolvedType` type + `CustomVariable` near `WorkflowNode`:
  ```go
  // CustomVariable binds a template var name to an upstream node's text output.
  // SourceNodeId is a workflow-LOCAL node id at plan time (lives on the node's
  // VarBindings); PlanCustom rewrites it to the produced todo id (SourceTodoId)
  // after CreateGraph (two-pass) and injects it into params.variables.
  type CustomVariable struct {
  	Name         string `json:"name"`
  	SourceNodeId string `json:"sourceNodeId,omitempty"`
  	SourceTodoId string `json:"sourceTodoId,omitempty"`
  }

  // ResolvedType is the run handler's per-node registry resolution (org-scoped):
  // the entry's Kind + raw Params (LlmParams: systemPrompt/userPrompt/model/
  // temperature/outputFormat — NO variables; variable bindings come from the node's
  // VarBindings). The handler builds map[nodeID]ResolvedType and passes it into
  // PlanCustom; the planner never reads the registry (store-thin).
  type ResolvedType struct {
  	Kind   string
  	Params json.RawMessage
  }
  ```
- [ ] Add the unbound predicate (replaces the run-gate use of `HasCustomNode`):
  ```go
  // HasUnboundCustomNode reports whether any node is a custom:* node WITHOUT a
  // typeId (= a Phase 1 annotation, non-runnable). Run handlers refuse such
  // workflows (typed-only workflows are runnable). Discriminator = explicit typeId.
  func HasUnboundCustomNode(nodes []WorkflowNode) bool {
  	for _, n := range nodes {
  		if isCustomType(n.Type) && n.TypeId == "" {
  			return true
  		}
  	}
  	return false
  }
  ```
- [ ] **Clean the `HasCustomNode` orphan (deviation #3).** After flipping the two run-gate sites to `HasUnboundCustomNode` (Task 7), grep for remaining references:
  `GOWORK=off grep -rn 'HasCustomNode' internal/ web/ cmd/`
  - If the ONLY remaining callers are its Phase-1 test (`TestHasCustomNode`) and nothing else live (the two gate sites are now flipped), REMOVE `HasCustomNode` and `TestHasCustomNode` — they are orphans of our own change (house rule: clean orphans you create). Do this as the final step of Task 7 (after the gate flip lands), not here.
  - If any OTHER live (non-test) reference exists, KEEP `HasCustomNode` and note where. State the grep result in the Task 7 commit.
- [ ] In `planner_test.go`, add:
  ```go
  func TestHasUnboundCustomNode(t *testing.T) {
  	annotated := []planner.WorkflowNode{{ID: "a", Type: "custom:note"}}
  	if !planner.HasUnboundCustomNode(annotated) {
  		t.Fatal("annotation custom node must be unbound")
  	}
  	typed := []planner.WorkflowNode{{ID: "a", Type: "custom:llm", TypeId: "reg-1"}}
  	if planner.HasUnboundCustomNode(typed) {
  		t.Fatal("typed custom node must NOT be unbound")
  	}
  }
  ```
- [ ] Run (expect FAIL → then PASS after the code above compiles): `GOWORK=off go test ./internal/planner/... -run TestHasUnboundCustomNode -count=1`
  - Expected: `ok` once the planner compiles.
- [ ] Commit: `feat(planner): TypeId, HasUnboundCustomNode, ResolvedType/CustomVariable`

---

### Task 5: Planner — `PlanCustom` signature + typed input_json + variable two-pass

This is the load-bearing planner change. Update BOTH the function and the `PlannerPort` interface (interface update is wired in Task 7, but signature must change here).

**Files:**
- Modify: `internal/planner/planner.go` (`PlanCustom` ~239-308)
- Test: `internal/planner/planner_test.go`

- [ ] Change the `PlanCustom` signature to accept the handler-resolved map:
  ```go
  func (p *Planner) PlanCustom(ctx context.Context, projectID, workflowID string, b Brief, nodes []WorkflowNode, resolved map[string]ResolvedType) (Result, error) {
  ```
- [ ] At the top of `PlanCustom`, after `ValidateCustomGraph(nodes)`, add typed-node validation: every typed node's `VarBindings[].SourceNodeId` must be in that node's `DependsOn`. Read the bindings from the NODE (`n.VarBindings`), NOT from resolved params:
  ```go
  	// Validate typed-node variable bindings: every binding's SourceNodeId MUST be an
  	// upstream dependency (in DependsOn) so the data is actually produced before this
  	// node runs. Reject otherwise (would read a non-existent / unordered output).
  	// Bindings live on the NODE (n.VarBindings), not registry params.
  	depSet := make(map[string]map[string]bool, len(nodes))
  	for _, n := range nodes {
  		ds := make(map[string]bool, len(n.DependsOn))
  		for _, d := range n.DependsOn {
  			ds[d] = true
  		}
  		depSet[n.ID] = ds
  	}
  	for _, n := range nodes {
  		if _, ok := resolved[n.ID]; !ok {
  			continue // not a typed node
  		}
  		for _, v := range n.VarBindings {
  			if v.SourceNodeId == "" {
  				continue
  			}
  			if !depSet[n.ID][v.SourceNodeId] {
  				return Result{}, fmt.Errorf("planner: custom node %q variable %q sourceNodeId %q must be in dependsOn", n.ID, v.Name, v.SourceNodeId)
  			}
  		}
  	}
  ```
- [ ] In the `specs` build loop (existing ~256-293), add a typed-node branch that writes the executor input shape into `input_json`. Keep the existing script/prompt branches for built-in node types. For a typed node, build `{kind, params}` where `params.variables[].sourceNodeId` is still the LOCAL id (rewritten in pass 2):
  ```go
  	for _, n := range nodes {
  		inputMap := map[string]interface{}{}
  		if rt, ok := resolved[n.ID]; ok {
  			// Typed custom node: write {kind, params} into input_json. params = the
  			// registry params (NO variables) PLUS an injected variables list built from
  			// the NODE's VarBindings, with LOCAL sourceNodeId here; pass 2 (after
  			// CreateGraph) rewrites each to its todo id.
  			var params map[string]interface{}
  			if err := json.Unmarshal(rt.Params, &params); err != nil {
  				return Result{}, fmt.Errorf("planner: unmarshal resolved params for %q: %w", n.ID, err)
  			}
  			vars := make([]map[string]interface{}, 0, len(n.VarBindings))
  			for _, v := range n.VarBindings {
  				vars = append(vars, map[string]interface{}{"name": v.Name, "sourceNodeId": v.SourceNodeId})
  			}
  			params["variables"] = vars
  			inputMap["kind"] = rt.Kind
  			inputMap["params"] = params
  		} else {
  			// Built-in node (script/storyboard/asset): existing prompt-precedence logic.
  			if n.Type == "script" {
  				inputMap["brief"] = b.Brief
  				inputMap["contentType"] = b.ContentType
  				inputMap["targetPlatform"] = b.TargetPlatform
  				inputMap["style"] = b.Style
  			}
  			if n.PromptText != "" {
  				inputMap["systemPrompt"] = n.PromptText
  			} else if n.PromptID != "" {
  				if content, ok := prompt.BasicPromptContent(n.PromptID); ok {
  					inputMap["systemPrompt"] = content
  				} else {
  					var promptContent string
  					err := p.db.WithContext(ctx).Raw("SELECT content FROM prompts WHERE id=$1", n.PromptID).Row().Scan(&promptContent)
  					if err != nil {
  						return Result{}, fmt.Errorf("planner: get prompt %q: %w", n.PromptID, err)
  					}
  					inputMap["systemPrompt"] = promptContent
  				}
  			}
  		}
  		inputBytes, err := json.Marshal(inputMap)
  		if err != nil {
  			return Result{}, fmt.Errorf("planner: marshal node input: %w", err)
  		}
  		specs = append(specs, todos.NodeSpec{
  			LocalID: n.ID, Type: n.Type, DependsOn: n.DependsOn, InputJSON: inputBytes,
  		})
  	}
  ```
  (Note: the existing planner.go:277 unscoped prompt read stays ONLY for built-in `promptId` library lookups — typed custom nodes never hit it; T2's "do not replicate" concern is about the REGISTRY read, which now lives in the handler.)
- [ ] After `idMap, err := p.todos.CreateGraph(...)` succeeds (existing ~295), add PASS 2 — rewrite each typed node's variable `sourceNodeId(local)`→`sourceTodoId(idMap[local])` and UPDATE the todo:
  ```go
  	// Pass 2: rewrite each typed-node binding's sourceNodeId (local) → sourceTodoId
  	// (todo id from idMap) and UPDATE input_json. The local→todo map only exists now.
  	// Source of truth is the NODE's VarBindings (registry params never held variables).
  	for _, n := range nodes {
  		rt, ok := resolved[n.ID]
  		if !ok {
  			continue
  		}
  		if len(n.VarBindings) == 0 {
  			continue
  		}
  		var params map[string]interface{}
  		if err := json.Unmarshal(rt.Params, &params); err != nil {
  			return Result{}, fmt.Errorf("planner: re-parse params for %q: %w", n.ID, err)
  		}
  		newVars := make([]map[string]interface{}, 0, len(n.VarBindings))
  		for _, v := range n.VarBindings {
  			out := map[string]interface{}{"name": v.Name}
  			if v.SourceNodeId != "" {
  				todoID, ok := idMap[v.SourceNodeId]
  				if !ok {
  					return Result{}, fmt.Errorf("planner: variable %q references unknown local node %q", v.Name, v.SourceNodeId)
  				}
  				out["sourceTodoId"] = todoID // drop the local sourceNodeId key
  			}
  			newVars = append(newVars, out)
  		}
  		params["variables"] = newVars
  		inputBytes, err := json.Marshal(map[string]interface{}{"kind": rt.Kind, "params": params})
  		if err != nil {
  			return Result{}, fmt.Errorf("planner: marshal rewritten input for %q: %w", n.ID, err)
  		}
  		if err := p.db.WithContext(ctx).Exec(
  			`UPDATE todos SET input_json=$1 WHERE id=$2`, inputBytes, idMap[n.ID]).Error; err != nil {
  			return Result{}, fmt.Errorf("planner: update typed node input for %q: %w", n.ID, err)
  		}
  	}
  ```
- [ ] Update ALL existing callers of `PlanCustom` in `planner_test.go` to pass a `resolved` map (use `nil` where there are no typed nodes).
- [ ] Add a DB-gated test `TestPlanCustom_TypedVariableRewrite` to `planner_test.go`: build a 2-node graph `script-1` → `c1` (type `custom:llm`, TypeId `reg-1`, DependsOn `["script-1"]`, `VarBindings: []planner.CustomVariable{{Name:"draft", SourceNodeId:"script-1"}}`) with `resolved["c1"] = ResolvedType{Kind:"llm", Params: {"systemPrompt":"s","userPrompt":"{{draft}}","outputFormat":"text"}}` (NO `variables` key in Params — bindings come from the node's VarBindings). After `PlanCustom`, read `c1`'s todo `input_json`, assert `kind=="llm"` and `params.variables[0].sourceTodoId == idMap["script-1"]` (i.e. equals the script todo id, not `"script-1"`) and that no `sourceNodeId` key remains. Mirror the existing PlanCustom DB-gated test setup in `planner_test.go` for the harness.
- [ ] Add `TestPlanCustom_VariableNotInDependsOn`: typed node whose `VarBindings[0].SourceNodeId` is NOT in `DependsOn` → `PlanCustom` returns an error containing `must be in dependsOn`. (Put the offending binding in the NODE's `VarBindings`, not in resolved params.)
- [ ] Run: `GOWORK=off go build ./internal/planner/...` then (fresh DB) `LLM_AGENT_STUDIO_PG_URL=... GOWORK=off go test ./internal/planner/... -count=1 -p 1`
  - Expected: builds; tests pass (or skip without DB; ensure pass with DB).
- [ ] Commit: `feat(planner): PlanCustom typed input_json + variable two-pass rewrite`

---

### Task 6: Worker — `runCustom` dispatch fallback + llm executor + `resolveOutputText` + `node_outputs`

TDD: write the executor test with a mock chat model first.

**Files:**
- Modify: `internal/worker/worker.go` (`process()` ~321-333; add `runCustom`, `runCustomLLM`, `resolveOutputText`)
- Test: `internal/worker/worker_custom_test.go`

- [ ] In `process()`, change the executor-lookup block (current ~321-333) so a missing exact executor for a `custom:` type routes to `runCustom`:
  ```go
  	var outputRef string
  	var perr error
  	executor, exists := w.executors[c.typ]
  	switch {
  	case exists:
  		todo := ClaimedTodo{
  			TodoID:    c.todoID,
  			ProjectID: c.projectID,
  			Type:      c.typ,
  			Attempts:  c.attempts,
  			Input:     c.input,
  		}
  		outputRef, perr = executor(dctx, todo)
  	case strings.HasPrefix(c.typ, "custom:"):
  		// Generic custom dispatch fallback (Phase 2A): no exact executor for a
  		// custom:* type → runCustom switches on input_json.kind. runCustom's switch
  		// is the B/C extension point (http/script/python).
  		outputRef, perr = w.runCustom(dctx, claimed{todoID: c.todoID, projectID: c.projectID, typ: c.typ, attempts: c.attempts, input: c.input})
  	default:
  		perr = fmt.Errorf("worker: unknown todo type %q", c.typ)
  	}
  ```
- [ ] Add `runCustom` + `runCustomLLM` + `resolveOutputText` to `worker.go` (use `coreagents` — add `coreagents "github.com/costa92/llm-agent"` to the import block):
  ```go
  // customInput is the shape PlanCustom writes into a typed custom todo's input_json:
  // {kind, params} where params = registry LlmParams PLUS the merged variables list
  // (injected from the node's varBindings at plan time, rewritten local→sourceTodoId).
  // The executor only ever reads params.variables[].sourceTodoId — it never sees the
  // registry/node split.
  type customInput struct {
  	Kind   string `json:"kind"`
  	Params struct {
  		SystemPrompt string  `json:"systemPrompt"`
  		UserPrompt   string  `json:"userPrompt"`
  		Model        string  `json:"model"`
  		Temperature  float64 `json:"temperature"`
  		OutputFormat string  `json:"outputFormat"` // "text" | "json"
  		Variables    []struct {
  			Name         string `json:"name"`
  			SourceTodoId string `json:"sourceTodoId"`
  		} `json:"variables"`
  	} `json:"params"`
  }

  // runCustom dispatches a typed custom todo by its input_json.kind. A only
  // implements "llm"; the switch is the B/C extension point (http/script/python).
  func (w *Worker) runCustom(ctx context.Context, c claimed) (string, error) {
  	var in customInput
  	if err := json.Unmarshal(c.input, &in); err != nil {
  		return "", fmt.Errorf("worker: custom input unmarshal: %w", err)
  	}
  	switch in.Kind {
  	case "llm":
  		return w.runCustomLLM(ctx, c, in)
  	default:
  		return "", fmt.Errorf("worker: unsupported custom kind %q", in.Kind)
  	}
  }

  // runCustomLLM executes the "llm" kind: resolve each variable's upstream text
  // output, substitute {{name}} in system/user prompt, call the routed chat model
  // (same routing as runScript), optionally instruct+validate JSON, write a
  // node_outputs row, return "custom:<id>".
  func (w *Worker) runCustomLLM(ctx context.Context, c claimed, in customInput) (string, error) {
  	// 1. Resolve variables: sourceTodoId → that todo's output_ref → resolveOutputText.
  	replacer := map[string]string{}
  	for _, v := range in.Params.Variables {
  		if v.SourceTodoId == "" {
  			continue
  		}
  		var outputRef string
  		if err := w.cfg.DB.WithContext(ctx).Raw(
  			`SELECT COALESCE(output_ref,'') FROM todos WHERE id=$1`, v.SourceTodoId).Row().Scan(&outputRef); err != nil {
  			return "", fmt.Errorf("worker: load variable %q source todo: %w", v.Name, err)
  		}
  		text, err := w.resolveOutputText(ctx, outputRef)
  		if err != nil {
  			return "", fmt.Errorf("worker: resolve variable %q: %w", v.Name, err)
  		}
  		replacer[v.Name] = text
  	}

  	system := substituteVars(in.Params.SystemPrompt, replacer)
  	user := substituteVars(in.Params.UserPrompt, replacer)
  	if in.Params.OutputFormat == "json" {
  		system = strings.TrimSpace(system + "\nRespond with a single valid JSON value and nothing else.")
  	}

  	// 2. Call the routed chat model (BYOK per-org), falling back to the bound
  	// default — same routing as runScript. Build a one-shot SimpleAgent.
  	model, _ := w.routedChatModel(ctx, c.projectID)
  	if model == nil {
  		return "", fmt.Errorf("worker: custom llm: no chat model available")
  	}
  	agent := coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
  		Name: "custom-llm", SystemPrompt: system,
  	})
  	res, err := agent.Run(ctx, user)
  	if err != nil {
  		return "", fmt.Errorf("worker: custom llm run: %w", err)
  	}
  	content := res.Answer
  	format := "text"
  	if in.Params.OutputFormat == "json" {
  		var probe any
  		if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &probe); err != nil {
  			// JSON parse failure ⇒ execution failure (retried by the worker).
  			return "", fmt.Errorf("worker: custom llm expected JSON output: %w", err)
  		}
  		content = strings.TrimSpace(content)
  		format = "json"
  	}

  	// 3. Land the output in node_outputs (INSERT, pure $N).
  	outID := newID()
  	if err := w.cfg.DB.WithContext(ctx).Exec(
  		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
  		 VALUES ($1,$2,$3,$4,$5,$6)`,
  		outID, c.projectID, c.todoID, c.typ, content, format).Error; err != nil {
  		return "", fmt.Errorf("worker: insert node_output: %w", err)
  	}
  	return "custom:" + outID, nil
  }

  // substituteVars replaces every {{name}} occurrence with its resolved value.
  func substituteVars(tpl string, vars map[string]string) string {
  	out := tpl
  	for name, val := range vars {
  		out = strings.ReplaceAll(out, "{{"+name+"}}", val)
  	}
  	return out
  }

  // resolveOutputText is the single ref→text seam: "script:<id>" → scripts.content_json
  // text; "custom:<id>" → node_outputs.content. asset:/shots: refs are binary/fan-out
  // and are a validation error here (A: custom nodes read only text outputs).
  func (w *Worker) resolveOutputText(ctx context.Context, outputRef string) (string, error) {
  	switch {
  	case strings.HasPrefix(outputRef, "script:"):
  		id := strings.TrimPrefix(outputRef, "script:")
  		var contentJSON []byte
  		if err := w.cfg.DB.WithContext(ctx).Raw(
  			`SELECT content_json FROM scripts WHERE id=$1`, id).Row().Scan(&contentJSON); err != nil {
  			return "", fmt.Errorf("worker: load script %s: %w", id, err)
  		}
  		return string(contentJSON), nil
  	case strings.HasPrefix(outputRef, "custom:"):
  		id := strings.TrimPrefix(outputRef, "custom:")
  		var content string
  		if err := w.cfg.DB.WithContext(ctx).Raw(
  			`SELECT content FROM node_outputs WHERE id=$1`, id).Row().Scan(&content); err != nil {
  			return "", fmt.Errorf("worker: load node_output %s: %w", id, err)
  		}
  		return content, nil
  	case strings.HasPrefix(outputRef, "asset:"), strings.HasPrefix(outputRef, "shots:"):
  		return "", fmt.Errorf("worker: output_ref %q is binary/fan-out, not a text source", outputRef)
  	default:
  		return "", fmt.Errorf("worker: unknown output_ref %q", outputRef)
  	}
  }
  ```
- [ ] Create `internal/worker/worker_custom_test.go` (in-process, no DB-gating where possible). Since `runCustomLLM`/`resolveOutputText` touch the DB, gate them on `LLM_AGENT_STUDIO_PG_URL` mirroring other worker DB tests; for `substituteVars` write a plain unit test (no DB). Provide a mock `llm.ChatModel` via the worker's `Router` so `routedChatModel` returns it (or, if the worker exposes a test seam, inject directly). Minimum tests:
  - `TestSubstituteVars` (pure): `{{draft}}` replaced; unknown placeholders left intact.
  - `TestResolveOutputText_ScriptAndCustom` (DB-gated, fresh DB): insert a `scripts` row + a `node_outputs` row, assert `resolveOutputText("script:<id>")` and `("custom:<id>")` return the stored content; assert `("asset:x")` errors.
  - `TestRunCustomLLM_TextAndJSON` (DB-gated): seed a script todo + its `scripts` row (text), a typed custom todo whose `input_json` has `variables:[{name:"draft",sourceTodoId:<scriptTodo>}]`, run with a mock chat model returning a fixed string; assert a `node_outputs` row is written with substituted prompt reflected (or just that the row exists + format), and that `outputFormat:"json"` with a non-JSON model answer returns an error.

  (Reuse the worker package's existing test scaffolding for constructing a `Worker` with a mock router/chat model — search `internal/worker/*_test.go` for the existing mock chat model and `worker.New(Config{...})` test setup and mirror it.)
- [ ] Run: `GOWORK=off go build ./internal/worker/...` then `GOWORK=off go test ./internal/worker/... -run TestSubstituteVars -count=1` (pure), then fresh-DB run for the DB-gated ones.
  - Expected: builds; `TestSubstituteVars` passes; DB-gated pass with a fresh DB.
- [ ] Commit: `feat(worker): runCustom llm executor + resolveOutputText + node_outputs`

---

### Task 7: Run handlers — gate flip + org-scoped resolution + PlanCustom rewiring

**Files:**
- Modify: `internal/httpapi/handlers.go` (`PlannerPort` ~72; `runHandler` ~377-458)
- Modify: `internal/httpapi/workflowhandlers.go` (`runWorkflowHandler` ~131-199)
- Modify: `internal/httpapi/httpapi.go` (Deps + route wiring; pass resolver)
- Test: `internal/httpapi/customnodetypehandlers_test.go` (covers gate; see Task 9)

- [ ] In `handlers.go`, update `PlannerPort.PlanCustom` to the new signature:
  ```go
  	PlanCustom(ctx context.Context, projectID, workflowID string, b planner.Brief, nodes []planner.WorkflowNode, resolved map[string]planner.ResolvedType) (planner.Result, error)
  ```
- [ ] Add a resolver interface to `handlers.go` (or `httpapi.go`) for org-scoped registry reads:
  ```go
  // CustomNodeTypeResolver resolves a typed custom node's registry entry, org-scoped
  // (satisfied by *customnodetype.Store via a thin adapter). nil → typed nodes are
  // rejected at run time (treated as unresolvable).
  type CustomNodeTypeResolver interface {
  	Get(ctx context.Context, id, orgID string) (customnodetype.CustomNodeType, error)
  }
  ```
  (Import `customnodetype` in the httpapi package.)
- [ ] Add a shared helper in `handlers.go` to build the `resolved` map from nodes + org, used by both run handlers:
  ```go
  // resolveCustomTypes reads each typed node's (typeId) registry entry org-scoped and
  // returns kind+params so PlanCustom can build input_json. Variable bindings are NOT
  // resolved here — they live on the node's VarBindings and PlanCustom merges them.
  // The handler holds org context (T2); the planner never reads the registry.
  func resolveCustomTypes(ctx context.Context, res CustomNodeTypeResolver, orgID string, nodes []planner.WorkflowNode) (map[string]planner.ResolvedType, error) {
  	resolved := map[string]planner.ResolvedType{}
  	for _, n := range nodes {
  		if n.TypeId == "" {
  			continue
  		}
  		if res == nil {
  			return nil, fmt.Errorf("custom node %q references a type but registry is unavailable", n.ID)
  		}
  		ct, err := res.Get(ctx, n.TypeId, orgID)
  		if err != nil {
  			return nil, fmt.Errorf("custom node %q: resolve type %q: %w", n.ID, n.TypeId, err)
  		}
  		resolved[n.ID] = planner.ResolvedType{Kind: ct.Kind, Params: ct.Params}
  	}
  	return resolved, nil
  }
  ```
- [ ] In `runHandler` (handlers.go): replace the `planner.HasCustomNode(customNodes)` gate (line ~405) with:
  ```go
  		if planner.HasUnboundCustomNode(customNodes) {
  			http.Error(w, "当前 Workflow 包含未绑定类型的自定义节点，请先在注册表中为其指定类型后再运行", http.StatusBadRequest)
  			return
  		}
  ```
  Then before the `pl.PlanCustom` call (line ~429), build `resolved` and pass it:
  ```go
  		if p.CustomWorkflowEnabled {
  			resolved, rerr := resolveCustomTypes(r.Context(), customTypeResolver, p.OrgID, customNodes)
  			if rerr != nil {
  				http.Error(w, rerr.Error(), http.StatusBadRequest)
  				return
  			}
  			res, err = pl.PlanCustom(r.Context(), id, "", brief, customNodes, resolved)
  		} else {
  			// ... unchanged Plan/PlanWith branch ...
  		}
  ```
  Add `customTypeResolver CustomNodeTypeResolver` as a new parameter to `runHandler` (and thread it through the route registration).
- [ ] In `runWorkflowHandler` (workflowhandlers.go): same gate flip (line ~166 → `HasUnboundCustomNode`), add `res CustomNodeTypeResolver` param, build `resolved` after the gate, and pass it into `PlanCustom` (line ~186):
  ```go
  		if planner.HasUnboundCustomNode(nodes) {
  			http.Error(w, "当前 Workflow 包含未绑定类型的自定义节点，请先在注册表中为其指定类型后再运行", http.StatusBadRequest)
  			return
  		}
  		// ... quota + SetStatus + planner_started unchanged ...
  		resolved, rerr := resolveCustomTypes(r.Context(), res, p.OrgID, nodes)
  		if rerr != nil {
  			http.Error(w, rerr.Error(), http.StatusBadRequest)
  			return
  		}
  		result, err := pl.PlanCustom(r.Context(), id, wfID, brief, nodes, resolved)
  ```
  (rename the local `res` planner.Result to avoid clashing with the resolver param — e.g. keep resolver named `res` and Result named `result`, updating the trailing writeJSON accordingly.)
- [ ] In `httpapi.go`: add `CustomNodeType CustomNodeTypeResolver` (or reuse a combined store dep) to `Deps`; pass it into both run-handler registrations:
  ```go
  	mux.Handle("POST /api/projects/{id}/run", proj(roleEditor, runHandler(d.Projects, d.Planner, d.Events, d.Cost, d.GenQuota, d.ChatRouter, d.CustomNodeType)))
  	...
  		mux.Handle("POST /api/projects/{id}/workflows/{wfId}/run", proj(roleEditor, runWorkflowHandler(d.Projects, d.Workflows, d.Planner, d.Events, d.Cost, d.GenQuota, d.CustomNodeType)))
  ```
- [ ] Update the in-package fake planner used by existing httpapi tests (search `internal/httpapi/*_test.go` for a type implementing `PlannerPort`) to the new `PlanCustom` signature.
- [ ] **Clean the `HasCustomNode` orphan (deviation #3, final step).** Now that BOTH run-gate sites are flipped to `HasUnboundCustomNode`, grep: `GOWORK=off grep -rn 'HasCustomNode' internal/ web/ cmd/`. If the only remaining references are `planner.HasCustomNode`'s definition + `TestHasCustomNode`, REMOVE both (orphan of our own change — house rule). If any other live (non-test) caller exists, KEEP it and record where in the commit message.
- [ ] Run: `GOWORK=off go build ./internal/httpapi/... ./internal/planner/... ./cmd/...` then `GOWORK=off go test ./internal/planner/... -run TestHasUnboundCustomNode -count=1`
  - Expected: build errors only where `cmd/studiod/main.go` doesn't yet pass `CustomNodeType` (fixed in Task 8). Build `./internal/httpapi/...` + `./internal/planner/...` clean first; planner predicate test green.
- [ ] Commit: `feat(httpapi): flip run gate to unbound + org-scoped typed resolution`

---

### Task 8: Wire registry store in `cmd/studiod/main.go`

**Files:**
- Modify: `cmd/studiod/main.go` (construct store ~near other stores; add to `httpapi.Deps` ~336-347)

- [ ] Near where `workflowStore`/`storageConfigStore` are constructed, add:
  ```go
  	customNodeTypeStore := customnodetype.New(st.GORM())
  ```
  (Add the import `"github.com/costa92/llm-agent-studio/internal/customnodetype"`.)
- [ ] In the `httpapi.Deps{...}` literal, add:
  ```go
  		CustomNodeType: customNodeTypeStore,
  ```
- [ ] Run: `GOWORK=off go build ./...`
  - Expected: whole module builds clean.
- [ ] Commit: `feat(studiod): wire customnodetype store into httpapi deps`

---

### Task 9: Registry CRUD HTTP endpoints

TDD: handler test first (table-driven, in-package fake store — no DB needed for handler-shape tests; gate/202 path can use a fake planner).

**Files:**
- Create: `internal/httpapi/customnodetypehandlers.go`
- Modify: `internal/httpapi/httpapi.go` (routes ~after storage-config routes)
- Create: `internal/httpapi/customnodetypehandlers_test.go`

- [ ] Create `internal/httpapi/customnodetypehandlers.go` mirroring `storagehandlers.go`:
  ```go
  package httpapi

  import (
  	"context"
  	"encoding/json"
  	"errors"
  	"net/http"

  	"github.com/costa92/llm-agent-studio/internal/customnodetype"
  )

  // CustomNodeTypeStore is the registry HTTP surface (satisfied by *customnodetype.Store).
  type CustomNodeTypeStore interface {
  	List(ctx context.Context, orgID string) ([]customnodetype.CustomNodeType, error)
  	Create(ctx context.Context, orgID string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error)
  	Update(ctx context.Context, id, orgID string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error)
  	Delete(ctx context.Context, id, orgID string) error
  	Get(ctx context.Context, id, orgID string) (customnodetype.CustomNodeType, error)
  }

  type customNodeTypeBody struct {
  	Label  string          `json:"label"`
  	Color  string          `json:"color"`
  	Kind   string          `json:"kind"`
  	Params json.RawMessage `json:"params"`
  }

  func (b customNodeTypeBody) toInput() customnodetype.UpsertInput {
  	return customnodetype.UpsertInput{Label: b.Label, Color: b.Color, Kind: b.Kind, Params: b.Params}
  }

  func listCustomNodeTypesHandler(s CustomNodeTypeStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		items, err := s.List(r.Context(), r.PathValue("org"))
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusInternalServerError)
  			return
  		}
  		if items == nil {
  			items = []customnodetype.CustomNodeType{}
  		}
  		writeJSON(w, http.StatusOK, map[string]any{"items": items})
  	}
  }

  func createCustomNodeTypeHandler(s CustomNodeTypeStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		var b customNodeTypeBody
  		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
  			http.Error(w, "bad request", http.StatusBadRequest)
  			return
  		}
  		ct, err := s.Create(r.Context(), r.PathValue("org"), b.toInput())
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusBadRequest)
  			return
  		}
  		writeJSON(w, http.StatusOK, ct)
  	}
  }

  func updateCustomNodeTypeHandler(s CustomNodeTypeStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		var b customNodeTypeBody
  		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
  			http.Error(w, "bad request", http.StatusBadRequest)
  			return
  		}
  		ct, err := s.Update(r.Context(), r.PathValue("id"), r.PathValue("org"), b.toInput())
  		if errors.Is(err, customnodetype.ErrNotFound) {
  			http.Error(w, "not found", http.StatusNotFound)
  			return
  		}
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusBadRequest)
  			return
  		}
  		writeJSON(w, http.StatusOK, ct)
  	}
  }

  func deleteCustomNodeTypeHandler(s CustomNodeTypeStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		err := s.Delete(r.Context(), r.PathValue("id"), r.PathValue("org"))
  		if errors.Is(err, customnodetype.ErrInUse) {
  			http.Error(w, "该类型被工作流节点引用，请先移除引用再删除", http.StatusConflict)
  			return
  		}
  		if errors.Is(err, customnodetype.ErrNotFound) {
  			http.Error(w, "not found", http.StatusNotFound)
  			return
  		}
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusInternalServerError)
  			return
  		}
  		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
  	}
  }
  ```
- [ ] In `httpapi.go`, after the storage-config routes (~line 232), register (role: editor — node types are authored by the same role that edits workflows; keep consistent with workflow routes which are `roleEditor`):
  ```go
  	if d.CustomNodeType != nil {
  		mux.Handle("GET /api/orgs/{org}/custom-node-types", scoped(roleViewer, orgScope, listCustomNodeTypesHandler(d.CustomNodeType)))
  		mux.Handle("POST /api/orgs/{org}/custom-node-types", scoped(roleEditor, orgScope, createCustomNodeTypeHandler(d.CustomNodeType)))
  		mux.Handle("PUT /api/orgs/{org}/custom-node-types/{id}", scoped(roleEditor, orgScope, updateCustomNodeTypeHandler(d.CustomNodeType)))
  		mux.Handle("DELETE /api/orgs/{org}/custom-node-types/{id}", scoped(roleEditor, orgScope, deleteCustomNodeTypeHandler(d.CustomNodeType)))
  	}
  ```
  Change the `Deps` field type to the combined store: `CustomNodeType CustomNodeTypeStore` and have `CustomNodeTypeResolver`'s `Get` be satisfied by the same `*customnodetype.Store` (it has `Get`). In `httpapi.go` where `runHandler`/`runWorkflowHandler` receive the resolver, pass `d.CustomNodeType` (a `CustomNodeTypeStore` also satisfies `CustomNodeTypeResolver` because it has `Get`). To keep types clean, make `CustomNodeTypeStore` embed/duplicate `Get` (already listed above) — pass `d.CustomNodeType` for both purposes.
- [ ] Create `internal/httpapi/customnodetypehandlers_test.go`: in-package fake store; assert `POST` returns the created DTO, `DELETE` maps `ErrInUse`→409 and `ErrNotFound`→404, cross-org `Update` (fake returns `ErrNotFound`)→404. Add a run-gate test using a fake planner + fake project store: a project with one annotation custom node (`type:"custom:x"`, no typeId) → `POST /run` returns 400; a project with one typed node whose fake resolver returns a valid llm type → 202 (assert `PlanCustom` was called with a non-nil `resolved[node]`). Mirror existing `internal/httpapi/*_test.go` request-construction helpers.
- [ ] Run: `GOWORK=off go test ./internal/httpapi/... -count=1`
  - Expected: `ok`.
- [ ] Commit: `feat(httpapi): registry CRUD endpoints + run-gate tests`

---

### Task 10: Run-view minimal — `node_outputs` surfaced in `GraphNode` (T3)

**Files:**
- Modify: `internal/projectstate/state.go` (`GraphNode` ~38; `Input`/`Asset`-style output plumbing; `buildGraph` ~383)
- Modify: `internal/project/store.go` (`LoadState` ~579: join `node_outputs`)
- Test: `internal/projectstate/state_test.go` (add a buildGraph output case)

- [ ] In `state.go`, add `Output` to `GraphNode`:
  ```go
  	AssetID string `json:"assetId,omitempty"` // asset 节点的产物 id,供右栏预览
  	// Output 是 custom 节点 (node_outputs) 的文本/JSON 产物,供运行视图选中面板渲染 (T3)。
  	Output string `json:"output,omitempty"`
  	// OutputFormat ∈ "text"|"json" (Output 非空时有意义)。
  	OutputFormat string `json:"outputFormat,omitempty"`
  ```
- [ ] Add an `Output`/`Format` carrier to `Input` keyed by todo id:
  ```go
  // NodeOutput is one custom node's produced text/JSON output (joined by todo id).
  type NodeOutput struct {
  	TodoID string
  	Content string
  	Format string
  }
  ```
  Add `Outputs []NodeOutput` to `Input` (~line 110).
- [ ] In `Compute`, build a `map[todoID]NodeOutput` and pass it into `buildGraph`; change `buildGraph` to accept it and set `n.Output`/`n.OutputFormat` for matching todos:
  ```go
  	outputByTodo := map[string]NodeOutput{}
  	for _, o := range in.Outputs {
  		outputByTodo[o.TodoID] = o
  	}
  	st.Nodes, st.Edges = buildGraph(in.Todos, assetByTodo, outputByTodo)
  ```
  and in `buildGraph` after the `assetByTodo` lookup:
  ```go
  		if o, ok := outputByTodo[t.ID]; ok {
  			n.Output = o.Content
  			n.OutputFormat = o.Format
  		}
  ```
- [ ] In `internal/project/store.go` `LoadState`, after loading todos (~line 567), load `node_outputs` for the resolved plan's todos and append to `in.Outputs`:
  ```go
  	// custom 节点产物 (node_outputs)，按本 plan 的 todo 关联 (T3 运行视图最小面板)。
  	norows, err := s.db.WithContext(ctx).Raw(
  		`SELECT no.todo_id, no.content, no.format
  		 FROM node_outputs no
  		 JOIN todos t ON no.todo_id = t.id
  		 WHERE t.plan_id=$1`, planRowID).Rows()
  	if err != nil {
  		return projectstate.ProjectState{}, fmt.Errorf("project: load node outputs: %w", err)
  	}
  	defer norows.Close()
  	for norows.Next() {
  		var o projectstate.NodeOutput
  		if err := norows.Scan(&o.TodoID, &o.Content, &o.Format); err != nil {
  			return projectstate.ProjectState{}, fmt.Errorf("project: scan node output: %w", err)
  		}
  		in.Outputs = append(in.Outputs, o)
  	}
  	if err := norows.Err(); err != nil {
  		return projectstate.ProjectState{}, fmt.Errorf("project: node outputs rows: %w", err)
  	}
  ```
- [ ] In `state_test.go`, add `TestBuildGraph_CustomOutput`: a custom todo with a matching `Input.Outputs` entry → the resulting `GraphNode.Output`/`OutputFormat` are set; a node without an output has empty `Output`.
- [ ] Run: `GOWORK=off go test ./internal/projectstate/... -count=1` then `GOWORK=off go build ./internal/project/...`
  - Expected: pure projectstate tests pass; project builds.
- [ ] Commit: `feat(projectstate): surface custom node_outputs on GraphNode (T3)`

---

### Task 11: Frontend types + registry react-query API

**Files:**
- Modify: `web/src/lib/types.ts` (add registry DTOs + `LlmParams`)
- Modify: `web/src/lib/projectState.ts` (add `output?`/`outputFormat?` to `GraphNode`)
- Create: `web/src/features/custom-node-types/api.ts`

- [ ] In `web/src/lib/types.ts` add:
  ```ts
  // custom_node_types/store.go CustomNodeType。组织级 typed 自定义节点注册表条目。
  export interface CustomNodeType {
    id: string
    orgId: string
    slug: string
    label: string
    color: string
    kind: "llm"
    params: LlmParams
  }

  // llm kind 参数 (组织级)。NO variables — 变量名隐含于 {{name}} 模板，
  // 绑定 (name→sourceNodeId) 存在节点实例的 varBindings 上 (per-node, workflow-local)。
  export interface LlmParams {
    systemPrompt?: string
    userPrompt: string
    model?: string
    temperature?: number
    outputFormat?: "text" | "json"
  }

  // POST/PUT 入参：/api/orgs/{org}/custom-node-types[/{id}]。
  export interface UpsertCustomNodeTypeInput {
    label: string
    color: string
    kind: "llm"
    params: LlmParams
  }
  ```
- [ ] In `web/src/lib/projectState.ts`, add to `GraphNode`: `output?: string` and `outputFormat?: "text" | "json"` (mirror the Go `omitempty` tags).
- [ ] Create `web/src/features/custom-node-types/api.ts` mirroring an existing react-query feature api (e.g. `web/src/features/prompt/api.ts` or `web/src/features/storage-config/api.ts`): `useCustomNodeTypes(org)`, `useCreateCustomNodeType(org)`, `useUpdateCustomNodeType(org)`, `useDeleteCustomNodeType(org)` hitting the Task-9 endpoints; invalidate the list query on mutations. Use the repo's existing fetch wrapper.
- [ ] Run: `cd web && npx tsc -b --noEmit` (or `npm run build`'s typecheck step)
  - Expected: no type errors in the new files.
- [ ] Commit: `feat(web): registry DTOs + react-query api`

---

### Task 12: Frontend — Rich llm param form + registry manager UI

TDD: form test first.

**Files:**
- Create: `web/src/features/custom-node-types/LlmParamForm.tsx`
- Create: `web/src/features/custom-node-types/LlmParamForm.test.tsx`
- Create: `web/src/features/custom-node-types/CustomNodeTypeManager.tsx`
- Create: `web/src/features/custom-node-types/CustomNodeTypeManager.test.tsx`

- [ ] Create `LlmParamForm.test.tsx`: render `<LlmParamForm value={...} onChange={spy}/>` (NO `upstreamNodes` prop — this is the org-level type editor, it has no workflow context); assert editing systemPrompt/userPrompt/temperature calls `onChange` with the updated `LlmParams`; assert toggling outputFormat to `json` updates `outputFormat`. There are NO variable-binding rows here (variable binding is per-node, in PropertiesPanel — Task 13).
- [ ] Run (expect FAIL): `cd web && npx vitest run src/features/custom-node-types/LlmParamForm.test.tsx`
- [ ] Create `LlmParamForm.tsx`: a controlled form (props `value: LlmParams`, `onChange` — NO `upstreamNodes`). Fields: systemPrompt (Textarea), userPrompt (Textarea), model (Input, optional), temperature (number Input, optional), outputFormat (Select text|json). NO variable→source rows (org-level form has no workflow context). Reuse `@/components/ui/*` primitives as in `PropertiesPanel.tsx`.
- [ ] Run (expect PASS): same vitest command.
- [ ] Create `CustomNodeTypeManager.test.tsx`: with a mocked api (`vi.mock("./api")`), assert the list renders types; clicking 新建 opens a dialog with the Rich form (kind fixed to `llm`); submitting calls `useCreateCustomNodeType`'s mutate with `{label, color, kind:"llm", params}`; delete calls the delete mutation; a 409 surfaces an in-use message.
- [ ] Create `CustomNodeTypeManager.tsx`: org-scoped page — list (label chip + color + kind), 新建/编辑 dialog wrapping `LlmParamForm` + label/color (reuse `CUSTOM_PALETTE` from `workflow-canvas/nodeColor`), delete with confirm. Route it under the org settings area mirroring how `storage-config` management is surfaced (find the existing org-settings route registration and add a sibling entry/tab).
- [ ] Run: `cd web && npx vitest run src/features/custom-node-types/`
  - Expected: all green.
- [ ] Commit: `feat(web): registry manager + Rich llm param form`

---

### Task 13: Frontend — canvas dual-type entry + PropertiesPanel typed display

**Files:**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts` (`createNode`/`addNodeAt` accept `typeId`; `collectCustomTypes` already covers annotation; add typed source)
- Modify: `web/src/features/workflow-canvas/NodePalette.tsx` + `NodeTypePicker.tsx` (list org typed types alongside annotation types; picking a typed type passes its `typeId`)
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx` (fetch org typed types; merge into palette/picker `customTypes`; on typed pick set `typeId`)
- Modify: `web/src/features/workflow-canvas/PropertiesPanel.tsx` (typed node shows kind+params read-only / link to edit type)
- Test: extend `canvasModel.test.ts`, `NodeTypePicker.test.tsx`, `PropertiesPanel.test.tsx`

- [ ] Extend `addNodeAt`/`createNode`/`insertNodeOnEdge` `display` arg to optionally carry `typeId` (e.g. `display?: { label?: string; color?: string; typeId?: string }`) and write `...(display?.typeId ? { typeId: display.typeId } : {})` onto the `WorkflowNode`. Add a `canvasModel.test.ts` case: creating a node with `display.typeId` produces a node whose `data.node.typeId` is set (and `toStudioNodes` preserves it — already covered by Task 3, but assert through `createNode` here).
- [ ] In `WorkflowCanvas.tsx`: fetch org typed types via `useCustomNodeTypes(org)`; build a merged `customTypes` list = annotation types (from `collectCustomTypes(rfNodes)`) + typed registry types mapped to `{ type: "custom:"+slug, label, color, typeId: id }`. Pass to `NodePalette`/`NodeTypePicker`. When a typed type is picked/dropped, call the create helpers with `display.typeId` set so the new node is typed. (Annotation types keep the Phase-1 path — no typeId.) Distinguish in the picker which entries are typed vs annotation (e.g. a small badge); on pick of a typed entry pass its `typeId`.
- [ ] In `PropertiesPanel.tsx`: THIS is where per-node variable binding lives (PropertiesPanel has workflow context — it knows the node's upstream/DependsOn nodes; the org-level registry form does not). For a typed node (`node.typeId`):
  - Parse the resolved type's template (`systemPrompt` + `userPrompt`) for unique `{{name}}` tokens → render ONE binding row per name, each with an upstream-node Select (options = the nodes this node dependsOn — the simplest correct ancestor set). Selecting a source writes `varBindings: [{ name, sourceNodeId }]` onto the node (merge/replace by name). Pre-fill each row from the node's existing `varBindings`.
  - Also show the type's kind + a compact read-only params summary (systemPrompt/userPrompt/outputFormat), plus an "编辑类型" link that opens the registry manager / edit dialog for that `typeId` (cascades to all nodes of that type via the registry).
  - Keep annotation nodes on the existing path.
  - Add a `PropertiesPanel.test.tsx` case: a typed node whose template has `{{draft}}` renders a binding row; selecting an upstream node sets `node.varBindings` to `[{ name: "draft", sourceNodeId: <upstream> }]`. Add another asserting a typed node renders its kind label and does NOT render the prompt-library Select (that's built-in only).
- [ ] Run: `cd web && npx vitest run src/features/workflow-canvas/`
  - Expected: all green.
- [ ] Commit: `feat(web): canvas dual-type entry + typed PropertiesPanel`

---

### Task 14: Frontend — run-gating predicate + run-view output panel

**Files:**
- Modify: `web/src/features/workflow-canvas/canvasModel.ts` (`hasCustomNode` → `hasUnboundCustomNode`)
- Modify: `web/src/features/workflow-canvas/WorkflowCanvas.tsx` (`runDisabled` uses unbound predicate; message)
- Modify: `web/src/features/workflow-canvas/RunCanvas.tsx` / `WorkflowNode.tsx` (surface `runOverlay` node `output` text/JSON in the selected-node panel)
- Modify: `web/src/features/workflow-canvas/runOverlay.ts` (`RunNodeStatus` carries `output`/`outputFormat`)
- Test: extend `canvasModel.test.ts`, `runOverlay.test.ts`

- [ ] In `canvasModel.ts`, add (and keep `hasCustomNode` if referenced elsewhere, or replace its callers):
  ```ts
  // 画布是否含未绑定 (annotation) 自定义节点（用于禁运行；typed 节点放行）。
  export function hasUnboundCustomNode(rfNodes: RFNode[]): boolean {
    return rfNodes.some((n) => isCustomType(n.data.node.type) && !n.data.node.typeId)
  }
  ```
  Add a `canvasModel.test.ts` case: a typed node (with `typeId`) → `hasUnboundCustomNode` false; an annotation node → true.
- [ ] In `WorkflowCanvas.tsx`, change `runDisabled` (line ~752) to `hasUnboundCustomNode(rfNodes as RFNode[])` and update the title/message (line ~788) to "含未绑定类型的自定义节点 · 暂不支持运行".
- [ ] In `runOverlay.ts`, add `output?: string` and `outputFormat?: "text" | "json"` to `RunNodeStatus` and populate them from `rn.output`/`rn.outputFormat` in `runByTypeOrdinal`. Add a `runOverlay.test.ts` case asserting a state node carrying `output` flows to the overlay map.
- [ ] In the run-view selected-node panel (find where `RunCanvas.tsx`/`WorkflowNode.tsx` renders the selected run node's `assetId` preview), add a text/JSON block: when `run.output` is present, render it as preformatted text; if `run.outputFormat === "json"`, render it in a monospace/`<pre>` block (no heavy artifact UX — minimal per T3).
- [ ] Run: `cd web && npx vitest run src/features/workflow-canvas/`
  - Expected: all green.
- [ ] Commit: `feat(web): unbound run-gate + minimal run-view output panel`

---

### Task 15: Full build + test sweep + manual verification

**Files:** none (verification only).

- [ ] Run backend build + unit tests (non-DB): `GOWORK=off go build ./... && GOWORK=off go test ./internal/planner/... ./internal/projectstate/... ./internal/httpapi/... -run 'TestHasUnboundCustomNode|TestBuildGraph_CustomOutput|TestSubstituteVars' -count=1`
  - Expected: build clean; listed tests pass.
- [ ] Run DB-gated suites against a FRESH DB (single connection): `LLM_AGENT_STUDIO_PG_URL=postgres://...freshdb GOWORK=off go test ./internal/customnodetype/... ./internal/planner/... ./internal/worker/... -count=1 -p 1`
  - Expected: `ok` for all (stale data trips `(org_id, slug)` / `assets_todo_uniq` — use a clean DB).
- [ ] Run web tests: `cd web && npm test`
  - Expected: all suites pass.
- [ ] Manual (`:5173`, see memory `reference_studio-dev-runtime.md` to start studiod :8083 + Vite :5173): create a typed `llm` type (systemPrompt + `{{draft}}` userPrompt + a variable bound to an upstream `script` node) → on the canvas drop a `script` node + the typed node, connect script→typed → 保存 → 运行 → confirm the run completes and the typed node's `node_outputs` text shows in the run-view panel. Then add an annotation custom node (no typeId) → confirm 运行 is disabled with the unbound message. Confirm a pure built-in workflow still runs (regression).
- [ ] Commit (if any verification-driven fixups): `chore: phase2a verification fixups`

---

## Notes for the executor

- `GOWORK=off` on EVERY go command (umbrella `go.work` would otherwise mask the standalone studio module).
- DB-gated Go tests need a FRESH DB and `-p 1` (parallel migrate race + stale-data uniqueness collisions are documented repo hazards).
- studio changes land via branch → push → PR → rebase merge (no direct push to main; see memory `feedback_studio-changes-via-pr.md`). Do not commit to `main` directly.
- The GORM house rules are non-negotiable: INSERT…RETURNING (never `gorm.Create`), no `AutoMigrate`, pure `$N` Raw, NULL columns marshaled via `[]byte`/`pq.StringArray`, multi-statement under `db.Transaction`.

## Pre-execution review nits (GO-WITH-NITS gate, 2026-06-23)

Fresh-eyes pre-flight confirmed the plan GO against real code. Handle these small items while executing:

1. **(Task 6) `runCustomLLM` requires a `Router`.** Unlike `runScript` (which falls back to the bound `w.cfg.Script.Run` when `routedChatModel` returns `ok=false`), the llm executor needs a routed model. A worker started without a `Router` (legacy/non-BYOK) cannot run custom llm nodes. Add a one-line comment that this path requires a Router; make the "no chat model available" failure fast and clearly worded (don't silently retry-to-exhaustion as a vague error). Phase 2A assumes BYOK, so acceptable.
2. **(Task 6) `SELECT COALESCE(output_ref,'')` is redundant** — `todos.output_ref` is `TEXT NOT NULL DEFAULT ''` (storage.go:121). Harmless; don't infer output_ref can be NULL.
3. **(Task 6) `resolveOutputText("script:<id>")` returns the raw `scripts.content_json` blob** (serialized `ScriptOutput`), not extracted prose. Defensible A-scope choice, but `{{draft}}` will inject a JSON blob. If Task 15's `:5173` output looks JSON-y, that's the known shape, not a bug.
4. **(Task 6 test) Mock-model seam = `scriptedRouterModel` (`worker/router_test.go:191`, an `llm.ScriptedLLM`) wired via `modelrouter.New(Config{BuildChat: ...})` (router_test.go:79-83).** `routedChatModel` returns `(nil,false)` if `cfg.Router==nil`, so the test MUST construct a `Router`, not just set a model field.
5. **(Task 7) `res` name collision in `runWorkflowHandler`** — it already names the PlanCustom result `res` (workflowhandlers.go:186). Use distinct names and update the trailing `writeJSON` consuming the old `res`. Grep the function body after editing.
6. **(Task 4/7) Actually run the `HasCustomNode` orphan grep**: `GOWORK=off grep -rn 'HasCustomNode' internal/ web/ cmd/`. Remove `HasCustomNode`+`TestHasCustomNode` only if the sole callers were the two now-flipped gate sites. The frontend `hasCustomNode` (canvasModel.ts:441) is a SEPARATE TS function Task 14 handles — don't conflate.
7. **(Task 10) `node_outputs.todo_id` is a bare TEXT (no FK, intentional/additive)**; the LoadState join `JOIN todos t ON no.todo_id=t.id WHERE t.plan_id=$1` mirrors the assets join and is correct (append before `return Compute(in)`).
