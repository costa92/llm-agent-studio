package agents

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

func TestStoryboardAgentParsesShots(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"cafe","action":"door opens","prompt":"a cafe door opening","duration":3},{"shotNo":2,"camera":"close","scene":"cafe","action":"sip","prompt":"close up sip","duration":2}]}`,
	}))
	sb := NewStoryboardAgent(model)
	out, err := sb.Run(context.Background(), StoryboardInput{ScriptJSON: `{"title":"Coffee"}`, Style: "realistic"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Shots) != 2 || out.Shots[0].Camera != "wide" || out.Shots[1].ShotNo != 2 {
		t.Fatalf("bad parse: %+v", out)
	}
}

func TestStoryboardAgentEmptyShotsErrors(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{"shots":[]}`}))
	sb := NewStoryboardAgent(model)
	if _, err := sb.Run(context.Background(), StoryboardInput{ScriptJSON: "{}"}); err == nil {
		t.Fatalf("want error on empty shots")
	}
}
