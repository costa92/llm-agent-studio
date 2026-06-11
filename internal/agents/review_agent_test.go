package agents

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

func TestReviewAgentParsesScore(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: "Here is my assessment:\n```json\n{\"score\":87,\"flags\":[\"minor_blur\"],\"note\":\"prompt-consistent\"}\n```"},
	))
	a := NewReviewAgent(model)
	out, err := a.Run(context.Background(), ReviewInput{
		Prompt: "a teahouse, guofeng style", Style: "国风", Provider: "fake", Model: "m", MimeType: "image/png",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Score != 87 || len(out.Flags) != 1 || out.Flags[0] != "minor_blur" {
		t.Fatalf("parsed = %+v", out)
	}
}

func TestReviewAgentRejectsOutOfRangeScore(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{"score":150,"flags":[],"note":""}`}))
	if _, err := NewReviewAgent(model).Run(context.Background(), ReviewInput{Prompt: "x"}); err == nil {
		t.Fatalf("score outside 0..100 must error")
	}
}

func TestReviewAgentMalformedJSONErrors(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "I refuse to answer."}))
	if _, err := NewReviewAgent(model).Run(context.Background(), ReviewInput{Prompt: "x"}); err == nil {
		t.Fatalf("malformed output must error (worker records prescreen_error, never fails the todo)")
	}
}
