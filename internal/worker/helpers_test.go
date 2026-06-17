package worker

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
)

func randHex3() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// newStoryboardAgentWithShots builds a StoryboardAgent backed by a ScriptedLLM
// that returns a shots JSON with n shots (each shot carries a prompt the
// fan-out will hand to an asset todo).
func newStoryboardAgentWithShots(t *testing.T, n int) *studioagents.StoryboardAgent {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"shots":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(fmt.Sprintf(`{"shotNo":%d,"camera":"wide","scene":"s","action":"a","prompt":"shot %d prompt","duration":2}`, i+1, i+1))
	}
	b.WriteString(`]}`)
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: b.String()}))
	return studioagents.NewStoryboardAgent(model)
}

// newPictureBookStoryboardAgent builds a StoryboardAgent whose model returns a
// fixed 3-page 绘本 shot list: a cover (action="" → image only) plus n content
// pages (action set → image + audio). Each shot's prompt is the illustration,
// action is the narration the fan-out hands to the audio asset todo.
func newPictureBookStoryboardAgent(t *testing.T, contentPages int) *studioagents.StoryboardAgent {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"shots":[`)
	// page 1: cover, action MUST be empty (image only).
	b.WriteString(`{"shotNo":1,"camera":"","scene":"封面","action":"","prompt":"封面插图","duration":0}`)
	for i := 0; i < contentPages; i++ {
		b.WriteString(fmt.Sprintf(
			`,{"shotNo":%d,"camera":"","scene":"内容页","action":"第%d页旁白","prompt":"第%d页插图","duration":0}`,
			i+2, i+1, i+1))
	}
	b.WriteString(`]}`)
	// Two identical responses so a re-claimed/re-run storyboard todo still gets a
	// valid shot list — the idempotency guard (not an exhausted model) must be what
	// prevents a second fan-out batch.
	model := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: b.String()}, llm.Response{Text: b.String()}))
	return studioagents.NewStoryboardAgent(model)
}
