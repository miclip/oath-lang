package main

import (
	"fmt"
	"strings"
	"testing"
)

// THE CONSTRUCTOR PROTOCOL, witnessed (#149).
//
// synthCtor has TWO routes, and the defunctionalized checker must preserve them
// INDEPENDENTLY. Stack safety on one does not establish memo correctness on the
// other, and memo correctness does not establish stack safety:
//
//	monomorphic (tyvars = 0)   Str / SCons spine    single validation pass
//	polymorphic (tyvars > 0)   List a / Cons spine  infer -> solve -> publish -> validate
//
// `inferReady(tyvars, tyargs) = tyvars > 0 && len(tyargs) == 0`, so a string
// literal NEVER enters inference however long it is. That is not a detail: it
// means a differential built from string spines witnesses neither the memo nor
// pass-1 suppression, and timings taken on strings say nothing about either.
//
// These tests are the ORACLE for the port. They must keep passing when the
// recursive machine is replaced by an explicit one — and they must FAIL
// immediately if TyArgs publication moves, is deferred for purity, or is rolled
// back on failure, because all three change the machine.

// checkSource elaborates one definition and runs the checker over it exactly as
// checkDef's func branch does, returning the checker so its counters can be
// read. It performs NO store writes — elaboration and checking only.
func checkSource(t *testing.T, st *Store, src string) (*checker, *Def, error) {
	t.Helper()
	forms, err := parseForms(src)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(forms) != 1 {
		t.Fatalf("expected one form, got %d", len(forms))
	}
	def, _, err := elabFunc(st, forms[0])
	if err != nil {
		t.Fatalf("elaboration failed: %v", err)
	}
	c := &checker{st: st, selfTyVars: def.TyVars, selfTy: def.Ty}
	return c, def, c.check(nil, def.Body, def.Ty)
}

// polySpineSrc builds (Cons 0 (Cons 1 ... (Nil [Int]))) n deep, with the Cons
// type arguments OMITTED so every node enters inference.
func polySpineSrc(n int) string {
	s := "(Nil [Int])"
	for i := 0; i < n; i++ {
		s = fmt.Sprintf("(Cons %d %s)", i, s)
	}
	return fmt.Sprintf("(defn spine [] [] (List Int) %s)", s)
}

// TestPolymorphicSpineEntersInferenceOncePerNode is the MEMO WITNESS, and it is
// deliberately a counter rather than a stopwatch.
//
// Publishing TyArgs between the inference and validation passes is what makes
// the validation pass skip inference on nodes it re-enters. Without it the
// recurrence is T(n) = 2*T(n-1) — and with maxSyntaxNesting capping a spine at
// 512, 2^512 is not "slow", it is a hang no timeout distinguishes from a
// deadlock.
func TestPolymorphicSpineEntersInferenceOncePerNode(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	for _, n := range []int{1, 5, 50, 200} {
		c, _, err := checkSource(t, st, polySpineSrc(n))
		if err != nil {
			t.Fatalf("n=%d should typecheck, got %v", n, err)
		}
		// EXACTLY one inference entry per Cons node. Not "at most n" — an exact
		// count is what fails when a re-entry sneaks back in, and "at most"
		// would also pass over a machine that stopped inferring entirely.
		if c.inferEntries != n {
			t.Errorf("n=%d: inference entered %d times, want exactly %d — "+
				"a count above n means TyArgs publication no longer suppresses re-entry; "+
				"below n means inference is being skipped", n, c.inferEntries, n)
		}
	}
}

// TestMonomorphicSpineNeverEntersInference pins the OTHER route, and with it
// the reason the two witnesses cannot substitute for each other. A string
// literal of any length must enter inference ZERO times.
//
// If this ever counts above zero, the string witness has silently become an
// inference test and the timings taken on it stop meaning what they say.
func TestMonomorphicSpineNeverEntersInference(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	for _, n := range []int{1, 100, 2000} {
		src := `(defn s [] [] Str "` + strings.Repeat("a", n) + `")`
		c, _, err := checkSource(t, st, src)
		if err != nil {
			t.Fatalf("a %d-rune literal should typecheck, got %v", n, err)
		}
		if c.inferEntries != 0 {
			t.Errorf("%d-rune literal entered inference %d times, want 0 — "+
				"Str has tyvars=0 so inferReady must be false at every SCons node", n, c.inferEntries)
		}
	}
}

// TestInferenceCounterDiscriminates is the CONTROL. A counter that cannot
// distinguish the two routes would let both tests above pass while measuring
// nothing — the failure this repo keeps finding.
func TestInferenceCounterDiscriminates(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	poly, _, err := checkSource(t, st, polySpineSrc(20))
	if err != nil {
		t.Fatalf("polymorphic spine should typecheck: %v", err)
	}
	mono, _, err := checkSource(t, st, `(defn s [] [] Str "`+strings.Repeat("a", 20)+`")`)
	if err != nil {
		t.Fatalf("string literal should typecheck: %v", err)
	}
	if poly.inferEntries == mono.inferEntries {
		t.Fatalf("the counter reports %d for BOTH routes — it is not measuring inference entry",
			poly.inferEntries)
	}
	// And explicit type arguments must suppress inference on the polymorphic
	// route too, which is the other half of what inferReady decides.
	explicit := "(Nil [Int])"
	for i := 0; i < 5; i++ {
		explicit = fmt.Sprintf("(Cons [Int] %d %s)", i, explicit)
	}
	c, _, err := checkSource(t, st, "(defn spine [] [] (List Int) "+explicit+")")
	if err != nil {
		t.Fatalf("explicit type arguments should typecheck: %v", err)
	}
	if c.inferEntries != 0 {
		t.Errorf("explicit TyArgs entered inference %d times, want 0 — inferReady requires len(tyargs)==0",
			c.inferEntries)
	}
}
