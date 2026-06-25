package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// itemRef wraps a single Item produced by $node["id"] so a following .json /
// .binary member step can reach into it. It is an internal evaluation-only value
// kind, never returned to callers of Resolve.
type itemRef struct{ item Item }

// decodeJSON decodes raw JSON with UseNumber() so numeric literals keep their
// exact textual form (42 stays "42", not "42.000000"). The resulting value model
// is: map[string]any (object), []any (array), json.Number (number), string,
// bool, nil (null).
func decodeJSON(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("expr: invalid JSON: %w", err)
	}
	return v, nil
}

// eval evaluates one AST node against ctx, returning a value from the concrete
// value model: map[string]any / []any / json.Number / string / bool / nil /
// map[string]BinaryRef / BinaryRef / itemRef.
func eval(n node, ctx Context) (any, error) {
	switch e := n.(type) {
	case rootRef:
		return evalRoot(e, ctx)
	case nodeRef:
		return evalNodeRef(e, ctx)
	case member:
		return evalMember(e, ctx)
	case indexExpr:
		return evalIndex(e, ctx)
	case strLit:
		return e.val, nil
	case intLit:
		return e.val, nil
	case concat:
		return evalConcat(e, ctx)
	case method:
		return evalMethod(e, ctx)
	case jsonStringify:
		return evalStringify(e, ctx)
	default:
		return nil, fmt.Errorf("expr: cannot evaluate node %T", n)
	}
}

func evalRoot(e rootRef, ctx Context) (any, error) {
	switch e.name {
	case "$json":
		if ctx.ItemIdx < 0 || ctx.ItemIdx >= len(ctx.Self) {
			return nil, fmt.Errorf("expr: $json item index %d out of range (have %d items)", ctx.ItemIdx, len(ctx.Self))
		}
		return decodeJSON(ctx.Self[ctx.ItemIdx].JSON)
	case "$binary":
		if ctx.ItemIdx < 0 || ctx.ItemIdx >= len(ctx.Self) {
			return nil, fmt.Errorf("expr: $binary item index %d out of range (have %d items)", ctx.ItemIdx, len(ctx.Self))
		}
		return ctx.Self[ctx.ItemIdx].Binary, nil
	case "$now":
		return time.Now().UTC().Format(time.RFC3339), nil
	default:
		return nil, fmt.Errorf("expr: unknown root reference %q", e.name)
	}
}

func evalNodeRef(e nodeRef, ctx Context) (any, error) {
	if ctx.NodeByID == nil {
		return nil, fmt.Errorf("expr: no node resolver for $node[%q]", e.id)
	}
	items, err := ctx.NodeByID(e.id)
	if err != nil {
		// Propagate the resolver's error verbatim (e.g. authz denial).
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("expr: $node[%q] produced no items", e.id)
	}
	// First item only; no pairedItem semantics (D-3).
	return itemRef{item: items[0]}, nil
}

func evalMember(e member, ctx Context) (any, error) {
	recv, err := eval(e.recv, ctx)
	if err != nil {
		return nil, err
	}
	switch r := recv.(type) {
	case itemRef:
		switch e.name {
		case "json":
			return decodeJSON(r.item.JSON)
		case "binary":
			return r.item.Binary, nil
		default:
			return nil, fmt.Errorf("expr: item has no member %q (only .json/.binary)", e.name)
		}
	case map[string]any:
		v, ok := r[e.name]
		if !ok {
			return nil, fmt.Errorf("expr: field %q not found", e.name)
		}
		return v, nil
	case map[string]BinaryRef:
		v, ok := r[e.name]
		if !ok {
			return nil, fmt.Errorf("expr: binary key %q not found", e.name)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("expr: cannot access field %q on %T", e.name, recv)
	}
}

func evalIndex(e indexExpr, ctx Context) (any, error) {
	recv, err := eval(e.recv, ctx)
	if err != nil {
		return nil, err
	}
	if e.isInt {
		arr, ok := recv.([]any)
		if !ok {
			return nil, fmt.Errorf("expr: cannot index %T with integer", recv)
		}
		i, err := strconv.Atoi(e.key)
		if err != nil {
			return nil, fmt.Errorf("expr: invalid array index %q", e.key)
		}
		if i < 0 || i >= len(arr) {
			return nil, fmt.Errorf("expr: array index %d out of range (len %d)", i, len(arr))
		}
		return arr[i], nil
	}
	switch r := recv.(type) {
	case map[string]any:
		v, ok := r[e.key]
		if !ok {
			return nil, fmt.Errorf("expr: field %q not found", e.key)
		}
		return v, nil
	case map[string]BinaryRef:
		v, ok := r[e.key]
		if !ok {
			return nil, fmt.Errorf("expr: binary key %q not found", e.key)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("expr: cannot index %T with string key", recv)
	}
}

func evalConcat(e concat, ctx Context) (any, error) {
	// Concatenation is string-based in this restricted grammar: each part is
	// evaluated, stringified, then joined.
	var sb strings.Builder
	for _, p := range e.parts {
		v, err := eval(p, ctx)
		if err != nil {
			return nil, err
		}
		s, err := stringify(v)
		if err != nil {
			return nil, err
		}
		sb.WriteString(s)
	}
	return sb.String(), nil
}

func evalMethod(e method, ctx Context) (any, error) {
	if e.name != "toLowerCase" {
		return nil, fmt.Errorf("expr: unknown method %q", e.name)
	}
	recv, err := eval(e.recv, ctx)
	if err != nil {
		return nil, err
	}
	s, err := stringify(recv)
	if err != nil {
		return nil, err
	}
	return strings.ToLower(s), nil
}

func evalStringify(e jsonStringify, ctx Context) (any, error) {
	v, err := eval(e.arg, ctx)
	if err != nil {
		return nil, err
	}
	// For an item reference, marshal the whole Item. json.Number marshals as a
	// bare number, so UseNumber-decoded values round-trip exactly.
	var target any = v
	if ir, ok := v.(itemRef); ok {
		target = ir.item
	}
	b, err := json.Marshal(target)
	if err != nil {
		return nil, fmt.Errorf("expr: JSON.stringify failed: %w", err)
	}
	return string(b), nil
}

// stringify renders an evaluated value to its final string form (used for a
// segExpr result and for each operand of a '+' concatenation).
func stringify(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(x), nil
	case nil:
		// A genuine JSON null renders as the empty string. This is distinct from
		// a MISSING field, which is an error raised in eval (member/indexExpr),
		// never reached here.
		return "", nil
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err != nil {
			return "", fmt.Errorf("expr: cannot stringify %T: %w", x, err)
		}
		return string(b), nil
	case BinaryRef:
		// A binary reference stringifies to its asset id, so $binary.image yields
		// the asset id string (the useful identifier for downstream wiring).
		return x.AssetID, nil
	case itemRef:
		// An item reference stringifies to the compact JSON of the whole Item.
		b, err := json.Marshal(x.item)
		if err != nil {
			return "", fmt.Errorf("expr: cannot stringify item: %w", err)
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("expr: cannot stringify value of type %T", v)
	}
}
