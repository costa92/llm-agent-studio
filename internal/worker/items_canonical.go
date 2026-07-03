package worker

// items cut-over PR-A (docs/specs/items-cutover.md §3): the ItemsCanonical ON
// branch for the two built-in upstream-input consumers that the plain loadInputs
// concatenation cannot serve — runStoryboard and runPrescreen both need the
// legacy "多 dep 按 updated_at 挑最新单个上游" SELECTION over their deps, so this
// file adds a per-dep variant (loadInputsByDep) that preserves the dep grouping +
// the todo metadata the selection keys on, and rebuilds each consumer's legacy
// choice equivalently on top of it. Every read stays project-scoped/fail-closed
// (F1) via itemsForDep; items missing → itemsForDep's output_ref projection
// fallback (★M-4) covers straddling-deploy runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// depInput is one dependency's items plus the dep-todo metadata needed to
// rebuild the legacy selection semantics (newest-single-upstream, prefix
// filtering) that loadInputs' flat concatenation drops.
type depInput struct {
	todoID    string
	typ       string
	outputRef string
	updatedAt time.Time
	items     []Item
}

// loadInputsByDep is the per-dep variant of loadInputs: the same project-scoped
// items read + output_ref projection fallback (itemsForDep, F1/★M-4), but it
// PRESERVES the dep grouping and carries each dep todo's type/output_ref/
// updated_at so callers can equivalently rebuild legacy selection semantics.
func (w *Worker) loadInputsByDep(ctx context.Context, todoID string) ([]depInput, error) {
	var depIDs pq.StringArray
	var projectID string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT depends_on, project_id FROM todos WHERE id=$1`, todoID).Row().Scan(&depIDs, &projectID); err != nil {
		return nil, fmt.Errorf("worker: load %s depends_on: %w", todoID, err)
	}
	out := make([]depInput, 0, len(depIDs))
	for _, dep := range depIDs {
		d := depInput{todoID: dep}
		// Project-scoped metadata read (F1): a forged cross-project dep id reads
		// zero rows and fails closed, mirroring itemsForDep's fallback read.
		if err := w.cfg.DB.WithContext(ctx).Raw(
			`SELECT type, COALESCE(output_ref,''), updated_at FROM todos WHERE id=$1 AND project_id=$2`,
			dep, projectID).Row().Scan(&d.typ, &d.outputRef, &d.updatedAt); err != nil {
			return nil, fmt.Errorf("worker: load dep %s metadata: %w", dep, err)
		}
		// An empty output_ref (a dep that never completed) has nothing to project;
		// skip the items read — itemsForDep's default case would return nil anyway,
		// but its fallback scans output_ref without COALESCE and a NULL would error
		// where the legacy JOIN filters silently.
		if d.outputRef != "" {
			items, err := w.itemsForDep(ctx, dep, projectID)
			if err != nil {
				return nil, err
			}
			d.items = items
		}
		out = append(out, d)
	}
	return out, nil
}

// storyboardScriptInput resolves the storyboard's upstream script via the items
// canonical channel (ItemsCanonical ON branch). It equivalently rebuilds the
// legacy selection: among depends_on parents, the NEWEST (updated_at) 'script'
// todo whose output_ref is 'script:<id>' wins; its single item carries the
// script content (dual-written by runScript, or projected from the scripts row
// by itemsForDep's fallback for a straddling dep). The no-parent-edge fallback
// — project-wide newest script (M1 compat heuristic) — is preserved AS-IS;
// removing it is a Phase 3 decision, not this cut-over's.
func (w *Worker) storyboardScriptInput(ctx context.Context, c claimed) (string, []byte, error) {
	deps, err := w.loadInputsByDep(ctx, c.todoID)
	if err != nil {
		return "", nil, err
	}
	var sel *depInput
	for i := range deps {
		d := &deps[i]
		if d.typ != "script" || !strings.HasPrefix(d.outputRef, "script:") {
			continue
		}
		if sel == nil || d.updatedAt.After(sel.updatedAt) {
			sel = d
		}
	}
	if sel != nil {
		if len(sel.items) == 0 {
			return "", nil, fmt.Errorf("worker: storyboard upstream script %s has no items", sel.todoID)
		}
		return strings.TrimPrefix(sel.outputRef, "script:"), []byte(sel.items[0].JSON), nil
	}
	var scriptID string
	var contentJSON []byte
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`SELECT id, content_json FROM scripts WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
		c.projectID).Row().Scan(&scriptID, &contentJSON); err != nil {
		return "", nil, fmt.Errorf("worker: load upstream script: %w", err)
	}
	return scriptID, contentJSON, nil
}

// prescreenUpstreamText resolves the prescreen's upstream text via the items
// canonical channel (ItemsCanonical ON branch). Legacy selection rebuilt
// equivalently: the NEWEST (updated_at) dep whose output_ref is a text source
// (script:/custom:) wins; asset:/shots: deps are excluded exactly like the
// legacy JOIN filter. The item→text projection uses the same Q1=A' accessor
// inference as the expr channel (exprNodeAccessor): a text-shaped dep unwraps
// its {"text":...} item byte-identically to the legacy content read; anything
// else is the item's json — JSONB-normalized, i.e. semantically equal to the
// legacy content string (the accepted soak envelope).
func (w *Worker) prescreenUpstreamText(ctx context.Context, c claimed) (string, error) {
	deps, err := w.loadInputsByDep(ctx, c.todoID)
	if err != nil {
		return "", err
	}
	var sel *depInput
	for i := range deps {
		d := &deps[i]
		if !strings.HasPrefix(d.outputRef, "script:") && !strings.HasPrefix(d.outputRef, "custom:") {
			continue
		}
		if sel == nil || d.updatedAt.After(sel.updatedAt) {
			sel = d
		}
	}
	if sel == nil {
		return "", fmt.Errorf("worker: prescreen found no upstream text node")
	}
	if len(sel.items) == 0 {
		return "", fmt.Errorf("worker: prescreen upstream %s has no items", sel.todoID)
	}
	if w.exprNodeAccessor(ctx, sel.todoID, c.projectID, sel.outputRef) == ".json.text" {
		var wrap struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(sel.items[0].JSON, &wrap); err != nil {
			return "", fmt.Errorf("worker: prescreen upstream %s text item decode: %w", sel.todoID, err)
		}
		return wrap.Text, nil
	}
	return string(sel.items[0].JSON), nil
}
