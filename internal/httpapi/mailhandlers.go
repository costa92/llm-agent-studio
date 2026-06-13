package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/mailconfig"
)

type MailConfigStore interface {
	UpsertGlobal(ctx context.Context, in mailconfig.UpsertInput) error
	GetGlobal(ctx context.Context) (mailconfig.MailConfig, error)
}

type mailConfigWriteBody struct {
	SMTPHost string `json:"smtpHost"`
	SMTPPort int    `json:"smtpPort"`
	SMTPUser string `json:"smtpUser"`
	SMTPFrom string `json:"smtpFrom"`
	SMTPPass string `json:"smtpPass"`
	Enabled  bool   `json:"enabled"`
}

func (b mailConfigWriteBody) toInput() mailconfig.UpsertInput {
	return mailconfig.UpsertInput{
		SMTPHost: b.SMTPHost,
		SMTPPort: b.SMTPPort,
		SMTPUser: b.SMTPUser,
		SMTPFrom: b.SMTPFrom,
		SMTPPass: b.SMTPPass,
		Enabled:  b.Enabled,
	}
}

func getGlobalMailConfigHandler(store MailConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := store.GetGlobal(r.Context())
		if errors.Is(err, mailconfig.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"config": nil})
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"config": cfg})
	}
}

func putGlobalMailConfigHandler(store MailConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body mailConfigWriteBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.SMTPHost == "" {
			http.Error(w, "smtpHost is required", http.StatusBadRequest)
			return
		}
		if body.SMTPPort <= 0 {
			body.SMTPPort = 587
		}
		err := store.UpsertGlobal(r.Context(), body.toInput())
		if errors.Is(err, mailconfig.ErrEncUnavailable) {
			http.Error(w, err.Error(), http.StatusPreconditionFailed)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Retrieve and return the updated config
		cfg, err := store.GetGlobal(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}
