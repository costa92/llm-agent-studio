package generate

import (
	"context"
	"fmt"
	"sync"
)

// Fake is the image analog of llm.ScriptedLLM: a deterministic MediaGenerator
// for tests. Cursor advances per Generate call; when looping, a single canned
// result is reused for every call (handy for fan-out over N shots).
type Fake struct {
	mu      sync.Mutex
	results []GenResult
	cursor  int
	loop    bool
}

// NewFake builds a Fake that returns results in order, erroring when exhausted.
func NewFake(results ...GenResult) *Fake { return &Fake{results: results} }

// NewFakeLooping builds a Fake that reuses one result for every call.
func NewFakeLooping(r GenResult) *Fake { return &Fake{results: []GenResult{r}, loop: true} }

// Kind reports "image" (the only M2 fake kind).
func (f *Fake) Kind() string { return "image" }

// Generate returns the next canned result (or loops / errors when exhausted).
func (f *Fake) Generate(_ context.Context, _ GenRequest) (GenResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loop {
		return f.results[0], nil
	}
	if f.cursor >= len(f.results) {
		f.cursor++
		return GenResult{}, fmt.Errorf("generate.fake: exhausted after %d results", len(f.results))
	}
	r := f.results[f.cursor]
	f.cursor++
	return r, nil
}
