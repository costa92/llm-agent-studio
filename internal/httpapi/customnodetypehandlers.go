package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"
	authzrole "github.com/costa92/llm-agent-authz/role"
	"github.com/costa92/llm-agent-studio/internal/customnodetype"
)

// CustomNodeTypeStore is the registry HTTP surface (satisfied by *customnodetype.Store).
type CustomNodeTypeStore interface {
	List(ctx context.Context, orgID string) ([]customnodetype.CustomNodeType, error)
	Create(ctx context.Context, orgID string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error)
	Upsert(ctx context.Context, orgID string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error)
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

var httpSecretRefRe = regexp.MustCompile(`\{\{\s*secret:`)

// bodyBearsSecret reports whether a custom-node-type body is an http type whose
// headers reference {{secret:...}} (so create/update needs roleAdmin, spec 裁决 #2).
func bodyBearsSecret(b customNodeTypeBody) bool {
	if b.Kind != "http" || len(b.Params) == 0 {
		return false
	}
	var p struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(b.Params, &p); err != nil {
		return false
	}
	for _, v := range p.Headers {
		if httpSecretRefRe.MatchString(v) {
			return true
		}
	}
	return false
}

// requireAdminForSecret enforces caller AtLeast(roleAdmin) when the body bears a
// secret. The middleware verifies editor; the role itself is NOT in ctx, so we
// resolve it here (mirrors RequireScopeRole). Returns false + writes 403 on
// insufficient role; true to proceed.
func requireAdminForSecret(w http.ResponseWriter, r *http.Request, rr authzhttp.RoleResolver, b customNodeTypeBody) bool {
	if !bodyBearsSecret(b) {
		return true
	}
	if rr == nil {
		http.Error(w, "secret-bearing types require admin (role resolver unavailable)", http.StatusForbidden)
		return false
	}
	org := r.PathValue("org")
	uid := authzhttp.UserID(r.Context())
	eff, err := rr.ResolveRole(r.Context(), uid, org, "org", "")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	if !eff.AtLeast(authzrole.RoleAdmin) {
		http.Error(w, "含密钥引用的 HTTP 类型需要管理员权限", http.StatusForbidden)
		return false
	}
	return true
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

func createCustomNodeTypeHandler(s CustomNodeTypeStore, rr authzhttp.RoleResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b customNodeTypeBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !requireAdminForSecret(w, r, rr, b) {
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

func updateCustomNodeTypeHandler(s CustomNodeTypeStore, rr authzhttp.RoleResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b customNodeTypeBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !requireAdminForSecret(w, r, rr, b) {
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
