package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"
	authzsvc "github.com/costa92/llm-agent-authz/service"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/nodedesc"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/projectstate"
	"github.com/costa92/llm-agent-studio/internal/studiosvc"
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

// OrgLister lists the orgs the current user belongs to (satisfied by
// *studiosvc.OrgList). Returned maps carry {id,name,role}.
type OrgLister interface {
	OrgsForUser(ctx context.Context, userID string) ([]map[string]any, error)
}

// UserRegistrar creates and verifies self-serve user accounts (satisfied by *studiosvc.Register).
// A duplicate email surfaces as studiosvc.ErrEmailExists → 409.
type UserRegistrar interface {
	Create(ctx context.Context, email, password string) (string, error)
	Verify(ctx context.Context, email, code string) (bool, string, error)
	Resend(ctx context.Context, email string) error
}

// SessionIssuer mints a session (satisfied by the authz *service.Service).
type SessionIssuer interface {
	Login(ctx context.Context, email, password, userAgent string) (authzsvc.LoginResult, error)
	IssueSession(ctx context.Context, userID, userAgent string) (authzsvc.LoginResult, error)
}

// ProjectStore is the project surface the handlers need.
type ProjectStore interface {
	Create(ctx context.Context, in project.CreateInput) (project.Project, error)
	Get(ctx context.Context, id string) (project.Project, error)
	ListByOrg(ctx context.Context, orgID string, limit int, cursor string) ([]project.Project, string, error)
	Update(ctx context.Context, id string, in project.UpdateInput) (project.Project, error)
	SetStatus(ctx context.Context, id, status string) error
	// TryBeginRun 原子 CAS 把项目翻到 planning（非在途→planning）；false=已有在途 run。
	TryBeginRun(ctx context.Context, id string) (bool, error)
	SetCover(ctx context.Context, projectID, assetID string) error
	Cancel(ctx context.Context, projectID string) error
	// Deleted 供 requireLiveProject 门禁探测软删（missing → project.ErrNotFound）。
	Deleted(ctx context.Context, id string) (bool, error)
	// SoftDelete 置 tombstone + 级联取消在途 todos/assets/export_jobs（事务内）。
	SoftDelete(ctx context.Context, id string) error
	OrgIDForProject(ctx context.Context, projectID string) (string, error)
	ListPlans(ctx context.Context, projectID string) ([]project.Plan, error)
	LoadState(ctx context.Context, projectID, planID string) (projectstate.ProjectState, error)
}

// PlannerPort kicks off planning (satisfied by *planner.Planner). Only custom
// workflows are planned now — the LLM auto-planner (Plan/PlanWith) was removed.
type PlannerPort interface {
	PlanCustom(ctx context.Context, projectID, workflowID string, b planner.Brief, nodes []planner.WorkflowNode, resolved map[string]planner.ResolvedType, runInputs json.RawMessage) (planner.Result, error)
}

// CustomNodeTypeResolver resolves a typed custom node's registry entry, org-scoped
// (satisfied by *customnodetype.Store via a thin adapter). nil → typed nodes are
// rejected at run time (treated as unresolvable).
type CustomNodeTypeResolver interface {
	Get(ctx context.Context, id, orgID string) (customnodetype.CustomNodeType, error)
}

// resolveCustomTypes reads each typed node's (typeId) registry entry org-scoped and
// returns kind+params so PlanCustom can build input_json. Variable bindings are NOT
// resolved here — they live on the node's VarBindings and PlanCustom merges them.
// The handler holds org context (T2); the planner never reads the registry.
func resolveCustomTypes(ctx context.Context, res CustomNodeTypeResolver, orgID string, nodes []planner.WorkflowNode) (map[string]planner.ResolvedType, error) {
	resolved := map[string]planner.ResolvedType{}
	for _, n := range nodes {
		if n.TypeId == "" {
			continue
		}
		if res == nil {
			return nil, fmt.Errorf("custom node %q references a type but registry is unavailable", n.ID)
		}
		ct, err := res.Get(ctx, n.TypeId, orgID)
		if err != nil {
			return nil, fmt.Errorf("custom node %q: resolve type %q: %w", n.ID, n.TypeId, err)
		}
		merged := ct.Params
		if len(n.Parameters) > 0 || n.TypeVersion != 0 {
			desc, ok := nodedesc.DescByKind(ct.Kind, n.TypeVersion)
			if !ok {
				// Fail closed (spec §4.3): unknown typeVersion would mis-select the
				// danger classification. Never silently fall back to v1.
				return nil, fmt.Errorf("custom node %q: typeVersion %d unsupported (max %d); please upgrade", n.ID, n.TypeVersion, nodedesc.Version)
			}
			m, mErr := nodedesc.MergeOverlay(ct.Params, n.Parameters, desc)
			if mErr != nil {
				return nil, fmt.Errorf("custom node %q: merge params: %w", n.ID, mErr)
			}
			if vErr := customnodetype.ValidateParams(ct.Kind, m); vErr != nil {
				return nil, fmt.Errorf("custom node %q: invalid merged params: %w", n.ID, vErr)
			}
			merged = m
		}
		resolved[n.ID] = planner.ResolvedType{Kind: ct.Kind, Params: merged}
	}
	return resolved, nil
}

// ArtifactReader reads todos/script/shots for the artifact endpoints.
type ArtifactReader interface {
	Todos(ctx context.Context, projectID string, planID string) ([]map[string]any, error)
	Script(ctx context.Context, projectID string, planID string, todoID string) (json.RawMessage, bool, error)
	Shots(ctx context.Context, projectID string, planID string, todoID string) ([]map[string]any, error)
	Assets(ctx context.Context, projectID, planID, status string) ([]map[string]any, error)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// refreshCookieName mirrors authz httpapi's const verbatim — register sets the
// SAME cookie so /api/auth/refresh and /logout (which read it) work afterwards.
const refreshCookieName = "authz_refresh"

// setRefreshCookie replicates authz httpapi.setRefreshCookie EXACTLY (same name,
// Path, HttpOnly, Secure, SameSite) so the session register issues is
// indistinguishable from a login session.
func setRefreshCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: value, Path: "/api/auth",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

// registerHandler (POST /api/auth/register): OPEN, unauthenticated account
// creation. Validates input (400), creates the user (409 on existing email).
// Under email verification mode, returns {"verified": false, "email": req.Email}.
func registerHandler(reg UserRegistrar) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !strings.Contains(req.Email, "@") {
			http.Error(w, "bad request: invalid email", http.StatusBadRequest)
			return
		}
		if len(req.Password) < 8 {
			http.Error(w, "bad request: password too short (min 8)", http.StatusBadRequest)
			return
		}
		if _, err := reg.Create(r.Context(), req.Email, req.Password); errors.Is(err, studiosvc.ErrEmailExists) {
			http.Error(w, "email already exists", http.StatusConflict)
			return
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"verified": false, "email": req.Email})
	}
}

// verifyHandler (POST /api/auth/verify): checks the 6-digit code.
// On success, sets the user as verified, issues a session, and logs in.
func verifyHandler(ver UserRegistrar, iss SessionIssuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Email == "" || req.Code == "" {
			http.Error(w, "bad request: email and code required", http.StatusBadRequest)
			return
		}
		ok, userID, err := ver.Verify(r.Context(), req.Email, req.Code)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "invalid or expired verification code", http.StatusForbidden)
			return
		}

		res, err := iss.IssueSession(r.Context(), userID, r.UserAgent())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		setRefreshCookie(w, res.RefreshToken)
		writeJSON(w, http.StatusOK, map[string]any{"access_token": res.AccessToken, "expires_in": res.ExpiresIn})
	}
}

// resendVerificationHandler (POST /api/auth/resend-verification): resends code.
func resendVerificationHandler(ver UserRegistrar) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Email == "" {
			http.Error(w, "bad request: email required", http.StatusBadRequest)
			return
		}
		if err := ver.Resend(r.Context(), req.Email); err != nil {
			if err.Error() == "studiosvc: user not found" {
				http.Error(w, "user not found", http.StatusNotFound)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
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

// listOrgsHandler (GET /api/orgs): any authenticated user; lists the orgs the
// caller belongs to so the UI can offer a picker instead of a blind id entry.
func listOrgsHandler(l OrgLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := l.OrgsForUser(r.Context(), authzhttp.UserID(r.Context()))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// createProjectHandler (POST /api/orgs/{org}/projects): editor+. When the project
// enables a custom workflow with inline nodes, save-time parameter-overlay
// validation (W2, P-write-4) runs before the store write — mirrors the W1
// workflow create/update gate. The run-time resolve+worker gates stay authoritative.
func createProjectHandler(ps ProjectStore, res CustomNodeTypeResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := authzhttp.UserID(r.Context())
		var req struct {
			Name                  string          `json:"name"`
			Brief                 string          `json:"brief"`
			ContentType           string          `json:"contentType"`
			TargetPlatform        string          `json:"targetPlatform"`
			Style                 string          `json:"style"`
			PlannerProvider       string          `json:"plannerProvider"`
			PlannerModel          string          `json:"plannerModel"`
			ImageProvider         string          `json:"imageProvider"`
			ImageModel            string          `json:"imageModel"`
			StorageMode           string          `json:"storageMode"`
			StorageConfigID       string          `json:"storageConfigId"`
			CustomWorkflowEnabled bool            `json:"customWorkflowEnabled"`
			WorkflowNodes         json.RawMessage `json:"workflowNodes"`
			Kind                  string          `json:"kind"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
		}
		if req.CustomWorkflowEnabled && len(req.WorkflowNodes) > 0 {
			var nodes []planner.WorkflowNode
			if err := json.Unmarshal(req.WorkflowNodes, &nodes); err != nil {
				http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := validateNodeParameterOverlays(r.Context(), res, r.PathValue("org"), nodes); err != nil {
				http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		p, err := ps.Create(r.Context(), project.CreateInput{
			OrgID: r.PathValue("org"), Name: req.Name, Brief: req.Brief,
			ContentType: req.ContentType, TargetPlatform: req.TargetPlatform,
			Style: req.Style, CreatedBy: uid,
			PlannerProvider:       req.PlannerProvider,
			PlannerModel:          req.PlannerModel,
			ImageProvider:         req.ImageProvider,
			ImageModel:            req.ImageModel,
			StorageMode:           req.StorageMode,
			StorageConfigID:       req.StorageConfigID,
			CustomWorkflowEnabled: req.CustomWorkflowEnabled,
			WorkflowNodes:         req.WorkflowNodes,
			Kind:                  req.Kind,
		})
		if errors.Is(err, project.ErrInvalidStorageConfig) ||
			errors.Is(err, project.ErrEmptyName) ||
			errors.Is(err, project.ErrNameTooLong) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		} else if err != nil {
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

// updateProjectHandler (PUT /api/projects/{id}): editor+. 允许编辑项目基本信息
// （名称/创意需求/内容类型/目标平台/风格）+ 规划/图片模型 + 存储方式。
// body=project.UpdateInput 形式，返 200 + 更新后的 Project DTO。找不到 → 404。
// 名称为空 → 400（与 create 的 name 必填对齐）。
func updateProjectHandler(ps ProjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in project.UpdateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad request: invalid body", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			http.Error(w, "bad request: name is required", http.StatusBadRequest)
			return
		}
		p, err := ps.Update(r.Context(), r.PathValue("id"), in)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if errors.Is(err, project.ErrInvalidStorageConfig) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// quotaExceeded reports whether the org used up its rolling-24h generation
// quota (spec §12 生成调用配额，防成本失控). quota<=0 disables the check.
func quotaExceeded(ctx context.Context, cs CostStore, quota int, orgID string) (bool, error) {
	if quota <= 0 {
		return false, nil
	}
	n, err := cs.CountByOrgSince(ctx, orgID, time.Now().Add(-24*time.Hour))
	if err != nil {
		return false, err
	}
	return n >= quota, nil
}

// deleteProjectHandler (DELETE /api/projects/{id}): admin——项目级最大破坏性操作，
// 与成本中心/存储配置同级门禁（spec §2）。软删：置 deleted_at + 级联取消在途
// todos/assets/export_jobs（事务内，SoftDelete）。幂等：重复删除/不存在 → 404
// （与 workflow/prompt/model-config 的 DELETE 端点对 missing 一致；正常情况下
// 重复删除已被 requireLiveProject 门禁 404，这里兜竞态）。
func deleteProjectHandler(ps ProjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := ps.SoftDelete(r.Context(), r.PathValue("id"))
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		planID := r.URL.Query().Get("planId")
		evs, err := reader.List(r.Context(), r.PathValue("id"), planID, after, 200)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": evs})
	}
}

// listPlansHandler (GET /api/projects/{id}/plans): viewer+.
func listPlansHandler(ps ProjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		plans, err := ps.ListPlans(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": plans})
	}
}

// StateReader is the project-state surface the /state endpoint + SSE need.
type StateReader interface {
	LoadState(ctx context.Context, projectID, planID string) (projectstate.ProjectState, error)
}

// stateHandler (GET /api/projects/{id}/state): viewer+. Returns the
// authoritative semantic snapshot computed by projectstate.Compute.
// Accepts optional ?planId=<id> to scope the state to a specific run;
// when omitted, returns the latest run's state (unchanged behavior).
func stateHandler(sr StateReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		planID := r.URL.Query().Get("planId")
		st, err := sr.LoadState(r.Context(), r.PathValue("id"), planID)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}

// artifactHandlers (GET .../todos|script|shots): viewer+.
func todosHandler(ar ArtifactReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		planID := r.URL.Query().Get("planId")
		items, err := ar.Todos(r.Context(), r.PathValue("id"), planID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func scriptHandler(ar ArtifactReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		planID := r.URL.Query().Get("planId")
		todoID := r.URL.Query().Get("todoId")
		content, ok, err := ar.Script(r.Context(), r.PathValue("id"), planID, todoID)
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
		planID := r.URL.Query().Get("planId")
		todoID := r.URL.Query().Get("todoId")
		items, err := ar.Shots(r.Context(), r.PathValue("id"), planID, todoID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// projectAssetsHandler (GET /api/projects/{id}/assets?status=): viewer+.
func projectAssetsHandler(ar ArtifactReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		planID := r.URL.Query().Get("planId")
		items, err := ar.Assets(r.Context(), r.PathValue("id"), planID, r.URL.Query().Get("status"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// roleAdmin / roleEditor / roleViewer aliases for readability at mount sites.
var (
	roleViewer = authzrole.RoleViewer
	roleEditor = authzrole.RoleEditor
	roleAdmin  = authzrole.RoleAdmin
)
