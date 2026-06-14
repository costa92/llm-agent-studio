// Package modelrouter centralizes per-org 模型路由 (BYOK): it resolves an org's
// stored model_config (provider/model/base_url/api_key) and constructs the
// matching chat model or media generator, falling back to env defaults when the
// org has no usable config. It does NOT import concrete provider packages —
// construction is injected via BuildChat/BuildMedia factory funcs (those live in
// cmd/studiod), so this package stays provider-agnostic and unit-testable.
package modelrouter

import (
	"context"
	"log/slog"

	"github.com/costa92/llm-agent-contract/llm"

	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/models"
)

// resolver is the slice of *models.Store the router needs (extracted so router
// unit tests can fake resolution without a live PG — *models.Store satisfies it).
type resolver interface {
	ResolveForOrg(ctx context.Context, orgID, kind string) (models.ResolvedModel, bool, error)
	ResolveForOrgNamed(ctx context.Context, orgID, kind, provider, modelName string) (models.ResolvedModel, bool, error)
}

// registryDefaulter is the slice of *generate.Registry the router needs.
type registryDefaulter interface {
	Resolve(provider, model string) (generate.MediaGenerator, error)
	Default() generate.MediaGenerator
}

// Config configures a Router. Models/Registry are required for routing;
// DefaultChat/BuildChat/BuildMedia may be nil (the router degrades gracefully —
// see ChatModelFor/MediaGeneratorFor).
type Config struct {
	Models      resolver
	Registry    registryDefaulter
	DefaultChat llm.ChatModel // env-default chat model (fallback)
	BuildChat   func(provider, model, apiKey, baseURL string) (llm.ChatModel, error)
	BuildMedia  func(kind, provider, model, apiKey, baseURL string) (generate.MediaGenerator, error)
	Logger      *slog.Logger // nil → slog.Default()
}

// Router resolves+constructs per-org chat models and media generators.
type Router struct {
	models      resolver
	registry    registryDefaulter
	defaultChat llm.ChatModel
	buildChat   func(provider, model, apiKey, baseURL string) (llm.ChatModel, error)
	buildMedia  func(kind, provider, model, apiKey, baseURL string) (generate.MediaGenerator, error)
	log         *slog.Logger
}

// New builds a Router. Building a provider client per call is acceptable for now
// (low volume) — no cache.
func New(cfg Config) *Router {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Router{
		models:      cfg.Models,
		registry:    cfg.Registry,
		defaultChat: cfg.DefaultChat,
		buildChat:   cfg.BuildChat,
		buildMedia:  cfg.BuildMedia,
		log:         log,
	}
}

// ChatModelFor returns the org's configured text/chat model, else DefaultChat.
// NEVER returns nil-meaningfully: on any miss/error/build-failure it returns
// DefaultChat (callers depend on a usable model; if DefaultChat is also nil the
// caller handles that — the router does not invent one).
//
// 不检查 rm.APIKey：是否需要 key 由 buildChatFactory 按 provider 决定（ollama 不
// 需要 key；openai/deepseek 拿到空 key 会在请求时 401，让 caller 看到真错而不是
// 默默退回 default）。M5.1 之前 ChatModelFor 这里就有一个 `rm.APIKey == ""` 短路，
// 跟 ollama 等"keyless provider"不兼容，移掉。
func (r *Router) ChatModelFor(ctx context.Context, orgID string) llm.ChatModel {
	if r.models == nil {
		return r.defaultChat
	}
	rm, ok, err := r.models.ResolveForOrg(ctx, orgID, "text")
	if err != nil {
		r.log.Warn("modelrouter: resolve chat config failed; using default chat model", "org", orgID, "err", err)
		return r.defaultChat
	}
	if !ok || r.buildChat == nil {
		return r.defaultChat
	}
	m, berr := r.buildChat(rm.Provider, rm.Model, rm.APIKey, rm.BaseURL)
	if berr != nil {
		r.log.Warn("modelrouter: build org chat model failed; using default chat model",
			"org", orgID, "provider", rm.Provider, "model", rm.Model, "err", berr)
		return r.defaultChat
	}
	return m
}

// ChatModelForNamed 解析 org 下特定 (provider, model) 的 chat config，供 per-project
// 规划模型 override 走（M5.1：project.planner_provider/planner_model）。找不到
// 对应配置或 build 失败时返 nil（caller 走 ChatModelFor 拿默认 chat）。
func (r *Router) ChatModelForNamed(ctx context.Context, orgID, provider, modelName string) llm.ChatModel {
	if r.models == nil || provider == "" || modelName == "" {
		return nil
	}
	rm, ok, err := r.models.ResolveForOrgNamed(ctx, orgID, "text", provider, modelName)
	if err != nil {
		r.log.Warn("modelrouter: resolve named chat config failed; falling back",
			"org", orgID, "provider", provider, "model", modelName, "err", err)
		return nil
	}
	if !ok || r.buildChat == nil {
		return nil
	}
	m, berr := r.buildChat(rm.Provider, rm.Model, rm.APIKey, rm.BaseURL)
	if berr != nil {
		r.log.Warn("modelrouter: build named chat model failed; falling back",
			"org", orgID, "provider", provider, "model", modelName, "err", berr)
		return nil
	}
	return m
}

// MediaGeneratorFor returns the org's configured generator for kind, else the
// env-keyed registry adapter, else the registry default. Never returns nil if a
// default exists. Resolution preserves today's behavior: a config WITHOUT a
// per-config key still routes through the env-keyed registry (Registry.Resolve).
func (r *Router) MediaGeneratorFor(ctx context.Context, orgID, kind string) generate.MediaGenerator {
	if r.models == nil || r.registry == nil {
		if r.registry != nil {
			return r.registry.Default()
		}
		return nil
	}
	rm, ok, err := r.models.ResolveForOrg(ctx, orgID, kind)
	if err != nil {
		r.log.Warn("modelrouter: resolve media config failed; using registry default",
			"org", orgID, "kind", kind, "err", err)
		return r.registry.Default()
	}
	if !ok {
		// No org config for this kind → registry default (fresh org runs).
		return r.registry.Default()
	}
	// Per-config key present → construct the BYOK generator.
	if rm.APIKey != "" && r.buildMedia != nil {
		g, berr := r.buildMedia(kind, rm.Provider, rm.Model, rm.APIKey, rm.BaseURL)
		if berr == nil {
			return g
		}
		r.log.Warn("modelrouter: build org media generator failed; falling back to registry",
			"org", orgID, "kind", kind, "provider", rm.Provider, "model", rm.Model, "err", berr)
	}
	// Config present but no per-config key (or build failed) → env-keyed registry
	// adapter (preserves M3 routing), else registry default.
	if g, rerr := r.registry.Resolve(rm.Provider, rm.Model); rerr == nil {
		return g
	} else {
		r.log.Warn("modelrouter: org-selected model has no registered adapter; using registry default (provider API key likely missing)",
			"org", orgID, "kind", kind, "provider", rm.Provider, "model", rm.Model, "err", rerr)
	}
	return r.registry.Default()
}

// MediaGeneratorForNamed 解析 org 下特定 (kind, provider, model) 的 media config，供 per-project
// 媒体生成模型 override 走（如：project.image_provider/image_model）。找不到
// 对应配置或 build 失败时返 nil（caller 走 MediaGeneratorFor 拿默认 media）。
func (r *Router) MediaGeneratorForNamed(ctx context.Context, orgID, kind, provider, modelName string) generate.MediaGenerator {
	if r.models == nil || provider == "" || modelName == "" {
		return nil
	}
	rm, ok, err := r.models.ResolveForOrgNamed(ctx, orgID, kind, provider, modelName)
	if err != nil {
		r.log.Warn("modelrouter: resolve named media config failed; falling back",
			"org", orgID, "kind", kind, "provider", provider, "model", modelName, "err", err)
		return nil
	}
	if !ok {
		return nil
	}
	if (rm.APIKey != "" || provider == "fake") && r.buildMedia != nil {
		g, berr := r.buildMedia(kind, rm.Provider, rm.Model, rm.APIKey, rm.BaseURL)
		if berr == nil {
			return g
		}
		r.log.Warn("modelrouter: build named media generator failed; falling back to registry",
			"org", orgID, "kind", kind, "provider", rm.Provider, "model", rm.Model, "err", berr)
	}
	if r.registry != nil {
		if g, rerr := r.registry.Resolve(rm.Provider, rm.Model); rerr == nil {
			return g
		}
	}
	return nil
}
