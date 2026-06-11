package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/prompt"
)

func TestAssetAgentBuildsPromptAndGenerates(t *testing.T) {
	fake := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("PNG"), MimeType: "image/png", Provider: "fake", Model: "fake-img",
		Tokens: 20, ImageCount: 1, LatencyMS: 100,
	})
	a := NewAssetAgent(prompt.NewBuilder(), fake)
	out, err := a.Run(context.Background(), AssetInput{ShotPrompt: "a teahouse at dusk", Style: "国风"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.Prompt, "a teahouse at dusk") || !strings.Contains(out.Prompt, "guofeng") {
		t.Fatalf("style not injected into prompt: %q", out.Prompt)
	}
	if string(out.Bytes) != "PNG" || out.MimeType != "image/png" || out.Provider != "fake" || out.Model != "fake-img" {
		t.Fatalf("genresult not surfaced: %+v", out)
	}
	if out.Tokens != 20 || out.ImageCount != 1 {
		t.Fatalf("usage not surfaced: %+v", out)
	}
}

func TestAssetAgentPropagatesGenError(t *testing.T) {
	fake := generate.NewFake() // empty → exhausted on first call
	a := NewAssetAgent(prompt.NewBuilder(), fake)
	if _, err := a.Run(context.Background(), AssetInput{ShotPrompt: "x", Style: ""}); err == nil {
		t.Fatalf("expected error from exhausted generator")
	}
}

func TestAssetAgentRunWithOverridesGenerator(t *testing.T) {
	bound := generate.NewFakeLooping(generate.GenResult{Provider: "bound", Model: "b", Bytes: []byte("X"), ImageCount: 1})
	routed := generate.NewFakeLooping(generate.GenResult{Provider: "routed", Model: "r", Bytes: []byte("Y"), ImageCount: 1})
	a := NewAssetAgent(prompt.NewBuilder(), bound)
	out, err := a.RunWith(context.Background(), routed, AssetInput{ShotPrompt: "a cafe"})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if out.Provider != "routed" {
		t.Fatalf("RunWith must use the explicit generator, got provider %q", out.Provider)
	}
	// Run still uses the bound default.
	out2, err := a.Run(context.Background(), AssetInput{ShotPrompt: "a cafe"})
	if err != nil || out2.Provider != "bound" {
		t.Fatalf("Run must keep the bound generator: %+v err=%v", out2, err)
	}
}
