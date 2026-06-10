package studiosvc

import (
	"context"
	"os"
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

	a := NewArtifacts(pool)
	todos, err := a.Todos(ctx, projID)
	if err != nil || len(todos) != 1 {
		t.Fatalf("todos: %v len=%d", err, len(todos))
	}
	script, ok, err := a.Script(ctx, projID)
	if err != nil || !ok || string(script) == "" {
		t.Fatalf("script: %v ok=%v", err, ok)
	}
	shots, err := a.Shots(ctx, projID)
	if err != nil || len(shots) != 1 {
		t.Fatalf("shots: %v len=%d", err, len(shots))
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
	ar := NewArtifacts(pool)
	items, err := ar.Assets(ctx, pid, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("assets: %v len=%d", err, len(items))
	}
	if items[0]["status"] != "pending_acceptance" {
		t.Fatalf("status: %v", items[0]["status"])
	}
	// status filter narrows.
	none, _ := ar.Assets(ctx, pid, "accepted")
	if len(none) != 0 {
		t.Fatalf("filter leaked: %d", len(none))
	}
}
