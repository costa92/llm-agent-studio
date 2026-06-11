// Package video houses key-gated async video-generation adapters (spec §8 M4
// build-vs-buy: Runway/Kling/Veo). M4 ships SKELETONS — the AsyncGenerator
// interface + key-gated registration + a not-configured stub. Real SaaS HTTP
// (submit/poll REST, each provider's shape differs) + credentials are OUT,
// deferred to M5 (marked TODO). Until then an unkeyed/unwired adapter errors
// cleanly rather than calling a real API; the FakeAsync generator covers the
// engine end-to-end in the sandbox.
package video

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-studio/internal/generate"
)

// skeleton implements generate.AsyncGenerator for a real video provider but does
// NOT wire real HTTP in M4 (every call reports not-configured). One type backs
// Runway/Kling/Veo — they differ only in provider/model labels + (M5) HTTP shape.
type skeleton struct {
	provider string
	model    string
	apiKey   string
}

// NewRunway builds the Runway Gen-3 skeleton.
func NewRunway(apiKey string) *skeleton {
	return &skeleton{provider: "runway", model: "gen-3", apiKey: apiKey}
}

// NewKling builds the Kling skeleton.
func NewKling(apiKey string) *skeleton {
	return &skeleton{provider: "kling", model: "kling-v1", apiKey: apiKey}
}

// NewVeo builds the Google Veo skeleton (key reuses GoogleAPIKey at the call site).
func NewVeo(apiKey string) *skeleton {
	return &skeleton{provider: "google", model: "veo-2", apiKey: apiKey}
}

func (s *skeleton) Kind() string { return "video" }

func (s *skeleton) notConfigured() error {
	return fmt.Errorf("generate.video: %s/%s real SaaS HTTP not wired in M4 (skeleton; deferred to M5)", s.provider, s.model)
}

// Generate is the sync convenience form — not supported by the M4 skeleton.
func (s *skeleton) Generate(context.Context, generate.GenRequest) (generate.GenResult, error) {
	return generate.GenResult{}, s.notConfigured()
}

// Submit would POST the generation job + forward idempotencyKey as the provider
// client-token header. TODO(m5): real HTTP wiring (Runway/Kling/Veo differ).
func (s *skeleton) Submit(context.Context, generate.GenRequest, string) (generate.SubmitResult, error) {
	return generate.SubmitResult{}, s.notConfigured()
}

// Poll would GET the job status. TODO(m5): real HTTP wiring.
func (s *skeleton) Poll(context.Context, string) (generate.PollResult, error) {
	return generate.PollResult{}, s.notConfigured()
}

// Cancel is the optional provider-side cancel (Q4). TODO(m5): real HTTP; M4 no-op.
func (s *skeleton) Cancel(context.Context, string) error { return nil }
