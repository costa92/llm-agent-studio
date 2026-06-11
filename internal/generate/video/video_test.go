package video

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/generate"
)

func TestRunwaySkeletonIsAsyncGenerator(t *testing.T) {
	var g generate.MediaGenerator = NewRunway("test-key")
	if g.Kind() != "video" {
		t.Fatalf("Kind = %q, want video", g.Kind())
	}
	ag, ok := g.(generate.AsyncGenerator)
	if !ok {
		t.Fatalf("Runway must implement AsyncGenerator")
	}
	// M4 skeleton: Submit returns a not-configured error (no real SaaS HTTP).
	if _, err := ag.Submit(context.Background(), generate.GenRequest{}, "k"); err == nil {
		t.Fatalf("M4 skeleton Submit must error (real HTTP is M5)")
	}
}

func TestKlingAndVeoSkeletonsAreVideoAsync(t *testing.T) {
	for name, g := range map[string]generate.MediaGenerator{
		"kling": NewKling("k"),
		"veo":   NewVeo("k"),
	} {
		if g.Kind() != "video" {
			t.Fatalf("%s Kind = %q, want video", name, g.Kind())
		}
		ag, ok := g.(generate.AsyncGenerator)
		if !ok {
			t.Fatalf("%s must implement AsyncGenerator", name)
		}
		if _, err := ag.Poll(context.Background(), "job"); err == nil {
			t.Fatalf("%s skeleton Poll must error (real HTTP is M5)", name)
		}
	}
}
