// Package generate is the unified media-generation seam (spec §7.2): AssetAgent
// calls a MediaGenerator without knowing image/video/audio. M2 ships the image
// adapter (wrapping contract/llm.ImageGenerator) + a fake + a registry that
// resolves model_configs (provider+model) to a generator. image + audio (MiniMax
// T2A) are wired real; there is no video adapter (the M5 video/audio skeletons
// were retired) — the async submit→poll engine survives, exercised via FakeAsync.
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

	// M4 加性字段 (image 适配器忽略): 视频/音频时长诉求 (计费 + provider 参数)
	// 与 TTS 音色。AssetAgent.RunWith 仍只填 Prompt/N/Size — image 调用零改。
	DurationSeconds int
	Voice           string
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

// SubmitResult is the outcome of an async Submit: the provider-side job handle
// (lands in assets.external_job_id) + the duration known at submit time (feeds
// the ledger pre-registration estimate; the real seconds come back at Poll).
type SubmitResult struct {
	ExternalJobID string
	Provider      string
	Model         string
	EstSeconds    int
}

// PollStatus is the lifecycle state an async Poll reports.
type PollStatus int

const (
	PollPending PollStatus = iota // job still running → reschedule with backoff
	PollDone                      // complete → Result carries URL/Bytes + real usage
	PollFailed                    // provider reported terminal failure → fail the todo
)

// PollResult is one async poll outcome. Result is filled only when Status==
// PollDone (URL preferred — the worker pulls it through internal/fetch). Err
// carries the provider error string when Status==PollFailed.
type PollResult struct {
	Status PollStatus
	Result GenResult
	Err    string
}

// AsyncGenerator is the optional seam long-running generators implement (spec
// §4.2). A Kind()=="video"|"audio" generator implements it; image does not (the
// worker type-asserts to decide sync vs submit→poll). It still embeds
// MediaGenerator so the registry resolves it uniformly; Generate on an async
// generator is the convenience "Submit then block-poll to completion" form
// (the worker does NOT use it — it drives Submit/Poll across short dispatches).
//
// idempotencyKey (B1) is derived deterministically from the todo id by the
// worker. Real adapters MUST forward it as the provider's client-token /
// idempotency header so a crash-and-reclaim second Submit dedups provider-side;
// the fake echoes it (same key → same jobID).
type AsyncGenerator interface {
	MediaGenerator
	Submit(ctx context.Context, req GenRequest, idempotencyKey string) (SubmitResult, error)
	Poll(ctx context.Context, jobID string) (PollResult, error)
}

// Canceler is the OPTIONAL provider-side cancel seam (Q4). M4 ships a no-op
// default in adapters; real cancel HTTP is deferred to M5. The worker's LOCAL
// cancel (stop polling + terminal-state the submitted asset) does NOT depend on
// this — see project.Cancel + the poll-dispatch cancel detection (spec §5.4).
type Canceler interface {
	Cancel(ctx context.Context, jobID string) error
}
