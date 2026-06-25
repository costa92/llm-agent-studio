package worker

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// TestExprNodeProbe_Classify drives exprNodeProbe directly against seeded deps and
// asserts the per-variable classification recorded in the captured log buffer. It
// proves the probe exercises the REAL $node resolution path (via exprNodeResolver,
// S-2 enforced) the cut-over will use — NOT the old whole-output parity probe.
//
// F4: the buffer must carry metadata ONLY (no resolved values, no var Names).
func TestExprNodeProbe_Classify(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker $node shadow-probe tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)

	var projA string
	if err := db.WithContext(ctx).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_nodeprobe','pA','u') RETURNING id`,
	).Row().Scan(&projA); err != nil {
		t.Fatalf("seed project A: %v", err)
	}

	// --- dep 1: text dep -------------------------------------------------------
	// resolveOutputText("custom:"+coid) reads node_outputs.content WHERE id=coid.
	// itemsForDep(dText) reads the newest node_outputs row WHERE todo_id=dText for
	// the expr side. Seed BOTH consistently with a single node_outputs row whose
	// id == coid AND todo_id == dText, content='hello', format='text',
	// items=[{"json":{"text":"hello"}}], so:
	//   legacy: resolveOutputText("custom:"+coid) -> content 'hello'
	//   expr  : $node[dText].json.text           -> 'hello'  => exact
	dText := newID()
	coid := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,'custom:llm','hello','text',$4)`,
		coid, projA, dText, []byte(`[{"json":{"text":"hello"}}]`)).Error; err != nil {
		t.Fatalf("seed text dep node_output: %v", err)
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}')`,
		dText, projA, "custom:"+coid).Error; err != nil {
		t.Fatalf("seed text dep todo: %v", err)
	}

	// --- dep 2: json dep -------------------------------------------------------
	// format='json' -> accessor '.json' -> stringify re-marshals the object, which
	// may reorder keys vs the legacy content string, so the class is benign (or
	// exact if no reorder). Legacy reads node_outputs.content WHERE id=coid2.
	dJson := newID()
	coid2 := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,'custom:llm',$4,'json',$5)`,
		coid2, projA, dJson, `{"a":1,"b":2}`, []byte(`[{"json":{"a":1,"b":2}}]`)).Error; err != nil {
		t.Fatalf("seed json dep node_output: %v", err)
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}')`,
		dJson, projA, "custom:"+coid2).Error; err != nil {
		t.Fatalf("seed json dep todo: %v", err)
	}

	// --- dep 3: out-of-deps id (NOT in exec.depends_on) ------------------------
	// A real todo that exec does NOT depend on. The expr side routes through
	// exprNodeResolver, which denies an id outside exec's direct depends_on set
	// (S-2 fail-closed) -> exprErr -> divergent. (legacy side would resolve, but
	// classification only needs exprErr to flip to divergent.)
	dOut := newID()
	coid3 := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,'custom:llm','elsewhere','text',$4)`,
		coid3, projA, dOut, []byte(`[{"json":{"text":"elsewhere"}}]`)).Error; err != nil {
		t.Fatalf("seed out-of-deps node_output: %v", err)
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}')`,
		dOut, projA, "custom:"+coid3).Error; err != nil {
		t.Fatalf("seed out-of-deps todo: %v", err)
	}

	// --- exec: the executing custom todo, depends_on = {dText, dJson} (NOT dOut)
	exec := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
		 VALUES ($1,$2,'plan-x','custom:next','running',ARRAY[$3,$4]::text[],'{}')`,
		exec, projA, dText, dJson).Error; err != nil {
		t.Fatalf("seed exec todo: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	w := New(Config{
		DB:         db,
		Todos:      todos.New(db),
		Projects:   project.New(db),
		Events:     events.New(db),
		Logger:     logger,
		ExprParity: true,
		WorkerID:   "nodeprobe-test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})

	// Var names are arbitrary (the probe must NOT log them). Use distinctive names
	// so the no-leak assertion can prove they never appear.
	vars := []customVariable{
		{Name: "varNameTEXT_secret", SourceTodoId: dText}, // index 0 -> exact
		{Name: "varNameJSON_secret", SourceTodoId: dJson}, // index 1 -> exact|benign
		{Name: "varNameOUT_secret", SourceTodoId: dOut},   // index 2 -> divergent (S-2 denied)
	}

	w.exprNodeProbe(ctx, claimed{todoID: exec, projectID: projA}, vars)

	out := buf.String()
	if out == "" {
		t.Fatalf("expected probe log lines, got empty buffer")
	}

	classOf := func(varIdx int) (string, string) {
		t.Helper()
		// Find the probe line for this var_index and pull class + expr_err.
		re := regexp.MustCompile(`var_index=` + itoa(varIdx) + `\b.*`)
		loc := re.FindString(out)
		if loc == "" {
			t.Fatalf("no probe log line for var_index=%d in:\n%s", varIdx, out)
		}
		return fieldVal(loc, "class"), fieldVal(loc, "expr_err")
	}

	// case 1: text dep -> exact, expr_err=false
	if cls, ee := classOf(0); cls != "exact" {
		t.Fatalf("var_index=0 (text dep): want class=exact, got class=%q expr_err=%q\nlog:\n%s", cls, ee, out)
	} else if ee != "false" {
		t.Fatalf("var_index=0 (text dep): want expr_err=false, got %q\nlog:\n%s", ee, out)
	}

	// case 2: json dep -> exact|benign, expr_err=false
	if cls, ee := classOf(1); cls != "exact" && cls != "benign" {
		t.Fatalf("var_index=1 (json dep): want class in {exact,benign}, got class=%q\nlog:\n%s", cls, out)
	} else if ee != "false" {
		t.Fatalf("var_index=1 (json dep): want expr_err=false, got %q\nlog:\n%s", ee, out)
	}

	// case 3: out-of-deps -> divergent, expr_err=true (S-2 live path exercised)
	if cls, ee := classOf(2); cls != "divergent" {
		t.Fatalf("var_index=2 (out-of-deps): want class=divergent, got class=%q\nlog:\n%s", cls, out)
	} else if ee != "true" {
		t.Fatalf("var_index=2 (out-of-deps): want expr_err=true, got %q\nlog:\n%s", ee, out)
	}

	// case 4: no value leak — none of the resolved values nor var Names appear.
	for _, leak := range []string{"hello", `{"a":1,"b":2}`, "elsewhere",
		"varNameTEXT_secret", "varNameJSON_secret", "varNameOUT_secret"} {
		if strings.Contains(out, leak) {
			t.Fatalf("F4 violation: log contains forbidden token %q:\n%s", leak, out)
		}
	}
}

// itoa is a tiny int->string helper (avoids importing strconv just for tests).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// fieldVal extracts the value of slog text-handler key=value from a log line.
// Handles both bare values (class=exact) and the trailing-token case.
func fieldVal(line, key string) string {
	idx := strings.Index(line, key+"=")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(key)+1:]
	// value runs until the next space (slog text handler space-separates fields;
	// our values here are simple tokens: exact/benign/divergent, true/false, ints).
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		return rest[:sp]
	}
	return strings.TrimRight(rest, "\n")
}
