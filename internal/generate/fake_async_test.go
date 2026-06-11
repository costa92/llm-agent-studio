package generate

import (
	"context"
	"testing"
)

func TestFakeAsyncSubmitPollLifecycle(t *testing.T) {
	ctx := context.Background()
	want := GenResult{URL: "https://example/v.mp4", MimeType: "video/mp4", Provider: "fake", Model: "fake-video-async"}
	f := NewFakeAsync("video", 2, want)
	if f.Kind() != "video" {
		t.Fatalf("Kind = %q, want video", f.Kind())
	}
	sub, err := f.Submit(ctx, GenRequest{Prompt: "a city", DurationSeconds: 6}, "idem-1")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if sub.ExternalJobID == "" || sub.EstSeconds != 6 {
		t.Fatalf("submit result = %+v, want non-empty jobID + EstSeconds 6", sub)
	}
	// First poll: still pending (pollsToDone=2).
	pr, err := f.Poll(ctx, sub.ExternalJobID)
	if err != nil || pr.Status != PollPending {
		t.Fatalf("poll#1 = %+v err=%v, want Pending", pr, err)
	}
	// Second poll: done with the canned result.
	pr, err = f.Poll(ctx, sub.ExternalJobID)
	if err != nil || pr.Status != PollDone {
		t.Fatalf("poll#2 = %+v err=%v, want Done", pr, err)
	}
	if pr.Result.URL != want.URL || pr.Result.MimeType != "video/mp4" {
		t.Fatalf("done result = %+v", pr.Result)
	}
}

func TestFakeAsyncIdempotentSubmit(t *testing.T) {
	// B1 (crash idempotency): the SAME idempotency key must yield the SAME
	// jobID (a real provider dedups on its client-token; the fake echoes it),
	// so a reclaim-driven second Submit does not create a second billed job.
	ctx := context.Background()
	f := NewFakeAsync("video", 1, GenResult{URL: "u"})
	a, err := f.Submit(ctx, GenRequest{}, "same-key")
	if err != nil {
		t.Fatalf("submit a: %v", err)
	}
	b, err := f.Submit(ctx, GenRequest{}, "same-key")
	if err != nil {
		t.Fatalf("submit b: %v", err)
	}
	if a.ExternalJobID != b.ExternalJobID {
		t.Fatalf("same idem key must return same jobID: %q vs %q", a.ExternalJobID, b.ExternalJobID)
	}
	c, _ := f.Submit(ctx, GenRequest{}, "other-key")
	if c.ExternalJobID == a.ExternalJobID {
		t.Fatalf("different idem key must return a different jobID")
	}
}

func TestFakeAsyncIsAsyncGenerator(t *testing.T) {
	var g MediaGenerator = NewFakeAsync("audio", 1, GenResult{})
	if _, ok := g.(AsyncGenerator); !ok {
		t.Fatalf("FakeAsync must satisfy AsyncGenerator")
	}
}
