// Package expr is a leaf, stdlib-only expression engine for n8n-style template
// strings ({{ ... }}). It deliberately mirrors worker.Item / worker.BinaryRef
// field-for-field rather than importing internal/worker, so it stays a leaf
// package (no back-edge into the worker). A thin bridge converts between the two
// representations at the call site in a later task.
package expr

import "encoding/json"

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
