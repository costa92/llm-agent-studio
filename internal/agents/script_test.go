package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

// captureModel records the system prompt it receives and returns a valid script
// JSON ONLY when the prompt still carries the screenwriter format contract —
// mirroring the dev fakeChatModel which keys off "screenwriter". Lets the test
// prove a custom prompt does not drop the structured-output contract.
type captureModel struct {
	llm.ScriptedLLM
	gotSys string
}

func (m *captureModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	m.gotSys = req.SystemPrompt
	if !strings.Contains(req.SystemPrompt, "screenwriter") {
		return llm.Response{Text: `{"score":80}`}, nil // wrong shape → empty script
	}
	return llm.Response{Text: `{"title":"T","logline":"l","scenes":[{"heading":"H","description":"d","dialogue":"x"}]}`}, nil
}

// TestScriptAgentCustomPromptKeepsFormatContract: a per-node custom system prompt
// must AUGMENT, not replace, the JSON-shape contract — otherwise the script comes
// back empty (the live bug on the run-detail page).
func TestScriptAgentCustomPromptKeepsFormatContract(t *testing.T) {
	m := &captureModel{}
	sa := NewScriptAgent(m)
	out, err := sa.Run(context.Background(), ScriptInput{
		Brief: "b", SystemPrompt: "你是一名资深广告创意编剧，只写广告脚本。",
	})
	if err != nil {
		t.Fatalf("custom prompt should still produce a valid script, got: %v", err)
	}
	if out.Title != "T" || len(out.Scenes) != 1 {
		t.Fatalf("bad parse with custom prompt: %+v", out)
	}
	// The contract (JSON shape) survived AND the custom guidance is present.
	if !strings.Contains(m.gotSys, "scenes") || !strings.Contains(m.gotSys, "screenwriter") {
		t.Fatalf("custom prompt dropped the format contract: %q", m.gotSys)
	}
	if !strings.Contains(m.gotSys, "广告创意编剧") {
		t.Fatalf("custom guidance not layered in: %q", m.gotSys)
	}
}

// pbCaptureModel records the system prompt and returns a script JSON that also
// carries a characterSheet field, so the picture-book branch can be asserted.
type pbCaptureModel struct {
	llm.ScriptedLLM
	gotSys string
}

func (m *pbCaptureModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	m.gotSys = req.SystemPrompt
	return llm.Response{Text: `{"title":"小熊找朋友","logline":"l","characterSheet":"主角：小熊 / 主色：棕 / 服饰：红围巾 / 特征：圆耳朵","scenes":[{"heading":"P1","description":"d","dialogue":"x"}]}`}, nil
}

// TestScriptAgent_PictureBookEmitsCharacterSheet: a picture-book input must
// drive a child-oriented prompt AND surface the structured characterSheet field.
func TestScriptAgent_PictureBookEmitsCharacterSheet(t *testing.T) {
	m := &pbCaptureModel{}
	sa := NewScriptAgent(m)
	out, err := sa.Run(context.Background(), ScriptInput{
		Brief:       "小熊和好朋友的故事",
		PictureBook: true,
		PBAgeBand:   "3-6",
		PBBookType:  "narrative",
		PBThemes:    []string{"友谊", "勇气"},
	})
	if err != nil {
		t.Fatalf("picture-book run: %v", err)
	}
	if out.CharacterSheet == "" {
		t.Fatalf("picture-book output missing characterSheet: %+v", out)
	}
	// The prompt must carry picture-book constraints (age band + child framing).
	if !strings.Contains(m.gotSys, "3-6") || !strings.Contains(m.gotSys, "儿童") {
		t.Fatalf("picture-book constraints missing from prompt: %q", m.gotSys)
	}
}

// TestScriptAgent_StandardUnchanged: a non-picture-book input keeps the original
// screenwriter prompt and does not leak picture-book constraints.
func TestScriptAgent_StandardUnchanged(t *testing.T) {
	m := &captureModel{}
	sa := NewScriptAgent(m)
	out, err := sa.Run(context.Background(), ScriptInput{
		Brief: "a short film about coffee", ContentType: "short", Platform: "web", Style: "realistic",
	})
	if err != nil {
		t.Fatalf("standard run: %v", err)
	}
	if out.CharacterSheet != "" {
		t.Fatalf("standard output should not carry characterSheet: %+v", out)
	}
	if !strings.Contains(m.gotSys, "screenwriter") {
		t.Fatalf("standard prompt dropped screenwriter contract: %q", m.gotSys)
	}
	if strings.Contains(m.gotSys, "儿童") || strings.Contains(m.gotSys, "characterSheet") {
		t.Fatalf("standard prompt leaked picture-book constraints: %q", m.gotSys)
	}
}

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
