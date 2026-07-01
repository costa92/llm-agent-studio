package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/project"
)

// audioExt maps an audio MIME type to a blob-key file extension. The minimax TTS
// adapter emits audio/mpeg (MP3); unknown types default to .mp3 (the only format
// this synchronous lyrics path produces). Deliberately NOT reusing coverhandlers'
// mimeToExt — that one is image-only.
func audioExt(mime string) string {
	switch mime {
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	default:
		return ".mp3"
	}
}

// lyricsAudioHandler (POST /api/projects/{id}/lyrics-audio): editor+.
// Body {planId,text}. Synthesizes a music run's lyrics into speech via the org's
// default audio model, stores the MP3 as an accepted 'lyrics-audio' asset, and
// books the ledger. Synchronous (~1s) — mirrors coverGenerateHandler.
//
// Quota is advisory here (logged, never hard-blocked) — a read-aloud is a one-off.
func lyricsAudioHandler(ps ProjectStore, aw CoverAssetWriter, cg CoverGenerator, br BlobRouter, cs CostStore, quota int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req struct {
			PlanID string `json:"planId"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		text := strings.TrimSpace(req.Text)
		if text == "" {
			http.Error(w, "text required", http.StatusBadRequest)
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

		// Advisory quota: log if over, never block.
		if quota > 0 {
			if over, qerr := quotaExceeded(r.Context(), cs, quota, proj.OrgID); qerr == nil && over {
				slog.Warn("lyrics audio over generation quota (advisory; not blocked)", "org", proj.OrgID, "project", id)
			}
		}

		// CRITICAL GUARD: MediaGeneratorFor falls back to the registry default
		// (an IMAGE generator) when the org has no audio model configured. Without
		// the Kind()=="audio" check, lyrics would be synthesized as an image.
		g := cg.MediaGeneratorFor(r.Context(), proj.OrgID, "audio")
		if g == nil || g.Kind() != "audio" {
			http.Error(w, "no audio model configured for this org", http.StatusBadRequest)
			return
		}

		res, err := g.Generate(r.Context(), generate.GenRequest{Prompt: text})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		created, err := aw.Create(r.Context(), assets.CreateInput{
			ProjectID: id, Type: "audio", Status: "accepted", Tags: []string{"lyrics-audio"},
			Prompt: text, Provider: res.Provider, Model: res.Model,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		blobKey := "assets/" + id + "/" + created.ID + audioExt(res.MimeType)
		// Record which backend the audio bytes land in (serve re-resolves THAT one).
		bs, storageConfigID, err := br.ResolveWriteTarget(r.Context(), proj.OrgID, proj.StorageConfigID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := bs.Put(r.Context(), blobKey, bytes.NewReader(res.Bytes), res.MimeType); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// SetCoverBlob is a generic UPDATE assets SET blob_key,url,storage_config_id
		// — fine for an audio asset (nothing cover-specific about the write).
		if err := aw.SetCoverBlob(r.Context(), created.ID, blobKey, res.URL, storageConfigID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := cs.Record(r.Context(), cost.Generation{
			ProjectID: id, AssetID: created.ID, Kind: "audio",
			Provider: res.Provider, Model: res.Model, LatencyMS: res.LatencyMS,
		}); err != nil {
			// Ledger write failure must not strand a stored asset — log + 200.
			slog.Warn("lyrics audio: ledger record failed", "project", id, "asset", created.ID, "err", err)
		}

		writeJSON(w, http.StatusOK, map[string]any{"audioAssetId": created.ID})
	}
}
