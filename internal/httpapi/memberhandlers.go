package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"

	"github.com/costa92/llm-agent-studio/internal/studiosvc"
)

// MemberService 是 org 成员管理的 HTTP 暴露面（由 *studiosvc.Members 满足）。
// org 成员 = 一条 org-level authz membership (scope_kind='org', scope_id=nil)。
type MemberService interface {
	ListMembers(ctx context.Context, orgID string) ([]studiosvc.OrgMember, error)
	AddMemberByEmail(ctx context.Context, orgID, email string, r authzrole.Role) (studiosvc.OrgMember, error)
	SetMemberRole(ctx context.Context, orgID, userID string, r authzrole.Role) error
	RemoveMember(ctx context.Context, orgID, userID string) error
}

// listMembersHandler (GET /api/orgs/{org}/members): viewer+，列出 org 成员名册。nil → []。
func listMembersHandler(s MemberService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.ListMembers(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []studiosvc.OrgMember{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// meRoleHandler (GET /api/orgs/{org}/members/me): viewer+，返回调用者在该 org 的有效角色。
// 前端角色感知层用它区分 viewer/editor/admin（替代对 admin-only 端点的 200/403 二元探针），
// 从而正确门控写 CTA（viewer 不显示新建/保存/设封面）。非成员由路由 scoped(roleViewer) 拦为 403。
func meRoleHandler(rr authzhttp.RoleResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := authzhttp.UserID(r.Context())
		eff, err := rr.ResolveRole(r.Context(), uid, r.PathValue("org"), "org", "")
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"userId": uid, "role": string(eff)})
	}
}

// addMemberHandler (POST /api/orgs/{org}/members) body {email, role}: admin。
// 缺 email / 解码失败 → 400；非法角色 → 400；无对应用户 → 404；成功 → 201 OrgMember。
func addMemberHandler(s MemberService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
			http.Error(w, "bad request: email required", http.StatusBadRequest)
			return
		}
		role, err := authzrole.Parse(req.Role)
		if err != nil {
			http.Error(w, "bad request: invalid role", http.StatusBadRequest)
			return
		}
		mem, err := s.AddMemberByEmail(r.Context(), r.PathValue("org"), req.Email, role)
		if errors.Is(err, studiosvc.ErrUserNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, mem)
	}
}

// setMemberRoleHandler (PUT /api/orgs/{org}/members/{userId}) body {role}: admin。
// 空 userId / 非法角色 → 400；非成员 → 404；最后一名 org_admin 降级 → 409；成功 → 200 {ok:true}。
func setMemberRoleHandler(s MemberService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.PathValue("userId")
		if userID == "" {
			http.Error(w, "bad request: userId required", http.StatusBadRequest)
			return
		}
		var req struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: role required", http.StatusBadRequest)
			return
		}
		role, err := authzrole.Parse(req.Role)
		if err != nil {
			http.Error(w, "bad request: invalid role", http.StatusBadRequest)
			return
		}
		err = s.SetMemberRole(r.Context(), r.PathValue("org"), userID, role)
		switch {
		case errors.Is(err, studiosvc.ErrMemberNotFound):
			http.Error(w, "member not found", http.StatusNotFound)
		case errors.Is(err, studiosvc.ErrLastOrgAdmin):
			http.Error(w, "cannot demote the last org admin", http.StatusConflict)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		}
	}
}

// removeMemberHandler (DELETE /api/orgs/{org}/members/{userId}): admin。
// 空 userId → 400；非成员 → 404；最后一名 org_admin → 409；成功 → 200 {ok:true}。
func removeMemberHandler(s MemberService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.PathValue("userId")
		if userID == "" {
			http.Error(w, "bad request: userId required", http.StatusBadRequest)
			return
		}
		err := s.RemoveMember(r.Context(), r.PathValue("org"), userID)
		switch {
		case errors.Is(err, studiosvc.ErrMemberNotFound):
			http.Error(w, "member not found", http.StatusNotFound)
		case errors.Is(err, studiosvc.ErrLastOrgAdmin):
			http.Error(w, "cannot remove the last org admin", http.StatusConflict)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		}
	}
}
