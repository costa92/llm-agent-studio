// Package planner turns a custom workflow node DAG into a validated, persisted
// todo graph — no LLM. graph.go = the node-type whitelist + custom-type
// predicate; planner.go = PlanCustom validation + persistence. (The LLM
// auto-planner that parsed and validated a graph from a project brief was
// removed with the workflow-only pivot.)
package planner

import (
	"strings"
	"sync"

	"github.com/costa92/llm-agent-studio/internal/builtinnode"
)

var (
	typesMu          sync.RWMutex
	whitelistedTypes = builtinnode.Types()
)

// RegisterType registers a custom task type with the planner's whitelist.
func RegisterType(typ string) {
	typesMu.Lock()
	defer typesMu.Unlock()
	whitelistedTypes[typ] = true
}

func isTypeAllowed(typ string) bool {
	typesMu.RLock()
	defer typesMu.RUnlock()
	return whitelistedTypes[typ]
}

// isCustomType reports whether typ is a user-defined custom node type
// (prefix "custom:" with a non-empty slug). Custom types are accepted by
// ValidateCustomGraph (save path) but refused at run time (no executor yet).
// The slug is case-sensitive and not trimmed, so the frontend must mirror the
// exact byte sequence (e.g. "custom:translate" ≠ "custom:Translate").
func isCustomType(typ string) bool {
	return strings.HasPrefix(typ, "custom:") && len(typ) > len("custom:")
}
