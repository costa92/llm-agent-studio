package events

import (
	"context"
	"os"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run events tests")
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
	projID := "ev_" + randHex()
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return New(st.Pool()), projID
}

func TestAppendAndList(t *testing.T) {
	s, projID := newStore(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, projID, "planner_started", "", nil); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := s.Append(ctx, projID, "todo_ready", "t1", map[string]any{"type": "script"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	evs, err := s.List(ctx, projID, "", 0, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[0].Kind != "planner_started" || evs[1].Kind != "todo_ready" {
		t.Fatalf("bad order/kind: %+v", evs)
	}
	if evs[1].Seq <= evs[0].Seq {
		t.Fatalf("seq not monotonic: %+v", evs)
	}
	// afterSeq filters.
	tail, err := s.List(ctx, projID, "", evs[0].Seq, 100)
	if err != nil {
		t.Fatalf("list tail: %v", err)
	}
	if len(tail) != 1 || tail[0].Kind != "todo_ready" {
		t.Fatalf("afterSeq filter wrong: %+v", tail)
	}
}

func TestAppendRunDoneDedupsPerRun(t *testing.T) {
	s, pid := newStore(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, pid, "planner_started", "", nil); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, ok, err := s.AppendRunDone(ctx, pid); err != nil || !ok {
		t.Fatalf("first run_done should insert: ok=%v err=%v", ok, err)
	}
	// Second emit within the SAME run is deduped (M1 carry: Workers>1 could
	// both see allDone and double-emit).
	if _, ok, err := s.AppendRunDone(ctx, pid); err != nil || ok {
		t.Fatalf("second run_done should dedup: ok=%v err=%v", ok, err)
	}
	// A re-run (new planner_started) opens a new dedup window.
	if _, err := s.Append(ctx, pid, "planner_started", "", nil); err != nil {
		t.Fatalf("append rerun: %v", err)
	}
	if _, ok, err := s.AppendRunDone(ctx, pid); err != nil || !ok {
		t.Fatalf("run_done after re-plan should insert again: ok=%v err=%v", ok, err)
	}
}
