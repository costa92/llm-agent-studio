package health

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/dbtest"
)

// TestMain gives the health package its own fresh database. health's checks
// scan the WHOLE DB (orphan_assets / status_divergence aggregate over every
// row, with a 5-id sample cap), so when the full suite runs against one shared
// server DB, sibling packages' rows crowd this package's seeded ids out of the
// sample and the tests fail. A per-package DB restores run-alone isolation.
// When LLM_AGENT_STUDIO_PG_URL is unset the tests skip, so we just run.
func TestMain(m *testing.M) {
	base := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if base == "" {
		os.Exit(m.Run())
	}
	dsn, drop, err := dbtest.CreateFresh(context.Background(), base, "studio_health_test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "health test: %v\n", err)
		os.Exit(1)
	}
	_ = os.Setenv("LLM_AGENT_STUDIO_PG_URL", dsn)
	code := m.Run()
	_ = os.Setenv("LLM_AGENT_STUDIO_PG_URL", base)
	drop()
	os.Exit(code)
}
