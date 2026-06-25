package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/expr"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// recordingSecrets is a SecretResolver spy that records every Resolve call so the
// parity probe can be proven to never resolve secrets.
type recordingSecrets struct {
	calls int
}

func (r *recordingSecrets) Resolve(ctx context.Context, orgID, name string) (string, error) {
	r.calls++
	return "RESOLVED_" + name, nil
}

// resolveViaExpr replicates exactly what exprParityCheck computes for the
// {{name}} → {{ $json.<name> }} channel, but returns the rendered value so the
// test can compare it to substituteVars. Self-only context (no $node).
func resolveViaExpr(t *testing.T, tpl string, replacer map[string]string) string {
	t.Helper()
	names := make([]string, 0, len(replacer))
	for n := range replacer {
		names = append(names, n)
	}
	exprTpl := toExprTemplate(tpl, names)
	selfJSON, err := json.Marshal(replacer)
	if err != nil {
		t.Fatalf("marshal replacer: %v", err)
	}
	got, err := expr.Resolve(exprTpl, expr.Context{Self: []expr.Item{{JSON: selfJSON}}})
	if err != nil {
		t.Fatalf("expr.Resolve(%q): %v", exprTpl, err)
	}
	return got
}

func TestExprParity_NameChannel(t *testing.T) {
	cases := []struct {
		name     string
		tpl      string
		replacer map[string]string
		expected string
	}{
		{"single", "Hello {{name}}", map[string]string{"name": "Ada"}, "Hello Ada"},
		{"spaced-and-unspaced", "{{ greeting }}, {{name}}!", map[string]string{"greeting": "Hi", "name": "Ada"}, "Hi, Ada!"},
		{"multi-occurrence", "{{x}}-{{x}}", map[string]string{"x": "7"}, "7-7"},
		{"no-vars", "static text", map[string]string{}, "static text"},
		{"secret-literal-stays", "call {{secret:STRIPE}} for {{name}}", map[string]string{"name": "Ada"}, "call {{secret:STRIPE}} for Ada"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := substituteVars(tc.tpl, tc.replacer)
			got := resolveViaExpr(t, tc.tpl, tc.replacer)
			if got != legacy {
				t.Fatalf("expr diverges from substituteVars: expr=%q legacy=%q", got, legacy)
			}
			if got != tc.expected {
				t.Fatalf("expr result %q != expected %q", got, tc.expected)
			}
			if tc.name == "secret-literal-stays" && !strings.Contains(got, "{{secret:STRIPE}}") {
				t.Fatalf("expr output must contain verbatim secret span, got %q", got)
			}
		})
	}
}

// TestExprParity_HTTPChannel_NameEquivalence (NO DB) proves the expr engine's
// {{name}} channel equals substituteVars for the header/body template shapes the
// HTTP probe operates on — including the security-critical case that {{secret:...}}
// stays VERBATIM (the probe never resolves a secret).
func TestExprParity_HTTPChannel_NameEquivalence(t *testing.T) {
	cases := []struct {
		name     string
		tpl      string
		nameVals map[string]string
		expected string
	}{
		{"bearer-token", "Bearer {{token}}", map[string]string{"token": "abc"}, "Bearer abc"},
		{"two-vars", "{{a}}/{{b}}", map[string]string{"a": "1", "b": "2"}, "1/2"},
		{"secret-stays-literal", "Bearer {{secret:KEY}} {{tok}}", map[string]string{"tok": "T"}, "Bearer {{secret:KEY}} T"},
		{"json-body", `{"q":"{{q}}"}`, map[string]string{"q": "hi"}, `{"q":"hi"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := substituteVars(tc.tpl, tc.nameVals)
			got := resolveViaExpr(t, tc.tpl, tc.nameVals)
			if got != legacy {
				t.Fatalf("expr diverges from substituteVars: expr=%q legacy=%q", got, legacy)
			}
			if got != tc.expected {
				t.Fatalf("expr result %q != expected %q", got, tc.expected)
			}
			if tc.name == "secret-stays-literal" {
				if !strings.Contains(got, "{{secret:KEY}}") {
					t.Fatalf("probe must leave {{secret:KEY}} verbatim, got %q", got)
				}
				if got != substituteVars(tc.tpl, tc.nameVals) {
					t.Fatalf("probe must equal substituteVars on the RAW template (never resolving the secret), got %q", got)
				}
			}
		})
	}
}

// TestExprParity_HTTPChannel_WiringFiresAndIsSafe drives runCustomHTTP with
// ExprParity=true and a captured logger, an http node carrying BOTH a {{name}} var
// (resolving to a sensitive upstream value) AND a {{secret:KEY}} header, plus a
// {{name}} body. It asserts the probe fires for http.header.* AND http.body, that
// the run still succeeds, and that the log NEVER contains the sensitive name value
// or any resolved secret plaintext (F4 — the probe operates on RAW templates only).
func TestExprParity_HTTPChannel_WiringFiresAndIsSafe(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	const sensitiveName = "SENSITIVE_NAME_VALUE"
	const secretPlain = "RESOLVED_SECRET_PLAINTEXT"
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_http_" + randHex3()
	projID, c := seedHTTPProjectTodo(t, db, orgID)

	// Seed an upstream node whose text output is the sensitive name value, bound to
	// the {{up}} {{name}} channel used in a header AND the body.
	upOutID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
		 VALUES ($1,$2,'t-up','custom:up',$3,'text')`,
		upOutID, projID, sensitiveName).Error; err != nil {
		t.Fatalf("seed upstream node_output: %v", err)
	}
	upTodoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:up','done',$3,'{}')`,
		upTodoID, projID, "custom:"+upOutID).Error; err != nil {
		t.Fatalf("seed upstream todo: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	secrets := &recordingSecrets{}
	doer := &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	w := New(Config{
		DB:          db,
		Todos:       todos.New(db),
		Projects:    project.New(db),
		Events:      events.New(db),
		Secrets:     secrets,
		HTTPFetcher: doer,
		Logger:      logger,
		ExprParity:  true,
		WorkerID:    "http-parity-test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})

	ref, err := w.runCustomHTTP(ctx, c, httpParams{
		Method:       "POST",
		URL:          "https://api.example.com",
		Headers:      map[string]string{"X-Up": "value-{{up}}", "Authorization": "Bearer {{secret:KEY}}"},
		BodyTemplate: `{"u":"{{up}}"}`,
		Variables:    []customVariable{{Name: "up", SourceTodoId: upTodoID}},
	})
	if err != nil {
		t.Fatalf("runCustomHTTP: %v", err)
	}
	if !strings.HasPrefix(ref, "custom:") {
		t.Fatalf("want custom: output ref, got %q", ref)
	}

	out := buf.String()
	// Probe must fire for both header keys AND the body.
	for _, want := range []string{"http.header.X-Up", "http.header.Authorization", "http.body"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected parity probe log for %q, log:\n%s", want, out)
		}
	}
	// F4: the log must NOT carry the sensitive {{name}} value or resolved secret.
	if strings.Contains(out, sensitiveName) {
		t.Fatalf("F4 violation: sensitive name value leaked into log:\n%s", out)
	}
	if strings.Contains(out, secretPlain) || strings.Contains(out, "RESOLVED_KEY") {
		t.Fatalf("F4 violation: resolved secret plaintext leaked into log:\n%s", out)
	}
}

func TestExprParity_NoSecretsResolveAndSafeLog(t *testing.T) {
	var buf bytes.Buffer
	spy := &recordingSecrets{}
	w := &Worker{cfg: Config{
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
		Secrets: spy,
	}}

	const sensitive = "SENSITIVE_VALUE_XYZ"
	w.exprParityCheck(
		context.Background(),
		claimed{todoID: "t-1"},
		"user",
		"render {{name}}",
		"render "+sensitive,
		map[string]string{"name": sensitive},
	)

	if spy.calls != 0 {
		t.Fatalf("parity probe must not call Secrets.Resolve, got %d calls", spy.calls)
	}
	out := buf.String()
	if !strings.Contains(out, "diverged") || !strings.Contains(out, "todo_id") {
		t.Fatalf("probe must log diverged + todo_id, got: %q", out)
	}
	if strings.Contains(out, sensitive) {
		t.Fatalf("F4 violation: resolved value leaked into log line: %q", out)
	}
}
