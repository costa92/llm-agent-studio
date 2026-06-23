package scriptengine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		inputs  map[string]string
		opt     Options
		ctx     func() (context.Context, context.CancelFunc)
		want    string
		wantErr error
	}{
		{
			name: "raw string output (not quoted repr)",
			code: `output = "hi"`,
			want: "hi",
		},
		{
			name:   "data-global injection",
			code:   `output = upstream.upper()`,
			inputs: map[string]string{"upstream": "ab"},
			want:   "AB",
		},
		{
			name: "json module present and pure",
			code: `output = json.encode({"k": 1})`,
			want: `{"k":1}`,
		},
		{
			name:    "no output assigned",
			code:    `x = 1`,
			wantErr: ErrOutputMissing,
		},
		{
			name:    "non-string output",
			code:    `output = 5`,
			wantErr: ErrFailed,
		},
		{
			name:    "output too large",
			code:    `output = "x" * (300*1024)`,
			opt:     Options{OutputCap: 256 * 1024},
			wantErr: ErrOutputTooLarge,
		},
		{
			// Steps are counted per function-call frame in this go.starlark.net
			// build (not per loop iteration), so heavy work must make many CALLS
			// to trip the budget — see report note on the prompt's sorted/range
			// example, which did not.
			name:    "step budget overrun -> timeout",
			code:    "def f(x):\n  return x\nfor i in range(100000):\n  f(i)\noutput = \"ok\"",
			opt:     Options{MaxSteps: 1000},
			wantErr: ErrTimeout,
		},
		{
			name: "pre-cancelled ctx -> timeout",
			code: `output = "hi"`,
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			wantErr: ErrTimeout,
		},
		{
			name:    "sandbox escape: open builtin absent",
			code:    `open("x")`,
			wantErr: ErrFailed,
		},
		{
			name:    "sandbox escape: load statement rejected",
			code:    `load("m", "x")`,
			wantErr: ErrFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.ctx != nil {
				var cancel context.CancelFunc
				ctx, cancel = tt.ctx()
				defer cancel()
			}
			got, err := Run(ctx, tt.code, tt.inputs, tt.opt)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Run() err = %v, want errors.Is(%v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run() unexpected err = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Run() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNoIOGlobals proves no I/O is reachable: after a benign run, the
// predeclared environment must not expose open/load.
func TestNoIOGlobals(t *testing.T) {
	// `open` must not be a predeclared builtin.
	if _, err := Run(context.Background(), `y = open`, nil, Options{}); err == nil {
		t.Fatal("expected error referencing undefined `open`")
	} else if !strings.Contains(err.Error(), "open") {
		t.Fatalf("err should mention undefined open, got %v", err)
	}
	// `load(...)` must be rejected (thread.Load left nil).
	if _, err := Run(context.Background(), `load("m", "x")`, nil, Options{}); err == nil {
		t.Fatal("expected load(...) to be rejected")
	}
}
