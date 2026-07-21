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
// There is no subprocess and no wall cap: the rlimit bounds the work
// deterministically, so capHit is only ever set when the bridge is absent.
func execZ3(full string) (string, bool) {
	fn := js.Global().Get("oathZ3")
	if fn.Type() != js.TypeFunction {
		return "", true // no bridge wired → invalid attempt, never a false verdict
	}
	return fn.Invoke(full).String(), false
}

func z3Available() error {
	if js.Global().Get("oathZ3").Type() != js.TypeFunction {
		return fmt.Errorf("z3 bridge not available (globalThis.oathZ3 is not wired)")
	}
	return nil
}
