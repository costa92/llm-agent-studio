package nodedesc

import (
	"encoding/json"
	"fmt"
)

// DescByKind selects a builtin description by its base kind (llm/http/script) and
// the node's pinned TypeVersion. typeVersion 0 (omitempty / old node) defaults to
// the current Version. A typeVersion that matches no known description version
// returns ok=false — callers MUST fail closed (spec §4.3 / D-1): never silently
// fall back, because that would select the wrong danger classification.
func DescByKind(kind string, typeVersion int) (NodeTypeDescription, bool) {
	if typeVersion == 0 {
		typeVersion = Version
	}
	if typeVersion != Version {
		return NodeTypeDescription{}, false
	}
	for _, d := range builtins {
		if d.Type == kind {
			return d, true
		}
	}
	return NodeTypeDescription{}, false
}

// MergeOverlay merges a per-node parameters overlay onto the registry base params,
// keyed by the description. inject_keys = {description-known property names} ∩
// {non-RegistryOnly}. Unknown keys are dropped (M2). RegistryOnly keys are
// default-denied — base wins (M1/M4). A nil/empty overlay returns base unchanged.
// Value legality (enums, cross-field) is NOT checked here — callers run the full
// validate* on the result (spec §4.2 / §6.3, m1).
func MergeOverlay(base, overlay json.RawMessage, desc NodeTypeDescription) (json.RawMessage, error) {
	var merged map[string]json.RawMessage
	if len(base) == 0 {
		merged = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(base, &merged); err != nil {
		return nil, fmt.Errorf("nodedesc: merge base: %w", err)
	}
	if len(overlay) == 0 {
		return base, nil
	}
	var ov map[string]json.RawMessage
	if err := json.Unmarshal(overlay, &ov); err != nil {
		return nil, fmt.Errorf("nodedesc: merge overlay: %w", err)
	}
	injectable := map[string]bool{}
	for _, p := range desc.Properties {
		if p.Constraints != nil && p.Constraints.RegistryOnly {
			continue // default-deny RegistryOnly
		}
		injectable[p.Name] = true // description-known AND non-RegistryOnly
	}
	for k, v := range ov {
		if injectable[k] {
			merged[k] = v
		}
		// else: unknown key (drop-unknown) OR RegistryOnly (default-deny) → ignored.
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("nodedesc: merge marshal: %w", err)
	}
	return out, nil
}
