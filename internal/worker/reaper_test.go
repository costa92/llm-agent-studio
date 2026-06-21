package worker

import (
	"context"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/assets"
)

func TestReapStaleSubmitted(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_reap','p','u') RETURNING id`).Scan(&pid)
	// One stale submitted (submitted_at 1h ago), one fresh.
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,type,status,submitted_at) VALUES
		(md5(random()::text),$1,'video','submitted', now() - interval '1 hour'),
		(md5(random()::text),$1,'video','submitted', now())`, pid)
	n, err := assets.New(assetTestGorm(t)).ReapStaleSubmitted(ctx, time.Now().Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1 (only the stale one)", n)
	}
}
