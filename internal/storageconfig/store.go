// Package storageconfig owns storage_configs CRUD: per-org / global 对象存储后端
// 配置 (localfs/s3/oss/cos)。secret 半段 (S3 SecretAccessKey / OSS AccessKeySecret /
// COS SecretKey) 静态加密入库 (secret_enc BYTEA)，与 BYOK 同一把 secretbox。永不暴露
// secret：公开 DTO 只回 HasSecret 布尔；明文 secret 仅 ResolveForOrg 内部可见 (供
// StorageRouter 构造 BlobStore，绝不进 HTTP handler)。
package storageconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	blobgithub "github.com/costa92/llm-agent-studio/internal/blob/github"
	"github.com/costa92/llm-agent-studio/internal/secretbox"
)

// ErrEncUnavailable 表示请求存储 secret，但加密 box 未启用 (未配置
// STUDIO_CONFIG_ENC_KEY)，无法静态加密，故拒绝 (不静默丢弃 secret)。
var ErrEncUnavailable = errors.New("storageconfig: secret storage requires STUDIO_CONFIG_ENC_KEY")

// ErrNotFound 表示按 org 定位的配置不存在。DeleteForOrg 影响 0 行时返回它。
var ErrNotFound = errors.New("storageconfig: config not found")

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

// UpsertInput 是 UpsertGlobal/UpsertForOrg 的入参。Secret 走 keep-or-replace 语义：
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
}

// Store persists storage_configs.
type Store struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
}

// New builds a Store. box 提供 secret 的静态加解密；nil/disabled box 表示无法存储
// secret (带非空 Secret 的 Upsert 返回 ErrEncUnavailable)。
func New(pool *pgxpool.Pool, box *secretbox.Box) *Store { return &Store{pool: pool, box: box} }

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
			(id, scope, org_id, mode, endpoint, region, bucket, access_key_id, secret_enc, use_ssl, public_prefix, enabled)
		VALUES ($1, 'global', '', $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (scope) WHERE scope='global' DO UPDATE SET
			mode=EXCLUDED.mode, endpoint=EXCLUDED.endpoint, region=EXCLUDED.region,
			bucket=EXCLUDED.bucket, access_key_id=EXCLUDED.access_key_id,
			secret_enc=CASE WHEN $11 THEN EXCLUDED.secret_enc ELSE storage_configs.secret_enc END,
			use_ssl=EXCLUDED.use_ssl, public_prefix=EXCLUDED.public_prefix,
			enabled=EXCLUDED.enabled, updated_at=now()
		RETURNING id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL) AS has_secret, use_ssl, public_prefix, enabled`
	row := s.pool.QueryRow(ctx, q,
		newID(), in.Mode, in.Endpoint, in.Region, in.Bucket, in.AccessKeyID, enc, in.UseSSL, in.PublicPrefix, in.Enabled, replace)
	return scanConfig(row)
}

// UpsertForOrg 写 (scope='org', org_id=orgID) 配置。ON CONFLICT 命中 org partial
// unique index 时 DO UPDATE (keep-or-replace secret)。
func (s *Store) UpsertForOrg(ctx context.Context, orgID string, in UpsertInput) (StorageConfig, error) {
	if orgID == "" {
		return StorageConfig{}, fmt.Errorf("storageconfig: orgID required")
	}
	if err := validate(in); err != nil {
		return StorageConfig{}, err
	}
	replace, enc, err := s.encryptSecret(in.Secret)
	if err != nil {
		return StorageConfig{}, err
	}
	const q = `
		INSERT INTO storage_configs
			(id, scope, org_id, mode, endpoint, region, bucket, access_key_id, secret_enc, use_ssl, public_prefix, enabled)
		VALUES ($1, 'org', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (org_id, mode) WHERE scope='org' DO UPDATE SET
			mode=EXCLUDED.mode, endpoint=EXCLUDED.endpoint, region=EXCLUDED.region,
			bucket=EXCLUDED.bucket, access_key_id=EXCLUDED.access_key_id,
			secret_enc=CASE WHEN $12 THEN EXCLUDED.secret_enc ELSE storage_configs.secret_enc END,
			use_ssl=EXCLUDED.use_ssl, public_prefix=EXCLUDED.public_prefix,
			enabled=EXCLUDED.enabled, updated_at=now()
		RETURNING id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL) AS has_secret, use_ssl, public_prefix, enabled`
	row := s.pool.QueryRow(ctx, q,
		newID(), orgID, in.Mode, in.Endpoint, in.Region, in.Bucket, in.AccessKeyID, enc, in.UseSSL, in.PublicPrefix, in.Enabled, replace)
	return scanConfig(row)
}

// scanConfig 把 RETURNING/SELECT 行扫进公开 DTO (列序固定)。
func scanConfig(row pgx.Row) (StorageConfig, error) {
	var sc StorageConfig
	if err := row.Scan(&sc.ID, &sc.Scope, &sc.OrgID, &sc.Mode, &sc.Endpoint, &sc.Region,
		&sc.Bucket, &sc.AccessKeyID, &sc.HasSecret, &sc.UseSSL, &sc.PublicPrefix, &sc.Enabled); err != nil {
		return StorageConfig{}, fmt.Errorf("storageconfig: scan: %w", err)
	}
	return sc, nil
}

// GetGlobal 读 global 配置。无行 → ok=false。
func (s *Store) GetGlobal(ctx context.Context) (StorageConfig, bool, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
			(secret_enc IS NOT NULL) AS has_secret, use_ssl, public_prefix, enabled
		 FROM storage_configs WHERE scope='global' LIMIT 1`)
	sc, err := scanConfig(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StorageConfig{}, false, nil
		}
		return StorageConfig{}, false, err
	}
	return sc, true, nil
}

// GetForOrg 读某 org 的指定 mode 配置。如果 mode 为空，则返回该 org 的任意一个配置。无行 → ok=false。
func (s *Store) GetForOrg(ctx context.Context, orgID string, mode string) (StorageConfig, bool, error) {
	var row pgx.Row
	if mode != "" {
		row = s.pool.QueryRow(ctx,
			`SELECT id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
				(secret_enc IS NOT NULL) AS has_secret, use_ssl, public_prefix, enabled
			 FROM storage_configs WHERE scope='org' AND org_id=$1 AND mode=$2 LIMIT 1`, orgID, mode)
	} else {
		row = s.pool.QueryRow(ctx,
			`SELECT id, scope, org_id, mode, endpoint, region, bucket, access_key_id,
				(secret_enc IS NOT NULL) AS has_secret, use_ssl, public_prefix, enabled
			 FROM storage_configs WHERE scope='org' AND org_id=$1 LIMIT 1`, orgID)
	}
	sc, err := scanConfig(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StorageConfig{}, false, nil
		}
		return StorageConfig{}, false, err
	}
	return sc, true, nil
}

// DeleteForOrg 删某 org 的指定 mode 配置。如果 mode 为空，则删除该 org 的所有配置。
// 0 行受影响 → ErrNotFound。
func (s *Store) DeleteForOrg(ctx context.Context, orgID string, mode string) error {
	if orgID == "" {
		return fmt.Errorf("storageconfig: orgID required")
	}
	var rowsAffected int64
	var err error
	if mode != "" {
		tag, terr := s.pool.Exec(ctx, `DELETE FROM storage_configs WHERE scope='org' AND org_id=$1 AND mode=$2`, orgID, mode)
		err = terr
		if terr == nil {
			rowsAffected = tag.RowsAffected()
		}
	} else {
		tag, terr := s.pool.Exec(ctx, `DELETE FROM storage_configs WHERE scope='org' AND org_id=$1`, orgID)
		err = terr
		if terr == nil {
			rowsAffected = tag.RowsAffected()
		}
	}
	if err != nil {
		return fmt.Errorf("storageconfig: delete: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
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

// ResolveByID 按 storage_configs.id 直接解析生效配置 (含解密后的 SecretKey)，供
// serve 路径使用：asset 在写入时持久化了它落地的后端身份 (config id)，读回时按该
// id 解析回 EXACTLY 那个后端，独立于 org 当前的 storage_mode。只命中 enabled=true
// 行；未知/已禁用 id → ok=false (调用方回落)。这是 ResolveForOrgAndMode 的 by-id 同伴，
// 行→ResolvedStorage 映射 (含 secret 解密) 完全一致 (复用 resolveOne)。
func (s *Store) ResolveByID(ctx context.Context, id string) (ResolvedStorage, bool, error) {
	return s.resolveOne(ctx, `WHERE id=$1 AND enabled=true`, id)
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
func (s *Store) configIDOne(ctx context.Context, where string, args ...any) (string, bool, error) {
	q := `SELECT id FROM storage_configs ` + where + ` LIMIT 1`
	var id string
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("storageconfig: config id: %w", err)
	}
	return id, true, nil
}

// resolveOne 跑一条 WHERE 子句的 SELECT 并解密 secret。无行 → ok=false。
func (s *Store) resolveOne(ctx context.Context, where string, args ...any) (ResolvedStorage, bool, error) {
	q := `SELECT mode, endpoint, region, bucket, access_key_id, secret_enc, use_ssl, public_prefix
		 FROM storage_configs ` + where + ` LIMIT 1`
	row := s.pool.QueryRow(ctx, q, args...)
	var rs ResolvedStorage
	var enc []byte
	if err := row.Scan(&rs.Mode, &rs.Endpoint, &rs.Region, &rs.Bucket, &rs.AccessKeyID, &enc, &rs.UseSSL, &rs.PublicPrefix); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
