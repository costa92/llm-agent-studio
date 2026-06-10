package limits

import (
	"testing"
	"time"
)

func TestGuardCapsPerMinute(t *testing.T) {
	g := New(2)
	now := time.Unix(120, 0)
	if !g.AllowAt("u", now) || !g.AllowAt("u", now) {
		t.Fatalf("first two should be allowed")
	}
	if g.AllowAt("u", now) {
		t.Fatalf("third in same minute should be denied")
	}
	if !g.AllowAt("u", now.Add(time.Minute)) {
		t.Fatalf("next minute should reset")
	}
}

func TestZeroDisablesLimiting(t *testing.T) {
	g := New(0)
	for i := 0; i < 100; i++ {
		if !g.Allow("u") {
			t.Fatalf("unlimited guard denied at %d", i)
		}
	}
}
