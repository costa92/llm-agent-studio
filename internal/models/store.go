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
)

// ErrSecretParam is returned when params_json carries a credential-looking
// field (密钥审计, spec §6: API key 不在此 — server-side env only, never the DB).
var ErrSecretParam = errors.New("models: params must not contain credentials (API keys live in server env only)")

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
	}
}

// ModelConfig is a model_configs row.
type ModelConfig struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"orgId"`
	Kind      string          `json:"kind"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Enabled   bool            `json:"enabled"`
	IsDefault bool            `json:"isDefault"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// CreateInput is the input to Create.
type CreateInput struct {
	OrgID     string
	Kind      string
	Provider  string
	Model     string
	Enabled   bool
	IsDefault bool
	Params    json.RawMessage
}

// Store persists model_configs.
type Store struct{ pool *pgxpool.Pool }

// New builds a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

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
	if len(in.Params) > 0 {
		var m map[string]any
		if err := json.Unmarshal(in.Params, &m); err == nil {
			if key, found := secretKeyIn(m); found {
				return ModelConfig{}, fmt.Errorf("%w (field %q)", ErrSecretParam, key)
			}
		}
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
		Enabled: in.Enabled, IsDefault: in.IsDefault, Params: in.Params,
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO model_configs (id, org_id, kind, provider, model, enabled, is_default, params_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		mc.ID, mc.OrgID, mc.Kind, mc.Provider, mc.Model, mc.Enabled, mc.IsDefault, mc.Params); err != nil {
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
		`SELECT id, org_id, kind, provider, model, enabled, is_default, params_json
		 FROM model_configs WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ModelConfig, 0)
	for rows.Next() {
		var mc ModelConfig
		if err := rows.Scan(&mc.ID, &mc.OrgID, &mc.Kind, &mc.Provider, &mc.Model, &mc.Enabled, &mc.IsDefault, &mc.Params); err != nil {
			return nil, err
		}
		out = append(out, mc)
	}
	return out, rows.Err()
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
