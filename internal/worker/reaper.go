package worker

import (
	"context"
	"time"

	"github.com/costa92/llm-agent-studio/internal/assets"
)

// RunOrphanReaper periodically terminal-states stale 'submitted' assets whose
// external job never returned (spec §5.4 M1: prevent permanent strand). TTL is
// derived from the poll budget so it only reaps jobs well past any legitimate
// poll window. Stops on ctx cancel.
func RunOrphanReaper(ctx context.Context, store *assets.Store, interval, ttl time.Duration) {
	if store == nil || interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = store.ReapStaleSubmitted(ctx, time.Now().Add(-ttl))
		}
	}
}
