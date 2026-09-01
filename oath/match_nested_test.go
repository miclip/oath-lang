package main

import (
	"strings"
	"testing"
)

// Nested constructor patterns in `match` (generic-consumer demand 2). They desugar to
// a fresh binder plus an inner match, so `(Cons (MkRun n x) t)` is exactly the
// hand-written `(Cons r t) (match r ((MkRun n x) ...))` — same AST, same hash.

func setupRun(t *testing.T) *Store {
	t.Helper()
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Run [a] (MkRun Int a))`)
	put(t, st, `(data Box [a] (MkBox (Run a)))`)
	put(t, st, `(data Two [a] (MkTwo (Run a) (Run a)))`)
	return st
}

// A nested pattern produces the IDENTICAL artifact to the two-step form — binder
// names are metadata, so the desugaring's fresh name changes no hash.
func TestNestedPatternPreservesIdentity(t *testing.T) {
	st := setupRun(t)
	flat := put(t, st, `(defn flatp [a] [(rs (List (Run a)))] Int
		(match rs ((Nil) 0) ((Cons r t) (match r ((MkRun n x) n)))))`)
	nest := put(t, st, `(defn nestp [a] [(rs (List (Run a)))] Int
		(match rs ((Nil) 0) ((Cons (MkRun n x) t) n)))`)
	if flat[0].Status != "accepted" || nest[0].Status != "accepted" {
		t.Fatalf("both must elaborate: flat=%s nest=%s", flat[0].Status, nest[0].Status)
	}
	if flat[0].Hash != nest[0].Hash {
		t.Errorf("nested pattern must desugar to the two-step form (identical hash):\n flat %s\n nest %s",
			flat[0].Hash, nest[0].Hash)
	}
}

func TestNestedPatternEvaluates(t *testing.T) {
	st := setupRun(t)
	// Deep nesting: (MkBox (MkRun n x)).
	put(t, st, `(defn unbox [a] [(b (Box a))] Int (match b ((MkBox (MkRun n x)) n)))`)
	if got, err := apiEval(st, `(unbox [Int] (MkBox [Int] (MkRun 5 7)))`); err != nil || !strings.HasPrefix(got, "5 ") {
		t.Errorf("deep-nested match: got %q err %v, want 5", got, err)
	}
	// Multiple nested fields: (MkTwo (MkRun n x) (MkRun m y)).
	put(t, st, `(defn sumc [a] [(p (Two a))] Int (match p ((MkTwo (MkRun n x) (MkRun m y)) (+ n m))))`)
	if got, err := apiEval(st, `(sumc [Int] (MkTwo [Int] (MkRun 3 9) (MkRun 4 9)))`); err != nil || !strings.HasPrefix(got, "7 ") {
		t.Errorf("multiple-nested match: got %q err %v, want 7", got, err)
	}
}

// A pattern position that is neither a name nor a nested constructor (a literal) is
// still rejected — the feature widens names to nested patterns, nothing else.
func TestNonPatternBinderStillRejected(t *testing.T) {
	st := setupRun(t)
	reps, err := apiPut(st, `(defn bad [a] [(r (Run a))] Int (match r ((MkRun 5 x) 0)))`, "t", "")
	if err == nil && (len(reps) == 0 || reps[0].Status == "accepted") {
		t.Fatal("a literal in a pattern position must be rejected")
	}
}

// A source binder named like the generated prefix must not be captured by the
// desugaring. `(MkTwo (MkRun n x) __nest1)` binds the second field to `__nest1`; if the
// fresh name for the first field were also `__nest1`, the inner match would destructure
// the wrong field. The result must still be the FIRST run's count.
func TestNestedPatternAvoidsBinderCapture(t *testing.T) {
	st := setupRun(t)
	put(t, st, `(defn firstc [a] [(p (Two a))] Int
		(match p ((MkTwo (MkRun n x) __nest1) n)))`)
	if got, err := apiEval(st, `(firstc [Int] (MkTwo [Int] (MkRun 3 9) (MkRun 4 9)))`); err != nil || !strings.HasPrefix(got, "3 ") {
		t.Errorf("binder capture: got %q err %v, want 3 (the first run's count)", got, err)
	}
}

// A nested pattern over a SUM type is refused, not silently mis-desugared. The naive
// per-arm rewrite would give each of `(Wrap (None))`/`(Wrap (Some x))` a single-arm
// inner match (non-exhaustive) and duplicate the outer `Wrap` arm; grouping them is
// pattern-matrix compilation, out of scope. Fail closed and name the reason.
func TestNestedSumPatternRefused(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Opt [a] (None) (Some a))`)
	put(t, st, `(data Wrap [a] (MkWrap (Opt a)))`)
	_, err := apiPut(st, `(defn unwrap [a] [(w (Wrap a))] Int
		(match w ((MkWrap (Some x)) 1) ((MkWrap (None)) 0)))`, "t", "")
	if err == nil || !strings.Contains(err.Error(), "single-constructor") {
		t.Fatalf("nested sum pattern must be refused by name; got err %v", err)
	}
}
