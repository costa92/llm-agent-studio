package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/prompt"
)

// promptPresetsHandler GET /api/prompt-presets — auth-only. Returns the built-in
// basic prompt presets (code-defined, read-only) selectable in workflow nodes
// when an org has no prompt-library entries of its own.
func promptPresetsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": prompt.BasicPrompts()})
	}
}

func listPromptsHandler(s *prompt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := r.PathValue("org")
		list, err := s.ListByOrg(r.Context(), org)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Envelope is {"items": [...]} per project patterns for lists without cursor pagination
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
	}
}

func createPromptHandler(s *prompt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := r.PathValue("org")
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
			Style   string `json:"style"`
			Kind    string `json:"kind"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Content == "" {
			http.Error(w, "name and content are required", http.StatusBadRequest)
			return
		}
		p, err := s.Create(r.Context(), org, req.Name, req.Content, req.Style, req.Kind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, p)
	}
}

func updatePromptHandler(s *prompt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := r.PathValue("org")
		id := r.PathValue("id")
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
			Style   string `json:"style"`
			Kind    string `json:"kind"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Content == "" {
			http.Error(w, "name and content are required", http.StatusBadRequest)
			return
		}
		p, err := s.Update(r.Context(), id, org, req.Name, req.Content, req.Style, req.Kind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func setPromptDefaultHandler(s *prompt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := r.PathValue("org")
		id := r.PathValue("id")
		p, err := s.SetDefault(r.Context(), id, org)
		if err != nil {
			if errors.Is(err, prompt.ErrNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func deletePromptHandler(s *prompt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := r.PathValue("org")
		id := r.PathValue("id")
		err := s.Delete(r.Context(), id, org)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// 与其余 delete handler（workflow/model-config/storage/member/platform）一致返回
		// 200 {ok:true}：前端 apiJSON 对 204 空体调 res.json() 会抛解析错，删除成功却误报失败。
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
