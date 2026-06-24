package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/orgsecret"
)

// OrgSecretStore is the org_secrets HTTP surface (satisfied by *orgsecret.Store).
// It deliberately does NOT expose Resolve — plaintext never reaches an HTTP handler.
type OrgSecretStore interface {
	List(ctx context.Context, orgID string) ([]orgsecret.OrgSecret, error)
	Create(ctx context.Context, orgID string, in orgsecret.UpsertInput) (orgsecret.OrgSecret, error)
	Update(ctx context.Context, orgID, name string, in orgsecret.UpsertInput) (orgsecret.OrgSecret, error)
	Delete(ctx context.Context, orgID, name string) error
}

type orgSecretBody struct {
	Name  string `json:"name"`
	Value string `json:"value"` // write-only：响应里绝不回显
}

func (b orgSecretBody) toInput() orgsecret.UpsertInput {
	return orgsecret.UpsertInput{Name: b.Name, Value: b.Value}
}

func listOrgSecretsHandler(s OrgSecretStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.List(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []orgsecret.OrgSecret{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func createOrgSecretHandler(s OrgSecretStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b orgSecretBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		sec, err := s.Create(r.Context(), r.PathValue("org"), b.toInput())
		if errors.Is(err, orgsecret.ErrEncUnavailable) {
			http.Error(w, "未配置加密主密钥 (STUDIO_CONFIG_ENC_KEY)，无法存储密钥", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, sec) // sec is {id, orgId, name, hasValue} only
	}
}

func updateOrgSecretHandler(s OrgSecretStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b orgSecretBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		sec, err := s.Update(r.Context(), r.PathValue("org"), r.PathValue("name"), b.toInput())
		if errors.Is(err, orgsecret.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, orgsecret.ErrEncUnavailable) {
			http.Error(w, "未配置加密主密钥 (STUDIO_CONFIG_ENC_KEY)，无法存储密钥", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, sec)
	}
}

func deleteOrgSecretHandler(s OrgSecretStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.Delete(r.Context(), r.PathValue("org"), r.PathValue("name"))
		if errors.Is(err, orgsecret.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
