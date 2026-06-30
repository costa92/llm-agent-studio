package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

// sbCaptureModel records the system prompt it receives and returns a canned
// shot list, so the picture-book branch's prompt construction can be asserted.
type sbCaptureModel struct {
	llm.ScriptedLLM
	gotSys string
	text   string
}

func (m *sbCaptureModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	m.gotSys = req.SystemPrompt
	return llm.Response{Text: m.text}, nil
}

// TestStoryboardAgent_PictureBookCoverAndPrompt: a picture-book input must drive
// a cover + content + ending spread layout, and fold the illustration style +
// character sheet into the prompt sent to the model.
func TestStoryboardAgent_PictureBookCoverAndPrompt(t *testing.T) {
	m := &sbCaptureModel{text: `{"shots":[` +
		`{"shotNo":1,"action":"","prompt":"封面：森林全景"},` +
		`{"shotNo":2,"action":"小白兔出门散步","prompt":"小白兔走在林间小路"},` +
		`{"shotNo":3,"action":"它们成了好朋友","prompt":"两只兔子拥抱"}]}`}
	sb := NewStoryboardAgent(m)
	out, err := sb.Run(context.Background(), StoryboardInput{
		ScriptJSON:          `{"title":"小白兔"}`,
		PictureBook:         true,
		PBIllustrationStyle: "watercolor",
		PBCharacterSheet:    "小白兔,蓝背带裤,长耳",
	})
	if err != nil {
		t.Fatalf("picture-book run: %v", err)
	}
	if len(out.Shots) < 3 {
		t.Fatalf("want >=3 spreads (cover + content + ending), got %d: %+v", len(out.Shots), out)
	}
	if out.Shots[0].Action != "" {
		t.Fatalf("cover spread should have empty narration, got %q", out.Shots[0].Action)
	}
	if !strings.Contains(m.gotSys, "watercolor") {
		t.Fatalf("illustration style missing from prompt: %q", m.gotSys)
	}
	if !strings.Contains(m.gotSys, "小白兔") {
		t.Fatalf("character sheet missing from prompt: %q", m.gotSys)
	}
}

// TestStoryboardAgent_TruncatesOverlongNarration: when PBMaxWordsPerSpread is set,
// each spread's narration (Action) is truncated by rune count.
func TestStoryboardAgent_TruncatesOverlongNarration(t *testing.T) {
	long := strings.Repeat("字", 200)
	m := &sbCaptureModel{text: `{"shots":[{"shotNo":1,"action":"` + long + `","prompt":"p"}]}`}
	sb := NewStoryboardAgent(m)
	out, err := sb.Run(context.Background(), StoryboardInput{
		ScriptJSON:          "{}",
		PictureBook:         true,
		PBMaxWordsPerSpread: 50,
	})
	if err != nil {
		t.Fatalf("truncation run: %v", err)
	}
	if got := len([]rune(out.Shots[0].Action)); got > 50 {
		t.Fatalf("narration not truncated: %d runes", got)
	}
}

// TestStoryboardAgent_TargetPagesClause: PBTargetPages>0 injects a soft "约 N 页"
// page-count constraint into the prompt; =0 leaves it out (no regression).
func TestStoryboardAgent_TargetPagesClause(t *testing.T) {
	const shots = `{"shots":[{"shotNo":1,"action":"","prompt":"p"},{"shotNo":2,"action":"a","prompt":"p"}]}`
	with := &sbCaptureModel{text: shots}
	if _, err := NewStoryboardAgent(with).Run(context.Background(), StoryboardInput{
		ScriptJSON: "{}", PictureBook: true, PBTargetPages: 12,
	}); err != nil {
		t.Fatalf("with-pages run: %v", err)
	}
	if !strings.Contains(with.gotSys, "12 页") {
		t.Fatalf("target-pages clause missing from prompt: %q", with.gotSys)
	}

	without := &sbCaptureModel{text: shots}
	if _, err := NewStoryboardAgent(without).Run(context.Background(), StoryboardInput{
		ScriptJSON: "{}", PictureBook: true, PBTargetPages: 0,
	}); err != nil {
		t.Fatalf("no-pages run: %v", err)
	}
	if strings.Contains(without.gotSys, "页（含封面") {
		t.Fatalf("target-pages clause should be absent when PBTargetPages=0: %q", without.gotSys)
	}
}

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

func TestStoryboardAgentRunWithUsesPassedModel(t *testing.T) {
	bound := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"shots":[{"shotNo":1,"camera":"BOUND","scene":"s","action":"a","prompt":"p","duration":2}]}`,
	}))
	routed := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"shots":[{"shotNo":1,"camera":"ROUTED","scene":"s","action":"a","prompt":"p","duration":2}]}`,
	}))
	sb := NewStoryboardAgent(bound)
	out, err := sb.RunWith(context.Background(), routed, StoryboardInput{ScriptJSON: "{}"})
	if err != nil {
		t.Fatalf("runWith: %v", err)
	}
	if out.Shots[0].Camera != "ROUTED" {
		t.Fatalf("RunWith ignored the passed model: camera=%q", out.Shots[0].Camera)
	}
}

func TestStoryboardAgentEmptyShotsErrors(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{"shots":[]}`}))
	sb := NewStoryboardAgent(model)
	if _, err := sb.Run(context.Background(), StoryboardInput{ScriptJSON: "{}"}); err == nil {
		t.Fatalf("want error on empty shots")
	}
}
