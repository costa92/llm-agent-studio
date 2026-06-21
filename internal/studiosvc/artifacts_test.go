package studiosvc

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func TestArtifactsReadTodosScriptShots(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, _ := storage.Open(ctx, storage.Config{PGURL: dsn})
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()
	projID := "art_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ('t1',$1,'pl','script','done')`, projID)
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ('sc1',$1,'t1','{"title":"X"}')`, projID)
	_, _ = pool.Exec(ctx, `INSERT INTO shots (id, project_id, script_id, todo_id, shot_no) VALUES ('sh1',$1,'sc1','t1',1)`, projID)

	a := NewArtifacts(st.GORM())
	todos, err := a.Todos(ctx, projID, "")
	if err != nil || len(todos) != 1 {
		t.Fatalf("todos: %v len=%d", err, len(todos))
	}
	script, ok, err := a.Script(ctx, projID, "", "")
	if err != nil || !ok || string(script) == "" {
		t.Fatalf("script: %v ok=%v", err, ok)
	}
	shots, err := a.Shots(ctx, projID, "", "")
	if err != nil || len(shots) != 1 {
		t.Fatalf("shots: %v len=%d", err, len(shots))
	}
}

func TestScript_ScopedByTodo(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, _ := storage.Open(ctx, storage.Config{PGURL: dsn})
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()

	projID := "scp_" + randHexSvc()
	planID := "pl_" + randHexSvc()
	todoA := "ta_" + randHexSvc()
	todoB := "tb_" + randHexSvc()

	_, _ = pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID)
	_, _ = pool.Exec(ctx, `INSERT INTO plans (id, project_id) VALUES ($1,$2)`, planID, projID)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'script','done')`, todoA, projID, planID)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'script','done')`, todoB, projID, planID)
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ($1,$2,$3,'{"title":"A"}')`, "sc_a_"+randHexSvc(), projID, todoA)
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ($1,$2,$3,'{"title":"B"}')`, "sc_b_"+randHexSvc(), projID, todoB)

	a := NewArtifacts(st.GORM())

	// Scoped by todoA: must return A's content only.
	content, ok, err := a.Script(ctx, projID, planID, todoA)
	if err != nil || !ok {
		t.Fatalf("Script(todoA): err=%v ok=%v", err, ok)
	}
	if !strings.Contains(string(content), `"A"`) || strings.Contains(string(content), `"B"`) {
		t.Fatalf("Script(todoA) returned wrong content: %s", content)
	}

	// Scoped by todoB: must return B's content only.
	content, ok, err = a.Script(ctx, projID, planID, todoB)
	if err != nil || !ok {
		t.Fatalf("Script(todoB): err=%v ok=%v", err, ok)
	}
	if !strings.Contains(string(content), `"B"`) || strings.Contains(string(content), `"A"`) {
		t.Fatalf("Script(todoB) returned wrong content: %s", content)
	}

	// Empty todoID: existing behavior (latest for project) still works.
	content, ok, err = a.Script(ctx, projID, "", "")
	if err != nil || !ok || string(content) == "" {
		t.Fatalf("Script(no todoId): err=%v ok=%v", err, ok)
	}
}

func TestShots_ScopedByTodo(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, _ := storage.Open(ctx, storage.Config{PGURL: dsn})
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()

	projID := "shp_" + randHexSvc()
	planID := "pl_" + randHexSvc()
	todoA := "ta_" + randHexSvc()
	todoB := "tb_" + randHexSvc()
	scriptA := "sca_" + randHexSvc()
	scriptB := "scb_" + randHexSvc()

	_, _ = pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID)
	_, _ = pool.Exec(ctx, `INSERT INTO plans (id, project_id) VALUES ($1,$2)`, planID, projID)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'shots','done')`, todoA, projID, planID)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'shots','done')`, todoB, projID, planID)
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ($1,$2,$3,'{}')`, scriptA, projID, todoA)
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ($1,$2,$3,'{}')`, scriptB, projID, todoB)
	// 2 shots for todoA, 3 shots for todoB.
	_, _ = pool.Exec(ctx, `INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, ordering) VALUES ($1,$2,$3,$4,1,1)`, "sh_a1_"+randHexSvc(), projID, scriptA, todoA)
	_, _ = pool.Exec(ctx, `INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, ordering) VALUES ($1,$2,$3,$4,2,2)`, "sh_a2_"+randHexSvc(), projID, scriptA, todoA)
	_, _ = pool.Exec(ctx, `INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, ordering) VALUES ($1,$2,$3,$4,1,1)`, "sh_b1_"+randHexSvc(), projID, scriptB, todoB)
	_, _ = pool.Exec(ctx, `INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, ordering) VALUES ($1,$2,$3,$4,2,2)`, "sh_b2_"+randHexSvc(), projID, scriptB, todoB)
	_, _ = pool.Exec(ctx, `INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, ordering) VALUES ($1,$2,$3,$4,3,3)`, "sh_b3_"+randHexSvc(), projID, scriptB, todoB)

	a := NewArtifacts(st.GORM())

	// Scoped by todoA: must return exactly 2 shots.
	shotsA, err := a.Shots(ctx, projID, planID, todoA)
	if err != nil {
		t.Fatalf("Shots(todoA): %v", err)
	}
	if len(shotsA) != 2 {
		t.Fatalf("Shots(todoA): want 2, got %d", len(shotsA))
	}

	// Scoped by todoB: must return exactly 3 shots.
	shotsB, err := a.Shots(ctx, projID, planID, todoB)
	if err != nil {
		t.Fatalf("Shots(todoB): %v", err)
	}
	if len(shotsB) != 3 {
		t.Fatalf("Shots(todoB): want 3, got %d", len(shotsB))
	}

	// Empty todoID: existing behavior (all shots for project) still works.
	all, err := a.Shots(ctx, projID, "", "")
	if err != nil || len(all) == 0 {
		t.Fatalf("Shots(no todoId): err=%v len=%d", err, len(all))
	}
}

func TestArtifactsAssets(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, _ := storage.Open(ctx, storage.Config{PGURL: dsn})
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()
	pid := "art_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES ($1,'org','p','u')`, pid)
	_, _ = pool.Exec(ctx,
		`INSERT INTO assets (id,project_id,shot_id,type,blob_key,prompt,style,provider,model,status,version)
		 VALUES (md5(random()::text),$1,'s1','image','k','p','国风','fake','m','pending_acceptance',1)`, pid)
	ar := NewArtifacts(st.GORM())
	items, err := ar.Assets(ctx, pid, "", "")
	if err != nil || len(items) != 1 {
		t.Fatalf("assets: %v len=%d", err, len(items))
	}
	if items[0]["status"] != "pending_acceptance" {
		t.Fatalf("status: %v", items[0]["status"])
	}
	// status filter narrows.
	none, _ := ar.Assets(ctx, pid, "", "accepted")
	if len(none) != 0 {
		t.Fatalf("filter leaked: %d", len(none))
	}
}
