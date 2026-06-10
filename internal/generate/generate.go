// Package generate is the unified media-generation seam (spec §7.2): AssetAgent
// calls a MediaGenerator without knowing image/video/audio. M2 ships the image
// adapter (wrapping contract/llm.ImageGenerator) + a fake + a registry that
// resolves model_configs (provider+model) to a generator. Video/audio are M4.
package generate

import "context"

// GenRequest is a media-generation request. Prompt is the fully-built prompt
// (PromptBuilder has already injected the style suffix). Size/Quality/Format
// map to the underlying provider knobs.
type GenRequest struct {
	Prompt  string
	N       int
	Size    string
	Quality string
	Format  string
}

// GenResult is one produced asset. Exactly one of Bytes / URL is the primary
// payload (Bytes when the provider returns inline; URL after pull-to-blob the
// adapter sets Bytes). Tokens/ImageCount feed the generations ledger.
type GenResult struct {
	Bytes      []byte
	URL        string
	MimeType   string
	Provider   string
	Model      string
	Tokens     int
	ImageCount int
	LatencyMS  int
}

// MediaGenerator is the seam every asset generator implements (spec §7.2).
type MediaGenerator interface {
	Kind() string // "image" | "video" | "audio"
	Generate(ctx context.Context, req GenRequest) (GenResult, error)
}
