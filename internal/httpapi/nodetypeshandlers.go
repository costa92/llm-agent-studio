package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/nodedesc"
)

// nodeTypesHandler (GET /api/orgs/{org}/node-types): viewer+. Merges the static
// built-in node descriptions with the org's custom_node_types rows into one
// catalog the canvas renders from.
//
// Security (spec ★B-A5): a custom row must NEVER shadow a built-in type — built-in
// always wins — and reserved-namespace slugs (studio.*, llm, http, script) are
// dropped, so a malicious/buggy org cannot hijack the canvas. Built-ins are
// emitted first (declared order); customs follow, sorted by Type for stability.
// exprChannel reflects config.ExprChannel: a read-only capability flag the FE
// uses to gate field-level varBindings (B/P5) — field bindings only function when
// the expr channel is ON, so the FE disables the field selector when it is OFF.
func nodeTypesHandler(s CustomNodeTypeStore, exprChannel bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		builtins := nodedesc.Builtins()
		// Base description per kind a custom row may extend.
		baseByKind := map[string]nodedesc.NodeTypeDescription{}
		for _, b := range builtins {
			switch b.Type {
			case "llm", "http", "script":
				baseByKind[b.Type] = b
			}
		}

		rows, err := s.List(r.Context(), r.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		customs := make([]nodedesc.NodeTypeDescription, 0, len(rows))
		for _, row := range rows {
			// Reserved namespace → drop (built-in-wins / no canvas hijack).
			if nodedesc.ReservedNamespace(row.Slug) {
				continue
			}
			base, ok := baseByKind[row.Kind]
			if !ok {
				// Unknown/unsupported base kind → nothing to render against; drop.
				continue
			}
			customs = append(customs, customFromRow(base, row))
		}
		sort.Slice(customs, func(i, j int) bool { return customs[i].Type < customs[j].Type })

		out := make([]nodedesc.NodeTypeDescription, 0, len(builtins)+len(customs))
		out = append(out, builtins...)
		out = append(out, customs...)

		writeJSON(w, http.StatusOK, map[string]any{
			"version":     nodedesc.Version,
			"exprChannel": exprChannel,
			"nodeTypes":   out,
		})
	}
}

// customFromRow projects a custom_node_types row onto its base kind description:
// Type becomes custom:<slug>, Label is the row's, and each row param value is
// projected onto the matching property's Default (best-effort).
//
// The Properties slice is DEEP-COPIED before any Default is set — Builtins()
// shallow-copies the descriptions, so their Properties slices share the package
// global's backing array. Mutating an element in place would corrupt the global
// built-in for every later request. We allocate a fresh []Property and copy.
func customFromRow(base nodedesc.NodeTypeDescription, row customnodetype.CustomNodeType) nodedesc.NodeTypeDescription {
	d := base
	d.Type = "custom:" + row.Slug
	if row.Label != "" {
		d.Label = row.Label
	}

	props := make([]nodedesc.Property, len(base.Properties))
	copy(props, base.Properties)

	// Best-effort: invalid params → no projected defaults.
	var params map[string]json.RawMessage
	if len(row.Params) > 0 {
		if err := json.Unmarshal(row.Params, &params); err != nil {
			params = nil
		}
	}
	for i := range props {
		if v, ok := params[props[i].Name]; ok {
			props[i].Default = v
		}
	}
	d.Properties = props
	return d
}
