package worker

import (
	"context"
	"fmt"

	"github.com/lib/pq"

	"github.com/costa92/llm-agent-studio/internal/expr"
)

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
