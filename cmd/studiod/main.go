// Command studiod is the AI Studio backend server (M1: text pipeline).
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzsvc "github.com/costa92/llm-agent-authz/service"
	authzstore "github.com/costa92/llm-agent-authz/store"
	authztoken "github.com/costa92/llm-agent-authz/token"
	"github.com/costa92/llm-agent-contract/llm"
	deepseekprovider "github.com/costa92/llm-agent-providers/deepseek"
	googleprovider "github.com/costa92/llm-agent-providers/google"
	minimaxprovider "github.com/costa92/llm-agent-providers/minimax"
	openaiprovider "github.com/costa92/llm-agent-providers/openai"
	volcengineprovider "github.com/costa92/llm-agent-providers/volcengine"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/blob/localfs"
	bloboss "github.com/costa92/llm-agent-studio/internal/blob/oss"
	blobs3 "github.com/costa92/llm-agent-studio/internal/blob/s3"
	"github.com/costa92/llm-agent-studio/internal/config"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/generate"
	genaudio "github.com/costa92/llm-agent-studio/internal/generate/audio"
	genimage "github.com/costa92/llm-agent-studio/internal/generate/image"
	genvideo "github.com/costa92/llm-agent-studio/internal/generate/video"
	"github.com/costa92/llm-agent-studio/internal/httpapi"
	"github.com/costa92/llm-agent-studio/internal/modelrouter"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/obs"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/review"
	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/studiosvc"
	"github.com/costa92/llm-agent-studio/internal/todos"
	"github.com/costa92/llm-agent-studio/internal/worker"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("studiod: config: %v", err)
	}
	ctx := context.Background()
	app, cleanup, err := build(ctx, cfg)
	if err != nil {
		log.Fatalf("studiod: build: %v", err)
	}
	defer cleanup()

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: app}
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		shutCtx, cancel := context.WithTimeout(ctx, cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Printf("studiod: shutdown: %v", err)
		}
	}()
	log.Printf("studiod: listening on %s", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("studiod: serve: %v", err)
	}
	log.Printf("studiod: stopped")
}

// build wires every dependency and returns the root handler + cleanup. Exported
// shape (package-visible) so main_test.go can drive it.
func build(ctx context.Context, cfg config.Config) (http.Handler, func(), error) {
	tp, err := obs.NewTracerProvider(ctx, obs.Config{
		Endpoint: cfg.OTLPEndpoint, Protocol: cfg.OTLPProtocol, Insecure: cfg.OTLPInsecure,
	})
	if err != nil {
		return nil, nil, err
	}

	st, err := storage.Open(ctx, storage.Config{PGURL: cfg.PGURL})
	if err != nil {
		return nil, nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	az := authzstore.New(st.Pool())
	if err := az.Migrate(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}

	model, err := buildModel(cfg)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	model = obs.WrapModel(model, tp) // otel decorator (spec §12)

	projectStore := project.New(st.Pool())
	todoStore := todos.New(st.Pool())
	eventStore := events.New(st.Pool())
	plannerSvc := planner.New(model, todoStore, st.Pool())
	scriptAgent := studioagents.NewScriptAgent(model)
	storyboardAgent := studioagents.NewStoryboardAgent(model)

	// BlobStore (spec §10): localfs (dev), S3/minio (presigned), Alibaba OSS
	// (official SDK), or Tencent COS (S3-compatible → reuses the s3 adapter).
	var blobStore blob.BlobStore
	var blobServer *localfs.Store // non-nil only in localfs mode (回源 handler)
	switch cfg.BlobMode {
	case "s3":
		s3s, err := blobs3.New(blobs3.Config{
			Endpoint: cfg.S3Endpoint, Bucket: cfg.S3Bucket, Region: cfg.S3Region,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, UseSSL: cfg.S3UseSSL,
		})
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		blobStore = s3s
	case "oss":
		o, err := bloboss.New(bloboss.Config{
			Endpoint: cfg.OSSEndpoint, Bucket: cfg.OSSBucket,
			AccessKeyID: cfg.OSSAccessKeyID, AccessKeySecret: cfg.OSSAccessKeySecret,
		})
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		blobStore = o
	case "cos":
		// COS is S3-compatible; reuse the minio-go adapter with COS's
		// virtual-hosted endpoint (always TLS). SecretID → AccessKey.
		c, err := blobs3.New(blobs3.Config{
			Endpoint: cfg.COSEndpointHost(), Bucket: cfg.COSBucket, Region: cfg.COSRegion,
			AccessKey: cfg.COSSecretID, SecretKey: cfg.COSSecretKey, UseSSL: true,
		})
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		blobStore = c
	default:
		lfs := localfs.New(cfg.BlobDir, []byte(cfg.BlobSecret), cfg.BlobPublic)
		blobStore = lfs
		blobServer = lfs
	}

	// Generator registry: build the org-agnostic default image generator from
	// the configured provider (real) or a fake (e2e via generatorOverride).
	registry := generate.NewRegistry()
	gen, err := buildGenerator(cfg)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	gen = obs.WrapGenerator(gen, tp) // otel decorator (spec §12)
	registry.SetDefault(gen)
	// M3 模型路由: register a REAL adapter per catalog entry whose provider has a
	// key configured; un-keyed providers resolve to the env default generator.
	if err := registerImageGenerators(registry, cfg, tp); err != nil {
		st.Close()
		return nil, nil, err
	}
	// M4: key-gated async video/audio adapters (skeletons; real HTTP is M5).
	if err := registerVideoGenerators(registry, cfg, tp); err != nil {
		st.Close()
		return nil, nil, err
	}
	if err := registerAudioGenerators(registry, cfg, tp); err != nil {
		st.Close()
		return nil, nil, err
	}
	if registryHook != nil {
		registryHook(registry) // e2e seam: inject distinguishable fakes
	}

	promptBuilder := prompt.NewBuilder()
	assetStore := assets.New(st.Pool())
	costStore := cost.New(st.Pool())
	// BYOK: per-config api key 静态加密 box (env STUDIO_CONFIG_ENC_KEY)。未配置时
	// 返回 disabled box——存 key 会被拒，但服务仍可启动 (env-only key 老路径不受影响)。
	encBox, err := secretbox.NewBoxFromEnv()
	if err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("studiod: secretbox: %w", err)
	}
	modelStore := models.New(st.Pool(), encBox)

	// BYOK 模型路由 (ModelRouter): resolves an org's stored model_config and builds
	// the matching chat model / media generator via the factories below; falls
	// back to the env-default chat model + the registry for orgs with no config.
	router := modelrouter.New(modelrouter.Config{
		Models:      modelStore,
		Registry:    registry,
		DefaultChat: model, // env-default chat model (already otel-wrapped)
		BuildChat:   buildChatFactory(tp),
		BuildMedia:  buildMediaFactory(tp),
	})

	assetAgent := studioagents.NewAssetAgent(promptBuilder, gen)
	var reviewAgent *studioagents.ReviewAgent
	if cfg.ReviewPrescreen {
		reviewAgent = studioagents.NewReviewAgent(model) // same (otel-wrapped) chat model
	}
	reviewSvc := review.New(assetStore, todoStore)

	// SSRF-safe video/audio result puller (spec §9.4): content-type allowlist +
	// 512MB hard cap (no streaming in M4 — memory ceiling ≈ MaxConcurrent×512MB).
	videoFetcher := fetch.New(fetch.Config{
		Timeout:             5 * time.Minute,
		MaxBytes:            cfg.VideoFetchMaxBytes,
		AllowedContentTypes: []string{"video/", "audio/", "application/octet-stream"},
	})

	// Worker pool — bounded concurrency (agents call LLMs; slow).
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		w := worker.New(worker.Config{
			Pool: st.Pool(), Todos: todoStore, Projects: projectStore, Events: eventStore,
			Script: scriptAgent, Storyboard: storyboardAgent,
			Asset: assetAgent, Review: reviewAgent, Blob: blobStore, Assets: assetStore, Cost: costStore,
			Models: modelStore, Registry: registry, Router: router,
			WorkerID:           fmt.Sprintf("studiod-%d", i),
			GenQuota:           cfg.OrgDailyGenQuota,
			MaxConcurrentGen:   cfg.MaxConcurrentGen,
			MaxConcurrentVideo: cfg.MaxConcurrentVideo,
			MaxConcurrentAudio: cfg.MaxConcurrentAudio,
			VideoFetcher:       videoFetcher,
			PollBackoff:        cfg.PollBackoff,
			MaxPollBackoff:     cfg.MaxPollBackoff,
			MaxPollAttempts:    cfg.MaxPollAttempts,
			LeaseRenewInterval: cfg.LeaseRenewInterval,
			Lease:              cfg.WorkerLease,
			MaxAttempts:        cfg.WorkerMaxAttempt,
			BaseBackoff:        cfg.WorkerBackoff,
			CallTimeout:        cfg.WorkerCallTimeout,
			Tracer:             tp.Tracer("studio.worker"),
		})
		wg.Add(1)
		go func() { defer wg.Done(); w.Run(workerCtx, cfg.WorkerPoll) }()
	}

	// Orphan reaper — terminal-states 'submitted' assets whose external job
	// never returned (spec §5.4 M1). TTL = 2× the full poll budget so it only
	// fires well past any legitimate poll window.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ttl := 2 * time.Duration(cfg.MaxPollAttempts) * cfg.MaxPollBackoff
		worker.RunOrphanReaper(workerCtx, assetStore, cfg.MaxPollBackoff, ttl)
	}()

	issuer := authztoken.NewIssuer([]byte(cfg.JWTSecret), cfg.AccessTTL)
	authService := authzsvc.New(az, issuer, cfg.RefreshTTL)
	authHandlers := authzhttp.New(authService)

	var webFS fs.FS
	if cfg.WebDir != "" {
		webFS = os.DirFS(cfg.WebDir)
	}

	mux := httpapi.NewMux(httpapi.Deps{
		Issuer:       issuer,
		AuthHandlers: authHandlers,
		AuthService:  authService,
		RoleResolver: az,
		Register:     studiosvc.NewRegister(az),
		OrgBootstrap: studiosvc.NewOrg(az),
		OrgList:      studiosvc.NewOrgList(st.Pool()),
		Projects:     projectStore,
		Planner:      plannerSvc,
		ChatRouter:   router,
		Events:       eventStore,
		EventReader:  eventStore,
		Artifacts:    studiosvc.NewArtifacts(st.Pool()),
		PerUserLimit: cfg.PerUserLimit,

		Review:         reviewSvc,
		AssetLibrary:   assetStore,
		BlobSigner:     blobStore,
		BlobServer:     blobServerOrNil(blobServer),
		Models:         modelStore,
		Cost:           costStore,
		PromptBuilder:  promptBuilder,
		GenQuota:       cfg.OrgDailyGenQuota,
		ModelAvailable: modelAvailable(cfg),
		WebFS:          webFS,
	})

	cleanup := func() {
		stopWorkers()
		wg.Wait()
		_ = tp.Shutdown(ctx)
		st.Close()
	}
	return mux, cleanup, nil
}

// providerOverride lets tests inject a scripted model instead of a real provider.
var providerOverride func(config.Config) (llm.ChatModel, error)

func buildModel(cfg config.Config) (llm.ChatModel, error) {
	if providerOverride != nil {
		return providerOverride(cfg)
	}
	if cfg.FakeGen {
		// Keyless dev/demo mode: a content-aware fake ChatModel. The planner falls
		// back to its default script→storyboard pipeline; the script + storyboard
		// agents need VALID JSON to keep the pipeline flowing to the asset stage, so
		// the fake returns canned JSON keyed off each agent's system prompt. No real
		// API key is required anywhere.
		return &fakeChatModel{}, nil
	}
	switch cfg.Provider {
	case "openai":
		return openaiprovider.New(
			openaiprovider.WithModel(cfg.Model),
			openaiprovider.WithAPIKey(cfg.APIKey),
			openaiprovider.WithBaseURL(cfg.BaseURL),
		)
	default: // deepseek
		opts := []deepseekprovider.Option{
			deepseekprovider.WithModel(cfg.Model),
			deepseekprovider.WithAPIKey(cfg.APIKey),
		}
		if cfg.BaseURL != "" {
			opts = append(opts, deepseekprovider.WithBaseURL(cfg.BaseURL))
		}
		return deepseekprovider.New(opts...)
	}
}

// fakeChatModel is the keyless dev/demo ChatModel (FakeGen mode). It returns
// canned, valid JSON keyed off each agent's system prompt so the script +
// storyboard agents keep the pipeline flowing to the asset stage with no real
// API key. Stream/Info are inherited from an embedded empty ScriptedLLM. NOT a
// test double — it backs the runtime PROVIDER=fake / STUDIO_FAKE_GEN=1 mode.
type fakeChatModel struct{ llm.ScriptedLLM }

func (m *fakeChatModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	sys := req.SystemPrompt
	var text string
	switch {
	case strings.Contains(sys, "screenwriter"):
		text = `{"title":"Fake Demo","logline":"a keyless demo","scenes":[{"heading":"INT. STUDIO","description":"a placeholder scene","dialogue":"hello"}]}`
	case strings.Contains(sys, "storyboard"):
		text = `{"shots":[{"shotNo":1,"camera":"wide","scene":"studio","action":"open","prompt":"a placeholder shot","duration":3}]}`
	case strings.Contains(sys, "planner"):
		text = `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`
	default:
		// ReviewAgent + anything else: a neutral advisory verdict.
		text = `{"score":80,"flags":[],"note":"fake-mode placeholder"}`
	}
	return llm.Response{Text: text, Provider: "fake", Model: "fake"}, nil
}

// generatorOverride lets e2e inject a fake MediaGenerator instead of a real
// image provider (the image analog of providerOverride).
var generatorOverride func(config.Config) (generate.MediaGenerator, error)

func buildGenerator(cfg config.Config) (generate.MediaGenerator, error) {
	if generatorOverride != nil {
		return generatorOverride(cfg)
	}
	if cfg.FakeGen {
		// Keyless dev/demo mode: a placeholder-PNG generator so the sync image path
		// (blob Put + asset pending_acceptance) succeeds with no provider key. The
		// keyed registerImage/Video/AudioGenerators below skip every unkeyed
		// provider, so fake mode leaves the registry default as this fake.
		return generate.NewDevFakeGenerator(), nil
	}
	// Org-agnostic fallback: NEVER build an image generator from the CHAT model
	// (cfg.Provider/cfg.Model/cfg.APIKey is a chat triple — binding it to an image
	// generator yields capability-not-supported on every asset). Real image models
	// come from registerImageGenerators (keyed per catalog entry) and are selected
	// per-org via model-configs; the default is only a safe fallback → placeholder.
	log.Printf("studiod: no default image model configured — using placeholder generator; configure a model per-org in 模型配置 (model-configs) and set the matching provider API key")
	return generate.NewDevFakeGenerator(), nil
}

// modelAvailable reports whether a catalog (provider, kind) entry is usable —
// its provider API key is configured, so registerImage/Video/AudioGenerators
// has registered (or will register) the adapter. Mirrors the key-gating in those
// register* funcs exactly. The "fake" provider needs no key (always available).
func modelAvailable(cfg config.Config) func(provider, kind string) bool {
	return func(provider, kind string) bool {
		if provider == "fake" {
			return true
		}
		switch kind {
		case "image":
			switch provider {
			case "openai":
				return cfg.OpenAIAPIKey != ""
			case "google":
				return cfg.GoogleAPIKey != ""
			case "minimax":
				return cfg.MinimaxAPIKey != ""
			case "volcengine":
				return cfg.VolcengineAPIKey != ""
			}
		case "video":
			switch provider {
			case "runway":
				return cfg.RunwayAPIKey != ""
			case "kling":
				return cfg.KlingAPIKey != ""
			case "google":
				return cfg.GoogleAPIKey != ""
			}
		case "audio":
			switch provider {
			case "openai":
				return cfg.TTSAPIKey != ""
			}
		case "text":
			// BYOK: text 模型可在表单自带 key，available:false 仅表示"无服务端 env key"
			// (UI 视为"可自带 key"，非硬阻断)。deepseek 是默认 chat provider (env API_KEY)。
			switch provider {
			case "openai":
				return cfg.OpenAIAPIKey != "" || cfg.APIKey != ""
			case "deepseek":
				return cfg.APIKey != ""
			}
		}
		return false
	}
}

// registryHook lets e2e mutate the generator registry after assembly (register
// extra fakes for the routing e2e). nil in production.
var registryHook func(*generate.Registry)

// registerImageGenerators registers one image adapter per catalog entry whose
// provider has an API key configured (M3 模型管理面接线). The contract types are
// verified: all four providers implement llm.ImageGenerator (providers v0.7.0).
func registerImageGenerators(reg *generate.Registry, cfg config.Config, tp trace.TracerProvider) error {
	for _, e := range models.Catalog() {
		if e.Kind != "image" {
			continue // M3 catalog now carries video/audio entries (M4); skip them here.
		}
		var ig llm.ImageGenerator
		var err error
		switch e.Provider {
		case "openai":
			if cfg.OpenAIAPIKey == "" {
				continue
			}
			ig, err = openaiprovider.New(openaiprovider.WithModel(e.Model), openaiprovider.WithAPIKey(cfg.OpenAIAPIKey))
		case "google":
			if cfg.GoogleAPIKey == "" {
				continue
			}
			ig, err = googleprovider.New(googleprovider.WithModel(e.Model), googleprovider.WithAPIKey(cfg.GoogleAPIKey))
		case "minimax":
			if cfg.MinimaxAPIKey == "" {
				continue
			}
			ig, err = minimaxprovider.New(minimaxprovider.WithModel(e.Model), minimaxprovider.WithAPIKey(cfg.MinimaxAPIKey))
		case "volcengine":
			if cfg.VolcengineAPIKey == "" {
				continue
			}
			ig, err = volcengineprovider.New(volcengineprovider.WithModel(e.Model), volcengineprovider.WithAPIKey(cfg.VolcengineAPIKey))
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("studiod: build %s/%s image generator: %w", e.Provider, e.Model, err)
		}
		reg.Register(e.Provider, e.Model, obs.WrapGenerator(genimage.New(ig, nil), tp))
	}
	return nil
}

// registerVideoGenerators registers a key-gated async video adapter per video
// catalog entry whose provider has a key (spec §8.1, mirrors registerImage-
// Generators). Filtered to e.Kind=="video" (M3). Wrapped with obs.WrapGenerator
// — which preserves the AsyncGenerator seam (B1). Unkeyed providers resolve to
// the registry default.
func registerVideoGenerators(reg *generate.Registry, cfg config.Config, tp trace.TracerProvider) error {
	for _, e := range models.Catalog() {
		if e.Kind != "video" {
			continue
		}
		var ag generate.MediaGenerator
		switch e.Provider {
		case "runway":
			if cfg.RunwayAPIKey == "" {
				continue
			}
			ag = genvideo.NewRunway(cfg.RunwayAPIKey)
		case "kling":
			if cfg.KlingAPIKey == "" {
				continue
			}
			ag = genvideo.NewKling(cfg.KlingAPIKey)
		case "google":
			if cfg.GoogleAPIKey == "" {
				continue
			}
			ag = genvideo.NewVeo(cfg.GoogleAPIKey)
		default:
			continue
		}
		reg.Register(e.Provider, e.Model, obs.WrapGenerator(ag, tp))
	}
	return nil
}

// registerAudioGenerators is the audio analog (key-gated, e.Kind=="audio").
func registerAudioGenerators(reg *generate.Registry, cfg config.Config, tp trace.TracerProvider) error {
	for _, e := range models.Catalog() {
		if e.Kind != "audio" {
			continue
		}
		switch e.Provider {
		case "openai":
			if cfg.TTSAPIKey == "" {
				continue
			}
			reg.Register(e.Provider, e.Model, obs.WrapGenerator(genaudio.NewOpenAITTS(cfg.TTSAPIKey), tp))
		default:
			continue
		}
	}
	return nil
}

// buildChatFactory returns the ModelRouter BuildChat func: it constructs a chat
// model for an org's stored config (own provider/model/api_key/base_url), otel-
// wrapped. "openai-compatible" routes through the OpenAI adapter pointed at the
// config's base_url (deepseek/siliconflow/local/etc.). Unknown provider → error
// (the router logs + falls back to the env-default chat model).
func buildChatFactory(tp trace.TracerProvider) func(provider, model, apiKey, baseURL string) (llm.ChatModel, error) {
	return func(provider, model, apiKey, baseURL string) (llm.ChatModel, error) {
		var (
			m   llm.ChatModel
			err error
		)
		switch provider {
		case "openai", "openai-compatible":
			opts := []openaiprovider.Option{openaiprovider.WithModel(model), openaiprovider.WithAPIKey(apiKey)}
			if baseURL != "" {
				opts = append(opts, openaiprovider.WithBaseURL(baseURL))
			}
			m, err = openaiprovider.New(opts...)
		case "deepseek":
			opts := []deepseekprovider.Option{deepseekprovider.WithModel(model), deepseekprovider.WithAPIKey(apiKey)}
			if baseURL != "" {
				opts = append(opts, deepseekprovider.WithBaseURL(baseURL))
			}
			m, err = deepseekprovider.New(opts...)
		case "google":
			opts := []googleprovider.Option{googleprovider.WithModel(model), googleprovider.WithAPIKey(apiKey)}
			if baseURL != "" {
				opts = append(opts, googleprovider.WithBaseURL(baseURL))
			}
			m, err = googleprovider.New(opts...)
		default:
			return nil, fmt.Errorf("studiod: unknown chat provider %q", provider)
		}
		if err != nil {
			return nil, fmt.Errorf("studiod: build %s/%s chat model: %w", provider, model, err)
		}
		return obs.WrapModel(m, tp), nil
	}
}

// buildMediaFactory returns the ModelRouter BuildMedia func: it constructs a
// MediaGenerator for an org's stored config using its OWN api_key (and base_url
// for image where the SDK supports it). image → the provider's llm.ImageGenerator
// wrapped in genimage; "openai-compatible" → OpenAI image at base_url. video/audio
// → the known async adapter for the provider (fixed-endpoint SDKs ignore baseURL).
// Result is otel-wrapped. Unknown provider/kind → error (router falls back).
func buildMediaFactory(tp trace.TracerProvider) func(kind, provider, model, apiKey, baseURL string) (generate.MediaGenerator, error) {
	return func(kind, provider, model, apiKey, baseURL string) (generate.MediaGenerator, error) {
		switch kind {
		case "image":
			var (
				ig  llm.ImageGenerator
				err error
			)
			switch provider {
			case "openai", "openai-compatible":
				opts := []openaiprovider.Option{openaiprovider.WithModel(model), openaiprovider.WithAPIKey(apiKey)}
				if baseURL != "" {
					opts = append(opts, openaiprovider.WithBaseURL(baseURL))
				}
				ig, err = openaiprovider.New(opts...)
			case "google":
				ig, err = googleprovider.New(googleprovider.WithModel(model), googleprovider.WithAPIKey(apiKey))
			case "minimax":
				ig, err = minimaxprovider.New(minimaxprovider.WithModel(model), minimaxprovider.WithAPIKey(apiKey))
			case "volcengine":
				ig, err = volcengineprovider.New(volcengineprovider.WithModel(model), volcengineprovider.WithAPIKey(apiKey))
			default:
				return nil, fmt.Errorf("studiod: unknown image provider %q", provider)
			}
			if err != nil {
				return nil, fmt.Errorf("studiod: build %s/%s image generator: %w", provider, model, err)
			}
			return obs.WrapGenerator(genimage.New(ig, nil), tp), nil
		case "video":
			var ag generate.MediaGenerator
			switch provider {
			case "runway":
				ag = genvideo.NewRunway(apiKey)
			case "kling":
				ag = genvideo.NewKling(apiKey)
			case "google":
				ag = genvideo.NewVeo(apiKey)
			default:
				return nil, fmt.Errorf("studiod: unknown video provider %q", provider)
			}
			return obs.WrapGenerator(ag, tp), nil
		case "audio":
			switch provider {
			case "openai":
				return obs.WrapGenerator(genaudio.NewOpenAITTS(apiKey), tp), nil
			default:
				return nil, fmt.Errorf("studiod: unknown audio provider %q", provider)
			}
		default:
			return nil, fmt.Errorf("studiod: unknown media kind %q", kind)
		}
	}
}

// blobServerOrNil avoids handing NewMux a typed-nil *localfs.Store wrapped in the
// BlobServer interface (which would be non-nil and crash the blob route in S3
// mode). Returns a nil interface when there's no localfs回源 server.
func blobServerOrNil(s *localfs.Store) httpapi.BlobServer {
	if s == nil {
		return nil
	}
	return s
}
