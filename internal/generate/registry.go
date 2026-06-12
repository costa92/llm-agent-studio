package generate

import (
	"context"
	"fmt"
	"sync"
)

// Registry resolves a (provider, model) pair from model_configs to a concrete
// MediaGenerator (spec §7.2). A default generator handles the case where no
// exact match is registered (so a fresh org with no model_configs still runs).
type Registry struct {
	mu  sync.RWMutex
	gen map[string]MediaGenerator // key "provider/model"
	def MediaGenerator
}

// NewRegistry builds an empty Registry.
func NewRegistry() *Registry { return &Registry{gen: map[string]MediaGenerator{}} }

func key(provider, model string) string { return provider + "/" + model }

// Register binds a generator to a provider+model.
func (r *Registry) Register(provider, model string, g MediaGenerator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen[key(provider, model)] = g
}

// SetDefault sets the fallback generator used when no exact match exists.
func (r *Registry) SetDefault(g MediaGenerator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.def = g
}

// Default returns the fallback generator set via SetDefault (nil if unset).
// The ModelRouter uses it as the last-resort generator when an org has no
// usable config and no env-keyed adapter matches.
func (r *Registry) Default() MediaGenerator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.def
}

// Resolve returns the generator for provider+model, falling back to the default.
func (r *Registry) Resolve(provider, model string) (MediaGenerator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if g, ok := r.gen[key(provider, model)]; ok {
		return g, nil
	}
	if r.def != nil {
		return r.def, nil
	}
	return nil, fmt.Errorf("generate: no generator for %s/%s and no default", provider, model)
}

// Generate resolves then runs in one call (convenience for the AssetAgent).
func (r *Registry) Generate(ctx context.Context, provider, model string, req GenRequest) (GenResult, error) {
	g, err := r.Resolve(provider, model)
	if err != nil {
		return GenResult{}, err
	}
	return g.Generate(ctx, req)
}
