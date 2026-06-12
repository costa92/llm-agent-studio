package agents

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

func TestScriptAgentParsesScenes(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: "```json\n{\"title\":\"Coffee\",\"logline\":\"a cup\",\"scenes\":[{\"heading\":\"INT. CAFE\",\"description\":\"steam rises\",\"dialogue\":\"hi\"}]}\n```",
	}))
	sa := NewScriptAgent(model)
	out, err := sa.Run(context.Background(), ScriptInput{
		Brief: "a short film about coffee", ContentType: "short", Platform: "web", Style: "realistic",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Title != "Coffee" || len(out.Scenes) != 1 {
		t.Fatalf("bad parse: %+v", out)
	}
	if out.Scenes[0].Heading != "INT. CAFE" {
		t.Fatalf("scene heading: %q", out.Scenes[0].Heading)
	}
}

func TestScriptAgentRunWithUsesPassedModel(t *testing.T) {
	// Bound model returns the WRONG title; the passed model returns the right one.
	bound := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"BOUND","logline":"x","scenes":[{"heading":"H","description":"d","dialogue":"l"}]}`,
	}))
	routed := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"ROUTED","logline":"x","scenes":[{"heading":"H","description":"d","dialogue":"l"}]}`,
	}))
	sa := NewScriptAgent(bound)
	out, err := sa.RunWith(context.Background(), routed, ScriptInput{Brief: "b"})
	if err != nil {
		t.Fatalf("runWith: %v", err)
	}
	if out.Title != "ROUTED" {
		t.Fatalf("RunWith ignored the passed model: title=%q", out.Title)
	}
}

func TestScriptAgentMalformedJSONErrors(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "I cannot do that."}))
	sa := NewScriptAgent(model)
	if _, err := sa.Run(context.Background(), ScriptInput{Brief: "x"}); err == nil {
		t.Fatalf("want error on malformed JSON")
	}
}
