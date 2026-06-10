// Package limits is an in-process per-user fixed-window request counter.
package limits

import (
	"sync"
	"time"
)

// Guard caps requests per user per minute.
type Guard struct {
	perMinute int
	mu        sync.Mutex
	buckets   map[string]*bucket
}

type bucket struct {
	minute int64
	count  int
}

// New builds a Guard. perMinute <= 0 disables limiting (unlimited).
func New(perMinute int) *Guard {
	return &Guard{perMinute: perMinute, buckets: map[string]*bucket{}}
}

// Allow reports whether userID may make a request now.
func (g *Guard) Allow(userID string) bool { return g.AllowAt(userID, time.Now()) }

// AllowAt is Allow with an injectable clock (testable).
func (g *Guard) AllowAt(userID string, now time.Time) bool {
	if g.perMinute <= 0 {
		return true
	}
	minute := now.UTC().Unix() / 60
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.buckets[userID]
	if b == nil || b.minute != minute {
		b = &bucket{minute: minute}
		g.buckets[userID] = b
	}
	if b.count >= g.perMinute {
		return false
	}
	b.count++
	return true
}
