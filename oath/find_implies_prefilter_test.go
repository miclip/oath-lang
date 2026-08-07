package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// Tests for the pre-solver concrete pass in apiFindImplies (#80).
//
// The query is docs/tutorial/discovery.md §3's flipped-commutativity spec, held
// here as its own literal rather than shared with the measurement harness: a
// product test must not break because a measurement file was removed, and the
// two are asserting different things.
const prefilterFlippedQuery = `(defn wanted [] [(a Rat) (b Rat)] Rat (+ a b)
  (prop flipped [(a Rat) (b Rat)] (== (wanted b a) (wanted a b))))`

// prefilterExpectedHits is the set docs/tutorial/discovery.md advertises. It is
// the SEMANTIC content of the query's answer, and the pass must not change it.
var prefilterExpectedHits = []string{"apply2", "max2", "rat-add", "rat-mul"}

// TestFindImpliesPreservesTutorialHits is the SEMANTIC half of the #80 witness,
// and it is the only test here that runs the real corpus.
//
// It deliberately asserts NOTHING about solver launches. The latency claim is
// witnessed by TestConcretePassKeepsRefutedCandidateOutOfTheSolver on two
// synthetic definitions, where the mutation is provable in half a second; a
// launch-count assertion over the corpus would witness the same claim while
// making its own falsification a six-minute job, and a witness nobody can afford
// to run under mutation is a hypothesis.
//
// What this one witnesses instead cannot be reproduced synthetically: that the
// answer docs/tutorial/discovery.md advertises, over the corpus that document
// describes, is unchanged by the pass.
//
// WHAT MUTATION MAKES IT FAIL: widening the guard from `== ceFalsified` to
// `!= ceFalsified` — a prefilter that skips everything it did not refute.
// Verified by reverting: all four hits vanish and the fallback line fires.
// That is the right killing mutation for this assertion, because losing answers
// is the failure mode a latency change is most likely to introduce, and the
// bound it breaks is a semantic one rather than a timing one.
func TestFindImpliesPreservesTutorialHits(t *testing.T) {
	requireZ3(t)
	st := openCorpusCopy(t)

	out, err := apiFindImplies(st, prefilterFlippedQuery)
	if err != nil {
		t.Fatalf("find --implies: %v", err)
	}
	for _, want := range prefilterExpectedHits {
		if !strings.Contains(out, want) {
			t.Errorf("hit %q disappeared from find --implies output:\n%s", want, out)
		}
	}
	// CONTROL: the fallback must NOT fire. Without it a run that returned an
	// empty answer for some unrelated reason could still satisfy a `Contains`
	// loop that had itself stopped matching anything.
	if strings.Contains(out, "no definition provably satisfies this") {
		t.Errorf("the query returned no hits at all:\n%s", out)
	}
}

// The MINIMAL synthetic store: exactly two candidates, both admitted on the
// exact signature `[Int Int] -> Int`, differing only in whether the query's law
// holds of them.
//
//   - sum-int  SATISFIES flipped commutativity. It is the control: the solver
//     must still run, or every bound below is vacuous.
//   - diff-int REFUTES it, and refutes it at the FIRST sampled environment that
//     has a != b, so the concrete pass costs microseconds here.
//
// Names are local to a t.TempDir() store and never reach the canonical corpus,
// but they are still written to be legible to a stranger.
const prefilterSyntheticSum = `(defn sum-int [] [(a Int) (b Int)] Int (+ a b))`
const prefilterSyntheticDiff = `(defn diff-int [] [(a Int) (b Int)] Int (- a b))`

const prefilterSyntheticQuery = `(defn wanted [] [(a Int) (b Int)] Int (+ a b)
  (prop flipped [(a Int) (b Int)] (== (wanted b a) (wanted a b))))`

// TestConcretePassKeepsRefutedCandidateOutOfTheSolver is the LANDABLE regression
// witness for #80's concrete pass, and it is deliberately not a corpus test.
//
// THE CLAIM: adding a candidate that the query's law is FALSE of costs ZERO
// solver launches. That is the whole of what the pass does, and it needs two
// definitions to state, not a hundred.
//
// WHY THE ASSERTION IS A DIFFERENCE AND NOT A BOUND: how many z3 subprocesses
// one surviving goal spends is a property of the prover's strategy ladder, which
// is free to change. A hard bound would either be loose enough to prove nothing
// or brittle enough to fail on an unrelated prover improvement. The count that
// belongs to the REFUTED candidate is exactly `both - onlyProven`, and the claim
// is that it is zero — which stays true however the ladder evolves.
//
// WHAT MUTATION MAKES IT FAIL: removing the `concreteCounterexample(...) ==
// ceFalsified { continue }` guard from apiFindImplies. Verified by reverting;
// the exact mutation and the observed counts are recorded in the test log.
func TestConcretePassKeepsRefutedCandidateOutOfTheSolver(t *testing.T) {
	requireZ3(t)

	// VACUITY GUARD, asserted before anything is counted: if diff-int were not
	// admitted — wrong signature, failing checkDef, a query the pass cannot
	// evaluate — the difference below would be zero for a reason that has
	// nothing to do with the feature under test.
	probe := newStore(t)
	put(t, probe, prefilterSyntheticSum)
	put(t, probe, prefilterSyntheticDiff)
	_, admitted := findImpliesAdmitForTest(t, probe, prefilterSyntheticQuery)
	for _, want := range []string{"sum-int", "diff-int"} {
		if _, ok := admitted[want]; !ok {
			t.Fatalf("%s was not admitted, so this test cannot witness anything", want)
		}
	}
	if o := concreteCounterexample(probe, admitted["diff-int"].hash, ptrProp(admitted["diff-int"].prop)); o != ceFalsified {
		t.Fatalf("diff-int is not concretely refuted (outcome=%d) — it is not the case this test needs", o)
	}
	if o := concreteCounterexample(probe, admitted["sum-int"].hash, ptrProp(admitted["sum-int"].prop)); o == ceFalsified {
		t.Fatalf("UNSOUND: the concrete pass refuted sum-int, which satisfies the law")
	}

	// basePATH is captured ONCE, before any shim exists, and restored ahead of
	// each arm. Without it the arms are not comparable: installZ3Counter PREPENDS
	// its directory, so the second arm would resolve the "real" z3 to the first
	// arm's shim and chain through it. The counts survive that, but the two arms
	// would then run under different amounts of process indirection while being
	// compared to each other — and an instrument whose arms differ in anything
	// but the variable under test is the failure this repo keeps cataloguing.
	basePATH := os.Getenv("PATH")

	// ARM 1 — the proven candidate alone. This is the solver cost the query is
	// entitled to spend.
	onlyProven, outProven := prefilterSyntheticArm(t, basePATH, prefilterSyntheticSum)
	if onlyProven == 0 {
		t.Fatal("no solver launch at all for sum-int — the query never reaches the prover, so the difference below is vacuous")
	}
	if !strings.Contains(outProven, "sum-int") {
		t.Errorf("sum-int is not reported as satisfying the law:\n%s", outProven)
	}

	// ARM 2 — the same query with the refuted candidate ALSO in the store.
	both, outBoth := prefilterSyntheticArm(t, basePATH, prefilterSyntheticSum, prefilterSyntheticDiff)
	t.Logf("z3 subprocesses: proven-only=%d proven+refuted=%d (delta attributable to diff-int: %d)",
		onlyProven, both, both-onlyProven)

	if both != onlyProven {
		t.Errorf("the refuted candidate reached the solver: %d launches with it vs %d without (delta %d, want 0)",
			both, onlyProven, both-onlyProven)
	}
	// The semantic answer must be unchanged by the pass in BOTH directions: the
	// hit survives, and the refuted candidate is not reported as a hit.
	if !strings.Contains(outBoth, "sum-int") {
		t.Errorf("sum-int disappeared once diff-int was in the store:\n%s", outBoth)
	}
	if strings.Contains(outBoth, "diff-int") {
		t.Errorf("diff-int is reported as satisfying a law it falsifies:\n%s", outBoth)
	}
}

// prefilterSyntheticArm builds a fresh store holding exactly the given
// definitions, runs the synthetic query against it, and returns the number of z3
// SUBPROCESSES it launched together with the rendered output.
//
// basePATH is the PATH as it stood before any counter was installed; restoring
// it here means every arm resolves z3 identically and shims never chain.
//
// The shim is installed AFTER the puts so the count covers the query alone —
// apiPut does no proving today, but a count that silently included it would be
// measuring setup rather than the path under test.
func prefilterSyntheticArm(t *testing.T, basePATH string, defs ...string) (int, string) {
	t.Helper()
	st := newStore(t)
	for _, d := range defs {
		put(t, st, d)
	}
	t.Setenv("PATH", basePATH)
	count := installZ3Counter(t)
	out, err := apiFindImplies(st, prefilterSyntheticQuery)
	if err != nil {
		t.Fatalf("find --implies: %v", err)
	}
	return count(), out
}

// ptrProp exists only so a map value's Prop field can be addressed.
func ptrProp(p Prop) *Prop { return &p }

// TestConcretePassNeverRejectsOnImplementationLimits is the soundness control,
// and it is the half that a more "productive" prefilter would fail.
//
// An instrument that knows less about its subject reports more, and more
// rejections is the FEELING of a better prefilter. These assertions are what
// distinguish a sound one: a candidate the evaluator cannot finish, and a
// candidate that genuinely satisfies the law, must both survive.
func TestConcretePassNeverRejectsOnImplementationLimits(t *testing.T) {
	st := openCorpusCopy(t)
	qd, admitted := findImpliesAdmitForTest(t, st, prefilterFlippedQuery)
	_ = qd

	seen := map[string]ceOutcome{}
	for name, p := range admitted {
		seen[name] = concreteCounterexample(st, p.hash, &p.prop)
	}

	// CONTROL 1: definitions that provably satisfy the law must not be rejected.
	// This is the unsoundness that would silently delete answers.
	for _, n := range prefilterExpectedHits {
		o, ok := seen[n]
		if !ok {
			t.Fatalf("control candidate %s was not admitted — the control cannot run", n)
		}
		if o == ceFalsified {
			t.Errorf("UNSOUND: the concrete pass refuted %s, which provably satisfies the law", n)
		}
	}

	// CONTROL 2: a candidate whose evaluation CANNOT FINISH must be reported as
	// ceIndeterminate — exactly that, not merely "not falsified".
	//
	// spin-partial is the corpus's non-terminating exhibit, so its goal exhausts
	// fuel on every sample. That makes it the one candidate that can tell the two
	// halves of the soundness claim apart:
	//
	//   ceNone           "no sample was false"  — every sample RAN and came back true
	//   ceIndeterminate  "no sample was false, AND some sample never finished"
	//
	// Asserting only `!= ceFalsified` would accept ceNone here, and ceNone on
	// spin-partial would mean the evaluator silently reported a completed run for
	// a computation that cannot complete. That is a worse defect than the one the
	// weaker assertion was written to catch, and it would pass it.
	//
	// NO SKIP BRANCH. If spin-partial is not admitted, the fuel discrimination is
	// unwitnessed by this suite, and a test that quietly logs its own absence
	// reports success for having measured nothing. Whoever removes the exhibit
	// from the corpus should have to replace the witness deliberately.
	o, ok := seen["spin-partial"]
	if !ok {
		t.Fatalf("spin-partial is not admitted, so nothing here witnesses the fuel/error discrimination; "+
			"admitted set was %v", sortedNames(seen))
	}
	if o != ceIndeterminate {
		t.Errorf("spin-partial outcome = %d, want %d (ceIndeterminate): a computation that cannot "+
			"finish must be reported as an implementation limit, not as %s", o, ceIndeterminate, ceName(o))
	}

	// CONTROL 3: the pass must actually FIRE, or every bound above is vacuous.
	fired := 0
	for _, o := range seen {
		if o == ceFalsified {
			fired++
		}
	}
	if fired == 0 {
		t.Error("the concrete pass refuted NOTHING — it cannot be doing the work it is credited with")
	}
	t.Logf("concrete pass refuted %d of %d admitted candidates; spin-partial = %s", fired, len(seen), ceName(o))
}

// ceName renders an outcome for a failure message. A bare integer in an
// assertion about a three-valued result makes the reader consult the source to
// learn whether the test found the wrong answer or the wrong KIND of answer.
func ceName(o ceOutcome) string {
	switch o {
	case ceNone:
		return "ceNone (no sample was false, and every sample completed)"
	case ceFalsified:
		return "ceFalsified (a concrete countermodel)"
	case ceIndeterminate:
		return "ceIndeterminate (an implementation limit)"
	}
	return fmt.Sprintf("unknown outcome %d", int(o))
}

func sortedNames(m map[string]ceOutcome) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// findImpliesAdmitCand is one candidate plus the query property re-typed to it.
type findImpliesAdmitCand struct {
	hash string
	prop Prop
}

// findImpliesAdmitForTest reproduces apiFindImplies's admission decision using
// the SHIPPED predicates, so the controls above are exercised over exactly the
// goals the shipped path builds rather than over a re-implementation of them.
func findImpliesAdmitForTest(t *testing.T, st *Store, query string) (*Def, map[string]findImpliesAdmitCand) {
	t.Helper()
	forms, err := parseForms(query)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	qd, _, err := elabFunc(st, forms[0])
	if err != nil {
		t.Fatalf("elaborate query: %v", err)
	}
	qsig := tyBytes(qd.Ty)
	out := map[string]findImpliesAdmitCand{}
	for name, h := range st.Names() {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" {
			continue
		}
		qp := qd.Props[0]
		if string(tyBytes(d.Ty)) != string(qsig) {
			sub, ok := crossTypeCompatible(qd.Ty, d.Ty)
			if !ok {
				continue
			}
			qp = Prop{Binders: crossTypeRetypeBinders(qp.Binders, sub), Body: qp.Body}
		}
		aug := *d
		aug.Props = append(append([]Prop{}, d.Props...), qp)
		if err := checkDef(st, &aug); err != nil {
			continue
		}
		out[name] = findImpliesAdmitCand{hash: h, prop: qp}
	}
	return qd, out
}
