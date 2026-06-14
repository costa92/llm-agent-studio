package agents

import (
	"context"
	"encoding/json"
	"fmt"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
)

// StoryboardInput carries the upstream script (raw JSON) + style.
type StoryboardInput struct {
	ScriptJSON   string
	Style        string
	SystemPrompt string
}

// Shot is one storyboard shot (persisted as a shots row).
type Shot struct {
	ShotNo   int    `json:"shotNo"`
	Camera   string `json:"camera"`
	Scene    string `json:"scene"`
	Action   string `json:"action"`
	Prompt   string `json:"prompt"`
	Duration int    `json:"duration"`
}

// StoryboardOutput is the parsed shot list.
type StoryboardOutput struct {
	Shots []Shot `json:"shots"`
}

// StoryboardAgent turns a script into a shot list via a single LLM call.
type StoryboardAgent struct {
	model llm.ChatModel // bound default for Run; RunWith routes per-org (BYOK)
}

const storyboardSystemPrompt = `You are a storyboard artist. Given a script (JSON), break it into shots. Produce a JSON object with EXACTLY this shape and nothing else:
{"shots":[{"shotNo":int,"camera":string,"scene":string,"action":string,"prompt":string,"duration":int}]}
"prompt" is a vivid image-generation prompt for the shot. Output ONLY the JSON object.`

// NewStoryboardAgent builds a StoryboardAgent over the given model.
func NewStoryboardAgent(model llm.ChatModel) *StoryboardAgent {
	return &StoryboardAgent{model: model}
}

// RunWith is Run with an explicit model (BYOK 模型路由): the worker resolves the
// org's text model through the ModelRouter and passes it here. Run keeps the
// bound default for un-routed callers.
func (a *StoryboardAgent) RunWith(ctx context.Context, model llm.ChatModel, in StoryboardInput) (StoryboardOutput, error) {
	sysPrompt := storyboardSystemPrompt
	if in.SystemPrompt != "" {
		sysPrompt = in.SystemPrompt
	}
	agent := coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
		Name: "storyboard", SystemPrompt: sysPrompt,
	})
	prompt := fmt.Sprintf("Script JSON:\n%s\n\nStyle: %s", in.ScriptJSON, in.Style)
	res, err := agent.Run(ctx, prompt)
	if err != nil {
		return StoryboardOutput{}, fmt.Errorf("storyboard: generate: %w", err)
	}
	raw, err := extractJSONObject(res.Answer)
	if err != nil {
		return StoryboardOutput{}, fmt.Errorf("storyboard: %w", err)
	}
	var out StoryboardOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return StoryboardOutput{}, fmt.Errorf("storyboard: unmarshal: %w", err)
	}
	if len(out.Shots) == 0 {
		return StoryboardOutput{}, fmt.Errorf("storyboard: no shots produced")
	}
	return out, nil
}

// Run produces a StoryboardOutput. Empty/unparseable returns an error.
func (a *StoryboardAgent) Run(ctx context.Context, in StoryboardInput) (StoryboardOutput, error) {
	return a.RunWith(ctx, a.model, in)
}
