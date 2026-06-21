// Package storageconfig owns storage_configs CRUD: per-org / global 对象存储后端
// 配置 (localfs/s3/oss/cos)。secret 半段 (S3 SecretAccessKey / OSS AccessKeySecret /
// COS SecretKey) 静态加密入库 (secret_enc BYTEA)，与 BYOK 同一把 secretbox。永不暴露
// secret：公开 DTO 只回 HasSecret 布尔；明文 secret 仅 ResolveForOrg 内部可见 (供
// StorageRouter 构造 BlobStore，绝不进 HTTP handler)。
package storageconfig

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"gorm.io/gorm"

	blobgithub "github.com/costa92/llm-agent-studio/internal/blob/github"
	"github.com/costa92/llm-agent-studio/internal/secretbox"
)

// ErrEncUnavailable 表示请求存储 secret，但加密 box 未启用 (未配置
// STUDIO_CONFIG_ENC_KEY)，无法静态加密，故拒绝 (不静默丢弃 secret)。
var ErrEncUnavailable = errors.New("storageconfig: secret storage requires STUDIO_CONFIG_ENC_KEY")

// ErrNotFound 表示按 org 定位的配置不存在。Delete 影响 0 行时返回它。
var ErrNotFound = errors.New("storageconfig: config not found")

// ErrInUse 表示配置被 asset 引用，不可删除。
var ErrInUse = errors.New("storageconfig: config in use by assets")

// validModes 是支持的存储后端。
var validModes = map[string]bool{"localfs": true, "s3": true, "oss": true, "cos": true, "github": true}

// StorageConfig 是 storage_configs 行返回给客户端的公开 DTO。永不暴露 secret：
// 只回 HasSecret 布尔 (解密后的 secret 仅 ResolveForOrg 内部可见)。
type StorageConfig struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"`
	OrgID        string `json:"orgId"`
	Mode         string `json:"mode"`
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	AccessKeyID  string `json:"accessKeyId"`
	PublicPrefix string `json:"publicPrefix"`
	UseSSL       bool   `json:"useSsl"`
	Enabled      bool   `json:"enabled"`
	HasSecret    bool   `json:"hasSecret"`
	Name         string `json:"name"`
	IsDefault    bool   `json:"isDefault"`
}

// ResolvedStorage 是运行层 (StorageRouter) 用的解析结果，带解密后的 SecretKey。
// 这是唯一暴露明文 secret 的路径，仅服务端内部调用 (绝不进 HTTP handler)。
type ResolvedStorage struct {
	Mode         string
	Endpoint     string
	Region       string
	Bucket       string
	AccessKeyID  string
	SecretKey    string
	PublicPrefix string
	UseSSL       bool
}

// UpsertInput 是 Create/Update 的入参。Secret 走 keep-or-replace 语义：
// 空 → 保留既有 secret_enc 不动；非空 → 重新加密替换 (box 未启用时返回 ErrEncUnavailable)。
type UpsertInput struct {
	Mode         string
	Endpoint     string
	Region       string
	Bucket       string
	AccessKeyID  string
	PublicPrefix string
	UseSSL       bool
	Enabled      bool
	Secret       string // write-only：空=保留既有 secret_enc；非空=重新加密替换
	Name         string
}

// Store persists storage_configs.
type Store struct {
	db  *gorm.DB
	box *secretbox.Box
}

// New builds a Store. box 提供 secret 的静态加解密；nil/disabled box 表示无法存储
// secret (带非空 Secret 的 Upsert 返回 ErrEncUnavailable)。
func New(db *gorm.DB, box *secretbox.Box) *Store { return &Store{db: db, box: box} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// validate 校验 mode + 必填字段。校验先于任何 DB 访问。
func validate(in UpsertInput) error {
	if !validModes[in.Mode] {
		return fmt.Errorf("storageconfig: invalid mode %q (want localfs|s3|oss|cos|github)", in.Mode)
	}
	switch in.Mode {
	case "s3", "cos", "oss":
		// 远端对象存储至少需要 bucket + endpoint 才能 round-trip。
		// (COS endpoint 可后续在 router 由 region 派生，但 store 层先要求齐全。)
		if in.Bucket == "" || in.Endpoint == "" {
			return fmt.Errorf("storageconfig: mode %q requires bucket and endpoint", in.Mode)
		}
	case "github":
		// 列复用：Bucket=repo, AccessKeyID=owner。store 层只校验这两项；token
		// (Secret) 必填性留给 adapter New (keep-blank 编辑语义在 store 层成立)。
		if in.Bucket == "" || in.AccessKeyID == "" {
			return fmt.Errorf("storageconfig: mode %q requires repo (bucket) and owner (accessKeyId)", in.Mode)
		}
		// Endpoint 在 github 模式下被映射为 GitHub API 根（adapter New 的 APIBase）。
		// 真实生产事故：把 jsDelivr CDN / raw.githubusercontent 误填进 Endpoint，blob
		// Put 前 getSHA 直接 EOF——挡在 save 之前比静默 fallback 到 localfs 默认好。
		if in.Endpoint != "" {
			if err := blobgithub.ValidateAPIBase(in.Endpoint); err != nil {
				return fmt.Errorf("storageconfig: %w", err)
			}
		}
	case "localfs":
		// 本地盘无需 bucket/endpoint。
	}
	return nil
}

// encryptSecret 返回 keep-or-replace 用的 (replace, enc, err)。Secret 非空但 box
// 未启用 → ErrEncUnavailable。
func (s *Store) encryptSecret(secret string) (replace bool, enc []byte, err error) {
	if secret == "" {
		return false, nil, nil
	}
	if !s.box.Enabled() {
		return false, nil, ErrEncUnavailable
	}
	ct, err := s.box.Encrypt([]byte(secret))
	if err != nil {
		return false, nil, fmt.Errorf("storageconfig: encrypt secret: %w", err)
	}
	return true, ct, nil
}

// UpsertGlobal 写 (scope='global') 单例配置。ON CONFLICT 命中 global partial unique
// index 时 DO UPDATE (keep-or-replace secret)。
func (s *Store) UpsertGlobal(ctx context.Context, in UpsertInput) (StorageConfig, error) {
	if err := validate(in); err != nil {
		return StorageConfig{}, err
	}
	replace, enc, err := s.encryptSecret(in.Secret)
	if err != nil {
		return StorageConfig{}, err
	}
	const q = `
		INSERT INTO storage_configs
			(id, scope, org_id, mode, endpoint, region, bucket, access_key_id, secret_enc, use_ssl, public_prefix, enabled, name)
		VALUES ($1, 'global', '', $2, $3, $4, $5, $6, $7, $8, $9, $10, $12)
		ON CONFLICT (scope) WHERE scope='global' DO UPDATE SET
			mode=EXCLUDED.mode, endpoint=EXCLUDED.endpoint, region=EXCLUDED.region,
			bucket=EXCLUDED.bucket, access_key_id=EXCLUDED.access_key_id,
			secret_enc=CASE WHEN $11 THEN EXCLUDED.secret_enc ELSE storage_configs.secret_enc END,
			use_ssl=EXCLUDED.use_ssl, public_prefix=EXCLUDED.public_prefix,
			enabled=EXCLUDED.enabled, updated_at=now()
		RETURNING id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL) AS has_secret, use_ssl, public_prefix, enabled, name, is_default`
	row := s.db.WithContext(ctx).Raw(q,
		newID(), in.Mode, in.Endpoint, in.Region, in.Bucket, in.AccessKeyID, enc, in.UseSSL, in.PublicPrefix, in.Enabled, replace, in.Name).Row()
	return scanConfig(row)
}

// scanConfig 把 RETURNING/SELECT 行扫进公开 DTO (列序固定)。
func scanConfig(row interface{ Scan(...any) error }) (StorageConfig, error) {
	var sc StorageConfig
	if err := row.Scan(&sc.ID, &sc.Scope, &sc.OrgID, &sc.Mode, &sc.Endpoint, &sc.Region,
		&sc.Bucket, &sc.AccessKeyID, &sc.HasSecret, &sc.UseSSL, &sc.PublicPrefix, &sc.Enabled,
		&sc.Name, &sc.IsDefault); err != nil {
		return StorageConfig{}, fmt.Errorf("storageconfig: scan: %w", err)
	}
	return sc, nil
}

// GetGlobal 读 global 配置。无行 → ok=false。
func (s *Store) GetGlobal(ctx context.Context) (StorageConfig, bool, error) {
	row := s.db.WithContext(ctx).Raw(
		`SELECT id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL) AS has_secret, use_ssl, public_prefix, enabled, name, is_default
		 FROM storage_configs WHERE scope='global' LIMIT 1`).Row()
	sc, err := scanConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StorageConfig{}, false, nil
		}
		return StorageConfig{}, false, err
	}
	return sc, true, nil
}

// List 返回 org 的所有 org-scope 配置，默认在前。
func (s *Store) List(ctx context.Context, orgID string) ([]StorageConfig, error) {
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL), use_ssl, public_prefix, enabled, name, is_default
		 FROM storage_configs WHERE scope='org' AND org_id=$1
		 ORDER BY is_default DESC, created_at ASC`, orgID).Rows()
	if err != nil {
		return nil, fmt.Errorf("storageconfig: list: %w", err)
	}
	defer rows.Close()
	out := []StorageConfig{}
	for rows.Next() {
		sc, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Create 插入一条新的 org 配置(纯 INSERT，无 ON CONFLICT —— org×mode 唯一约束已移除)。
// 若该 org 当前无 enabled 默认，本条自动设为默认。
func (s *Store) Create(ctx context.Context, orgID string, in UpsertInput) (StorageConfig, error) {
	if orgID == "" {
		return StorageConfig{}, fmt.Errorf("storageconfig: orgID required")
	}
	if err := validate(in); err != nil {
		return StorageConfig{}, err
	}
	_, enc, err := s.encryptSecret(in.Secret)
	if err != nil {
		return StorageConfig{}, err
	}
	var hasDefault bool
	if err := s.db.WithContext(ctx).Raw(
		`SELECT EXISTS(SELECT 1 FROM storage_configs WHERE scope='org' AND org_id=$1 AND enabled=true AND is_default=true)`,
		orgID).Row().Scan(&hasDefault); err != nil {
		return StorageConfig{}, fmt.Errorf("storageconfig: check default: %w", err)
	}
	isDefault := in.Enabled && !hasDefault
	const q = `
		INSERT INTO storage_configs
			(id, scope, org_id, mode, endpoint, region, bucket, access_key_id, secret_enc, use_ssl, public_prefix, enabled, name, is_default)
		VALUES ($1,'org',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL), use_ssl, public_prefix, enabled, name, is_default`
	row := s.db.WithContext(ctx).Raw(q,
		newID(), orgID, in.Mode, in.Endpoint, in.Region, in.Bucket, in.AccessKeyID, enc,
		in.UseSSL, in.PublicPrefix, in.Enabled, in.Name, isDefault).Row()
	return scanConfig(row)
}

// Update 按 id 更新一条 org 配置(secret 空=保留)。停用时一并清 is_default(避免「停用却默认」)。
func (s *Store) Update(ctx context.Context, orgID, id string, in UpsertInput) (StorageConfig, error) {
	if orgID == "" || id == "" {
		return StorageConfig{}, fmt.Errorf("storageconfig: orgID+id required")
	}
	if err := validate(in); err != nil {
		return StorageConfig{}, err
	}
	replace, enc, err := s.encryptSecret(in.Secret)
	if err != nil {
		return StorageConfig{}, err
	}
	const q = `
		UPDATE storage_configs SET
			mode=$3, endpoint=$4, region=$5, bucket=$6, access_key_id=$7,
			secret_enc=CASE WHEN $8 THEN $9 ELSE secret_enc END,
			use_ssl=$10, public_prefix=$11, enabled=$12, name=$13,
			is_default=CASE WHEN $12 THEN is_default ELSE false END,
			updated_at=now()
		WHERE id=$1 AND org_id=$2 AND scope='org'
		RETURNING id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL), use_ssl, public_prefix, enabled, name, is_default`
	row := s.db.WithContext(ctx).Raw(q,
		id, orgID, in.Mode, in.Endpoint, in.Region, in.Bucket, in.AccessKeyID,
		replace, enc, in.UseSSL, in.PublicPrefix, in.Enabled, in.Name).Row()
	sc, err := scanConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StorageConfig{}, ErrNotFound
	}
	return sc, err
}

// SetDefault 事务：先清零该 org 全部 is_default，再置一(顺序不可反，否则部分唯一索引冲突)。
// 目标必须 enabled。
func (s *Store) SetDefault(ctx context.Context, orgID, id string) error {
	if orgID == "" || id == "" {
		return fmt.Errorf("storageconfig: orgID+id required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var enabled bool
		if err := tx.Raw(
			`SELECT enabled FROM storage_configs WHERE id=$1 AND org_id=$2 AND scope='org'`, id, orgID).
			Row().Scan(&enabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if !enabled {
			return fmt.Errorf("storageconfig: cannot set a disabled config as default")
		}
		if res := tx.Exec(
			`UPDATE storage_configs SET is_default=false WHERE org_id=$1 AND scope='org'`, orgID); res.Error != nil {
			return res.Error
		}
		if res := tx.Exec(
			`UPDATE storage_configs SET is_default=true WHERE id=$1 AND org_id=$2 AND scope='org'`, id, orgID); res.Error != nil {
			return res.Error
		}
		return nil
	})
}

// DefaultConfigID 返回 org 默认 enabled 配置 id。
func (s *Store) DefaultConfigID(ctx context.Context, orgID string) (string, bool, error) {
	var id string
	err := s.db.WithContext(ctx).Raw(
		`SELECT id FROM storage_configs WHERE scope='org' AND org_id=$1 AND enabled=true AND is_default=true LIMIT 1`,
		orgID).Row().Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// Delete 按 id 删除一条 org 配置。守卫：被 asset 引用 → 拒(返回 ErrInUse)。
// 成功后清空指向它的 project 覆盖。ref-count 与 DELETE 在同一事务内执行，避免 TOCTOU。
func (s *Store) Delete(ctx context.Context, orgID, id string) error {
	if orgID == "" || id == "" {
		return fmt.Errorf("storageconfig: orgID+id required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refs int
		if err := tx.Raw(
			`SELECT count(*) FROM assets WHERE storage_config_id=$1`, id).Row().Scan(&refs); err != nil {
			return fmt.Errorf("storageconfig: ref check: %w", err)
		}
		if refs > 0 {
			return ErrInUse
		}
		res := tx.Exec(
			`DELETE FROM storage_configs WHERE id=$1 AND org_id=$2 AND scope='org'`, id, orgID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if res := tx.Exec(
			`UPDATE projects SET storage_config_id='' WHERE storage_config_id=$1`, id); res.Error != nil {
			return res.Error
		}
		return nil
	})
}

// ResolveForOrgAndMode 解析某 org 在指定 mode 下生效的存储配置 (含解密后的 SecretKey)，供 StorageRouter
// 使用。如果指定了 mode，则按 mode 查找：优先该 org 启用的对应 mode 配置，其次全局启用的对应 mode 配置。
// 如果 mode 为空，则回退到该 org 的任意启用配置（即原本默认配置）。都无 → ok=false。
func (s *Store) ResolveForOrgAndMode(ctx context.Context, orgID string, mode string) (ResolvedStorage, bool, error) {
	if mode != "" {
		if orgID != "" {
			rs, ok, err := s.resolveOne(ctx, `WHERE scope='org' AND org_id=$1 AND mode=$2 AND enabled=true`, orgID, mode)
			if err != nil {
				return ResolvedStorage{}, false, err
			}
			if ok {
				return rs, true, nil
			}
		}
		// 回落 global 对应的 mode.
		return s.resolveOne(ctx, `WHERE scope='global' AND mode=$1 AND enabled=true`, mode)
	}

	// mode == ""，保持 ResolveForOrg 逻辑
	if orgID != "" {
		rs, ok, err := s.resolveOne(ctx, `WHERE scope='org' AND org_id=$1 AND enabled=true`, orgID)
		if err != nil {
			return ResolvedStorage{}, false, err
		}
		if ok {
			return rs, true, nil
		}
	}
	return s.resolveOne(ctx, `WHERE scope='global' AND enabled=true`)
}

// ResolveForOrg 解析某 org 生效的存储配置 (含解密后的 SecretKey)，供 StorageRouter
// 使用。优先 per-org enabled 行；否则 global enabled 行；都无 → ok=false。这是唯一
// 暴露明文 secret 的路径，仅服务端内部调用 (绝不进 HTTP handler)。
func (s *Store) ResolveForOrg(ctx context.Context, orgID string) (ResolvedStorage, bool, error) {
	return s.ResolveForOrgAndMode(ctx, orgID, "")
}

// orgOwnedOrGlobal 限定 by-id 解析只命中「本 org 的 org-scope 行」或「global 共享行」，
// 阻止跨租户解析他 org 的 org-scope 配置 (defense-in-depth)：即便某 asset/项目覆盖
// 因 bug 或人为篡改持久化了他 org 的 config id，解析也 ok=false → 回落 default，绝不
// 用他 org 的凭证。asset 可合法持久化 global id (per-(org,mode) 回落)，故须放行 global。
const orgOwnedOrGlobal = ` AND (org_id=$2 OR scope='global')`

// ResolveByID 按 storage_configs.id 直接解析「生效(enabled) 且属本 org 或 global」配置
// (含解密后的 SecretKey)。未知/已禁用/他 org → ok=false (调用方回落)。用于「写目标」
// 解析 (ResolveWriteTarget 的项目覆盖 / org 默认重解析)：禁用的配置不得再作为新写入
// 落点。这是 ResolveForOrgAndMode 的 by-id 同伴，行→ResolvedStorage 映射 (含 secret
// 解密) 完全一致 (复用 resolveOne)。注意：serve 路径用 ResolveByIDForServe (不过滤 enabled)。
func (s *Store) ResolveByID(ctx context.Context, orgID, id string) (ResolvedStorage, bool, error) {
	return s.resolveOne(ctx, `WHERE id=$1 AND enabled=true`+orgOwnedOrGlobal, id, orgID)
}

// ResolveByIDForServe 与 ResolveByID 同，但「不」要求 enabled=true：serve 路径按
// asset 写入时持久化的后端身份 (config id) 把字节读回 EXACTLY 那个后端，独立于该
// 配置当前是否启用。语义：禁用一个存储配置只应阻止「新写入」(写路径仍按 enabled
// 过滤) 与「被选为覆盖/默认」，已落在该后端的历史 asset 必须继续可读 (读非破坏性)——
// 否则禁用会静默孤立既有资产 (回落 builtin default → 找不到字节 → 404)。删除才是
// 硬下线 (Delete 有 in-use 守卫)。仍按 org 限定 (本 org 或 global)：跨租户 id → ok=false。
func (s *Store) ResolveByIDForServe(ctx context.Context, orgID, id string) (ResolvedStorage, bool, error) {
	return s.resolveOne(ctx, `WHERE id=$1`+orgOwnedOrGlobal, id, orgID)
}

// ConfigIDForOrgAndMode 返回写路径要持久化的 token：某 (org,mode) 解析到的
// storage_configs.id，精度匹配 ResolveForOrgAndMode 的 per-org → global 回落顺序。
// 无匹配 config 行 (即将落 builtin 内置默认) → ok=false，由调用方落 "builtin" sentinel。
// 只看 enabled=true 行 (与 resolve 一致)。
func (s *Store) ConfigIDForOrgAndMode(ctx context.Context, orgID, mode string) (string, bool, error) {
	if mode == "" {
		// 与 ResolveForOrgAndMode 的 mode=="" 分支一致：org 任意启用 → 否则 global 任意启用。
		if orgID != "" {
			if id, ok, err := s.configIDOne(ctx, `WHERE scope='org' AND org_id=$1 AND enabled=true`, orgID); err != nil || ok {
				return id, ok, err
			}
		}
		return s.configIDOne(ctx, `WHERE scope='global' AND enabled=true`)
	}
	if orgID != "" {
		if id, ok, err := s.configIDOne(ctx, `WHERE scope='org' AND org_id=$1 AND mode=$2 AND enabled=true`, orgID, mode); err != nil || ok {
			return id, ok, err
		}
	}
	return s.configIDOne(ctx, `WHERE scope='global' AND mode=$1 AND enabled=true`, mode)
}

// configIDOne 跑一条 WHERE 子句的 SELECT id。无行 → ok=false。
// ORDER BY created_at DESC, id 保证确定性：org 有多条启用行时每次返回同一行，与
// resolveOne 的顺序完全一致，写路径持久化的 config id 与读路径解析到的后端绑定。
func (s *Store) configIDOne(ctx context.Context, where string, args ...any) (string, bool, error) {
	q := `SELECT id FROM storage_configs ` + where + ` ORDER BY created_at DESC, id LIMIT 1`
	var id string
	if err := s.db.WithContext(ctx).Raw(q, args...).Row().Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("storageconfig: config id: %w", err)
	}
	return id, true, nil
}

// resolveOne 跑一条 WHERE 子句的 SELECT 并解密 secret。无行 → ok=false。
// ORDER BY created_at DESC, id 保证确定性：org 有多条启用行时每次返回同一行，与
// configIDOne 的顺序完全一致，两者必须绑定到相同行——任何分歧都会导致 cover bytes
// 落在后端 X 但 asset 记录持久化的是后端 Y 的 id，资产不可读。
func (s *Store) resolveOne(ctx context.Context, where string, args ...any) (ResolvedStorage, bool, error) {
	q := `SELECT mode, endpoint, region, bucket, access_key_id, secret_enc, use_ssl, public_prefix
		 FROM storage_configs ` + where + ` ORDER BY created_at DESC, id LIMIT 1`
	row := s.db.WithContext(ctx).Raw(q, args...).Row()
	var rs ResolvedStorage
	var enc []byte
	if err := row.Scan(&rs.Mode, &rs.Endpoint, &rs.Region, &rs.Bucket, &rs.AccessKeyID, &enc, &rs.UseSSL, &rs.PublicPrefix); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResolvedStorage{}, false, nil
		}
		return ResolvedStorage{}, false, fmt.Errorf("storageconfig: resolve: %w", err)
	}
	if len(enc) > 0 {
		if !s.box.Enabled() {
			return ResolvedStorage{}, false, ErrEncUnavailable
		}
		pt, err := s.box.Decrypt(enc)
		if err != nil {
			return ResolvedStorage{}, false, fmt.Errorf("storageconfig: decrypt secret: %w", err)
		}
		rs.SecretKey = string(pt)
	}
	return rs, true, nil
}
