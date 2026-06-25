package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/expr"
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
