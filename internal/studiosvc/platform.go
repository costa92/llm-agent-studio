package studiosvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authzstore "github.com/costa92/llm-agent-authz/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// platformOrgID 是平台级 membership 的占位 org_id：平台管理员不属于任何业务 org，
// 故用空串作哨兵。authz 的 ResolveRole/UpsertMembership 以 (org_id, user_id,
// scope_kind, scope_id) 唯一定位，对 org_id 不做语义解读——但 auth_membership.org_id
// 带 FK → auth_org(id)，所以必须先存在一行 id=” 的哨兵 org（见 EnsureSentinelOrg），
// 否则插入平台 membership 会触发外键冲突 (SQLSTATE 23503)。
const platformOrgID = ""

// platformScopeKind 是平台角色的 scope_kind，与业务的 "org" 区隔——平台权限是
// studio 自有概念，借 authz 的 membership 表承载，不污染 org 维度的 RBAC。
const platformScopeKind = "platform"

// sentinelOrgName 是哨兵 org（id=”）的名字，仅为满足 auth_membership 的 FK 而存在；
// ListAllOrgs 永远以 id <> ” 过滤掉它，绝不出现在“所有 org”列表里。
const sentinelOrgName = "__platform__"

// ErrUserNotFound 由 GrantByEmail 在邮箱无对应 auth_user 时返回 → handler 映射 404。
var ErrUserNotFound = errors.New("studiosvc: user not found")

// Platform 管理 studio 的“平台超级管理员”角色。平台管理员 = 一条 authz membership
// (org_id=”, scope_kind='platform', scope_id=nil, role=admin)。authz store 不暴露
// 跨表查询，故部分读取直接走共享 pool（镜像 OrgList 的做法）。
type Platform struct {
	authz *authzstore.Store
	pool  *pgxpool.Pool
}

// PlatformAdmin 是一名平台管理员的列表项。
type PlatformAdmin struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
}

// PlatformUser 是“全部用户”列表里的一行：身份 + 是否平台管理员 + 所属业务 org 数。
type PlatformUser struct {
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	CreatedAt any    `json:"createdAt"`
	IsAdmin   bool   `json:"isPlatformAdmin"`
	OrgCount  int64  `json:"orgCount"`
}

// UserOrgMembership 是用户详情里的一条 org 归属：角色 + 是否该 org 的唯一 org_admin
// （SoleOrgAdmin 用于前端在删除/降级前提示“这是该 org 仅存的管理员”）。
type UserOrgMembership struct {
	OrgID        string `json:"orgId"`
	OrgName      string `json:"orgName"`
	Role         string `json:"role"`
	SoleOrgAdmin bool   `json:"soleOrgAdmin"`
}

// UserDetail 是单个用户的详情：身份 + 是否平台管理员 + 业务 org 归属列表。
type UserDetail struct {
	UserID    string              `json:"userId"`
	Email     string              `json:"email"`
	CreatedAt any                 `json:"createdAt"`
	IsAdmin   bool                `json:"isPlatformAdmin"`
	Orgs      []UserOrgMembership `json:"orgs"`
}

// NewPlatform 构造 Platform 适配器。
func NewPlatform(az *authzstore.Store, pool *pgxpool.Pool) *Platform {
	return &Platform{authz: az, pool: pool}
}

// EnsureSentinelOrg 幂等地写入哨兵 org（id=”），满足平台 membership 的外键约束。
// 必须在 az.Migrate 之后、任何 Grant/SeedFromEmails 之前调用一次（见 main.go）。
func (p *Platform) EnsureSentinelOrg(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx,
		`INSERT INTO auth_org (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		platformOrgID, sentinelOrgName); err != nil {
		return fmt.Errorf("studiosvc: ensure sentinel platform org: %w", err)
	}
	return nil
}

// Grant 授予 userID 平台管理员（幂等：ON CONFLICT DO UPDATE）。
func (p *Platform) Grant(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("studiosvc: userID required")
	}
	if err := p.authz.UpsertMembership(ctx, platformOrgID, userID, platformScopeKind, nil, authzrole.RoleAdmin); err != nil {
		return fmt.Errorf("studiosvc: grant platform admin: %w", err)
	}
	return nil
}

// Revoke 删除该用户的平台 membership（无该行则 no-op）。
func (p *Platform) Revoke(ctx context.Context, userID string) error {
	if _, err := p.pool.Exec(ctx,
		`DELETE FROM auth_membership WHERE user_id = $1 AND org_id = $2 AND scope_kind = $3`,
		userID, platformOrgID, platformScopeKind); err != nil {
		return fmt.Errorf("studiosvc: revoke platform admin: %w", err)
	}
	return nil
}

// IsPlatformAdmin 报告 userID 是否为平台管理员。
func (p *Platform) IsPlatformAdmin(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	r, err := p.authz.ResolveRole(ctx, userID, platformOrgID, platformScopeKind, "")
	if err != nil {
		return false, fmt.Errorf("studiosvc: resolve platform role: %w", err)
	}
	return r.AtLeast(authzrole.RoleAdmin), nil
}

// ListAdmins 列出所有平台管理员（按 email 排序）。
func (p *Platform) ListAdmins(ctx context.Context) ([]PlatformAdmin, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT m.user_id, u.email
		   FROM auth_membership m
		   JOIN auth_user u ON u.id = m.user_id
		  WHERE m.scope_kind = $1 AND m.org_id = $2 AND u.deleted_at IS NULL
		  ORDER BY u.email ASC`, platformScopeKind, platformOrgID)
	if err != nil {
		return nil, fmt.Errorf("studiosvc: list platform admins: %w", err)
	}
	defer rows.Close()
	out := make([]PlatformAdmin, 0)
	for rows.Next() {
		var a PlatformAdmin
		if err := rows.Scan(&a.UserID, &a.Email); err != nil {
			return nil, fmt.Errorf("studiosvc: scan platform admin: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GrantByEmail 按邮箱查 auth_user 后授予平台管理员。无对应用户 → ErrUserNotFound。
func (p *Platform) GrantByEmail(ctx context.Context, email string) (string, error) {
	uid, err := p.userIDByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	if err := p.Grant(ctx, uid); err != nil {
		return "", err
	}
	return uid, nil
}

// SeedFromEmails 为每个 emails 中已存在的用户授予平台管理员（幂等；缺失用户跳过，
// 非错误——他们会在注册时被 top-up，见 Register）。
func (p *Platform) SeedFromEmails(ctx context.Context, emails []string) error {
	for _, e := range emails {
		if e == "" {
			continue
		}
		uid, err := p.userIDByEmail(ctx, e)
		if errors.Is(err, ErrUserNotFound) {
			continue // 用户尚未注册——跳过，注册时再 top-up
		}
		if err != nil {
			return err
		}
		if err := p.Grant(ctx, uid); err != nil {
			return err
		}
	}
	return nil
}

// ListAllOrgs 列出所有业务 org（含成员数），按创建时间倒序。哨兵 org（id=”）以
// id <> ” 过滤排除。返回 JSON 可序列化的 maps（镜像 OrgList.OrgsForUser 风格）。
func (p *Platform) ListAllOrgs(ctx context.Context) ([]map[string]any, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT o.id, o.name, o.created_at,
		        COUNT(m.user_id) FILTER (WHERE m.scope_kind = 'org') AS members
		   FROM auth_org o
		   LEFT JOIN auth_membership m ON m.org_id = o.id
		  WHERE o.id <> $1
		  GROUP BY o.id, o.name, o.created_at
		  ORDER BY o.created_at DESC`, platformOrgID)
	if err != nil {
		return nil, fmt.Errorf("studiosvc: list all orgs: %w", err)
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id, name  string
			createdAt any
			members   int64
		)
		if err := rows.Scan(&id, &name, &createdAt, &members); err != nil {
			return nil, fmt.Errorf("studiosvc: scan org: %w", err)
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "createdAt": createdAt, "memberCount": members,
		})
	}
	return out, rows.Err()
}

// ListUsers 列出所有用户（按 email 升序），含是否平台管理员与所属业务 org 数。
// 空时返回非 nil 空切片。
func (p *Platform) ListUsers(ctx context.Context) ([]PlatformUser, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT u.id, u.email, u.created_at,
		        EXISTS(SELECT 1 FROM auth_membership pa
		               WHERE pa.user_id=u.id AND pa.scope_kind='platform' AND pa.org_id='' AND pa.role='admin') AS is_admin,
		        (SELECT count(DISTINCT m.org_id) FROM auth_membership m
		           WHERE m.user_id=u.id AND m.scope_kind='org') AS org_count
		   FROM auth_user u WHERE u.deleted_at IS NULL ORDER BY u.email ASC`)
	if err != nil {
		return nil, fmt.Errorf("studiosvc: list users: %w", err)
	}
	defer rows.Close()
	out := make([]PlatformUser, 0)
	for rows.Next() {
		var u PlatformUser
		if err := rows.Scan(&u.UserID, &u.Email, &u.CreatedAt, &u.IsAdmin, &u.OrgCount); err != nil {
			return nil, fmt.Errorf("studiosvc: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserDetail 返回单个用户的详情：身份 + 是否平台管理员 + 业务 org 归属（含 SoleOrgAdmin）。
// 无对应 auth_user → ErrUserNotFound。Orgs 空时为非 nil 空切片。
func (p *Platform) UserDetail(ctx context.Context, userID string) (UserDetail, error) {
	var d UserDetail
	d.Orgs = make([]UserOrgMembership, 0)
	err := p.pool.QueryRow(ctx,
		`SELECT id, email, created_at FROM auth_user WHERE id=$1 AND deleted_at IS NULL`, userID).
		Scan(&d.UserID, &d.Email, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserDetail{}, ErrUserNotFound
	}
	if err != nil {
		return UserDetail{}, fmt.Errorf("studiosvc: lookup user: %w", err)
	}
	admin, err := p.IsPlatformAdmin(ctx, userID)
	if err != nil {
		return UserDetail{}, err
	}
	d.IsAdmin = admin

	rows, err := p.pool.Query(ctx,
		`SELECT o.id, o.name, m.role,
		        ((SELECT count(*) FROM auth_membership a
		            WHERE a.org_id=o.id AND a.scope_kind='org' AND a.scope_id IS NULL AND a.role='org_admin') = 1
		         AND m.role='org_admin') AS sole_org_admin
		   FROM auth_membership m JOIN auth_org o ON o.id=m.org_id
		  WHERE m.user_id=$1 AND m.scope_kind='org' AND m.scope_id IS NULL AND o.id<>''
		  ORDER BY o.name ASC`, userID)
	if err != nil {
		return UserDetail{}, fmt.Errorf("studiosvc: list user orgs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m UserOrgMembership
		if err := rows.Scan(&m.OrgID, &m.OrgName, &m.Role, &m.SoleOrgAdmin); err != nil {
			return UserDetail{}, fmt.Errorf("studiosvc: scan user org: %w", err)
		}
		d.Orgs = append(d.Orgs, m)
	}
	if err := rows.Err(); err != nil {
		return UserDetail{}, err
	}
	return d, nil
}

// DeleteUser 软删 auth_user（issue #23 方案 B）：经 authz 的 SoftDeleteUser stamp
// deleted_at + 撤销该用户全部 refresh session。软删后该用户无法再登录（authz
// GetUserByEmail 过滤 deleted_at）、在途 refresh cookie 失效；其行与 FK 子表
// （membership / session）保留，故审计可追溯、projects.created_by 等血缘仍可解析。
// 已签发的 access token 在 ≤AccessTTL 内仍有效（无状态 JWT 固有残留窗口）。
//
// 无对应行 / 已软删 → ErrUserNotFound。自删 / 最后一名平台管理员的守护在 handler 层。
// 选 B 而非硬删：删后唯一会悬空的是 projects.created_by（assets/generations 无 user
// 列、不受影响）；软删保留该指针的可解析性，且为合规审计留痕。
func (p *Platform) DeleteUser(ctx context.Context, userID string) error {
	if err := p.authz.SoftDeleteUser(ctx, userID); err != nil {
		if errors.Is(err, authzstore.ErrNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("studiosvc: soft-delete user: %w", err)
	}
	return nil
}

// userIDByEmail 查 auth_user 的 id；无对应行 → ErrUserNotFound。emails 在 config 已
// 规整为小写 trim，这里再 normalize 一次以容错直接传入的入参。
func (p *Platform) userIDByEmail(ctx context.Context, email string) (string, error) {
	var uid string
	err := p.pool.QueryRow(ctx,
		`SELECT id FROM auth_user WHERE email = $1 AND deleted_at IS NULL`, normalizePlatformEmail(email)).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("studiosvc: lookup user by email: %w", err)
	}
	return uid, nil
}

// normalizePlatformEmail 与 authz store 的 normalizeEmail 行为一致（小写 + trim），
// 保证按邮箱查 auth_user 时与建用户时落库的形态匹配。
func normalizePlatformEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }
