// Package scriptengine runs author-authored Starlark with step/time/output
// limits. Secure-by-construction: NO I/O builtins are granted (no open/file/
// network), so a script can only transform its given inputs into an output
// string. Errors are CLASSIFIED sentinels — the raw Starlark error (which
// embeds source lines + variable values) is wrapped for server logs only and
// MUST NOT be surfaced to the frontend (the caller maps these to a bare
// opaque enum).
package scriptengine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	starlarkjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

var (
	ErrFailed         = errors.New("scriptengine: script failed")
	ErrTimeout        = errors.New("scriptengine: timed out")
	ErrOutputMissing  = errors.New("scriptengine: no output assigned")
	ErrOutputTooLarge = errors.New("scriptengine: output too large")
)

const (
	DefaultMaxSteps  uint64 = 10_000_000
	DefaultOutputCap int    = 256 * 1024
)

type Options struct {
	MaxSteps  uint64
	OutputCap int
}

// Run executes Starlark `code` with `inputs` injected as predeclared string
// globals (plus a pure `json` module). The script must assign a string to the
// global `output`. ctx cancellation / step-budget overrun → ErrTimeout; any
// other failure → ErrFailed (raw err wrapped for logs only).
func Run(ctx context.Context, code string, inputs map[string]string, opt Options) (out string, err error) {
	// This is the isolation boundary for untrusted author code, and the worker
	// does not recover around node execution — convert any library panic into a
	// classified failure rather than crashing the (single, multi-tenant) binary.
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("%w: panic: %v", ErrFailed, r)
		}
	}()

	maxSteps := opt.MaxSteps
	if maxSteps == 0 {
		maxSteps = DefaultMaxSteps
	}
	outCap := opt.OutputCap
	if outCap == 0 {
		outCap = DefaultOutputCap
	}

	// Inject inputs first, THEN the json module, so the module always wins and an
	// input named "json" cannot shadow it.
	predeclared := starlark.StringDict{}
	for k, v := range inputs {
		predeclared[k] = starlark.String(v)
	}
	predeclared["json"] = starlarkjson.Module

	thread := &starlark.Thread{Name: "node"}
	thread.SetMaxExecutionSteps(maxSteps)
	thread.Print = func(*starlark.Thread, string) {}

	// If ctx is already cancelled, cancel the thread synchronously so the
	// outcome is deterministic (the watcher goroutine may otherwise lose the
	// race against a fast script and let it run to completion).
	if ctx.Err() != nil {
		thread.Cancel("ctx")
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel("ctx")
		case <-done:
		}
	}()

	fileOpts := &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true}
	globals, err := starlark.ExecFileOptions(fileOpts, thread, "node.star", []byte(code), predeclared)
	if err != nil {
		// Step-budget AND wall-time both surface as "...cancelled..." with an
		// unreadable reason — branch on ctx.Err() FIRST.
		if ctx.Err() != nil || strings.Contains(err.Error(), "cancelled") {
			return "", fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return "", fmt.Errorf("%w: %v", ErrFailed, err)
	}

	outVal, ok := globals["output"]
	if !ok {
		return "", ErrOutputMissing
	}
	s, ok := outVal.(starlark.String)
	if !ok {
		return "", fmt.Errorf("%w: output must be a string (use json.encode for JSON)", ErrFailed)
	}
	str := string(s) // raw bytes; NEVER s.String() (returns a quoted repr)
	if len(str) > outCap {
		return "", ErrOutputTooLarge
	}
	return str, nil
}
