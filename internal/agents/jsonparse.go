// Package agents holds the studio content-production agents (ScriptAgent,
// StoryboardAgent) plus tolerant JSON parsing (spec R1: providers have no
// native structured output, so agents prompt for JSON and parse leniently).
package agents

import (
	"fmt"
	"strings"
)

// extractJSONObject pulls the first balanced top-level JSON object out of an
// LLM reply. It strips ```json / ``` fences and tolerates leading/trailing
// prose. Returns an error if no '{' ... matching '}' is found. It does not
// validate that the slice is well-formed JSON — the caller json.Unmarshal does.
func extractJSONObject(s string) (string, error) {
	// Strip code fences first so a fenced object's braces are the only ones.
	t := s
	if i := strings.Index(t, "```"); i >= 0 {
		t = t[i+3:]
		// optional language tag on the same line (e.g. "json")
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			firstLine := strings.TrimSpace(t[:nl])
			if firstLine == "" || isWord(firstLine) {
				t = t[nl+1:]
			}
		}
		if j := strings.Index(t, "```"); j >= 0 {
			t = t[:j]
		}
	}
	start := strings.IndexByte(t, '{')
	if start < 0 {
		return "", fmt.Errorf("agents: no JSON object found")
	}
	depth := 0
	inStr := false
	esc := false
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
	return "", fmt.Errorf("agents: unbalanced JSON object")
}

// isWord reports whether s is a single alphanumeric token (a fence language tag).
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
