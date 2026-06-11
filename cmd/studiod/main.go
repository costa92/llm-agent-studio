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
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/obs"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/review"
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

	// BlobStore (spec §10): localfs (dev) or S3 (minio-go presigned).
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
	modelStore := models.New(st.Pool())
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
			Models: modelStore, Registry: registry,
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
		RoleResolver: az,
		OrgBootstrap: studiosvc.NewOrg(az),
		Projects:     projectStore,
		Planner:      plannerSvc,
		Events:       eventStore,
		EventReader:  eventStore,
		Artifacts:    studiosvc.NewArtifacts(st.Pool()),
		PerUserLimit: cfg.PerUserLimit,

		Review:        reviewSvc,
		AssetLibrary:  assetStore,
		BlobSigner:    blobStore,
		BlobServer:    blobServerOrNil(blobServer),
		Models:        modelStore,
		Cost:          costStore,
		PromptBuilder: promptBuilder,
		GenQuota:      cfg.OrgDailyGenQuota,
		WebFS:         webFS,
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

// generatorOverride lets e2e inject a fake MediaGenerator instead of a real
// image provider (the image analog of providerOverride).
var generatorOverride func(config.Config) (generate.MediaGenerator, error)

func buildGenerator(cfg config.Config) (generate.MediaGenerator, error) {
	if generatorOverride != nil {
		return generatorOverride(cfg)
	}
	switch cfg.Provider {
	case "minimax":
		mm, err := minimaxprovider.New(
			minimaxprovider.WithModel(cfg.Model),
			minimaxprovider.WithAPIKey(cfg.APIKey),
		)
		if err != nil {
			return nil, err
		}
		return genimage.New(mm, nil), nil
	default: // openai image
		oa, err := openaiprovider.New(
			openaiprovider.WithModel(cfg.Model),
			openaiprovider.WithAPIKey(cfg.APIKey),
			openaiprovider.WithBaseURL(cfg.BaseURL),
		)
		if err != nil {
			return nil, err
		}
		return genimage.New(oa, nil), nil
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

// blobServerOrNil avoids handing NewMux a typed-nil *localfs.Store wrapped in the
// BlobServer interface (which would be non-nil and crash the blob route in S3
// mode). Returns a nil interface when there's no localfs回源 server.
func blobServerOrNil(s *localfs.Store) httpapi.BlobServer {
	if s == nil {
		return nil
	}
	return s
}
