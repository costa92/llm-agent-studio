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
	ollamaprovider "github.com/costa92/llm-agent-providers/ollama"
	openaiprovider "github.com/costa92/llm-agent-providers/openai"
	volcengineprovider "github.com/costa92/llm-agent-providers/volcengine"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/alerts"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	blobgithub "github.com/costa92/llm-agent-studio/internal/blob/github"
	"github.com/costa92/llm-agent-studio/internal/blob/localfs"
	bloboss "github.com/costa92/llm-agent-studio/internal/blob/oss"
	blobs3 "github.com/costa92/llm-agent-studio/internal/blob/s3"
	"github.com/costa92/llm-agent-studio/internal/config"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/exports"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/generate"
	genaudio "github.com/costa92/llm-agent-studio/internal/generate/audio"
	genimage "github.com/costa92/llm-agent-studio/internal/generate/image"
	genvideo "github.com/costa92/llm-agent-studio/internal/generate/video"
	"github.com/costa92/llm-agent-studio/internal/health"
	"github.com/costa92/llm-agent-studio/internal/httpapi"
	"github.com/costa92/llm-agent-studio/internal/mail"
	"github.com/costa92/llm-agent-studio/internal/mailconfig"
	"github.com/costa92/llm-agent-studio/internal/modelrouter"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/obs"
	"github.com/costa92/llm-agent-studio/internal/orgsecret"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/review"
	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/storageconfig"
	"github.com/costa92/llm-agent-studio/internal/storagerouter"
	"github.com/costa92/llm-agent-studio/internal/studiosvc"
	"github.com/costa92/llm-agent-studio/internal/todos"
	"github.com/costa92/llm-agent-studio/internal/worker"
	"github.com/costa92/llm-agent-studio/internal/workflows"
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

	// 平台超级管理员 (platform role)。哨兵 org (id='') 必须先于任何平台 membership 写入，
	// 满足 auth_membership.org_id 的外键约束 (authz schema)。随后按 env 种子名单授予。
	membersSvc := studiosvc.NewMembers(az, st.GORM())
	platformSvc := studiosvc.NewPlatform(az, st.GORM())
	if err := platformSvc.EnsureSentinelOrg(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	if err := platformSvc.SeedFromEmails(ctx, cfg.PlatformAdminEmails); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("studiod: seed platform admins: %w", err)
	}
	if n := len(cfg.PlatformAdminEmails); n > 0 {
		log.Printf("studiod: platform admin seed: %d email(s) configured (already-registered users granted; others topped-up at register)", n)
	}

	model, err := buildModel(cfg)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	model = obs.WrapModel(model, tp) // otel decorator (spec §12)

	projectStore := project.New(st.GORM())
	healthStore := health.New(st.GORM(), projectStore)
	workflowStore := workflows.New(st.GORM())
	customNodeTypeStore := customnodetype.New(st.GORM())
	todoStore := todos.New(st.GORM())
	eventStore := events.New(st.GORM())
	plannerSvc := planner.New(todoStore, st.GORM())
	scriptAgent := studioagents.NewScriptAgent(model)
	storyboardAgent := studioagents.NewStoryboardAgent(model)

	// BlobStore (spec §10): 内置 localfs 默认 + 单一回源 server，env 配置 (dev/默认)。
	// 远端对象存储 (S3/minio、Alibaba OSS、Tencent COS) 改由 DB storage_configs 配置 +
	// StorageRouter 按 org 路由 (storageRouter 在 encBox 就绪后构造，见下)。这一个
	// *localfs.Store 同时是 router 的 Default (实现 blob.BlobStore) 与回源 handler。
	localfsDefault := localfs.New(cfg.BlobDir, []byte(cfg.BlobSecret), cfg.BlobPublic)

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
	assetStore := assets.New(st.GORM())
	costStore := cost.New(st.GORM())
	taskBoard := studiosvc.NewTaskBoard(st.GORM())
	// BYOK: per-config api key 静态加密 box (env STUDIO_CONFIG_ENC_KEY)。未配置时
	// 返回 disabled box——存 key 会被拒，但服务仍可启动 (env-only key 老路径不受影响)。
	encBox, err := secretbox.NewBoxFromEnv()
	if err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("studiod: secretbox: %w", err)
	}
	modelStore := models.New(st.GORM(), encBox)
	promptStore := prompt.NewStore(st.GORM())
	mailConfigStore := mailconfig.New(st.GORM(), encBox)
	envMailCfg := mail.EnvConfig{
		SMTPHost: cfg.SMTPHost,
		SMTPPort: cfg.SMTPPort,
		SMTPUser: cfg.SMTPUser,
		SMTPPass: cfg.SMTPPass,
		SMTPFrom: cfg.SMTPFrom,
	}
	wd, _ := os.Getwd()
	mailClient := mail.New(mailConfigStore, envMailCfg, nil, wd)

	// Run 失败邮件告警：org 级配置 + 通知器（一次 run 一封、org 级限频、异步发信，
	// 邮件路径绝不阻塞 worker）。内存限频/去重 = 单实例部署假设（见 alerts.Notifier）。
	alertStore := alerts.NewStore(st.GORM())
	alertNotifier := alerts.NewNotifier(alerts.NotifierConfig{
		DB:         st.GORM(),
		Settings:   alertStore,
		Mailer:     mailClient,
		ConsoleURL: cfg.PublicURL,
	})

	// StorageRouter (Phase 3): per-org → global → 内置 localfs 默认 的对象存储路由。
	// storageStore 复用 BYOK 同一把加密 box 解密 secret。buildStorageStore 复用 main 里
	// 既有的 adapter 构造器 (绝不重实现 adapter)。零 storage_config 行时 ResolveForOrg
	// 返回 !ok，router 始终回落 localfsDefault → 全流程仍可跑 (内置默认)。
	storageStore := storageconfig.New(st.GORM(), encBox)
	orgSecretStore := orgsecret.New(st.GORM(), encBox)
	buildStorageStore := func(rs storageconfig.ResolvedStorage) (blob.BlobStore, error) {
		switch rs.Mode {
		case "localfs":
			// 所有 localfs 配置共用同一根/secret，签名才能在唯一回源 handler 上验证通过。
			// 故忽略 per-row root/secret，复用 localfsDefault (保持单一回源 server)。
			return localfsDefault, nil
		case "s3":
			return blobs3.New(blobs3.Config{
				Endpoint: rs.Endpoint, Bucket: rs.Bucket, Region: rs.Region,
				AccessKey: rs.AccessKeyID, SecretKey: rs.SecretKey, UseSSL: rs.UseSSL,
			})
		case "oss":
			return bloboss.New(bloboss.Config{
				Endpoint: rs.Endpoint, Bucket: rs.Bucket,
				AccessKeyID: rs.AccessKeyID, AccessKeySecret: rs.SecretKey,
			})
		case "cos":
			// COS S3-compatible：复用 minio-go adapter，虚拟主机式 endpoint，恒 TLS。
			return blobs3.New(blobs3.Config{
				Endpoint: cosEndpointHost(rs.Region, rs.Endpoint), Bucket: rs.Bucket, Region: rs.Region,
				AccessKey: rs.AccessKeyID, SecretKey: rs.SecretKey, UseSSL: true,
			})
		case "github":
			// 列复用：AccessKeyID=owner, Bucket=repo, Region=branch, PublicPrefix=path
			// 前缀, Endpoint=GHE API 根覆盖, SecretKey=token。token 缺失 → New 报错 →
			// router 回落 default store (见下方 silent-fallback 注释)。
			return blobgithub.New(blobgithub.Config{
				Owner: rs.AccessKeyID, Repo: rs.Bucket, Branch: rs.Region,
				PathPrefix: rs.PublicPrefix, Token: rs.SecretKey, APIBase: rs.Endpoint,
			})
		default:
			return nil, fmt.Errorf("studiod: unknown storage mode %q", rs.Mode)
		}
	}
	storageRouter := storagerouter.New(storagerouter.Config{
		Configs: storageStore,
		Default: localfsDefault,
		Build:   buildStorageStore,
	})

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
	reviewSvc := review.New(assetStore, todoStore, st.GORM())

	// SSRF-safe video/audio result puller (spec §9.4): content-type allowlist +
	// 512MB hard cap (no streaming in M4 — memory ceiling ≈ MaxConcurrent×512MB).
	videoFetcher := fetch.New(fetch.Config{
		Timeout:             5 * time.Minute,
		MaxBytes:            cfg.VideoFetchMaxBytes,
		AllowedContentTypes: []string{"video/", "audio/", "application/octet-stream"},
	})

	// SSRF-safe outbound for http custom nodes (spec B1/B2): 10s timeout, reuse the
	// existing fetch byte cap, and NO content-type allowlist (http kind permits any
	// type — the body policy + opaque errors guard secret leakage, not content type).
	httpFetcher := fetch.New(fetch.Config{
		Timeout:  10 * time.Second,
		MaxBytes: cfg.VideoFetchMaxBytes,
	})

	// Worker pool — bounded concurrency (agents call LLMs; slow).
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		w := worker.New(worker.Config{
			DB: st.GORM(), Todos: todoStore, Projects: projectStore, Events: eventStore,
			Script: scriptAgent, Storyboard: storyboardAgent,
			Asset: assetAgent, Review: reviewAgent, Storage: storageRouter, Assets: assetStore, Cost: costStore,
			Models: modelStore, Registry: registry, Router: router,
			Secrets:                  orgSecretStore,
			HTTPFetcher:              httpFetcher,
			Alerts:                   alertNotifier,
			WorkerID:                 fmt.Sprintf("studiod-%d", i),
			GenQuota:                 cfg.OrgDailyGenQuota,
			MaxConcurrentGen:         cfg.MaxConcurrentGen,
			MaxConcurrentVideo:       cfg.MaxConcurrentVideo,
			MaxConcurrentAudio:       cfg.MaxConcurrentAudio,
			MaxConcurrentVideoPerOrg: cfg.MaxConcurrentVideoPerOrg,
			MaxConcurrentAudioPerOrg: cfg.MaxConcurrentAudioPerOrg,
			VideoFetcher:             videoFetcher,
			PollBackoff:              cfg.PollBackoff,
			MaxPollBackoff:           cfg.MaxPollBackoff,
			MaxPollAttempts:          cfg.MaxPollAttempts,
			LeaseRenewInterval:       cfg.LeaseRenewInterval,
			Lease:                    cfg.WorkerLease,
			MaxAttempts:              cfg.WorkerMaxAttempt,
			BaseBackoff:              cfg.WorkerBackoff,
			CallTimeout:              cfg.WorkerCallTimeout,
			ExprParity:               cfg.ExprParity,  // P3: $node shadow probe (default off)
			ExprChannel:              cfg.ExprChannel, // P3: expr live value channel (default ON; STUDIO_EXPR_CHANNEL=0 reverts to legacy)
			Tracer:                   tp.Tracer("studio.worker"),
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

	// 工作流作品导出 (PDF/EPUB/ZIP): 独立的 export_jobs 异步队列 — 单 runner + reaper，
	// 共享 worker pool 的 ctx/wg 优雅退出。导出是只读消费运行产出资产→渲染→落 blob，
	// 与 todos 运行队列完全隔离 (见 internal/exports)。
	exportStore := exports.New(st.GORM())
	exportLeaseTTL := 2 * time.Minute
	exportRunner := exports.NewRunner(exportStore, exports.NewBookData(st.GORM()), exports.NewProjectInfo(st.GORM()), storageRouter, exports.RunnerConfig{
		WorkerID:    "studiod-export",
		LeaseTTL:    exportLeaseTTL,
		CallTimeout: 5 * time.Minute,
	})
	wg.Add(1)
	go func() { defer wg.Done(); exportRunner.Run(workerCtx, 10*time.Second) }()
	// Export reaper — terminal-states RUNNING export jobs whose lease has expired
	// beyond 2× the lease TTL (a crashed runner stranded them) → 'failed'. Mirrors
	// the orphan reaper.
	wg.Add(1)
	go func() {
		defer wg.Done()
		exportReapTTL := 2 * exportLeaseTTL
		ticker := time.NewTicker(exportLeaseTTL)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if _, err := exportStore.Reap(workerCtx, exportReapTTL); err != nil {
					log.Printf("studiod: export reaper failed: %v", err)
				}
			}
		}
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
		Register:     studiosvc.NewRegister(az).WithMail(mailClient).WithPlatformTopUp(platformSvc, cfg.PlatformAdminEmails),
		MailConfig:   mailConfigStore,
		OrgBootstrap: studiosvc.NewOrg(az),
		OrgList:      studiosvc.NewOrgList(st.GORM()),
		Projects:     projectStore,
		Workflows:    workflowStore,
		Planner:      plannerSvc,
		ChatRouter:   router,
		Events:       eventStore,
		EventReader:  eventStore,
		Artifacts:    studiosvc.NewArtifacts(st.GORM()),
		PerUserLimit: cfg.PerUserLimit,

		Review:         reviewSvc,
		AssetLibrary:   assetStore,
		CoverGen:       router,
		CoverAssets:    assetStore,
		BlobRouter:     storageRouter,
		BlobServer:     localfsDefault,
		Models:         modelStore,
		StorageConfig:  storageStore,
		CustomNodeType: customNodeTypeStore,
		OrgSecret:      orgSecretStore,
		AlertSettings:  alertStore,
		Exports:        exportStore,
		ExportBook:     exports.NewBookData(st.GORM()),
		ExprChannel:    cfg.ExprChannel, // B/P5: read-only capability for field-level varBindings FE gate
		Members:        membersSvc,
		Platform:       platformSvc,
		TaskBoard:      taskBoard,
		Health:         healthStore,
		Cost:           costStore,
		PromptBuilder:  promptBuilder,
		PromptStore:    promptStore,
		GenQuota:       cfg.OrgDailyGenQuota,
		ModelAvailable: modelAvailable(cfg),
		ModelKeyLookup: modelStore.KeyForConfig,
		WebFS:          webFS,
	})

	cleanup := func() {
		stopWorkers()
		wg.Wait()
		// 在关池前等在途告警邮件收尾（每个通知 goroutine 自带 SendTimeout 上界，
		// 不会无限阻塞），避免 shutdown 期间 lookup 撞已关闭的连接池刷错误日志。
		alertNotifier.Wait()
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
	case "ollama":
		// Ollama 本地运行时，无需 API key；base_url 缺省 http://localhost:11434。
		opts := []ollamaprovider.Option{ollamaprovider.WithModel(cfg.Model)}
		if cfg.BaseURL != "" {
			opts = append(opts, ollamaprovider.WithBaseURL(cfg.BaseURL))
		}
		return ollamaprovider.New(opts...)
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
	case strings.Contains(sys, "planner"):
		// 必须最先匹配：planner 系统提示（plannerSystemPrompt）本身含 "script" 与
		// "storyboard" 等词，若排在 storyboard 分支之后会被其抢先命中 → planner 收到
		// 分镜 JSON → ParseGraph 失败 → 每次运行 fallback_used=true，UI 恒弹「Planner
		// 输出畸形，已回落默认管线」。规划图本身就含 script→storyboard，下游 worker
		// 在 storyboard 完成后按 shot 自动派生 asset todos（asset 不在规划图里）。
		text = `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`
	case strings.Contains(sys, "儿童绘本作家"):
		// 绘本脚本提示是中文（agents.pictureBookSystemPrompt），且 JSON 契约比标准
		// 脚本多了 characterSheet 字段——必须先于英文 "screenwriter" 分支匹配，否则
		// 落到 default 的评审 JSON，ScriptAgent 拿不到 title/scenes 而失败，导致绘本
		// 在无 key 的 dev/demo 栈里永远生成不出来（脚本阶段 failed）。
		text = `{"title":"假数据绘本","logline":"无密钥 demo 占位绘本","characterSheet":"主角：小兔子 / 主色：米白 / 服饰：蓝色背带裤 / 特征：长耳朵","scenes":[{"heading":"第一页","description":"小兔子在清晨醒来","dialogue":"新的一天开始啦"},{"heading":"第二页","description":"小兔子走进森林","dialogue":"去探险吧"},{"heading":"第三页","description":"小兔子遇到好朋友","dialogue":"我们一起玩"},{"heading":"第四页","description":"大家快乐地回家","dialogue":"今天真开心"}]}`
	case strings.Contains(sys, "screenwriter"):
		text = `{"title":"Fake Demo","logline":"a keyless demo","scenes":[{"heading":"INT. STUDIO","description":"a placeholder scene","dialogue":"hello"}]}`
	case strings.Contains(sys, "儿童绘本分镜师"):
		// 绘本分镜提示同样是中文（agents.pictureBookStoryboardSystemPrompt），不含英文
		// "storyboard" 标记——必须单独匹配，否则落到 default 而「no shots produced」。
		// 产出封面（action 留空）+ 3 内容页 + 结尾页，让无 key 的 dev/demo 绘本能跑到
		// 可成书阈值（contentCount=3 → 需 2 张已接受插图即 bookReady）。
		text = `{"shots":[` +
			`{"shotNo":1,"camera":"wide","scene":"封面","action":"","prompt":"卡通风格，小兔子站在森林入口，米白主色，蓝色背带裤","duration":3},` +
			`{"shotNo":2,"camera":"medium","scene":"清晨","action":"小兔子在清晨醒来","prompt":"卡通风格，小兔子在床上伸懒腰","duration":3},` +
			`{"shotNo":3,"camera":"wide","scene":"森林","action":"小兔子走进森林","prompt":"卡通风格，小兔子走在森林小路上","duration":3},` +
			`{"shotNo":4,"camera":"medium","scene":"相遇","action":"小兔子遇到好朋友","prompt":"卡通风格，小兔子和小熊握手","duration":3},` +
			`{"shotNo":5,"camera":"wide","scene":"结尾","action":"大家快乐地回家","prompt":"卡通风格，伙伴们在夕阳下挥手","duration":3}]}`
	case strings.Contains(sys, "storyboard"):
		text = `{"shots":[{"shotNo":1,"camera":"wide","scene":"studio","action":"open","prompt":"a placeholder shot","duration":3}]}`
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
			case "ollama":
				// Ollama 本地运行、无需服务端 key → 始终标可用。
				return true
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
		case "ollama":
			// 本地 Ollama：无需 key；base_url 缺省 http://localhost:11434。
			opts := []ollamaprovider.Option{ollamaprovider.WithModel(model)}
			if baseURL != "" {
				opts = append(opts, ollamaprovider.WithBaseURL(baseURL))
			}
			m, err = ollamaprovider.New(opts...)
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
			case "fake":
				return obs.WrapGenerator(generate.NewDevFakeGenerator(), tp), nil
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
			case "minimax":
				// 真实 MiniMax T2A（speech-2.8-hd 等）：同步 TTS，用 config 的
				// api_key + base_url。补 M4 骨架遗留的真实音频实现。
				return obs.WrapGenerator(genaudio.NewMinimaxTTS(apiKey, model, baseURL), tp), nil
			default:
				return nil, fmt.Errorf("studiod: unknown audio provider %q", provider)
			}
		default:
			return nil, fmt.Errorf("studiod: unknown media kind %q", kind)
		}
	}
}

// cosEndpointHost 返回 COS S3-compatible endpoint host (无 scheme) 供 minio-go
// adapter 使用：显式 endpoint 优先，否则按 region 派生 cos.<region>.myqcloud.com。
// (Phase 3: 从 config.COSEndpointHost 迁来——env 存储配置已删，仅 Build factory 用。)
func cosEndpointHost(region, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if region == "" {
		return ""
	}
	return "cos." + region + ".myqcloud.com"
}
