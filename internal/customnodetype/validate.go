package customnodetype

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateParams runs the kind's full save-time/runtime validator on an arbitrary
// flat param blob (NOT bound to UpsertInput). The single source of truth for value
// legality, reused at: registry save (store.go), per-node merge (httpapi resolve),
// and worker run-time revalidation. llm has no hardcoded checks (any valid JSON).
func ValidateParams(kind string, params json.RawMessage) error {
	if len(params) == 0 || !json.Valid(params) {
		return fmt.Errorf("customnodetype: params must be valid JSON")
	}
	switch kind {
	case "http":
		return validateHTTPParams(params)
	case "script":
		return validateScriptParams(params)
	default:
		return nil
	}
}

// validateHTTPParams enforces the http kind's save-time rules (spec 必做项 #5):
// method enum; url required + static literal (no {{...}}); {{secret:}} only in
// header values (never url/body); outputFormat ∈ text|json.
func validateHTTPParams(raw json.RawMessage) error {
	var p struct {
		Method       string            `json:"method"`
		URL          string            `json:"url"`
		Headers      map[string]string `json:"headers"`
		BodyTemplate string            `json:"bodyTemplate"`
		OutputFormat string            `json:"outputFormat"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("customnodetype: http params: %w", err)
	}
	if !httpMethods[p.Method] {
		return fmt.Errorf("customnodetype: http method %q invalid (GET|POST|PUT|PATCH|DELETE)", p.Method)
	}
	if strings.TrimSpace(p.URL) == "" {
		return fmt.Errorf("customnodetype: http url required")
	}
	if strings.Contains(p.URL, "{{") {
		return fmt.Errorf("customnodetype: http url must be a static literal (no {{...}} templates)")
	}
	if secretRefRe.MatchString(p.BodyTemplate) {
		return fmt.Errorf("customnodetype: {{secret:...}} not allowed in bodyTemplate (headers only)")
	}
	for _, v := range p.Headers {
		_ = v // {{secret:}} IS allowed in header values; no per-value rejection here.
	}
	if p.OutputFormat != "" && p.OutputFormat != "text" && p.OutputFormat != "json" {
		return fmt.Errorf("customnodetype: http outputFormat %q invalid (text|json)", p.OutputFormat)
	}
	return nil
}

// validateScriptParams enforces the script kind's save-time rules: code
// required; outputFormat ∈ text|json; {{secret:}} forbidden (D1 — Starlark has
// no network, an injected secret is a pure exfil oracle).
func validateScriptParams(raw json.RawMessage) error {
	var p struct {
		Code         string `json:"code"`
		OutputFormat string `json:"outputFormat"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("customnodetype: script params: %w", err)
	}
	if strings.TrimSpace(p.Code) == "" {
		return fmt.Errorf("customnodetype: script code required")
	}
	if secretRefRe.MatchString(p.Code) {
		return fmt.Errorf("customnodetype: {{secret:...}} not allowed in script code")
	}
	if p.OutputFormat != "" && p.OutputFormat != "text" && p.OutputFormat != "json" {
		return fmt.Errorf("customnodetype: script outputFormat %q invalid (text|json)", p.OutputFormat)
	}
	return nil
}
