package main

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

// STEP 3: the first ported family, run SIDE BY SIDE with the recursive checker.
//
// Ported: synthesis of leaves (var, int, rat, float, bool) and check's default
// path — synthesize, then compare. Deliberately the smallest family that
// exercises continuation plumbing at all: leaves alone never suspend, and the
// default check path is a real continuation while touching neither inference
// nor mutation. A divergence here is attributable to the machinery rather than
// to the constructor protocol.

type familyCase struct {
	name string
	ctx  []*Ty
	term *Term
	exp  *Ty // nil = synth mode
}

func i(n int64) *Term { return &Term{K: "int", Int: big.NewInt(n)} }

func portedFamilyCases() []familyCase {
	return []familyCase{
		// synth of every ported leaf
		{"synth-int", nil, i(7), nil},
		{"synth-bool", nil, &Term{K: "bool", Bool: true}, nil},
		{"synth-rat", nil, &Term{K: "rat", Rat: big.NewRat(1, 2)}, nil},
		{"synth-float", nil, &Term{K: "float", Float: 1.5}, nil},
		{"synth-var-0", []*Ty{tInt()}, &Term{K: "var", Idx: 0}, nil},
		{"synth-var-1", []*Ty{tBool(), tInt()}, &Term{K: "var", Idx: 1}, nil},

		// de Bruijn edges: the index must be read from the RIGHT end
		{"synth-var-innermost", []*Ty{tBool(), tInt()}, &Term{K: "var", Idx: 0}, nil},
		{"synth-var-out-of-scope", []*Ty{tInt()}, &Term{K: "var", Idx: 3}, nil},
		{"synth-var-negative", []*Ty{tInt()}, &Term{K: "var", Idx: -1}, nil},
		{"synth-var-empty-ctx", nil, &Term{K: "var", Idx: 0}, nil},

		// check via the default path: agreeing and disagreeing
		{"check-int-ok", nil, i(1), tInt()},
		{"check-int-vs-bool", nil, i(1), tBool()},
		{"check-bool-ok", nil, &Term{K: "bool"}, tBool()},
		{"check-rat-vs-int", nil, &Term{K: "rat", Rat: big.NewRat(1, 2)}, tInt()},
		{"check-float-ok", nil, &Term{K: "float", Float: 0}, tFloat()},
		{"check-var-ok", []*Ty{tInt()}, &Term{K: "var", Idx: 0}, tInt()},
		{"check-var-mismatch", []*Ty{tBool()}, &Term{K: "var", Idx: 0}, tInt()},
		{"check-var-out-of-scope", nil, &Term{K: "var", Idx: 0}, tInt()},

		// missing term: both must refuse rather than fault
		{"synth-nil-term", nil, nil, nil},

		// --- `if`: SEQUENCING is the invariant, not just the answer ---------
		// Symmetric branches would let a machine that reordered or skipped a
		// branch agree by coincidence, so every case below is asymmetric.
		{"if-synth-agree", nil, mkIf(bt(true), i(1), i(2)), nil},
		{"if-synth-disagree-bool-int", nil, mkIf(bt(true), bt(true), i(1)), nil},
		{"if-synth-disagree-int-bool", nil, mkIf(bt(true), i(1), bt(true)), nil},
		{"if-synth-bad-cond", nil, mkIf(i(1), i(1), i(2)), nil},
		// The condition must be diagnosed BEFORE the branches, even when the
		// branches also disagree — otherwise a reordered machine reports a
		// different (and still plausible) error.
		{"if-synth-cond-beats-branches", nil, mkIf(i(1), bt(true), i(2)), nil},
		{"if-synth-nested", nil, mkIf(bt(true), mkIf(bt(false), i(1), i(2)), i(3)), nil},

		// CHECK MODE: both branches go against exp. A machine that checked the
		// else-branch against the THEN-branch's inferred type would accept
		// if-check-both-wrong, which the real checker rejects.
		{"if-check-ok", nil, mkIf(bt(true), i(1), i(2)), tInt()},
		{"if-check-both-wrong", nil, mkIf(bt(true), i(1), i(2)), tBool()},
		{"if-check-else-wrong", nil, mkIf(bt(true), i(1), bt(true)), tInt()},
		// then fails first: a machine checking the else first reports the other
		// branch's message.
		{"if-check-then-wrong", nil, mkIf(bt(true), bt(true), i(1)), tInt()},
		{"if-check-bad-cond", nil, mkIf(i(1), bt(true), bt(false)), tBool()},
		// BOTH branches wrong, with DIFFERENT messages: the only shape in which
		// branch ORDER is observable. if-check-both-wrong cannot see it, because
		// two Int branches against Bool produce the same text twice.
		{"if-check-both-wrong-differently", nil, mkIf(bt(true), bt(true), &Term{K: "rat", Rat: big.NewRat(3, 2)}), tInt()},
		{"if-synth-both-wrong-differently", nil, mkIf(bt(true), bt(true), &Term{K: "rat", Rat: big.NewRat(3, 2)}), nil},
		{"if-check-nested", nil, mkIf(bt(true), mkIf(bt(false), i(1), i(2)), i(3)), tInt()},
		{"if-check-var-branch", []*Ty{tInt()}, mkIf(bt(true), &Term{K: "var", Idx: 0}, i(2)), tInt()},
	}
}

func bt(b bool) *Term          { return &Term{K: "bool", Bool: b} }
func mkIf(a, b, c *Term) *Term { return &Term{K: "if", A: a, B: b, C: c} }

// TestPortedFamilyMatchesRecursiveChecker is obligations 1-3: same outcome, same
// diagnostic, no surviving mutation, unchanged inference accounting.
func TestPortedFamilyMatchesRecursiveChecker(t *testing.T) {
	st := canonicalStore(t)

	for _, tc := range portedFamilyCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Hash the term before either runs, so a mutation by EITHER is
			// visible. The ported family must not write to the AST at all.
			before := ""
			if tc.term != nil {
				before = hashDef(&Def{K: "func", Ty: tInt(), Body: tc.term})
			}

			rc := &checker{st: st}
			var rTy *Ty
			var rErr error
			if tc.exp == nil {
				rTy, rErr = rc.synth(tc.ctx, tc.term)
			} else {
				rErr = rc.check(tc.ctx, tc.term, tc.exp)
			}

			m := &checkerMachine{st: st}
			step := checkerStep{mode: modeSynth, ctx: tc.ctx, term: tc.term}
			if tc.exp != nil {
				step = checkerStep{mode: modeCheck, ctx: tc.ctx, term: tc.term, exp: tc.exp}
			}
			mTy, mErr := m.run(step)

			// The machine must not be refusing the whole family — that would
			// make every comparison below vacuously "agree" on an error.
			var notPorted errFamilyNotPorted
			if errors.As(mErr, &notPorted) {
				t.Fatalf("case is inside the ported family but the machine refused it: %v", mErr)
			}

			// Same verdict.
			if (rErr == nil) != (mErr == nil) {
				t.Fatalf("outcome differs\n  recursive: %v\n  machine:   %v", rErr, mErr)
			}
			// Same diagnostic, compared as TEXT here rather than as a category:
			// this family is small enough that exact agreement is achievable,
			// and a needless rewording is still an unrequested change.
			if rErr != nil && rErr.Error() != mErr.Error() {
				t.Fatalf("diagnostic differs\n  recursive: %q\n  machine:   %q", rErr, mErr)
			}
			// Same synthesized type.
			if tc.exp == nil && rErr == nil && !tyEq(rTy, mTy) {
				t.Fatalf("synthesized type differs\n  recursive: %s\n  machine:   %s", debugTy(rTy), debugTy(mTy))
			}
			// No surviving mutation.
			if tc.term != nil {
				if after := hashDef(&Def{K: "func", Ty: tInt(), Body: tc.term}); after != before {
					t.Fatalf("the term was MUTATED: %s -> %s; this family must not write to the AST", before[:12], after[:12])
				}
			}
			// Inference accounting untouched on both sides.
			if rc.inferEntries != 0 || m.inferEntries != 0 {
				t.Fatalf("inference entered on a family with no inference: recursive=%d machine=%d",
					rc.inferEntries, m.inferEntries)
			}
			// The stack must be empty: a frame left behind is a leak that would
			// corrupt the next run on a reused machine.
			if len(m.stack) != 0 {
				t.Fatalf("machine finished with %d frame(s) still on the stack", len(m.stack))
			}
		})
	}
}

// TestUnportedFamiliesRefuse is obligation 5: an unsupported family must fail
// with "not yet ported" — never fall back to the recursive checker, never
// panic, and never silently approximate.
//
// A FALLBACK WOULD BE THE WORST OUTCOME: the differential would pass while
// measuring the old machine, which is precisely what this port is guarding
// against.
func TestUnportedFamiliesRefuse(t *testing.T) {
	st := canonicalStore(t)
	unported := []*Term{
		{K: "ctor"}, {K: "app"}, {K: "lam"}, {K: "let"},
		{K: "match"}, {K: "prim"}, {K: "record"},
		{K: "field"}, {K: "ref"}, {K: "self"}, {K: "str"},
	}
	for _, term := range unported {
		for _, mode := range []checkMode{modeSynth, modeCheck} {
			m := &checkerMachine{st: st}
			step := checkerStep{mode: mode, term: term}
			if mode == modeCheck {
				step.exp = tInt()
			}
			_, err := m.run(step)
			var nf errFamilyNotPorted
			if !errors.As(err, &nf) {
				t.Errorf("%s of %q: expected a not-yet-ported refusal, got %v", mode, term.K, err)
				continue
			}
			if !strings.Contains(err.Error(), term.K) {
				t.Errorf("%s of %q: the refusal must name the family, got %q", mode, term.K, err)
			}
		}
	}
}

// TestPortedFamilyUsesTheExplicitStack is the reason any of this exists. A
// check of a ported leaf must actually push a continuation rather than compare
// inline — otherwise the plumbing is untested and the next family inherits
// machinery nothing has exercised.
func TestPortedFamilyUsesTheExplicitStack(t *testing.T) {
	st := canonicalStore(t)
	m := &checkerMachine{st: st}

	// A frame that records that it was resumed, installed beneath the run so
	// the machine must unwind THROUGH it to finish.
	probe := &recordingFrame{}
	m.stack = append(m.stack, probe)
	if _, err := m.run(checkerStep{mode: modeCheck, term: &Term{K: "int", Int: big.NewInt(1)}, exp: tInt()}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !probe.resumed {
		t.Fatal("the machine finished without unwinding through its stack — " +
			"the check path is not using continuations, so the plumbing is untested")
	}
}

type recordingFrame struct{ resumed bool }

func (f *recordingFrame) describe() string { return "test:recording" }
func (f *recordingFrame) resume(m *checkerMachine, r checkResult) (frameOutcome, error) {
	f.resumed = true
	return frameOutcome{done: &r}, nil
}
