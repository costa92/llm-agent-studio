package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/prompt"
)

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
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Content == "" {
			http.Error(w, "name and content are required", http.StatusBadRequest)
			return
		}
		p, err := s.Create(r.Context(), org, req.Name, req.Content, req.Style)
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
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || req.Content == "" {
			http.Error(w, "name and content are required", http.StatusBadRequest)
			return
		}
		p, err := s.Update(r.Context(), id, org, req.Name, req.Content, req.Style)
		if err != nil {
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
		w.WriteHeader(http.StatusNoContent)
	}
}
