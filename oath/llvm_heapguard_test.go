package main

import (
	"fmt"
	"strings"
	"testing"
)

// The heap guard (#180) is the allocation analogue of the stack guard (#178). An
// immutable native container built incrementally — set-add in a fold — copies
// the whole array on every add, so N adds allocate O(N^2) element-slots into a
// request arena that a batch CLI entry never releases. On an overcommitting host
// malloc keeps succeeding until the kernel OOM-killer SIGKILLs the process (exit
// 137, with nothing on stdout or stderr an operator can act on). A proactive
// budget check at the single growth point (o_block_new) turns that into a
// legible exit-70 refusal that names the budget, exactly as the stack floor
// turns a 139 into a 70.
//
// The claim is held in two halves, mirroring the stack-guard tests: the artifact
// REFUSES (exit 70 + a diagnostic) rather than being killed, and a control
// proves the refusal is the BUDGET rather than the program — a fire case that
// cannot also show the program succeeding under a larger budget is not evidence
// the guard discriminates.

// hgStore builds a program whose large allocation is GATED behind a non-empty
// argument, so verification (a closed, binder-free property over the empty-args
// branch) never builds the list — only the running binary, when given an arg,
// does. The built list is a structural (List Int): ordinary O(N) arena
// allocation, not a native Set. The guard fires on any allocation past budget;
// the O(N^2) set idiom #180 names is the MOTIVATING case, not a precondition.
func hgStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(defn hg-build [] [(n Int)] (List Int)
	  (if (< n 1) (Nil [Int]) (Cons n (hg-build (- n 1))))
	  (prop base-is-nil [] (== (hg-build 0) (Nil [Int]))))`)
	put(t, st, `(defn hg-main [] [(args (List Str))] Str
	  (match args
	    ((Nil) "empty")
	    ((Cons a rest) (match (hg-build 500000)
	      ((Nil) "empty")
	      ((Cons b more) "built"))))
	  (prop empty-args-is-empty [] (== (hg-main (Nil [Str])) "empty")))`)
	return st
}

// hgBuild builds hg-main with a chosen heap budget. heapBytes > 0 overrides the
// compiled-in default via -DO_HEAP_BUDGET; heapBytes == 0 leaves the default.
func hgBuild(t *testing.T, st *Store, heapBytes int) string {
	t.Helper()
	old := llvmExtraCFlags
	if heapBytes > 0 {
		llvmExtraCFlags = []string{fmt.Sprintf("-DO_HEAP_BUDGET=%d", heapBytes)}
	} else {
		llvmExtraCFlags = nil
	}
	defer func() { llvmExtraCFlags = old }()
	return buildLLVM(t, st, "hg-main")
}

func TestLLVMHeapGuardRefusesInsteadOfOOMKill(t *testing.T) {
	requireClang(t)
	st := hgStore(t)

	// FIRE: an 8 MiB budget, a 500k-element build (given an arg) that exceeds it.
	// 8 MiB is well above process startup, so reaching it is the build, not the
	// runtime coming up.
	bin := hgBuild(t, st, 8*1024*1024)
	out, code := sgRun(t, bin, "trigger")
	if code == 137 || code == 139 || code == -1 {
		t.Fatalf("the artifact was KILLED (exit %d) instead of refusing — the "+
			"heap guard did not fire, which is the defect it exists to remove:\n%s", code, out)
	}
	if code != 70 {
		t.Fatalf("expected exit 70, this backend's runtime-refusal code; got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "exhausted its heap budget") {
		t.Fatalf("exit 70 with no heap diagnostic. 70 is also the provisioning-"+
			"failure code, so without the message an operator cannot tell the two "+
			"apart:\n%s", out)
	}

	// CONTROL A: the SAME build under a large budget exits 0 and prints "built",
	// so the refusal above is the budget and not the program.
	bin2 := hgBuild(t, st, 512*1024*1024)
	out2, code2 := sgRun(t, bin2, "trigger")
	if code2 != 0 {
		t.Fatalf("control: the same build under a 512 MiB budget must exit 0; got %d:\n%s", code2, out2)
	}
	if !strings.Contains(out2, "built") {
		t.Fatalf("control: expected \"built\" output; got:\n%s", out2)
	}

	// CONTROL B: even under the tiny 8 MiB budget, the EMPTY-args path allocates
	// almost nothing and exits 0 — the guard fires on the allocation, not on the
	// budget being small per se.
	out3, code3 := sgRun(t, bin)
	if code3 != 0 || !strings.Contains(out3, "empty") {
		t.Fatalf("control: empty-args under the tiny budget should exit 0 \"empty\"; got %d:\n%s", code3, out3)
	}
}
