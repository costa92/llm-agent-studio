// Package expr is a leaf, stdlib-only expression engine for n8n-style template
// strings ({{ ... }}). It deliberately mirrors worker.Item / worker.BinaryRef
// field-for-field rather than importing internal/worker, so it stays a leaf
// package (no back-edge into the worker). A thin bridge converts between the two
// representations at the call site in a later task.
package expr

import (
	"encoding/json"
	"strings"
)

// Item mirrors worker.Item field-for-field (see internal/worker/items.go). Keeping
// an independent copy is what keeps this package a leaf: the worker can depend on
// expr, never the reverse.
type Item struct {
	JSON   json.RawMessage      `json:"json"`
	Binary map[string]BinaryRef `json:"binary,omitempty"`
}

// BinaryRef mirrors worker.BinaryRef field-for-field.
type BinaryRef struct {
	AssetID  string `json:"assetId"`
	MimeType string `json:"mimeType"`
	Kind     string `json:"kind"`
	Status   string `json:"status,omitempty"`
}

// Context carries the per-evaluation data the parser/evaluator needs: the
// current node's input items ($json / $binary read Self[ItemIdx]), a resolver
// for cross-node refs ($node["id"]), and the index of the item being evaluated.
// Unused by T1's tokenizer; defined here as part of the package's public surface
// and consumed in later tasks (T3/T5).
type Context struct {
	Self     []Item
	NodeByID func(id string) ([]Item, error)
	ItemIdx  int
}

// Resolve renders an n8n-style template string against ctx. Literal text and
// secret: spans pass through verbatim; {{ expr }} spans are parsed + evaluated +
// stringified. Any parse/eval error aborts and is returned (no partial output on
// error). A missing field is an error, never a silent empty string.
func Resolve(template string, ctx Context) (string, error) {
	var sb strings.Builder
	for _, seg := range splitTemplate(template) {
		switch seg.kind {
		case segLiteral:
			sb.WriteString(seg.text)
		case segSecretLiteral:
			// Emit verbatim — text still contains the {{ }}, intentionally: the
			// engine NEVER resolves secrets.
			sb.WriteString(seg.text)
		case segExpr:
			n, err := parse(seg.text)
			if err != nil {
				return "", err
			}
			v, err := eval(n, ctx)
			if err != nil {
				return "", err
			}
			s, err := stringify(v)
			if err != nil {
				return "", err
			}
			sb.WriteString(s)
		}
	}
	return sb.String(), nil
}
