package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
)

// StoryboardInput carries the upstream script (raw JSON) + style.
type StoryboardInput struct {
	ScriptJSON   string
	Style        string
	SystemPrompt string

	// 儿童绘本变体：PictureBook 为 true 时按「绘本跨页」分镜——每页产出朗读旁白
	// （复用 Shot.Action）+ 插图描述（复用 Shot.Prompt），相机/场景/时长不强求。
	PictureBook         bool
	PBMaxWordsPerSpread int    // 每页旁白字数上限（rune 计数），>0 才截断
	PBIllustrationStyle string // 插图统一风格，如 "watercolor"
	PBCharacterSheet    string // 主角固定外观，写进插图前缀以保证跨页一致
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

// pictureBookStoryboardSystemPrompt 是绘本变体的分镜 prompt：把脚本拆成「跨页」，
// 第一页为封面（action 留空），中间为内容页，最后一页为结尾页。每页 action 写该页的
// 朗读旁白，prompt 写插图描述。{{MAXWORDS}} 给每页旁白字数上限，{{ILLUSTRATION}} 是
// 插图统一前缀（风格 + 主角固定外观）。JSON 形状与标准分镜一致，便于下游复用。
const pictureBookStoryboardSystemPrompt = `你是一名儿童绘本分镜师。请把脚本（JSON）拆成「跨页」：第一页是封面（其 "action" 必须留空），中间是若干内容页，最后一页是结尾页。
每页的 "action" 写该页的朗读旁白（不超过 {{MAXWORDS}} 个字），"prompt" 写该页插图描述。
每页 "prompt" 都必须以这段插图前缀开头，确保跨页风格与主角外观一致：{{ILLUSTRATION}}
产出一个 JSON 对象，EXACTLY 为以下形状，不要有别的内容：
{"shots":[{"shotNo":int,"camera":string,"scene":string,"action":string,"prompt":string,"duration":int}]}
只输出该 JSON 对象。`

// pictureBookStoryboardSystemPromptFor 用绘本输入填充上面的占位符。
func pictureBookStoryboardSystemPromptFor(in StoryboardInput) string {
	maxWords := "适量"
	if in.PBMaxWordsPerSpread > 0 {
		maxWords = fmt.Sprintf("%d", in.PBMaxWordsPerSpread)
	}
	illustration := strings.TrimSpace(in.PBIllustrationStyle + " " + in.PBCharacterSheet)
	r := strings.NewReplacer("{{MAXWORDS}}", maxWords, "{{ILLUSTRATION}}", illustration)
	return r.Replace(pictureBookStoryboardSystemPrompt)
}

// NewStoryboardAgent builds a StoryboardAgent over the given model.
func NewStoryboardAgent(model llm.ChatModel) *StoryboardAgent {
	return &StoryboardAgent{model: model}
}

// RunWith is Run with an explicit model (BYOK 模型路由): the worker resolves the
// org's text model through the ModelRouter and passes it here. Run keeps the
// bound default for un-routed callers.
func (a *StoryboardAgent) RunWith(ctx context.Context, model llm.ChatModel, in StoryboardInput) (StoryboardOutput, error) {
	// Same contract as the script agent: the JSON-shape instruction is mandatory;
	// a per-node custom prompt augments it as guidance, never replaces it (else
	// the shots JSON can't be parsed).
	base := storyboardSystemPrompt
	if in.PictureBook {
		base = pictureBookStoryboardSystemPromptFor(in)
	}
	sysPrompt := base
	if in.SystemPrompt != "" {
		sysPrompt = base +
			"\n\nAdditional creative guidance (apply it, but ALWAYS keep the exact JSON output format described above):\n" +
			strings.TrimSpace(in.SystemPrompt)
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
	// 绘本变体：对每页旁白按 rune 计数硬截断（模型可能超出 prompt 里给的上限）。
	if in.PBMaxWordsPerSpread > 0 {
		for i := range out.Shots {
			r := []rune(out.Shots[i].Action)
			if len(r) > in.PBMaxWordsPerSpread {
				out.Shots[i].Action = string(r[:in.PBMaxWordsPerSpread])
			}
		}
	}
	return out, nil
}

// Run produces a StoryboardOutput. Empty/unparseable returns an error.
func (a *StoryboardAgent) Run(ctx context.Context, in StoryboardInput) (StoryboardOutput, error) {
	return a.RunWith(ctx, a.model, in)
}
