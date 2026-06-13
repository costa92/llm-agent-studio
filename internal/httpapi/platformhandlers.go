package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"

	"github.com/costa92/llm-agent-studio/internal/studiosvc"
)

// PlatformService 是平台超级管理员的 HTTP 暴露面（由 *studiosvc.Platform 满足）。
// 平台管理员 = 一条 authz membership (org_id=”, scope_kind='platform', role=admin)。
type PlatformService interface {
	IsPlatformAdmin(ctx context.Context, userID string) (bool, error)
	ListAdmins(ctx context.Context) ([]studiosvc.PlatformAdmin, error)
	GrantByEmail(ctx context.Context, email string) (string, error)
	Revoke(ctx context.Context, userID string) error
	ListAllOrgs(ctx context.Context) ([]map[string]any, error)
	ListUsers(ctx context.Context) ([]studiosvc.PlatformUser, error)
	UserDetail(ctx context.Context, userID string) (studiosvc.UserDetail, error)
	DeleteUser(ctx context.Context, userID string) error
	ResetUserPassword(ctx context.Context, userID, newPassword string) error
}

// platformScope 把请求映射到平台 scope (orgID="", scopeID="")，供 RequireScopeRole
// 以 scope_kind="platform" 解析角色——平台 membership 不绑任何业务 org。
func platformScope(*http.Request) (string, string) { return "", "" }

// platformWhoamiHandler (GET /api/platform/whoami): authOnly（不经平台门禁），
// 返回 {isPlatformAdmin: bool}，供前端决定是否展示平台导航而不必吃一个 403。
func platformWhoamiHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := authzhttp.UserID(r.Context())
		ok, err := p.IsPlatformAdmin(r.Context(), uid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"isPlatformAdmin": ok})
	}
}

// platformOrgsHandler (GET /api/platform/orgs): 平台门禁，列出所有业务 org（含成员数）。
func platformOrgsHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := p.ListAllOrgs(r.Context())
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

// platformListAdminsHandler (GET /api/platform/admins): 平台门禁，列出所有平台管理员。
func platformListAdminsHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admins, err := p.ListAdmins(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if admins == nil {
			admins = []studiosvc.PlatformAdmin{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": admins})
	}
}

// platformGrantAdminHandler (POST /api/platform/admins) body {email}: 平台门禁，
// 按邮箱授予平台管理员。无对应用户 → 404；成功 → 200 {userId}。
func platformGrantAdminHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
			http.Error(w, "bad request: email required", http.StatusBadRequest)
			return
		}
		uid, err := p.GrantByEmail(r.Context(), req.Email)
		if errors.Is(err, studiosvc.ErrUserNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"userId": uid})
	}
}

// platformRevokeAdminHandler (DELETE /api/platform/admins/{userId}): 平台门禁。
// 守护：禁止撤销最后一名平台管理员——否则平台将永久无人可管理（也覆盖了自撤的退化情形：
// 当自己是唯一管理员时一并拦下）。成功 → 200 {ok:true}；尝试撤销最后一名 → 409。
func platformRevokeAdminHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.PathValue("userId")
		if target == "" {
			http.Error(w, "bad request: userId required", http.StatusBadRequest)
			return
		}
		admins, err := p.ListAdmins(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// 仅当 target 当前确实是平台管理员、且是唯一一名时，拒绝撤销（防把平台管成无人可管）。
		if len(admins) <= 1 {
			for _, a := range admins {
				if a.UserID == target {
					http.Error(w, "cannot remove the last platform admin", http.StatusConflict)
					return
				}
			}
		}
		if err := p.Revoke(r.Context(), target); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// platformListUsersHandler (GET /api/platform/users): 平台门禁，列出所有用户
// （含是否平台管理员与所属 org 数）。nil → []。
func platformListUsersHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := p.ListUsers(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if users == nil {
			users = []studiosvc.PlatformUser{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": users})
	}
}

// platformUserDetailHandler (GET /api/platform/users/{userId}): 平台门禁，
// 返回用户详情（身份 + 平台管理员标志 + org 归属）。无对应用户 → 404。
func platformUserDetailHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.PathValue("userId")
		if userID == "" {
			http.Error(w, "bad request: userId required", http.StatusBadRequest)
			return
		}
		d, err := p.UserDetail(r.Context(), userID)
		if errors.Is(err, studiosvc.ErrUserNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, d)
	}
}

// platformDeleteUserHandler (DELETE /api/platform/users/{userId}): 平台门禁。
// 守护：不能删除自己（409）；不能删除最后一名平台管理员（409，否则平台将无人可管）。
// 无对应用户 → 404；成功 → 200 {ok:true}。
func platformDeleteUserHandler(p PlatformService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.PathValue("userId")
		if target == "" {
			http.Error(w, "bad request: userId required", http.StatusBadRequest)
			return
		}
		if target == authzhttp.UserID(r.Context()) {
			http.Error(w, "cannot delete yourself", http.StatusConflict)
			return
		}
		admins, err := p.ListAdmins(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// 仅当 target 当前是平台管理员、且是唯一一名时，拒绝删除（防把平台管成无人可管）。
		if len(admins) <= 1 {
			for _, a := range admins {
				if a.UserID == target {
					http.Error(w, "cannot remove the last platform admin", http.StatusConflict)
					return
				}
			}
		}
		if err := p.DeleteUser(r.Context(), target); errors.Is(err, studiosvc.ErrUserNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
