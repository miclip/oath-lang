package main

import (
	"strings"
	"testing"
)

// THE THREE-WAY PROPERTY OUTCOME, with firing and non-firing controls.
//
// The claim under test is one sentence: ONLY AN EVALUATED BOOLEAN FALSE
// REFUTES. Everything a case can do other than evaluate — exhaust fuel, exceed
// depth, fail to generate — produces no verdict, and no verdict is not a
// refutation.
//
// The discipline this repo asks for is "what mutation makes this fail?", and
// for a three-way classification the honest answer needs BOTH directions:
//
//	FIRING      an unevaluable case must come back INDETERMINATE, with no
//	            counterexample. Controlled by starving a REAL proven definition
//	            of fuel — `fib`, which #161's blast-radius measurement showed
//	            being downgraded to `falsified` on exactly this.
//	NON-FIRING  a genuinely false property must still come back FALSIFIED, with
//	            its counterexample. Controlled by `bad-reverse`, the corpus's
//	            deliberate refuted exhibit. If the new state swallowed real
//	            refutations it would be worse than the bug it replaces.
//
// The two controls run the SAME code over the SAME store and differ only in
// which definition and budget they use, so a change that collapses the
// distinction fails one of them whichever way it collapses.

func openCommittedStore(t *testing.T) *Store {
	t.Helper()
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	return st
}

func propIndex(t *testing.T, st *Store, name, prop string) (string, *Def, int) {
	t.Helper()
	h, ok := st.Resolve(name)
	if !ok {
		t.Fatalf("%s is not live in the committed store", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	m, err := st.GetMeta(h)
	if err != nil {
		t.Fatalf("reading %s metadata: %v", name, err)
	}
	for i, n := range m.PropNames {
		if n == prop {
			return h, d, i
		}
	}
	t.Fatalf("%s has no property %q (has %v)", name, prop, m.PropNames)
	return "", nil, 0
}

// TestFuelExhaustionIsIndeterminateNotFalsified is the FIRING control. Same
// definition, same property, same seeds — only the fuel budget differs. Under
// the real budget it passes; starved, it must report INDETERMINATE and NOT
// invent a counterexample.
func TestFuelExhaustionIsIndeterminateNotFalsified(t *testing.T) {
	st := openCommittedStore(t)
	h, d, pi := propIndex(t, st, "fib", "nonneg")
	base := caseSeedBase(h)

	// NON-FIRING half: the real budget. fib is committed `proven`; its property
	// holds on every generated case.
	full := runProp(st, h, &d.Props[pi], "nonneg", base, pi, 20, propFuel)
	if full.Outcome != PropPassed {
		t.Fatalf("CONTROL FAILED: fib.nonneg at the real fuel budget came back %q (%s); "+
			"the starved arm below cannot be attributed to fuel if the baseline is not clean",
			full.Outcome, full.Headline())
	}
	if full.Indet != 0 {
		t.Errorf("fib.nonneg at full fuel reported %d unevaluable cases; expected 0", full.Indet)
	}

	// FIRING half: starve it. fib is naive-exponential, so a tiny budget cannot
	// evaluate it — the same shape the blast-radius measurement produced with a
	// wider Int draw, reached here without touching the generator.
	starved := runProp(st, h, &d.Props[pi], "nonneg", base, pi, 20, 200)
	if starved.Outcome != PropIndeterminate {
		t.Fatalf("FIRING CONTROL FAILED: fib.nonneg starved of fuel came back %q, want %q. "+
			"An implementation limit is being reported as a semantic fact.",
			starved.Outcome, PropIndeterminate)
	}
	if starved.Falsified() {
		t.Error("a fuel-exhausted property reports Falsified(); refutation must require an evaluated false")
	}
	if starved.Counter != "" {
		t.Errorf("an indeterminate property carries a counterexample %q; "+
			"inputs that were never evaluated refute nothing and must not be labelled as refuting",
			starved.Counter)
	}
	if !strings.Contains(starved.Err, "fuel") {
		t.Errorf("indeterminate reason %q does not name fuel; the report must say WHY no verdict was reached", starved.Err)
	}
	if starved.Indet == 0 {
		t.Error("indeterminate outcome recorded 0 unevaluable cases")
	}
	// The rendered form must not print a counterexample line.
	label, text, ok := starved.Detail()
	if !ok || label != "no verdict" || text == "" {
		t.Errorf("indeterminate Detail() = (%q, %q, %v); want a non-empty \"no verdict\" reason", label, text, ok)
	}
	if strings.Contains(renderVerifyReports([]PropReport{starved}), "counterexample") {
		t.Error("the indeterminate transcript prints a counterexample label")
	}
}

// TestRealFalsePropertyStillFalsifies is the NON-FIRING control: the new
// indeterminate state must not swallow genuine refutations. bad-reverse is the
// corpus's deliberate falsified exhibit.
func TestRealFalsePropertyStillFalsifies(t *testing.T) {
	st := openCommittedStore(t)
	h, d, pi := propIndex(t, st, "bad-reverse", "antidistributes-over-append")
	rep := runProp(st, h, &d.Props[pi], "antidistributes-over-append", caseSeedBase(h), pi, propCases, propFuel)

	if rep.Outcome != PropFalsified {
		t.Fatalf("NON-FIRING CONTROL FAILED: bad-reverse.antidistributes-over-append came back %q, want %q. "+
			"The three-way outcome has swallowed a real refutation.", rep.Outcome, PropFalsified)
	}
	if rep.Counter == "" {
		t.Error("a refuted property carries no counterexample; the evidence for the refutation is missing")
	}
	if rep.Err != "" {
		t.Errorf("a refuted property carries an indeterminate reason %q; the two fields must not both be set", rep.Err)
	}
	label, _, ok := rep.Detail()
	if !ok || label != "counterexample" {
		t.Errorf("falsified Detail() label = %q, want \"counterexample\"", label)
	}
}

// TestRefutationDominatesIndeterminacy: a property that is unevaluable on some
// cases and FALSE on another must report FALSIFIED. Stopping at the first
// unevaluable case would hide the refutation behind a fuel limit — turning a
// real defect into "no verdict", which is the same substitution running in the
// opposite direction, and just as wrong.
//
// The case is CONSTRUCTED, in a temp store, because a sweep over every property
// of the committed corpus at seven fuel budgets produced no natural instance:
// the corpus's refuted properties fail on their first case, so any budget small
// enough to strand a case strands that one too. An unwitnessed rule is a
// hypothesis, so the witness is built rather than waited for.
func TestRefutationDominatesIndeterminacy(t *testing.T) {
	st := newStore(t)
	// `countdown` is linear, so its cost is proportional to n and a small fuel
	// budget strands exactly the large draws. The property is FALSE at n == 2 —
	// cheap to evaluate, so the refutation is reachable at any budget — and
	// merely expensive elsewhere.
	put(t, st, `(defn countdown [] [(n Int)] Int
  (if (<= n 0) 0 (+ 1 (countdown (- n 1))))
  (prop mixed [(n Int)] (if (== n 2) false (== (countdown n) (countdown n)))))`)
	h, ok := st.Resolve("countdown")
	if !ok {
		t.Fatal("countdown was not bound in the temp store")
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("reading countdown: %v", err)
	}

	// CONTROL, full budget: no case is stranded, so the outcome is a clean
	// refutation. This is what the mixed run must still agree with.
	clean := runProp(st, h, &d.Props[0], "mixed", caseSeedBase(h), 0, propCases, propFuel)
	if clean.Outcome != PropFalsified || clean.Indet != 0 {
		t.Fatalf("CONTROL FAILED: at full fuel `mixed` is %q with %d unevaluable cases; want a clean refutation",
			clean.Outcome, clean.Indet)
	}

	// THE MIXED RUN: a budget that strands the large draws while leaving the
	// refuting case evaluable.
	mixed := runProp(st, h, &d.Props[0], "mixed", caseSeedBase(h), 0, propCases, 30)
	if mixed.Indet == 0 {
		t.Fatalf("SETUP FAILED: fuel 30 stranded no case, so this run does not test dominance at all "+
			"(outcome %q, passed %d)", mixed.Outcome, mixed.Passed)
	}
	if mixed.Outcome != PropFalsified {
		t.Fatalf("DOMINANCE FAILED: with %d unevaluable cases AND a refuting case, the outcome is %q, want %q. "+
			"A fuel limit is hiding a real defect.", mixed.Indet, mixed.Outcome, PropFalsified)
	}
	if mixed.Counter == "" {
		t.Error("the mixed outcome is falsified but carries no counterexample")
	}
	if mixed.Counter != clean.Counter {
		t.Errorf("the mixed run reports counterexample %q but the full-fuel run reports %q; "+
			"stranding unrelated cases must not change WHICH input refutes", mixed.Counter, clean.Counter)
	}
	// A refutation must not also carry a no-verdict reason. Err is documented
	// as indeterminate-only, and a record holding both a counterexample and an
	// error is the conflation this change removes, reappearing inside one row.
	if mixed.Err != "" {
		t.Errorf("the mixed run is falsified but still carries the no-verdict reason %q; "+
			"a refutation must clear it", mixed.Err)
	}
	if _, _, ok := mixed.Detail(); !ok {
		t.Error("the mixed run reports no detail line; a refutation always prints its counterexample")
	}
	if label, _, _ := mixed.Detail(); label != "counterexample" {
		t.Errorf("the mixed run's detail label is %q, want \"counterexample\"", label)
	}
	// SPEC §4.1: the reported count is PASSES, never attempts. With stranded
	// cases present the two differ, and this is the run that distinguishes them.
	if mixed.Passed+mixed.Indet >= propCases {
		t.Fatalf("SETUP: the mixed run consumed every case (%d passed + %d unevaluable); "+
			"it cannot show that the reported count excludes attempts", mixed.Passed, mixed.Indet)
	}
	if strings.Contains(mixed.Headline(), "after 0 cases") && mixed.Passed != 0 {
		t.Error("the headline count disagrees with the passed count")
	}
}

// TestGuaranteeFromReportsEveryOutcome asserts the ladder as a pure function —
// every combination, including the ones the corpus does not currently exhibit.
func TestGuaranteeFromReportsEveryOutcome(t *testing.T) {
	twoProps := &Def{Props: []Prop{{}, {}}}
	noProps := &Def{}
	p := func(o PropOutcome, name string) PropReport { return PropReport{Name: name, Outcome: o} }

	cases := []struct {
		name      string
		d         *Def
		reports   []PropReport
		wantLevel string
		wantFals  []string
		wantIndet []string
	}{
		{"no properties", noProps, nil, "asserted", nil, nil},
		{"all passed", twoProps,
			[]PropReport{p(PropPassed, "a"), p(PropPassed, "b")}, "tested", nil, nil},
		{"one falsified", twoProps,
			[]PropReport{p(PropPassed, "a"), p(PropFalsified, "b")}, "falsified", []string{"b"}, nil},
		{"both falsified", twoProps,
			[]PropReport{p(PropFalsified, "a"), p(PropFalsified, "b")}, "falsified", []string{"a", "b"}, nil},
		// The load-bearing row: indeterminate is NOT tested and NOT falsified.
		{"one indeterminate", twoProps,
			[]PropReport{p(PropPassed, "a"), p(PropIndeterminate, "b")}, "asserted", nil, []string{"b"}},
		{"all indeterminate", twoProps,
			[]PropReport{p(PropIndeterminate, "a"), p(PropIndeterminate, "b")}, "asserted", nil, []string{"a", "b"}},
		// Refutation dominates indeterminacy at the ladder — but the
		// indeterminate NAMES survive, or `explain` reports an unevaluable
		// property as tested on a definition that is already known bad.
		{"falsified beats indeterminate", twoProps,
			[]PropReport{p(PropIndeterminate, "a"), p(PropFalsified, "b")}, "falsified", []string{"b"}, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := guaranteeFromReports(tc.d, tc.reports)
			if g.Level != tc.wantLevel {
				t.Errorf("level = %q, want %q", g.Level, tc.wantLevel)
			}
			if strings.Join(g.Falsified, ",") != strings.Join(tc.wantFals, ",") {
				t.Errorf("falsified = %v, want %v", g.Falsified, tc.wantFals)
			}
			if strings.Join(g.Indeterminate, ",") != strings.Join(tc.wantIndet, ",") {
				t.Errorf("indeterminate = %v, want %v", g.Indeterminate, tc.wantIndet)
			}
			if tc.wantLevel == "tested" && g.Cases != propCases {
				t.Errorf("tested guarantee records %d cases, want %d", g.Cases, propCases)
			}
			// An `asserted` level must never claim a case count: it would read
			// as evidence that testing established something.
			if tc.wantLevel == "asserted" && g.Cases != 0 {
				t.Errorf("asserted guarantee claims %d cases; it established nothing", g.Cases)
			}
		})
	}
}

// TestResolveLevelPreservesProofsAcrossIndeterminacy asserts every combination
// of the proof-preservation rule. The row that matters: a `proven` definition
// whose run came back INDETERMINATE keeps its proof. A proof is about all
// inputs; a case that ran out of fuel is about the budget.
func TestResolveLevelPreservesProofsAcrossIndeterminacy(t *testing.T) {
	cases := []struct {
		computed, prev string
		allProven      bool
		want           string
	}{
		// The whole point of the change.
		{"asserted", "proven", true, "proven"},
		{"tested", "proven", true, "proven"},
		// A refutation contradicts the proof outright and must win.
		{"falsified", "proven", true, "falsified"},
		// Without a complete standing proof there is nothing to preserve.
		{"asserted", "proven", false, "asserted"},
		{"tested", "proven", false, "tested"},
		// A definition that was never proven is not promoted by indeterminacy.
		{"asserted", "tested", true, "asserted"},
		{"asserted", "asserted", true, "asserted"},
		{"tested", "tested", true, "tested"},
		{"tested", "asserted", false, "tested"},
		{"falsified", "tested", false, "falsified"},
	}
	for _, tc := range cases {
		got := resolveLevel(tc.computed, tc.prev, tc.allProven)
		if got != tc.want {
			t.Errorf("resolveLevel(computed=%q, prev=%q, allProven=%v) = %q, want %q",
				tc.computed, tc.prev, tc.allProven, got, tc.want)
		}
	}
}

// TestIndeterminateGuaranteeIsDistinguishableFromNoProperties. `asserted` now
// covers two situations, and a level that cannot tell them apart tells a reader
// a definition has no properties when it has properties nobody could evaluate.
func TestIndeterminateGuaranteeIsDistinguishableFromNoProperties(t *testing.T) {
	bare := guaranteeFromReports(&Def{}, nil)
	if len(bare.Indeterminate) != 0 {
		t.Errorf("a definition with no properties names %v as indeterminate", bare.Indeterminate)
	}
	if got := guaranteeString(bare); !strings.Contains(got, "no properties") {
		t.Errorf("bare asserted renders as %q; want it to say the definition has no properties", got)
	}

	indet := guaranteeFromReports(&Def{Props: []Prop{{}, {}}}, []PropReport{
		{Name: "holds", Outcome: PropPassed},
		{Name: "unevaluable", Outcome: PropIndeterminate},
	})
	if len(indet.Indeterminate) != 1 || indet.Indeterminate[0] != "unevaluable" {
		t.Fatalf("indeterminate names = %v, want [unevaluable]", indet.Indeterminate)
	}
	got := guaranteeString(indet)
	if strings.Contains(got, "no properties") {
		t.Errorf("an indeterminate guarantee renders as %q — it claims the definition has no "+
			"properties, when it has one that reached no verdict", got)
	}
	if !strings.Contains(got, "unevaluable") {
		t.Errorf("an indeterminate guarantee renders as %q without naming the property", got)
	}
}

// TestProvePromotionAcceptsIndeterminate asserts the promotability rule that
// the ladder change created. Without it a definition whose properties cannot be
// evaluated is unprovable IN PRINCIPLE: testing can never lift it to `tested`,
// so a complete proof has nowhere to land — and those are exactly the
// definitions a proof is most needed for.
func TestProvePromotionAcceptsIndeterminate(t *testing.T) {
	// CALLS the prover's own predicate. Re-spelling it here would let this test
	// agree with itself while disagreeing with the code it claims to check.
	provable := promotableToProven
	cases := []struct {
		name string
		g    Guarantee
		want bool
	}{
		{"tested", Guarantee{Level: "tested"}, true},
		{"already proven", Guarantee{Level: "proven"}, true},
		{"asserted through indeterminacy", Guarantee{Level: "asserted", Indeterminate: []string{"p"}}, true},
		// Nothing to prove, and nothing to promote.
		{"asserted with no properties", Guarantee{Level: "asserted"}, false},
		// A refutation and a proof cannot both stand.
		{"falsified", Guarantee{Level: "falsified", Falsified: []string{"p"}}, false},
	}
	for _, tc := range cases {
		if got := provable(tc.g); got != tc.want {
			t.Errorf("%s: provable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestOnlyRefutationKillsAMutant: a mutant whose properties cannot be evaluated
// has not been caught. Counting it as killed would credit the specification for
// the harness running out of budget — and the score already answers REACH, not
// exclusion.
// Both outcomes are asserted on ONE definition, so the classifier is shown to
// DISCRIMINATE rather than merely to have produced each label somewhere:
// `pow`'s `- → +` mutant turns its recursion non-terminating (previously scored
// as a kill), while other `pow` mutants are genuinely refuted.
func TestOnlyRefutationKillsAMutant(t *testing.T) {
	st := openCommittedStore(t)
	h, ok := st.Resolve("pow")
	if !ok {
		t.Skip("pow is not live in the committed store")
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("reading pow: %v", err)
	}
	m, err := st.GetMeta(h)
	if err != nil {
		t.Fatalf("reading pow metadata: %v", err)
	}
	var indetSurvivors, killedByRefutation []string
	for _, mu := range genMutants(st, d) {
		st.CacheDef(mu.hash, mu.def)
		killer, indeterminate := mutantKiller(st, m, mu)
		if killer != "" && indeterminate {
			t.Errorf("mutant %q reports BOTH a killer (%s) and indeterminacy; a refutation "+
				"short-circuits, so the two must not both be returned", mu.desc, killer)
		}
		switch {
		case killer != "":
			killedByRefutation = append(killedByRefutation, mu.desc)
		case indeterminate:
			indetSurvivors = append(indetSurvivors, mu.desc)
		}
	}
	// FIRING: an unevaluable mutant must NOT be counted as killed.
	if len(indetSurvivors) == 0 {
		t.Errorf("FIRING CONTROL FAILED: no mutant of `pow` survived by indeterminacy, though its "+
			"recursion can be mutated into non-termination. Unevaluable mutants are being counted "+
			"as killed. (killed: %v)", killedByRefutation)
	}
	// NON-FIRING: real refutations must still kill, or the change has simply
	// disarmed mutation scoring.
	if len(killedByRefutation) == 0 {
		t.Errorf("NON-FIRING CONTROL FAILED: no mutant of `pow` was killed by a refutation; "+
			"the three-way outcome has disarmed mutation scoring. (indeterminate survivors: %v)",
			indetSurvivors)
	}
}
