package agents

import (
	"context"
	"encoding/json"
	"fmt"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
)

// ReviewInput is the metadata the prescreen scores (spec §7.1 ReviewAgent,
// M3). NOTE: this is a TEXT-LLM metadata/prompt-consistency review — the
// studio's chat models take no image input (SimpleAgent.Run is string-in;
// the default provider has no vision capability). MimeType is carried so a
// future vision-capable model can be swapped in behind the same input without
// touching the worker (documented seam; do NOT invent vision APIs now).
type ReviewInput struct {
	Prompt   string // the fully-built generation prompt
	Style    string
	Provider string
	Model    string
	MimeType string
}

// ReviewOutput is the advisory prescreen verdict. Score is 0..100 (higher =
// safer/more consistent); Flags name concerns (e.g. "possible_trademark",
// "nsfw_risk"); Note is a one-line rationale. HITL remains the hard gate.
type ReviewOutput struct {
	Score int      `json:"score"`
	Flags []string `json:"flags"`
	Note  string   `json:"note"`
}

const reviewSystemPrompt = `You are a content-safety and quality pre-screener for AI-generated images. You see only the generation METADATA (prompt, style, provider/model), not the pixels. Assess: prompt/style consistency, sensitive or infringing content risk implied by the prompt (violence, nudity, real persons, trademarks, copyrighted characters), and overall production quality risk. Produce a JSON object with EXACTLY this shape and nothing else:
{"score":int,"flags":[string],"note":string}
"score" is 0-100 (100 = clearly safe and on-brief). "flags" lists short snake_case concern tags (empty array if none). Output ONLY the JSON object. No prose, no markdown fences.`

// ReviewAgent runs the advisory prescreen via a single LLM call (same pattern
// as ScriptAgent/StoryboardAgent: SimpleAgent + JSON prompt + tolerant parse).
type ReviewAgent struct {
	model llm.ChatModel // bound default for Run; RunWith routes per-org (BYOK)
}

// NewReviewAgent builds a ReviewAgent over the given model.
func NewReviewAgent(model llm.ChatModel) *ReviewAgent {
	return &ReviewAgent{model: model}
}

// RunWith is Run with an explicit model (BYOK 模型路由): the worker resolves the
// org's text model through the ModelRouter and passes it here. Run keeps the
// bound default for un-routed callers.
func (a *ReviewAgent) RunWith(ctx context.Context, model llm.ChatModel, in ReviewInput) (ReviewOutput, error) {
	agent := coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
		Name: "review", SystemPrompt: reviewSystemPrompt,
	})
	prompt := fmt.Sprintf(
		"Generation prompt: %s\nStyle: %s\nProvider/model: %s/%s\nMime type: %s",
		in.Prompt, in.Style, in.Provider, in.Model, in.MimeType)
	res, err := agent.Run(ctx, prompt)
	if err != nil {
		return ReviewOutput{}, fmt.Errorf("review: generate: %w", err)
	}
	raw, err := extractJSONObject(res.Answer)
	if err != nil {
		return ReviewOutput{}, fmt.Errorf("review: %w", err)
	}
	var out ReviewOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ReviewOutput{}, fmt.Errorf("review: unmarshal: %w", err)
	}
	if out.Score < 0 || out.Score > 100 {
		return ReviewOutput{}, fmt.Errorf("review: score %d outside 0..100", out.Score)
	}
	if out.Flags == nil {
		out.Flags = []string{}
	}
	return out, nil
}

// Run produces a ReviewOutput. Malformed JSON or an out-of-range score returns
// an error — the WORKER decides what to do with it (record prescreen_error and
// move on; a prescreen failure must never fail the asset todo).
func (a *ReviewAgent) Run(ctx context.Context, in ReviewInput) (ReviewOutput, error) {
	return a.RunWith(ctx, a.model, in)
}
