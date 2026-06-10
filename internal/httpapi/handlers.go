package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"

	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
)

// orgLookup resolves a project's org (satisfied by *project.Store).
type orgLookup interface {
	OrgIDForProject(ctx context.Context, projectID string) (string, error)
}

// OrgBootstrapper creates an org + grants the creator org_admin (mirrors
// orgkb.CreateOrg; implemented in this package over the authz store).
type OrgBootstrapper interface {
	CreateOrg(ctx context.Context, name, creatorUserID string) (string, error)
}

// ProjectStore is the project surface the handlers need.
type ProjectStore interface {
	Create(ctx context.Context, in project.CreateInput) (project.Project, error)
	Get(ctx context.Context, id string) (project.Project, error)
	ListByOrg(ctx context.Context, orgID string, limit int, cursor string) ([]project.Project, string, error)
	SetStatus(ctx context.Context, id, status string) error
	Cancel(ctx context.Context, projectID string) error
	OrgIDForProject(ctx context.Context, projectID string) (string, error)
}

// PlannerPort kicks off planning (satisfied by *planner.Planner).
type PlannerPort interface {
	Plan(ctx context.Context, projectID string, b planner.Brief) (planner.Result, error)
}

// ArtifactReader reads todos/script/shots for the artifact endpoints.
type ArtifactReader interface {
	Todos(ctx context.Context, projectID string) ([]map[string]any, error)
	Script(ctx context.Context, projectID string) (json.RawMessage, bool, error)
	Shots(ctx context.Context, projectID string) ([]map[string]any, error)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// createOrgHandler (POST /api/orgs): any authenticated user; creator becomes
// org_admin. Mirrors kb's bootstrap seam.
func createOrgHandler(boot OrgBootstrapper) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := authzhttp.UserID(r.Context())
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
		}
		orgID, err := boot.CreateOrg(r.Context(), req.Name, uid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": orgID, "name": req.Name})
	}
}

// createProjectHandler (POST /api/orgs/{org}/projects): editor+.
func createProjectHandler(ps ProjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := authzhttp.UserID(r.Context())
		var req struct {
			Name           string `json:"name"`
			Brief          string `json:"brief"`
			ContentType    string `json:"contentType"`
			TargetPlatform string `json:"targetPlatform"`
			Style          string `json:"style"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
		}
		p, err := ps.Create(r.Context(), project.CreateInput{
			OrgID: r.PathValue("org"), Name: req.Name, Brief: req.Brief,
			ContentType: req.ContentType, TargetPlatform: req.TargetPlatform,
			Style: req.Style, CreatedBy: uid,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// listProjectsHandler (GET /api/orgs/{org}/projects): viewer+.
func listProjectsHandler(ps ProjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		items, next, err := ps.ListByOrg(r.Context(), r.PathValue("org"), limit, r.URL.Query().Get("cursor"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]project.Project, 0, len(items))
		out = append(out, items...)
		writeJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": next})
	}
}

// getProjectHandler (GET /api/projects/{id}): viewer+.
func getProjectHandler(ps ProjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := ps.Get(r.Context(), r.PathValue("id"))
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// runHandler (POST /api/projects/{id}/run): editor+. Sets status=planning, runs
// the planner (synchronously enqueues todos), emits planner_started.
func runHandler(ps ProjectStore, pl PlannerPort, ev EventAppender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		p, err := ps.Get(r.Context(), id)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := ps.SetStatus(r.Context(), id, "planning"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = ev.Append(r.Context(), id, "planner_started", "", nil)
		res, err := pl.Plan(r.Context(), id, planner.Brief{
			Brief: p.Description, ContentType: p.ContentType,
			TargetPlatform: p.TargetPlatform, Style: p.Style,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Announce the initially-ready node(s) (the script todo) so the timeline
		// shows todo_ready before todo_started (spec §9). The worker's
		// emitNewlyReady dedups via NOT EXISTS, so it won't re-emit these.
		for _, rt := range res.ReadyTodos {
			_, _ = ev.Append(r.Context(), id, "todo_ready", rt.ID, map[string]any{"type": rt.Type})
		}
		_ = ps.SetStatus(r.Context(), id, "running")
		writeJSON(w, http.StatusAccepted, map[string]any{
			"planId": res.PlanID, "valid": res.Valid, "fallbackUsed": res.FallbackUsed,
		})
	}
}

// cancelHandler (POST /api/projects/{id}/cancel): editor+.
func cancelHandler(ps ProjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ps.Cancel(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "canceled"})
	}
}

// listEventsHandler (GET /api/projects/{id}/events): viewer+, paged by seq.
func listEventsHandler(reader EventReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var after int64
		if v := r.URL.Query().Get("afterSeq"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				after = n
			}
		}
		evs, err := reader.List(r.Context(), r.PathValue("id"), after, 200)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": evs})
	}
}

// artifactHandlers (GET .../todos|script|shots): viewer+.
func todosHandler(ar ArtifactReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := ar.Todos(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func scriptHandler(ar ArtifactReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, ok, err := ar.Script(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "no script yet", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(content)
	}
}

func shotsHandler(ar ArtifactReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := ar.Shots(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// roleEditor / roleViewer aliases for readability at mount sites.
var (
	roleViewer = authzrole.RoleViewer
	roleEditor = authzrole.RoleEditor
)
