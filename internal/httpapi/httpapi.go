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
	OrgBootstrap OrgBootstrapper
	OrgList      OrgLister
	Projects     ProjectStore
	Planner      PlannerPort
	ChatRouter   ChatRouter // BYOK 模型路由 for the run handler's planner; nil → bound default
	Events       EventAppender
	EventReader  EventReader
	Artifacts    ArtifactReader
	PerUserLimit int

	Review        ReviewPort
	AssetLibrary  AssetLibrary
	BlobRouter    BlobRouter // per-org → global → 内置默认 的对象存储路由 (asset content 按 org 签名)
	BlobServer    BlobServer // 内置 localfs回源 handler (始终非空)
	Models        ModelStore
	StorageConfig StorageConfigStore // per-org / global 对象存储后端配置 (secret write-only)
	Cost          CostStore
	PromptBuilder *prompt.Builder
	GenQuota      int // rolling-24h per-org generation quota; 0 = unlimited

	// ModelAvailable reports whether a catalog (provider, kind) entry is actually
	// usable — i.e. its provider API key is configured so the adapter is/will be
	// registered. nil → all entries treated as available (focused unit tests omit it).
	ModelAvailable func(provider, kind string) bool

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
	if d.Register != nil && d.AuthService != nil {
		mux.Handle("POST /api/auth/register", registerHandler(d.Register, d.AuthService))
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

	// Org bootstrap + project create/list (org-scoped).
	mux.Handle("POST /api/orgs", authOnly(createOrgHandler(d.OrgBootstrap)))
	mux.Handle("GET /api/orgs", authOnly(listOrgsHandler(d.OrgList)))
	mux.Handle("POST /api/orgs/{org}/projects", scoped(roleEditor, orgScope, createProjectHandler(d.Projects)))
	mux.Handle("GET /api/orgs/{org}/projects", scoped(roleViewer, orgScope, listProjectsHandler(d.Projects)))

	// Project-scoped routes ({id}).
	mux.Handle("GET /api/projects/{id}", proj(roleViewer, getProjectHandler(d.Projects)))
	mux.Handle("POST /api/projects/{id}/run", proj(roleEditor, runHandler(d.Projects, d.Planner, d.Events, d.Cost, d.GenQuota, d.ChatRouter)))
	mux.Handle("POST /api/projects/{id}/cancel", proj(roleEditor, cancelHandler(d.Projects)))
	mux.Handle("GET /api/projects/{id}/events", proj(roleViewer, listEventsHandler(d.EventReader)))
	mux.Handle("GET /api/projects/{id}/events/stream", proj(roleViewer, streamEventsHandler(d.EventReader)))
	mux.Handle("GET /api/projects/{id}/todos", proj(roleViewer, todosHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/script", proj(roleViewer, scriptHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/shots", proj(roleViewer, shotsHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/assets", proj(roleViewer, projectAssetsHandler(d.Artifacts)))

	asset := func(min authzrole.Role, h http.HandlerFunc) http.Handler {
		return scoped(min, assetScope(d.AssetLibrary), h)
	}
	// Prompt builder (viewer+, org-agnostic preview — auth only).
	mux.Handle("GET /api/prompt-styles", authOnly(promptStylesHandler()))
	mux.Handle("POST /api/prompt/build", authOnly(promptBuildHandler(d.PromptBuilder)))
	// HITL (admin-only) — asset-scoped.
	mux.Handle("POST /api/assets/{id}/accept", asset(roleAdmin, acceptHandler(d.Review)))
	mux.Handle("POST /api/assets/{id}/reject", asset(roleAdmin, rejectHandler(d.Review)))
	mux.Handle("POST /api/assets/{id}/regenerate", asset(roleAdmin, regenerateHandler(d.Review, d.AssetLibrary, d.Cost, d.GenQuota)))
	// Asset library + single asset (viewer+).
	mux.Handle("GET /api/orgs/{org}/assets", scoped(roleViewer, orgScope, libraryHandler(d.AssetLibrary)))
	mux.Handle("GET /api/assets/{id}", asset(roleViewer, getAssetHandler(d.AssetLibrary)))
	mux.Handle("GET /api/assets/{id}/content", asset(roleViewer, assetContentHandler(d.AssetLibrary, d.BlobRouter)))
	// Signed blob回源 (NO auth — HMAC sig in query gates access, spec §10).
	if d.BlobServer != nil {
		mux.Handle("GET /api/blob/{key...}", blobHandler(d.BlobServer))
	}
	// Model management (admin).
	mux.Handle("GET /api/model-catalog", authOnly(modelCatalogHandler(d.ModelAvailable)))
	mux.Handle("POST /api/orgs/{org}/model-configs", scoped(roleAdmin, orgScope, createModelConfigHandler(d.Models)))
	mux.Handle("GET /api/orgs/{org}/model-configs", scoped(roleAdmin, orgScope, listModelConfigsHandler(d.Models)))
	mux.Handle("PUT /api/orgs/{org}/model-configs/{id}", scoped(roleAdmin, orgScope, updateModelConfigHandler(d.Models)))
	mux.Handle("DELETE /api/orgs/{org}/model-configs/{id}", scoped(roleAdmin, orgScope, deleteModelConfigHandler(d.Models)))
	// Storage config (对象存储后端). Per-org: org_admin scoped. Global: any-org-admin gate.
	mux.Handle("GET /api/orgs/{org}/storage-config", scoped(roleAdmin, orgScope, getOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("PUT /api/orgs/{org}/storage-config", scoped(roleAdmin, orgScope, putOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("DELETE /api/orgs/{org}/storage-config", scoped(roleAdmin, orgScope, deleteOrgStorageConfigHandler(d.StorageConfig)))
	mux.Handle("GET /api/storage-config/global", authOnly(requireAnyOrgAdmin(d.OrgList, getGlobalStorageConfigHandler(d.StorageConfig))))
	mux.Handle("PUT /api/storage-config/global", authOnly(requireAnyOrgAdmin(d.OrgList, putGlobalStorageConfigHandler(d.StorageConfig))))
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
