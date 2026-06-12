// Package models owns model_configs CRUD + the built-in model catalog (spec §9
// 模型管理). API keys are NOT stored here (server-side config, spec §6). The
// catalog is the fixed set of ecosystem-supported image providers (spec §13 R3:
// only openai/google/minimax/volcengine — no Flux/SDXL/Midjourney).
package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
)

// ErrSecretParam is returned when params_json carries a credential-looking
// field (密钥审计, spec §6: params 仍不得夹带凭据 — 专用 APIKey 字段是唯一合法入口)。
var ErrSecretParam = errors.New("models: params must not contain credentials (use the dedicated api key field)")

// ErrEncUnavailable 表示请求存储 per-config api key，但加密 box 未启用 (未配置
// STUDIO_CONFIG_ENC_KEY)，无法静态加密，故拒绝。
var ErrEncUnavailable = errors.New("models: api key storage requires STUDIO_CONFIG_ENC_KEY")

// forbiddenParamKeys flag anywhere inside a (lowercased) key name. NOTE
// (评审修复 M2): bare "token" is deliberately NOT in this substring list — it
// false-positives on legitimate count/config fields (max_tokens, token_budget).
// Credential token/key names are caught word-finally in isCredentialKey.
var forbiddenParamKeys = []string{"apikey", "secret", "password", "passwd", "credential"}

// isCredentialKey reports whether a params key looks like a credential.
// "token"/"_key" match only as the FINAL word (api_token, access_token,
// accessToken, api_key) so *_tokens-style count fields stay legal.
func isCredentialKey(k string) bool {
	lk := strings.ToLower(k)
	for _, f := range forbiddenParamKeys {
		if strings.Contains(lk, f) {
			return true
		}
	}
	return strings.HasSuffix(lk, "token") || strings.HasSuffix(lk, "_key")
}

// secretKeyIn recursively scans a decoded params object for credential-looking
// key names. Returns the offending key.
func secretKeyIn(m map[string]any) (string, bool) {
	for k, v := range m {
		if isCredentialKey(k) {
			return k, true
		}
		if sub, ok := v.(map[string]any); ok {
			if found, ok := secretKeyIn(sub); ok {
				return found, true
			}
		}
	}
	return "", false
}

// CatalogEntry is one selectable provider+model in the built-in catalog.
type CatalogEntry struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
}

// Catalog returns the fixed M2 image-model catalog (spec §13 R3).
func Catalog() []CatalogEntry {
	return []CatalogEntry{
		{Provider: "openai", Model: "gpt-image-1", Kind: "image", Label: "OpenAI GPT-Image-1"},
		{Provider: "openai", Model: "dall-e-3", Kind: "image", Label: "OpenAI DALL·E 3"},
		{Provider: "google", Model: "imagen-3.0-generate-002", Kind: "image", Label: "Google Imagen 3"},
		{Provider: "minimax", Model: "image-01", Kind: "image", Label: "MiniMax image-01"},
		{Provider: "volcengine", Model: "doubao-seedream-3-0-t2i", Kind: "image", Label: "Volcengine Seedream"},
		// M4 二期: video/audio entries. fake-* drive the sandbox FakeAsync live
		// verification; the real models are key-gated skeletons (spec §8, TODO m5).
		{Provider: "fake", Model: "fake-video-async", Kind: "video", Label: "Fake Async Video (sandbox)"},
		{Provider: "fake", Model: "fake-audio-async", Kind: "audio", Label: "Fake Async Audio (sandbox)"},
		{Provider: "runway", Model: "gen-3", Kind: "video", Label: "Runway Gen-3"},
		{Provider: "kling", Model: "kling-v1", Kind: "video", Label: "Kling v1"},
		{Provider: "google", Model: "veo-2", Kind: "video", Label: "Google Veo 2"},
		{Provider: "openai", Model: "tts-1", Kind: "audio", Label: "OpenAI TTS-1"},
		// BYOK: text/chat 模型建议项 (provider/model 在 store 中自由填写，UI 还可经
		// "openai-compatible" 伪 provider 自定义 base_url + model — 后续任务)。
		{Provider: "deepseek", Model: "deepseek-chat", Kind: "text", Label: "DeepSeek Chat"},
		{Provider: "openai", Model: "gpt-4o-mini", Kind: "text", Label: "OpenAI GPT-4o mini"},
	}
}

// ModelConfig is a model_configs row returned to clients. 永不暴露 api key：只回
// BaseURL + HasAPIKey 布尔标记 (解密后的 key 仅 ResolveForOrg 内部可见)。
type ModelConfig struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"orgId"`
	Kind      string          `json:"kind"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Enabled   bool            `json:"enabled"`
	IsDefault bool            `json:"isDefault"`
	BaseURL   string          `json:"baseUrl"`
	HasAPIKey bool            `json:"hasApiKey"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// ResolvedModel 是运行层 (ModelRouter) 用的解析结果，带解密后的 APIKey。
// 这是唯一暴露明文 key 的路径，且只在服务端内部 (绝不进 HTTP handler)。
type ResolvedModel struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
	Params   json.RawMessage
}

// CreateInput is the input to Create.
type CreateInput struct {
	OrgID     string
	Kind      string
	Provider  string
	Model     string
	Enabled   bool
	IsDefault bool
	BaseURL   string          // 可选，per-config endpoint (openai-compatible)
	APIKey    string          // 可选明文 key；非空则经 box 加密入库，绝不回显
	Params    json.RawMessage
}

// Store persists model_configs.
type Store struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
}

// New builds a Store. box 提供 per-config api key 的静态加解密；nil/disabled box
// 表示无法存储 key (带非空 APIKey 的 Create 返回 ErrEncUnavailable)。
func New(pool *pgxpool.Pool, box *secretbox.Box) *Store { return &Store{pool: pool, box: box} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create inserts a model config. A new default clears other defaults for the
// same org+kind (one default per kind).
func (s *Store) Create(ctx context.Context, in CreateInput) (ModelConfig, error) {
	if in.OrgID == "" || in.Provider == "" || in.Model == "" {
		return ModelConfig{}, fmt.Errorf("models: OrgID, Provider, Model required")
	}
	kind := in.Kind
	if kind == "" {
		kind = "image"
	}
	// 守卫仍只扫 params_json (params 不得夹带凭据)。专用 APIKey 字段是合法入口。
	if len(in.Params) > 0 {
		var m map[string]any
		if err := json.Unmarshal(in.Params, &m); err == nil {
			if key, found := secretKeyIn(m); found {
				return ModelConfig{}, fmt.Errorf("%w (field %q)", ErrSecretParam, key)
			}
		}
	}
	// per-config api key：非空则静态加密入库；无可用 box 则拒绝 (不静默丢弃 key)。
	var keyEnc []byte
	if in.APIKey != "" {
		if !s.box.Enabled() {
			return ModelConfig{}, ErrEncUnavailable
		}
		enc, err := s.box.Encrypt([]byte(in.APIKey))
		if err != nil {
			return ModelConfig{}, fmt.Errorf("models: encrypt api key: %w", err)
		}
		keyEnc = enc
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ModelConfig{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if in.IsDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE model_configs SET is_default=false WHERE org_id=$1 AND kind=$2`, in.OrgID, kind); err != nil {
			return ModelConfig{}, fmt.Errorf("models: clear default: %w", err)
		}
	}
	mc := ModelConfig{
		ID: newID(), OrgID: in.OrgID, Kind: kind, Provider: in.Provider, Model: in.Model,
		Enabled: in.Enabled, IsDefault: in.IsDefault, BaseURL: in.BaseURL,
		HasAPIKey: keyEnc != nil, Params: in.Params,
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO model_configs (id, org_id, kind, provider, model, enabled, is_default, base_url, api_key_enc, params_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		mc.ID, mc.OrgID, mc.Kind, mc.Provider, mc.Model, mc.Enabled, mc.IsDefault, mc.BaseURL, keyEnc, mc.Params); err != nil {
		return ModelConfig{}, fmt.Errorf("models: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelConfig{}, err
	}
	return mc, nil
}

// ListByOrg lists an org's model configs, newest first.
func (s *Store) ListByOrg(ctx context.Context, orgID string) ([]ModelConfig, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, kind, provider, model, enabled, is_default,
		        COALESCE(base_url,''), (api_key_enc IS NOT NULL) AS has_api_key, params_json
		 FROM model_configs WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ModelConfig, 0)
	for rows.Next() {
		var mc ModelConfig
		if err := rows.Scan(&mc.ID, &mc.OrgID, &mc.Kind, &mc.Provider, &mc.Model, &mc.Enabled, &mc.IsDefault,
			&mc.BaseURL, &mc.HasAPIKey, &mc.Params); err != nil {
			return nil, err
		}
		out = append(out, mc)
	}
	return out, rows.Err()
}

// ResolveForOrg 返回某 kind 的启用默认配置 (含解密后的 APIKey)，供运行层
// ModelRouter 使用。无启用默认时 ok=false。这是唯一暴露明文 key 的路径，仅服务端
// 内部调用 (绝不进 HTTP handler)。api_key_enc 为 NULL 时 APIKey 为空。
func (s *Store) ResolveForOrg(ctx context.Context, orgID, kind string) (ResolvedModel, bool, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT provider, model, COALESCE(base_url,''), api_key_enc, params_json
		 FROM model_configs
		 WHERE org_id=$1 AND kind=$2 AND enabled=true AND is_default=true
		 ORDER BY created_at DESC LIMIT 1`, orgID, kind)
	var rm ResolvedModel
	var keyEnc []byte
	if err := row.Scan(&rm.Provider, &rm.Model, &rm.BaseURL, &keyEnc, &rm.Params); err != nil {
		if err == pgx.ErrNoRows {
			return ResolvedModel{}, false, nil
		}
		return ResolvedModel{}, false, fmt.Errorf("models: resolve: %w", err)
	}
	if len(keyEnc) > 0 {
		if !s.box.Enabled() {
			return ResolvedModel{}, false, ErrEncUnavailable
		}
		pt, err := s.box.Decrypt(keyEnc)
		if err != nil {
			return ResolvedModel{}, false, fmt.Errorf("models: decrypt api key: %w", err)
		}
		rm.APIKey = string(pt)
	}
	return rm, true, nil
}

// DefaultForOrg returns the org's default provider+model for a kind. ok=false
// when no enabled default exists (caller falls back to the registry default).
func (s *Store) DefaultForOrg(ctx context.Context, orgID, kind string) (provider, model string, ok bool, err error) {
	row := s.pool.QueryRow(ctx,
		`SELECT provider, model FROM model_configs
		 WHERE org_id=$1 AND kind=$2 AND enabled=true AND is_default=true
		 ORDER BY created_at DESC LIMIT 1`, orgID, kind)
	if err := row.Scan(&provider, &model); err != nil {
		if err == pgx.ErrNoRows {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("models: default: %w", err)
	}
	return provider, model, true, nil
}
