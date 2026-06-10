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

func TestScriptAgentMalformedJSONErrors(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "I cannot do that."}))
	sa := NewScriptAgent(model)
	if _, err := sa.Run(context.Background(), ScriptInput{Brief: "x"}); err == nil {
		t.Fatalf("want error on malformed JSON")
	}
}
