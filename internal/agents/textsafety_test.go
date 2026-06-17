package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

func TestNarrationSafetyUnsafe(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: `{"safe":false,"reason":"暴力"}`},
	))
	v, err := NewNarrationSafety(model).Check(context.Background(), "他用刀砍向对方", "3-6")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if v.Safe {
		t.Fatalf("want Safe=false, got %+v", v)
	}
	if v.Reason != "暴力" {
		t.Fatalf("want reason 暴力, got %q", v.Reason)
	}
}

func TestNarrationSafetySafe(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: `{"safe":true}`},
	))
	v, err := NewNarrationSafety(model).Check(context.Background(), "小白兔在草地上玩耍", "3-6")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !v.Safe {
		t.Fatalf("want Safe=true, got %+v", v)
	}
}

// ageCaptureModel records the user prompt so the age band + narration text can
// be asserted (SimpleAgent puts the Run prompt into the user message turn).
type ageCaptureModel struct {
	llm.ScriptedLLM
	gotUser string
}

func (m *ageCaptureModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			m.gotUser = msg.Content
		}
	}
	return llm.Response{Text: `{"safe":true}`}, nil
}

func TestNarrationSafetyPromptCarriesAgeBand(t *testing.T) {
	m := &ageCaptureModel{}
	if _, err := NewNarrationSafety(m).Check(context.Background(), "旁白", "7-9"); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(m.gotUser, "7-9") {
		t.Fatalf("prompt missing age band: %q", m.gotUser)
	}
}
