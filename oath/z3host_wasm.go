//go:build js && wasm

package main

import (
	"fmt"
	"syscall/js"
)

// execZ3 hands the assembled script to the browser's z3 bridge: a Web Worker
// running z3-solver@4.16.0 (the corpus's exact solver version), reached
// SYNCHRONOUSLY. globalThis.oathZ3 posts the script to that worker and blocks
// on Atomics.wait over a SharedArrayBuffer until it answers, so this call
// returns z3's output inline — the prover stays synchronous and unchanged.
// The rlimit bounds the work deterministically, so capHit is normally never
// set; the bridge also enforces a wall cap and signals a wedged/absent solver by
// returning an empty string, which we treat as an invalid attempt (never a false
// verdict) — the goal is recorded unproven and the prover moves on.
func execZ3(full string) (string, bool) {
	fn := js.Global().Get("oathZ3")
	if fn.Type() != js.TypeFunction {
		return "", true // no bridge wired → invalid attempt, never a false verdict
	}
	out := fn.Invoke(full).String()
	if out == "" {
		return "", true // bridge poisoned or timed out → invalid attempt
	}
	return out, false
}

func z3Available() error {
	if js.Global().Get("oathZ3").Type() != js.TypeFunction {
		return fmt.Errorf("z3 bridge not available (globalThis.oathZ3 is not wired)")
	}
	return nil
}
