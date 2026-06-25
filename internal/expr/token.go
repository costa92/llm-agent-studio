package expr

import (
	"fmt"
	"strings"
)

// segKind classifies a template segment produced by splitTemplate.
type segKind int

const (
	segLiteral segKind = iota // verbatim text outside {{ }}
	segExpr                   // trimmed inner content of a {{ ... }} span, to be parsed later
	segSecretLiteral          // a {{ ... }} span whose trimmed inner starts with "secret:"; emitted verbatim
)

// segment is one ordered piece of a template string. The meaning of text
// depends on kind:
//   - segLiteral: the literal text exactly as it appeared outside braces.
//   - segExpr: the TRIMMED inner expression (no braces, no surrounding spaces).
//   - segSecretLiteral: the VERBATIM full original span INCLUDING the {{ }} and
//     all inner whitespace, so it round-trips byte-for-byte (it is never parsed
//     or resolved — emitted as-is).
type segment struct {
	kind segKind
	text string
}

// splitTemplate is total: it never errors or panics. It walks tpl left to right,
// peeling off literal runs and {{ ... }} spans. An unterminated "{{" (no closing
// "}}") is treated as plain literal text — grammar errors inside a span are the
// parser's job (T2), not the splitter's. An empty input yields no segments (nil).
func splitTemplate(tpl string) []segment {
	var segs []segment
	for len(tpl) > 0 {
		open := strings.Index(tpl, "{{")
		if open < 0 {
			// No more spans: the rest is literal.
			segs = append(segs, segment{kind: segLiteral, text: tpl})
			break
		}
		rest := tpl[open:]
		close := strings.Index(rest, "}}")
		if close < 0 {
			// Unterminated "{{": no valid span boundary exists, so the whole
			// remaining input (including any text before this "{{") is one literal.
			segs = append(segs, segment{kind: segLiteral, text: tpl})
			break
		}
		if open > 0 {
			segs = append(segs, segment{kind: segLiteral, text: tpl[:open]})
		}
		full := rest[:close+2]          // includes both braces, e.g. "{{ x }}"
		inner := rest[2:close]          // between the braces, untrimmed
		trimmed := strings.TrimSpace(inner)
		if strings.HasPrefix(trimmed, "secret:") {
			// Store the verbatim full span so it round-trips byte-for-byte; it is
			// never parsed or resolved.
			segs = append(segs, segment{kind: segSecretLiteral, text: full})
		} else {
			segs = append(segs, segment{kind: segExpr, text: trimmed})
		}
		tpl = tpl[open+close+2:]
	}
	return segs
}

// tokKind enumerates lexical token kinds for one expression span.
type tokKind int

const (
	tIdent    tokKind = iota // identifier or $-head ($json/$node/$binary/$now) or JSON
	tDot                     // .
	tLBracket                // [
	tRBracket                // ]
	tString                  // string literal (val holds the UNQUOTED value)
	tInt                     // integer literal (val holds the digits)
	tPlus                    // +
	tLParen                  // (
	tRParen                  // )
	tEOF                     // end of input
)

// token is one lexical unit. val carries the literal text for tIdent/tString/tInt
// (tString's val is unquoted); for punctuation and tEOF, val is left empty.
type token struct {
	kind tokKind
	val  string
}

// tokenize lexes one expression span (the trimmed inner of a segExpr) into
// tokens, always terminated with a tEOF. It returns a non-nil error on an
// unterminated string or an unexpected character (never panics). String escapes
// are out of scope for the restricted grammar — a backslash is an ordinary byte.
func tokenize(span string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(span) {
		c := span[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '.':
			toks = append(toks, token{kind: tDot})
			i++
		case c == '[':
			toks = append(toks, token{kind: tLBracket})
			i++
		case c == ']':
			toks = append(toks, token{kind: tRBracket})
			i++
		case c == '+':
			toks = append(toks, token{kind: tPlus})
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen})
			i++
		case c == '\'' || c == '"':
			quote := c
			j := i + 1
			for j < len(span) && span[j] != quote {
				j++
			}
			if j >= len(span) {
				return nil, fmt.Errorf("expr: unterminated string starting at offset %d", i)
			}
			toks = append(toks, token{kind: tString, val: span[i+1 : j]})
			i = j + 1
		case c == '$' || isIdentStart(c):
			// Identifier = "$"? followed by [A-Za-z_][A-Za-z0-9_]*. A lone "$"
			// not followed by an identifier head is an unexpected character.
			j := i
			if c == '$' {
				j++
				if j >= len(span) || !isIdentStart(span[j]) {
					return nil, fmt.Errorf("expr: unexpected character %q at offset %d", string(c), i)
				}
			}
			j++
			for j < len(span) && isIdentPart(span[j]) {
				j++
			}
			toks = append(toks, token{kind: tIdent, val: span[i:j]})
			i = j
		case isDigit(c):
			j := i
			for j < len(span) && isDigit(span[j]) {
				j++
			}
			toks = append(toks, token{kind: tInt, val: span[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("expr: unexpected character %q at offset %d", string(c), i)
		}
	}
	toks = append(toks, token{kind: tEOF})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
