package studiosvc

import (
	"context"
	"errors"
	"fmt"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authzstore "github.com/costa92/llm-agent-authz/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLastOrgAdmin 由 SetMemberRole/RemoveMember 在该操作会移除 org 仅存的 org_admin
// 时返回 → handler 映射 409。否则该 org 将永久无人可管理（镜像平台层的 last-admin 守护）。
var ErrLastOrgAdmin = errors.New("studiosvc: cannot remove or demote the last org admin")

// ErrMemberNotFound 在目标 userID 不是该 org 的成员时返回 → handler 映射 404。
var ErrMemberNotFound = errors.New("studiosvc: membership not found")

// Members 管理某个业务 org 的成员名册（org-level membership：scope_kind="org",
// scope_id=nil）。authz store 不暴露跨表/聚合查询，故读取直接走共享 pool
// （镜像 Platform / OrgList 的做法）；写入复用 authz.UpsertMembership。
type Members struct {
	authz *authzstore.Store
	pool  *pgxpool.Pool
}

// OrgMember 是 org 成员名册里的一行：身份 + 角色。
type OrgMember struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

// NewMembers 构造 Members 适配器。
func NewMembers(az *authzstore.Store, pool *pgxpool.Pool) *Members {
	return &Members{authz: az, pool: pool}
}

// ListMembers 列出 orgID 的 org-level 成员（按 email 升序）。空时返回非 nil 空切片。
func (m *Members) ListMembers(ctx context.Context, orgID string) ([]OrgMember, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT m.user_id, u.email, m.role
		   FROM auth_membership m JOIN auth_user u ON u.id=m.user_id
		  WHERE m.org_id=$1 AND m.scope_kind='org' AND m.scope_id IS NULL
		  ORDER BY u.email ASC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("studiosvc: list members: %w", err)
	}
	defer rows.Close()
	out := make([]OrgMember, 0)
	for rows.Next() {
		var mem OrgMember
		if err := rows.Scan(&mem.UserID, &mem.Email, &mem.Role); err != nil {
			return nil, fmt.Errorf("studiosvc: scan member: %w", err)
		}
		out = append(out, mem)
	}
	return out, rows.Err()
}

// AddMemberByEmail 按邮箱查 auth_user 后授予 orgID 的 org-level 角色 r（幂等 upsert：
// 已是成员则改角色）。无对应用户 → ErrUserNotFound。
func (m *Members) AddMemberByEmail(ctx context.Context, orgID, email string, r authzrole.Role) (OrgMember, error) {
	uid, normalized, err := m.userIDByEmail(ctx, email)
	if err != nil {
		return OrgMember{}, err
	}
	if err := m.authz.UpsertMembership(ctx, orgID, uid, "org", nil, r); err != nil {
		return OrgMember{}, fmt.Errorf("studiosvc: add org member: %w", err)
	}
	return OrgMember{UserID: uid, Email: normalized, Role: string(r)}, nil
}

// SetMemberRole 修改 userID 在 orgID 的 org-level 角色。非成员 → ErrMemberNotFound。
// 守护：若 userID 当前是 org_admin、新角色不再 ≥ org_admin、且其为该 org 仅存的
// org_admin → ErrLastOrgAdmin。
func (m *Members) SetMemberRole(ctx context.Context, orgID, userID string, r authzrole.Role) error {
	cur, ok, err := m.currentOrgRole(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrMemberNotFound
	}
	if cur == authzrole.RoleOrgAdmin && !r.AtLeast(authzrole.RoleOrgAdmin) {
		n, err := m.countOrgAdmins(ctx, orgID)
		if err != nil {
			return err
		}
		if n == 1 {
			return ErrLastOrgAdmin
		}
	}
	if err := m.authz.UpsertMembership(ctx, orgID, userID, "org", nil, r); err != nil {
		return fmt.Errorf("studiosvc: set member role: %w", err)
	}
	return nil
}

// RemoveMember 移除 userID 在 orgID 的 org-level membership。非成员 → ErrMemberNotFound。
// 守护：若 userID 是该 org 仅存的 org_admin → ErrLastOrgAdmin。
func (m *Members) RemoveMember(ctx context.Context, orgID, userID string) error {
	cur, ok, err := m.currentOrgRole(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrMemberNotFound
	}
	if cur == authzrole.RoleOrgAdmin {
		n, err := m.countOrgAdmins(ctx, orgID)
		if err != nil {
			return err
		}
		if n == 1 {
			return ErrLastOrgAdmin
		}
	}
	tag, err := m.pool.Exec(ctx,
		`DELETE FROM auth_membership WHERE org_id=$1 AND user_id=$2 AND scope_kind='org' AND scope_id IS NULL`,
		orgID, userID)
	if err != nil {
		return fmt.Errorf("studiosvc: remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// countOrgAdmins 返回 orgID 的 org-level org_admin 数。
func (m *Members) countOrgAdmins(ctx context.Context, orgID string) (int, error) {
	var n int
	if err := m.pool.QueryRow(ctx,
		`SELECT count(*) FROM auth_membership
		  WHERE org_id=$1 AND scope_kind='org' AND scope_id IS NULL AND role='org_admin'`, orgID).Scan(&n); err != nil {
		return 0, fmt.Errorf("studiosvc: count org admins: %w", err)
	}
	return n, nil
}

// currentOrgRole 读取 userID 在 orgID 的 org-level 角色。无该行 → ("", false, nil)。
func (m *Members) currentOrgRole(ctx context.Context, orgID, userID string) (authzrole.Role, bool, error) {
	var r string
	err := m.pool.QueryRow(ctx,
		`SELECT role FROM auth_membership
		  WHERE org_id=$1 AND user_id=$2 AND scope_kind='org' AND scope_id IS NULL`, orgID, userID).Scan(&r)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("studiosvc: current org role: %w", err)
	}
	return authzrole.Role(r), true, nil
}

// userIDByEmail 查 auth_user 的 id；无对应行 → ErrUserNotFound。返回归一化后的 email
// 以便回填到 OrgMember（与建用户时落库的形态一致）。复制 Platform.userIDByEmail 的小查询，
// 不导出 authz 内部。
func (m *Members) userIDByEmail(ctx context.Context, email string) (string, string, error) {
	normalized := normalizePlatformEmail(email)
	var uid string
	err := m.pool.QueryRow(ctx, `SELECT id FROM auth_user WHERE email = $1`, normalized).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrUserNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("studiosvc: lookup user by email: %w", err)
	}
	return uid, normalized, nil
}
