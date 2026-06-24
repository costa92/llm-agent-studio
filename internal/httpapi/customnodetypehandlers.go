package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
)

// CustomNodeTypeStore is the registry HTTP surface (satisfied by *customnodetype.Store).
type CustomNodeTypeStore interface {
	List(ctx context.Context, orgID string) ([]customnodetype.CustomNodeType, error)
	Create(ctx context.Context, orgID string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error)
	Update(ctx context.Context, id, orgID string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error)
	Delete(ctx context.Context, id, orgID string) error
	Get(ctx context.Context, id, orgID string) (customnodetype.CustomNodeType, error)
}

type customNodeTypeBody struct {
	Label  string          `json:"label"`
	Color  string          `json:"color"`
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params"`
}

func (b customNodeTypeBody) toInput() customnodetype.UpsertInput {
	return customnodetype.UpsertInput{Label: b.Label, Color: b.Color, Kind: b.Kind, Params: b.Params}
}

func listCustomNodeTypesHandler(s CustomNodeTypeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.List(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []customnodetype.CustomNodeType{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func createCustomNodeTypeHandler(s CustomNodeTypeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b customNodeTypeBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ct, err := s.Create(r.Context(), r.PathValue("org"), b.toInput())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, ct)
	}
}

func updateCustomNodeTypeHandler(s CustomNodeTypeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b customNodeTypeBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		ct, err := s.Update(r.Context(), r.PathValue("id"), r.PathValue("org"), b.toInput())
		if errors.Is(err, customnodetype.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, ct)
	}
}

func deleteCustomNodeTypeHandler(s CustomNodeTypeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := s.Delete(r.Context(), r.PathValue("id"), r.PathValue("org"))
		if errors.Is(err, customnodetype.ErrInUse) {
			http.Error(w, "该类型被工作流节点引用，请先移除引用再删除", http.StatusConflict)
			return
		}
		if errors.Is(err, customnodetype.ErrNotFound) {
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
