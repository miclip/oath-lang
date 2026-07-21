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
	ctx, cancel := context.WithTimeout(context.Background(), proveWallCap)
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
