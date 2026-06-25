package expr

import (
	"reflect"
	"testing"
)

func TestSplitTemplate(t *testing.T) {
	tests := []struct {
		name string
		tpl  string
		want []segment
	}{
		{
			name: "pure literal",
			tpl:  "hello world",
			want: []segment{{kind: segLiteral, text: "hello world"}},
		},
		{
			name: "pure expr",
			tpl:  "{{ $json.name }}",
			want: []segment{{kind: segExpr, text: "$json.name"}},
		},
		{
			name: "mixed",
			tpl:  "Hi {{ $json.name }}!",
			want: []segment{
				{kind: segLiteral, text: "Hi "},
				{kind: segExpr, text: "$json.name"},
				{kind: segLiteral, text: "!"},
			},
		},
		{
			name: "secret span no spaces",
			tpl:  "{{secret:STRIPE_KEY}}",
			want: []segment{{kind: segSecretLiteral, text: "{{secret:STRIPE_KEY}}"}},
		},
		{
			name: "secret span inner spaces",
			tpl:  "{{ secret:FOO }}",
			want: []segment{{kind: segSecretLiteral, text: "{{ secret:FOO }}"}},
		},
		{
			name: "unterminated",
			tpl:  "a {{ b",
			want: []segment{{kind: segLiteral, text: "a {{ b"}},
		},
		{
			name: "empty string",
			tpl:  "",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitTemplate(tt.tpl)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitTemplate(%q) = %#v, want %#v", tt.tpl, got, tt.want)
			}
		})
	}
}

// secret span round-trips byte-for-byte when its stored text is reassembled.
func TestSplitTemplateSecretRoundTrip(t *testing.T) {
	for _, orig := range []string{"{{secret:STRIPE_KEY}}", "{{ secret:FOO }}"} {
		segs := splitTemplate(orig)
		if len(segs) != 1 || segs[0].kind != segSecretLiteral {
			t.Fatalf("splitTemplate(%q) = %#v, want one segSecretLiteral", orig, segs)
		}
		if segs[0].text != orig {
			t.Fatalf("secret round-trip: stored %q, want %q", segs[0].text, orig)
		}
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		span string
		want []token
	}{
		{
			name: "json dot name",
			span: "$json.name",
			want: []token{
				{kind: tIdent, val: "$json"},
				{kind: tDot},
				{kind: tIdent, val: "name"},
				{kind: tEOF},
			},
		},
		{
			name: "node bracket string",
			span: `$node["id-1"].json`,
			want: []token{
				{kind: tIdent, val: "$node"},
				{kind: tLBracket},
				{kind: tString, val: "id-1"},
				{kind: tRBracket},
				{kind: tDot},
				{kind: tIdent, val: "json"},
				{kind: tEOF},
			},
		},
		{
			name: "count plus int",
			span: "$json.count + 1",
			want: []token{
				{kind: tIdent, val: "$json"},
				{kind: tDot},
				{kind: tIdent, val: "count"},
				{kind: tPlus},
				{kind: tInt, val: "1"},
				{kind: tEOF},
			},
		},
		{
			name: "json stringify call",
			span: "JSON.stringify($json)",
			want: []token{
				{kind: tIdent, val: "JSON"},
				{kind: tDot},
				{kind: tIdent, val: "stringify"},
				{kind: tLParen},
				{kind: tIdent, val: "$json"},
				{kind: tRParen},
				{kind: tEOF},
			},
		},
		{
			name: "single quoted string",
			span: "'hello'",
			want: []token{
				{kind: tString, val: "hello"},
				{kind: tEOF},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tokenize(tt.span)
			if err != nil {
				t.Fatalf("tokenize(%q) unexpected error: %v", tt.span, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tokenize(%q) = %#v, want %#v", tt.span, got, tt.want)
			}
		})
	}
}

func TestTokenizeErrors(t *testing.T) {
	tests := []struct {
		name string
		span string
	}{
		{name: "unterminated string", span: `"oops`},
		{name: "unexpected char", span: "$json @ x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tokenize(tt.span)
			if err == nil {
				t.Fatalf("tokenize(%q) = nil error, want non-nil", tt.span)
			}
		})
	}
}
