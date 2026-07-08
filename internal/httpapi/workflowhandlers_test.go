package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/workflows"
)

// stubWorkflows is a configurable WorkflowStore for handler tests.
type stubWorkflows struct {
	getErr   error
	got      workflows.Workflow
	created  workflows.Workflow
	createIn struct {
		projectID, name string
		nodes           json.RawMessage
		inputsSchema    json.RawMessage
		settings        json.RawMessage
	}
}

func (s *stubWorkflows) Create(_ context.Context, projectID, name string, nodes, inputsSchema, settings json.RawMessage) (workflows.Workflow, error) {
	s.createIn.projectID, s.createIn.name, s.createIn.nodes, s.createIn.inputsSchema, s.createIn.settings = projectID, name, nodes, inputsSchema, settings
	return workflows.Workflow{ID: "wf1", ProjectID: projectID, Name: name, Nodes: nodes, InputsSchema: inputsSchema, Settings: settings}, nil
}
func (s *stubWorkflows) Get(_ context.Context, _, id string) (workflows.Workflow, error) {
	if s.getErr != nil {
		return workflows.Workflow{}, s.getErr
	}
	w := s.got
	w.ID = id
	return w, nil
}
func (s *stubWorkflows) ListByProject(_ context.Context, _ string) ([]workflows.Workflow, error) {
	return []workflows.Workflow{{ID: "wf1", Name: "a"}}, nil
}
func (s *stubWorkflows) Update(_ context.Context, projectID, id, name string, expectedVersion int, nodes, inputsSchema, settings json.RawMessage) (workflows.Workflow, error) {
	return workflows.Workflow{ID: id, ProjectID: projectID, Name: name, Version: expectedVersion, Nodes: nodes, InputsSchema: inputsSchema, Settings: settings}, nil
}
func (s *stubWorkflows) Delete(_ context.Context, _, _ string) error { return nil }

// recordingPlanner captures the workflowID, brief and runInputs passed to PlanCustom.
type recordingPlanner struct {
	gotWorkflowID string
	gotBrief      planner.Brief
	gotRunInputs  json.RawMessage
}

func (recordingPlanner) Plan(_ context.Context, _ string, _ planner.Brief, _ json.RawMessage) (planner.Result, error) {
	return planner.Result{PlanID: "pl"}, nil
}
func (recordingPlanner) PlanWith(_ context.Context, _ string, _ llm.ChatModel, _ planner.Brief, _ json.RawMessage) (planner.Result, error) {
	return planner.Result{PlanID: "pl"}, nil
}

func TestCreateWorkflowHandlerRejectsEmptyName(t *testing.T) {
	h := createWorkflowHandler(stubProjects{orgID: "o1"}, &stubWorkflows{}, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(`{"name":""}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty name should 400, got %d", rr.Code)
	}
}

func TestCreateWorkflowHandlerHappy(t *testing.T) {
	ws := &stubWorkflows{}
	h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
	body := `{"name":"工作流 A","nodes":[{"id":"n1","type":"script","promptId":"","dependsOn":[]}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ws.createIn.projectID != "p1" || ws.createIn.name != "工作流 A" {
		t.Fatalf("create args mismatch: %+v", ws.createIn)
	}
}

// TestCreateWorkflowSettings 验证工作流级 settings 的保存期校验 + 透传：合法 style
// （prompt 库名或空）200 且原样传给 store；未知 style 400 且 Create 不被调用。
func TestCreateWorkflowSettings(t *testing.T) {
	t.Run("合法 style 透传", func(t *testing.T) {
		ws := &stubWorkflows{}
		h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
		body := `{"name":"wf","settings":{"style":"皮克斯","contentType":"短视频"}}`
		req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
		req.SetPathValue("id", "p1")
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("create should 200, got %d: %s", rr.Code, rr.Body.String())
		}
		if !sameJSONBytes(ws.createIn.settings, json.RawMessage(`{"style":"皮克斯","contentType":"短视频"}`)) {
			t.Fatalf("settings not passed through: %s", ws.createIn.settings)
		}
	})
	t.Run("空 settings 合法", func(t *testing.T) {
		ws := &stubWorkflows{}
		h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
		req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(`{"name":"wf","settings":{}}`))
		req.SetPathValue("id", "p1")
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("empty settings should 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})
	t.Run("未知 style 400", func(t *testing.T) {
		ws := &cycleRejectingWorkflows{t: t} // Create must NOT be called
		h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
		req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(`{"name":"wf","settings":{"style":"不存在的风格"}}`))
		req.SetPathValue("id", "p1")
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("unknown style should 400, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

// TestCreateWorkflowRejectsInvalidInputsSchema verifies store-time schema
// validation: an illegal inputs_schema is rejected with 400 BEFORE the store
// write (Create must not be called).
func TestCreateWorkflowRejectsInvalidInputsSchema(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{"bad name", `[{"name":"1bad","type":"text","target":"variable"}]`},
		{"select no options", `[{"name":"voice","type":"select","target":"variable"}]`},
		{"unknown type", `[{"name":"x","type":"color","target":"variable"}]`},
		{"unknown target", `[{"name":"x","type":"text","target":"secret"}]`},
		{"retired multiselect type", `[{"name":"themes","type":"multiselect","target":"variable","options":[{"value":"a"}]}]`},
		{"retired pbConfig target", `[{"name":"voice","type":"text","target":"pbConfig"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := &cycleRejectingWorkflows{t: t} // Create must NOT be called
			h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
			body := `{"name":"wf1","inputsSchema":` + tc.schema + `}`
			req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
			req.SetPathValue("id", "p1")
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("invalid inputs_schema should 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestCreateWorkflowAcceptsValidInputsSchema verifies a legal schema persists
// (passed through to the store), and an empty/absent schema also passes.
func TestCreateWorkflowAcceptsValidInputsSchema(t *testing.T) {
	ws := &stubWorkflows{}
	h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
	schema := `[{"name":"heroName","type":"text","target":"variable","required":true}]`
	body := `{"name":"wf1","inputsSchema":` + schema + `}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid schema should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !sameJSONBytes(ws.createIn.inputsSchema, json.RawMessage(schema)) {
		t.Fatalf("store did not receive inputs_schema: %q", ws.createIn.inputsSchema)
	}
}

func TestUpdateWorkflowRejectsInvalidInputsSchema(t *testing.T) {
	ws := &cycleRejectingWorkflows{t: t} // Update must NOT be called
	h := updateWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
	body := `{"name":"wf1","inputsSchema":[{"name":"x","type":"select","target":"variable"}]}`
	req := httptest.NewRequest("PUT", "/api/projects/p1/workflows/w1", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "w1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid inputs_schema update should 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func sameJSONBytes(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

func TestRunWorkflowHandlerNotFound(t *testing.T) {
	ws := &stubWorkflows{getErr: workflows.ErrNotFound}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/missing/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "missing")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing workflow run should 404, got %d", rr.Code)
	}
}

func TestRunWorkflowHandlerPassesWorkflowID(t *testing.T) {
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "wf",
		Nodes: json.RawMessage(`[{"id":"n1","type":"script","promptId":"","dependsOn":[]}]`),
	}}
	rp := &recordingPlanner{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, rp, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("run should 202, got %d: %s", rr.Code, rr.Body.String())
	}
	if rp.gotWorkflowID != "wfX" {
		t.Fatalf("PlanCustom got workflowID %q, want wfX", rp.gotWorkflowID)
	}
}

func TestRunWorkflowHandlerEmptyNodes(t *testing.T) {
	ws := &stubWorkflows{got: workflows.Workflow{Name: "wf", Nodes: json.RawMessage(`[]`)}}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty-nodes run should 400, got %d", rr.Code)
	}
}

// TestCreateWorkflow_RejectsCycle verifies that save-time validation rejects a
// cyclic graph with 400 and does NOT call WorkflowStore.Create.
func TestCreateWorkflow_RejectsCycle(t *testing.T) {
	// ws.Create will fail the test if called — cycle must be caught before the store.
	ws := &cycleRejectingWorkflows{t: t}
	h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
	// A↔B cycle.
	body := `{"name":"cycle-wf","nodes":[{"id":"A","type":"script","dependsOn":["B"]},{"id":"B","type":"storyboard","dependsOn":["A"]}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cyclic workflow should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cycle") {
		t.Fatalf("body should mention \"cycle\", got: %s", rr.Body.String())
	}
}

func TestUpdateWorkflow_RejectsCycle(t *testing.T) {
	// ws.Update will fail the test if called — cycle must be caught before the store.
	ws := &cycleRejectingWorkflows{t: t}
	h := updateWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
	body := `{"name":"cycle-wf","nodes":[{"id":"A","type":"script","dependsOn":["B"]},{"id":"B","type":"storyboard","dependsOn":["A"]}]}`
	req := httptest.NewRequest("PUT", "/api/projects/p1/workflows/w1", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "w1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cyclic workflow should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cycle") {
		t.Fatalf("body should mention \"cycle\", got: %s", rr.Body.String())
	}
}

// cycleRejectingWorkflows is a WorkflowStore stub whose Create fails the test
// (to prove validation happens BEFORE the store write).
type cycleRejectingWorkflows struct {
	t *testing.T
}

func (s *cycleRejectingWorkflows) Create(_ context.Context, _, _ string, _, _, _ json.RawMessage) (workflows.Workflow, error) {
	s.t.Fatal("Create must not be called when the graph is invalid")
	return workflows.Workflow{}, nil
}
func (s *cycleRejectingWorkflows) Get(_ context.Context, _, id string) (workflows.Workflow, error) {
	return workflows.Workflow{ID: id}, nil
}
func (s *cycleRejectingWorkflows) ListByProject(_ context.Context, _ string) ([]workflows.Workflow, error) {
	return nil, nil
}
func (s *cycleRejectingWorkflows) Update(_ context.Context, _, id, name string, expectedVersion int, nodes, inputsSchema, settings json.RawMessage) (workflows.Workflow, error) {
	s.t.Fatal("Update must not be called when the graph is invalid")
	return workflows.Workflow{}, nil
}
func (s *cycleRejectingWorkflows) Delete(_ context.Context, _, _ string) error { return nil }

// trackingAppender records how many times Append was called so tests can assert
// "no planner_started was emitted".
type trackingAppender struct{ count int }

func (a *trackingAppender) Append(_ context.Context, _, _, _ string, _ any) (int64, error) {
	a.count++
	return int64(a.count), nil
}

// TestRunWorkflow_CyclicReturns400 verifies that running a workflow with a
// cyclic graph returns 400 (not 500) and does NOT emit planner_started.
func TestRunWorkflow_CyclicReturns400(t *testing.T) {
	// WorkflowStore returns a workflow whose nodes form a cycle.
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "cyclic-wf",
		Nodes: json.RawMessage(`[{"id":"A","type":"script","dependsOn":["B"]},{"id":"B","type":"storyboard","dependsOn":["A"]}]`),
	}}
	ev := &trackingAppender{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, ev, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfCycle/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfCycle")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cyclic run should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if ev.count != 0 {
		t.Fatalf("planner_started must not be emitted for cyclic run (got %d Append calls)", ev.count)
	}
}

func TestRunWorkflowHandlerRefusesCustomNodes(t *testing.T) {
	nodes, _ := json.Marshal([]planner.WorkflowNode{
		{ID: "s1", Type: "script"},
		{ID: "c1", Type: "custom:translate", DependsOn: []string{"s1"}},
	})
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "wf-custom",
		Nodes: json.RawMessage(nodes),
	}}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfC/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfC")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("custom-node run should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "未绑定类型") {
		t.Fatalf("body should contain \"未绑定类型\", got: %s", rr.Body.String())
	}
}

func (rp *recordingPlanner) PlanCustom(_ context.Context, _, workflowID string, b planner.Brief, _ []planner.WorkflowNode, _ map[string]planner.ResolvedType, runInputs json.RawMessage) (planner.Result, error) {
	rp.gotWorkflowID = workflowID
	rp.gotBrief = b
	rp.gotRunInputs = runInputs
	return planner.Result{PlanID: "pl", Valid: true, ReadyTodos: []planner.ReadyTodo{{ID: "t1", Type: "script"}}}, nil
}

// scriptNode is the standard single valid node used across run tests.
const scriptNode = `[{"id":"n1","type":"script","promptId":"","dependsOn":[]}]`

// TestRunWorkflowAppliesInputs: a legal body with a brief-target field and a
// variable-target field → PlanCustom receives the overridden brief and a
// {values,schema} run_inputs snapshot carrying both submitted values.
func TestRunWorkflowAppliesInputs(t *testing.T) {
	schema := `[{"name":"heroBrief","type":"text","target":"brief"},{"name":"heroName","type":"text","target":"variable"}]`
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:         "wf",
		Nodes:        json.RawMessage(scriptNode),
		InputsSchema: json.RawMessage(schema),
	}}
	rp := &recordingPlanner{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, rp, stubAppender{}, &stubCost{count: 0}, 100, nil)
	body := `{"inputs":{"heroBrief":"勇敢的小熊","heroName":"阿力"}}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("run-with-inputs should 202, got %d: %s", rr.Code, rr.Body.String())
	}
	if rp.gotBrief.Brief != "勇敢的小熊" {
		t.Fatalf("brief override not applied: got %q", rp.gotBrief.Brief)
	}
	// run_inputs snapshot must carry both submitted values + the schema snapshot.
	var snap struct {
		Values map[string]json.RawMessage `json:"values"`
		Schema json.RawMessage            `json:"schema"`
	}
	if err := json.Unmarshal(rp.gotRunInputs, &snap); err != nil {
		t.Fatalf("runInputs not valid json: %v (%s)", err, rp.gotRunInputs)
	}
	if _, ok := snap.Values["heroBrief"]; !ok {
		t.Fatalf("runInputs.values missing heroBrief: %s", rp.gotRunInputs)
	}
	if _, ok := snap.Values["heroName"]; !ok {
		t.Fatalf("runInputs.values missing heroName: %s", rp.gotRunInputs)
	}
	if !sameJSONBytes(snap.Schema, json.RawMessage(schema)) {
		t.Fatalf("runInputs.schema snapshot mismatch: %s", snap.Schema)
	}
}

// TestRunWorkflowStylePrecedence 验证风格解析四级优先级（在 run handler 内一次解析）：
// run-input override > workflow.settings > project 行 > 无。断言 PlanCustom 收到的
// brief.Style 与每一级一致，且每次都正常 202（settings 叠加不改变翻转/校验时序）。
func TestRunWorkflowStylePrecedence(t *testing.T) {
	// styleSchema 声明一个 target=style 的运行期输入，供最高优先级（run-input）用例覆盖。
	const styleSchema = `[{"name":"styleIn","type":"text","target":"style"}]`
	cases := []struct {
		name     string
		project  stubProjects
		settings string
		body     string
		want     string
	}{
		{
			name:    "仅 project 行",
			project: stubProjects{orgID: "o1", style: "国风"},
			want:    "国风",
		},
		{
			name:     "workflow.settings 覆盖 project",
			project:  stubProjects{orgID: "o1", style: "国风"},
			settings: `{"style":"皮克斯"}`,
			want:     "皮克斯",
		},
		{
			name:     "settings='{}' 落回 project 行（零回归）",
			project:  stubProjects{orgID: "o1", style: "国风"},
			settings: `{}`,
			want:     "国风",
		},
		{
			name:     "run-input 覆盖 workflow.settings（最高优先级）",
			project:  stubProjects{orgID: "o1", style: "国风"},
			settings: `{"style":"皮克斯"}`,
			body:     `{"inputs":{"styleIn":"写实"}}`,
			want:     "写实",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := &stubWorkflows{got: workflows.Workflow{
				Name:         "wf",
				Nodes:        json.RawMessage(scriptNode),
				InputsSchema: json.RawMessage(styleSchema),
				Settings:     json.RawMessage(tc.settings),
			}}
			rp := &recordingPlanner{}
			ev := &trackingAppender{}
			h := runWorkflowHandler(tc.project, ws, rp, ev, &stubCost{count: 0}, 100, nil)
			var req *http.Request
			if tc.body == "" {
				req = httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
			} else {
				req = httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", strings.NewReader(tc.body))
			}
			req.SetPathValue("id", "p1")
			req.SetPathValue("wfId", "wfX")
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("run should 202, got %d: %s", rr.Code, rr.Body.String())
			}
			if rp.gotBrief.Style != tc.want {
				t.Fatalf("brief.Style precedence: got %q want %q", rp.gotBrief.Style, tc.want)
			}
			// 时序不变式：planner_started 恰好一次（校验全通过后才翻转），PlanCustom 已触发。
			if ev.count == 0 {
				t.Fatalf("planner_started should fire once on a valid run")
			}
			if rp.gotWorkflowID == "" {
				t.Fatalf("PlanCustom should fire on a valid run")
			}
		})
	}
}

// TestRunWorkflowMalformedSettingsNoFailure：即便 settings 是坏 JSON（保存期校验兜不住
// 的极端情形），叠加也只是跳过、不新增 TryBeginRun 之前/之后的失败点——run 仍 202，
// 落回 project 行的风格。证明 settings 叠加是纯内存、不会把项目悬挂在 planning。
func TestRunWorkflowMalformedSettingsNoFailure(t *testing.T) {
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:     "wf",
		Nodes:    json.RawMessage(scriptNode),
		Settings: json.RawMessage(`{bad json`),
	}}
	rp := &recordingPlanner{}
	h := runWorkflowHandler(stubProjects{orgID: "o1", style: "国风"}, ws, rp, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("malformed settings should not fail the run, got %d: %s", rr.Code, rr.Body.String())
	}
	if rp.gotBrief.Style != "国风" {
		t.Fatalf("malformed settings should fall back to project style, got %q", rp.gotBrief.Style)
	}
}

// TestRunWorkflowRejectsInvalidInputs: a required field missing → 400, and
// neither planner_started (event) nor PlanCustom fires (validation precedes
// all side effects, so the project never hangs in "planning").
func TestRunWorkflowRejectsInvalidInputs(t *testing.T) {
	schema := `[{"name":"heroName","type":"text","target":"variable","required":true}]`
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:         "wf",
		Nodes:        json.RawMessage(scriptNode),
		InputsSchema: json.RawMessage(schema),
	}}
	rp := &recordingPlanner{}
	ev := &trackingAppender{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, rp, ev, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", strings.NewReader(`{"inputs":{}}`))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid inputs should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if ev.count != 0 {
		t.Fatalf("planner_started must not fire on invalid inputs (got %d Append calls)", ev.count)
	}
	if rp.gotWorkflowID != "" {
		t.Fatalf("PlanCustom must not be called on invalid inputs")
	}
}

// TestRunWorkflowEnumOutOfRange: a select value outside its options → 400.
func TestRunWorkflowEnumOutOfRange(t *testing.T) {
	schema := `[{"name":"tone","type":"select","target":"style","options":[{"value":"warm"}]}]`
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:         "wf",
		Nodes:        json.RawMessage(scriptNode),
		InputsSchema: json.RawMessage(schema),
	}}
	ev := &trackingAppender{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, ev, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", strings.NewReader(`{"inputs":{"tone":"cold"}}`))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("enum-out-of-range should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if ev.count != 0 {
		t.Fatalf("planner_started must not fire on invalid inputs (got %d)", ev.count)
	}
}

// TestRunWorkflowBodyTooLarge: a body over the 64KB read cap → 400 at the
// MaxBytesReader read layer (never reaches Validate/planner).
func TestRunWorkflowBodyTooLarge(t *testing.T) {
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "wf",
		Nodes: json.RawMessage(scriptNode),
	}}
	ev := &trackingAppender{}
	huge := strings.Repeat("x", 70*1024)
	body := `{"inputs":{"x":"` + huge + `"}}`
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, ev, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized body should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if ev.count != 0 {
		t.Fatalf("planner_started must not fire on oversized body (got %d)", ev.count)
	}
}

// TestRunWorkflowEmptyBodyNoRegression: no body (today's behavior) → run
// proceeds; PlanCustom receives an empty {values,schema} snapshot.
func TestRunWorkflowEmptyBodyNoRegression(t *testing.T) {
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "wf",
		Nodes: json.RawMessage(scriptNode),
	}}
	rp := &recordingPlanner{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, rp, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("empty-body run should 202, got %d: %s", rr.Code, rr.Body.String())
	}
	var snap struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(rp.gotRunInputs, &snap); err != nil {
		t.Fatalf("runInputs not valid json: %v (%s)", err, rp.gotRunInputs)
	}
	if len(snap.Values) != 0 {
		t.Fatalf("empty body should yield empty values, got %s", rp.gotRunInputs)
	}
}

// TestCreateWorkflowRejectsRegistryOnlyOverlay (W1 create) [DB]: a node overlay
// that smuggles a RegistryOnly field (http url retargeted + allowResponseBody:true)
// must be rejected with 400 at SAVE, before the store write.
func TestCreateWorkflowRejectsRegistryOnlyOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "fetch", Kind: "http", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ws := &cycleRejectingWorkflows{t: t} // Create must NOT be called
	h := createWorkflowHandler(stubProjects{orgID: org}, ws, store)
	body := `{"name":"wf1","nodes":[{"id":"n1","type":"custom:fetch","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"url":"http://attacker/collect","allowResponseBody":true}}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("RegistryOnly overlay must be rejected at create, got %d: %s", rr.Code, rr.Body.String())
	}
	// Opaque: the rejected attacker URL must NOT leak into the response body.
	if strings.Contains(rr.Body.String(), "attacker") {
		t.Fatalf("error body leaked attacker payload: %s", rr.Body.String())
	}
}

// TestUpdateWorkflowRejectsRegistryOnlyOverlay (W1 update) [DB].
func TestUpdateWorkflowRejectsRegistryOnlyOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"code": "print(1)", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "code", Kind: "script", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ws := &cycleRejectingWorkflows{t: t} // Update must NOT be called
	h := updateWorkflowHandler(stubProjects{orgID: org}, ws, store)
	body := `{"name":"wf1","nodes":[{"id":"n1","type":"custom:code","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"code":"x = {{secret:K}}"}}]}`
	req := httptest.NewRequest("PUT", "/api/projects/p1/workflows/w1", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "w1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("RegistryOnly overlay must be rejected at update, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestCreateWorkflowAcceptsCleanOverlay (W1 create) [DB]: a non-dangerous overlay
// (outputFormat=json) merges cleanly and the save proceeds to the store.
func TestCreateWorkflowAcceptsCleanOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "fetch", Kind: "http", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ws := &stubWorkflows{}
	h := createWorkflowHandler(stubProjects{orgID: org}, ws, store)
	body := `{"name":"wf1","nodes":[{"id":"n1","type":"custom:fetch","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"outputFormat":"json"}}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clean overlay should 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
