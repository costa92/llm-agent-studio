// Command studiod is the AI Studio backend server (M1: text pipeline).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"sync"
	"syscall"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzsvc "github.com/costa92/llm-agent-authz/service"
	authzstore "github.com/costa92/llm-agent-authz/store"
	authztoken "github.com/costa92/llm-agent-authz/token"
	"github.com/costa92/llm-agent-contract/llm"
	deepseekprovider "github.com/costa92/llm-agent-providers/deepseek"
	minimaxprovider "github.com/costa92/llm-agent-providers/minimax"
	openaiprovider "github.com/costa92/llm-agent-providers/openai"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/blob/localfs"
	blobs3 "github.com/costa92/llm-agent-studio/internal/blob/s3"
	"github.com/costa92/llm-agent-studio/internal/config"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/generate"
	genimage "github.com/costa92/llm-agent-studio/internal/generate/image"
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
	registry.SetDefault(gen)
	// Register catalog entries to the same default generator's provider/model so
	// model_configs resolving to a catalog entry finds a generator (M2: one shared
	// image generator; per-provider routing is M3).
	for _, e := range models.Catalog() {
		registry.Register(e.Provider, e.Model, gen)
	}

	promptBuilder := prompt.NewBuilder()
	assetStore := assets.New(st.Pool())
	costStore := cost.New(st.Pool())
	modelStore := models.New(st.Pool())
	assetAgent := studioagents.NewAssetAgent(promptBuilder, gen)
	reviewSvc := review.New(assetStore, todoStore)

	// Worker pool — bounded concurrency (agents call LLMs; slow).
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		w := worker.New(worker.Config{
			Pool: st.Pool(), Todos: todoStore, Projects: projectStore, Events: eventStore,
			Script: scriptAgent, Storyboard: storyboardAgent,
			Asset: assetAgent, Blob: blobStore, Assets: assetStore, Cost: costStore,
			Models: modelStore, Registry: registry,
			WorkerID:    fmt.Sprintf("studiod-%d", i),
			Lease:       cfg.WorkerLease,
			MaxAttempts: cfg.WorkerMaxAttempt,
			BaseBackoff: cfg.WorkerBackoff,
		})
		wg.Add(1)
		go func() { defer wg.Done(); w.Run(workerCtx, cfg.WorkerPoll) }()
	}

	issuer := authztoken.NewIssuer([]byte(cfg.JWTSecret), cfg.AccessTTL)
	authService := authzsvc.New(az, issuer, cfg.RefreshTTL)
	authHandlers := authzhttp.New(authService)

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

// blobServerOrNil avoids handing NewMux a typed-nil *localfs.Store wrapped in the
// BlobServer interface (which would be non-nil and crash the blob route in S3
// mode). Returns a nil interface when there's no localfs回源 server.
func blobServerOrNil(s *localfs.Store) httpapi.BlobServer {
	if s == nil {
		return nil
	}
	return s
}
