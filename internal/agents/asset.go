package agents

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/prompt"
)

// AssetInput is one shot to generate an asset for (spec §7.1 AssetAgent).
type AssetInput struct {
	ShotPrompt string // the storyboard shot's image prompt
	Style      string // project style → PromptBuilder suffix
	Size       string // optional provider size hint
}

// AssetOutput is the generated asset payload + usage (worker persists it).
type AssetOutput struct {
	Prompt     string // the fully-built prompt (style injected)
	Bytes      []byte
	URL        string
	MimeType   string
	Provider   string
	Model      string
	Tokens     int
	ImageCount int
	LatencyMS  int
}

// AssetAgent turns a shot + style into a generated asset via PromptBuilder +
// MediaGenerator. It performs NO I/O beyond the generator call (no DB, no blob);
// the worker persists the result (mirrors M1 ScriptAgent/StoryboardAgent purity).
type AssetAgent struct {
	builder *prompt.Builder
	gen     generate.MediaGenerator
}

// NewAssetAgent builds an AssetAgent over a PromptBuilder + a MediaGenerator.
func NewAssetAgent(builder *prompt.Builder, gen generate.MediaGenerator) *AssetAgent {
	return &AssetAgent{builder: builder, gen: gen}
}

// RunWith is Run with an explicit generator (M3 模型路由): the worker resolves
// the org's default model_config through the registry and passes the resolved
// generator here. Run keeps the bound default for un-routed callers.
func (a *AssetAgent) RunWith(ctx context.Context, gen generate.MediaGenerator, in AssetInput) (AssetOutput, error) {
	built := a.builder.Build(in.ShotPrompt, in.Style)
	res, err := gen.Generate(ctx, generate.GenRequest{Prompt: built, N: 1, Size: in.Size})
	if err != nil {
		return AssetOutput{}, fmt.Errorf("asset: generate: %w", err)
	}
	return AssetOutput{
		Prompt: built, Bytes: res.Bytes, URL: res.URL, MimeType: res.MimeType,
		Provider: res.Provider, Model: res.Model, Tokens: res.Tokens,
		ImageCount: res.ImageCount, LatencyMS: res.LatencyMS,
	}, nil
}

// Run builds the prompt then calls the bound generator. The generator error
// propagates so the worker can retry/fail the todo (spec §7.3 step 4).
func (a *AssetAgent) Run(ctx context.Context, in AssetInput) (AssetOutput, error) {
	return a.RunWith(ctx, a.gen, in)
}

// BuildPrompt exposes the PromptBuilder for the async path (the worker submits
// to AsyncGenerator directly, not via Run/RunWith).
func (a *AssetAgent) BuildPrompt(shotPrompt, style string) string {
	return a.builder.Build(shotPrompt, style)
}
