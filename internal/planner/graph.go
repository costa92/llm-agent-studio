// Package planner turns a project brief into a validated todo graph via an LLM
// (graph.go = pure validation/fallback; planner.go = LLM + persistence). Spec
// §7.1: type whitelist + acyclic + must-contain-script; malformed → default
// pipeline fallback.
package planner

import (
	"encoding/json"
	"fmt"
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

// Node is one planner-emitted todo node.
type Node struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	DependsOn []string `json:"dependsOn"`
}

// Graph is the planner's todo graph.
type Graph struct {
	Nodes []Node `json:"nodes"`
}

// ParseGraph tolerantly extracts a Graph from an LLM reply (R1). Strips fences
// and surrounding prose. Returns an error if no JSON object is present.
func ParseGraph(s string) (Graph, error) {
	raw, err := extractObject(s)
	if err != nil {
		return Graph{}, err
	}
	var g Graph
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return Graph{}, fmt.Errorf("planner: unmarshal graph: %w", err)
	}
	return g, nil
}

// Validate enforces spec §7.1: non-empty, unique ids, whitelisted types, every
// dependency resolves, at least one script node, and the graph is acyclic.
func Validate(g Graph) error {
	if len(g.Nodes) == 0 {
		return fmt.Errorf("planner: empty graph")
	}
	ids := make(map[string]bool, len(g.Nodes))
	hasScript := false
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("planner: node with empty id")
		}
		if ids[n.ID] {
			return fmt.Errorf("planner: duplicate node id %q", n.ID)
		}
		ids[n.ID] = true
		if !isTypeAllowed(n.Type) {
			return fmt.Errorf("planner: node %q has non-whitelisted type %q", n.ID, n.Type)
		}
		if n.Type == "script" {
			hasScript = true
		}
	}
	if !hasScript {
		return fmt.Errorf("planner: graph must contain at least one script node")
	}
	for _, n := range g.Nodes {
		for _, dep := range n.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("planner: node %q depends on unknown node %q", n.ID, dep)
			}
		}
	}
	if err := checkAcyclic(g); err != nil {
		return err
	}
	return nil
}

// checkAcyclic runs a DFS cycle detection over the dependency edges.
func checkAcyclic(g Graph) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.Nodes))
	deps := make(map[string][]string, len(g.Nodes))
	for _, n := range g.Nodes {
		deps[n.ID] = n.DependsOn
	}
	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		for _, dep := range deps[id] {
			switch color[dep] {
			case gray:
				return fmt.Errorf("planner: dependency cycle at %q→%q", id, dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for _, n := range g.Nodes {
		if color[n.ID] == white {
			if err := visit(n.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// DefaultPipeline is the fallback graph (spec §7.1): script → storyboard.
// M1 stops at storyboard (asset is M2); the default pipeline is intentionally
// the two text stages M1 can actually run.
func DefaultPipeline() Graph {
	return Graph{Nodes: []Node{
		{ID: "script-1", Type: "script", DependsOn: nil},
		{ID: "storyboard-1", Type: "storyboard", DependsOn: []string{"script-1"}},
	}}
}

// extractObject mirrors internal/agents.extractJSONObject; duplicated (not
// imported) so the planner package has no dependency on agents (spec §5 single
// responsibility). Strips ```json fences + surrounding prose, returns the first
// balanced top-level object.
func extractObject(s string) (string, error) {
	t := s
	if i := strings.Index(t, "```"); i >= 0 {
		t = t[i+3:]
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			if first := strings.TrimSpace(t[:nl]); first == "" || isWord(first) {
				t = t[nl+1:]
			}
		}
		if j := strings.Index(t, "```"); j >= 0 {
			t = t[:j]
		}
	}
	start := strings.IndexByte(t, '{')
	if start < 0 {
		return "", fmt.Errorf("planner: no JSON object found")
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(t); i++ {
		c := t[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return t[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("planner: unbalanced JSON object")
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
