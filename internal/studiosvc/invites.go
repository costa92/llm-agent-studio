package studiosvc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authzstore "github.com/costa92/llm-agent-authz/store"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/orginvite"
)

// 邀请默认有效期：14 天。到期后 token 失效，需重新邀请。
const inviteTTL = 14 * 24 * time.Hour

// ErrAlreadyMember 在被邀请邮箱已是该 org 成员时返回 → handler 映射 409。
// （既有用户直接用「添加成员」即可，无需走邀请。）
var ErrAlreadyMember = errors.New("studiosvc: email is already an org member")

// ErrInviteNotPending 表示邀请已被接受或撤销 → handler 映射 409。
var ErrInviteNotPending = errors.New("studiosvc: invite is not pending")

// ErrInviteExpired 表示邀请已过期 → handler 映射 410。
var ErrInviteExpired = errors.New("studiosvc: invite has expired")

// ErrInviteEmailMismatch 表示当前登录用户的邮箱与邀请目标邮箱不符 → handler 映射 403。
// 邀请是定向的：只有收件邮箱本人能接受，不能转让。
var ErrInviteEmailMismatch = errors.New("studiosvc: invite email does not match the logged-in user")

// Invites 编排组织邀请生命周期：持久化走 orginvite.Store；成员授予复用
// authz.UpsertMembership（与 Members 一致）；已是成员/邮箱归属等校验直接查 auth 表
// （authz store 不暴露聚合查询，镜像 Members 的做法）。
type Invites struct {
	store *orginvite.Store
	authz *authzstore.Store
	db    *gorm.DB
}

// NewInvites 构造 Invites 适配器。
func NewInvites(store *orginvite.Store, az *authzstore.Store, db *gorm.DB) *Invites {
	return &Invites{store: store, authz: az, db: db}
}

// AcceptResult 是接受邀请成功后回传给 handler 的最小信息：接受者被授予角色的 org 与角色，
// 供前端接受后跳转到该 org。
type AcceptResult struct {
	OrgID string `json:"orgId"`
	Role  string `json:"role"`
}

// CreateInvite 为 orgID 创建一封发给 email、拟授角色 r 的 pending 邀请（invitedBy=邀请人
// userID）。email 已是成员 → ErrAlreadyMember。重复邀请：先撤掉该邮箱现存 pending 再发新
// （刷新 token 与有效期，旧链接失效）。返回的 Invite 带 token 供调用方拼邀请链接。
func (i *Invites) CreateInvite(ctx context.Context, orgID, email string, r authzrole.Role, invitedBy string) (orginvite.Invite, error) {
	normalized := normalizePlatformEmail(email)
	if normalized == "" {
		return orginvite.Invite{}, fmt.Errorf("studiosvc: invite email required")
	}
	member, err := i.isOrgMember(ctx, orgID, normalized)
	if err != nil {
		return orginvite.Invite{}, err
	}
	if member {
		return orginvite.Invite{}, ErrAlreadyMember
	}
	if err := i.store.RevokePendingByEmail(ctx, orgID, normalized); err != nil {
		return orginvite.Invite{}, err
	}
	return i.store.Create(ctx, orgID, normalized, string(r), invitedBy, inviteTTL)
}

// ListInvites 列出 orgID 的待接受邀请（pending，倒序）。空时返回非 nil 空切片。
func (i *Invites) ListInvites(ctx context.Context, orgID string) ([]orginvite.Invite, error) {
	return i.store.ListByOrg(ctx, orgID)
}

// RevokeInvite 撤销 (org,id) 的 pending 邀请。跨租户/不存在/非 pending → orginvite.ErrNotFound。
func (i *Invites) RevokeInvite(ctx context.Context, orgID, id string) error {
	return i.store.Revoke(ctx, orgID, id)
}

// AcceptInvite 由已登录用户 actorUserID 凭 token 接受邀请：校验 pending + 未过期 +
// 当前用户邮箱与邀请目标一致后，按邀请角色授予 org-level membership（复用
// UpsertMembership，与「添加成员」同一落库路径）并标记 accepted。这条 studio 侧授予
// 取代了旧「用户须预先存在 + 管理员手动添加」的唯一路径——被邀请人自助注册/登录后即可入组织。
//   - token 无对应邀请 → orginvite.ErrNotFound
//   - 邀请非 pending（已接受/已撤销）→ ErrInviteNotPending
//   - 邀请已过期 → ErrInviteExpired
//   - 当前用户邮箱 ≠ 邀请邮箱 → ErrInviteEmailMismatch
func (i *Invites) AcceptInvite(ctx context.Context, token, actorUserID string) (AcceptResult, error) {
	inv, err := i.store.GetByToken(ctx, token)
	if err != nil {
		return AcceptResult{}, err
	}
	if inv.Status != orginvite.StatusPending {
		return AcceptResult{}, ErrInviteNotPending
	}
	if time.Now().After(inv.ExpiresAt) {
		return AcceptResult{}, ErrInviteExpired
	}
	actorEmail, err := i.emailByUserID(ctx, actorUserID)
	if err != nil {
		return AcceptResult{}, err
	}
	// 邀请邮箱建时已归一化；接受者邮箱同样归一化后严格比对（定向邀请不可转让）。
	if normalizePlatformEmail(actorEmail) != inv.Email {
		return AcceptResult{}, ErrInviteEmailMismatch
	}
	role, err := authzrole.Parse(inv.Role)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("studiosvc: invite carries invalid role %q: %w", inv.Role, err)
	}
	if err := i.authz.UpsertMembership(ctx, inv.OrgID, actorUserID, "org", nil, role); err != nil {
		return AcceptResult{}, fmt.Errorf("studiosvc: accept invite grant membership: %w", err)
	}
	if err := i.store.MarkAccepted(ctx, inv.ID); err != nil {
		// membership 已授予；标记失败仅让该邀请留在 pending（下次接受为幂等 upsert）。
		return AcceptResult{}, fmt.Errorf("studiosvc: accept invite mark accepted: %w", err)
	}
	return AcceptResult{OrgID: inv.OrgID, Role: inv.Role}, nil
}

// isOrgMember 报告 email 对应用户是否已是 orgID 的 org-level 成员。无该用户/无 membership
// → false（可被邀请）。
func (i *Invites) isOrgMember(ctx context.Context, orgID, normalizedEmail string) (bool, error) {
	var one int
	err := i.db.WithContext(ctx).Raw(
		`SELECT 1 FROM auth_membership m JOIN auth_user u ON u.id=m.user_id
		  WHERE u.email=$1 AND u.deleted_at IS NULL
		    AND m.org_id=$2 AND m.scope_kind='org' AND m.scope_id IS NULL
		  LIMIT 1`, normalizedEmail, orgID).Row().Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("studiosvc: check existing member: %w", err)
	}
	return true, nil
}

// emailByUserID 读取 userID 的邮箱（auth_user 反查，userIDByEmail 的逆向）。接受邀请时
// ctx 只带 UserID（authzhttp.UserID），须据此取邮箱与邀请目标比对。无对应用户 → ErrUserNotFound。
func (i *Invites) emailByUserID(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", ErrUserNotFound
	}
	var email string
	err := i.db.WithContext(ctx).Raw(
		`SELECT email FROM auth_user WHERE id=$1 AND deleted_at IS NULL`, userID).Row().Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("studiosvc: lookup email by user id: %w", err)
	}
	return email, nil
}
