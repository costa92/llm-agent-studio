package audio

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/generate"
)

func TestOpenAITTSSkeletonIsAudioAsync(t *testing.T) {
	var g generate.MediaGenerator = NewOpenAITTS("test-key")
	if g.Kind() != "audio" {
		t.Fatalf("Kind = %q, want audio", g.Kind())
	}
	// I5 (deliberate divergence): the TTS skeleton ships as an AsyncGenerator for
	// engine uniformity (synchronous short-TTS path is deferred to M5).
	ag, ok := g.(generate.AsyncGenerator)
	if !ok {
		t.Fatalf("OpenAITTS must implement AsyncGenerator (I5: async for engine uniformity)")
	}
	// M4 skeleton: Submit returns a not-configured error (no real SaaS HTTP).
	if _, err := ag.Submit(context.Background(), generate.GenRequest{Voice: "alloy"}, "k"); err == nil {
		t.Fatalf("M4 skeleton Submit must error (real HTTP is M5)")
	}
}
