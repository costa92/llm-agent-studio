package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/lib/pq"

	"github.com/costa92/llm-agent-studio/internal/expr"
)

// toExprTemplate rewrites the {{name}} parity subset to {{ $json.<name> }} so the
// expr engine resolves the same upstream-variable channel as substituteVars. It
// rewrites ONLY the known variable names (mirroring substituteVars' match), leaving
// {{secret:...}} and any other braces untouched.
//
// IMPORTANT: it uses ReplaceAllLiteralString, NOT ReplaceAllString — the replacement
// contains "$json", and ReplaceAllString would interpret "$j"/"$json" as a
// capture-group reference and silently drop it.
func toExprTemplate(tpl string, varNames []string) string {
	out := tpl
	for _, name := range varNames {
		trimmed := strings.TrimSpace(name)
		re := regexp.MustCompile(`\{\{\s*` + regexp.QuoteMeta(trimmed) + `\s*\}\}`)
		out = re.ReplaceAllLiteralString(out, "{{ $json."+trimmed+" }}")
	}
	return out
}

// exprParityCheck recomputes the {{name}} substitution for one template via the expr
// engine and logs ONLY safe metadata — todo id, label, a diverged bool, and the two
// lengths. It is a PARITY PROBE: it never feeds the result downstream and never calls
// Secrets.Resolve.
//
// F4: the log line must NEVER contain the resolved value strings — they can carry
// secret or cross-project content; only id/label/bool/lengths are emitted.
// F3: runCustomLLM has NO secret pass at all; the secret-literal guarantee here comes
// from the engine treating "secret:" as a literal, NOT from any "secret pass ran
// first" ordering (that argument applies only to runCustomHTTP).
func (w *Worker) exprParityCheck(ctx context.Context, c claimed, label, tpl, legacy string, replacer map[string]string) {
	names := make([]string, 0, len(replacer))
	for n := range replacer {
		names = append(names, n)
	}
	exprTpl := toExprTemplate(tpl, names)
	selfJSON, err := json.Marshal(replacer) // {"name":"value", ...}
	if err != nil {
		// F4: log metadata only — never the value strings. Treat a marshal
		// failure as a diverged probe and return before calling expr.Resolve.
		w.cfg.Logger.Info("worker: expr parity probe",
			"todo_id", c.todoID, "label", label, "diverged", true, "marshal_err", true)
		return
	}
	ec := w.exprNodeResolver(ctx, c, []Item{{JSON: selfJSON}})
	got, err := expr.Resolve(exprTpl, ec)
	diverged := err != nil || got != legacy
	w.cfg.Logger.Info("worker: expr parity probe",
		"todo_id", c.todoID, "label", label,
		"diverged", diverged, "len_legacy", len(legacy), "len_expr", len(got))
}

// exprNodeProbe is the real-$node shadow probe (P3b). For each variable with a
// non-empty SourceTodoId it compares the LEGACY whole-output value (mirroring
// resolveVariables exactly) against the value the cut-over will use: a real
// $node["<src>"]<accessor> resolution through the LIVE, S-2-enforcing
// exprNodeResolver (NOT a fresh unscoped expr.Context). The accessor (Q1=A')
// is derived from the dep's stored node_outputs.format (or, for a straddling dep
// with no node_outputs row, inferred from the output_ref prefix the itemsForDep
// fallback will project). Each comparison is classified exact / benign (the
// accepted H-3/M-1 key-reorder/whitespace case: byte-different but
// json.Unmarshal+reflect.DeepEqual equal) / divergent.
//
// It is a SHADOW probe: it NEVER feeds the resolved value downstream and a probe
// failure NEVER fails the run (it returns nothing; it only logs). It is gated on
// w.cfg.ExprParity at the call sites.
//
// F4: the log line carries metadata ONLY — todo id, the slice INDEX (not v.Name),
// the class, the two lengths, and the two error bools. NEVER the resolved values,
// NEVER v.Name.
func (w *Worker) exprNodeProbe(ctx context.Context, c claimed, vars []customVariable) {
	for i, v := range vars {
		if v.SourceTodoId == "" {
			continue
		}

		// 1. Legacy value — mirror resolveVariables EXACTLY.
		var legacyVal string
		var legacyErr error
		var outputRef string
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT COALESCE(output_ref,'') FROM todos WHERE id=$1`, v.SourceTodoId).Row().Scan(&outputRef); err != nil {
			legacyErr = err
		} else {
			legacyVal, legacyErr = w.resolveOutputText(ctx, outputRef)
		}

		// 2. Determine the Q1=A' accessor from the dep's stored format. A "text"
		// output is projected as {"text": ...} (accessor .json.text); any other
		// non-empty format wraps the object itself (accessor .json). With no
		// node_outputs row (straddling dep — itemsForDep falls back to projecting
		// output_ref), infer from the output_ref prefix: custom: -> .json.text
		// (the custom fallback wraps as a textItem), everything else -> .json.
		accessor := w.exprNodeAccessor(ctx, v.SourceTodoId, c.projectID, outputRef)

		// 3. Expr value via the LIVE resolver (S-2 enforced; Self=nil — custom
		// nodes reference $node, not $json).
		tpl := `{{ $node["` + v.SourceTodoId + `"]` + accessor + ` }}`
		exprVal, exprErr := expr.Resolve(tpl, w.exprNodeResolver(ctx, c, nil))

		// 4. Classify.
		class := "divergent"
		switch {
		case exprErr != nil || legacyErr != nil:
			class = "divergent"
		case exprVal == legacyVal:
			class = "exact"
		default:
			var a, b any
			if json.Unmarshal([]byte(exprVal), &a) == nil &&
				json.Unmarshal([]byte(legacyVal), &b) == nil &&
				reflect.DeepEqual(a, b) {
				class = "benign"
			} else {
				class = "divergent"
			}
		}

		// 5. Log metadata ONLY (F4).
		w.cfg.Logger.Info("worker: expr $node shadow probe",
			"todo_id", c.todoID, "var_index", i, "class", class,
			"len_legacy", len(legacyVal), "len_expr", len(exprVal),
			"expr_err", exprErr != nil, "legacy_err", legacyErr != nil)
	}
}

// exprNodeAccessor picks the Q1=A' accessor for srcTodoID's dep value: ".json.text"
// when the dep's stored output is text-shaped, ".json" otherwise. It reads the
// newest node_outputs.format for the dep; when there is no row (a straddling dep
// that itemsForDep will satisfy via the output_ref projection) it infers from the
// output_ref prefix (custom: -> textItem -> .json.text; script:/other -> .json).
func (w *Worker) exprNodeAccessor(ctx context.Context, srcTodoID, projectID, outputRef string) string {
	var format string
	err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT COALESCE(format,'') FROM node_outputs WHERE todo_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 1`,
		srcTodoID, projectID).Row().Scan(&format)
	if err == nil && format != "" {
		if format == "text" {
			return ".json.text"
		}
		return ".json"
	}
	// No node_outputs row: infer from the output_ref prefix the fallback projects.
	if strings.HasPrefix(outputRef, "custom:") {
		return ".json.text"
	}
	return ".json"
}

// exprNodeResolver builds an expr.Context for evaluating {{ }} templates over the
// executing todo c's items. It enforces the S-2 cross-tenant invariant: $node["id"]
// resolves ONLY ids in c's DIRECT depends_on set (out-of-set is denied), and every
// underlying read is PROJECT-SCOPED to c.projectID via itemsForDep (a forged
// cross-project dep id reads zero rows → fail-closed, never another tenant's items).
// Direct-deps + project-scope is a deliberate fail-closed ceiling (A1/F5): a
// transitive-ancestor id is denied by default — widening to a transitive closure is
// a non-goal; future ancestor access must add an explicit edge (a direct dep).
func (w *Worker) exprNodeResolver(ctx context.Context, c claimed, self []Item) expr.Context {
	return expr.Context{
		Self:    toExprItems(self),
		ItemIdx: 0,
		NodeByID: func(id string) ([]expr.Item, error) {
			var depIDs pq.StringArray
			if err := w.cfg.DB.WithContext(ctx).Raw(
				`SELECT depends_on FROM todos WHERE id=$1 AND project_id=$2`, c.todoID, c.projectID).Row().Scan(&depIDs); err != nil {
				return nil, fmt.Errorf("expr: load %s depends_on: %w", c.todoID, err)
			}
			inSet := false
			for _, d := range depIDs {
				if d == id {
					inSet = true
					break
				}
			}
			if !inSet {
				return nil, fmt.Errorf("expr: node %q not in dependsOn of %s (denied)", id, c.todoID)
			}
			items, err := w.itemsForDep(ctx, id, c.projectID)
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				return nil, fmt.Errorf("expr: node %q has no items (denied)", id)
			}
			return toExprItems(items), nil
		},
	}
}

// toExprItems bridges worker.Item → expr.Item. The leaf engine mirrors worker.Item
// and worker.BinaryRef field-for-field, so the worker depends on expr, never the
// reverse.
func toExprItems(in []Item) []expr.Item {
	if in == nil {
		return nil
	}
	out := make([]expr.Item, len(in))
	for i, it := range in {
		out[i] = expr.Item{JSON: it.JSON}
		if it.Binary != nil {
			b := make(map[string]expr.BinaryRef, len(it.Binary))
			for k, v := range it.Binary {
				b[k] = expr.BinaryRef{AssetID: v.AssetID, MimeType: v.MimeType, Kind: v.Kind, Status: v.Status}
			}
			out[i].Binary = b
		}
	}
	return out
}
