// Package audio houses key-gated async audio-generation adapters (spec §8 M4:
// TTS). M4 ships a SKELETON — the AsyncGenerator interface + key-gated
// registration + a not-configured stub. Real SaaS HTTP + credentials are OUT,
// deferred to M5 (marked TODO).
//
// I5 (deliberate divergence, NOT scope creep): spec §8.2/§4.2 describes short
// TTS as a synchronous Generate. M4 ships the TTS skeleton as an AsyncGenerator
// (submit/poll) instead, so the worker's async engine drives video AND audio
// uniformly (zero branch fork). The synchronous short-TTS path is deferred to
// M5; documented in the M4 deferred-items list.
package audio

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-studio/internal/generate"
)

// skeleton implements generate.AsyncGenerator for a real audio (TTS) provider but
// does NOT wire real HTTP in M4 (every call reports not-configured).
type skeleton struct {
	provider string
	model    string
	apiKey   string
}

// NewOpenAITTS builds the OpenAI tts-1 skeleton.
func NewOpenAITTS(apiKey string) *skeleton {
	return &skeleton{provider: "openai", model: "tts-1", apiKey: apiKey}
}

func (s *skeleton) Kind() string { return "audio" }

func (s *skeleton) notConfigured() error {
	return fmt.Errorf("generate.audio: %s/%s real SaaS HTTP not wired in M4 (skeleton; deferred to M5)", s.provider, s.model)
}

// Generate is the sync convenience form — not supported by the M4 skeleton
// (I5: synchronous short-TTS is deferred to M5).
func (s *skeleton) Generate(context.Context, generate.GenRequest) (generate.GenResult, error) {
	return generate.GenResult{}, s.notConfigured()
}

// Submit would POST the TTS job + forward idempotencyKey as the provider
// client-token header. TODO(m5): real HTTP wiring.
func (s *skeleton) Submit(context.Context, generate.GenRequest, string) (generate.SubmitResult, error) {
	return generate.SubmitResult{}, s.notConfigured()
}

// Poll would GET the job status. TODO(m5): real HTTP wiring.
func (s *skeleton) Poll(context.Context, string) (generate.PollResult, error) {
	return generate.PollResult{}, s.notConfigured()
}

// Cancel is the optional provider-side cancel (Q4). TODO(m5): real HTTP; M4 no-op.
func (s *skeleton) Cancel(context.Context, string) error { return nil }
