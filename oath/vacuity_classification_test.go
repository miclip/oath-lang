package main

import (
	"math/big"
	"testing"
)

// The corpus exercises neither of these shapes, so without them the two
// refinements that produce the classification would be untested code.
func TestVacuityClassification(t *testing.T) {
	tru := &Term{K: "bool", Bool: true}
	fls := &Term{K: "bool", Bool: false}
	// A non-literal conclusion: `1 == 1`, which evaluates substantively.
	concl := &Term{K: "prim", Op: "==",
		Args: []Term{{K: "int", Int: big.NewInt(1)}, {K: "int", Int: big.NewInt(1)}}}

	run := func(body *Term) branchCount {
		acc := map[*Term]*branchCount{}
		ev := &evaluator{st: nil, fuel: propFuel, branch: acc}
		if _, err := ev.eval(nil, "", body); err != nil {
			t.Fatalf("eval: %v", err)
		}
		for _, b := range acc {
			return *b
		}
		return branchCount{}
	}

	// IMPLICATION whose guard FAILS: the conclusion is skipped. Vacuous.
	got := run(&Term{K: "if", A: fls, B: concl, C: tru})
	if got.Trivial != 1 || got.Substantive != 0 {
		t.Errorf("(if false CONCL true): got %+v, want one trivial — the conclusion was skipped", got)
	}

	// IMPLICATION whose guard HOLDS: the conclusion is tested. Not vacuous.
	got = run(&Term{K: "if", A: tru, B: concl, C: tru})
	if got.Substantive != 1 || got.Trivial != 0 {
		t.Errorf("(if true CONCL true): got %+v, want one substantive", got)
	}

	// DISJUNCTION `P or Q`, written (if P true Q), with P satisfied. The case
	// tested P and passed; it did not skip anything. Only the literal's
	// POSITION distinguishes this from the implication above.
	got = run(&Term{K: "if", A: tru, B: tru, C: concl})
	if got.Substantive != 1 || got.Trivial != 0 {
		t.Errorf("(if true true CONCL): got %+v, want one substantive — P held, which is a pass", got)
	}

	// PREDICATE encoding, satisfied. `(if P true false)` IS the assertion, so a
	// case where P holds is a PASS, not a skip. Classifying by the landed arm
	// alone would call this vacuous and reject a sound property.
	got = run(&Term{K: "if", A: tru, B: tru, C: fls})
	if got.Trivial != 0 || got.Substantive != 1 {
		t.Errorf("(if true true false): got %+v, want one substantive — both arms are literals, "+
			"so this encodes a boolean and has no conclusion to skip", got)
	}

	// THE NEGATED PREDICATE `not P`, written (if P false true), with P false.
	// It lands on the else-side `true`, which is where an implication puts its
	// tautology — but both arms are literals, so the condition IS the
	// assertion and this case tested it.
	got = run(&Term{K: "if", A: fls, B: fls, C: tru})
	if got.Substantive != 1 || got.Trivial != 0 {
		t.Errorf("(if false false true): got %+v, want one substantive — this is `not P` passing", got)
	}

	// WHOLLY TAUTOLOGICAL: neither arm can falsify, so no case tests anything,
	// whichever way the guard goes. A rule that merely asked "is the sibling a
	// literal?" would classify this as a predicate and wave it through.
	got = run(&Term{K: "if", A: tru, B: tru, C: tru})
	if got.Trivial != 1 || got.Substantive != 0 {
		t.Errorf("(if true true true): got %+v, want one trivial — nothing here can be refuted", got)
	}
	got = run(&Term{K: "if", A: fls, B: tru, C: tru})
	if got.Trivial != 1 {
		t.Errorf("(if false true true): got %+v, want one trivial", got)
	}

	// The same predicate, unsatisfied: it is REFUTED, which is substantive too.
	got = run(&Term{K: "if", A: fls, B: tru, C: fls})
	if got.Substantive != 1 {
		t.Errorf("(if false true false): got %+v, want one substantive", got)
	}
}

// A guard NO case reaches must be reported, not silently absent: the ratchet
// iterates the property's own if nodes rather than the recorded counters, so a
// nested guard behind an untaken branch still appears with zero reach.
func TestUnreachedGuardIsVisible(t *testing.T) {
	tru := &Term{K: "bool", Bool: true}
	fls := &Term{K: "bool", Bool: false}
	concl := &Term{K: "prim", Op: "==",
		Args: []Term{{K: "int", Int: big.NewInt(1)}, {K: "int", Int: big.NewInt(1)}}}
	inner := &Term{K: "if", A: tru, B: concl, C: tru}
	outer := &Term{K: "if", A: fls, B: inner, C: tru}

	own := map[*Term]string{}
	ownIfPaths(outer, "", own)
	if len(own) != 2 {
		t.Fatalf("ownIfPaths found %d if nodes, want 2 (outer and the nested guard)", len(own))
	}
	acc := map[*Term]*branchCount{}
	ev := &evaluator{st: nil, fuel: propFuel, branch: acc}
	if _, err := ev.eval(nil, "", outer); err != nil {
		t.Fatal(err)
	}
	if _, recorded := acc[inner]; recorded {
		t.Fatal("the nested guard was evaluated; this test no longer covers the unreached case")
	}
	// Iterating `own` is what makes it visible. Iterating `acc` would not.
	unreached := 0
	for node := range own {
		if b := acc[node]; b == nil || b.Substantive == 0 {
			unreached++
		}
	}
	if unreached != 2 {
		t.Errorf("unreached-or-skipped guards = %d, want 2 (the outer skipped, the inner never reached)", unreached)
	}
}
