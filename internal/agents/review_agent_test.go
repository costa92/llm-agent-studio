package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

// reviewCaptureModel records the system prompt so the适龄维度 can be asserted.
type reviewCaptureModel struct {
	llm.ScriptedLLM
	gotSys string
}

func (m *reviewCaptureModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	m.gotSys = req.SystemPrompt
	return llm.Response{Text: `{"score":80,"flags":[],"note":""}`}, nil
}

// TestReviewAgent_AgeBandAddsAgeAppropriateness: a non-empty AgeBand layers an
// 适龄性 dimension into the prompt naming the band.
func TestReviewAgent_AgeBandAddsAgeAppropriateness(t *testing.T) {
	m := &reviewCaptureModel{}
	if _, err := NewReviewAgent(m).Run(context.Background(), ReviewInput{Prompt: "x", AgeBand: "3-6"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(m.gotSys, "3-6") || !strings.Contains(m.gotSys, "适龄") {
		t.Fatalf("age-appropriateness dimension missing from prompt: %q", m.gotSys)
	}
}

// TestReviewAgent_NoAgeBandUnchanged: an empty AgeBand keeps the original prompt
// (零回归) — no适龄措辞 leaks in.
func TestReviewAgent_NoAgeBandUnchanged(t *testing.T) {
	m := &reviewCaptureModel{}
	if _, err := NewReviewAgent(m).Run(context.Background(), ReviewInput{Prompt: "x"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if m.gotSys != reviewSystemPrompt {
		t.Fatalf("empty AgeBand altered the prompt: %q", m.gotSys)
	}
}

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

func TestReviewAgentRunWithUsesPassedModel(t *testing.T) {
	bound := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{"score":10,"flags":[],"note":"bound"}`}))
	routed := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{"score":90,"flags":[],"note":"routed"}`}))
	a := NewReviewAgent(bound)
	out, err := a.RunWith(context.Background(), routed, ReviewInput{Prompt: "x"})
	if err != nil {
		t.Fatalf("runWith: %v", err)
	}
	if out.Score != 90 {
		t.Fatalf("RunWith ignored the passed model: score=%d", out.Score)
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
