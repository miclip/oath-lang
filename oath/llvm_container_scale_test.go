package main

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// #180: the native Set/Map representation is a PERSISTENT balanced tree, so an
// incremental build (set-add in a fold/recursion) allocates O(log N) per add and
// O(N log N) overall — not the O(N^2) the copied sorted array retained, which
// OOM-killed a compiled batch consumer (exit 137) past ~50K distinct records.
//
// These tests are the two things the design turned on and the existing fixed
// differential does not reach: that a build well ABOVE the old ~60K OOM point now
// completes, and that the tree stays correct (dedup, sort, membership) under an
// adversarial, duplicate-heavy insertion order — the case a balancing or
// path-copy bug would break, and the reason immutability is the whole safety
// argument (every such bug is an observable disagreement, never a silent one).

// scaleSetStore seeds the Set stdlib these tests lower natively on the LLVM
// backend. The structural (List Int) definitions are the model the prover sees;
// the backend refines them to the native tree.
func scaleSetStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(data Set [] (MkSet (List Int)))`)
	put(t, st, `(defn length [a] [(xs (List a))] Int
		(match xs ((Nil) 0) ((Cons h t) (+ 1 (length [a] t)))))`)
	put(t, st, `(defn si-member [] [(x Int) (xs (List Int))] Bool
		(match xs ((Nil) false)
			((Cons h t) (if (== x h) true (if (< x h) false (si-member x t))))))`)
	put(t, st, `(defn si-insert [] [(x Int) (xs (List Int))] (List Int)
		(match xs ((Nil) (Cons [Int] x (Nil [Int])))
			((Cons h t) (if (< x h) (Cons [Int] x xs)
				(if (== x h) xs (Cons [Int] h (si-insert x t)))))))`)
	put(t, st, `(defn set-empty [] [] Set (MkSet (Nil [Int])))`)
	put(t, st, `(defn set-member [] [(x Int) (s Set)] Bool (match s ((MkSet xs) (si-member x xs))))`)
	put(t, st, `(defn set-add [] [(x Int) (s Set)] Set (match s ((MkSet xs) (MkSet (si-insert x xs)))))`)
	put(t, st, `(defn set-size [] [(s Set)] Int (match s ((MkSet xs) (length [Int] xs))))`)
	put(t, st, `(defn set-elems [] [(s Set)] (List Int) (match s ((MkSet xs) xs)))`)
	put(t, st, `(defn head-or [] [(d Int) (xs (List Int))] Int
		(match xs ((Nil) d) ((Cons h t) h)))`)
	return st
}

// TestLLVMSetScaleNoOOM builds a set of 100_000 DISTINCT elements at run time,
// well past the ~60K point where the copied-array representation was OOM-killed,
// and checks the size is exactly 100_000. The build is gated behind a non-empty
// argument so verification (a closed property over the empty-args branch) never
// runs it — only the running binary does, exactly as the heap-guard test does.
// A representation that lost, duplicated, or mis-shared a node would report a
// size other than 100_000; the old O(N^2) representation would not reach here at
// all (SIGKILL / exit 137).
func TestLLVMSetScaleNoOOM(t *testing.T) {
	requireClang(t)
	st := scaleSetStore(t)
	// build-set n = {1..n}, each added once (distinct). Recursion is n deep, which
	// fits the LLVM backend's large worker stack (#178); the set-add itself is the
	// native tree op under test.
	put(t, st, `(defn build-set [] [(n Int)] Set
		(if (< n 1) set-empty (set-add n (build-set (- n 1)))))`)
	put(t, st, `(defn scale-main [] [(args (List Str))] Str
		(match args
			((Nil) "empty")
			((Cons a rest)
				(if (== (set-size (build-set 100000)) 100000) "ok-100k" "wrong-size")))
		(prop empty-args-is-empty [] (== (scale-main (Nil [Str])) "empty")))`)
	markVerified(t, st, "scale-main")
	bin := buildLLVM(t, st, "scale-main")

	got := runCaptured(t, bin, "go")
	if got.code == 137 || got.code == 139 || got.code == -1 {
		t.Fatalf("100K-element set build was KILLED (exit %d) — the O(N^2) arena "+
			"blowup #180 fixed is back:\n%s%s", got.code, got.stdout, got.stderr)
	}
	if got.code != 0 || strings.TrimRight(got.stdout, "\n") != "ok-100k" {
		t.Fatalf("100K set: exit %d, stdout %q (want exit 0, %q) stderr %q",
			got.code, got.stdout, "ok-100k", got.stderr)
	}

	// Control: the empty-args path allocates almost nothing and exits 0 "empty",
	// so "ok-100k" above is the build succeeding, not the program always printing it.
	ctrl := runCaptured(t, bin)
	if ctrl.code != 0 || strings.TrimRight(ctrl.stdout, "\n") != "empty" {
		t.Fatalf("control (empty args): exit %d stdout %q, want 0 %q", ctrl.code, ctrl.stdout, "empty")
	}
}

// nestSetAdds renders vals as (set-add v0 (set-add v1 (... set-empty))): v0 is
// added LAST, so the slice order is the insertion order the tree sees.
func nestSetAdds(vals []int) string {
	e := "set-empty"
	for i := len(vals) - 1; i >= 0; i-- {
		e = fmt.Sprintf("(set-add %d %s)", vals[i], e)
	}
	return e
}

// TestLLVMSetScrambledDifferential builds sets from adversarial, duplicate-heavy
// insertion orders and checks size, the minimum (the sorted set-elems boundary),
// and membership of present and absent values against a Go-computed reference.
// A tree whose rotations broke the BST invariant, whose path-copy dropped a
// subtree, or whose balancing lost an element would disagree with the reference
// on one of these — and because the structure is immutable, that disagreement is
// the ONLY way such a bug can present (there is no aliasing to hide it).
func TestLLVMSetScrambledDifferential(t *testing.T) {
	requireClang(t)
	st := scaleSetStore(t)

	for _, seed := range []int64{1, 7, 42, 1000003} {
		seed := seed
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			// ~240 inserts drawn from [0,120): heavy duplication (roughly 2x), so
			// dedup is exercised and the tree rebalances across many rotations.
			const inserts, span = 240, 120
			vals := make([]int, inserts)
			present := map[int]bool{}
			for i := range vals {
				vals[i] = rng.Intn(span)
				present[vals[i]] = true
			}
			// Reference: distinct count and minimum.
			wantSize := len(present)
			wantMin := span // sentinel above any element
			for v := range present {
				if v < wantMin {
					wantMin = v
				}
			}
			// Two present and two absent probe values.
			p1, p2 := firstN(present, span, true, 2)
			a1, a2 := firstN(present, span, false, 2)

			setExpr := nestSetAdds(vals)
			// "ok" iff size, min, and all four membership probes match the reference.
			entry := fmt.Sprintf(`(defn scr-main [] [(args (List Str))] Str
				(if (== (set-size %[1]s) %[2]d)
					(if (== (head-or 0 (set-elems %[1]s)) %[3]d)
						(if (set-member %[4]d %[1]s)
							(if (set-member %[5]d %[1]s)
								(if (set-member %[6]d %[1]s) "bad-absent1"
									(if (set-member %[7]d %[1]s) "bad-absent2" "ok"))
								"bad-present2")
							"bad-present1")
						"bad-min")
					"bad-size"))`, setExpr, wantSize, wantMin, p1, p2, a1, a2)
			put(t, st, entry)
			markVerified(t, st, "scr-main")
			bin := buildLLVM(t, st, "scr-main")
			got := runCaptured(t, bin)
			if got.code != 0 || strings.TrimRight(got.stdout, "\n") != "ok" {
				t.Fatalf("scrambled seed %d: exit %d stdout %q (want 0 %q) — native tree "+
					"diverged from the reference (size=%d min=%d present=%d,%d absent=%d,%d)\nstderr: %s",
					seed, got.code, got.stdout, "ok", wantSize, wantMin, p1, p2, a1, a2, got.stderr)
			}
		})
	}
}

// firstN returns the first n values in [0,span) whose membership equals want.
func firstN(present map[int]bool, span int, want bool, n int) (int, int) {
	out := []int{}
	for v := 0; v < span && len(out) < n; v++ {
		if present[v] == want {
			out = append(out, v)
		}
	}
	for len(out) < 2 {
		out = append(out, span+len(out)) // absent sentinels if the range was saturated
	}
	return out[0], out[1]
}
