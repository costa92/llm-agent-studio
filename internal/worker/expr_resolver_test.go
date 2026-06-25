package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	"gorm.io/gorm"
)

// seedExprProject inserts a bare project under orgID and returns its id.
func seedExprProject(t *testing.T, db *gorm.DB, orgID string) string {
	t.Helper()
	var projID string
	if err := db.WithContext(context.Background()).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// TestExprNodeResolver_Scope proves the S-2 cross-tenant invariant + the F1 TOCTOU
// fix: $node["id"] resolves ONLY direct deps, and every read is project-scoped so a
// forged cross-project dep id reads zero rows and fails closed (never another
// tenant's items).
func TestExprNodeResolver_Scope(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run expr resolver scope test")
	}
	ctx := context.Background()
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{}`})))
	db := w.cfg.DB

	projA := seedExprProject(t, db, "org_A")
	projB := seedExprProject(t, db, "org_B")

	seedTodo := func(t *testing.T, projID, dependsOn string) string {
		t.Helper()
		id := newID()
		if dependsOn == "" {
			if err := db.WithContext(ctx).Exec(
				`INSERT INTO todos (id, project_id, plan_id, type, status, input_json)
				 VALUES ($1,$2,'plan-x','custom:llm','running','{}')`,
				id, projID).Error; err != nil {
				t.Fatalf("seed todo: %v", err)
			}
			return id
		}
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,'plan-x','custom:next','running',ARRAY[$3]::text[],'{}')`,
			id, projID, dependsOn).Error; err != nil {
			t.Fatalf("seed todo with dep: %v", err)
		}
		return id
	}

	seedItems := func(t *testing.T, projID, todoID, items string) {
		t.Helper()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
			 VALUES ($1,$2,$3,'custom:llm','c','json',$4)`,
			newID(), projID, todoID, []byte(items)).Error; err != nil {
			t.Fatalf("seed node_output items: %v", err)
		}
	}

	// ---- shared fixtures ----
	// dep1 (project A) — direct dep of execTodo, has items {"title":"Hello A"}.
	dep1 := seedTodo(t, projA, "")
	seedItems(t, projA, dep1, `[{"json":{"title":"Hello A"}}]`)
	execTodo := seedTodo(t, projA, dep1)

	// sibling (project A) — NOT in execTodo.depends_on; give it observable items.
	sibling := seedTodo(t, projA, "")
	seedItems(t, projA, sibling, `[{"json":{"title":"SIBLING_DATA"}}]`)

	// bnode (project B) — has secret items; execTodo2 (project A) forges a dep on it.
	bnode := seedTodo(t, projB, "")
	seedItems(t, projB, bnode, `[{"json":{"secret":"PROJECT_B_DATA"}}]`)
	execTodo2 := seedTodo(t, projA, bnode)

	t.Run("happy", func(t *testing.T) {
		rc := w.exprNodeResolver(ctx, claimed{todoID: execTodo, projectID: projA}, nil)
		items, err := rc.NodeByID(dep1)
		if err != nil {
			t.Fatalf("happy: want err==nil, got %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("happy: want 1 item, got %d", len(items))
		}
		var v struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(items[0].JSON, &v); err != nil {
			t.Fatalf("happy: decode item[0].JSON: %v", err)
		}
		if v.Title != "Hello A" {
			t.Fatalf("happy: want title=Hello A, got %q", v.Title)
		}
	})

	t.Run("out-of-dependsOn rejection", func(t *testing.T) {
		rc := w.exprNodeResolver(ctx, claimed{todoID: execTodo, projectID: projA}, nil)
		items, err := rc.NodeByID(sibling)
		if err == nil {
			t.Fatalf("out-of-dependsOn: want err!=nil (fail-closed), got nil with %d items", len(items))
		}
		if len(items) != 0 {
			t.Fatalf("out-of-dependsOn: want 0 items, got %d", len(items))
		}
	})

	t.Run("forged cross-project dep", func(t *testing.T) {
		rc := w.exprNodeResolver(ctx, claimed{todoID: execTodo2, projectID: projA}, nil)
		items, err := rc.NodeByID(bnode)
		if err == nil {
			t.Fatalf("forged: want err!=nil (fail-closed), got nil")
		}
		if len(items) != 0 {
			t.Fatalf("forged: want 0 items, got %d", len(items))
		}
		// Even ignoring the error, project B's secret must NEVER surface.
		for i, it := range items {
			if strings.Contains(string(it.JSON), "PROJECT_B_DATA") {
				t.Fatalf("forged: PROJECT_B_DATA leaked in item[%d]: %s", i, it.JSON)
			}
		}
	})

	t.Run("fallback path is scoped", func(t *testing.T) {
		// dep2 (project A) — NO node_outputs.items; output_ref → script (project A).
		scriptID := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
			scriptID, projA, newID(), []byte(`{"k":"v"}`)).Error; err != nil {
			t.Fatalf("fallback: seed script: %v", err)
		}
		dep2 := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
			 VALUES ($1,$2,'plan-x','script','done',$3,'{}')`,
			dep2, projA, "script:"+scriptID).Error; err != nil {
			t.Fatalf("fallback: seed dep2 todo: %v", err)
		}
		execTodo3 := seedTodo(t, projA, dep2)

		rc := w.exprNodeResolver(ctx, claimed{todoID: execTodo3, projectID: projA}, nil)
		items, err := rc.NodeByID(dep2)
		if err != nil {
			t.Fatalf("fallback: want err==nil, got %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("fallback: want 1 item, got %d", len(items))
		}
		var v struct {
			K string `json:"k"`
		}
		if err := json.Unmarshal(items[0].JSON, &v); err != nil {
			t.Fatalf("fallback: decode item[0].JSON: %v", err)
		}
		if v.K != "v" {
			t.Fatalf("fallback: want k=v, got %q", v.K)
		}
	})

	t.Run("nonexistent", func(t *testing.T) {
		// A dep id listed in a todo's depends_on but with no todo/data rows.
		ghost := newID()
		execTodo4 := seedTodo(t, projA, ghost)
		rc := w.exprNodeResolver(ctx, claimed{todoID: execTodo4, projectID: projA}, nil)
		items, err := rc.NodeByID(ghost)
		if err == nil {
			t.Fatalf("nonexistent: want err!=nil (fail-closed), got nil")
		}
		if len(items) != 0 {
			t.Fatalf("nonexistent: want 0 items, got %d", len(items))
		}
	})
}
