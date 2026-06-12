package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
)

// ScriptInput is the project brief the ScriptAgent turns into a script.
type ScriptInput struct {
	Brief       string
	ContentType string
	Platform    string
	Style       string
}

// Scene is one scene of a script.
type Scene struct {
	Heading     string `json:"heading"`
	Description string `json:"description"`
	Dialogue    string `json:"dialogue"`
}

// ScriptOutput is the parsed script artifact (persisted as scripts.content_json).
type ScriptOutput struct {
	Title   string  `json:"title"`
	Logline string  `json:"logline"`
	Scenes  []Scene `json:"scenes"`
}

// ScriptAgent turns a brief into a structured script via a single LLM call
// (SimpleAgent = one Generate, see llm-agent/simple.go). JSON via prompt + R1
// tolerant parsing — providers have no native structured output.
type ScriptAgent struct {
	model llm.ChatModel // bound default for Run; RunWith routes per-org (BYOK)
}

const scriptSystemPrompt = `You are a screenwriter. Given a creative brief, produce a JSON object with EXACTLY this shape and nothing else:
{"title":string,"logline":string,"scenes":[{"heading":string,"description":string,"dialogue":string}]}
Output ONLY the JSON object. No prose, no markdown fences.`

// NewScriptAgent builds a ScriptAgent over the given model.
func NewScriptAgent(model llm.ChatModel) *ScriptAgent {
	return &ScriptAgent{model: model}
}

// RunWith is Run with an explicit model (BYOK 模型路由): the worker resolves the
// org's text model through the ModelRouter and passes it here. Run keeps the
// bound default for un-routed callers.
func (a *ScriptAgent) RunWith(ctx context.Context, model llm.ChatModel, in ScriptInput) (ScriptOutput, error) {
	agent := coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
		Name: "script", SystemPrompt: scriptSystemPrompt,
	})
	prompt := fmt.Sprintf(
		"Brief: %s\nContent type: %s\nTarget platform: %s\nStyle: %s",
		in.Brief, in.ContentType, in.Platform, in.Style)
	res, err := agent.Run(ctx, prompt)
	if err != nil {
		return ScriptOutput{}, fmt.Errorf("script: generate: %w", err)
	}
	raw, err := extractJSONObject(res.Answer)
	if err != nil {
		return ScriptOutput{}, fmt.Errorf("script: %w", err)
	}
	var out ScriptOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ScriptOutput{}, fmt.Errorf("script: unmarshal: %w", err)
	}
	if strings.TrimSpace(out.Title) == "" || len(out.Scenes) == 0 {
		return ScriptOutput{}, fmt.Errorf("script: empty script (title or scenes missing)")
	}
	return out, nil
}

// Run produces a ScriptOutput. Malformed/unparseable JSON returns an error so
// the worker can mark the todo failed (spec §7.3 step 4).
func (a *ScriptAgent) Run(ctx context.Context, in ScriptInput) (ScriptOutput, error) {
	return a.RunWith(ctx, a.model, in)
}
