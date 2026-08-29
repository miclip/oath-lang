//go:build !(js && wasm)

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// execZ3 runs the assembled script through the z3 subprocess and returns its
// combined output and whether the wall-clock safety cap fired (SPEC §7.2). This
// is the native run step; the browser build (z3host_wasm.go) reaches z3-solver
// through a worker bridge instead.
func execZ3(full string) (string, bool) {
	// An active search budget that has already elapsed aborts WITHOUT spawning z3:
	// proveOne runs several strategy attempts, and renewing a 1ms cap per attempt
	// would fork a subprocess for each one past the deadline. Short-circuiting to a
	// cap hit (an environmental abort, never a verdict) keeps the post-deadline
	// overhead at zero. Unset, searchDeadlinePassed is always false, so the default
	// path is unchanged.
	if searchDeadlinePassed() {
		return "", true
	}
	ctx, cancel := context.WithTimeout(context.Background(), attemptWallCap())
	defer cancel()
	cmd := exec.CommandContext(ctx, "z3", "-in")
	cmd.Stdin = strings.NewReader(full)
	out, _ := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", true
	}
	return string(out), false
}

// z3Available reports whether the solver can be reached before a prove run.
func z3Available() error {
	if _, err := exec.LookPath("z3"); err != nil {
		return fmt.Errorf("z3 not found on PATH (brew install z3)")
	}
	return nil
}
