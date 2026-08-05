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

		// --- `let`: the invariant is CONTEXT SCOPE, not the answer ----------
		{"let-synth-basic", nil, mkLet(tBool(), bt(true), v(0)), nil},
		{"let-synth-annotation-mismatch", nil, mkLet(tBool(), i(1), v(0)), nil},
		{"let-check-basic", nil, mkLet(tBool(), bt(true), v(0)), tBool()},
		// synth says "let annotation mismatch"; check says "expected ... got".
		// Same broken program, two different diagnostics, both preserved.
		{"let-check-annotation-mismatch", nil, mkLet(tBool(), i(1), v(0)), tBool()},
		{"let-check-body-mismatch", nil, mkLet(tBool(), bt(true), v(0)), tInt()},

		// The bound value is evaluated in the ORIGINAL context: Var 0 here is
		// the OUTER binding, not the one being introduced.
		{"let-bound-uses-outer-ctx", []*Ty{tInt()}, mkLet(tInt(), v(0), v(0)), nil},
		{"let-bound-sees-no-self", nil, mkLet(tInt(), v(0), v(0)), nil},

		// de Bruijn ordering under nesting: Var 0 is the innermost let, Var 1
		// the outer one, Var 2 the pre-existing binding.
		{"let-nested-inner", nil, mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(0))), nil},
		{"let-nested-outer", nil, mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(1))), nil},
		{"let-nested-preexisting", []*Ty{tFloat()}, mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(2))), nil},
		{"let-nested-out-of-scope", nil, mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(2))), nil},

		// THE LEAKAGE WITNESS. Var 0 resolves to Int outside and to Bool inside
		// the let, so if the let's extended context survived into the SIBLING
		// else-branch the two branches would agree and this would be accepted.
		// "Forget to pop" is invisible without a sibling to leak into.
		{"let-no-leak-to-sibling", []*Ty{tInt()},
			mkIf(bt(true), mkLet(tBool(), bt(true), v(0)), v(0)), nil},
		// Same shape in check mode: the else-branch must still see Int.
		{"let-no-leak-to-sibling-check", []*Ty{tInt()},
			mkIf(bt(true), mkLet(tInt(), i(9), v(0)), v(0)), tInt()},
		// A FAILING body must not leak the extended context either: the else
		// branch is evaluated after the then-branch has unwound with an error.
		{"let-no-leak-after-failure", []*Ty{tInt()},
			mkIf(bt(true), mkLet(tBool(), i(1), v(0)), v(0)), nil},
		// let inside a let's BOUND position, so the context grows and shrinks
		// on the way to a sibling rather than only on the way down.
		{"let-in-bound-position", []*Ty{tInt()},
			mkLet(tBool(), mkLet(tFloat(), &Term{K: "float"}, bt(true)), v(1)), nil},

		// --- `prim`: the invariant is INDEXED TRAVERSAL --------------------
		{"prim-add-ok", nil, mkPrim("+", i(1), i(2)), nil},
		{"prim-unary-neg", nil, mkPrim("neg", i(1)), nil},
		{"prim-unary-not", nil, mkPrim("not", bt(true)), nil},
		{"prim-and", nil, mkPrim("and", bt(true), bt(false)), nil},

		// BOUNDARIES: loop machinery usually gets many-argument right and the
		// edges wrong.
		{"prim-zero-args", nil, mkPrim("+"), nil},
		{"prim-zero-args-not", nil, mkPrim("not"), nil},
		{"prim-one-arg-to-binary", nil, mkPrim("+", i(1)), nil},
		{"prim-three-args-to-binary", nil, mkPrim("+", i(1), i(2), i(3)), nil},

		// ASYMMETRIC per-position failures. Identical arguments cannot witness
		// traversal order; these fail DIFFERENTLY per position, so skipping,
		// reordering, or double-visiting produces a distinguishable diagnostic.
		{"prim-arg0-fails", nil, mkPrim("+", v(5), i(2)), nil},
		{"prim-arg1-fails", nil, mkPrim("+", i(1), v(7)), nil},
		// LEFTMOST wins: a machine that visits right-to-left reports index 7.
		{"prim-both-fail-differently", nil, mkPrim("+", v(5), v(7)), nil},
		{"prim-arg2-fails-of-three", nil, mkPrim("+", i(1), i(2), v(9)), nil},

		// Off-by-one in the accumulator shows up as the OPERATOR's diagnostic,
		// because the wrong types reach primResultTy.
		{"prim-mixed-numeric", nil, mkPrim("+", i(1), &Term{K: "rat", Rat: big.NewRat(1, 2)}), nil},
		{"prim-bool-into-arith", nil, mkPrim("+", i(1), bt(true)), nil},
		{"prim-int-into-bool-op", nil, mkPrim("and", bt(true), i(1)), nil},
		{"prim-order-matters", nil, mkPrim("+", bt(true), i(1)), nil},

		{"prim-nested", nil, mkPrim("+", mkPrim("+", i(1), i(2)), i(3)), nil},
		{"prim-nested-inner-fails", nil, mkPrim("+", mkPrim("+", i(1), v(4)), i(3)), nil},
		{"prim-in-if", nil, mkIf(mkPrim("not", bt(true)), i(1), i(2)), nil},
		{"prim-in-let-body", nil, mkLet(tInt(), i(1), mkPrim("+", v(0), i(2))), nil},

		// check mode falls through to synthesize-then-compare, as check.go does.
		{"prim-check-ok", nil, mkPrim("+", i(1), i(2)), tInt()},
		{"prim-check-mismatch", nil, mkPrim("+", i(1), i(2)), tBool()},
		{"prim-check-arg-fails", nil, mkPrim("+", v(5), i(2)), tInt()},

		// --- `==`: the invariant is RECOVERY, not traversal -----------------
		// Path 1: left synthesizes, right is CHECKED against its type.
		{"eq-left-synths", nil, mkPrim("==", i(1), i(2)), nil},
		{"eq-left-synths-mismatch", nil, mkPrim("==", i(1), bt(true)), nil},
		{"eq-bools", nil, mkPrim("==", bt(true), bt(false)), nil},
		{"eq-nested", nil, mkPrim("==", mkPrim("==", i(1), i(1)), bt(true)), nil},

		// Path 3: BOTH operands fail synthesis. check.go discards both original
		// errors for a fixed message, so a machine that propagates the first
		// one — which looks more informative — is observably different.
		{"eq-both-fail", nil, mkPrim("==", v(5), v(7)), nil},
		{"eq-both-fail-same", nil, mkPrim("==", v(9), v(9)), nil},

		// Arity is checked before either operand runs.
		{"eq-arity-one", nil, mkPrim("==", i(1)), nil},
		{"eq-arity-three", nil, mkPrim("==", i(1), i(2), i(3)), nil},
		{"eq-arity-zero", nil, mkPrim("=="), nil},

		// Left fails, right succeeds: the RECOVERY path. Witnessed here only
		// for its failure tail — see TestEqRecoveryPathLacksAWitness.
		{"eq-left-fails-right-int", nil, mkPrim("==", v(5), i(2)), nil},
		{"eq-left-fails-right-bool", nil, mkPrim("==", v(5), bt(true)), nil},
		{"eq-in-if-cond", nil, mkIf(mkPrim("==", i(1), i(1)), i(1), i(2)), nil},
		{"eq-check-mode", nil, mkPrim("==", i(1), i(1)), tBool()},
		{"eq-check-mode-mismatch", nil, mkPrim("==", i(1), i(1)), tInt()},

		// --- `lam`: the invariant is the CHECK-MODE FALL-THROUGH ------------
		{"lam-synth-identity", nil, mkLam(tInt(), v(0)), nil},
		{"lam-synth-body-uses-param", nil, mkLam(tInt(), mkPrim("+", v(0), i(1))), nil},
		{"lam-synth-body-fails", nil, mkLam(tInt(), v(5)), nil},
		{"lam-synth-nested", nil, mkLam(tInt(), mkLam(tBool(), v(1))), nil},
		{"lam-synth-shadowing", []*Ty{tBool()}, mkLam(tInt(), v(1)), nil},

		// check against a FUNCTION type: parameter compared, body checked.
		{"lam-check-matching", nil, mkLam(tInt(), v(0)), tFun(tInt(), tInt())},
		{"lam-check-param-mismatch", nil, mkLam(tInt(), v(0)), tFun(tBool(), tBool())},
		{"lam-check-body-mismatch", nil, mkLam(tInt(), v(0)), tFun(tInt(), tBool())},
		{"lam-check-nested", nil, mkLam(tInt(), mkLam(tBool(), v(1))),
			tFun(tInt(), tFun(tBool(), tInt()))},

		// THE FALL-THROUGH. check.go's `case "lam"` does not return when the
		// expected type is not a function, so the diagnostic is the generic
		// "expected X, got (-> ...)" and NOT a lambda-specific message.
		{"lam-check-against-int", nil, mkLam(tInt(), v(0)), tInt()},
		{"lam-check-against-bool", nil, mkLam(tInt(), v(0)), tBool()},
		// Falling through means the BODY is synthesized, so a broken body is
		// reported by synthesis rather than by the comparison.
		{"lam-check-against-int-broken-body", nil, mkLam(tInt(), v(9)), tInt()},

		// `==` REFUSES FUNCTION TYPES — witnessable now that lam is ported.
		// Before this, deleting the tyHasFun check killed zero cases.
		{"eq-function-types", nil, mkPrim("==", mkLam(tInt(), v(0)), mkLam(tInt(), v(0))), nil},
		{"eq-function-left-only", nil, mkPrim("==", mkLam(tInt(), v(0)), i(1)), nil},
		{"eq-function-right-only", nil, mkPrim("==", i(1), mkLam(tInt(), v(0))), nil},

		// --- `app`: CURRIED, one argument per node -------------------------
		// Positive witnesses are possible now only because lam is ported.
		{"app-identity", nil, mkApp(mkLam(tInt(), v(0)), i(1)), nil},
		{"app-body-uses-param", nil, mkApp(mkLam(tInt(), mkPrim("+", v(0), i(1))), i(2)), nil},

		// Two applications, DISTINCT parameter types, so a machine that reused
		// the outer parameter type would be caught.
		{"app-curried-two", nil,
			mkApp(mkApp(mkLam(tInt(), mkLam(tBool(), v(1))), i(1)), bt(true)), nil},
		{"app-curried-returns-inner", nil,
			mkApp(mkApp(mkLam(tInt(), mkLam(tBool(), v(0))), i(1)), bt(true)), nil},
		// PARTIAL application: applying one of two parameters yields a function.
		{"app-partial-yields-function", nil,
			mkApp(mkLam(tInt(), mkLam(tBool(), v(1))), i(1)), nil},

		// Callee failures, in precedence order.
		{"app-callee-synth-fails", nil, mkApp(v(5), i(1)), nil},
		{"app-callee-non-function", nil, mkApp(i(1), i(2)), nil},
		{"app-callee-non-function-bool", nil, mkApp(bt(true), i(2)), nil},
		// The CALLEE is diagnosed before the argument: both are broken here,
		// and the callee's error must win.
		{"app-callee-beats-argument", nil, mkApp(i(1), v(9)), nil},
		// Too many arguments surfaces as "applied a non-function" at the node
		// where the callee stopped being one — currying has no arity check.
		{"app-too-many-arguments", nil,
			mkApp(mkApp(mkLam(tInt(), v(0)), i(1)), i(2)), nil},

		// Argument failures.
		{"app-argument-synth-fails", nil, mkApp(mkLam(tInt(), v(0)), v(7)), nil},
		{"app-argument-type-mismatch", nil, mkApp(mkLam(tInt(), v(0)), bt(true)), nil},
		{"app-argument-mismatch-nested", nil,
			mkApp(mkApp(mkLam(tInt(), mkLam(tBool(), v(1))), i(1)), i(2)), nil},

		// The argument is SYNTHESIZED and compared, never checked: a term that
		// only checks would fail here, and this pins the synthesize-first order.
		{"app-argument-is-synthesized", nil,
			mkApp(mkLam(tInt(), v(0)), mkIf(bt(true), i(1), bt(true))), nil},

		{"app-check-ok", nil, mkApp(mkLam(tInt(), v(0)), i(1)), tInt()},
		{"app-check-mismatch", nil, mkApp(mkLam(tInt(), v(0)), i(1)), tBool()},
		{"app-in-if", nil, mkIf(bt(true), mkApp(mkLam(tInt(), v(0)), i(1)), i(2)), nil},
	}
}

func mkApp(fn, arg *Term) *Term { return &Term{K: "app", A: fn, B: arg} }

func mkLam(param *Ty, body *Term) *Term { return &Term{K: "lam", Ty: param, A: body} }

func mkPrim(op string, args ...*Term) *Term {
	t := &Term{K: "prim", Op: op}
	for _, a := range args {
		t.Args = append(t.Args, *a)
	}
	return t
}

func v(idx int) *Term { return &Term{K: "var", Idx: idx} }
func mkLet(ty *Ty, bound, body *Term) *Term {
	return &Term{K: "let", Ty: ty, A: bound, B: body}
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
		{K: "ctor"},
		{K: "match"}, {K: "record"},
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

// TestEqRecoveryPathLacksAWitness records a GAP, and fails when the gap becomes
// closable so it cannot be forgotten.
//
// `==`'s recovery path is: left operand fails synthesis, right succeeds, and the
// LEFT is then CHECKED against the right's type — succeeding. Witnessing the
// SUCCESSFUL tail of that path needs an operand that fails synthesis but checks
// fine, and the natural one is a bare constructor: (== (Nil) (Nil [Int])), where
// (Nil) cannot be synthesized alone but checks against (List Int).
//
// No such term exists among the currently ported families: every ported form
// that fails to synthesize also fails to check. So the cases above exercise the
// recovery path only as far as its failure tail, and the machine's agreement on
// the SUCCESS tail is unwitnessed.
//
// ONE GAP REMAINS. The second — `==`'s function-type refusal, which no ported
// term could reach — was retired when `lam` landed and eq-function-types was
// added; the guard fired exactly as designed and was then removed with the gap
// it named.
//
// The assertion below is keyed to the family that makes its witness
// constructible, and FAILS when that family lands. That is the point: the
// reminder fires when the gap becomes closable, instead of depending on someone
// remembering an unwritten test six families later.
func TestEqWitnessGapsAreStillBlocked(t *testing.T) {
	st := canonicalStore(t)
	unported := func(k string) bool {
		m := &checkerMachine{st: st}
		_, err := m.run(checkerStep{mode: modeSynth, term: &Term{K: k}})
		var nf errFamilyNotPorted
		return errors.As(err, &nf)
	}
	if !unported("ctor") {
		t.Error("constructors are ported, so `==`'s RECOVERY path can now be witnessed " +
			"end to end: add (== (Nil) (Nil [Int])) — left fails synthesis, right " +
			"succeeds, left then CHECKS successfully — then drop this assertion")
	}
}

// TestAppRefusesRefSelfSpines keeps the family boundary explicit. Resolving a
// definition through the store and inferring type arguments across a whole
// application spine belongs to ref/self, not to `app`. Refusing by name — the
// same treatment `==` got inside prim — stops `app` absorbing two semantic
// families and blurring where a differential failure came from.
func TestAppRefusesRefSelfSpines(t *testing.T) {
	st := canonicalStore(t)
	for _, head := range []string{"ref", "self"} {
		m := &checkerMachine{st: st}
		term := mkApp(&Term{K: head}, i(1))
		_, err := m.run(checkerStep{mode: modeSynth, term: term})
		var nf errFamilyNotPorted
		if !errors.As(err, &nf) {
			t.Errorf("an app with a %s head must be refused by name, got %v", head, err)
		}
	}
	// ...while ordinary application still works, or the refusal above would
	// just be the whole family being absent.
	m := &checkerMachine{st: st}
	if _, err := m.run(checkerStep{mode: modeSynth, term: mkApp(mkLam(tInt(), v(0)), i(1))}); err != nil {
		t.Fatalf("ordinary application must still be ported, got %v", err)
	}
}
