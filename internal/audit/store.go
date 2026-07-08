// Package audit owns org_audit_log: 安全敏感管理操作的 append-only 审计流水。写路径
// (Record) 只追加、绝不改删；detail 必须最小化且非敏感（记 target id/name，绝不含明文
// 密钥）。读路径 (List) 供未来审计 UI，按 (org_id, created_at DESC, id DESC) keyset 翻页。
// Record 是 best-effort：调用方在管理动作成功之后调用，写失败不得影响该动作（见 httpapi
// 的 audited 包装器：写失败仅记日志）。
package audit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ErrBadCursor 标记非法翻页游标 (handler 映射为 400)。
var ErrBadCursor = errors.New("audit: bad cursor")

// Entry 是一次待写入的审计条目。ActorEmail 可空（当前 ctx 只带 user id 时留空）。Detail
// 为最小化、非敏感的键值（如 {"name": ...}）；nil → 入库为 '{}'。绝不放明文密钥。
type Entry struct {
	OrgID       string
	ActorUserID string
	ActorEmail  string
	Action      string
	TargetType  string
	TargetID    string
	Detail      map[string]any
}

// Record 是 org_audit_log 行的公开读 DTO。
type Record struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"orgId"`
	ActorUserID string          `json:"actorUserId"`
	ActorEmail  string          `json:"actorEmail"`
	Action      string          `json:"action"`
	TargetType  string          `json:"targetType"`
	TargetID    string          `json:"targetId"`
	Detail      json.RawMessage `json:"detail"`
	CreatedAt   time.Time       `json:"createdAt"`
}

// Store persists org_audit_log (append-only).
type Store struct{ db *gorm.DB }

// New builds a Store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Record 追加一条审计行 (INSERT…RETURNING，纯 $N)。Action 必填；Detail 为 nil 时入库
// '{}'。返回 error 供调用方 best-effort 记日志——绝不能因此让底层管理动作失败。
func (s *Store) Record(ctx context.Context, e Entry) error {
	if strings.TrimSpace(e.Action) == "" {
		return fmt.Errorf("audit: action required")
	}
	var detail []byte = []byte("{}")
	if e.Detail != nil {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("audit: marshal detail: %w", err)
		}
		detail = b
	}
	const q = `
		INSERT INTO org_audit_log
			(id, org_id, actor_user_id, actor_email, action, target_type, target_id, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id`
	var id string
	row := s.db.WithContext(ctx).Raw(q,
		newID(), e.OrgID, e.ActorUserID, e.ActorEmail, e.Action, e.TargetType, e.TargetID, detail).Row()
	if err := row.Scan(&id); err != nil {
		return fmt.Errorf("audit: record: %w", err)
	}
	return nil
}

// ActorEmail 反查 userID 的邮箱 (auth_user，与 invites.emailByUserID 同源查询)，供
// audited 包装器在写审计行前回填 actor_email。查不到 (无此用户 / 已软删) → 返回 ""，nil
// error：审计写入是 best-effort，缺 email 不该拦下管理动作。
func (s *Store) ActorEmail(ctx context.Context, userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", nil
	}
	var email string
	err := s.db.WithContext(ctx).Raw(
		`SELECT email FROM auth_user WHERE id=$1 AND deleted_at IS NULL`, userID).Row().Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("audit: actor email: %w", err)
	}
	return email, nil
}

// auditCursor encodes a keyset position as "<created_at RFC3339Nano UTC>_<id>"
// (id 为 hex，RFC3339Nano 不含 '_')。复合 created_at+id 让同时间戳多行翻页稳定。
func auditCursor(r Record) string {
	return r.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + r.ID
}

func parseAuditCursor(cursor string) (time.Time, string, error) {
	ts, id, ok := strings.Cut(cursor, "_")
	if !ok || id == "" {
		return time.Time{}, "", ErrBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	return t, id, nil
}

// List 返回 org 的审计流水，最新在前，按 (created_at, id) DESC keyset 翻页。cursor=""
// 从最新一行起。返回 (items, nextCursor, error)；nextCursor 在末页为 ""（与
// cost.RecentByOrg / assets.Library 同 envelope 约定）。
func (s *Store) List(ctx context.Context, orgID string, limit int, cursor string) ([]Record, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const cols = `id, org_id, actor_user_id, actor_email, action, target_type, target_id, detail, created_at`
	q := `SELECT ` + cols + `
		FROM org_audit_log WHERE org_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`
	args := []any{orgID, limit}
	if cursor != "" {
		ct, cid, err := parseAuditCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		q = `SELECT ` + cols + `
		FROM org_audit_log WHERE org_id=$1 AND (created_at, id) < ($2, $3)
		ORDER BY created_at DESC, id DESC LIMIT $4`
		args = []any{orgID, ct, cid, limit}
	}
	rows, err := s.db.WithContext(ctx).Raw(q, args...).Rows()
	if err != nil {
		return nil, "", fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()
	out := make([]Record, 0)
	for rows.Next() {
		var r Record
		var detail []byte
		if err := rows.Scan(&r.ID, &r.OrgID, &r.ActorUserID, &r.ActorEmail,
			&r.Action, &r.TargetType, &r.TargetID, &detail, &r.CreatedAt); err != nil {
			return nil, "", err
		}
		r.Detail = json.RawMessage(detail)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		next = auditCursor(out[len(out)-1])
	}
	return out, next, nil
}
