package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/workflows"
)

// WorkflowStore is the workflows CRUD surface (satisfied by *workflows.Store).
type WorkflowStore interface {
	Create(ctx context.Context, projectID, name string, nodes json.RawMessage) (workflows.Workflow, error)
	Get(ctx context.Context, projectID, id string) (workflows.Workflow, error)
	ListByProject(ctx context.Context, projectID string) ([]workflows.Workflow, error)
	Update(ctx context.Context, projectID, id, name string, nodes json.RawMessage) (workflows.Workflow, error)
	Delete(ctx context.Context, projectID, id string) error
}

// workflowReq is the create/update body. nodes is the DAG (array of
// planner.WorkflowNode shape); kept raw so the store stays decoupled.
type workflowReq struct {
	Name  string          `json:"name"`
	Nodes json.RawMessage `json:"nodes"`
}

// listWorkflowsHandler GET /api/projects/{id}/workflows — viewer+. Each item
// carries its latest run status (derived from plans.workflow_id).
func listWorkflowsHandler(ws WorkflowStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := ws.ListByProject(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []workflows.Workflow{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// createWorkflowHandler POST /api/projects/{id}/workflows — editor+.
func createWorkflowHandler(ws WorkflowStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workflowReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
		}
		if len(req.Nodes) > 0 {
			var nodes []planner.WorkflowNode
			if err := json.Unmarshal(req.Nodes, &nodes); err != nil {
				http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
				return
			}
			if len(nodes) > 0 {
				if err := planner.ValidateCustomGraph(nodes); err != nil {
					http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		wf, err := ws.Create(r.Context(), r.PathValue("id"), req.Name, req.Nodes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, wf)
	}
}

// updateWorkflowHandler PUT /api/projects/{id}/workflows/{wfId} — editor+.
func updateWorkflowHandler(ws WorkflowStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workflowReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
		}
		if len(req.Nodes) > 0 {
			var nodes []planner.WorkflowNode
			if err := json.Unmarshal(req.Nodes, &nodes); err != nil {
				http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
				return
			}
			if len(nodes) > 0 {
				if err := planner.ValidateCustomGraph(nodes); err != nil {
					http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		wf, err := ws.Update(r.Context(), r.PathValue("id"), r.PathValue("wfId"), req.Name, req.Nodes)
		if errors.Is(err, workflows.ErrNotFound) {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, wf)
	}
}

// deleteWorkflowHandler DELETE /api/projects/{id}/workflows/{wfId} — editor+.
func deleteWorkflowHandler(ws WorkflowStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := ws.Delete(r.Context(), r.PathValue("id"), r.PathValue("wfId"))
		if errors.Is(err, workflows.ErrNotFound) {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// runWorkflowHandler POST /api/projects/{id}/workflows/{wfId}/run — editor+.
// Runs ONE workflow as an independent unit: builds the Brief from the project
// row, loads the workflow's DAG, and calls PlanCustom with the workflow id so
// the resulting plan (run) is tagged with workflow_id. Mirrors runHandler's
// quota gate + status/event emission.
func runWorkflowHandler(ps ProjectStore, ws WorkflowStore, pl PlannerPort, ev EventAppender, cs CostStore, quota int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		wfID := r.PathValue("wfId")
		p, err := ps.Get(r.Context(), id)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		wf, err := ws.Get(r.Context(), id, wfID)
		if errors.Is(err, workflows.ErrNotFound) {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		var nodes []planner.WorkflowNode
		if len(wf.Nodes) > 0 {
			if err := json.Unmarshal(wf.Nodes, &nodes); err != nil {
				http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		if len(nodes) == 0 {
			http.Error(w, "workflow has no nodes", http.StatusBadRequest)
			return
		}
		if err := planner.ValidateCustomGraph(nodes); err != nil {
			http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
			return
		}
		if planner.HasCustomNode(nodes) {
			http.Error(w, "当前 Workflow 包含自定义节点，暂不支持运行", http.StatusBadRequest)
			return
		}
		if over, err := quotaExceeded(r.Context(), cs, quota, p.OrgID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if over {
			http.Error(w, "generation quota exceeded for org", http.StatusTooManyRequests)
			return
		}
		if err := ps.SetStatus(r.Context(), id, "planning"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = ev.Append(r.Context(), id, "planner_started", "", nil)
		brief := planner.Brief{
			Brief: p.Description, ContentType: p.ContentType,
			TargetPlatform: p.TargetPlatform, Style: p.Style,
		}
		res, err := pl.PlanCustom(r.Context(), id, wfID, brief, nodes, nil) // TODO(Task 7): resolve typed custom-node registry entries (org-scoped) and pass them here
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, rt := range res.ReadyTodos {
			_, _ = ev.Append(r.Context(), id, "todo_ready", rt.ID, map[string]any{"type": rt.Type})
		}
		_ = ps.SetStatus(r.Context(), id, "running")
		writeJSON(w, http.StatusAccepted, map[string]any{
			"planId": res.PlanID, "valid": res.Valid, "fallbackUsed": res.FallbackUsed, "workflowId": wfID,
		})
	}
}
