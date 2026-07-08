package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/nodedesc"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/runinputs"
	"github.com/costa92/llm-agent-studio/internal/workflows"
)

// maxRunInputsBody caps the run-with-inputs request body at the read layer, so
// a giant body can't blow up memory during decode (shared by all run paths).
const maxRunInputsBody = 64 << 10

// WorkflowStore is the workflows CRUD surface (satisfied by *workflows.Store).
type WorkflowStore interface {
	Create(ctx context.Context, projectID, name string, nodes, inputsSchema, settings json.RawMessage) (workflows.Workflow, error)
	Get(ctx context.Context, projectID, id string) (workflows.Workflow, error)
	ListByProject(ctx context.Context, projectID string) ([]workflows.Workflow, error)
	Update(ctx context.Context, projectID, id, name string, expectedVersion int, nodes, inputsSchema, settings json.RawMessage) (workflows.Workflow, error)
	Delete(ctx context.Context, projectID, id string) error
}

// workflowReq is the create/update body. nodes is the DAG (array of
// planner.WorkflowNode shape); inputsSchema is the design-time run-input
// declaration (array of runinputs.Field shape); settings is the workflow-level
// generation settings {style,contentType,targetPlatform}. All kept raw so the
// store stays decoupled.
type workflowReq struct {
	Name         string          `json:"name"`
	Nodes        json.RawMessage `json:"nodes"`
	InputsSchema json.RawMessage `json:"inputsSchema"`
	Settings     json.RawMessage `json:"settings"`
	// Version 是编辑保存（PUT）时客户端读到的乐观锁版本号；Store.Update 以它守卫，
	// 版本漂移 → 409。create 忽略此字段。缺省 0 —— 由于版本从 1 起，0 永不命中，
	// 缺省 version 的陈旧客户端会被 409 兜住而非无声覆盖（fail-closed）。
	Version int `json:"version"`
}

// workflowSettings is the decoded shape of workflows.settings: the workflow-level
// generation style/contentType/targetPlatform. Empty fields = 继承项目行（run 时
// 不覆盖）。仅 style 有白名单（必须是 prompt.Styles() 里的名字或空）。
type workflowSettings struct {
	Style          string `json:"style"`
	ContentType    string `json:"contentType"`
	TargetPlatform string `json:"targetPlatform"`
}

// validateWorkflowSettings runs the save-time check on a raw settings payload: it
// must decode to {style,contentType,targetPlatform} and, if style is non-empty,
// style must be a known prompt-library style name. An empty/absent settings is
// valid (= 继承项目行).
func validateWorkflowSettings(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var s workflowSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("invalid settings: %w", err)
	}
	if s.Style == "" {
		return nil
	}
	for _, st := range prompt.Styles() {
		if st.Name == s.Style {
			return nil
		}
	}
	return fmt.Errorf("unknown style %q", s.Style)
}

// validateInputsSchema runs the design-time (save-time) check on a raw
// inputs_schema payload: it unmarshals to []runinputs.Field then delegates to
// runinputs.ValidateSchema (name regex, type/target allowlist, select
// options). An empty/absent schema is valid.
func validateInputsSchema(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var fields []runinputs.Field
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("invalid inputs schema: %w", err)
	}
	return runinputs.ValidateSchema(fields)
}

// buildRunInputs serializes the immutable {values, schema} snapshot persisted to
// plans.run_inputs: values are the raw submitted inputs (empty map when none),
// schema is THIS run's inputs_schema snapshot (so replay/audit doesn't depend on
// the mutable workflows.inputs_schema). Shape matches the spec's plans.run_inputs.
func buildRunInputs(values map[string]json.RawMessage, schema json.RawMessage) json.RawMessage {
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	if len(schema) == 0 {
		schema = json.RawMessage("[]")
	}
	out, _ := json.Marshal(struct {
		Values map[string]json.RawMessage `json:"values"`
		Schema json.RawMessage            `json:"schema"`
	}{Values: values, Schema: schema})
	return out
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

// validateNodeParameterOverlays enforces, at SAVE time on an editor write path
// (W1 create/update, W2 create-project), the per-node parameters invariants:
// (a) no RegistryOnly key in the overlay (default-deny, fail-closed), and
// (b) the overlay's merged value shape is legal per the kind's validator. The
// run-time resolve+worker gates remain authoritative (W3 backfill / dirty JSON
// bypass save). A nil resolver → no typed-node overlay can be validated, so the
// check is skipped (focused tests that omit the registry); the run path still
// fails closed because resolveCustomTypes rejects typed nodes with a nil resolver.
// Errors are opaque by design: they name the offending node id + key, never the
// attacker-controlled value (url/secret/header/body), so nothing leaks.
func validateNodeParameterOverlays(ctx context.Context, res CustomNodeTypeResolver, orgID string, nodes []planner.WorkflowNode) error {
	if res == nil {
		return nil
	}
	for _, n := range nodes {
		if n.TypeId == "" || len(n.Parameters) == 0 {
			continue
		}
		ct, err := res.Get(ctx, n.TypeId, orgID)
		if err != nil {
			// Fail closed: cross-tenant / unknown typeId resolves to an error here.
			return fmt.Errorf("node %q: type unresolved", n.ID)
		}
		desc, ok := nodedesc.DescByKind(ct.Kind, n.TypeVersion)
		if !ok {
			return fmt.Errorf("node %q: typeVersion %d unsupported", n.ID, n.TypeVersion)
		}
		var overlay map[string]json.RawMessage
		if err := json.Unmarshal(n.Parameters, &overlay); err != nil {
			return fmt.Errorf("node %q: invalid parameters", n.ID)
		}
		registryOnly := map[string]bool{}
		for _, p := range desc.Properties {
			if p.Constraints != nil && p.Constraints.RegistryOnly {
				registryOnly[p.Name] = true
			}
		}
		for k := range overlay {
			if registryOnly[k] {
				return fmt.Errorf("node %q: parameter %q is registry-only and cannot be overridden", n.ID, k)
			}
		}
		// Merge onto base, then full validate (catches illegal non-dangerous values).
		merged, mErr := nodedesc.MergeOverlay(ct.Params, n.Parameters, desc)
		if mErr != nil {
			return fmt.Errorf("node %q: invalid parameters", n.ID)
		}
		if vErr := customnodetype.ValidateParams(ct.Kind, merged); vErr != nil {
			return fmt.Errorf("node %q: invalid params: %w", n.ID, vErr)
		}
	}
	return nil
}

// orgIDForProject loads the project's org via the project store. Used by the
// W1 editor write paths to scope the registry resolver (mirrors runWorkflowHandler).
func orgIDForProject(ctx context.Context, ps ProjectStore, projectID string) (string, error) {
	p, err := ps.Get(ctx, projectID)
	if err != nil {
		return "", err
	}
	return p.OrgID, nil
}

// createWorkflowHandler POST /api/projects/{id}/workflows — editor+.
func createWorkflowHandler(ps ProjectStore, ws WorkflowStore, res CustomNodeTypeResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workflowReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
		}
		if err := validateInputsSchema(req.InputsSchema); err != nil {
			http.Error(w, "invalid inputs schema: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateWorkflowSettings(req.Settings); err != nil {
			http.Error(w, "invalid settings: "+err.Error(), http.StatusBadRequest)
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
				orgID, err := orgIDForProject(r.Context(), ps, r.PathValue("id"))
				if err != nil {
					http.Error(w, "project not found", http.StatusNotFound)
					return
				}
				if err := validateNodeParameterOverlays(r.Context(), res, orgID, nodes); err != nil {
					http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		wf, err := ws.Create(r.Context(), r.PathValue("id"), req.Name, req.Nodes, req.InputsSchema, req.Settings)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, wf)
	}
}

// updateWorkflowHandler PUT /api/projects/{id}/workflows/{wfId} — editor+.
func updateWorkflowHandler(ps ProjectStore, ws WorkflowStore, res CustomNodeTypeResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req workflowReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "bad request: name required", http.StatusBadRequest)
			return
		}
		if err := validateInputsSchema(req.InputsSchema); err != nil {
			http.Error(w, "invalid inputs schema: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateWorkflowSettings(req.Settings); err != nil {
			http.Error(w, "invalid settings: "+err.Error(), http.StatusBadRequest)
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
				orgID, err := orgIDForProject(r.Context(), ps, r.PathValue("id"))
				if err != nil {
					http.Error(w, "project not found", http.StatusNotFound)
					return
				}
				if err := validateNodeParameterOverlays(r.Context(), res, orgID, nodes); err != nil {
					http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		wf, err := ws.Update(r.Context(), r.PathValue("id"), r.PathValue("wfId"), req.Name, req.Version, req.Nodes, req.InputsSchema, req.Settings)
		if errors.Is(err, workflows.ErrNotFound) {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, workflows.ErrVersionConflict) {
			// 并发编辑守卫：他人已保存导致 version 漂移。机器可读 code + 人话中文提示，
			// 前端据此提示重载并阻止盲存（外科手术式关掉 last-write-wins 数据丢失）。
			writeJSON(w, http.StatusConflict, map[string]any{
				"code":    "stale_version",
				"message": "工作流已被他人修改，请重新加载",
			})
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
// the resulting plan (run) is tagged with workflow_id. This is the sole run
// entry point (the legacy project-level POST /run was removed).
func runWorkflowHandler(ps ProjectStore, ws WorkflowStore, pl PlannerPort, ev EventAppender, cs CostStore, quota int, customTypeResolver CustomNodeTypeResolver) http.HandlerFunc {
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
			http.Error(w, "工作流没有任何节点，请先添加节点", http.StatusBadRequest)
			return
		}
		if err := planner.ValidateCustomGraph(nodes); err != nil {
			http.Error(w, "invalid workflow: "+err.Error(), http.StatusBadRequest)
			return
		}
		if planner.HasUnboundCustomNode(nodes) {
			http.Error(w, "当前 Workflow 包含未绑定类型的自定义节点，请先在注册表中为其指定类型后再运行", http.StatusBadRequest)
			return
		}
		// 运行期输入：读 body（带读取上限）→ 解析可选 {"inputs":...} → 按 workflow
		// 的 inputs_schema 校验。整段必须在 SetStatus("planning")/planner_started
		// 之前，校验失败 → 400，避免项目悬挂在 planning。
		r.Body = http.MaxBytesReader(w, r.Body, maxRunInputsBody)
		var runReq struct {
			Inputs map[string]json.RawMessage `json:"inputs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&runReq); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid run inputs: "+err.Error(), http.StatusBadRequest)
			return
		}
		var schema []runinputs.Field
		if len(wf.InputsSchema) > 0 {
			if err := json.Unmarshal(wf.InputsSchema, &schema); err != nil {
				http.Error(w, "invalid inputs schema: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		runResolved, verr := runinputs.Validate(schema, runReq.Inputs)
		if verr != nil {
			http.Error(w, "invalid run inputs: "+verr.Error(), http.StatusBadRequest)
			return
		}
		// 注册表解析：只读 org 注册表、不依赖状态翻转，其 resolved 结果供下方 PlanCustom。
		// 必须放在 TryBeginRun（翻 planning）之前——它可能 400，若在翻转之后失败会把项目
		// 悬挂在 planning，而 TryBeginRun 又拒绝 planning，导致该项目被永久 409 锁死无法再跑。
		resolved, rerr := resolveCustomTypes(r.Context(), customTypeResolver, p.OrgID, nodes)
		if rerr != nil {
			http.Error(w, rerr.Error(), http.StatusBadRequest)
			return
		}
		// 配额检查是「先读后判」，跨请求仍有 TOCTOU（同 org 不同项目并发可竞争过闸）。
		// 默认 quota=0（不限）；把每-org 配额做成原子（advisory lock / 条件插入）成本高，
		// 且成本账本由 worker 逐次生成时写、运行入口无单一插入点——故此处不改配额原子性。
		// 但下方 TryBeginRun 的单项目并发门禁已把「同一项目双跑」这条主要竞态关掉。
		// TODO(quota-atomicity)：若将来 per-org 配额需强一致，改为 org 级 advisory lock。
		if over, err := quotaExceeded(r.Context(), cs, quota, p.OrgID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if over {
			http.Error(w, "generation quota exceeded for org", http.StatusTooManyRequests)
			return
		}
		// 并发/幂等门禁：原子 CAS 把项目翻到 planning。项目已在 planning/running 时
		// 返回 409（一个项目同一时刻只允许一个在途 run）。所有会 400 的校验（含上面的
		// resolveCustomTypes）都在此之前完成——校验失败不翻状态，不会悬挂在 planning。
		// 翻转之后唯一还会失败的步骤是 PlanCustom（500），失败时下方回滚状态以免锁死。
		if ok, err := ps.TryBeginRun(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if !ok {
			http.Error(w, "该项目已有运行进行中，请等待其完成或取消后再运行", http.StatusConflict)
			return
		}
		_, _ = ev.Append(r.Context(), id, "planner_started", "", nil)
		brief := planner.Brief{
			Brief: p.Description, ContentType: p.ContentType,
			TargetPlatform: p.TargetPlatform, Style: p.Style,
		}
		// 优先级：run-input override > workflow.settings > project 行 > 无。此处叠加
		// workflow.settings（覆盖 project 行）——纯内存操作、作用于已加载的 wf.Settings，
		// 不新增 TryBeginRun 之前的失败点（本段已在翻转之后）；settings 非空字段才覆盖
		//（'{}' / 空字段 = 继承项目行）。下方 BriefOverride 叠加保持在最后（run > workflow）。
		if len(wf.Settings) > 0 {
			var s workflowSettings
			if json.Unmarshal(wf.Settings, &s) == nil {
				if s.Style != "" {
					brief.Style = s.Style
				}
				if s.ContentType != "" {
					brief.ContentType = s.ContentType
				}
				if s.TargetPlatform != "" {
					brief.TargetPlatform = s.TargetPlatform
				}
			}
		}
		// brief override：仅本 run 叠加，不写回 projects。
		if v, ok := runResolved.BriefOverride["brief"]; ok {
			brief.Brief = v
		}
		if v, ok := runResolved.BriefOverride["contentType"]; ok {
			brief.ContentType = v
		}
		if v, ok := runResolved.BriefOverride["targetPlatform"]; ok {
			brief.TargetPlatform = v
		}
		if v, ok := runResolved.BriefOverride["style"]; ok {
			brief.Style = v
		}
		result, err := pl.PlanCustom(r.Context(), id, wfID, brief, nodes, resolved, buildRunInputs(runReq.Inputs, wf.InputsSchema))
		if err != nil {
			// 建 plan 失败：把项目从 planning 回滚到 failed（非在途终态），否则 TryBeginRun
			// 会一直拒绝，项目被永久 409 锁死无法再跑。failed 不属于 planning/running，可再运行。
			_ = ps.SetStatus(r.Context(), id, "failed")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, rt := range result.ReadyTodos {
			_, _ = ev.Append(r.Context(), id, "todo_ready", rt.ID, map[string]any{"type": rt.Type})
		}
		_ = ps.SetStatus(r.Context(), id, "running")
		writeJSON(w, http.StatusAccepted, map[string]any{
			"planId": result.PlanID, "valid": result.Valid, "fallbackUsed": result.FallbackUsed, "workflowId": wfID,
		})
	}
}
