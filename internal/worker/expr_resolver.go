package worker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lib/pq"

	"github.com/costa92/llm-agent-studio/internal/expr"
)

// safeFieldRe is the §8.1 injection gate for varBinding sourceField at RUN time —
// the AUTHORITATIVE last line. resolveVariablesExpr concatenates the field into a
// {{ $node["id"].json.<field> }} template; a safe identifier contains no
// '}'/'{'/'"'/'['/'.'/whitespace, so it cannot break out of the template span.
// This run-time gate (not just the plan-time one in planner.go) is required
// because a re-run reuses existing todos.input_json.params.variables[] WITHOUT
// re-planning (§12 amendment 2) — a dirty direct write there bypasses the planner
// gate, so the field must be re-validated at the moment of execution. ASCII-only,
// matching the expr engine's identifier lexer (no unicode mismatch).
var safeFieldRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// resolveVariablesExpr is the {{name}} value resolver (the only channel since the
// items cut-over, docs/specs/items-cutover.md §3 PR-C). It resolves each upstream
// variable's whole-output value through the expr engine's $node path —
// project-scoped + direct-depends_on + fail-closed via exprNodeResolver. The
// returned map feeds the substituteVars interpolation.
// A missing / empty-items / out-of-deps / cross-project dep returns an error
// (fail-closed) — the run fails rather than silently resolving wrong/no data.
func (w *Worker) resolveVariablesExpr(ctx context.Context, c claimed, vars []customVariable) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vars {
		if v.SourceTodoId == "" {
			continue
		}
		var accessor string
		if v.SourceField != "" {
			// B/P5 field-level binding. §8.1 charset gate FIRST (run-time
			// authoritative). A whitespace-only field is non-empty and fails the
			// regex → rejected, NOT trimmed-to-empty-and-degraded (§12 amendment 3).
			// Built from the field directly — no node_outputs.format lookup needed.
			if !safeFieldRe.MatchString(v.SourceField) {
				return nil, fmt.Errorf("worker: variable %q invalid sourceField", v.Name)
			}
			accessor = ".json." + v.SourceField
		} else {
			// Default (field empty) MUST be byte-for-byte identical to today: infer
			// the accessor from the dep's stored output format via exprNodeAccessor.
			var outputRef string
			if err := w.cfg.DB.WithContext(ctx).Raw(
				`SELECT COALESCE(output_ref,'') FROM todos WHERE id=$1`, v.SourceTodoId).Row().Scan(&outputRef); err != nil {
				return nil, fmt.Errorf("worker: load variable %q source todo: %w", v.Name, err)
			}
			accessor = w.exprNodeAccessor(ctx, v.SourceTodoId, c.projectID, outputRef)
		}
		tpl := `{{ $node["` + v.SourceTodoId + `"]` + accessor + ` }}`
		val, err := expr.Resolve(tpl, w.exprNodeResolver(ctx, c, nil))
		if err != nil {
			return nil, fmt.Errorf("worker: resolve variable %q via expr: %w", v.Name, err)
		}
		out[v.Name] = val
	}
	return out, nil
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
