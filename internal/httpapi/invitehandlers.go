package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"

	"github.com/costa92/llm-agent-studio/internal/orginvite"
	"github.com/costa92/llm-agent-studio/internal/studiosvc"
)

// InviteService 是组织邀请生命周期的 HTTP 暴露面（由 *studiosvc.Invites 满足）。
// 邀请 = 一封 org_invites 待接受记录；接受时按角色落 org-level authz membership。
type InviteService interface {
	CreateInvite(ctx context.Context, orgID, email string, r authzrole.Role, invitedBy string) (orginvite.Invite, error)
	ListInvites(ctx context.Context, orgID string) ([]orginvite.Invite, error)
	RevokeInvite(ctx context.Context, orgID, id string) error
	AcceptInvite(ctx context.Context, token, actorUserID string) (studiosvc.AcceptResult, error)
}

// InviteMailer 发一封邀请邮件（由 *mail.Client 满足）。nil → 不发信（聚焦单测/邮件未配置），
// 邀请仍创建成功，管理员可从返回的链接手动分享。
type InviteMailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// listInvitesHandler (GET /api/orgs/{org}/invites): admin，列出待接受邀请。nil → []。
func listInvitesHandler(s InviteService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.ListInvites(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []orginvite.Invite{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// createInviteHandler (POST /api/orgs/{org}/invites) body {email, role}: admin。
// 缺 email / 解码失败 → 400；非法角色 → 400；该邮箱已是成员 → 409；成功 → 201 Invite（含
// token）。best-effort 发送邀请邮件（mailer/baseURL 齐备时）；发信失败不影响创建，管理员
// 仍可用响应里的 token 拼链接分享。
func createInviteHandler(s InviteService, mailer InviteMailer, baseURL string) http.HandlerFunc {
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
		orgID := r.PathValue("org")
		inv, err := s.CreateInvite(r.Context(), orgID, req.Email, role, authzhttp.UserID(r.Context()))
		switch {
		case errors.Is(err, studiosvc.ErrAlreadyMember):
			http.Error(w, "email is already a member", http.StatusConflict)
			return
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sendInviteMail(r.Context(), mailer, baseURL, inv)
		writeJSON(w, http.StatusCreated, inv)
	}
}

// revokeInviteHandler (DELETE /api/orgs/{org}/invites/{id}): admin。
// 非本 org 的 pending 邀请 → 404；成功 → 200 {ok:true}。
func revokeInviteHandler(s InviteService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "bad request: id required", http.StatusBadRequest)
			return
		}
		err := s.RevokeInvite(r.Context(), r.PathValue("org"), id)
		switch {
		case errors.Is(err, orginvite.ErrNotFound):
			http.Error(w, "invite not found", http.StatusNotFound)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		}
	}
}

// acceptInviteHandler (POST /api/invites/{token}/accept): 任意登录用户。
// 接受者取自 ctx 的 UserID（authzhttp.UserID）。token 无效 → 404；已接受/撤销 → 409；
// 已过期 → 410；邮箱不符 → 403；成功 → 200 {orgId, role}。
func acceptInviteHandler(s InviteService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if token == "" {
			http.Error(w, "bad request: token required", http.StatusBadRequest)
			return
		}
		res, err := s.AcceptInvite(r.Context(), token, authzhttp.UserID(r.Context()))
		switch {
		case errors.Is(err, orginvite.ErrNotFound):
			http.Error(w, "invite not found", http.StatusNotFound)
		case errors.Is(err, studiosvc.ErrInviteNotPending):
			http.Error(w, "invite is no longer pending", http.StatusConflict)
		case errors.Is(err, studiosvc.ErrInviteExpired):
			http.Error(w, "invite has expired", http.StatusGone)
		case errors.Is(err, studiosvc.ErrInviteEmailMismatch):
			http.Error(w, "invite is for a different email", http.StatusForbidden)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, res)
		}
	}
}

// sendInviteMail best-effort 投递邀请邮件：mailer 为 nil 直接返回（未配置/聚焦单测）。
// 邮件为中文纯文本，风格对照告警邮件；有 baseURL 时带可点邀请链接，否则给出 token 让管理员
// 转达。发信在后台 goroutine 里跑（用 background ctx），绝不阻塞管理员的创建请求，失败仅记日志。
func sendInviteMail(_ context.Context, mailer InviteMailer, baseURL string, inv orginvite.Invite) {
	if mailer == nil {
		return
	}
	subject := "【AI Studio】邀请你加入组织"
	link := inviteLink(baseURL, inv.Token)
	body := "你好，\n\n有人邀请你加入一个 AI Studio 组织协作。\n\n"
	if link != "" {
		body += "请登录（或先注册）后打开以下链接接受邀请：\n" + link + "\n\n"
	} else {
		body += "请登录（或先注册）后，在接受邀请页填入以下邀请码：\n" + inv.Token + "\n\n"
	}
	body += "注意：邀请须由本邮箱对应的账号接受。\n\n—— AI Studio"
	go func() {
		if err := mailer.Send(context.Background(), inv.Email, subject, body); err != nil {
			log.Printf("invite: send mail to %s failed: %v", inv.Email, err)
		}
	}()
}

// inviteLink 由控制台外部 base URL 与 token 拼接接受链接。baseURL 为空 → 返回空串（邮件改带
// token 让管理员转达）。前端路由为 /invites/{token}。
func inviteLink(baseURL, token string) string {
	if baseURL == "" {
		return ""
	}
	for len(baseURL) > 0 && baseURL[len(baseURL)-1] == '/' {
		baseURL = baseURL[:len(baseURL)-1]
	}
	return baseURL + "/invites/" + token
}
