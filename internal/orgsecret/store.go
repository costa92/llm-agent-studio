// Package orgsecret owns org_secrets CRUD: 组织级命名密钥注册表。value 走 AES-256-GCM
// 静态加密入库 (value_enc BYTEA)，与 BYOK/storageconfig 同一把 secretbox。永不暴露
// 明文：公开 DTO 只回 {name, hasValue}；明文仅 Resolve 内部可见 (供 worker 注入 http
// 请求 header，绝不进 HTTP handler)。组织隔离贯穿全部读写 (WHERE org_id=$N)。无
// delete-in-use 守卫 ({{secret:NAME}} 自由文本引用无结构化 FK)。需独立安全评审。
package orgsecret

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
)

// ErrEncUnavailable 表示请求存储 secret，但加密 box 未启用 (未配置 STUDIO_CONFIG_ENC_KEY)，
// 无法静态加密，故拒绝 (不静默丢弃/存明文)。
var ErrEncUnavailable = errors.New("orgsecret: secret storage requires STUDIO_CONFIG_ENC_KEY")

// ErrNotFound 表示按 org 定位的密钥不存在 (含跨租户访问被拒)。
var ErrNotFound = errors.New("orgsecret: secret not found")

// OrgSecret 是 org_secrets 行的公开 DTO。永不暴露 value：只回 name + hasValue。
type OrgSecret struct {
	ID       string `json:"id"`
	OrgID    string `json:"orgId"`
	Name     string `json:"name"`
	HasValue bool   `json:"hasValue"`
}

// UpsertInput 是 Create/Update 入参。Value 走 keep-or-replace：空=保留既有 value_enc；
// 非空=重新加密替换 (box 未启用 → ErrEncUnavailable)。
type UpsertInput struct {
	Name  string
	Value string // write-only：空=保留既有；非空=重新加密替换
}

// Store persists org_secrets.
type Store struct {
	db  *gorm.DB
	box *secretbox.Box
}

// New builds a Store. box 提供 value 的静态加解密；nil/disabled box → 带非空 Value 的
// Upsert 返回 ErrEncUnavailable，Resolve 返回 ErrEncUnavailable。
func New(db *gorm.DB, box *secretbox.Box) *Store { return &Store{db: db, box: box} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("orgsecret: name required")
	}
	return nil
}

// encryptValue 返回 keep-or-replace 用的 (replace, enc, err)。Value 非空但 box 未启用
// → ErrEncUnavailable。
func (s *Store) encryptValue(value string) (replace bool, enc []byte, err error) {
	if value == "" {
		return false, nil, nil
	}
	if !s.box.Enabled() {
		return false, nil, ErrEncUnavailable
	}
	ct, err := s.box.Encrypt([]byte(value))
	if err != nil {
		return false, nil, fmt.Errorf("orgsecret: encrypt: %w", err)
	}
	return true, ct, nil
}

func scanSecret(row interface{ Scan(...any) error }) (OrgSecret, error) {
	var sec OrgSecret
	if err := row.Scan(&sec.ID, &sec.OrgID, &sec.Name, &sec.HasValue); err != nil {
		return OrgSecret{}, err
	}
	return sec, nil
}

// List 返回 org 的全部命名密钥 (名序)。只回 {name, hasValue}，绝不带 value_enc。
func (s *Store) List(ctx context.Context, orgID string) ([]OrgSecret, error) {
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT id, org_id, name, (value_enc IS NOT NULL) AS has_value
		 FROM org_secrets WHERE org_id=$1 ORDER BY name ASC`, orgID).Rows()
	if err != nil {
		return nil, fmt.Errorf("orgsecret: list: %w", err)
	}
	defer rows.Close()
	out := []OrgSecret{}
	for rows.Next() {
		sec, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// Create 插入一条新命名密钥 (INSERT…RETURNING，纯 $N)。value_enc 必填 (非空 Value)。
func (s *Store) Create(ctx context.Context, orgID string, in UpsertInput) (OrgSecret, error) {
	if orgID == "" {
		return OrgSecret{}, fmt.Errorf("orgsecret: orgID required")
	}
	if err := validateName(in.Name); err != nil {
		return OrgSecret{}, err
	}
	if in.Value == "" {
		return OrgSecret{}, fmt.Errorf("orgsecret: value required on create")
	}
	_, enc, err := s.encryptValue(in.Value)
	if err != nil {
		return OrgSecret{}, err
	}
	const q = `
		INSERT INTO org_secrets (id, org_id, name, value_enc)
		VALUES ($1,$2,$3,$4)
		RETURNING id, org_id, name, (value_enc IS NOT NULL)`
	row := s.db.WithContext(ctx).Raw(q, newID(), orgID, in.Name, enc).Row()
	sec, err := scanSecret(row)
	if err != nil {
		return OrgSecret{}, fmt.Errorf("orgsecret: create: %w", err)
	}
	return sec, nil
}

// Update 按 (org, name) keep-or-replace value (空=保留既有 value_enc)。跨租户/不存在
// → ErrNotFound。
func (s *Store) Update(ctx context.Context, orgID, name string, in UpsertInput) (OrgSecret, error) {
	if orgID == "" || name == "" {
		return OrgSecret{}, fmt.Errorf("orgsecret: orgID+name required")
	}
	replace, enc, err := s.encryptValue(in.Value)
	if err != nil {
		return OrgSecret{}, err
	}
	const q = `
		UPDATE org_secrets SET
			value_enc=CASE WHEN $3 THEN $4 ELSE value_enc END,
			updated_at=now()
		WHERE org_id=$1 AND name=$2
		RETURNING id, org_id, name, (value_enc IS NOT NULL)`
	row := s.db.WithContext(ctx).Raw(q, orgID, name, replace, enc).Row()
	sec, err := scanSecret(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OrgSecret{}, ErrNotFound
	}
	if err != nil {
		return OrgSecret{}, fmt.Errorf("orgsecret: update: %w", err)
	}
	return sec, nil
}

// Delete 按 (org, name) 删除。无 in-use 守卫 (spec 非目标)。跨租户/不存在 → ErrNotFound。
func (s *Store) Delete(ctx context.Context, orgID, name string) error {
	if orgID == "" || name == "" {
		return fmt.Errorf("orgsecret: orgID+name required")
	}
	res := s.db.WithContext(ctx).Exec(`DELETE FROM org_secrets WHERE org_id=$1 AND name=$2`, orgID, name)
	if res.Error != nil {
		return fmt.Errorf("orgsecret: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Resolve 是唯一暴露明文 value 的路径，仅 worker 内部调用 (绝不进 HTTP handler)。按
// (org, name) 读 value_enc 并解密。不存在/跨租户 → ErrNotFound (绝不在错误里带 name 以外
// 的信息)；box 未启用 → ErrEncUnavailable。
func (s *Store) Resolve(ctx context.Context, orgID, name string) (string, error) {
	if orgID == "" || name == "" {
		return "", ErrNotFound
	}
	var enc []byte
	err := s.db.WithContext(ctx).Raw(
		`SELECT value_enc FROM org_secrets WHERE org_id=$1 AND name=$2`, orgID, name).Row().Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("orgsecret: resolve: %w", err)
	}
	if len(enc) == 0 {
		return "", ErrNotFound
	}
	if !s.box.Enabled() {
		return "", ErrEncUnavailable
	}
	pt, err := s.box.Decrypt(enc)
	if err != nil {
		return "", fmt.Errorf("orgsecret: decrypt: %w", err)
	}
	return string(pt), nil
}
