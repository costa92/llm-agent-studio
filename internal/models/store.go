// Package models owns model_configs CRUD + the built-in model catalog (spec §9
// 模型管理). API keys are NOT stored here (server-side config, spec §6). The
// catalog is the fixed set of ecosystem-supported image providers (spec §13 R3:
// only openai/google/minimax/volcengine — no Flux/SDXL/Midjourney).
package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
)

// ErrSecretParam is returned when params_json carries a credential-looking
// field (密钥审计, spec §6: params 仍不得夹带凭据 — 专用 APIKey 字段是唯一合法入口)。
var ErrSecretParam = errors.New("models: params must not contain credentials (use the dedicated api key field)")

// ErrEncUnavailable 表示请求存储 per-config api key，但加密 box 未启用 (未配置
// STUDIO_CONFIG_ENC_KEY)，无法静态加密，故拒绝。
var ErrEncUnavailable = errors.New("models: api key storage requires STUDIO_CONFIG_ENC_KEY")

// ErrNotFound 表示按 (id AND org_id) 定位的配置不存在 (含跨 org 误访问)。
// Update/Delete 影响 0 行时返回它，handler 映射 404。
var ErrNotFound = errors.New("models: config not found")

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
		{Provider: "fake", Model: "fake", Kind: "image", Label: "Fake Image (sandbox)"},
		// video/audio: fake-* drive the sandbox FakeAsync live verification;
		// audio 真实实现是 MiniMax T2A（同步，org config 自带 key）。M4 的必失败
		// 骨架条目（Runway/Kling/Veo/OpenAI-TTS/Hailuo-02）已随 Phase 2.1 下架——
		// 真实接线时再回表。
		{Provider: "fake", Model: "fake-video-async", Kind: "video", Label: "Fake Async Video (sandbox)"},
		{Provider: "fake", Model: "fake-audio-async", Kind: "audio", Label: "Fake Async Audio (sandbox)"},
		{Provider: "minimax", Model: "speech-02-hd", Kind: "audio", Label: "MiniMax speech-02-hd"},
		{Provider: "minimax", Model: "speech-01-turbo", Kind: "audio", Label: "MiniMax speech-01-turbo"},
		// BYOK: text/chat 模型建议项 (provider/model 在 store 中自由填写，UI 还可经
		// "openai-compatible" 伪 provider 自定义 base_url + model — 后续任务)。
		{Provider: "deepseek", Model: "deepseek-chat", Kind: "text", Label: "DeepSeek Chat"},
		{Provider: "openai", Model: "gpt-4o-mini", Kind: "text", Label: "OpenAI GPT-4o mini"},
		// MiniMax text/abab 系列（modellist 走 /v1/models 拉取；catalog 仅作兜底）。
		{Provider: "minimax", Model: "MiniMax-Text-01", Kind: "text", Label: "MiniMax Text-01"},
		{Provider: "minimax", Model: "MiniMax-M1", Kind: "text", Label: "MiniMax M1"},
		{Provider: "minimax", Model: "abab-6.5s-chat", Kind: "text", Label: "MiniMax abab-6.5s-chat"},
		// 本地 Ollama 文本模型（无需 key，base_url 缺省 http://localhost:11434；模型名可自由改）。
		{Provider: "ollama", Model: "llama3", Kind: "text", Label: "Ollama Llama 3 (本地)"},
		{Provider: "ollama", Model: "qwen2.5", Kind: "text", Label: "Ollama Qwen2.5 (本地)"},
	}
}

// ModelConfig is a model_configs row returned to clients.
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
	// NOTE: ModelConfig is the CLIENT-facing DTO and deliberately carries NO
	// plaintext api key — only HasAPIKey. The decrypted key is exposed solely
	// server-side via ResolvedModel (ResolveForOrg), never over HTTP (审计: 绝不回传 key)。
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
	BaseURL   string // 可选，per-config endpoint (openai-compatible)
	APIKey    string // 可选明文 key；非空则经 box 加密入库，绝不回显
	Params    json.RawMessage
}

// UpdateInput is the input to Update. 不含 OrgID（由 Update 的 orgID 参数传入，
// 永远按 (id AND org_id) 定位，禁止跨 org 编辑）。APIKey 走 keep-or-replace 语义：
// 空 → 保留既有 api_key_enc 不动；非空 → 重新加密替换。
type UpdateInput struct {
	Kind      string
	Provider  string
	Model     string
	Enabled   bool
	IsDefault bool
	BaseURL   string
	APIKey    string // 空=保留既有 key；非空=重新加密替换 (同 Create 的 box 守卫)
	Params    json.RawMessage
}

// Store persists model_configs.
type Store struct {
	db  *gorm.DB
	box *secretbox.Box
}

// New builds a Store. box 提供 per-config api key 的静态加解密；nil/disabled box
// 表示无法存储 key (带非空 APIKey 的 Create 返回 ErrEncUnavailable)。
func New(db *gorm.DB, box *secretbox.Box) *Store { return &Store{db: db, box: box} }

// KeyForConfig returns the DECRYPTED api key for one config, located by
// (id AND org_id). Used by (a) the live model-listing endpoint so an admin editing
// an existing config can refresh the model list WITHOUT re-entering the key, and
// (b) the admin reveal endpoint (GET .../model-configs/{id}/reveal), the single
// place the plaintext key is returned over HTTP. Returns "" (no error) when the
// config stores no key; ErrNotFound when the row is absent or belongs to another
// org. The bulk list/create/update responses still never carry the key.
func (s *Store) KeyForConfig(ctx context.Context, orgID, id string) (string, error) {
	var keyEnc []byte
	err := s.db.WithContext(ctx).Raw(
		`SELECT api_key_enc FROM model_configs WHERE id=$1 AND org_id=$2`, id, orgID).Row().Scan(&keyEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("models: key for config: %w", err)
	}
	if len(keyEnc) == 0 {
		return "", nil
	}
	pt, err := s.box.Decrypt(keyEnc)
	if err != nil {
		return "", fmt.Errorf("models: decrypt key: %w", err)
	}
	return string(pt), nil
}

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
	mc := ModelConfig{
		ID: newID(), OrgID: in.OrgID, Kind: kind, Provider: in.Provider, Model: in.Model,
		Enabled: in.Enabled, IsDefault: in.IsDefault, BaseURL: in.BaseURL,
		HasAPIKey: keyEnc != nil, Params: in.Params,
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if in.IsDefault {
			if res := tx.Exec(
				`UPDATE model_configs SET is_default=false WHERE org_id=$1 AND kind=$2`, in.OrgID, kind); res.Error != nil {
				return fmt.Errorf("models: clear default: %w", res.Error)
			}
		}
		if res := tx.Exec(
			`INSERT INTO model_configs (id, org_id, kind, provider, model, enabled, is_default, base_url, api_key_enc, params_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			mc.ID, mc.OrgID, mc.Kind, mc.Provider, mc.Model, mc.Enabled, mc.IsDefault, mc.BaseURL, keyEnc, mc.Params); res.Error != nil {
			return fmt.Errorf("models: insert: %w", res.Error)
		}
		return nil
	}); err != nil {
		return ModelConfig{}, err
	}
	return mc, nil
}

// Update updates an existing config scoped by (id AND org_id) — 禁止跨 org 编辑。
// 0 行受影响 → ErrNotFound。APIKey 走 keep-or-replace：空则保留既有 api_key_enc，
// 非空则重新加密替换 (box 未启用时同 Create 返回 ErrEncUnavailable)。新默认会清掉
// 同 org+kind 的其它默认 (与 Create 一致)。返回更新后的 ModelConfig (绝不含明文 key)。
func (s *Store) Update(ctx context.Context, id, orgID string, in UpdateInput) (ModelConfig, error) {
	if id == "" || orgID == "" || in.Provider == "" || in.Model == "" {
		return ModelConfig{}, fmt.Errorf("models: ID, OrgID, Provider, Model required")
	}
	kind := in.Kind
	if kind == "" {
		kind = "image"
	}
	// 守卫仍只扫 params_json (params 不得夹带凭据)。校验先于任何 DB 访问。
	if len(in.Params) > 0 {
		var m map[string]any
		if err := json.Unmarshal(in.Params, &m); err == nil {
			if key, found := secretKeyIn(m); found {
				return ModelConfig{}, fmt.Errorf("%w (field %q)", ErrSecretParam, key)
			}
		}
	}
	// keep-or-replace：APIKey 非空则静态加密 (无可用 box 则拒绝，不静默丢弃 key)。
	var keyEnc []byte
	replaceKey := in.APIKey != ""
	if replaceKey {
		if !s.box.Enabled() {
			return ModelConfig{}, ErrEncUnavailable
		}
		enc, err := s.box.Encrypt([]byte(in.APIKey))
		if err != nil {
			return ModelConfig{}, fmt.Errorf("models: encrypt api key: %w", err)
		}
		keyEnc = enc
	}
	var mc ModelConfig
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if in.IsDefault {
			// 清掉同 org+kind 的其它默认 (排除自己，避免随后把自己又设回 false 的竞态)。
			if res := tx.Exec(
				`UPDATE model_configs SET is_default=false WHERE org_id=$1 AND kind=$2 AND id<>$3`, orgID, kind, id); res.Error != nil {
				return fmt.Errorf("models: clear default: %w", res.Error)
			}
		}
		// api_key_enc 仅在 replaceKey 时写 ($8)，否则用 COALESCE 保留既有值。
		res := tx.Exec(
			`UPDATE model_configs
		 SET kind=$3, provider=$4, model=$5, enabled=$6, is_default=$7,
		     base_url=$8, api_key_enc=CASE WHEN $9 THEN $10 ELSE api_key_enc END, params_json=$11
		 WHERE id=$1 AND org_id=$2`,
			id, orgID, kind, in.Provider, in.Model, in.Enabled, in.IsDefault, in.BaseURL, replaceKey, keyEnc, in.Params)
		if res.Error != nil {
			return fmt.Errorf("models: update: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		// 重新读出以拿到准确的 has_api_key 以及明文 apiKey。
		var keyEncReload []byte
		var paramsReload []byte
		if err := tx.Raw(
			`SELECT id, org_id, kind, provider, model, enabled, is_default,
		        COALESCE(base_url,''), api_key_enc, params_json
		 FROM model_configs WHERE id=$1 AND org_id=$2`, id, orgID).Row().
			Scan(&mc.ID, &mc.OrgID, &mc.Kind, &mc.Provider, &mc.Model, &mc.Enabled, &mc.IsDefault,
				&mc.BaseURL, &keyEncReload, &paramsReload); err != nil {
			return fmt.Errorf("models: reload: %w", err)
		}
		mc.Params = json.RawMessage(paramsReload)
		mc.HasAPIKey = len(keyEncReload) > 0
		// 不解密回传明文 key——ModelConfig 是客户端 DTO，只暴露 HasAPIKey (审计: 绝不回传 key)。
		return nil
	}); err != nil {
		return ModelConfig{}, err
	}
	return mc, nil
}

// Delete removes a config scoped by (id AND org_id). 0 行受影响 → ErrNotFound。
func (s *Store) Delete(ctx context.Context, id, orgID string) error {
	if id == "" || orgID == "" {
		return fmt.Errorf("models: ID, OrgID required")
	}
	res := s.db.WithContext(ctx).Where("id = ? AND org_id = ?", id, orgID).Delete(&modelConfigRow{})
	if res.Error != nil {
		return fmt.Errorf("models: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByOrg lists an org's model configs, newest first.
func (s *Store) ListByOrg(ctx context.Context, orgID string) ([]ModelConfig, error) {
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT id, org_id, kind, provider, model, enabled, is_default,
		        COALESCE(base_url,''), api_key_enc, params_json
		 FROM model_configs WHERE org_id=$1 ORDER BY created_at DESC`, orgID).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ModelConfig, 0)
	for rows.Next() {
		var mc ModelConfig
		var keyEnc []byte
		var params []byte
		if err := rows.Scan(&mc.ID, &mc.OrgID, &mc.Kind, &mc.Provider, &mc.Model, &mc.Enabled, &mc.IsDefault,
			&mc.BaseURL, &keyEnc, &params); err != nil {
			return nil, err
		}
		mc.Params = json.RawMessage(params)
		mc.HasAPIKey = len(keyEnc) > 0
		// 不解密回传明文 key——只暴露 HasAPIKey (审计: 绝不回传 key)。
		out = append(out, mc)
	}
	return out, rows.Err()
}

// ResolveForOrg 返回某 kind 的启用默认配置 (含解密后的 APIKey)，供运行层
// ModelRouter 使用。无启用默认时 ok=false。这是唯一暴露明文 key 的路径，仅服务端
// 内部调用 (绝不进 HTTP handler)。api_key_enc 为 NULL 时 APIKey 为空。
func (s *Store) ResolveForOrg(ctx context.Context, orgID, kind string) (ResolvedModel, bool, error) {
	row := s.db.WithContext(ctx).Raw(
		`SELECT provider, model, COALESCE(base_url,''), api_key_enc, params_json
		 FROM model_configs
		 WHERE org_id=$1 AND kind=$2 AND enabled=true AND is_default=true
		 ORDER BY created_at DESC LIMIT 1`, orgID, kind).Row()
	var rm ResolvedModel
	var keyEnc []byte
	var params []byte
	if err := row.Scan(&rm.Provider, &rm.Model, &rm.BaseURL, &keyEnc, &params); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResolvedModel{}, false, nil
		}
		return ResolvedModel{}, false, fmt.Errorf("models: resolve: %w", err)
	}
	rm.Params = json.RawMessage(params)
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

// ResolveForOrgNamed 查某 org 下指定 (kind, provider, model) 的启用配置（不必是默认）。
// 供 per-project 规划模型 override 走（M5.1：project.planner_provider+planner_model →
// 拿到 org 的对应 model_config → 用其 api_key 调 buildChat）。同样唯一暴露明文 key。
// 找不到匹配 = (zero, false, nil)，caller 走默认。空串 provider/modelName 直接返
// 0 行（避免无谓的 SQL）。
func (s *Store) ResolveForOrgNamed(ctx context.Context, orgID, kind, provider, modelName string) (ResolvedModel, bool, error) {
	if provider == "" || modelName == "" {
		return ResolvedModel{}, false, nil
	}
	row := s.db.WithContext(ctx).Raw(
		`SELECT provider, model, COALESCE(base_url,''), api_key_enc, params_json
			 FROM model_configs
			 WHERE org_id=$1 AND kind=$2 AND provider=$3 AND model=$4 AND enabled=true
			 ORDER BY is_default DESC, created_at DESC LIMIT 1`, orgID, kind, provider, modelName).Row()
	var rm ResolvedModel
	var keyEnc []byte
	var params []byte
	if err := row.Scan(&rm.Provider, &rm.Model, &rm.BaseURL, &keyEnc, &params); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResolvedModel{}, false, nil
		}
		return ResolvedModel{}, false, fmt.Errorf("models: resolve named: %w", err)
	}
	rm.Params = json.RawMessage(params)
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
	row := s.db.WithContext(ctx).Raw(
		`SELECT provider, model FROM model_configs
		 WHERE org_id=$1 AND kind=$2 AND enabled=true AND is_default=true
		 ORDER BY created_at DESC LIMIT 1`, orgID, kind).Row()
	if err := row.Scan(&provider, &model); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("models: default: %w", err)
	}
	return provider, model, true, nil
}
