package worker

import (
	"context"
	"testing"
	"time"
)

// TestClaimSkipsDeletedProjectTodos：软删项目的 todo 不可 claim（spec
// docs/specs/project-delete.md 的双保险——SoftDelete 已级联取消在途 todos，这里
// 直接给 tombstoned 项目种一条 ready todo，模拟 tombstone 与 cancel 之间竞态
// 残留出的可跑行，claim 必须跳过它）。随后把 deleted_at 清空（运维 undelete
// 路径），同一行应重新可 claim——证明先前被挡确因 deleted_at 过滤。
func TestClaimSkipsDeletedProjectTodos(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by,deleted_at)
		 VALUES (md5(random()::text),'org_del','p','u',now()) RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("insert deleted project: %v", err)
	}
	readyID := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','script','ready','{}')`,
		readyID, pid); err != nil {
		t.Fatalf("insert ready todo: %v", err)
	}
	// 中和共享包库里其他 test 残留的可 claim 行（镜像 TestClaimRespectsGlobalGenerationCap
	// 的做法）：推出 claim 窗口，让下面的有界 drain 只会遇到本 test 的行。
	if _, err := pool.Exec(ctx, `
		UPDATE todos
		SET next_run_at = now() + interval '1 hour',
		    locked_until = CASE WHEN status='running' THEN now() + interval '1 hour' ELSE locked_until END
		WHERE project_id <> $1 AND status IN ('ready','running')`, pid); err != nil {
		t.Fatalf("neutralize leftovers: %v", err)
	}

	w := New(Config{DB: assetTestGorm(t), WorkerID: "del-test", Lease: time.Minute})
	for i := 0; i < 20; i++ {
		c, ok, err := w.claim(ctx)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if !ok {
			break
		}
		if c.todoID == readyID {
			t.Fatalf("claimed a todo belonging to a soft-deleted project")
		}
	}

	// undelete（运维 SQL 路径）→ 同一行重新可 claim。
	if _, err := pool.Exec(ctx, `UPDATE projects SET deleted_at=NULL WHERE id=$1`, pid); err != nil {
		t.Fatalf("undelete project: %v", err)
	}
	claimedOurs := false
	for i := 0; i < 20; i++ {
		c, ok, err := w.claim(ctx)
		if err != nil {
			t.Fatalf("claim after undelete: %v", err)
		}
		if !ok {
			break
		}
		if c.todoID == readyID {
			claimedOurs = true
			break
		}
	}
	if !claimedOurs {
		t.Fatalf("undeleted project's todo should be claimable again")
	}
}
