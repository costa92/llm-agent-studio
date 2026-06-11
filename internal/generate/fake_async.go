package generate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// FakeAsync is the video/audio analog of Fake: a deterministic async generator
// (zero network) for sandbox live-verification of the M4 async engine + seam.
// pollsToDone controls how many Poll calls return Pending before Done. The
// jobID is DERIVED from the idempotency key, so the same key yields the same
// jobID (B1 crash-idempotency assertion); the canned result is returned at Done
// (URL can point at an httptest server so the SSRF-safe pull path runs for real).
type FakeAsync struct {
	mu          sync.Mutex
	kind        string         // "video" | "audio"
	pollsToDone int            // Poll calls returning Pending before Done (>=1)
	jobs        map[string]int // jobID → polls seen so far
	result      GenResult
}

// NewFakeAsync builds a FakeAsync. pollsToDone<1 is clamped to 1.
func NewFakeAsync(kind string, pollsToDone int, result GenResult) *FakeAsync {
	if pollsToDone < 1 {
		pollsToDone = 1
	}
	return &FakeAsync{kind: kind, pollsToDone: pollsToDone, jobs: map[string]int{}, result: result}
}

// Kind reports the configured kind.
func (f *FakeAsync) Kind() string { return f.kind }

// jobIDFor derives a stable jobID from the idempotency key (same key → same id).
func jobIDFor(idempotencyKey string) string {
	sum := sha256.Sum256([]byte("fakeasync:" + idempotencyKey))
	return "fakejob_" + hex.EncodeToString(sum[:8])
}

// Submit echoes the idempotency key into a deterministic jobID and reports the
// requested duration as the estimate.
func (f *FakeAsync) Submit(_ context.Context, req GenRequest, idempotencyKey string) (SubmitResult, error) {
	return SubmitResult{
		ExternalJobID: jobIDFor(idempotencyKey),
		Provider:      f.result.Provider,
		Model:         f.result.Model,
		EstSeconds:    req.DurationSeconds,
	}, nil
}

// Poll returns Pending for the first pollsToDone-1 calls, then Done with the
// canned result (its real seconds taken from the configured EstSeconds via the
// done result — the worker reads req duration for billing).
func (f *FakeAsync) Poll(_ context.Context, jobID string) (PollResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[jobID]++
	if f.jobs[jobID] < f.pollsToDone {
		return PollResult{Status: PollPending}, nil
	}
	return PollResult{Status: PollDone, Result: f.result}, nil
}

// Generate is the convenience "submit then block-poll to done" form (single
// pass for non-worker callers / unit tests). The worker never calls it.
func (f *FakeAsync) Generate(ctx context.Context, req GenRequest) (GenResult, error) {
	sub, err := f.Submit(ctx, req, "generate-"+req.Prompt)
	if err != nil {
		return GenResult{}, err
	}
	for i := 0; i < f.pollsToDone+1; i++ {
		pr, perr := f.Poll(ctx, sub.ExternalJobID)
		if perr != nil {
			return GenResult{}, perr
		}
		switch pr.Status {
		case PollDone:
			return pr.Result, nil
		case PollFailed:
			return GenResult{}, fmt.Errorf("generate.fakeasync: failed: %s", pr.Err)
		}
	}
	return GenResult{}, fmt.Errorf("generate.fakeasync: not done after %d polls", f.pollsToDone+1)
}
