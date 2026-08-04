//go:build !js

package main

// The native budget: a MEMORY bound, because on a real OS the interpreter's
// recursion is limited by how much host stack the process may safely borrow.
const (
	// evalStackBudget is the host stack the interpreter may borrow for Oath
	// recursion. Sized to leave room for the rest of the process inside a 512Mi
	// container — the smallest environment the kernel runs in AS A SERVICE. The
	// browser is smaller still and is handled in eval_depth_wasm.go, because
	// there the constraint is not memory at all.
	evalStackBudget = 96 << 20 // 96 MiB
	// evalFrameBytes is the measured host-stack cost of one eval frame.
	evalFrameBytes = 8_700
	maxEvalDepth   = evalStackBudget / evalFrameBytes // ~11,500 frames
)
