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

	out, err := apiFindImplies(st, prefilterFlippedQuery, findImpliesSummary)
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

	// THE RESIDUE THE TUTORIAL ALSO PRINTS (#156). docs/tutorial/discovery.md now
	// shows this query's non-proven candidates as well as its hits, and reasons
	// about them by name — so those lines are corpus-derived claims in prose with
	// the same standing as the hit list above, and they rot the same way. Pinned
	// here for the same reason `prefilterExpectedHits` is: the document asserts
	// them and nothing else reads the document.
	//
	// NOT PINNED: the countermodel VALUES the tutorial prints (`2, 0` and the
	// rest). Those are whatever genValue produces, and pinning them would import
	// exactly the brittleness TestConcreteRefutationRetainsItsWitness declines a
	// golden string to avoid. The tutorial's CLAIMS about them — that `(pow 2 0)`
	// is 1 and `(pow 0 2)` is 0 — are facts about the language and hold whichever
	// environment the search happens to find.
	for _, want := range []string{
		"4 REFUTED",
		"1 NO VERDICT — the prover did not settle it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("docs/tutorial/discovery.md prints %q for this query and the summary no longer "+
				"says it:\n%s", want, out)
		}
	}
	detailed, err := apiFindImplies(st, prefilterFlippedQuery, findImpliesDetailed)
	if err != nil {
		t.Fatalf("find --implies --details: %v", err)
	}
	for _, want := range []string{"e-div", "e-mod", "pow", "rat-recover", "spin-partial"} {
		if !strings.Contains(detailed, want) {
			t.Errorf("docs/tutorial/discovery.md names %q in its --details transcript and it is no "+
				"longer reported:\n%s", want, detailed)
		}
	}
	// And the tutorial's reading of the last one: spin-partial is unsettled
	// BECAUSE THE PROVER DID NOT SETTLE IT — an implementation limit — which is a
	// narrower claim than "not refuted", and the narrower one is what the document
	// spends a paragraph on.
	//
	// TWO THINGS THIS ASSERTION HAD TO STOP DOING, both found by mutation:
	//
	//   - IT ASKED FOR THE "NO VERDICT" SECTION, AND THERE ARE TWO. Both rows in
	//     implyStatusRows begin "NO VERDICT", so implyDetailSection matched each of
	//     them and returned their union. Reclassifying spin-partial from
	//     implyUnknown to implyInvalidated moves it to the OTHER row — an
	//     ENVIRONMENTAL ABORT under SPEC §7.2, not a prover limit, so the tutorial's
	//     paragraph becomes wrong — and this assertion went on passing. It was
	//     rescued only by the count assertion above it, which is a different claim
	//     that a future edit is free to change. The row is now named by the half of
	//     its label that distinguishes it.
	//   - IT LOOKED FOR THE NAME ANYWHERE IN THE SECTION. Evidence text carries
	//     DEFINITION NAMES — spin-partial's own line reads "apply2 must be fully
	//     applied to inline" — so a section-wide match can be satisfied by another
	//     candidate's reason mentioning this one, which is a sentence about
	//     something else. The name must be the CANDIDATE of its line, which is what
	//     the leading-position check below requires.
	unsettled := implyDetailSection(t, detailed, "NO VERDICT — the prover did not settle it")
	spinLine := ""
	for _, l := range strings.Split(unsettled, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, " "), "spin-partial") {
			spinLine = l
		}
	}
	if spinLine == "" {
		t.Errorf("spin-partial is no longer reported as a candidate the PROVER DID NOT SETTLE; the "+
			"tutorial explains it as an implementation limit, which is neither a refutation nor an "+
			"environmental abort:\n%s", detailed)
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
// WHAT MUTATION MAKES IT FAIL: letting a concretely refuted candidate through to
// the prover — sending the `concreteProbe(...) == ceFalsified` branch on to
// newSmtCtx instead of recording the refutation and moving to the next candidate.
// Verified by reverting; the exact mutation and the observed counts are recorded
// in the test log.
//
// AND WHAT #156 ADDED TO IT: a refuted candidate is now a REPORTED RESULT, named
// with its countermodel in SUMMARY mode as well as detail. That does not change
// the solver cost — reporting a refutation launches no z3, because the whole
// reason it can be reported is that evaluation already settled it — but it does
// change what "the answer is unchanged" is allowed to mean here, so the second
// half of the semantic assertion below is stated positively rather than as an
// absence. See prefilterAssertRefutedByEvaluation for why.
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
	diffProbe := concreteProbe(probe, admitted["diff-int"].hash, ptrProp(admitted["diff-int"].prop))
	if diffProbe.outcome != ceFalsified {
		t.Fatalf("diff-int is not concretely refuted (%s) — it is not the case this test needs", ceName(diffProbe.outcome))
	}
	// diff-int's OWN countermodel, computed here against an independent store and
	// used below as the expected evidence in the report. It is derived rather than
	// pinned as a golden: findImpliesProbeSeed fixes the sample sequence and
	// printValue is structural, so the witness is a function of (defs, prop) and
	// this probe's store and the arm's store must produce the same one. That is
	// what makes the assertion CANDIDATE-SPECIFIC — the word "countermodel" alone
	// is satisfied by any candidate's evidence pasted onto any candidate's line,
	// and by the section heading, which itself contains it.
	wantWitness := diffProbe.witness(probe)
	if wantWitness == "" {
		t.Fatalf("the concrete refutation of diff-int rendered no witness, so there is no "+
			"candidate-specific evidence for this test to look for; env=%v", diffProbe.env)
	}
	if o := concreteProbe(probe, admitted["sum-int"].hash, ptrProp(admitted["sum-int"].prop)).outcome; o == ceFalsified {
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
	if satisfyingLineFor(outProven, "sum-int") == "" {
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
	// hit survives, and the refuted candidate is REPORTED AS REFUTED.
	//
	// This half doubles as satisfyingLineFor's own control: it must find the hit
	// that IS there, or its silence about diff-int would prove nothing.
	if satisfyingLineFor(outBoth, "sum-int") == "" {
		t.Errorf("sum-int disappeared once diff-int was in the store:\n%s", outBoth)
	}
	prefilterAssertRefutedByEvaluation(t, outBoth, "diff-int", wantWitness)
}

// prefilterAssertRefutedByEvaluation asserts the full disposition of a
// concretely refuted candidate in a rendered report: PRESENT under REFUTED, on
// its own line, carrying its OWN countermodel and naming EVALUATION as the
// source of it, and ABSENT from the satisfying lines.
//
// WHY ALL FOUR AND NOT THE LAST ONE ALONE. The assertion this replaces was
// `satisfyingLineFor(outBoth, "diff-int") == ""` — an exclusion, and exclusions
// are one-directional by construction. It is satisfied by the behaviour it was
// written to pin AND by a renderer that drops the candidate silently, which is
// precisely the conflation #156 removed from the product and left standing here.
// A test whose only claim is that something is missing cannot tell "correctly
// classified elsewhere" from "not classified at all".
//
// WHY THE EVIDENCE IS CHECKED ON THE CANDIDATE'S LINE AND AGAINST ITS OWN
// WITNESS. The REFUTED heading reads "...(a countermodel exists)", so a Contains
// for "countermodel" over the section is satisfied by the heading with every
// candidate's evidence stripped. And a line-scoped Contains for the bare word is
// satisfied by any candidate's values on any candidate's line. Requiring the
// witness that concreteProbe independently produces FOR THIS CANDIDATE is what
// makes the evidence attributable rather than merely present.
//
// WHY "by evaluation" IS PART OF IT. It is the report's claim that the verdict
// cost no solver launch, and it is the rendering-side counterpart of the count
// assertion above: the two would drift apart silently if only one were pinned.
//
// WHAT MUTATIONS MAKE IT FAIL — each verified by making the edit in
// apiFindImplies's ceFalsified branch, running, and restoring. The failure each
// one produces is named here rather than pointed at, so the record cannot come
// apart from the assertion it justifies:
//
//   - SILENT DROP: `continue` past a ceFalsified candidate without appending an
//     implyResult (the pre-#156 behaviour). No REFUTED section exists and the
//     lookup fatals. The old exclusion assertion passed this unchanged.
//   - MISCLASSIFICATION: record the refuted candidate as implyProven instead.
//     diff-int is reported as satisfying the law it falsifies; the satisfying
//     check fires and the REFUTED lookup fatals.
//   - EVIDENCE DROPPED: omit `evidence: probe.witness(st)`, keeping the
//     classification. The line renders `refuted (by evaluation)` and the
//     countermodel check fires. This is what stops clause 3 riding along on the
//     two above, both of which fail before the evidence is ever read.
//   - SOURCE MISREPORTED: omit `byEval: true`. The line renders
//     `countermodel (solver): …` — a verdict the report attributes to z3 while
//     the count assertion above says no z3 ran — and the source check fires.
func prefilterAssertRefutedByEvaluation(t *testing.T, out, name, wantWitness string) {
	t.Helper()
	if hit := satisfyingLineFor(out, name); hit != "" {
		t.Errorf("%s is reported as satisfying a law it falsifies:\n%s", name, hit)
	}
	// implyDetailSection fatals when the row is absent, which is the correct
	// verdict for a silent drop: there is no refuted section to read.
	sect := implyDetailSection(t, out, "REFUTED")
	line := ""
	for _, l := range strings.Split(sect, "\n") {
		if strings.Contains(l, name) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("%s was refuted by the concrete pass but is not named under REFUTED — a refutation "+
			"that is not reported is indistinguishable from a candidate that was never considered:\n%s", name, out)
	}
	if !strings.Contains(line, "by evaluation") {
		t.Errorf("%s's refutation does not name EVALUATION as its source, so the report no longer "+
			"says the verdict was reached without the solver:\n%s", name, line)
	}
	if !strings.Contains(line, wantWitness) {
		t.Errorf("%s's reported evidence does not contain its own countermodel (%q) — the refutation "+
			"is asserted rather than witnessed:\n%s", name, wantWitness, line)
	}
	t.Logf("refuted line: %s", strings.TrimSpace(line))
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
	out, err := apiFindImplies(st, prefilterSyntheticQuery, findImpliesSummary)
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
		seen[name] = concreteProbe(st, p.hash, &p.prop).outcome
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

// TestConcreteRefutationRetainsItsWitness is the regression witness for the
// countermodel the pre-solver search used to discard.
//
// THE CLAIM, in two halves that fail in opposite directions: a ceFalsified result
// carries the environment that actually falsified the goal and renders it
// deterministically, and the other two outcomes carry NOTHING.
//
// WHY THE LOAD-BEARING ASSERTION IS A RE-EVALUATION AND NOT A STRING CHECK: a
// result that retains SOME environment passes any "the witness is non-empty"
// test while being evidence for nothing. Re-running the goal under the retained
// environment is the only assertion that distinguishes the countermodel the
// search found from a plausible-looking neighbour of it, and it borrows no
// knowledge of how the search picked it.
//
// WHAT MUTATIONS MAKE IT FAIL — three, each verified by making the edit and
// watching it fail, then restoring:
//
//   - dropping the witness (`return ceResult{outcome: ceFalsified}`), which is
//     the behaviour this test regresses against. Fails on the arity check.
//   - retaining an environment that is not the falsifying one. Fails on the
//     re-evaluation: the goal comes back true.
//   - attaching the last sampled environment to a non-falsified outcome, the
//     mutation that would let an implementation limit read as a countermodel.
//     Fails on the sum-int and spin-partial arms.
//
// The rendering assertions deliberately do NOT pin a golden string. The values
// are whatever genValue produces, and a golden would fail on any unrelated
// generator change while witnessing nothing the determinism check below does not.
func TestConcreteRefutationRetainsItsWitness(t *testing.T) {
	probe := newStore(t)
	put(t, probe, prefilterSyntheticSum)
	put(t, probe, prefilterSyntheticDiff)
	_, admitted := findImpliesAdmitForTest(t, probe, prefilterSyntheticQuery)
	for _, want := range []string{"sum-int", "diff-int"} {
		if _, ok := admitted[want]; !ok {
			t.Fatalf("%s was not admitted, so this test cannot witness anything", want)
		}
	}

	// ARM 1 — the refuted candidate. diff-int falsifies flipped commutativity, so
	// this is the outcome that must carry evidence.
	diff := admitted["diff-int"]
	dp := diff.prop
	got := concreteProbe(probe, diff.hash, &dp)
	if got.outcome != ceFalsified {
		t.Fatalf("diff-int outcome = %s, want ceFalsified — this test needs the refuted case", ceName(got.outcome))
	}
	if len(got.env) != len(dp.Binders) {
		t.Fatalf("the refutation retained %d values for %d binders — the witness was discarded or truncated",
			len(got.env), len(dp.Binders))
	}

	// THE DISCRIMINATING ASSERTION: the retained environment, re-evaluated, must
	// make the goal false. A witness that does not falsify is not a witness.
	ev := &evaluator{st: probe, fuel: findImpliesProbeFuel}
	out, err := ev.eval(got.env, diff.hash, &dp.Body)
	if err != nil {
		t.Fatalf("the retained witness does not evaluate: %v — it is not an environment the goal was run in", err)
	}
	if out.K != "bool" {
		t.Fatalf("the retained witness evaluates to %q, not bool — the search reported a refutation it did not have", out.K)
	}
	if out.Bool {
		t.Errorf("the retained environment SATISFIES the goal: the result kept some environment, but not the one that falsified it")
	}

	w := got.witness(probe)
	if w == "" {
		t.Fatalf("a ceFalsified result rendered no witness")
	}
	for i, v := range got.env {
		if !strings.Contains(w, printValue(probe, v)) {
			t.Errorf("binder %d (%s) is missing from the rendered witness %q", i, printValue(probe, v), w)
		}
	}
	// DETERMINISM: an independent search over the same store must render the same
	// witness. This is what a discovery result reporting the witness would depend
	// on, and it is the same guarantee findImpliesProbeSeed gives the skip itself.
	again := concreteProbe(probe, diff.hash, &dp)
	if w2 := again.witness(probe); w2 != w {
		t.Errorf("the witness is not deterministic: %q then %q", w, w2)
	}
	t.Logf("diff-int witness: %s", w)

	// ARM 2 — a candidate that SATISFIES the law. No countermodel exists, so none
	// may be reported.
	sum := admitted["sum-int"]
	sp := sum.prop
	sr := concreteProbe(probe, sum.hash, &sp)
	if sr.outcome == ceFalsified {
		t.Fatalf("UNSOUND: the concrete pass refuted sum-int, which satisfies the law")
	}
	if sr.env != nil || sr.witness(probe) != "" {
		t.Errorf("%s carried a witness (%d values, rendered %q) — only a refutation has one",
			ceName(sr.outcome), len(sr.env), sr.witness(probe))
	}

	// ARM 3 — an IMPLEMENTATION LIMIT. spin-partial cannot finish, so every sample
	// exhausts fuel and the pass reports ceIndeterminate. This is the arm that
	// catches a result which attaches whatever environment it last generated:
	// there, an environment nothing was ever decided under would be rendered as a
	// countermodel, which is exactly the promotion this pass exists not to make.
	//
	// NO SKIP BRANCH, for the reason the control above it states: a test that logs
	// its own absence reports success for having measured nothing.
	corpus := openCorpusCopy(t)
	_, corpusAdmitted := findImpliesAdmitForTest(t, corpus, prefilterFlippedQuery)
	spin, ok := corpusAdmitted["spin-partial"]
	if !ok {
		t.Fatalf("spin-partial is not admitted, so nothing here witnesses that an implementation "+
			"limit carries no witness; admitted set was %v", sortedCandNames(corpusAdmitted))
	}
	sipr := spin.prop
	ir := concreteProbe(corpus, spin.hash, &sipr)
	if ir.outcome != ceIndeterminate {
		t.Fatalf("spin-partial outcome = %s, want ceIndeterminate — this arm needs the limit case", ceName(ir.outcome))
	}
	if ir.env != nil || ir.witness(corpus) != "" {
		t.Errorf("an implementation limit carried a witness (%d values, rendered %q): a computation that "+
			"never finished cannot have refuted anything", len(ir.env), ir.witness(corpus))
	}
}

func sortedCandNames(m map[string]findImpliesAdmitCand) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
