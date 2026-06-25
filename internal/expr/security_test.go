package expr

// SECURITY guard (plan §S-3, MERGE GATE).
//
// Why this file exists: the expr engine is a PEER to the worker's {{name}}
// secret channel and runs AFTER the worker's one-shot secret-resolution pass.
// The load-bearing invariant is that this engine NEVER resolves a secret. A
// `secret:` span is an OPAQUE LITERAL: it passes through {{ }} and all, never
// parsed, never resolved. And critically, a secret-looking string that arrives
// INSIDE resolved data (an upstream item's JSON, a $node ref, a concat result)
// is surfaced verbatim — the engine does NOT do a second pass over its own
// output, so a `{{secret:X}}` value reaching the engine through any data path
// can never be turned into a real secret. These tests pin that invariant
// against the real Resolve (no engine stubs). A failure here is a security
// finding, not a test to relax.

import (
	"encoding/json"
	"testing"
)

// nodeReturning builds a Context whose NodeByID resolves id -> items, so $node
// refs can be exercised without a worker.
func nodeReturning(id string, items []Item) Context {
	return Context{
		NodeByID: func(got string) ([]Item, error) {
			if got != id {
				return nil, nil
			}
			return items, nil
		},
	}
}

func TestSecretSpanPassthroughVerbatim(t *testing.T) {
	tests := []struct {
		name string
		tpl  string
		ctx  Context
		want string
	}{
		{
			name: "bare secret span no spaces",
			tpl:  "{{secret:STRIPE_KEY}}",
			ctx:  Context{},
			want: "{{secret:STRIPE_KEY}}",
		},
		{
			name: "bare secret span inner spaces",
			tpl:  "{{ secret:STRIPE_KEY }}",
			ctx:  Context{},
			want: "{{ secret:STRIPE_KEY }}",
		},
		{
			// The name channel resolves; the secret span stays verbatim. Proves
			// the two channels coexist without the secret span being touched.
			name: "mixed name resolve plus secret verbatim",
			tpl:  "{{ $json.name }}-{{secret:FOO}}",
			ctx:  Context{Self: []Item{{JSON: json.RawMessage(`{"name":"ada"}`)}}},
			want: "ada-{{secret:FOO}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.tpl, tt.ctx)
			if err != nil {
				t.Fatalf("Resolve(%q) unexpected error: %v", tt.tpl, err)
			}
			if got != tt.want {
				t.Fatalf("Resolve(%q) = %q, want %q (secret span must pass through verbatim)", tt.tpl, got, tt.want)
			}
		})
	}
}

// TestSecretInsideNodeDataNotRecursivelyResolved is the key S-3 guard: a
// secret-looking string living INSIDE node data is read as a plain data value
// and surfaced as-is. The engine must NOT recurse into resolved output and
// resolve a secret found there.
func TestSecretInsideNodeDataNotRecursivelyResolved(t *testing.T) {
	const secretLike = `{{secret:STRIPE_KEY}}`
	ctx := nodeReturning("dep-1", []Item{
		{JSON: json.RawMessage(`{"text":"{{secret:STRIPE_KEY}}"}`)},
	})

	got, err := Resolve(`{{ $node["dep-1"].json.text }}`, ctx)
	if err != nil {
		t.Fatalf("Resolve unexpected error: %v", err)
	}
	if got != secretLike {
		t.Fatalf("Resolve($node data) = %q, want literal %q (no recursive secret resolution)", got, secretLike)
	}
}

// TestSecretInsideStringifiedNodeDataUntouched pins the same invariant through
// JSON.stringify of a whole node item: the secret string sits inside the data
// and must round-trip into compact JSON untouched.
func TestSecretInsideStringifiedNodeDataUntouched(t *testing.T) {
	const want = `{"text":"{{secret:STRIPE_KEY}}"}`
	ctx := nodeReturning("dep-1", []Item{
		{JSON: json.RawMessage(`{"text":"{{secret:STRIPE_KEY}}"}`)},
	})

	got, err := Resolve(`{{ JSON.stringify($node["dep-1"].json) }}`, ctx)
	if err != nil {
		t.Fatalf("Resolve(JSON.stringify) unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("Resolve(JSON.stringify of node data) = %q, want %q (secret string inside data must be untouched)", got, want)
	}
}
