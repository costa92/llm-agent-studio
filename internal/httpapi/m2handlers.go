package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/models"
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
}

// AssetLibrary is the asset read/search surface (satisfied by *assets.Store).
type AssetLibrary interface {
	Get(ctx context.Context, id string) (assets.Asset, error)
	VersionHistory(ctx context.Context, id string) ([]assets.Asset, error)
	Library(ctx context.Context, f assets.LibraryFilter) ([]assets.Asset, string, error)
	OrgIDForAsset(ctx context.Context, assetID string) (string, error)
}

// BlobSigner mints signed blob URLs (satisfied by *localfs.Store or the s3 store).
type BlobSigner interface {
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
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
}

// CostStore is the cost aggregation surface (satisfied by *cost.Store).
type CostStore interface {
	ByOrg(ctx context.Context, orgID string) (cost.Aggregate, error)
	ByProject(ctx context.Context, projectID string) (cost.Aggregate, error)
}

const signedURLTTL = 10 * time.Minute

// promptStylesHandler (GET /api/prompt-styles): viewer+.
func promptStylesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"styles": prompt.Styles()})
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

// modelCatalogHandler (GET /api/model-catalog): admin.
func modelCatalogHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"catalog": models.Catalog()})
	}
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
func regenerateHandler(rv ReviewPort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
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
func assetContentHandler(lib AssetLibrary, signer BlobSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := lib.Get(r.Context(), r.PathValue("id"))
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
		signed, err := signer.SignedURL(r.Context(), a.BlobKey, signedURLTTL)
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
			Enabled: req.Enabled, IsDefault: req.IsDefault, Params: req.Params,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, mc)
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

// orgCostHandler (GET /api/orgs/{org}/cost): admin.
func orgCostHandler(cs CostStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agg, err := cs.ByOrg(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, agg)
	}
}

// projectCostHandler (GET /api/projects/{id}/cost): admin.
func projectCostHandler(cs CostStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agg, err := cs.ByProject(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, agg)
	}
}
