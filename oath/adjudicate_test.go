package main

import (
	"strings"
	"testing"
)

// The claim under test (#130):
//
//	a surviving mutant is `proof-refuted` exactly when some property recorded
//	PROVEN for the original is REFUTED against the mutant body.
//
// Both directions are asserted, because a checker that only looks for
// refutations is satisfied by `return "proof-refuted"`. The control cases below
// are the half that makes the witness discriminate.
//
// The fixture is chosen so the generator provably cannot reach the distinguishing
// input: `genValue` draws Int from [-20,20], so a guard at 48 is unsatisfiable in
// every generated case. That is the real shape from the corpus (`hex-nibble`
// scores 11/53 while PROVEN), reproduced small enough to assert exactly.
const adjudFixture = `(defn over48 [] [(c Int)] Int
  (if (<= 48 c) 1 0)
  (prop big-is-one [(c Int)] (if (<= 48 c) (== (over48 c) 1) true))
  (prop small-is-zero [] (== (over48 0) 0)))`

func adjudSetup(t *testing.T) (*Store, string, *Meta, *Def) {
	t.Helper()
	requireZ3(t)
	st := newStore(t)
	put(t, st, adjudFixture)
	if _, err := apiProve(st, "over48"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	h, ok := st.Resolve("over48")
	if !ok {
		t.Fatal("over48 did not resolve")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	// SETUP ASSERTION, not decoration: every case below is vacuous if the
	// property never proved, and a silently-unproven fixture would make the
	// whole file pass while measuring nothing.
	if len(m.ProvenProps) == 0 {
		t.Fatalf("fixture did not prove: ProvenProps=%v", m.ProvenProps)
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("GetDef: %v", err)
	}
	return st, h, m, d
}

// mutantNamed finds a generated mutant by its description.
func mutantNamed(t *testing.T, st *Store, d *Def, desc string) mutantDef {
	t.Helper()
	for _, mu := range genMutants(st, d) {
		if mu.desc == desc {
			st.CacheDef(mu.hash, mu.def)
			return mu
		}
	}
	var have []string
	for _, mu := range genMutants(st, d) {
		have = append(have, mu.desc)
	}
	t.Fatalf("no mutant %q; generated: %v", desc, have)
	return mutantDef{}
}

// The positive case: a mutant that generated testing cannot distinguish, and
// that the proof refutes outright.
func TestAdjudicateRefutesSurvivorTestingMissed(t *testing.T) {
	st, _, m, d := adjudSetup(t)
	mu := mutantNamed(t, st, d, "literal 48 → 49")

	// First establish it really IS a survivor. Without this the test could pass
	// on a mutant the generator already kills, which would witness nothing.
	if killer := firstKiller(st, m, mu); killer != "" {
		t.Fatalf("mutant was killed by %q — not a survivor, so it cannot witness this claim", killer)
	}

	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "proof-refuted" {
		t.Fatalf("kind=%q reason=%q, want proof-refuted", v.kind, v.reason)
	}
	if v.prop != "big-is-one" {
		t.Fatalf("prop=%q, want big-is-one", v.prop)
	}
}

// CONTROL 1 — the instrument must NOT refute a mutant the proven properties do
// not separate. `(<= 48 c)` → `(<= 47 c)` changes behaviour at c = 47 only, and
// no proven property here observes it.
func TestAdjudicateLeavesUnobservedMutantUnadjudicated(t *testing.T) {
	st, _, m, d := adjudSetup(t)
	mu := mutantNamed(t, st, d, "literal 48 → 47")
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "unadjudicated" {
		t.Fatalf("kind=%q, want unadjudicated — a mutant no proven property separates must never be reported as refuted", v.kind)
	}
	if !strings.Contains(v.reason, "still holds") {
		t.Fatalf("reason=%q, want the honest 'every proven property still holds' wording", v.reason)
	}
}

// CONTROL 2 — with nothing proven there is nothing to appeal to, and the verdict
// must say so rather than defaulting to a disposition it did not establish. This
// is the direction the instrument would lie in if it lied.
func TestAdjudicateWithoutProofsIsHonest(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, adjudFixture) // deliberately NOT proved
	h, _ := st.Resolve("over48")
	m, _ := st.GetMeta(h)
	d, _ := st.GetDef(h)
	if len(m.ProvenProps) != 0 {
		t.Fatalf("fixture was proved despite not calling apiProve: %v", m.ProvenProps)
	}
	mu := mutantNamed(t, st, d, "literal 48 → 49")
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "unadjudicated" || !strings.Contains(v.reason, "no proven property") {
		t.Fatalf("kind=%q reason=%q, want unadjudicated/no proven property", v.kind, v.reason)
	}
}

// CONTROL 3 — an attempt that never reached a verdict must NOT be reported as
// "every proven property still holds". This is the exact direction the
// instrument would lie in, and it is not reachable by ordinary means: a
// wall-clock safety cap needs a race against the solver. OATH_PROVE_FORCE_ABORT
// is the repo's existing fault injection for that path (it can only ever
// SUPPRESS a verdict, never fabricate one).
//
// Found by review: the first version of adjudicateSurvivor matched "unknown" by
// name, so "invalidated" fell through and a solver that gave up was recorded as
// a clean result. The switch now fails closed on anything but an explicit
// "proven", and this test is what holds it there.
func TestAdjudicateDoesNotTreatAbortedProofAsClean(t *testing.T) {
	st, _, m, d := adjudSetup(t)
	// The mutant no proven property separates — so WITHOUT the abort this
	// returns "every proven property still holds". That baseline is asserted
	// first: otherwise a broken fixture would make the abort case pass for the
	// wrong reason.
	mu := mutantNamed(t, st, d, "literal 48 → 47")
	if v := adjudicateSurvivor(st, m, mu.hash, mu.def); !strings.Contains(v.reason, "still holds") {
		t.Fatalf("baseline reason=%q, want 'still holds' — the abort case below would witness nothing", v.reason)
	}

	t.Setenv("OATH_PROVE_FORCE_ABORT", "big-is-one")
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "unadjudicated" {
		t.Fatalf("kind=%q, want unadjudicated", v.kind)
	}
	if strings.Contains(v.reason, "still holds") {
		t.Fatalf("an ABORTED proof attempt was reported as a clean result: %q", v.reason)
	}
	if !strings.Contains(v.reason, "did not reach a verdict") {
		t.Fatalf("reason=%q, want the did-not-reach-a-verdict wording", v.reason)
	}
}

// A mutant that DESTROYS termination must never be refuted. prove.go asserts a
// defining equation only for a function classified total, so a non-total mutant
// stays uninterpreted and z3 can "refute" any property using arbitrary values
// for it — the same fabrication as the missing-metadata bug, reached by a
// different route.
//
// The fixture is a real corpus shape — `show-nat`'s measure recursion on
// `(/ n 10)`. Termination there depends on the ARITHMETIC rather than on
// structural descent, so the catalogue's `/ → *` mutant recurses on `(* n 10)`
// and diverges. On the committed corpus `show-nat` has exactly two such mutants
// (`/ → *` and `literal 10 → 0`), both of which survive generation.
//
// A structural fixture does NOT exercise this: the first attempt used
// `take-n (- n 1) t`, whose recursion descends on the string, so `- → +` stays
// total. The setup assertion below is what caught that.
func TestAdjudicateRefusesNonTotalMutants(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn digits10 [] [(n Int)] Int
		(if (<= n 0) 0 (+ 1 (digits10 (/ n 10))))
		(prop zero-is-zero [] (== (digits10 0) 0)))`)
	if _, err := apiProve(st, "digits10"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	h, _ := st.Resolve("digits10")
	m, _ := st.GetMeta(h)
	d, _ := st.GetDef(h)
	if len(m.ProvenProps) == 0 {
		t.Fatal("fixture did not prove — nothing to adjudicate against")
	}

	mu := mutantNamed(t, st, d, "/ → *")
	// SETUP ASSERTION: the whole point is that this mutant is NOT total. If the
	// classifier ever calls it total, this test silently stops testing anything.
	if term := terminationOf(st, mu.def, mu.hash); isTotal(term) {
		t.Fatalf("fixture mutant classified %q (total) — it no longer exercises the non-total path", term)
	}
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind == "proof-refuted" {
		t.Fatalf("a NON-TOTAL mutant was proof-refuted by %q — the refutation is against an uninterpreted function, not this body", v.prop)
	}
	if !strings.Contains(v.reason, "not provably total") {
		t.Fatalf("reason=%q, want the non-total explanation", v.reason)
	}
	// And `zero-is-zero` genuinely HOLDS on this mutant — at n = 0 the guard
	// returns 0 before recursing at all — so "refuted" would have been false as
	// well as unfounded.
}

// THE SOUNDNESS CONTROL, and it is the one that matters most: adjudicating the
// ORIGINAL body as though it were a mutant must NEVER return proof-refuted. The
// original satisfies its proven properties by construction — that is what
// "proven" means — so a refutation there is the instrument fabricating evidence.
//
// This is not hypothetical. Before the metadata fix, a recursive mutant reached
// the prover as an UNINTERPRETED function: with no defining equation asserted,
// z3 may choose any values for it, so almost any property is trivially
// "refutable". `str-take` reported 6 proof-refuted survivors that way, and every
// one was false. The bug looked like extra power and was the opposite.
//
// A general control beats a per-fixture one here: it is expressible for every
// definition in the corpus without knowing anything about its body.
func TestAdjudicateNeverRefutesTheOriginal(t *testing.T) {
	requireZ3(t)
	for _, src := range []string{
		adjudFixture,
		`(defn dbl [] [(n Nat)] Nat
			(match n ((Z) (Z)) ((S m) (S (S (dbl m)))))
			(prop step [(n Nat)] (== (dbl (S n)) (S (S (dbl n)))))
			(prop z-is-z [] (== (dbl (Z)) (Z))))`,
		`(defn len2 [] [(xs NatList)] Nat
			(match xs ((LNil) (Z)) ((LCons y ys) (S (len2 ys))))
			(prop cons-grows [(y Nat) (ys NatList)] (== (len2 (LCons y ys)) (S (len2 ys)))))`,
	} {
		st := newStore(t)
		put(t, st, `(data Nat [] (Z) (S Nat))`)
		put(t, st, `(data NatList [] (LNil) (LCons Nat NatList))`)
		put(t, st, src)
		reps := put(t, st, src)
		name := reps[len(reps)-1].Name
		if _, err := apiProve(st, name); err != nil {
			t.Fatalf("%s: apiProve: %v", name, err)
		}
		h, _ := st.Resolve(name)
		m, _ := st.GetMeta(h)
		d, _ := st.GetDef(h)
		if len(m.ProvenProps) == 0 {
			t.Fatalf("%s did not prove — the control below would be vacuous", name)
		}
		if v := adjudicateSurvivor(st, m, h, d); v.kind == "proof-refuted" {
			t.Fatalf("%s: the ORIGINAL body was reported proof-refuted by its own proven property %q — the adjudicator is fabricating refutations", name, v.prop)
		}
	}
}

// RECURSION. Every case above uses a nonrecursive fixture, and that is exactly
// why the first implementation shipped a defect: the prover asserts a function's
// defining equation only for one classified TOTAL, reads that classification via
// GetMeta, and GetMeta fails for a cached mutant. So every recursive mutant was
// modeled as an uninterpreted function and came back inconclusive — on most of
// the corpus, silently, while the nonrecursive tests all passed.
//
// The lesson is the repo's own: a witness must derive its universe from the
// CLAIM ("a surviving mutant", any shape) rather than from the fixture that
// happened to be convenient.
func TestAdjudicateHandlesRecursiveMutants(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(data Nat [] (Z) (S Nat))`)
	// double is structurally recursive, and `two-is-four` pins a concrete value
	// the generator does reach — so the ORIGINAL proves, giving adjudication
	// something to appeal to.
	put(t, st, `(defn double [] [(n Nat)] Nat
		(match n ((Z) (Z)) ((S m) (S (S (double m)))))
		(prop z-is-z [] (== (double (Z)) (Z)))
		(prop one-is-two [] (== (double (S (Z))) (S (S (Z))))))`)
	if _, err := apiProve(st, "double"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	h, _ := st.Resolve("double")
	m, _ := st.GetMeta(h)
	d, _ := st.GetDef(h)
	if len(m.ProvenProps) == 0 {
		t.Skip("fixture did not prove; nothing to adjudicate against")
	}
	if m.Termination == "" || !isTotal(m.Termination) {
		t.Fatalf("fixture termination=%q, want a total classification", m.Termination)
	}

	// Every mutant of a recursive body must reach the prover with a termination
	// classification. Without one it is modeled as an UNINTERPRETED function,
	// which does not weaken the result — it corrupts it, because an unconstrained
	// function makes almost any property refutable.
	var checked int
	for _, mu := range genMutants(st, d) {
		st.CacheDef(mu.hash, mu.def)
		mm := mutantMeta(st, m, mu.def, mu.hash)
		if mm.Termination == "" {
			t.Fatalf("%s: mutant carries no termination classification — it would reach the prover uninterpreted", mu.desc)
		}
		// And it must be the MUTANT's classification, not the original's. A
		// mutation that destroys structural descent must not inherit "structural"
		// and get its defining equation asserted.
		if want := terminationOf(st, mu.def, mu.hash); mm.Termination != want {
			t.Fatalf("%s: termination %q, want the mutant's own %q", mu.desc, mm.Termination, want)
		}
		if len(mm.ProvenProps) != 0 {
			t.Fatalf("%s: mutant inherited proven properties %v — the original's self-lemmas would axiomatize the body under test", mu.desc, mm.ProvenProps)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no mutants generated — this test asserted nothing")
	}
}

// The score is the thing that must NOT move. Adjudication adds a reading; if it
// ever folded into killed/total it would change `analyses/*.json`, which
// oathrs/conformance.sh requires byte-identical across kernels.
func TestAdjudicationDoesNotChangeTheScore(t *testing.T) {
	st, h, _, _ := adjudSetup(t)
	plain, err := apiMutateHashOpt(st, h, false)
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	mPlain, _ := st.GetMeta(h)
	killedPlain, totalPlain := mPlain.MutantsKilled, mPlain.MutantsTotal

	adj, err := apiMutateHashOpt(st, h, true)
	if err != nil {
		t.Fatalf("mutate --prove: %v", err)
	}
	mAdj, _ := st.GetMeta(h)
	if mAdj.MutantsKilled != killedPlain || mAdj.MutantsTotal != totalPlain {
		t.Fatalf("score moved: %d/%d -> %d/%d", killedPlain, totalPlain, mAdj.MutantsKilled, mAdj.MutantsTotal)
	}
	scoreLine := func(s string) string {
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "generated mutation score:") {
				return l
			}
		}
		return ""
	}
	if scoreLine(plain) == "" {
		t.Fatalf("no score line in plain output:\n%s", plain)
	}
	if scoreLine(plain) != scoreLine(adj) {
		t.Fatalf("score line differs:\n plain: %q\n --prove: %q", scoreLine(plain), scoreLine(adj))
	}
	// And the adjudication must actually have run — otherwise this test passes
	// trivially by comparing two identical unadjudicated reports.
	if !strings.Contains(adj, "proof-refuted") {
		t.Fatalf("--prove produced no proof-refuted survivor, so the equality above witnesses nothing:\n%s", adj)
	}
	if strings.Contains(plain, "proof-refuted") {
		t.Fatalf("the default path adjudicated; it must stay prover-free:\n%s", plain)
	}
}

// firstKiller reproduces the scoring loop's kill test for one mutant.
func firstKiller(st *Store, m *Meta, mu mutantDef) string {
	base := mutantSeed(mu.hash)
	for pi := range mu.def.Props {
		rep := runProp(st, mu.hash, &mu.def.Props[pi], metaPropName(m, pi), base, pi, mutantCases, mutantFuel)
		if rep.Failed || rep.Err != "" {
			return rep.Name
		}
	}
	return ""
}
