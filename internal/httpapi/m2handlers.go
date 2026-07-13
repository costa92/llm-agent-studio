package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/builtinnode"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/modellist"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/review"
)

// errReviewConflict re-exports review.ErrConflict for handler tests/mapping.
var errReviewConflict = review.ErrConflict

// ReviewPort is the HITL surface (satisfied by *review.Service).
type ReviewPort interface {
	Accept(ctx context.Context, assetID string) error
	Reject(ctx context.Context, assetID string) error
	Regenerate(ctx context.Context, assetID, editedPrompt string) (newAssetID, todoID string, err error)
	RegenerateNarration(ctx context.Context, audioAssetID, newText string) (newAssetID, todoID string, err error)
}

// AssetLibrary is the asset read/search surface (satisfied by *assets.Store).
type AssetLibrary interface {
	Get(ctx context.Context, id string) (assets.Asset, error)
	VersionHistory(ctx context.Context, id string) ([]assets.Asset, error)
	Library(ctx context.Context, f assets.LibraryFilter) ([]assets.Asset, string, error)
	OrgIDForAsset(ctx context.Context, assetID string) (string, error)
}

type BlobRouter interface {
	BlobStoreFor(ctx context.Context, orgID string) (blob.BlobStore, error)
	BlobStoreForMode(ctx context.Context, orgID string, mode string) (blob.BlobStore, error)
	// BlobStoreForConfigID resolves the store by an asset's persisted backend
	// token (serve path). ConfigIDForMode returns the token to persist at write
	// time. Together they decouple serve from the project's CURRENT storage_mode.
	BlobStoreForConfigID(ctx context.Context, orgID string, configID string) (blob.BlobStore, error)
	ConfigIDForMode(ctx context.Context, orgID string, mode string) (string, error)
	// ResolveWriteTarget picks the write backend using priority:
	// project override (projConfigID) → org default → builtin.
	ResolveWriteTarget(ctx context.Context, orgID, projConfigID string) (blob.BlobStore, string, error)
}

type ProjectReader interface {
	Get(ctx context.Context, id string) (project.Project, error)
}

// BlobServer additionally serves bytes for the localfs回源 handler.
type BlobServer interface {
	KeyFromPath(path string) string
	Verify(key, exp, sig string) error
	ReadKey(key string) ([]byte, string, error)
}

// ModelStore is the model_configs surface (satisfied by *models.Store).
type ModelStore interface {
	Create(ctx context.Context, in models.CreateInput) (models.ModelConfig, error)
	ListByOrg(ctx context.Context, orgID string) ([]models.ModelConfig, error)
	Update(ctx context.Context, id, orgID string, in models.UpdateInput) (models.ModelConfig, error)
	Delete(ctx context.Context, id, orgID string) error
}

// CostStore is the cost aggregation surface (satisfied by *cost.Store).
type CostStore interface {
	Record(ctx context.Context, g cost.Generation) error
	ByOrgBetween(ctx context.Context, orgID string, from, to time.Time) (cost.Aggregate, error)
	ByProjectBetween(ctx context.Context, projectID string, from, to time.Time) (cost.Aggregate, error)
	PerProjectByOrg(ctx context.Context, orgID string, from, to time.Time) ([]cost.ProjectAggregate, error)
	PerActorByOrg(ctx context.Context, orgID string, from, to time.Time) ([]cost.ActorAggregate, error)
	ByPlan(ctx context.Context, projectID, planID string) (cost.PlanCost, error)
	RecentByOrg(ctx context.Context, orgID string, limit int, cursor string) ([]cost.LedgerEntry, string, error)
	CountByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error)
}

// CoverGenerator resolves a per-org media generator for the cover-generate
// handler (satisfied by *modelrouter.Router). MediaGeneratorForNamed honors a
// caller-supplied provider/model; MediaGeneratorFor falls back to the org default.
type CoverGenerator interface {
	MediaGeneratorFor(ctx context.Context, orgID, kind string) generate.MediaGenerator
	MediaGeneratorForNamed(ctx context.Context, orgID, kind, provider, model string) generate.MediaGenerator
}

// CoverAssetWriter creates a cover asset row and fills its blob_key/url after the
// bytes land in the blob store (satisfied by *assets.Store).
type CoverAssetWriter interface {
	Create(ctx context.Context, in assets.CreateInput) (assets.Asset, error)
	SetCoverBlob(ctx context.Context, assetID, blobKey, url, storageConfigID string) error
}

const signedURLTTL = 10 * time.Minute

// promptStylesHandler (GET /api/prompt-styles): viewer+.
func promptStylesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"styles": prompt.Styles()})
	}
}

// builtinNodeTypesHandler (GET /api/node-types/builtin): authenticated, global.
// Returns the static built-in workflow node catalog (single source: builtinnode).
func builtinNodeTypesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": builtinnode.Catalog()})
	}
}

// promptBuildHandler (POST /api/prompt/build): viewer+. Previews the built prompt.
func promptBuildHandler(b *prompt.Builder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt string `json:"prompt"`
			Style  string `json:"style"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
			http.Error(w, "bad request: prompt required", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"prompt": b.Build(req.Prompt, req.Style)})
	}
}

// catalogEntryView augments a static models.CatalogEntry with a runtime
// `available` flag (provider key configured → adapter is/will be registered).
// Availability is runtime state, not catalog data, so it lives only in this view.
type catalogEntryView struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
}

// modelCatalogHandler (GET /api/model-catalog): admin. avail reports whether a
// (provider, kind) entry's key is configured; nil → treat all as available.
func modelCatalogHandler(avail func(provider, kind string) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cat := models.Catalog()
		out := make([]catalogEntryView, 0, len(cat))
		for _, e := range cat {
			available := true
			if avail != nil {
				available = avail(e.Provider, e.Kind)
			}
			out = append(out, catalogEntryView{
				Provider: e.Provider, Model: e.Model, Kind: e.Kind, Label: e.Label,
				Available: available,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"catalog": out})
	}
}

// listModelsRequest is the body of the live model-listing endpoint.
type listModelsRequest struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`   // entered key (create / key change); optional
	ConfigID string `json:"configId"` // existing config → reuse its stored key
}

// listModelsHandler (POST /api/orgs/{org}/model-configs/list-models): admin.
// Fetches the provider's live model list from its OFFICIAL API. When apiKey is
// omitted but configId names an existing config, keyLookup resolves that config's
// stored (decrypted) key so an admin can refresh the list without re-typing the
// key. Any failure (unsupported provider, bad key, network) falls back to the
// static catalog for that provider, with a clean user-facing "message" + optional
// actionable "hint" in the response. The raw cause is logged for ops but never
// echoed to the client. The key is sent only to the provider and is never
// echoed back.
func listModelsHandler(keyLookup func(ctx context.Context, orgID, configID string) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := r.PathValue("org")
		var req listModelsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Provider == "" {
			http.Error(w, "provider required", http.StatusBadRequest)
			return
		}
		apiKey := req.APIKey
		if apiKey == "" && req.ConfigID != "" && keyLookup != nil {
			if k, err := keyLookup(r.Context(), org, req.ConfigID); err == nil {
				apiKey = k
			}
		}
		res, info := modellist.List(r.Context(), req.Provider, req.BaseURL, apiKey, catalogModelsFor(req.Provider))
		out := map[string]any{"models": res.Models, "source": res.Source}
		if info != nil {
			if info.Internal != nil {
				slog.Warn("list models: live fetch failed, falling back to catalog",
					"provider", req.Provider, "err", info.Internal)
			}
			out["message"] = info.Message
			if info.Hint != "" {
				out["hint"] = info.Hint
			}
			out["error"] = info.Message
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// revealModelKeyHandler (GET /api/orgs/{org}/model-configs/{id}/reveal): admin.
// 解密并回传单个配置的完整 API key 明文，供管理员核对已存密钥。这是唯一会经 HTTP
// 回传明文 key 的端点——列表/创建/更新响应仍绝不夹带 key（守卫测试钉死该约束）。
// keyLookup 返回 "" 表示该配置未存 per-config key（→ hasApiKey:false，apiKey 空）；
// ErrNotFound（不存在/跨 org）→ 404；解密失败等 → 500。
func revealModelKeyHandler(keyLookup func(ctx context.Context, orgID, configID string) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if keyLookup == nil {
			http.Error(w, "key reveal unavailable", http.StatusServiceUnavailable)
			return
		}
		key, err := keyLookup(r.Context(), r.PathValue("org"), r.PathValue("id"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				http.Error(w, "model config not found", http.StatusNotFound)
				return
			}
			// 解密失败等内部错误：原始 error（含 NaCl secretbox 实现细节）只记服务端日志，
			// 绝不原文回传客户端——否则会泄漏底层加密实现。返回脱敏可操作消息。
			slog.Error("model key reveal failed", "org", r.PathValue("org"), "config", r.PathValue("id"), "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": "密钥解密失败，请重新保存该模型配置"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hasApiKey": key != "", "apiKey": key})
	}
}

// catalogModelsFor returns the static catalog model ids for a provider — the
// fallback list when live fetching is unavailable.
func catalogModelsFor(provider string) []string {
	out := []string{}
	for _, e := range models.Catalog() {
		if e.Provider == provider {
			out = append(out, e.Model)
		}
	}
	return out
}

// acceptHandler (POST /api/assets/{id}/accept): admin. 409 on non-pending. No
// run_event is written: HITL transitions are not part of the SSE run timeline
// (the review board polls asset status), and run_events.project_id has a FK to
// projects, so writing with an empty project id would fail.
func acceptHandler(rv ReviewPort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := rv.Accept(r.Context(), id); err != nil {
			if errors.Is(err, errReviewConflict) {
				http.Error(w, "asset not pending_acceptance", http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "accepted"})
	}
}

// rejectHandler (POST /api/assets/{id}/reject): admin.
func rejectHandler(rv ReviewPort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := rv.Reject(r.Context(), id); err != nil {
			if errors.Is(err, errReviewConflict) {
				http.Error(w, "asset not pending_acceptance", http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "rejected"})
	}
}

// regenerateHandler (POST /api/assets/{id}/regenerate): admin. Body = edited
// prompt. No run_event (same reason as acceptHandler); the spawned asset todo's
// own todo_ready is emitted by the worker's emitNewlyReady on the next claim.
func regenerateHandler(rv ReviewPort, lib AssetLibrary, cs CostStore, quota int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		orgID, err := lib.OrgIDForAsset(r.Context(), id)
		if err == nil {
			if over, qerr := quotaExceeded(r.Context(), cs, quota, orgID); qerr != nil {
				http.Error(w, qerr.Error(), http.StatusInternalServerError)
				return
			} else if over {
				http.Error(w, "generation quota exceeded for org", http.StatusTooManyRequests)
				return
			}
		}
		newAssetID, todoID, err := rv.Regenerate(r.Context(), id, req.Prompt)
		if err != nil {
			if errors.Is(err, errReviewConflict) {
				http.Error(w, "asset not pending_acceptance", http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"newAssetId": newAssetID, "todoId": todoID, "status": "generating"})
	}
}

// libraryHandler (GET /api/orgs/{org}/assets): viewer+. Keyset-paginated search.
func libraryHandler(lib AssetLibrary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := 0
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		items, next, err := lib.Library(r.Context(), assets.LibraryFilter{
			OrgID: r.PathValue("org"), ProjectID: q.Get("project"), Type: q.Get("type"),
			Status: q.Get("status"), Style: q.Get("style"), Tag: q.Get("tag"),
			Limit: limit, Cursor: q.Get("cursor"),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]assets.Asset, 0, len(items))
		out = append(out, items...)
		writeJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": next})
	}
}

// getAssetHandler (GET /api/assets/{id}): viewer+. Includes version lineage.
func getAssetHandler(lib AssetLibrary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		a, err := lib.Get(r.Context(), id)
		if errors.Is(err, assets.ErrNotFound) {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hist, _ := lib.VersionHistory(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"asset": a, "versions": hist})
	}
}

// assetContentHandler (GET /api/assets/{id}/content): viewer+. 302 to signed URL.
// 按 asset 所属 org 路由对象存储后再签名 (per-org → global → 内置默认)，多租户各自
// 落在自己的 bucket/store 上。
func assetContentHandler(lib AssetLibrary, router BlobRouter, ps ProjectReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		a, err := lib.Get(r.Context(), id)
		if errors.Is(err, assets.ErrNotFound) {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Provider-hosted URL-only assets redirect to the external URL directly.
		if a.BlobKey == "" && a.URL != "" {
			http.Redirect(w, r, a.URL, http.StatusFound)
			return
		}
		orgID, err := lib.OrgIDForAsset(r.Context(), a.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Resolve the blob store from the asset's WRITE-TIME backend identity, NOT
		// the project's current storage_mode: after an org switches storage type, a
		// historical asset's bytes still live in the old backend. token=="" is a
		// legacy (pre-m15) row — fall back to current mode until it's backfilled.
		var bs blob.BlobStore
		if a.StorageConfigID == "" {
			proj, perr := ps.Get(r.Context(), a.ProjectID)
			if perr != nil {
				http.Error(w, perr.Error(), http.StatusInternalServerError)
				return
			}
			bs, err = router.BlobStoreForMode(r.Context(), orgID, proj.StorageMode)
		} else {
			bs, err = router.BlobStoreForConfigID(r.Context(), orgID, a.StorageConfigID)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type ctxReader interface {
			ReadKey(ctx context.Context, key string) ([]byte, string, error)
		}
		if rdr, ok := bs.(ctxReader); ok {
			data, ct, err := rdr.ReadKey(r.Context(), a.BlobKey)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "sandbox")
			_, _ = w.Write(data)
			return
		}

		signed, err := bs.SignedURL(r.Context(), a.BlobKey, signedURLTTL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, signed, http.StatusFound)
	}
}

// blobHandler (GET /api/blob/{key...}): NO auth middleware — access is gated by
// the HMAC signature + expiry in the query (spec §10). Verifies then serves bytes.
func blobHandler(srv BlobServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := srv.KeyFromPath(r.URL.Path)
		exp := r.URL.Query().Get("exp")
		sig := r.URL.Query().Get("sig")
		if err := srv.Verify(key, exp, sig); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		data, ct, err := srv.ReadKey(key)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		// Blobs are provider-fetched bytes served on the app origin: forbid MIME
		// sniffing and sandbox active content (e.g. scripted SVG) — <img> rendering
		// is unaffected.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox")
		_, _ = w.Write(data)
	}
}

// createModelConfigHandler (POST /api/orgs/{org}/model-configs): admin.
func createModelConfigHandler(ms ModelStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind      string          `json:"kind"`
			Provider  string          `json:"provider"`
			Model     string          `json:"model"`
			BaseURL   string          `json:"baseUrl"` // 可选 per-config endpoint (openai-compatible)
			APIKey    string          `json:"apiKey"`  // 可选明文 key；加密入库，绝不回显
			Enabled   bool            `json:"enabled"`
			IsDefault bool            `json:"isDefault"`
			Params    json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Provider == "" || req.Model == "" {
			http.Error(w, "bad request: provider+model required", http.StatusBadRequest)
			return
		}
		mc, err := ms.Create(r.Context(), models.CreateInput{
			OrgID: r.PathValue("org"), Kind: req.Kind, Provider: req.Provider, Model: req.Model,
			Enabled: req.Enabled, IsDefault: req.IsDefault, BaseURL: req.BaseURL, APIKey: req.APIKey, Params: req.Params,
		})
		if err != nil {
			// ErrSecretParam (params 夹带凭据) 与 ErrEncUnavailable (要存 key 但未配
			// STUDIO_CONFIG_ENC_KEY) 都是客户端可纠正的 400——后者带原文，UI 据此提示
			// 管理员配置加密主密钥。其它错误才 500。响应永不回显 apiKey。
			if errors.Is(err, models.ErrSecretParam) || errors.Is(err, models.ErrEncUnavailable) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, mc)
	}
}

// updateModelConfigHandler (PUT /api/orgs/{org}/model-configs/{id}): admin.
// body 同 create（含可选 baseUrl/apiKey；apiKey 空=保留既有 key、非空=替换）。
// ErrNotFound→404，ErrSecretParam/ErrEncUnavailable→400，成功→200 ModelConfig（绝不回显 key）。
func updateModelConfigHandler(ms ModelStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind      string          `json:"kind"`
			Provider  string          `json:"provider"`
			Model     string          `json:"model"`
			BaseURL   string          `json:"baseUrl"` // 可选 per-config endpoint (openai-compatible)
			APIKey    string          `json:"apiKey"`  // 空=保留既有 key；非空=重新加密替换，绝不回显
			Enabled   bool            `json:"enabled"`
			IsDefault bool            `json:"isDefault"`
			Params    json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Provider == "" || req.Model == "" {
			http.Error(w, "bad request: provider+model required", http.StatusBadRequest)
			return
		}
		mc, err := ms.Update(r.Context(), r.PathValue("id"), r.PathValue("org"), models.UpdateInput{
			Kind: req.Kind, Provider: req.Provider, Model: req.Model,
			Enabled: req.Enabled, IsDefault: req.IsDefault, BaseURL: req.BaseURL, APIKey: req.APIKey, Params: req.Params,
		})
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				http.Error(w, "model config not found", http.StatusNotFound)
				return
			}
			// ErrSecretParam / ErrEncUnavailable 是客户端可纠正的 400（同 create）。响应永不回显 apiKey。
			if errors.Is(err, models.ErrSecretParam) || errors.Is(err, models.ErrEncUnavailable) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, mc)
	}
}

// deleteModelConfigHandler (DELETE /api/orgs/{org}/model-configs/{id}): admin.
// ErrNotFound→404，成功→200 {ok:true}（匹配仓内 writeJSON 约定，无 204 先例）。
func deleteModelConfigHandler(ms ModelStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ms.Delete(r.Context(), r.PathValue("id"), r.PathValue("org")); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				http.Error(w, "model config not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// listModelConfigsHandler (GET /api/orgs/{org}/model-configs): admin.
func listModelConfigsHandler(ms ModelStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := ms.ListByOrg(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
	}
}

// parseTimeRange reads optional RFC3339 from/to query params. Malformed values
// are a 400 (not silently ignored — M1 carry lesson: don't swallow parse errors).
func parseTimeRange(r *http.Request) (from, to time.Time, err error) {
	if v := r.URL.Query().Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("bad from: %w", err)
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("bad to: %w", err)
		}
	}
	return from, to, nil
}

// orgCostHandler (GET /api/orgs/{org}/cost?from=&to=): admin. 时间范围聚合.
func orgCostHandler(cs CostStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to, err := parseTimeRange(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		agg, err := cs.ByOrgBetween(r.Context(), r.PathValue("org"), from, to)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, agg)
	}
}

// projectCostHandler (GET /api/projects/{id}/cost?from=&to=): admin.
func projectCostHandler(cs CostStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to, err := parseTimeRange(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		agg, err := cs.ByProjectBetween(r.Context(), r.PathValue("id"), from, to)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, agg)
	}
}

// orgCostProjectsHandler (GET /api/orgs/{org}/cost/projects?from=&to=): admin.
// Per-project rollup (UI 按项目成本条).
func orgCostProjectsHandler(cs CostStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to, err := parseTimeRange(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, err := cs.PerProjectByOrg(r.Context(), r.PathValue("org"), from, to)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// memberCostDTO 是「按成员」成本条的一行：userId 为权威身份，email 由 actorEmailResolver
// 解析（best-effort，解析不到留空）。userId 空 = 未归属（历史）；unpriced 表示该成员的
// 生成里存在未定价（cost_micros=0 但确有 token/图片/时长的行无法在聚合层精确判定，这里沿用
// 与按项目一致的口径：成本为 0 但有生成即视为可能未定价，交前端 badge 提示）。
type memberCostDTO struct {
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	CostMicros  int64  `json:"costMicros"`
	Tokens      int    `json:"tokens"`
	ImageCount  int    `json:"imageCount"`
	Generations int    `json:"generations"`
	Unpriced    bool   `json:"unpriced"`
}

// orgCostMembersHandler (GET /api/orgs/{org}/cost/by-member?from=&to=): admin.
// Per-member rollup (UI 按成员成本条). actor_user_id 经 ActorEmail 解析成 email；空
// actor（未归属）email 留空，前端显示「未归属（历史）」。email 解析器缺失 (nil) 时全部
// 留空，成本口径不受影响（actor_user_id 才是权威身份）。
func orgCostMembersHandler(cs CostStore, er actorEmailResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to, err := parseTimeRange(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rows, err := cs.PerActorByOrg(r.Context(), r.PathValue("org"), from, to)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items := make([]memberCostDTO, 0, len(rows))
		for _, a := range rows {
			email := ""
			if er != nil && a.ActorUserID != "" {
				if e, eerr := er.ActorEmail(r.Context(), a.ActorUserID); eerr != nil {
					slog.Warn("cost: resolve actor email failed", "actor", a.ActorUserID, "err", eerr)
				} else {
					email = e
				}
			}
			items = append(items, memberCostDTO{
				UserID:      a.ActorUserID,
				Email:       email,
				CostMicros:  a.CostMicros,
				Tokens:      a.Tokens,
				ImageCount:  a.ImageCount,
				Generations: a.Generations,
				Unpriced:    a.CostMicros == 0 && a.Generations > 0,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// planCostHandler (GET /api/projects/{id}/plans/{planId}/cost): admin.
// 一次 run 的 token/成本汇总 + 按 todo（节点）分解。org 隔离由 project scope 中间件
// 保证；ByPlan 的 project_id 过滤再兜住跨项目 planId 猜测（只会得到零报表）。
func planCostHandler(cs CostStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pc, err := cs.ByPlan(r.Context(), r.PathValue("id"), r.PathValue("planId"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, pc)
	}
}

// orgGenerationsHandler (GET /api/orgs/{org}/generations?limit=&cursor=): admin.
// Recent usage-detail rows (UI 用量明细表), keyset-paginated: cursor 缺省 = 首页
//（与现状兼容），响应带 next_cursor（空串 = 到底，同 libraryHandler 信封）。
func orgGenerationsHandler(cs CostStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		items, next, err := cs.RecentByOrg(r.Context(), r.PathValue("org"), limit, r.URL.Query().Get("cursor"))
		if errors.Is(err, cost.ErrBadCursor) {
			http.Error(w, "bad request: invalid cursor", http.StatusBadRequest)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
	}
}
