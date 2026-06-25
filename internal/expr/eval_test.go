package expr

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// selfCtx builds a Context whose Self holds a single Item with the given JSON.
func selfCtx(raw string) Context {
	return Context{Self: []Item{{JSON: json.RawMessage(raw)}}}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		template string
		ctx      Context
		want     string
	}{
		{
			name:     "field access",
			template: `{{ $json.name }}`,
			ctx:      selfCtx(`{"name":"Ada"}`),
			want:     "Ada",
		},
		{
			name:     "number keeps exact literal",
			template: `{{ $json.count }}`,
			ctx:      selfCtx(`{"count":42}`),
			want:     "42",
		},
		{
			name:     "mixed literal and expr",
			template: `Hi {{ $json.name }}!`,
			ctx:      selfCtx(`{"name":"Ada"}`),
			want:     "Hi Ada!",
		},
		{
			name:     "array index",
			template: `{{ $json.items[1] }}`,
			ctx:      selfCtx(`{"items":["a","b","c"]}`),
			want:     "b",
		},
		{
			name:     "string concat",
			template: `{{ $json.first + ' ' + $json.last }}`,
			ctx:      selfCtx(`{"first":"Ada","last":"Lovelace"}`),
			want:     "Ada Lovelace",
		},
		{
			name:     "toLowerCase method",
			template: `{{ $json.name.toLowerCase() }}`,
			ctx:      selfCtx(`{"name":"ADA"}`),
			want:     "ada",
		},
		{
			name:     "JSON.stringify compact",
			template: `{{ JSON.stringify($json) }}`,
			ctx:      selfCtx(`{"a":1}`),
			want:     `{"a":1}`,
		},
		{
			name:     "secret passthrough verbatim",
			template: `{{secret:STRIPE_KEY}}`,
			ctx:      selfCtx(`{}`),
			want:     `{{secret:STRIPE_KEY}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.template, tc.ctx)
			if err != nil {
				t.Fatalf("Resolve(%q) unexpected error: %v", tc.template, err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.template, got, tc.want)
			}
		})
	}
}

func TestResolveNow(t *testing.T) {
	got, err := Resolve(`{{ $now }}`, selfCtx(`{}`))
	if err != nil {
		t.Fatalf("Resolve($now) unexpected error: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Fatalf("Resolve($now) = %q, not RFC3339: %v", got, err)
	}
}

func TestResolveNodeRef(t *testing.T) {
	ctx := Context{
		Self: []Item{{JSON: json.RawMessage(`{}`)}},
		NodeByID: func(id string) ([]Item, error) {
			if id != "dep-1" {
				return nil, errors.New("unknown node")
			}
			return []Item{{JSON: json.RawMessage(`{"title":"Hello"}`)}}, nil
		},
	}
	got, err := Resolve(`{{ $node["dep-1"].json.title }}`, ctx)
	if err != nil {
		t.Fatalf("Resolve($node) unexpected error: %v", err)
	}
	if got != "Hello" {
		t.Fatalf("Resolve($node) = %q, want %q", got, "Hello")
	}
}

func TestResolveBinary(t *testing.T) {
	ctx := Context{Self: []Item{{
		JSON:   json.RawMessage(`{}`),
		Binary: map[string]BinaryRef{"image": {AssetID: "asset-9", MimeType: "image/png", Kind: "image"}},
	}}}
	got, err := Resolve(`{{ $binary.image }}`, ctx)
	if err != nil {
		t.Fatalf("Resolve($binary) unexpected error: %v", err)
	}
	if got != "asset-9" {
		t.Fatalf("Resolve($binary.image) = %q, want %q", got, "asset-9")
	}
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		name     string
		template string
		ctx      Context
	}{
		{
			name:     "missing field is error",
			template: `{{ $json.missing }}`,
			ctx:      selfCtx(`{"name":"x"}`),
		},
		{
			name:     "array index out of range",
			template: `{{ $json.items[9] }}`,
			ctx:      selfCtx(`{"items":["a","b"]}`),
		},
		{
			name:     "node empty items",
			template: `{{ $node["x"].json.a }}`,
			ctx: Context{
				Self:     []Item{{JSON: json.RawMessage(`{}`)}},
				NodeByID: func(id string) ([]Item, error) { return []Item{}, nil },
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tc.template, tc.ctx)
			if err == nil {
				t.Fatalf("Resolve(%q) expected error, got nil", tc.template)
			}
		})
	}
}

func TestResolveNodeErrorPropagated(t *testing.T) {
	ctx := Context{
		Self:     []Item{{JSON: json.RawMessage(`{}`)}},
		NodeByID: func(id string) ([]Item, error) { return nil, errors.New("denied") },
	}
	_, err := Resolve(`{{ $node["x"].json.a }}`, ctx)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error %q does not propagate %q", err.Error(), "denied")
	}
}
