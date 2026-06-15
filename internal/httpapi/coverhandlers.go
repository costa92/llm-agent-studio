package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/project"
)

// mimeToExt maps an image MIME type to a file extension for the cover blob key.
// Unknown types default to .png (covers are always images; the allowlist upstream
// keeps this to the three accepted types in practice).
func mimeToExt(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

// coverGenerateHandler (POST /api/projects/{id}/cover/generate): editor+.
// Body {prompt?,provider?,model?}. Generates an image, stores it as an accepted
// asset tagged 'cover', links it as the project cover, and books the ledger.
// Quota is advisory here (logged, never hard-blocked) — a cover is a one-off.
func coverGenerateHandler(ps ProjectStore, aw CoverAssetWriter, cg CoverGenerator, br BlobRouter, cs CostStore, quota int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req struct {
			Prompt   string `json:"prompt"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		proj, err := ps.Get(r.Context(), id)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		prompt := req.Prompt
		if prompt == "" {
			prompt = proj.Name + " " + proj.Style
		}

		// Advisory quota: log if over, never block.
		if quota > 0 {
			if over, qerr := quotaExceeded(r.Context(), cs, quota, proj.OrgID); qerr == nil && over {
				slog.Warn("cover generate over generation quota (advisory; not blocked)", "org", proj.OrgID, "project", id)
			}
		}

		g := cg.MediaGeneratorForNamed(r.Context(), proj.OrgID, "image", req.Provider, req.Model)
		if g == nil {
			g = cg.MediaGeneratorFor(r.Context(), proj.OrgID, "image")
		}
		if g == nil {
			http.Error(w, "no image generator available", http.StatusInternalServerError)
			return
		}

		res, err := g.Generate(r.Context(), generate.GenRequest{Prompt: prompt, N: 1, Size: "1024x1024"})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		created, err := aw.Create(r.Context(), assets.CreateInput{
			ProjectID: id, Type: "image", Status: "accepted", Tags: []string{"cover"},
			Prompt: prompt, Style: proj.Style, Provider: res.Provider, Model: res.Model,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		blobKey := "assets/" + id + "/" + created.ID + mimeToExt(res.MimeType)
		bs, err := br.BlobStoreFor(r.Context(), proj.OrgID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := bs.Put(r.Context(), blobKey, bytes.NewReader(res.Bytes), res.MimeType); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := aw.SetCoverBlob(r.Context(), created.ID, blobKey, res.URL); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := ps.SetCover(r.Context(), id, created.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := cs.Record(r.Context(), cost.Generation{
			ProjectID: id, AssetID: created.ID, Kind: "image",
			Provider: res.Provider, Model: res.Model,
			ImageCount: res.ImageCount, LatencyMS: res.LatencyMS,
		}); err != nil {
			// Ledger write failure must not strand a successfully-set cover — log + 200.
			slog.Warn("cover generate: ledger record failed", "project", id, "asset", created.ID, "err", err)
		}

		writeJSON(w, http.StatusOK, map[string]any{"coverAssetId": created.ID})
	}
}

const coverMaxUpload = 5 << 20 // 5 MiB

// coverUploadHandler (POST /api/projects/{id}/cover/upload): editor+. multipart
// "file". Sniffs the content type, allowlists png/jpeg/webp, stores the file as
// an accepted 'cover' asset, and links it as the project cover.
func coverUploadHandler(ps ProjectStore, aw CoverAssetWriter, br BlobRouter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		r.Body = http.MaxBytesReader(w, r.Body, coverMaxUpload)
		if err := r.ParseMultipartForm(coverMaxUpload); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				http.Error(w, "file too large (max 5 MiB)", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "bad multipart form", http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Sniff the first 512 bytes; combine with the multipart header's declared type.
		sniff := make([]byte, 512)
		n, err := io.ReadFull(file, sniff)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sniff = sniff[:n]
		ct := http.DetectContentType(sniff)
		if !coverTypeAllowed(ct) {
			// Fall back to the client-declared type before rejecting.
			if hdr != nil {
				if hct := hdr.Header.Get("Content-Type"); coverTypeAllowed(hct) {
					ct = hct
				}
			}
		}
		if !coverTypeAllowed(ct) {
			http.Error(w, "unsupported image type (png/jpeg/webp only)", http.StatusBadRequest)
			return
		}

		proj, err := ps.Get(r.Context(), id)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		created, err := aw.Create(r.Context(), assets.CreateInput{
			ProjectID: id, Type: "image", Status: "accepted", Tags: []string{"cover"},
			Style: proj.Style, Provider: "upload",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		blobKey := "assets/" + id + "/" + created.ID + mimeToExt(ct)
		bs, err := br.BlobStoreFor(r.Context(), proj.OrgID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// We consumed up to 512 bytes for sniffing — re-join them with the rest.
		full := io.MultiReader(bytes.NewReader(sniff), file)
		if err := bs.Put(r.Context(), blobKey, full, ct); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := aw.SetCoverBlob(r.Context(), created.ID, blobKey, ""); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := ps.SetCover(r.Context(), id, created.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"coverAssetId": created.ID})
	}
}

func coverTypeAllowed(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

// coverSetHandler (PUT /api/projects/{id}/cover): editor+. Body {assetId}. Picks
// an EXISTING project asset as the cover (or clears it with ""). The chosen asset
// must belong to this project (else 400).
func coverSetHandler(ps ProjectStore, ag AssetLibrary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req struct {
			AssetID string `json:"assetId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.AssetID == "" {
			if err := ps.SetCover(r.Context(), id, ""); err != nil {
				if errors.Is(err, project.ErrNotFound) {
					http.Error(w, "project not found", http.StatusNotFound)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"coverAssetId": ""})
			return
		}
		a, err := ag.Get(r.Context(), req.AssetID)
		if errors.Is(err, assets.ErrNotFound) {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if a.ProjectID != id {
			http.Error(w, "asset does not belong to this project", http.StatusBadRequest)
			return
		}
		if err := ps.SetCover(r.Context(), id, req.AssetID); err != nil {
			if errors.Is(err, project.ErrNotFound) {
				http.Error(w, "project not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"coverAssetId": req.AssetID})
	}
}

// coverOptionsHandler (GET /api/projects/{id}/cover/options): viewer+. Lists the
// project's existing image assets so the UI can offer "pick an existing image".
func coverOptionsHandler(ps ProjectStore, ag AssetLibrary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		orgID, err := ps.OrgIDForProject(r.Context(), id)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, _, err := ag.Library(r.Context(), assets.LibraryFilter{OrgID: orgID, ProjectID: id, Type: "image"})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]assets.Asset, 0, len(items))
		out = append(out, items...)
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}
