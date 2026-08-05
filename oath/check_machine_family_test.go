package main

import (
	"encoding/json"
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

	// selfTy/selfTyVars stand in for the enclosing definition, which `self`
	// resolves against. Zero values mean "outside a function definition",
	// which is itself a case.
	selfTy     *Ty
	selfTyVars int
}

func i(n int64) *Term { return &Term{K: "int", Int: big.NewInt(n)} }

func portedFamilyCases() []familyCase {
	return []familyCase{
		// synth of every ported leaf
		{name: "synth-int", ctx: nil, term: i(7), exp: nil},
		{name: "synth-bool", ctx: nil, term: &Term{K: "bool", Bool: true}, exp: nil},
		{name: "synth-rat", ctx: nil, term: &Term{K: "rat", Rat: big.NewRat(1, 2)}, exp: nil},
		{name: "synth-float", ctx: nil, term: &Term{K: "float", Float: 1.5}, exp: nil},
		{name: "synth-var-0", ctx: []*Ty{tInt()}, term: &Term{K: "var", Idx: 0}, exp: nil},
		{name: "synth-var-1", ctx: []*Ty{tBool(), tInt()}, term: &Term{K: "var", Idx: 1}, exp: nil},

		// de Bruijn edges: the index must be read from the RIGHT end
		{name: "synth-var-innermost", ctx: []*Ty{tBool(), tInt()}, term: &Term{K: "var", Idx: 0}, exp: nil},
		{name: "synth-var-out-of-scope", ctx: []*Ty{tInt()}, term: &Term{K: "var", Idx: 3}, exp: nil},
		{name: "synth-var-negative", ctx: []*Ty{tInt()}, term: &Term{K: "var", Idx: -1}, exp: nil},
		{name: "synth-var-empty-ctx", ctx: nil, term: &Term{K: "var", Idx: 0}, exp: nil},

		// check via the default path: agreeing and disagreeing
		{name: "check-int-ok", ctx: nil, term: i(1), exp: tInt()},
		{name: "check-int-vs-bool", ctx: nil, term: i(1), exp: tBool()},
		{name: "check-bool-ok", ctx: nil, term: &Term{K: "bool"}, exp: tBool()},
		{name: "check-rat-vs-int", ctx: nil, term: &Term{K: "rat", Rat: big.NewRat(1, 2)}, exp: tInt()},
		{name: "check-float-ok", ctx: nil, term: &Term{K: "float", Float: 0}, exp: tFloat()},
		{name: "check-var-ok", ctx: []*Ty{tInt()}, term: &Term{K: "var", Idx: 0}, exp: tInt()},
		{name: "check-var-mismatch", ctx: []*Ty{tBool()}, term: &Term{K: "var", Idx: 0}, exp: tInt()},
		{name: "check-var-out-of-scope", ctx: nil, term: &Term{K: "var", Idx: 0}, exp: tInt()},

		// missing term: both must refuse rather than fault
		{name: "synth-nil-term", ctx: nil, term: nil, exp: nil},

		// --- `if`: SEQUENCING is the invariant, not just the answer ---------
		// Symmetric branches would let a machine that reordered or skipped a
		// branch agree by coincidence, so every case below is asymmetric.
		{name: "if-synth-agree", ctx: nil, term: mkIf(bt(true), i(1), i(2)), exp: nil},
		{name: "if-synth-disagree-bool-int", ctx: nil, term: mkIf(bt(true), bt(true), i(1)), exp: nil},
		{name: "if-synth-disagree-int-bool", ctx: nil, term: mkIf(bt(true), i(1), bt(true)), exp: nil},
		{name: "if-synth-bad-cond", ctx: nil, term: mkIf(i(1), i(1), i(2)), exp: nil},
		// The condition must be diagnosed BEFORE the branches, even when the
		// branches also disagree — otherwise a reordered machine reports a
		// different (and still plausible) error.
		{name: "if-synth-cond-beats-branches", ctx: nil, term: mkIf(i(1), bt(true), i(2)), exp: nil},
		{name: "if-synth-nested", ctx: nil, term: mkIf(bt(true), mkIf(bt(false), i(1), i(2)), i(3)), exp: nil},

		// CHECK MODE: both branches go against exp. A machine that checked the
		// else-branch against the THEN-branch's inferred type would accept
		// if-check-both-wrong, which the real checker rejects.
		{name: "if-check-ok", ctx: nil, term: mkIf(bt(true), i(1), i(2)), exp: tInt()},
		{name: "if-check-both-wrong", ctx: nil, term: mkIf(bt(true), i(1), i(2)), exp: tBool()},
		{name: "if-check-else-wrong", ctx: nil, term: mkIf(bt(true), i(1), bt(true)), exp: tInt()},
		// then fails first: a machine checking the else first reports the other
		// branch's message.
		{name: "if-check-then-wrong", ctx: nil, term: mkIf(bt(true), bt(true), i(1)), exp: tInt()},
		{name: "if-check-bad-cond", ctx: nil, term: mkIf(i(1), bt(true), bt(false)), exp: tBool()},
		// BOTH branches wrong, with DIFFERENT messages: the only shape in which
		// branch ORDER is observable. if-check-both-wrong cannot see it, because
		// two Int branches against Bool produce the same text twice.
		{name: "if-check-both-wrong-differently", ctx: nil, term: mkIf(bt(true), bt(true), &Term{K: "rat", Rat: big.NewRat(3, 2)}), exp: tInt()},
		{name: "if-synth-both-wrong-differently", ctx: nil, term: mkIf(bt(true), bt(true), &Term{K: "rat", Rat: big.NewRat(3, 2)}), exp: nil},
		{name: "if-check-nested", ctx: nil, term: mkIf(bt(true), mkIf(bt(false), i(1), i(2)), i(3)), exp: tInt()},
		{name: "if-check-var-branch", ctx: []*Ty{tInt()}, term: mkIf(bt(true), &Term{K: "var", Idx: 0}, i(2)), exp: tInt()},

		// --- `let`: the invariant is CONTEXT SCOPE, not the answer ----------
		{name: "let-synth-basic", ctx: nil, term: mkLet(tBool(), bt(true), v(0)), exp: nil},
		{name: "let-synth-annotation-mismatch", ctx: nil, term: mkLet(tBool(), i(1), v(0)), exp: nil},
		{name: "let-check-basic", ctx: nil, term: mkLet(tBool(), bt(true), v(0)), exp: tBool()},
		// synth says "let annotation mismatch"; check says "expected ... got".
		// Same broken program, two different diagnostics, both preserved.
		{name: "let-check-annotation-mismatch", ctx: nil, term: mkLet(tBool(), i(1), v(0)), exp: tBool()},
		{name: "let-check-body-mismatch", ctx: nil, term: mkLet(tBool(), bt(true), v(0)), exp: tInt()},

		// The bound value is evaluated in the ORIGINAL context: Var 0 here is
		// the OUTER binding, not the one being introduced.
		{name: "let-bound-uses-outer-ctx", ctx: []*Ty{tInt()}, term: mkLet(tInt(), v(0), v(0)), exp: nil},
		{name: "let-bound-sees-no-self", ctx: nil, term: mkLet(tInt(), v(0), v(0)), exp: nil},

		// de Bruijn ordering under nesting: Var 0 is the innermost let, Var 1
		// the outer one, Var 2 the pre-existing binding.
		{name: "let-nested-inner", ctx: nil, term: mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(0))), exp: nil},
		{name: "let-nested-outer", ctx: nil, term: mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(1))), exp: nil},
		{name: "let-nested-preexisting", ctx: []*Ty{tFloat()}, term: mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(2))), exp: nil},
		{name: "let-nested-out-of-scope", ctx: nil, term: mkLet(tBool(), bt(true), mkLet(tInt(), i(1), v(2))), exp: nil},

		// THE LEAKAGE WITNESS. Var 0 resolves to Int outside and to Bool inside
		// the let, so if the let's extended context survived into the SIBLING
		// else-branch the two branches would agree and this would be accepted.
		// "Forget to pop" is invisible without a sibling to leak into.
		{name: "let-no-leak-to-sibling", ctx: []*Ty{tInt()}, term: mkIf(bt(true), mkLet(tBool(), bt(true), v(0)), v(0)), exp: nil},
		// Same shape in check mode: the else-branch must still see Int.
		{name: "let-no-leak-to-sibling-check", ctx: []*Ty{tInt()}, term: mkIf(bt(true), mkLet(tInt(), i(9), v(0)), v(0)), exp: tInt()},
		// A FAILING body must not leak the extended context either: the else
		// branch is evaluated after the then-branch has unwound with an error.
		{name: "let-no-leak-after-failure", ctx: []*Ty{tInt()}, term: mkIf(bt(true), mkLet(tBool(), i(1), v(0)), v(0)), exp: nil},
		// let inside a let's BOUND position, so the context grows and shrinks
		// on the way to a sibling rather than only on the way down.
		{name: "let-in-bound-position", ctx: []*Ty{tInt()}, term: mkLet(tBool(), mkLet(tFloat(), &Term{K: "float"}, bt(true)), v(1)), exp: nil},

		// --- `prim`: the invariant is INDEXED TRAVERSAL --------------------
		{name: "prim-add-ok", ctx: nil, term: mkPrim("+", i(1), i(2)), exp: nil},
		{name: "prim-unary-neg", ctx: nil, term: mkPrim("neg", i(1)), exp: nil},
		{name: "prim-unary-not", ctx: nil, term: mkPrim("not", bt(true)), exp: nil},
		{name: "prim-and", ctx: nil, term: mkPrim("and", bt(true), bt(false)), exp: nil},

		// BOUNDARIES: loop machinery usually gets many-argument right and the
		// edges wrong.
		{name: "prim-zero-args", ctx: nil, term: mkPrim("+"), exp: nil},
		{name: "prim-zero-args-not", ctx: nil, term: mkPrim("not"), exp: nil},
		{name: "prim-one-arg-to-binary", ctx: nil, term: mkPrim("+", i(1)), exp: nil},
		{name: "prim-three-args-to-binary", ctx: nil, term: mkPrim("+", i(1), i(2), i(3)), exp: nil},

		// ASYMMETRIC per-position failures. Identical arguments cannot witness
		// traversal order; these fail DIFFERENTLY per position, so skipping,
		// reordering, or double-visiting produces a distinguishable diagnostic.
		{name: "prim-arg0-fails", ctx: nil, term: mkPrim("+", v(5), i(2)), exp: nil},
		{name: "prim-arg1-fails", ctx: nil, term: mkPrim("+", i(1), v(7)), exp: nil},
		// LEFTMOST wins: a machine that visits right-to-left reports index 7.
		{name: "prim-both-fail-differently", ctx: nil, term: mkPrim("+", v(5), v(7)), exp: nil},
		{name: "prim-arg2-fails-of-three", ctx: nil, term: mkPrim("+", i(1), i(2), v(9)), exp: nil},

		// Off-by-one in the accumulator shows up as the OPERATOR's diagnostic,
		// because the wrong types reach primResultTy.
		{name: "prim-mixed-numeric", ctx: nil, term: mkPrim("+", i(1), &Term{K: "rat", Rat: big.NewRat(1, 2)}), exp: nil},
		{name: "prim-bool-into-arith", ctx: nil, term: mkPrim("+", i(1), bt(true)), exp: nil},
		{name: "prim-int-into-bool-op", ctx: nil, term: mkPrim("and", bt(true), i(1)), exp: nil},
		{name: "prim-order-matters", ctx: nil, term: mkPrim("+", bt(true), i(1)), exp: nil},

		{name: "prim-nested", ctx: nil, term: mkPrim("+", mkPrim("+", i(1), i(2)), i(3)), exp: nil},
		{name: "prim-nested-inner-fails", ctx: nil, term: mkPrim("+", mkPrim("+", i(1), v(4)), i(3)), exp: nil},
		{name: "prim-in-if", ctx: nil, term: mkIf(mkPrim("not", bt(true)), i(1), i(2)), exp: nil},
		{name: "prim-in-let-body", ctx: nil, term: mkLet(tInt(), i(1), mkPrim("+", v(0), i(2))), exp: nil},

		// check mode falls through to synthesize-then-compare, as check.go does.
		{name: "prim-check-ok", ctx: nil, term: mkPrim("+", i(1), i(2)), exp: tInt()},
		{name: "prim-check-mismatch", ctx: nil, term: mkPrim("+", i(1), i(2)), exp: tBool()},
		{name: "prim-check-arg-fails", ctx: nil, term: mkPrim("+", v(5), i(2)), exp: tInt()},

		// --- `==`: the invariant is RECOVERY, not traversal -----------------
		// Path 1: left synthesizes, right is CHECKED against its type.
		{name: "eq-left-synths", ctx: nil, term: mkPrim("==", i(1), i(2)), exp: nil},
		{name: "eq-left-synths-mismatch", ctx: nil, term: mkPrim("==", i(1), bt(true)), exp: nil},
		{name: "eq-bools", ctx: nil, term: mkPrim("==", bt(true), bt(false)), exp: nil},
		{name: "eq-nested", ctx: nil, term: mkPrim("==", mkPrim("==", i(1), i(1)), bt(true)), exp: nil},

		// Path 3: BOTH operands fail synthesis. check.go discards both original
		// errors for a fixed message, so a machine that propagates the first
		// one — which looks more informative — is observably different.
		{name: "eq-both-fail", ctx: nil, term: mkPrim("==", v(5), v(7)), exp: nil},
		{name: "eq-both-fail-same", ctx: nil, term: mkPrim("==", v(9), v(9)), exp: nil},

		// Arity is checked before either operand runs.
		{name: "eq-arity-one", ctx: nil, term: mkPrim("==", i(1)), exp: nil},
		{name: "eq-arity-three", ctx: nil, term: mkPrim("==", i(1), i(2), i(3)), exp: nil},
		{name: "eq-arity-zero", ctx: nil, term: mkPrim("=="), exp: nil},

		// Left fails, right succeeds: the RECOVERY path. Witnessed here only
		// for its failure tail — see TestEqRecoveryPathLacksAWitness.
		{name: "eq-left-fails-right-int", ctx: nil, term: mkPrim("==", v(5), i(2)), exp: nil},
		{name: "eq-left-fails-right-bool", ctx: nil, term: mkPrim("==", v(5), bt(true)), exp: nil},
		{name: "eq-in-if-cond", ctx: nil, term: mkIf(mkPrim("==", i(1), i(1)), i(1), i(2)), exp: nil},
		{name: "eq-check-mode", ctx: nil, term: mkPrim("==", i(1), i(1)), exp: tBool()},
		{name: "eq-check-mode-mismatch", ctx: nil, term: mkPrim("==", i(1), i(1)), exp: tInt()},

		// --- `lam`: the invariant is the CHECK-MODE FALL-THROUGH ------------
		{name: "lam-synth-identity", ctx: nil, term: mkLam(tInt(), v(0)), exp: nil},
		{name: "lam-synth-body-uses-param", ctx: nil, term: mkLam(tInt(), mkPrim("+", v(0), i(1))), exp: nil},
		{name: "lam-synth-body-fails", ctx: nil, term: mkLam(tInt(), v(5)), exp: nil},
		{name: "lam-synth-nested", ctx: nil, term: mkLam(tInt(), mkLam(tBool(), v(1))), exp: nil},
		{name: "lam-synth-shadowing", ctx: []*Ty{tBool()}, term: mkLam(tInt(), v(1)), exp: nil},

		// check against a FUNCTION type: parameter compared, body checked.
		{name: "lam-check-matching", ctx: nil, term: mkLam(tInt(), v(0)), exp: tFun(tInt(), tInt())},
		{name: "lam-check-param-mismatch", ctx: nil, term: mkLam(tInt(), v(0)), exp: tFun(tBool(), tBool())},
		{name: "lam-check-body-mismatch", ctx: nil, term: mkLam(tInt(), v(0)), exp: tFun(tInt(), tBool())},
		{name: "lam-check-nested", ctx: nil, term: mkLam(tInt(), mkLam(tBool(), v(1))), exp: tFun(tInt(), tFun(tBool(), tInt()))},

		// THE FALL-THROUGH. check.go's `case "lam"` does not return when the
		// expected type is not a function, so the diagnostic is the generic
		// "expected X, got (-> ...)" and NOT a lambda-specific message.
		{name: "lam-check-against-int", ctx: nil, term: mkLam(tInt(), v(0)), exp: tInt()},
		{name: "lam-check-against-bool", ctx: nil, term: mkLam(tInt(), v(0)), exp: tBool()},
		// Falling through means the BODY is synthesized, so a broken body is
		// reported by synthesis rather than by the comparison.
		{name: "lam-check-against-int-broken-body", ctx: nil, term: mkLam(tInt(), v(9)), exp: tInt()},

		// `==` REFUSES FUNCTION TYPES — witnessable now that lam is ported.
		// Before this, deleting the tyHasFun check killed zero cases.
		{name: "eq-function-types", ctx: nil, term: mkPrim("==", mkLam(tInt(), v(0)), mkLam(tInt(), v(0))), exp: nil},
		{name: "eq-function-left-only", ctx: nil, term: mkPrim("==", mkLam(tInt(), v(0)), i(1)), exp: nil},
		{name: "eq-function-right-only", ctx: nil, term: mkPrim("==", i(1), mkLam(tInt(), v(0))), exp: nil},

		// --- `app`: CURRIED, one argument per node -------------------------
		// Positive witnesses are possible now only because lam is ported.
		{name: "app-identity", ctx: nil, term: mkApp(mkLam(tInt(), v(0)), i(1)), exp: nil},
		{name: "app-body-uses-param", ctx: nil, term: mkApp(mkLam(tInt(), mkPrim("+", v(0), i(1))), i(2)), exp: nil},

		// Two applications, DISTINCT parameter types, so a machine that reused
		// the outer parameter type would be caught.
		{name: "app-curried-two", ctx: nil, term: mkApp(mkApp(mkLam(tInt(), mkLam(tBool(), v(1))), i(1)), bt(true)), exp: nil},
		{name: "app-curried-returns-inner", ctx: nil, term: mkApp(mkApp(mkLam(tInt(), mkLam(tBool(), v(0))), i(1)), bt(true)), exp: nil},
		// PARTIAL application: applying one of two parameters yields a function.
		{name: "app-partial-yields-function", ctx: nil, term: mkApp(mkLam(tInt(), mkLam(tBool(), v(1))), i(1)), exp: nil},

		// Callee failures, in precedence order.
		{name: "app-callee-synth-fails", ctx: nil, term: mkApp(v(5), i(1)), exp: nil},
		{name: "app-callee-non-function", ctx: nil, term: mkApp(i(1), i(2)), exp: nil},
		{name: "app-callee-non-function-bool", ctx: nil, term: mkApp(bt(true), i(2)), exp: nil},
		// The CALLEE is diagnosed before the argument: both are broken here,
		// and the callee's error must win.
		{name: "app-callee-beats-argument", ctx: nil, term: mkApp(i(1), v(9)), exp: nil},
		// Too many arguments surfaces as "applied a non-function" at the node
		// where the callee stopped being one — currying has no arity check.
		{name: "app-too-many-arguments", ctx: nil, term: mkApp(mkApp(mkLam(tInt(), v(0)), i(1)), i(2)), exp: nil},

		// Argument failures.
		{name: "app-argument-synth-fails", ctx: nil, term: mkApp(mkLam(tInt(), v(0)), v(7)), exp: nil},
		{name: "app-argument-type-mismatch", ctx: nil, term: mkApp(mkLam(tInt(), v(0)), bt(true)), exp: nil},
		{name: "app-argument-mismatch-nested", ctx: nil, term: mkApp(mkApp(mkLam(tInt(), mkLam(tBool(), v(1))), i(1)), i(2)), exp: nil},

		// The argument is SYNTHESIZED and compared, never checked: a term that
		// only checks would fail here, and this pins the synthesize-first order.
		{name: "app-argument-is-synthesized", ctx: nil, term: mkApp(mkLam(tInt(), v(0)), mkIf(bt(true), i(1), bt(true))), exp: nil},

		{name: "app-check-ok", ctx: nil, term: mkApp(mkLam(tInt(), v(0)), i(1)), exp: tInt()},
		{name: "app-check-mismatch", ctx: nil, term: mkApp(mkLam(tInt(), v(0)), i(1)), exp: tBool()},
		{name: "app-in-if", ctx: nil, term: mkIf(bt(true), mkApp(mkLam(tInt(), v(0)), i(1)), i(2)), exp: nil},

		// --- `record` / `field`: INTERLEAVED ordering and synthesis ---------
		{name: "record-empty", ctx: nil, term: mkRec(nil), exp: nil},
		{name: "record-one", ctx: nil, term: mkRec([]string{"a"}, i(1)), exp: nil},
		{name: "record-sorted", ctx: nil, term: mkRec([]string{"a", "b"}, i(1), bt(true)), exp: nil},
		{name: "record-three", ctx: nil, term: mkRec([]string{"a", "b", "c"}, i(1), bt(true), i(2)), exp: nil},

		{name: "record-names-values-mismatch", ctx: nil, term: &Term{K: "record", Names: []string{"a", "b"}, Args: []Term{*i(1)}}, exp: nil},
		{name: "record-unsorted", ctx: nil, term: mkRec([]string{"b", "a"}, i(1), bt(true)), exp: nil},
		{name: "record-duplicate-names", ctx: nil, term: mkRec([]string{"a", "a"}, i(1), bt(true)), exp: nil},

		// THE INTERLEAVING. Ordering for index i is checked BEFORE value i is
		// synthesized, so a broken value at the OUT-OF-ORDER index is never
		// reached and the ordering error wins...
		{name: "record-unsorted-beats-later-bad-value", ctx: nil, term: mkRec([]string{"b", "a"}, i(1), v(9)), exp: nil},
		// ...while a broken value at index 0 is reported first, because index 0
		// has no predecessor to compare against.
		{name: "record-bad-value-at-zero-beats-unsorted", ctx: nil, term: mkRec([]string{"b", "a"}, v(9), i(1)), exp: nil},
		{name: "record-bad-value-middle", ctx: nil, term: mkRec([]string{"a", "b", "c"}, i(1), v(9), i(2)), exp: nil},
		{name: "record-two-bad-values-differently", ctx: nil, term: mkRec([]string{"a", "b"}, v(5), v(7)), exp: nil},

		{name: "field-ok", ctx: nil, term: mkField(mkRec([]string{"a", "b"}, i(1), bt(true)), "a"), exp: nil},
		{name: "field-ok-second", ctx: nil, term: mkField(mkRec([]string{"a", "b"}, i(1), bt(true)), "b"), exp: nil},
		{name: "field-missing", ctx: nil, term: mkField(mkRec([]string{"a"}, i(1)), "zz"), exp: nil},
		{name: "field-on-non-record", ctx: nil, term: mkField(i(1), "a"), exp: nil},
		{name: "field-record-fails", ctx: nil, term: mkField(mkRec([]string{"b", "a"}, i(1), i(2)), "a"), exp: nil},
		{name: "field-nested", ctx: nil, term: mkField(mkRec([]string{"r"}, mkRec([]string{"a"}, i(1))), "r"), exp: nil},

		{name: "record-check-ok", ctx: nil, term: mkRec([]string{"a"}, i(1)), exp: &Ty{K: "record", Names: []string{"a"}, Args: []Ty{*tInt()}}},
		{name: "record-check-mismatch", ctx: nil, term: mkRec([]string{"a"}, i(1)), exp: tInt()},
		{name: "field-check-ok", ctx: nil, term: mkField(mkRec([]string{"a"}, i(1)), "a"), exp: tInt()},
		{name: "record-in-lam", ctx: nil, term: mkLam(tInt(), mkRec([]string{"a"}, v(0))), exp: nil},
	}
}

func mkRec(names []string, vals ...*Term) *Term {
	t := &Term{K: "record", Names: names}
	for _, x := range vals {
		t.Args = append(t.Args, *x)
	}
	return t
}
func mkField(rec *Term, name string) *Term { return &Term{K: "field", A: rec, Op: name} }

func mkApp(fn, arg *Term) *Term { return &Term{K: "app", A: fn, B: arg} }

// snapshotTerm renders a term for mutation detection. Total by construction,
// unlike the canonical encoder — see the note at its call site.
func snapshotTerm(t *Term) string {
	if t == nil {
		return "<nil>"
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "<unmarshalable: " + err.Error() + ">"
	}
	return string(b)
}

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

	cases := append(portedFamilyCases(), matchCases(t, st)...)
	cases = append(cases, refSelfCases(t, st)...)
	cases = append(cases, ctorCases(t, st)...)
	cases = append(cases, inferAppCases(t, st)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// EACH CHECKER GETS ITS OWN COPY. Constructors publish TyArgs into
			// the term, so running both over one term would let the second see
			// the first's mutation — and a constructor with TyArgs already
			// populated takes the MONOMORPHIC route, so the machine would be
			// silently tested on a different path than the recursive checker.
			//
			// The snapshots are then compared to EACH OTHER: the claim is that
			// both checkers leave the term in the same state, not that neither
			// writes to it. Families before this one write nothing, and that
			// remains visible as both snapshots equalling the original.
			//
			// json.Marshal, NOT hashDef. The canonical encoder is deliberately
			// PARTIAL — it panics on unknown kinds and malformed structures —
			// because production only ever hashes Defs that already passed the
			// gate. This population contains deliberately malformed terms (a
			// record whose Names and Args disagree), so hashing them was a
			// defect in the test rather than in either checker. Marshalling is
			// total over these structs and still detects any field change.
			before := snapshotTerm(tc.term)
			var recTerm, machTerm *Term
			if tc.term != nil { // deepCopyTerm(nil) yields an empty term, not nil
				recTerm, machTerm = deepCopyTerm(tc.term), deepCopyTerm(tc.term)
			}

			rc := &checker{st: st, selfTy: tc.selfTy, selfTyVars: tc.selfTyVars}
			var rTy *Ty
			var rErr error
			if tc.exp == nil {
				rTy, rErr = rc.synth(tc.ctx, recTerm)
			} else {
				rErr = rc.check(tc.ctx, recTerm, tc.exp)
			}

			m := &checkerMachine{st: st, selfTy: tc.selfTy, selfTyVars: tc.selfTyVars}
			step := checkerStep{mode: modeSynth, ctx: tc.ctx, term: machTerm}
			if tc.exp != nil {
				step = checkerStep{mode: modeCheck, ctx: tc.ctx, term: machTerm, exp: tc.exp}
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
			// SURVIVING MUTATION MUST MATCH. Constructors publish solved type
			// arguments into the term; that is the behaviour under test, not a
			// defect. What must hold is that both checkers leave the term
			// identical — including leaving it untouched where nothing infers.
			if got, want := snapshotTerm(machTerm), snapshotTerm(recTerm); got != want {
				t.Fatalf("the checkers left the term in DIFFERENT states\n"+
					"  original:  %s\n  recursive: %s\n  machine:   %s", before, want, got)
			}
			// INFERENCE ACCOUNTING MUST MATCH, exactly. This is the complexity
			// witness: a machine that loses the memo effect of publishing
			// TyArgs mid-flight computes the same type and enters inference
			// exponentially more often.
			if rc.inferEntries != m.inferEntries {
				t.Fatalf("inference entry count differs: recursive=%d machine=%d",
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
		{K: "str"},
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
// The two deferred `==` witnesses are now CLOSED, and the guard that held them
// open retired with them:
//
//	function-type refusal  closed when `lam` landed (step 8)
//	recovery-success path  closed here, by eq-recovery-bare-ctor-left
//
// The guard failed on the exact commit that made each witness constructible,
// which is what a deferred obligation should do instead of relying on someone
// remembering an unwritten test six families later.

// TestAppRefusesRefSelfSpines was retired when the spine family landed: `app`
// no longer refuses a ref/self head, because inferAppCases now witnesses it.

// matchCases needs the corpus, because a match's arms are per-CONSTRUCTOR and
// its scrutinee must have a data type. `List` supplies both: Nil binds nothing,
// Cons binds Int and (List Int), so the two arms are ASYMMETRIC — identical
// arms could not witness per-arm context extension, traversal order, or which
// arm establishes the result.
//
// The scrutinee is a `var` of list type rather than a constructor, so this
// family is testable before `ctor` is ported.
func matchCases(t *testing.T, st *Store) []familyCase {
	t.Helper()
	consHash, _, ok := st.FindCtor("Cons")
	if !ok {
		t.Fatal("corpus has no Cons; match cannot be exercised")
	}
	listInt := &Ty{K: "data", Hash: consHash, Args: []Ty{*tInt()}}
	inList := []*Ty{listInt}

	okHash, _, ok2 := st.FindCtor("Ok")
	if !ok2 {
		t.Fatal("corpus has no Ok; the both-arms-bind witness is unavailable")
	}
	inResult := []*Ty{{K: "data", Hash: okHash, Args: []Ty{*tInt(), *tBool()}}}

	mkMatch := func(scrut *Term, arms ...*Term) *Term {
		m := &Term{K: "match", A: scrut}
		for _, a := range arms {
			m.Arms = append(m.Arms, *a)
		}
		return m
	}
	// Inside the Cons arm the context gains Int then (List Int), so Var 0 is
	// the tail, Var 1 the head, Var 2 the original scrutinee.
	return []familyCase{
		{name: "match-arms-agree", ctx: inList, term: mkMatch(v(0), i(0), v(1)), exp: nil},
		{name: "match-arms-disagree", ctx: inList, term: mkMatch(v(0), i(0), v(0)), exp: nil},
		// Reversed, so the "X vs Y" order in the diagnostic is observable.
		{name: "match-arms-disagree-reversed", ctx: inList, term: mkMatch(v(0), bt(true), v(1)), exp: nil},

		// PER-ARM CONTEXT. The Nil arm binds nothing, so Var 0 there is the
		// scrutinee itself; in the Cons arm Var 0 is the tail. A machine that
		// reused one arm's context for the other changes both arms' types.
		{name: "match-nil-arm-sees-scrutinee", ctx: inList, term: mkMatch(v(0), v(0), v(0)), exp: nil},
		{name: "match-cons-arm-binds-head", ctx: inList, term: mkMatch(v(0), i(0), v(1)), exp: nil},
		{name: "match-cons-arm-binds-tail", ctx: inList, term: mkMatch(v(0), v(0), v(0)), exp: nil},
		{name: "match-cons-arm-reaches-outer", ctx: inList, term: mkMatch(v(0), v(0), v(2)), exp: nil},
		// Out of scope in the NIL arm but in scope in the Cons arm: a machine
		// that extended both arms identically would accept this.
		{name: "match-nil-arm-out-of-scope", ctx: inList, term: mkMatch(v(0), v(1), v(1)), exp: nil},

		// Scrutinee and shape failures, in precedence order.
		{name: "match-scrutinee-not-data", ctx: nil, term: mkMatch(i(1), i(0), i(0)), exp: nil},
		{name: "match-scrutinee-fails", ctx: nil, term: mkMatch(v(9), i(0), i(0)), exp: nil},
		{name: "match-too-few-arms", ctx: inList, term: mkMatch(v(0), i(0)), exp: nil},
		{name: "match-too-many-arms", ctx: inList, term: mkMatch(v(0), i(0), i(1), i(2)), exp: nil},
		// The scrutinee is diagnosed before the arm count.
		{name: "match-scrutinee-beats-arm-count", ctx: nil, term: mkMatch(i(1), i(0)), exp: nil},

		// Arm failures: LEFTMOST wins, and the two fail differently.
		{name: "match-first-arm-fails", ctx: inList, term: mkMatch(v(0), v(9), v(1)), exp: nil},
		{name: "match-second-arm-fails", ctx: inList, term: mkMatch(v(0), i(0), v(9)), exp: nil},
		{name: "match-both-arms-fail-differently", ctx: inList, term: mkMatch(v(0), v(8), v(9)), exp: nil},

		// CHECK MODE: every arm is checked against exp; there is no candidate
		// and no arms-disagree diagnostic.
		{name: "match-check-ok", ctx: inList, term: mkMatch(v(0), i(0), v(1)), exp: tInt()},
		{name: "match-check-arm-mismatch", ctx: inList, term: mkMatch(v(0), i(0), v(0)), exp: tInt()},
		{name: "match-check-both-arms-wrong", ctx: inList, term: mkMatch(v(0), bt(true), v(0)), exp: tInt()},
		{name: "match-check-scrutinee-not-data", ctx: nil, term: mkMatch(i(1), i(0), i(0)), exp: tInt()},

		// `Result [a e]` is the witness `List` cannot provide: BOTH its
		// constructors bind, so a context that leaks from arm 0 into arm 1
		// changes what Var 1 resolves to there. In List the only binding
		// constructor is the LAST arm, leaving a leak nothing to contaminate —
		// which is why a leak mutant killed zero cases until this was added.
		{name: "match-result-both-arms-bind", ctx: inResult, term: mkMatch(v(0), v(0), v(0)), exp: nil},
		{name: "match-result-arm-reaches-outer", ctx: inResult, term: mkMatch(v(0), v(1), v(1)), exp: nil},
		{name: "match-result-leak-witness", ctx: inResult, term: mkMatch(v(0), i(0), v(1)), exp: nil},
		{name: "match-result-check-mode", ctx: inResult, term: mkMatch(v(0), v(0), i(0)), exp: tInt()},

		{name: "match-nested-in-arm", ctx: inList, term: mkMatch(v(0), i(0), mkMatch(v(0), i(1), v(1))), exp: nil},
		{name: "match-in-lam", ctx: nil, term: mkLam(listInt, mkMatch(v(0), i(0), v(1))), exp: nil},
	}
}

// refSelfCases needs the corpus: a `ref` names a stored definition by hash, so
// the family cannot be exercised against hand-built terms alone.
func refSelfCases(t *testing.T, st *Store) []familyCase {
	t.Helper()
	names := st.Names()
	var mono, poly, data string
	for n, h := range names {
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		switch {
		case d.K == "data" && data == "":
			data = h
		case d.K == "func" && d.TyVars == 0 && mono == "":
			mono = h
			_ = n
		case d.K == "func" && d.TyVars == 1 && poly == "":
			poly = h
		}
	}
	if mono == "" || poly == "" || data == "" {
		t.Fatalf("corpus lacks a monomorphic func, a 1-parameter func, or a datatype "+
			"(%q %q %q); ref/self cannot be witnessed", mono, poly, data)
	}
	ref := func(h string, args ...Ty) *Term { return &Term{K: "ref", Hash: h, TyArgs: args} }
	self := func(args ...Ty) *Term { return &Term{K: "self", TyArgs: args} }

	return []familyCase{
		{name: "ref-monomorphic", term: ref(mono)},
		{name: "ref-polymorphic-instantiated", term: ref(poly, *tInt())},
		{name: "ref-polymorphic-other-instantiation", term: ref(poly, *tBool())},

		// Arity is reported BEFORE any argument's well-formedness, so a
		// wrong-count reference with a bad argument still blames the count.
		{name: "ref-too-few-tyargs", term: ref(poly)},
		{name: "ref-too-many-tyargs", term: ref(mono, *tInt())},
		{name: "ref-wrong-count-and-bad-arg", term: ref(mono, Ty{K: "data", Hash: "nope"})},
		{name: "ref-bad-tyarg", term: ref(poly, Ty{K: "data", Hash: "nope"})},

		{name: "ref-to-a-datatype", term: ref(data)},
		{name: "ref-unknown-hash", term: &Term{K: "ref", Hash: "0000000000000000"}},

		// `self` resolves against the ENCLOSING definition, not the store.
		{name: "self-outside-a-definition", term: self()},
		{name: "self-monomorphic", term: self(), selfTy: tInt()},
		{name: "self-wrong-tyarg-count", term: self(*tInt()), selfTy: tInt()},
		{name: "self-polymorphic", term: self(*tInt()),
			selfTy: tFun(&Ty{K: "var", Var: 0}, &Ty{K: "var", Var: 0}), selfTyVars: 1},
		{name: "self-polymorphic-missing-arg", term: self(),
			selfTy: tFun(&Ty{K: "var", Var: 0}, &Ty{K: "var", Var: 0}), selfTyVars: 1},

		// Composed with ported families, so substitution flows outward.
		{name: "ref-in-if", term: mkIf(bt(true), ref(mono), ref(mono))},
		// NOTE: an application whose HEAD is a ref is NOT this family — it is
		// the spine inference  refuses, covered by
		// TestAppRefusesRefSelfSpines. A case for it here would fail as
		// not-yet-ported, which is the correct answer to the wrong question.
		{name: "self-in-let-body", term: mkLet(tInt(), i(1), self()), selfTy: tInt()},
		{name: "ref-check-mode", term: ref(mono), exp: tInt()},
	}
}

// ctorCases is the last family, and it carries every mechanism the machine has
// accumulated: two routes, a suppressed inference pass, a substitution
// published mid-flight, and the memo that publication creates.
func ctorCases(t *testing.T, st *Store) []familyCase {
	t.Helper()
	find := func(name string) (string, int) {
		h, idx, ok := st.FindCtor(name)
		if !ok {
			t.Fatalf("corpus has no constructor %q; the final family cannot be witnessed", name)
		}
		return h, idx
	}
	consH, consI := find("Cons")
	nilH, nilI := find("Nil")
	someH, someI := find("Some")
	noneH, noneI := find("None")
	okH, okI := find("Ok")
	sconsH, sconsI := find("SCons")
	snilH, snilI := find("SNil")

	ctor := func(h string, idx int, tyargs []Ty, args ...*Term) *Term {
		c := &Term{K: "ctor", Hash: h, Idx: idx, TyArgs: tyargs}
		for _, a := range args {
			c.Args = append(c.Args, *a)
		}
		return c
	}
	intT, boolT := []Ty{*tInt()}, []Ty{*tBool()}
	listInt := &Ty{K: "data", Hash: consH, Args: []Ty{*tInt()}}

	// A monomorphic Str spine: SCons(Int, Str) with tyvars = 0.
	strSpine := func(n int) *Term {
		acc := ctor(snilH, snilI, nil)
		for k := 0; k < n; k++ {
			acc = ctor(sconsH, sconsI, nil, i(int64(97+k%26)), acc)
		}
		return acc
	}
	// A polymorphic Cons spine with type arguments OMITTED at every level.
	polySpine := func(n int) *Term {
		acc := ctor(nilH, nilI, intT)
		for k := 0; k < n; k++ {
			acc = ctor(consH, consI, nil, i(int64(k)), acc)
		}
		return acc
	}

	return []familyCase{
		// --- MONOMORPHIC ROUTE: no inference, whatever the depth ------------
		{name: "ctor-mono-snil", term: ctor(snilH, snilI, nil)},
		{name: "ctor-mono-scons-one", term: ctor(sconsH, sconsI, nil, i(97), ctor(snilH, snilI, nil))},
		{name: "ctor-mono-spine-8", term: strSpine(8)},
		{name: "ctor-mono-spine-64", term: strSpine(64)},
		{name: "ctor-mono-bad-field", term: ctor(sconsH, sconsI, nil, bt(true), ctor(snilH, snilI, nil))},

		// --- POLYMORPHIC ROUTE ---------------------------------------------
		{name: "ctor-poly-explicit", term: ctor(nilH, nilI, intT)},
		{name: "ctor-poly-inferred-from-arg", term: ctor(someH, someI, nil, i(1))},
		{name: "ctor-poly-inferred-bool", term: ctor(someH, someI, nil, bt(true))},
		{name: "ctor-poly-cons-inferred", term: ctor(consH, consI, nil, i(1), ctor(nilH, nilI, intT))},
		{name: "ctor-poly-spine-3", term: polySpine(3)},
		{name: "ctor-poly-spine-16", term: polySpine(16)},

		// A nullary polymorphic constructor determines nothing on its own; the
		// EXPECTED type must seed the substitution.
		{name: "ctor-none-bare", term: ctor(noneH, noneI, nil)},
		{name: "ctor-none-with-expected", term: ctor(noneH, noneI, nil),
			exp: &Ty{K: "data", Hash: someH, Args: []Ty{*tInt()}}},
		{name: "ctor-ok-underdetermined", term: ctor(okH, okI, nil, i(1))},
		{name: "ctor-ok-with-expected", term: ctor(okH, okI, nil, i(1)),
			exp: &Ty{K: "data", Hash: okH, Args: []Ty{*tInt(), *tBool()}}},

		// Arity, index and datatype failures.
		{name: "ctor-arity-too-few", term: ctor(consH, consI, nil, i(1))},
		{name: "ctor-arity-too-many", term: ctor(nilH, nilI, intT, i(1))},
		{name: "ctor-index-out-of-range", term: &Term{K: "ctor", Hash: consH, Idx: 99}},
		{name: "ctor-unknown-hash", term: &Term{K: "ctor", Hash: "0000000000000000"}},
		{name: "ctor-explicit-wrong-count", term: ctor(consH, consI, []Ty{*tInt(), *tBool()}, i(1), ctor(nilH, nilI, intT))},

		// Field mismatches under an explicit instantiation.
		{name: "ctor-explicit-field-mismatch", term: ctor(consH, consI, intT, bt(true), ctor(nilH, nilI, intT))},
		{name: "ctor-explicit-tail-mismatch", term: ctor(consH, consI, intT, i(1), ctor(nilH, nilI, boolT))},

		// PASS-1 SUPPRESSION: the first argument cannot be synthesized, the
		// second determines the parameter, and validation then succeeds.
		{name: "ctor-suppressed-then-ok", ctx: []*Ty{listInt},
			term: ctor(consH, consI, nil, i(1), v(0))},
		{name: "ctor-arg-fails-both-passes", term: ctor(someH, someI, nil, v(9))},

		// check mode: exp seeds the substitution AND is compared afterwards.
		{name: "ctor-check-matching", term: ctor(someH, someI, nil, i(1)),
			exp: &Ty{K: "data", Hash: someH, Args: []Ty{*tInt()}}},
		{name: "ctor-check-mismatch", term: ctor(someH, someI, nil, i(1)),
			exp: &Ty{K: "data", Hash: someH, Args: []Ty{*tBool()}}},
		{name: "ctor-check-against-int", term: ctor(someH, someI, nil, i(1)), exp: tInt()},

		// Composed with earlier families.
		{name: "ctor-in-if", term: mkIf(bt(true), ctor(someH, someI, nil, i(1)), ctor(noneH, noneI, intT))},
		{name: "ctor-in-lam", term: mkLam(tInt(), ctor(someH, someI, nil, v(0)))},
		{name: "ctor-in-match-arm", ctx: []*Ty{listInt},
			term: &Term{K: "match", A: v(0), Arms: []Term{
				*ctor(nilH, nilI, intT), *ctor(consH, consI, nil, v(1), v(0))}}},

		// THE DEFERRED `==` RECOVERY WITNESS, held open since step 7. The left
		// operand is a BARE constructor that cannot be synthesized alone; its
		// failure is suppressed; the right operand determines the type; the
		// left then CHECKS successfully against it.
		{name: "eq-recovery-bare-ctor-left",
			term: mkPrim("==", ctor(nilH, nilI, nil), ctor(nilH, nilI, intT))},
		{name: "eq-recovery-bare-ctor-right",
			term: mkPrim("==", ctor(nilH, nilI, intT), ctor(nilH, nilI, nil))},
		{name: "eq-recovery-both-bare",
			term: mkPrim("==", ctor(nilH, nilI, nil), ctor(nilH, nilI, nil))},
	}
}

// inferAppCases exercises the ref/self-headed application SPINE (#35): omitted
// type arguments inferred from the whole spine at once.
func inferAppCases(t *testing.T, st *Store) []familyCase {
	t.Helper()
	var poly1, poly2, mono, data string
	for _, h := range st.Names() {
		d, err := st.GetDef(h)
		if err != nil {
			continue
		}
		switch {
		case d.K == "data" && data == "":
			data = h
		case d.K != "func":
		case d.TyVars == 0 && mono == "":
			mono = h
		case d.TyVars == 1 && poly1 == "" && d.Ty != nil && d.Ty.K == "fun":
			poly1 = h
		case d.TyVars == 2 && poly2 == "" && d.Ty != nil && d.Ty.K == "fun":
			poly2 = h
		}
	}
	if poly1 == "" || mono == "" || data == "" {
		t.Fatalf("corpus lacks the definitions this family needs (%q %q %q)", poly1, mono, data)
	}
	ref := func(h string, args ...Ty) *Term { return &Term{K: "ref", Hash: h, TyArgs: args} }
	app := func(fn *Term, as ...*Term) *Term {
		for _, a := range as {
			fn = mkApp(fn, a)
		}
		return fn
	}
	selfTy1 := tFun(&Ty{K: "var", Var: 0}, &Ty{K: "var", Var: 0})

	cases := []familyCase{
		// Inference across the spine, with the head's type arguments omitted.
		{name: "inferapp-poly-one-arg", term: app(ref(poly1), i(1))},
		{name: "inferapp-poly-bool-arg", term: app(ref(poly1), bt(true))},
		// An argument that cannot be synthesized is SUPPRESSED in pass 1 and
		// CHECKED in pass 2 against the solved parameter.
		{name: "inferapp-suppressed-arg", term: app(ref(poly1), v(9))},

		// Applicability: these are NOT this family and must fall through to
		// ordinary application, not error here.
		{name: "inferapp-explicit-tyargs-falls-through", term: app(ref(poly1, *tInt()), i(1))},
		{name: "inferapp-monomorphic-head-falls-through", term: app(ref(mono), i(1))},
		{name: "inferapp-data-head-falls-through", term: app(ref(data), i(1))},
		{name: "inferapp-unknown-hash-falls-through",
			term: app(&Term{K: "ref", Hash: "0000000000000000"}, i(1))},

		// Over-application: the spine peels one parameter per argument.
		{name: "inferapp-too-many-arguments", term: app(ref(poly1), i(1), i(2), i(3))},

		// self-headed spines resolve against the enclosing definition.
		{name: "inferapp-self", term: app(&Term{K: "self"}, i(1)),
			selfTy: selfTy1, selfTyVars: 1},
		{name: "inferapp-self-outside-definition", term: app(&Term{K: "self"}, i(1))},
		{name: "inferapp-self-explicit-falls-through",
			term:   app(&Term{K: "self", TyArgs: []Ty{*tInt()}}, i(1)),
			selfTy: selfTy1, selfTyVars: 1},

		// check mode: exp seeds the substitution and is compared afterwards.
		{name: "inferapp-check-ok", term: app(ref(poly1), i(1)), exp: tInt()},
		{name: "inferapp-check-mismatch", term: app(ref(poly1), i(1)), exp: tBool()},

		{name: "inferapp-nested-in-if",
			term: mkIf(bt(true), app(ref(poly1), i(1)), app(ref(poly1), i(2)))},
	}
	if poly2 != "" {
		cases = append(cases,
			familyCase{name: "inferapp-two-tyvars", term: app(ref(poly2), i(1))},
			familyCase{name: "inferapp-two-tyvars-with-expected",
				term: app(ref(poly2), i(1)), exp: tInt()})
	}
	return cases
}

// TestLargeSpineWitnesses are the two #149 witnesses at scale, and they test
// DIFFERENT things — neither substitutes for the other:
//
//	monomorphic Str spine   STACK SAFETY. Shallow syntax, inside the portable
//	                        node profile, no inference at all. This is the shape
//	                        that crashes the recursive checker on wasm.
//	polymorphic Cons spine  COMPLEXITY. Every node enters inference exactly
//	                        once, which holds only because TyArgs is published
//	                        between the two passes.
//
// Both run NATIVELY here, where Go's growable stacks let the recursive checker
// survive too — so this pair does not by itself prove the wasm claim. It proves
// PARITY at depth. The discriminating witness on the failing target is the
// served-artifact gate; both are required.
func TestLargeSpineWitnesses(t *testing.T) {
	st := canonicalStore(t)
	sconsH, sconsI, ok := st.FindCtor("SCons")
	if !ok {
		t.Fatal("corpus has no SCons")
	}
	snilH, snilI, _ := st.FindCtor("SNil")
	consH, consI, _ := st.FindCtor("Cons")
	nilH, nilI, _ := st.FindCtor("Nil")

	ctor := func(h string, idx int, tyargs []Ty, args ...*Term) *Term {
		c := &Term{K: "ctor", Hash: h, Idx: idx, TyArgs: tyargs}
		for _, a := range args {
			c.Args = append(c.Args, *a)
		}
		return c
	}

	t.Run("monomorphic-5000-rune-string", func(t *testing.T) {
		const n = 5000
		build := func() *Term {
			acc := ctor(snilH, snilI, nil)
			for k := 0; k < n; k++ {
				acc = ctor(sconsH, sconsI, nil, i(int64(97+k%26)), acc)
			}
			return acc
		}
		rc := &checker{st: st}
		rTy, rErr := rc.synth(nil, build())
		m := &checkerMachine{st: st}
		mTy, mErr := m.run(checkerStep{mode: modeSynth, term: build()})

		if rErr != nil || mErr != nil {
			t.Fatalf("a %d-node Str spine must typecheck\n  recursive: %v\n  machine:   %v", n, rErr, mErr)
		}
		if !tyEq(rTy, mTy) {
			t.Fatalf("type differs: %s vs %s", debugTy(rTy), debugTy(mTy))
		}
		// MONOMORPHIC: no inference, whatever the depth. If this ever moves,
		// the route selection has broken and the spine is exponential.
		if rc.inferEntries != 0 || m.inferEntries != 0 {
			t.Fatalf("Str has no type parameters, so inference must never run: recursive=%d machine=%d",
				rc.inferEntries, m.inferEntries)
		}
	})

	t.Run("polymorphic-spine-inference-parity", func(t *testing.T) {
		for _, n := range []int{1, 2, 8, 64, 400} {
			build := func() *Term {
				acc := ctor(nilH, nilI, []Ty{*tInt()})
				for k := 0; k < n; k++ {
					acc = ctor(consH, consI, nil, i(int64(k)), acc)
				}
				return acc
			}
			rc := &checker{st: st}
			rTy, rErr := rc.synth(nil, build())
			m := &checkerMachine{st: st}
			mTy, mErr := m.run(checkerStep{mode: modeSynth, term: build()})

			if rErr != nil || mErr != nil {
				t.Fatalf("n=%d must typecheck\n  recursive: %v\n  machine:   %v", n, rErr, mErr)
			}
			if !tyEq(rTy, mTy) {
				t.Fatalf("n=%d: type differs", n)
			}
			// EXACTLY one inference entry per node, on BOTH. n+1 counts the
			// terminating Nil, whose type arguments are explicit — so the
			// expected value is derived from the shape rather than guessed.
			if rc.inferEntries != n || m.inferEntries != n {
				t.Fatalf("n=%d: inference entries recursive=%d machine=%d, want %d each — "+
					"a count above n means the TyArgs memo stopped suppressing re-entry",
					n, rc.inferEntries, m.inferEntries, n)
			}
		}
	})
}
