package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"
	authzsvc "github.com/costa92/llm-agent-authz/service"
	"github.com/costa92/llm-agent-contract/llm"

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
	SetCover(ctx context.Context, projectID, assetID string) error
	Cancel(ctx context.Context, projectID string) error
	OrgIDForProject(ctx context.Context, projectID string) (string, error)
	ListPlans(ctx context.Context, projectID string) ([]project.Plan, error)
	LoadState(ctx context.Context, projectID string) (projectstate.ProjectState, error)
}

// PlannerPort kicks off planning (satisfied by *planner.Planner). PlanWith
// accepts an explicit chat model (BYOK 模型路由 via the ChatRouter); Plan uses
// the planner's bound default.
type PlannerPort interface {
	Plan(ctx context.Context, projectID string, b planner.Brief) (planner.Result, error)
	PlanWith(ctx context.Context, projectID string, model llm.ChatModel, b planner.Brief) (planner.Result, error)
	PlanCustom(ctx context.Context, projectID, workflowID string, b planner.Brief, nodes []planner.WorkflowNode) (planner.Result, error)
}

// ChatRouter resolves an org's BYOK chat model (satisfied by *modelrouter.Router).
// nil in Deps → the run handler uses PlannerPort.Plan (the bound default).
type ChatRouter interface {
	ChatModelFor(ctx context.Context, orgID string) llm.ChatModel
	// M5.1: per-project 规划模型 override 解析；caller 在 ChatModelForNamed
	// 返 nil 时退回 ChatModelFor。
	ChatModelForNamed(ctx context.Context, orgID, provider, modelName string) llm.ChatModel
}

// ArtifactReader reads todos/script/shots for the artifact endpoints.
type ArtifactReader interface {
	Todos(ctx context.Context, projectID string, planID string) ([]map[string]any, error)
	Script(ctx context.Context, projectID string, planID string) (json.RawMessage, bool, error)
	Shots(ctx context.Context, projectID string, planID string) ([]map[string]any, error)
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

// createProjectHandler (POST /api/orgs/{org}/projects): editor+.
func createProjectHandler(ps ProjectStore) http.HandlerFunc {
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
			CustomWorkflowEnabled bool            `json:"customWorkflowEnabled"`
			WorkflowNodes         json.RawMessage `json:"workflowNodes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
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
			CustomWorkflowEnabled: req.CustomWorkflowEnabled,
			WorkflowNodes:         req.WorkflowNodes,
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

// runHandler (POST /api/projects/{id}/run): editor+. Sets status=planning, runs
// the planner (synchronously enqueues todos), emits planner_started.
func runHandler(ps ProjectStore, pl PlannerPort, ev EventAppender, cs CostStore, quota int, cr ChatRouter) http.HandlerFunc {
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
		if over, err := quotaExceeded(r.Context(), cs, quota, p.OrgID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if over {
			http.Error(w, "generation quota exceeded for org", http.StatusTooManyRequests)
			return
		}
		if err := ps.SetStatus(r.Context(), id, "planning"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = ev.Append(r.Context(), id, "planner_started", "", nil)
		brief := planner.Brief{
			Brief: p.Description, ContentType: p.ContentType,
			TargetPlatform: p.TargetPlatform, Style: p.Style,
		}
		var res planner.Result
		if p.CustomWorkflowEnabled {
			var nodes []planner.WorkflowNode
			if len(p.WorkflowNodes) > 0 {
				if err := json.Unmarshal(p.WorkflowNodes, &nodes); err != nil {
					http.Error(w, "invalid custom workflow configuration: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
			// Legacy project-level custom run: no first-class workflow → NULL workflow_id.
			res, err = pl.PlanCustom(r.Context(), id, "", brief, nodes)
		} else {
			// M5.1: per-project 规划模型 override 优先于 org 默认。如果 project 上
			// 配了 planner_provider+planner_model，runHandler 拿这个去 modelrouter
			// 查 org 的对应 model_config（可能也是默认之一），拿其 key 走 buildChat。
			// 查不到 / build 失败 → 退回 org 默认 chat。空 = 走完全默认。
			plannerModel := chatModelForPlan(r.Context(), cr, p)
			if plannerModel != nil {
				res, err = pl.PlanWith(r.Context(), id, plannerModel, brief)
			} else {
				res, err = pl.Plan(r.Context(), id, brief)
			}
		}
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

// chatModelForPlan 决定本次 run 用哪个 chat model 给 planner。优先级：
//  1. project.PlannerProvider + PlannerModel 显式指定（per-project override），
//     经 router.ChatModelForNamed 查 org 的对应 model_config 拿其 key 复用。
//  2. 退到 router.ChatModelFor(org) 的 org 默认。
//  3. cr==nil（无 router）= 返 nil，caller 走 pl.Plan 走 planner 自绑默认。
//
// 返 nil = "caller 用 pl.Plan / planner bound default"。这条路径在没有 org
// 任何 chat model 配置时也会命中——planner.go:54 的 model: llm.NewScriptedLLM
// 是 dev 兜底。
func chatModelForPlan(ctx context.Context, cr ChatRouter, p project.Project) llm.ChatModel {
	if cr == nil {
		return nil
	}
	if p.PlannerProvider != "" && p.PlannerModel != "" {
		if m := cr.ChatModelForNamed(ctx, p.OrgID, p.PlannerProvider, p.PlannerModel); m != nil {
			return m
		}
		// override 解析不出来：不静默改用 org 默认——记日志（router 内部已 warn，
		// 这里补一条带 project 上下文的，方便 trace）。
		slog.Default().Warn("runHandler: project planner override not resolvable; using org default chat",
			"org", p.OrgID, "project", p.ID,
			"provider", p.PlannerProvider, "model", p.PlannerModel)
	}
	return cr.ChatModelFor(ctx, p.OrgID)
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
	LoadState(ctx context.Context, projectID string) (projectstate.ProjectState, error)
}

// stateHandler (GET /api/projects/{id}/state): viewer+. Returns the
// authoritative semantic snapshot computed by projectstate.Compute.
func stateHandler(sr StateReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := sr.LoadState(r.Context(), r.PathValue("id"))
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
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
		content, ok, err := ar.Script(r.Context(), r.PathValue("id"), planID)
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
		items, err := ar.Shots(r.Context(), r.PathValue("id"), planID)
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
