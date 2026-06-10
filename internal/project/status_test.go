package project

import "testing"

func TestDeriveStatus(t *testing.T) {
	cases := []struct {
		name string
		c    TodoCounts
		want string
	}{
		{"no todos → planning", TodoCounts{}, "planning"},
		{"any running → running", TodoCounts{Total: 3, Running: 1, Done: 1}, "running"},
		{"any ready → running", TodoCounts{Total: 2, Ready: 1, Done: 1}, "running"},
		{"any blocked → running", TodoCounts{Total: 2, Blocked: 1, Done: 1}, "running"},
		{"any failed (terminal) → failed", TodoCounts{Total: 2, Failed: 1, Done: 1}, "failed"},
		{"all done → completed", TodoCounts{Total: 2, Done: 2}, "completed"},
		{"any canceled present, rest done → canceled", TodoCounts{Total: 2, Canceled: 1, Done: 1}, "canceled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeriveStatus(c.c); got != c.want {
				t.Fatalf("DeriveStatus(%+v)=%q want %q", c.c, got, c.want)
			}
		})
	}
}

func TestDeriveStatusReviewWhenPendingAssets(t *testing.T) {
	// All todos done but assets await acceptance → 'review' (spec §6/§7.3).
	got := DeriveStatus(TodoCounts{Total: 3, Done: 3, PendingAssets: 2})
	if got != "review" {
		t.Fatalf("want review, got %q", got)
	}
}

func TestDeriveStatusCompletedWhenNoPending(t *testing.T) {
	got := DeriveStatus(TodoCounts{Total: 3, Done: 3, PendingAssets: 0})
	if got != "completed" {
		t.Fatalf("want completed, got %q", got)
	}
}

func TestDeriveStatusRunningIgnoresPending(t *testing.T) {
	// Active work dominates pending assets.
	got := DeriveStatus(TodoCounts{Total: 3, Running: 1, Done: 1, PendingAssets: 1})
	if got != "running" {
		t.Fatalf("want running, got %q", got)
	}
}
