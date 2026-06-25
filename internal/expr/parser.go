package expr

import (
	"fmt"
	"strconv"
)

// node is the unexported AST union produced by parse. Each concrete node carries
// only the data the evaluator (T3) needs; there is no position/source tracking
// because the restricted grammar errors out long before evaluation.
type node interface{ isNode() }

// rootRef is a bare head reference: "$json" | "$binary" | "$now". ($node is a
// distinct nodeRef because it always carries a captured id.)
type rootRef struct{ name string }

// nodeRef is $node["id"]; id is captured from the required string literal.
type nodeRef struct{ id string }

// member is receiver.name member access (e.g. $json.name).
type member struct {
	recv node
	name string
}

// indexExpr is receiver[key] where key is a string literal or int literal. When
// isInt is true, key holds the raw digits (e.g. "0") so the evaluator can decide
// array index vs object key.
type indexExpr struct {
	recv  node
	key   string
	isInt bool
}

// strLit is a string literal; val is the unquoted value.
type strLit struct{ val string }

// intLit is an integer literal already parsed to int.
type intLit struct{ val int }

// concat is one or more parts joined by '+' (string concatenation). A single
// part is never wrapped in concat — parse returns the bare node.
type concat struct{ parts []node }

// method is the only allowed method call, recv.toLowerCase(). name is always
// "toLowerCase" in P2b; kept as a field so T3 can switch on it if the whitelist
// grows.
type method struct {
	recv node
	name string
}

// jsonStringify is JSON.stringify(arg) — exactly one argument.
type jsonStringify struct{ arg node }

func (rootRef) isNode()       {}
func (nodeRef) isNode()       {}
func (member) isNode()        {}
func (indexExpr) isNode()     {}
func (strLit) isNode()        {}
func (intLit) isNode()        {}
func (concat) isNode()        {}
func (method) isNode()        {}
func (jsonStringify) isNode() {}

// allowedHeads is the closed set of head identifiers the grammar accepts.
// $node and JSON are handled specially (they require trailing syntax), so they
// are not bare rootRefs; the rest resolve to rootRef.
var allowedRootHeads = map[string]bool{
	"$json":   true,
	"$binary": true,
	"$now":    true,
}

// cursor is the parser state over a token slice. The slice always ends with a
// tEOF, so peek() never goes out of bounds once pos reaches the end.
type cursor struct {
	pos  int
	toks []token
}

func (c *cursor) peek() token { return c.toks[c.pos] }

func (c *cursor) next() token {
	t := c.toks[c.pos]
	if c.pos < len(c.toks)-1 {
		c.pos++
	}
	return t
}

// expect consumes the next token if it matches kind, else returns an error.
func (c *cursor) expect(kind tokKind) (token, error) {
	t := c.peek()
	if t.kind != kind {
		return token{}, fmt.Errorf("expr: expected token kind %d, got %d", kind, t.kind)
	}
	return c.next(), nil
}

// parse tokenizes and parses one expression span into an AST, enforcing the
// restricted grammar whitelist. Returns an error (never panics) on any
// tokenize failure or disallowed construct.
func parse(span string) (node, error) {
	toks, err := tokenize(span)
	if err != nil {
		return nil, err
	}
	c := &cursor{toks: toks}
	if c.peek().kind == tEOF {
		return nil, fmt.Errorf("expr: empty expression")
	}
	n, err := parseConcat(c)
	if err != nil {
		return nil, err
	}
	if c.peek().kind != tEOF {
		return nil, fmt.Errorf("expr: unexpected trailing token %q", c.peek().val)
	}
	return n, nil
}

// concat := unary ( '+' unary )*
func parseConcat(c *cursor) (node, error) {
	first, err := parseUnary(c)
	if err != nil {
		return nil, err
	}
	if c.peek().kind != tPlus {
		return first, nil
	}
	parts := []node{first}
	for c.peek().kind == tPlus {
		c.next() // consume '+'
		nxt, err := parseUnary(c)
		if err != nil {
			return nil, err
		}
		parts = append(parts, nxt)
	}
	return concat{parts: parts}, nil
}

// unary := primary postfix*
func parseUnary(c *cursor) (node, error) {
	recv, err := parsePrimary(c)
	if err != nil {
		return nil, err
	}
	return parsePostfix(c, recv)
}

// parsePostfix applies zero or more postfix operators (.member, [index],
// .toLowerCase()) to recv.
func parsePostfix(c *cursor, recv node) (node, error) {
	for {
		switch c.peek().kind {
		case tDot:
			c.next() // consume '.'
			id, err := c.expect(tIdent)
			if err != nil {
				return nil, fmt.Errorf("expr: expected member name after '.'")
			}
			// A '(' here means a method call. toLowerCase is the only one allowed.
			if c.peek().kind == tLParen {
				if id.val != "toLowerCase" {
					return nil, fmt.Errorf("expr: method %q not allowed (only toLowerCase)", id.val)
				}
				c.next() // consume '('
				if _, err := c.expect(tRParen); err != nil {
					return nil, fmt.Errorf("expr: toLowerCase() takes no arguments")
				}
				recv = method{recv: recv, name: "toLowerCase"}
				continue
			}
			recv = member{recv: recv, name: id.val}
		case tLBracket:
			c.next() // consume '['
			key := c.peek()
			switch key.kind {
			case tString:
				c.next()
				if _, err := c.expect(tRBracket); err != nil {
					return nil, fmt.Errorf("expr: expected ']' after index")
				}
				recv = indexExpr{recv: recv, key: key.val, isInt: false}
			case tInt:
				c.next()
				if _, err := c.expect(tRBracket); err != nil {
					return nil, fmt.Errorf("expr: expected ']' after index")
				}
				recv = indexExpr{recv: recv, key: key.val, isInt: true}
			default:
				return nil, fmt.Errorf("expr: index must be a string or int literal")
			}
		default:
			return recv, nil
		}
	}
}

// primary := head | strLit | intLit | '(' expr ')'
func parsePrimary(c *cursor) (node, error) {
	t := c.peek()
	switch t.kind {
	case tString:
		c.next()
		return strLit{val: t.val}, nil
	case tInt:
		c.next()
		v, err := strconv.Atoi(t.val)
		if err != nil {
			return nil, fmt.Errorf("expr: invalid integer literal %q", t.val)
		}
		return intLit{val: v}, nil
	case tLParen:
		c.next() // consume '('
		inner, err := parseConcat(c)
		if err != nil {
			return nil, err
		}
		if _, err := c.expect(tRParen); err != nil {
			return nil, fmt.Errorf("expr: expected ')'")
		}
		return inner, nil
	case tIdent:
		return parseHead(c)
	default:
		return nil, fmt.Errorf("expr: unexpected token %q", t.val)
	}
}

// parseHead resolves a head identifier, enforcing the closed whitelist:
//   - $json / $binary / $now  -> rootRef
//   - $node["id"]             -> nodeRef (requires a string index)
//   - JSON.stringify(expr)    -> jsonStringify (the only valid JSON usage)
func parseHead(c *cursor) (node, error) {
	id := c.next() // the head ident
	switch {
	case allowedRootHeads[id.val]:
		return rootRef{name: id.val}, nil
	case id.val == "$node":
		if _, err := c.expect(tLBracket); err != nil {
			return nil, fmt.Errorf("expr: $node must be indexed by a string id, e.g. $node[\"id\"]")
		}
		key := c.peek()
		if key.kind != tString {
			return nil, fmt.Errorf("expr: $node index must be a string literal")
		}
		c.next()
		if _, err := c.expect(tRBracket); err != nil {
			return nil, fmt.Errorf("expr: expected ']' after $node index")
		}
		return nodeRef{id: key.val}, nil
	case id.val == "JSON":
		// JSON is only valid as JSON.stringify(expr).
		if _, err := c.expect(tDot); err != nil {
			return nil, fmt.Errorf("expr: JSON is only valid as JSON.stringify(expr)")
		}
		meth, err := c.expect(tIdent)
		if err != nil || meth.val != "stringify" {
			return nil, fmt.Errorf("expr: only JSON.stringify is allowed")
		}
		if _, err := c.expect(tLParen); err != nil {
			return nil, fmt.Errorf("expr: JSON.stringify requires '('")
		}
		if c.peek().kind == tRParen {
			return nil, fmt.Errorf("expr: JSON.stringify requires exactly one argument")
		}
		arg, err := parseConcat(c)
		if err != nil {
			return nil, err
		}
		if _, err := c.expect(tRParen); err != nil {
			return nil, fmt.Errorf("expr: JSON.stringify takes exactly one argument")
		}
		return jsonStringify{arg: arg}, nil
	default:
		return nil, fmt.Errorf("expr: unknown identifier %q (not in whitelist)", id.val)
	}
}
