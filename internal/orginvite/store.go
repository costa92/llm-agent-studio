// Package orginvite owns org_invites 持久化：邀请协作者入组织的待接受邀请注册表。
// 每行一封邀请（org_id + 目标 email + 拟授 role + 唯一 token + 状态 + 邀请人 + 过期时间）。
// 组织隔离贯穿全部按 org 的读写（WHERE org_id=$N）。token 仅供构造邀请链接与接受时定位，
// 由 org-admin 网关保护；本包只做纯持久化，成员授予/邮箱校验等编排逻辑在 studiosvc.Invites。
package orginvite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 邀请状态枚举（存 org_invites.status）。
const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusRevoked  = "revoked"
)

// ErrNotFound 表示按 (org,id) 或 token 定位的邀请不存在（含跨租户访问被拒）。
var ErrNotFound = errors.New("orginvite: invite not found")

// Invite 是 org_invites 行的 DTO。token 随行返回：调用方（org-admin 网关内）据此
// 拼邀请链接分享给被邀请人。AcceptedAt 仅 accepted 行非空。
type Invite struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"orgId"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	Token      string     `json:"token"`
	Status     string     `json:"status"`
	InvitedBy  string     `json:"invitedBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	ExpiresAt  time.Time  `json:"expiresAt"`
}

// Store persists org_invites.
type Store struct {
	db *gorm.DB
}

// New builds a Store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

// newToken 返回 32 字节（256 bit）十六进制随机 token，作邀请链接的不可猜凭据。
func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// selectCols 是 Invite 的固定列序（RETURNING / SELECT 复用，避免列序漂移）。
const selectCols = `id, org_id, email, role, token, status, invited_by, created_at, accepted_at, expires_at`

func scanInvite(row interface{ Scan(...any) error }) (Invite, error) {
	var inv Invite
	var acceptedAt sql.NullTime
	if err := row.Scan(
		&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.Token,
		&inv.Status, &inv.InvitedBy, &inv.CreatedAt, &acceptedAt, &inv.ExpiresAt,
	); err != nil {
		return Invite{}, err
	}
	if acceptedAt.Valid {
		t := acceptedAt.Time
		inv.AcceptedAt = &t
	}
	return inv, nil
}

// Create 插入一条 pending 邀请（INSERT…RETURNING，纯 $N），生成随机 token 与过期时间
// （now + ttl）。email 由调用方归一化。并发下同一 (org,email) 的第二封 pending 会撞
// org_invites_pending_uniq → 返回错误（调用方应先 RevokePendingByEmail 再 Create）。
func (s *Store) Create(ctx context.Context, orgID, email, role, invitedBy string, ttl time.Duration) (Invite, error) {
	if orgID == "" || email == "" || role == "" {
		return Invite{}, fmt.Errorf("orginvite: orgID, email, role required")
	}
	expiresAt := time.Now().Add(ttl)
	const q = `
		INSERT INTO org_invites (id, org_id, email, role, token, status, invited_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$7)
		RETURNING ` + selectCols
	row := s.db.WithContext(ctx).Raw(q, newID(), orgID, email, role, newToken(), invitedBy, expiresAt).Row()
	inv, err := scanInvite(row)
	if err != nil {
		return Invite{}, fmt.Errorf("orginvite: create: %w", err)
	}
	return inv, nil
}

// GetByToken 按 token 读一封邀请（任意状态）。无对应行 → ErrNotFound。
func (s *Store) GetByToken(ctx context.Context, token string) (Invite, error) {
	if strings.TrimSpace(token) == "" {
		return Invite{}, ErrNotFound
	}
	row := s.db.WithContext(ctx).Raw(
		`SELECT `+selectCols+` FROM org_invites WHERE token=$1`, token).Row()
	inv, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Invite{}, ErrNotFound
	}
	if err != nil {
		return Invite{}, fmt.Errorf("orginvite: get by token: %w", err)
	}
	return inv, nil
}

// ListByOrg 列出 orgID 的全部 pending 邀请（按 created_at 倒序）。空时返回非 nil 空切片。
func (s *Store) ListByOrg(ctx context.Context, orgID string) ([]Invite, error) {
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT `+selectCols+` FROM org_invites
		  WHERE org_id=$1 AND status='pending'
		  ORDER BY created_at DESC, id DESC`, orgID).Rows()
	if err != nil {
		return nil, fmt.Errorf("orginvite: list: %w", err)
	}
	defer rows.Close()
	out := make([]Invite, 0)
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// Revoke 将 (org,id) 的 pending 邀请置为 revoked。跨租户/不存在/非 pending → ErrNotFound。
func (s *Store) Revoke(ctx context.Context, orgID, id string) error {
	if orgID == "" || id == "" {
		return ErrNotFound
	}
	res := s.db.WithContext(ctx).Exec(
		`UPDATE org_invites SET status='revoked' WHERE org_id=$1 AND id=$2 AND status='pending'`,
		orgID, id)
	if res.Error != nil {
		return fmt.Errorf("orginvite: revoke: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokePendingByEmail 撤掉 (org,email) 现存的全部 pending 邀请（供重复邀请时先清旧再发新，
// 让新链接生效、旧链接失效）。无 pending 行时零副作用。
func (s *Store) RevokePendingByEmail(ctx context.Context, orgID, email string) error {
	if orgID == "" || email == "" {
		return nil
	}
	res := s.db.WithContext(ctx).Exec(
		`UPDATE org_invites SET status='revoked' WHERE org_id=$1 AND email=$2 AND status='pending'`,
		orgID, email)
	if res.Error != nil {
		return fmt.Errorf("orginvite: revoke pending by email: %w", res.Error)
	}
	return nil
}

// MarkAccepted 将邀请置为 accepted 并记录 accepted_at（仅对 pending 行生效）。
// 已非 pending（并发接受/已撤销）→ ErrNotFound。
func (s *Store) MarkAccepted(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	res := s.db.WithContext(ctx).Exec(
		`UPDATE org_invites SET status='accepted', accepted_at=now() WHERE id=$1 AND status='pending'`,
		id)
	if res.Error != nil {
		return fmt.Errorf("orginvite: mark accepted: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
