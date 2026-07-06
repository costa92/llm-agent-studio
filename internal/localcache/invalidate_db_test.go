package localcache

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestCrossReplicaInvalidation verifies that an Invalidate on one hub reaches a
// peer hub's listener over PG LISTEN/NOTIFY, and that a hub skips its OWN
// NOTIFY (already reloaded synchronously).
func TestCrossReplicaInvalidation(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run invalidation integration test")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	hubA, err := NewHub(pool, dsn, slog.Default())
	if err != nil {
		t.Fatalf("hubA: %v", err)
	}
	hubB, err := NewHub(pool, dsn, slog.Default())
	if err != nil {
		t.Fatalf("hubB: %v", err)
	}

	var aReloads, bReloads atomic.Int64
	hubA.Register("pricing", func() error { aReloads.Add(1); return nil })
	hubB.Register("pricing", func() error { bReloads.Add(1); return nil })

	go func() { _ = hubB.Listen(ctx) }()
	// B does a catch-up reloadAll on connect; wait for LISTEN to be established.
	waitFor(t, func() bool { return bReloads.Load() >= 1 }, 5*time.Second, "B listener connect+reload")
	baseB := bReloads.Load()

	// A writes: local reload + NOTIFY. B must reload via its listener.
	if err := hubA.Invalidate(ctx, "pricing"); err != nil {
		t.Fatalf("A invalidate: %v", err)
	}
	if aReloads.Load() != 1 {
		t.Fatalf("A local reload=%d want 1", aReloads.Load())
	}
	waitFor(t, func() bool { return bReloads.Load() > baseB }, 5*time.Second, "B reload after A invalidate")

	// Self-skip: B's own Invalidate reloads once locally; its listener receives
	// the self-NOTIFY but must skip it (origin match) — so no second increment.
	baseSelf := bReloads.Load()
	if err := hubB.Invalidate(ctx, "pricing"); err != nil {
		t.Fatalf("B invalidate: %v", err)
	}
	time.Sleep(700 * time.Millisecond) // allow a stray listener reload to land if self-skip were broken
	if got := bReloads.Load(); got != baseSelf+1 {
		t.Fatalf("self-skip failed: B reloads=%d want %d (one local, zero from own NOTIFY)", got, baseSelf+1)
	}
}
