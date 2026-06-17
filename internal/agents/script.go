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
	Brief        string
	ContentType  string
	Platform     string
	Style        string
	SystemPrompt string

	// 儿童绘本变体：PictureBook 为 true 时走面向儿童的故事 prompt，并额外产出
	// 主角固定外观的 characterSheet。其余字段为绘本的生成参数。
	PictureBook bool
	PBAgeBand   string   // 年龄段，如 "3-6"
	PBBookType  string   // 书籍类型，如 "narrative" / "concept"
	PBThemes    []string // 主题，如 ["友谊", "勇气"]
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
	// CharacterSheet 仅绘本变体填充：主角固定外观（物种/主色/服饰/特征逐项）。
	CharacterSheet string `json:"characterSheet,omitempty"`
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

// pictureBookSystemPrompt 是绘本变体的故事 prompt：面向儿童、分页清晰，并额外
// 要求逐项输出主角固定外观的 characterSheet。{{AGE}}/{{TYPE}}/{{THEMES}} 由
// pictureBookSystemPromptFor 填入。JSON 契约里多了 characterSheet 字段。
const pictureBookSystemPrompt = `你是一名儿童绘本作家。请为 {{AGE}} 岁儿童写一个浅显易懂、分页清晰的故事，主题：{{THEMES}}，按 {{TYPE}} 的结构组织。语言简单、温暖、适龄。
额外要求：输出 characterSheet 字段，逐项写明主角的固定外观（物种 / 主色 / 服饰 / 特征），确保跨页一致。
产出一个 JSON 对象，EXACTLY 为以下形状，不要有别的内容：
{"title":string,"logline":string,"characterSheet":string,"scenes":[{"heading":string,"description":string,"dialogue":string}]}
只输出该 JSON 对象。不要前后缀文字，不要 markdown 代码块。`

// pictureBookSystemPromptFor 用绘本输入填充 pictureBookSystemPrompt 的占位符。
func pictureBookSystemPromptFor(in ScriptInput) string {
	age := in.PBAgeBand
	if age == "" {
		age = "3-6"
	}
	bookType := in.PBBookType
	if bookType == "" {
		bookType = "narrative"
	}
	themes := strings.Join(in.PBThemes, "、")
	if themes == "" {
		themes = "成长"
	}
	r := strings.NewReplacer("{{AGE}}", age, "{{TYPE}}", bookType, "{{THEMES}}", themes)
	return r.Replace(pictureBookSystemPrompt)
}

// NewScriptAgent builds a ScriptAgent over the given model.
func NewScriptAgent(model llm.ChatModel) *ScriptAgent {
	return &ScriptAgent{model: model}
}

// RunWith is Run with an explicit model (BYOK 模型路由): the worker resolves the
// org's text model through the ModelRouter and passes it here. Run keeps the
// bound default for un-routed callers.
func (a *ScriptAgent) RunWith(ctx context.Context, model llm.ChatModel, in ScriptInput) (ScriptOutput, error) {
	// The structured-output contract (scriptSystemPrompt: the exact JSON shape +
	// "output ONLY the JSON") is MANDATORY — the downstream parser needs it. A
	// per-node custom prompt (workflow promptId/promptText) is layered on as
	// creative guidance, never a replacement: replacing it drops the JSON contract
	// and the script comes back empty (title/scenes missing).
	base := scriptSystemPrompt
	if in.PictureBook {
		base = pictureBookSystemPromptFor(in)
	}
	sysPrompt := base
	if in.SystemPrompt != "" {
		sysPrompt = base +
			"\n\nAdditional creative guidance (apply it, but ALWAYS keep the exact JSON output format described above):\n" +
			strings.TrimSpace(in.SystemPrompt)
	}
	agent := coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
		Name: "script", SystemPrompt: sysPrompt,
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
