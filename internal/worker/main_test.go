package worker

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/dbtest"
)

// TestMain gives the worker package its own fresh database. The worker's claim
// drains a GLOBAL queue (`FOR UPDATE SKIP LOCKED ... ORDER BY next_run_at` with
// no org/project filter), so when the full suite runs against one shared server
// DB, a worker test's RunOnce can claim a sibling package's leftover todo and
// the test's own todo never gets processed. A per-package DB restores run-alone
// isolation. When LLM_AGENT_STUDIO_PG_URL is unset the tests skip, so we just run.
func TestMain(m *testing.M) {
	base := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if base == "" {
		os.Exit(m.Run())
	}
	dsn, drop, err := dbtest.CreateFresh(context.Background(), base, "studio_worker_test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker test: %v\n", err)
		os.Exit(1)
	}
	_ = os.Setenv("LLM_AGENT_STUDIO_PG_URL", dsn)
	code := m.Run()
	_ = os.Setenv("LLM_AGENT_STUDIO_PG_URL", base)
	drop()
	os.Exit(code)
}
