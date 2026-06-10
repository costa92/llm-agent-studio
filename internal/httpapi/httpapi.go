// Package httpapi mounts the studio REST routes (spec §9 M1 subset) and wires
// the auth→org→rbac→limit middleware chain over llm-agent-authz. Mirrors
// llm-agent-kb/internal/httpapi. scope_kind="org" (projects are org resources).
package httpapi

import (
	"context"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"
	authztoken "github.com/costa92/llm-agent-authz/token"

	"github.com/costa92/llm-agent-studio/internal/limits"
)

// EventAppender appends a run event (satisfied by *events.Store).
type EventAppender interface {
	Append(ctx context.Context, projectID, kind, todoID string, payload any) (int64, error)
}

// Deps are the dependencies NewMux wires.
type Deps struct {
	Issuer       *authztoken.Issuer
	AuthHandlers *authzhttp.Handlers // /api/auth/*; nil in focused unit tests
	RoleResolver authzhttp.RoleResolver
	OrgBootstrap OrgBootstrapper
	Projects     ProjectStore
	Planner      PlannerPort
	Events       EventAppender
	EventReader  EventReader
	Artifacts    ArtifactReader
	PerUserLimit int
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
	mux.Handle("POST /api/orgs/{org}/projects", scoped(roleEditor, orgScope, createProjectHandler(d.Projects)))
	mux.Handle("GET /api/orgs/{org}/projects", scoped(roleViewer, orgScope, listProjectsHandler(d.Projects)))

	// Project-scoped routes ({id}).
	mux.Handle("GET /api/projects/{id}", proj(roleViewer, getProjectHandler(d.Projects)))
	mux.Handle("POST /api/projects/{id}/run", proj(roleEditor, runHandler(d.Projects, d.Planner, d.Events)))
	mux.Handle("POST /api/projects/{id}/cancel", proj(roleEditor, cancelHandler(d.Projects)))
	mux.Handle("GET /api/projects/{id}/events", proj(roleViewer, listEventsHandler(d.EventReader)))
	mux.Handle("GET /api/projects/{id}/events/stream", proj(roleViewer, streamEventsHandler(d.EventReader, d.Projects)))
	mux.Handle("GET /api/projects/{id}/todos", proj(roleViewer, todosHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/script", proj(roleViewer, scriptHandler(d.Artifacts)))
	mux.Handle("GET /api/projects/{id}/shots", proj(roleViewer, shotsHandler(d.Artifacts)))
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
