//go:build js

package main

// THE WASM BOUND IS NOT A MEMORY BUDGET, and deriving it like one is what broke
// (#147). Under `GOOS=js` the interpreter's recursion is limited by the JS HOST
// STACK — Go's wasm runtime borrows it — so the ceiling is set by the embedder,
// not by anything this program can compute. Measured on the served artifact:
//
//	default node stack          1000 frames OK, 1200 throws RangeError
//	node --stack-size=4000      1800 frames OK
//
// It scales with the host's setting, which is the proof that the host stack is
// the binding constraint rather than Go's linear memory. A browser tab's
// ceiling is not knowable from here and may be lower than node's default, so
// this is set well beneath the lowest figure measured rather than close to it.
//
// The consequence is an HONEST BACKEND SUBSET, and it is the point: a deeply
// recursive definition that evaluates natively may report `recursion too deep`
// here. That is the same discipline the LLVM backend follows — refuse by name,
// never silently approximate — and it is what the depth guard is for. What it
// replaces is a hard crash that killed the kernel and took every later call in
// the page with it.
//
// Verified against the corpus by `make check-playground-wasm`: every corpus
// definition still elaborates to its recorded identity under this bound.
const maxEvalDepth = 400
