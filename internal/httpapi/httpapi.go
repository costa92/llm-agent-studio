// Package httpapi mounts the studio REST routes (spec §9 M1 subset) and wires
// the auth→org→rbac→limit middleware chain over llm-agent-authz. Mirrors
// llm-agent-kb/internal/httpapi. scope_kind="org" (projects are org resources).
package httpapi

import (
	"context"
	"io/fs"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"
	authztoken "github.com/costa92/llm-agent-authz/token"

	"github.com/costa92/llm-agent-studio/internal/limits"
	"github.com/costa92/llm-agent-studio/internal/prompt"
)

// EventAppender appends a run event (satisfied by *events.Store).
type EventAppender interface {
	Append(ctx context.Context, projectID, kind, todoID string, payload any) (int64, error)
}

// Deps are the dependencies NewMux wires.
type Deps struct {
	Issuer       *authztoken.Issuer
	AuthHandlers *authzhttp.Handlers // /api/auth/*; nil in focused unit tests
	AuthService  SessionIssuer       // authz service for register's auto-login; nil in focused unit tests
	RoleResolver authzhttp.RoleResolver
	Register     UserRegistrar // self-serve registration; nil in focused unit tests
	MailConfig   MailConfigStore
	OrgBootstrap OrgBootstrapper
	OrgList      OrgLister
	Projects     ProjectStore
	Workflows    WorkflowStore // first-class 1:N workflows per project; nil in focused unit tests
	Planner      PlannerPort
	ChatRouter   ChatRouter // BYOK 模型路由 for the run handler's planner; nil → bound default
	Events       EventAppender
	EventReader  EventReader
	Artifacts    ArtifactReader
	PerUserLimit int

	Review        ReviewPort
	AssetLibrary  AssetLibrary
	CoverGen      CoverGenerator   // per-org media generator for project cover生成; nil → cover/generate route unmounted
	CoverAssets   CoverAssetWriter // cover asset writer (create + set blob); nil → cover/generate+upload routes unmounted
	BlobRouter    BlobRouter // per-org → global → 内置默认 的对象存储路由 (asset content 按 org 签名)
	BlobServer    BlobServer // 内置 localfs回源 handler (始终非空)
	Models        ModelStore
	StorageConfig StorageConfigStore // per-org / global 对象存储后端配置 (secret write-only)
	Members       MemberService      // org 成员管理: 列出/按邮箱添加/改角色/移除 (org-scoped)
	Platform      PlatformService    // 平台超级管理员: 系统级存储配置 + 所有 org 视图 + 平台管理员名册
	TaskBoard     TaskBoardReader    // 任务中心: 跨项目运行状态聚合 (org-scoped, viewer+)
	Health        HealthReporter     // 平台健康/数据完整性监控 + 修复 (unauth probes + platformAdmin 报告/修复)
	Cost          CostStore
	PromptBuilder *prompt.Builder
	PromptStore   *prompt.Store
	GenQuota      int // rolling-24h per-org generation quota; 0 = unlimited
	CustomNodeType CustomNodeTypeStore // org-scoped typed custom node registry; nil in focused unit tests
	OrgSecret      OrgSecretStore      // org-scoped named-secret registry (roleAdmin); nil in focused unit tests

	// ModelAvailable reports whether a catalog (provider, kind) entry is actually
	// usable — i.e. its provider API key is configured so the adapter is/will be
	// registered. nil → all entries treated as available (focused unit tests omit it).
	ModelAvailable func(provider, kind string) bool

	// ModelKeyLookup resolves a model-config's stored (decrypted) api key by
	// (orgID, configID), so the live model-listing endpoint can refresh the list
	// for an existing config without the admin re-typing the key. nil → listing
	// only uses keys supplied in the request.
	ModelKeyLookup func(ctx context.Context, orgID, configID string) (string, error)

	// WebFS, when non-nil, mounts a built SPA under "GET /" (catch-all, ranked
	// below every /api/* route). nil = backend-only (no UI served).
	WebFS fs.FS
}

// assetScope resolves (orgID,"") for asset-scoped routes ({id}) via the asset's
// project→org. Missing asset → ("","") → RoleNone → 403 (safe default).
func assetScope(lib AssetLibrary) authzhttp.ScopeFromRequest {
	return func(r *http.Request) (string, string) {
		orgID, err := lib.OrgIDForAsset(r.Context(), r.PathValue("id"))
		if err != nil {
			return "", ""
		}
		return orgID, ""
	}
}

// projectScope resolves (orgID, scopeID="") for project-scoped routes ({id}):
// orgID via the project lookup, scopeID="" so an org-level membership row
// (org_admin) matches (ResolveRole: scope_id IS NULL OR scope_id=$4). On a
// missing project returns ("","") → RoleNone → 403 (safe default).
func projectScope(lookup orgLookup) authzhttp.ScopeFromRequest {
	return func(r *http.Request) (string, string) {
		orgID, err := lookup.OrgIDForProject(r.Context(), r.PathValue("id"))
		if err != nil {
			return "", ""
		}
		return orgID, ""
	}
}

// orgScope resolves (orgID from {org}, scopeID="") for org-scoped routes.
func orgScope(r *http.Request) (string, string) { return r.PathValue("org"), "" }

// NewMux builds the studio ServeMux.
func NewMux(d Deps) *http.ServeMux {
	mux := http.NewServeMux()
	if d.AuthHandlers != nil {
		d.AuthHandlers.Mount(mux, "/api/auth")
	}
	// Self-serve registration: OPEN (unauthenticated), an ADDITIONAL route on the
	// same mux as authz's login/refresh/logout (no pattern collision).
	if d.Register != nil {
		mux.Handle("POST /api/auth/register", registerHandler(d.Register))
		if d.AuthService != nil {
			mux.Handle("POST /api/auth/verify", verifyHandler(d.Register, d.AuthService))
			mux.Handle("POST /api/auth/resend-verification", resendVerificationHandler(d.Register))
		}
	}
	guard := limits.New(d.PerUserLimit)

	authOnly := func(h http.HandlerFunc) http.Handler {
		var handler http.Handler = withUserLimit(guard, h)
		return authzhttp.Authenticate(d.Issuer)(handler)
	}
	scoped := func(min authzrole.Role, scope authzhttp.ScopeFromRequest, h http.HandlerFunc) http.Handler {
		var handler http.Handler = withUserLimit(guard, h)
		handler = authzhttp.RequireScopeRole(d.RoleResolver, "org", min, scope)(handler)
		return authzhttp.Authenticate(d.Issuer)(handler)
	}
	projScope := projectScope(d.Projects)
	proj := func(min authzrole.Role, h http.HandlerFunc) http.Handler {
		return scoped(min, projScope, h)
	}
	// platformAdmin 门禁：以 scope_kind="platform" 在固定 scope (orgID="", scopeID="")
	// 上解析角色，要求 ≥ admin。镜像 scoped，但锚定 platform 维度而非 org。
	platformAdmin := func(h http.HandlerFunc) http.Handler {
		var handler http.Handler = withUserLimit(guard, h)
		handler = authzhttp.RequireScopeRole(d.RoleResolver, "platform", roleAdmin, platformScope)(handler)
		return authzhttp.Authenticate(d.Issuer)(handler)
	}

	// Org bootstrap + project create/list (org-scoped).
	mux.Handle("POST /api/orgs", authOnly(createOrgHandler(d.OrgBootstrap)))
	mux.Handle("GET /api/orgs", authOnly(listOrgsHandler(d.OrgList)))
	mux.Handle("POST /api/orgs/{org}/projects", scoped(roleEditor, orgScope, createProjectHandler(d.Projects)))
	mux.Handle("GET /api/orgs/{org}/projects", scoped(roleViewer, orgScope, listProjectsHandler(d.Projects)))
	// Task center (任务中心): cross-project run dashboard (org-scoped, viewer+).
	mux.Handle("GET /api/orgs/{org}/tasks", scoped(roleViewer, orgScope, taskboardHandler(d.TaskBoard)))

	// Project-scoped routes ({id}).
	mux.Handle("GET /api/projects/{id}", proj(roleViewer, getProjectHandler(d.Projects)))
	mux.Handle("PUT /api/projects/{id}", proj(roleEditor, updateProjectHandler(d.Projects)))
	mux.Handle("POST /api/projects/{id}/run", proj(roleEditor, runHandler(d.Projects, d.Planner, d.Events, d.Cost, d.GenQuota, d.ChatRouter, d.CustomNodeType)))
	mux.Handle("POST /api/projects/{id}/cancel", proj(roleEditor, cancelHandler(d.Projects)))
	mux.Handle("GET /api/projects/{id}/plans", proj(roleViewer, listPlansHandler(d.Projects)))
	mux.Handle("GET /api/projects/{id}/state", proj(roleViewer, stateHandler(d.Projects)))
	mux.Handle("GET /api/projects/{id}/events", proj(roleViewer, listEventsHandler(d.EventReader)))
	mux.Handle("GET /api/projects/{id}/events/stream", proj(roleViewer, streamEventsHandler(d.EventReader, d.Projects)))
	mux.Handle("GET /api/projects/{id}/todos", proj(roleViewer, todosHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/script", proj(roleViewer, scriptHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/shots", proj(roleViewer, shotsHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/assets", proj(roleViewer, projectAssetsHandler(d.Artifacts)))
	// First-class workflows (1:N per project). Each workflow is an independent
	// execution unit: its own DAG, its own runs (plans.workflow_id), its own
	// assets/timeline (reached via the run's planId). nil Workflows → focused tests.
	if d.Workflows != nil {
		mux.Handle("GET /api/projects/{id}/workflows", proj(roleViewer, listWorkflowsHandler(d.Workflows)))
		mux.Handle("POST /api/projects/{id}/workflows", proj(roleEditor, createWorkflowHandler(d.Workflows)))
		mux.Handle("PUT /api/projects/{id}/workflows/{wfId}", proj(roleEditor, updateWorkflowHandler(d.Workflows)))
		mux.Handle("DELETE /api/projects/{id}/workflows/{wfId}", proj(roleEditor, deleteWorkflowHandler(d.Workflows)))
		mux.Handle("POST /api/projects/{id}/workflows/{wfId}/run", proj(roleEditor, runWorkflowHandler(d.Projects, d.Workflows, d.Planner, d.Events, d.Cost, d.GenQuota, d.CustomNodeType)))
	}

	// Project cover image (3 sources: AI-generate / upload / pick existing).
	// Mounted as a group when the generator + writer ports are wired (focused unit
	// tests that omit them skip the whole cover surface).
	if d.CoverGen != nil && d.CoverAssets != nil {
		mux.Handle("POST /api/projects/{id}/cover/generate", proj(roleEditor, coverGenerateHandler(d.Projects, d.CoverAssets, d.CoverGen, d.BlobRouter, d.Cost, d.GenQuota)))
		mux.Handle("POST /api/projects/{id}/cover/upload", proj(roleEditor, coverUploadHandler(d.Projects, d.CoverAssets, d.BlobRouter)))
		mux.Handle("PUT /api/projects/{id}/cover", proj(roleEditor, coverSetHandler(d.Projects, d.AssetLibrary)))
		mux.Handle("GET /api/projects/{id}/cover/options", proj(roleViewer, coverOptionsHandler(d.Projects, d.AssetLibrary)))
	}

	asset := func(min authzrole.Role, h http.HandlerFunc) http.Handler {
		return scoped(min, assetScope(d.AssetLibrary), h)
	}
	// Prompt builder (viewer+, org-agnostic preview — auth only).
	mux.Handle("GET /api/node-types/builtin", authOnly(builtinNodeTypesHandler()))
	mux.Handle("GET /api/prompt-styles", authOnly(promptStylesHandler()))
	mux.Handle("GET /api/prompt-presets", authOnly(promptPresetsHandler()))
	mux.Handle("POST /api/prompt/build", authOnly(promptBuildHandler(d.PromptBuilder)))
	if d.PromptStore != nil {
		mux.Handle("GET /api/orgs/{org}/prompts", scoped(roleViewer, orgScope, listPromptsHandler(d.PromptStore)))
		mux.Handle("POST /api/orgs/{org}/prompts", scoped(roleEditor, orgScope, createPromptHandler(d.PromptStore)))
		mux.Handle("PUT /api/orgs/{org}/prompts/{id}", scoped(roleEditor, orgScope, updatePromptHandler(d.PromptStore)))
		mux.Handle("PUT /api/orgs/{org}/prompts/{id}/default", scoped(roleEditor, orgScope, setPromptDefaultHandler(d.PromptStore)))
		mux.Handle("DELETE /api/orgs/{org}/prompts/{id}", scoped(roleEditor, orgScope, deletePromptHandler(d.PromptStore)))
	}
	// HITL (admin-only) — asset-scoped.
	mux.Handle("POST /api/assets/{id}/accept", asset(roleAdmin, acceptHandler(d.Review)))
	mux.Handle("POST /api/assets/{id}/reject", asset(roleAdmin, rejectHandler(d.Review)))
	mux.Handle("POST /api/assets/{id}/regenerate", asset(roleAdmin, regenerateHandler(d.Review, d.AssetLibrary, d.Cost, d.GenQuota)))
	// 编辑旁白重配音 (绘本 Task 7): same admin gate as regenerate.
	mux.Handle("POST /api/assets/{id}/narration", asset(roleAdmin, narrationHandler(d.Review, d.AssetLibrary, d.Cost, d.GenQuota)))
	// Asset library + single asset (viewer+).
	mux.Handle("GET /api/orgs/{org}/assets", scoped(roleViewer, orgScope, libraryHandler(d.AssetLibrary)))
	mux.Handle("GET /api/assets/{id}", asset(roleViewer, getAssetHandler(d.AssetLibrary)))
	mux.Handle("GET /api/assets/{id}/content", asset(roleViewer, assetContentHandler(d.AssetLibrary, d.BlobRouter, d.Projects)))
	// Signed blob回源 (NO auth — HMAC sig in query gates access, spec §10).
	if d.BlobServer != nil {
		mux.Handle("GET /api/blob/{key...}", blobHandler(d.BlobServer))
	}
	// Health / metrics. Liveness + Prometheus scrape are UNAUTH (ops probes);
	// the rich report + repair + recent-failures are platformAdmin-gated below.
	if d.Health != nil {
		mux.Handle("GET /healthz", healthzHandler(d.Health))
		mux.Handle("GET /metrics", metricsHandler(d.Health))
	}
	// Model management (admin).
	mux.Handle("GET /api/model-catalog", authOnly(modelCatalogHandler(d.ModelAvailable)))
	mux.Handle("POST /api/orgs/{org}/model-configs/list-models", scoped(roleAdmin, orgScope, listModelsHandler(d.ModelKeyLookup)))
	mux.Handle("POST /api/orgs/{org}/model-configs", scoped(roleAdmin, orgScope, createModelConfigHandler(d.Models)))
	mux.Handle("GET /api/orgs/{org}/model-configs", scoped(roleAdmin, orgScope, listModelConfigsHandler(d.Models)))
	mux.Handle("PUT /api/orgs/{org}/model-configs/{id}", scoped(roleAdmin, orgScope, updateModelConfigHandler(d.Models)))
	mux.Handle("DELETE /api/orgs/{org}/model-configs/{id}", scoped(roleAdmin, orgScope, deleteModelConfigHandler(d.Models)))
	mux.Handle("GET /api/orgs/{org}/model-configs/{id}/reveal", scoped(roleAdmin, orgScope, revealModelKeyHandler(d.ModelKeyLookup)))
	// Storage configs (对象存储后端，多配置 list/CRUD/default). Per-org: org_admin scoped. Global: any-org-admin gate.
	mux.Handle("GET /api/orgs/{org}/storage-configs", scoped(roleAdmin, orgScope, listOrgStorageConfigsHandler(d.StorageConfig)))
	mux.Handle("POST /api/orgs/{org}/storage-configs", scoped(roleAdmin, orgScope, createOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("PUT /api/orgs/{org}/storage-configs/{id}", scoped(roleAdmin, orgScope, updateOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("DELETE /api/orgs/{org}/storage-configs/{id}", scoped(roleAdmin, orgScope, deleteOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("POST /api/orgs/{org}/storage-configs/{id}/default", scoped(roleAdmin, orgScope, setDefaultStorageConfigHandler(d.StorageConfig)))
	if d.CustomNodeType != nil {
		mux.Handle("GET /api/orgs/{org}/custom-node-types", scoped(roleViewer, orgScope, listCustomNodeTypesHandler(d.CustomNodeType)))
		mux.Handle("POST /api/orgs/{org}/custom-node-types", scoped(roleEditor, orgScope, createCustomNodeTypeHandler(d.CustomNodeType, d.RoleResolver)))
		mux.Handle("PUT /api/orgs/{org}/custom-node-types/{id}", scoped(roleEditor, orgScope, updateCustomNodeTypeHandler(d.CustomNodeType, d.RoleResolver)))
		mux.Handle("DELETE /api/orgs/{org}/custom-node-types/{id}", scoped(roleEditor, orgScope, deleteCustomNodeTypeHandler(d.CustomNodeType)))
	}
	// Org 命名密钥注册表 (org-scoped). 与 model/storage configs 同列：密钥型资源全程 roleAdmin。
	if d.OrgSecret != nil {
		mux.Handle("GET /api/orgs/{org}/secrets", scoped(roleAdmin, orgScope, listOrgSecretsHandler(d.OrgSecret)))
		mux.Handle("POST /api/orgs/{org}/secrets", scoped(roleAdmin, orgScope, createOrgSecretHandler(d.OrgSecret)))
		mux.Handle("PUT /api/orgs/{org}/secrets/{name}", scoped(roleAdmin, orgScope, updateOrgSecretHandler(d.OrgSecret)))
		mux.Handle("DELETE /api/orgs/{org}/secrets/{name}", scoped(roleAdmin, orgScope, deleteOrgSecretHandler(d.OrgSecret)))
	}
	// Org 成员管理 (org-scoped). 列出对任意 org 成员开放 (viewer)；增删改角色限 org_admin (admin).
	mux.Handle("GET /api/orgs/{org}/members", scoped(roleViewer, orgScope, listMembersHandler(d.Members)))
	mux.Handle("POST /api/orgs/{org}/members", scoped(roleAdmin, orgScope, addMemberHandler(d.Members)))
	mux.Handle("PUT /api/orgs/{org}/members/{userId}", scoped(roleAdmin, orgScope, setMemberRoleHandler(d.Members)))
	mux.Handle("DELETE /api/orgs/{org}/members/{userId}", scoped(roleAdmin, orgScope, removeMemberHandler(d.Members)))
	// 平台超级管理员 (spec: 平台角色). whoami 仅 authOnly（前端据此决定是否展示平台导航，
	// 不必吃 403）；其余路由经 platformAdmin 门禁。系统级 global 存储配置从旧的
	// any-org-admin 门禁迁到此处，由专属平台角色守护。
	mux.Handle("GET /api/platform/whoami", authOnly(platformWhoamiHandler(d.Platform)))
	mux.Handle("GET /api/platform/storage-config/global", platformAdmin(getGlobalStorageConfigHandler(d.StorageConfig)))
	mux.Handle("PUT /api/platform/storage-config/global", platformAdmin(putGlobalStorageConfigHandler(d.StorageConfig)))
	if d.MailConfig != nil {
		mux.Handle("GET /api/platform/mail-config/global", platformAdmin(getGlobalMailConfigHandler(d.MailConfig)))
		mux.Handle("PUT /api/platform/mail-config/global", platformAdmin(putGlobalMailConfigHandler(d.MailConfig)))
	}
	mux.Handle("GET /api/platform/orgs", platformAdmin(platformOrgsHandler(d.Platform)))
	mux.Handle("GET /api/platform/admins", platformAdmin(platformListAdminsHandler(d.Platform)))
	mux.Handle("POST /api/platform/admins", platformAdmin(platformGrantAdminHandler(d.Platform)))
	mux.Handle("DELETE /api/platform/admins/{userId}", platformAdmin(platformRevokeAdminHandler(d.Platform)))
	mux.Handle("GET /api/platform/users", platformAdmin(platformListUsersHandler(d.Platform)))
	mux.Handle("GET /api/platform/users/{userId}", platformAdmin(platformUserDetailHandler(d.Platform)))
	mux.Handle("DELETE /api/platform/users/{userId}", platformAdmin(platformDeleteUserHandler(d.Platform)))
	mux.Handle("POST /api/platform/users/{userId}/reset-password", platformAdmin(platformResetPasswordHandler(d.Platform)))
	// Platform health/data-integrity: full report + repair + recent failures.
	if d.Health != nil {
		mux.Handle("GET /api/platform/health", platformAdmin(platformHealthHandler(d.Health)))
		mux.Handle("POST /api/platform/health/repair", platformAdmin(platformHealthRepairHandler(d.Health)))
		mux.Handle("GET /api/platform/health/events", platformAdmin(platformHealthEventsHandler(d.Health)))
	}
	// Cost center (admin).
	mux.Handle("GET /api/orgs/{org}/cost", scoped(roleAdmin, orgScope, orgCostHandler(d.Cost)))
	mux.Handle("GET /api/projects/{id}/cost", proj(roleAdmin, projectCostHandler(d.Cost)))
	mux.Handle("GET /api/orgs/{org}/cost/projects", scoped(roleAdmin, orgScope, orgCostProjectsHandler(d.Cost)))
	mux.Handle("GET /api/orgs/{org}/generations", scoped(roleAdmin, orgScope, orgGenerationsHandler(d.Cost)))

	// SPA catch-all (ranked below every /api/* pattern). Optional: only when a
	// built web bundle is supplied; backend-only deployments leave it unmounted.
	if d.WebFS != nil {
		mux.Handle("GET /", spaHandler(d.WebFS))
	}
	return mux
}

// withUserLimit enforces the per-user budget after auth (UserID is set).
func withUserLimit(g *limits.Guard, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.Allow(authzhttp.UserID(r.Context())) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
