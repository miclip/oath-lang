package main

import "testing"

// TestEvalDepthIsAMemoryBudget pins #99: the depth limit exists to bound HOST
// STACK, and it was previously calibrated to a developer machine's 1GB stack.
// At 100_000 frames a non-terminating definition peaked at 869MB — 1.7x the
// registry container's 512Mi — so the guard fired correctly on a laptop while
// the deployed container was SIGKILLed before reaching it, taking the whole
// single-instance service down.
//
// Asserting the derivation rather than the number: a future frame-size change
// must be re-measured into evalFrameBytes, not absorbed by editing a constant
// that no longer means anything.
func TestEvalDepthIsAMemoryBudget(t *testing.T) {
	if maxEvalDepth != evalStackBudget/evalFrameBytes {
		t.Fatal("maxEvalDepth is no longer derived from the stack budget")
	}
	const containerLimit = 512 << 20
	worst := int64(maxEvalDepth) * evalFrameBytes
	if worst >= containerLimit/2 {
		t.Fatalf("worst-case eval stack is %dMB against a %dMB container: a bound that "+
			"permits this much allocation before firing is a delay, not a bound",
			worst>>20, containerLimit>>20)
	}
}
