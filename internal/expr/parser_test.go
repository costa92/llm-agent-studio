package expr

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		span string
		want node
	}{
		{
			name: "json member",
			span: "$json.name",
			want: member{recv: rootRef{name: "$json"}, name: "name"},
		},
		{
			name: "node ref member",
			span: `$node["id-1"].json`,
			want: member{recv: nodeRef{id: "id-1"}, name: "json"},
		},
		{
			name: "json string index",
			span: `$json["a key"]`,
			want: indexExpr{recv: rootRef{name: "$json"}, key: "a key", isInt: false},
		},
		{
			name: "json member int index",
			span: "$json.items[0]",
			want: indexExpr{recv: member{recv: rootRef{name: "$json"}, name: "items"}, key: "0", isInt: true},
		},
		{
			name: "concat two members",
			span: "$json.first + $json.last",
			want: concat{parts: []node{
				member{recv: rootRef{name: "$json"}, name: "first"},
				member{recv: rootRef{name: "$json"}, name: "last"},
			}},
		},
		{
			name: "method toLowerCase",
			span: "$json.name.toLowerCase()",
			want: method{recv: member{recv: rootRef{name: "$json"}, name: "name"}, name: "toLowerCase"},
		},
		{
			name: "json stringify",
			span: "JSON.stringify($json)",
			want: jsonStringify{arg: rootRef{name: "$json"}},
		},
		{
			name: "now head",
			span: "$now",
			want: rootRef{name: "$now"},
		},
		{
			name: "binary member",
			span: "$binary.image",
			want: member{recv: rootRef{name: "$binary"}, name: "image"},
		},
		{
			name: "string literal",
			span: "'hello'",
			want: strLit{val: "hello"},
		},
		{
			name: "concat member and int",
			span: "$json.count + 1",
			want: concat{parts: []node{
				member{recv: rootRef{name: "$json"}, name: "count"},
				intLit{val: 1},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(tc.span)
			if err != nil {
				t.Fatalf("parse(%q) returned error: %v", tc.span, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parse(%q) = %#v, want %#v", tc.span, got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		span string
	}{
		{"empty", ""},
		{"eval call", "eval('x')"},
		{"unknown head member", "window.location"},
		{"index with call", "$json[func()]"},
		{"index with ident", "$json[x]"},
		{"node no index", "$node"},
		{"node int index", "$node[0]"},
		{"json parse", "JSON.parse('x')"},
		{"json stringify no arg", "JSON.stringify()"},
		{"json foo", "JSON.foo"},
		{"disallowed method", "$json.toUpperCase()"},
		{"trailing tokens", "$json.name extra"},
		{"unterminated string", `"oops`},
		{"assignment", "a = b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(tc.span)
			if err == nil {
				t.Fatalf("parse(%q) expected error, got node %#v", tc.span, got)
			}
			if got != nil {
				t.Fatalf("parse(%q) expected nil node on error, got %#v", tc.span, got)
			}
		})
	}
}
