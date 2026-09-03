package main

import (
	"fmt"
	"strings"
	"testing"
)

// Deterministic instantiation (prove_instantiate.go). These tests witness the
// claim the strategy makes and the gate that keeps it sound; the controls are
// what make the positive results evidence rather than assertions.
//
// WHAT MUTATION MAKES THEM FAIL. Disabling the instantiated attempt returns both
// positive goals to `unproven` — verified by reverting, at OATH_PROVE_RLIMIT
// 15000000, in a throwaway store: `swap` and `opt` both went from PROVEN to
// "no direct proof; induction did not discharge" and back. An instance schema
// that asserted an equation off a function's defined points would prove the
// wrong-transform controls below.

// The composed-recursion slice: an expression tree, a recursive evaluator, and
// two recursive transformations over it. `ev (swap e) = ev e` holds because +
// and * commute; `ev (opt e) = ev e` holds because constant folding preserves
// value. Both are STUCK in their quantified form — the Mul constructor subgoal
// exhausts a 15,000,000 rlimit and returns `unknown` — because z3's e-matching
// on the two quantified defining equations diverges.
const composedRecursionSrc = `
(data Expr []
  (Lit Int)
  (Add Expr Expr)
  (Mul Expr Expr))

(defn ev [] [(e Expr)] Int
  (match e
    ((Lit n) n)
    ((Add a b) (+ (ev a) (ev b)))
    ((Mul a b) (* (ev a) (ev b)))))

(defn swap [] [(e Expr)] Expr
  (match e
    ((Lit n) (Lit n))
    ((Add a b) (Add (swap b) (swap a)))
    ((Mul a b) (Mul (swap b) (swap a))))
  (prop preserves [(e Expr)] (== (ev (swap e)) (ev e))))

(defn add-s [] [(a Expr) (b Expr)] Expr
  (match a
    ((Lit m) (match b ((Lit n) (Lit (+ m n))) ((Add x y) (Add a b)) ((Mul x y) (Add a b))))
    ((Add x y) (Add a b))
    ((Mul x y) (Add a b))))

(defn mul-s [] [(a Expr) (b Expr)] Expr
  (match a
    ((Lit m) (match b ((Lit n) (Lit (* m n))) ((Add x y) (Mul a b)) ((Mul x y) (Mul a b))))
    ((Add x y) (Mul a b))
    ((Mul x y) (Mul a b))))

(defn opt [] [(e Expr)] Expr
  (match e
    ((Lit n) (Lit n))
    ((Add a b) (add-s (opt a) (opt b)))
    ((Mul a b) (mul-s (opt a) (opt b))))
  (prop preserves [(e Expr)] (== (ev (opt e)) (ev e))))
`

// TestInstantiationProvesComposedRecursion is the positive witness: a goal the
// kernel could not prove before this strategy existed.
//
// It is `swap` rather than `opt` because swap is the STUCK-UNPROVEN direction of
// §7.2's non-monotonicity — a true theorem never recorded — and because its
// correctness rests on commutativity rather than on any smart-constructor
// soundness lemma, so nothing but the defining equations is in play.
func TestInstantiationProvesComposedRecursion(t *testing.T) {
	requireZ3(t)
	// The preview is opt-in in Phase 1 (default OFF); the witness turns it on.
	// With it off, both goals return to `unproven` — which is exactly the
	// revert-mutation this test is built to catch, now expressible as the flag.
	t.Setenv("OATH_PROVE_INSTANTIATE", "1")
	st := newStore(t)
	put(t, st, composedRecursionSrc)

	// BOTH measured goals, because they are stuck for structurally different
	// reasons and one is not evidence for the other. `swap` is correct by
	// COMMUTATIVITY and rebuilds the same constructor, so its transformed RHS is
	// a constructor application the schema can fold against; `opt` is correct by
	// FOLDING through smart constructors that inline into a nested case split, so
	// its transformed RHS is not a constructor at all and the observer has to be
	// instantiated at the whole `ite`. A schema that only handled the first shape
	// would pass a swap-only test.
	for _, name := range []string{"swap", "opt"} {
		out, err := apiProve(st, name)
		if err != nil {
			t.Fatalf("prove %s: %v", name, err)
		}
		if !strings.Contains(out, "PROVEN") {
			t.Errorf("%s.preserves is not proven — deterministic instantiation did "+
				"not discharge the composed-recursion induction:\n%s", name, out)
		}
	}
}

// TestInstantiationDoesNotFabricate is the non-fabrication control, and the
// positive test above is worth nothing without it: an instantiation procedure
// that returned `unsat` for a FALSE transform would be manufacturing proofs.
//
// `swapb` mirrors swap's shape exactly — same datatype, same evaluator, same
// recursive rebuild, same property — but its Add case DROPS one operand and
// duplicates the other, computing 2*ev(a). The kernel's own generator falsifies
// it; the prover must not then prove it.
func TestInstantiationDoesNotFabricate(t *testing.T) {
	requireZ3(t)
	// The experiment's own budget. A lower rlimit can only turn a terminal
	// verdict into NO verdict — it can never invert one and can never manufacture
	// an `unsat` — so the control keeps its force: a fabricating schema returns
	// `unsat` on these subgoals in a few thousand rlimit, three orders of
	// magnitude inside this bound. What it buys is the ~255 seconds the full
	// budget spends failing the quantified induction, which every `go test ./...`
	// would otherwise pay.
	t.Setenv("OATH_PROVE_RLIMIT", "15000000")
	// The control only has force while the strategy actually runs: with the
	// preview off, swapb is trivially unproven and a fabricating schema would
	// slip through. Turn it on.
	t.Setenv("OATH_PROVE_INSTANTIATE", "1")
	st := newStore(t)
	put(t, st, `
(data Expr []
  (Lit Int)
  (Add Expr Expr)
  (Mul Expr Expr))

(defn ev [] [(e Expr)] Int
  (match e
    ((Lit n) n)
    ((Add a b) (+ (ev a) (ev b)))
    ((Mul a b) (* (ev a) (ev b)))))

(defn swapb [] [(e Expr)] Expr
  (match e
    ((Lit n) (Lit n))
    ((Add a b) (Add (swapb a) (swapb a)))
    ((Mul a b) (Mul (swapb b) (swapb a))))
  (prop preserves [(e Expr)] (== (ev (swapb e)) (ev e))))
`)

	out, err := apiProve(st, "swapb")
	if err != nil {
		t.Fatalf("prove swapb: %v", err)
	}
	if strings.Contains(out, "PROVEN") {
		t.Fatalf("a FALSE transformation was proven — the instance schema is "+
			"asserting equations that are not theorems of the definitions:\n%s", out)
	}
}

// instancesFor builds the ground instances deterministic instantiation would
// emit for one constructor case, through the same code the prover runs. It is
// the only way to witness the totality gate: the instantiated attempt is
// deliberately excluded from enumeration, so scriptAttempts cannot show it, and
// a verdict-level assertion would pass just as happily if the goal failed for an
// unrelated reason.
func instancesFor(t *testing.T, st *Store, name string, propIdx, ctor int) ([]string, map[int]bool) {
	t.Helper()
	h, ok := st.Resolve(name)
	if !ok {
		t.Fatalf("%s was not stored", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("def: %v", err)
	}
	if propIdx >= len(d.Props) {
		t.Fatalf("%s has no property %d", name, propIdx)
	}
	p := &d.Props[propIdx]
	c := newSmtCtx(st, d, h)
	sort, err := c.sortOf(&p.Binders[0])
	if err != nil {
		t.Fatalf("sortOf: %v", err)
	}
	// Translating the goal declares the datatype and every axiomatized callee,
	// exactly as proveOneInner does before it reaches induction.
	if _, err := c.formulaWith(d, h, p, map[int]string{0: "b0"}); err != nil {
		t.Fatalf("formulaWith: %v", err)
	}
	dt := c.dtBySort[sort]
	if dt == nil {
		t.Fatalf("binder sort %s is not a datatype", sort)
	}
	var fieldConsts []string
	for fi := range dt.fields[ctor] {
		fieldConsts = append(fieldConsts, fmt.Sprintf("f%d", fi))
	}
	return c.instantiatedSubgoal(d, h, p, 0, dt, ctor, fieldConsts)
}

// TestInstantiationGatedOnTotality is the soundness gate, witnessed rather than
// argued. A ground instance of `f(x) = body` is a theorem only where f is
// defined, so a function whose termination is unproven must contribute NO
// instance — and it contributes none by construction, because the substitution-
// ready metadata is recorded on the same branch of ensureFn that decides whether
// to emit a quantified axiom at all.
//
// The control is a POSITIVE/NEGATIVE pair over the same shape: `idt` composed
// with a TOTAL evaluator yields instances, and the identical composition with a
// non-total one yields none. Without the positive half this test would pass on a
// build that never instantiated anything.
func TestInstantiationGatedOnTotality(t *testing.T) {
	st := newStore(t)
	put(t, st, `
(data Expr []
  (Lit Int)
  (Add Expr Expr)
  (Mul Expr Expr))

(defn spin [] [(e Expr)] Int
  (match e
    ((Lit n) (spin (Lit n)))
    ((Add a b) (+ (spin a) (spin b)))
    ((Mul a b) (* (spin a) (spin b)))))

(defn ev [] [(e Expr)] Int
  (match e
    ((Lit n) n)
    ((Add a b) (+ (ev a) (ev b)))
    ((Mul a b) (* (ev a) (ev b)))))

(defn idt [] [(e Expr)] Expr
  (match e
    ((Lit n) (Lit n))
    ((Add a b) (Add (idt a) (idt b)))
    ((Mul a b) (Mul (idt a) (idt b))))
  (prop spins [(e Expr)] (== (spin (idt e)) (spin e)))
  (prop keeps [(e Expr)] (== (ev (idt e)) (ev e))))
`)
	sh, ok := st.Resolve("spin")
	if !ok {
		t.Fatal("spin was not stored")
	}
	sm, err := st.GetMeta(sh)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if isTotal(sm.Termination) {
		t.Fatalf("spin is classified %q — this control needs a function whose "+
			"totality is UNPROVEN, so it would be testing nothing", sm.Termination)
	}

	// NEGATIVE: property 0 composes the non-total `spin` with `idt`.
	for _, ctor := range []int{1, 2} {
		inst, _ := instancesFor(t, st, "idt", 0, ctor)
		for _, a := range inst {
			if strings.Contains(a, "fn_spin") {
				t.Errorf("ctor %d: a ground instance mentioning a NON-TOTAL "+
					"function was emitted:\n  %s", ctor, a)
			}
		}
	}

	// POSITIVE control: the same composition over the TOTAL `ev` must produce
	// instances, or the negative half above proves only that nothing works.
	inst, omit := instancesFor(t, st, "idt", 1, 1)
	if len(inst) == 0 {
		t.Fatal("the TOTAL composition produced no instances — the negative half " +
			"of this control is vacuous")
	}
	if len(omit) == 0 {
		t.Error("instances were emitted but no quantified axiom is omitted — the " +
			"diverging axioms would still be present")
	}
}

// TestInstantiationLeavesEnumerationUnchanged pins the Phase-1 decision that the
// preview attempt is NOT recorded by scriptAttempts.
//
// prove/attempts.txt pins the candidate scripts of the strategies SPEC §7.2
// names, and §7.2 does not yet name this one; emitting it into the walk would
// rewrite that fixture ahead of the normative text licensing the rows. So the
// enumeration vocabulary must be exactly what it was, and theStrategies in
// prove_attempts_test.go must not need a new member.
func TestInstantiationLeavesEnumerationUnchanged(t *testing.T) {
	st := newStore(t)
	put(t, st, composedRecursionSrc)
	gh, _ := st.Resolve("swap")
	attempts, err := scriptAttempts(st, gh, 0)
	if err != nil {
		t.Fatalf("scriptAttempts: %v", err)
	}
	if len(attempts) == 0 {
		t.Fatal("no scripts emitted — nothing was measured")
	}
	sawInduction := false
	for _, a := range attempts {
		if a.strategy == "induction-instantiated" {
			t.Errorf("the instantiation preview was recorded by enumeration (%s); "+
				"prove/attempts.txt would gain rows for a strategy §7.2 does not "+
				"yet name", a.detail)
		}
		if a.strategy == "induction" {
			sawInduction = true
		}
	}
	if !sawInduction {
		t.Error("enumeration emitted no `induction` script for a goal that reaches " +
			"induction — the preview is displacing the pinned quantified attempt " +
			"rather than preceding it")
	}
}

// TestInstantiationPreservesQuantifiedScriptBytes witnesses the fallback claim:
// the quantified induction attempt is byte-identical to what it was before this
// strategy existed, so no goal that proves today can regress and no pinned
// script hash moves.
//
// The mutation this catches is the tempting one — giving the instantiated
// attempt its own axiom stream by MUTATING c.axioms rather than by omitting
// entries at emission time. That would leave every later script short an axiom,
// silently, and every fixture would be regenerated from the same bug.
func TestInstantiationPreservesQuantifiedScriptBytes(t *testing.T) {
	st := newStore(t)
	put(t, st, composedRecursionSrc)
	gh, _ := st.Resolve("swap")

	d, err := st.GetDef(gh)
	if err != nil {
		t.Fatalf("def: %v", err)
	}
	m, err := st.GetMeta(gh)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}

	// The emitted induction scripts must carry BOTH defining equations: the
	// quantified attempt is the fallback, and it is only a fallback if it is
	// unchanged.
	attempts, err := scriptAttempts(st, gh, 0)
	if err != nil {
		t.Fatalf("scriptAttempts: %v", err)
	}
	// Building the instances must not APPEND to the declaration or axiom
	// streams: both are shared with every later script, so a single new
	// declaration would silently change the bytes of the fallback this strategy
	// promises to leave alone. It cannot happen — the declarations a term
	// induces are a function of the TERM, not of the environment it is
	// translated in, and every instantiated body was already translated in full
	// by ensureFn — but "cannot happen" is the claim, so it is measured.
	c := newSmtCtx(st, d, gh)
	p := &d.Props[0]
	if _, err := c.formulaWith(d, gh, p, map[int]string{0: "b0"}); err != nil {
		t.Fatalf("formulaWith: %v", err)
	}
	dt := c.dtBySort["Expr"]
	if dt == nil {
		t.Fatal("Expr was not declared")
	}
	decls, axioms := len(c.decls), len(c.axioms)
	inst, _ := c.instantiatedSubgoal(d, gh, p, 0, dt, 1, []string{"f0", "f1"})
	if len(inst) == 0 {
		t.Fatal("no instances built — the growth check below measures nothing")
	}
	if len(c.decls) != decls || len(c.axioms) != axioms {
		t.Errorf("building instances grew the shared streams (decls %d→%d, "+
			"axioms %d→%d) — every later script's bytes have moved",
			decls, len(c.decls), axioms, len(c.axioms))
	}

	n := 0
	for _, a := range attempts {
		if a.strategy != "induction" {
			continue
		}
		n++
		for _, fn := range []string{"fn_ev", "fn_swap"} {
			if !strings.Contains(a.text, "(forall ((p0 Expr)) (! (= ("+fn+" p0)") {
				t.Errorf("the quantified defining equation for %s is missing from "+
					"the %s script (%s) — the fallback is not the unchanged "+
					"attempt it claims to be", fn, a.strategy, a.detail)
			}
		}
	}
	if n == 0 {
		t.Fatal("no induction scripts emitted — nothing was measured")
	}
	_ = m
}
