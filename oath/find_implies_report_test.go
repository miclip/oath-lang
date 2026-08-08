package main

import (
	"strings"
	"testing"
)

// Tests for what `find --implies` reports about the candidates it did NOT
// return (#156).
//
// THE DEFECT THESE REGRESS AGAINST: the search classified every admitted
// candidate four ways and printed only one of them, so "nothing in the corpus
// was close", "this definition provably does NOT satisfy your law" and "the
// prover declined" all rendered as the same silence — an implementation limit
// and a semantic fact wearing the same sentence.
//
// The split is deliberately tested at TWO levels. The classification and its
// rendering are asserted as PURE FUNCTIONS, because a rendering branch no query
// can cheaply provoke is otherwise indistinguishable from a correct one; the
// end-to-end arms then witness that the shipped path actually reaches all four.

// TestClassifyProofStatusIsTotal pins the mapping from the prover's status
// string onto the reported classification, INCLUDING the unrecognised case.
//
// WHAT MUTATIONS MAKE IT FAIL:
//   - mapping any two statuses onto one classification (the collapse #156
//     exists to undo) — the distinctness check below fires.
//   - reporting an unrecognised status as RECOGNISED, which would let a status
//     prove.go grows later vanish into the residue with no trace.
//   - degrading an unrecognised status to implyProven or implyRefuted, either of
//     which turns "this kernel does not know what happened" into a claim about
//     the definition.
func TestClassifyProofStatusIsTotal(t *testing.T) {
	for _, c := range []struct {
		status string
		want   implyStatus
	}{
		{"proven", implyProven},
		{"refuted", implyRefuted},
		{"unknown", implyUnknown},
		{"invalidated", implyInvalidated},
	} {
		got, known := classifyProofStatus(c.status)
		if !known {
			t.Errorf("%q is a status prove.go produces and must be recognised", c.status)
		}
		if got != c.want {
			t.Errorf("classifyProofStatus(%q) = %d, want %d", c.status, got, c.want)
		}
	}

	// THE DISTINCTNESS CHECK. Asserting each mapping separately still passes for
	// a function that sends every status to the same value, which is precisely
	// the summing this mechanism removes.
	seen := map[implyStatus]string{}
	for _, s := range []string{"proven", "refuted", "unknown", "invalidated"} {
		got, _ := classifyProofStatus(s)
		if prev, dup := seen[got]; dup {
			t.Errorf("%q and %q classify identically — the four statuses are not distinct", prev, s)
		}
		seen[got] = s
	}

	// An unrecognised status must be REPORTED as unrecognised and degraded to the
	// classification that claims nothing about the definition.
	got, known := classifyProofStatus("something-prove-go-grew-later")
	if known {
		t.Error("an unrecognised status must not be reported as recognised")
	}
	if got != implyUnknown {
		t.Errorf("an unrecognised status degraded to %d; only implyUnknown (%d) claims nothing about the definition",
			got, implyUnknown)
	}
}

// implyMixedResults is one of each classification, with both refutation sources
// and both admission paths represented.
func implyMixedResults() []implyResult {
	return []implyResult{
		{name: "proved-exact", status: implyProven, method: "direct"},
		{name: "proved-cross", status: implyProven, method: "direct", crossSig: "(-> Rat Rat Rat)"},
		{name: "refuted-eval", status: implyRefuted, evidence: "2, 0", byEval: true},
		{name: "refuted-smt", status: implyRefuted, evidence: "(define-fun b0 () Int 7)"},
		{name: "no-verdict", status: implyUnknown, evidence: "hmac-sha256 is outside the provable fragment"},
		{name: "abort-cand", status: implyInvalidated, evidence: "forced abort (test injection)"},
	}
}

// TestRenderImplyResultsKeepsFourStatusesDistinct asserts every outcome of the
// renderer directly, which is the only way to witness the branches a synthetic
// query cannot reach cheaply.
//
// WHAT MUTATIONS MAKE IT FAIL:
//   - merging the two NO VERDICT rows into one (they are different claims: one
//     is what a better prover would move, the other is not).
//   - printing a status row whose count is zero, which communicates that a
//     population was examined and cleared when it was never populated.
//   - ignoring the mode, in either direction: naming candidates in summary
//     buries the answer, and withholding them in detail is the original defect.
func TestRenderImplyResultsKeepsFourStatusesDistinct(t *testing.T) {
	var summary strings.Builder
	renderImplyResults(&summary, implyMixedResults(), findImpliesSummary)
	s := summary.String()

	// The answer is NAMED in both modes — a count of hits would be strictly less
	// information than the hits.
	for _, want := range []string{"proved-exact", "proved-cross", "(-> Rat Rat Rat)", "cross-type"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lost %q from the proven answer:\n%s", want, s)
		}
	}
	// Every non-proven status carries its COUNT.
	for _, want := range []string{"2 REFUTED", "1 NO VERDICT"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q:\n%s", want, s)
		}
	}
	// REFUTATIONS ARE NAMED IN SUMMARY, WITH THEIR EVIDENCE; THE RESIDUE IS NOT.
	// The asymmetry is the whole content of calling a refutation a RESULT rather
	// than residue: "2 REFUTED" is not actionable, because the finding IS which
	// definition was refuted and by what. An unresolved COUNT is actionable at a
	// glance — it says the answer is incomplete without needing the names.
	//
	// Asserted in BOTH directions, because either alone is satisfied by a
	// renderer that names everything or names nothing.
	for _, want := range []string{"refuted-eval", "refuted-smt", "2, 0"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary must NAME a refuted candidate and its countermodel (%q) — a "+
				"refutation is a result, and %q alone is not actionable:\n%s", want, "2 REFUTED", s)
		}
	}
	for _, unwanted := range []string{"no-verdict", "abort-cand"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("summary named an UNRESOLVED candidate (%q) — the residue is counted, not "+
				"named; that is detail mode's job:\n%s", unwanted, s)
		}
	}
	// THE TWO NO-VERDICT ROWS ARE NOT ONE ROW. Counting them is what catches a
	// merge: both labels start identically, so a Contains on the prefix passes
	// for a renderer that prints one line.
	if n := strings.Count(s, "NO VERDICT"); n != 2 {
		t.Errorf("got %d NO VERDICT rows, want 2 — the solver declining and an aborted attempt are different claims:\n%s", n, s)
	}
	if !strings.Contains(s, "a limit of this prover") {
		t.Errorf("the unsettled row must say it is a limit of the prover, not a fact about the definition:\n%s", s)
	}
	if !strings.Contains(s, "SPEC §7.2") {
		t.Errorf("the aborted row must cite the rule that makes a negative verdict invalid:\n%s", s)
	}

	// THE ZERO-ROW CONTROL. Without it every assertion above passes for a
	// renderer that always prints all three labels with a count beside them,
	// which reports an examined-and-cleared population that never existed.
	var proven strings.Builder
	renderImplyResults(&proven, []implyResult{{name: "only-hit", status: implyProven, method: "direct"}}, findImpliesSummary)
	if p := proven.String(); strings.Contains(p, "REFUTED") || strings.Contains(p, "NO VERDICT") {
		t.Errorf("a population with only proven candidates reported empty status rows:\n%s", p)
	}

	// DETAIL MODE: the same classification, with the candidates and their
	// evidence. The refutation SOURCE is part of the evidence — a concrete
	// countermodel is an evaluation of the reference semantics and the solver's
	// is not, and only one of them explains why the pre-solver pass may act on it.
	var detail strings.Builder
	renderImplyResults(&detail, implyMixedResults(), findImpliesDetailed)
	d := detail.String()
	for _, want := range []string{
		"refuted-eval", "countermodel (by evaluation): 2, 0",
		"refuted-smt", "countermodel (solver): (define-fun b0 () Int 7)",
		"no-verdict", "hmac-sha256 is outside the provable fragment",
		"abort-cand", "forced abort (test injection)",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("detail mode missing %q:\n%s", want, d)
		}
	}
	// Detail is a SUPERSET of summary: the counts stay, so a reader is never made
	// to count lines to learn how many there were.
	for _, want := range []string{"2 REFUTED", "1 NO VERDICT"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail mode dropped the count %q:\n%s", want, d)
		}
	}
}

// TestRenderImplyResultsFallbackIsAboutTheWorld asserts the one sentence in this
// report that quantifies over the corpus rather than over a candidate.
//
// "no definition provably satisfies this" is a claim about the WORLD. It is
// supportable only when the signature-compatible set was empty; with a
// refutation on record the report has already said what was established, and
// with an unsettled candidate on record it would be asserting exactly what the
// prover declined to decide.
//
// WHAT MUTATIONS MAKE IT FAIL: printing the fallback whenever nothing PROVED
// (the pre-change condition), and never printing it at all.
func TestRenderImplyResultsFallbackIsAboutTheWorld(t *testing.T) {
	const claim = "no definition provably satisfies this"

	// THE CONTROL, first: with nothing admitted the sentence is the whole report.
	// Without this the suppression assertions below pass for a renderer that has
	// simply lost the line.
	var empty strings.Builder
	renderImplyResults(&empty, nil, findImpliesSummary)
	if !strings.Contains(empty.String(), claim) {
		t.Fatalf("an empty candidate set must still report that nothing satisfies the query:\n%s", empty.String())
	}

	for _, c := range []struct {
		name string
		r    implyResult
	}{
		{"a refutation", implyResult{name: "x", status: implyRefuted, evidence: "1, 0", byEval: true}},
		{"an unsettled candidate", implyResult{name: "x", status: implyUnknown, evidence: "declined"}},
		{"an aborted attempt", implyResult{name: "x", status: implyInvalidated, evidence: "aborted"}},
	} {
		for _, mode := range []findImpliesMode{findImpliesSummary, findImpliesDetailed} {
			var b strings.Builder
			renderImplyResults(&b, []implyResult{c.r}, mode)
			if strings.Contains(b.String(), claim) {
				t.Errorf("%s on record, yet the report still claims nothing satisfies the query (mode %d):\n%s",
					c.name, mode, b.String())
			}
		}
	}
}

// The definitions and queries the end-to-end arms need. Each is chosen so that
// exactly one classification is reachable and reachable FAST — the whole set
// runs in well under a second, which is what makes it usable as a regression
// witness rather than as a nightly job.
const (
	reportSumInt  = `(defn sum-int [] [(a Int) (b Int)] Int (+ a b))`
	reportDiffInt = `(defn diff-int [] [(a Int) (b Int)] Int (- a b))`
	// hmac-sha256 is deliberately outside the provable fragment (#78), so a goal
	// that reaches it is untranslatable and the prover returns no verdict. That
	// is an IMPLEMENTATION LIMIT about this kernel's proof surface, and the point
	// of the arm is that it must not read as a fact about `mac`.
	reportMac = `(data List [a]
  (Nil)
  (Cons a (List a)))
(defn mac [] [(k (List Int)) (m (List Int))] (List Int) (hmac-sha256 k m))`

	reportFlipped = `(defn q [] [(a Int) (b Int)] Int (+ a b)
  (prop flipped [(a Int) (b Int)] (== (q b a) (q a b))))`
	// True of every sampled environment (generated Ints are small) and false in
	// general, so the concrete pass passes it through and the SOLVER refutes it.
	// That separates the two refutation sources inside the shipped path.
	reportBounded = `(defn q [] [(a Int) (b Int)] Int (+ a b)
  (prop bounded [(a Int) (b Int)] (< (q a b) 1000000)))`
	reportMacRefl = `(defn q [] [(k (List Int)) (m (List Int))] (List Int) (hmac-sha256 k m)
  (prop refl [(k (List Int)) (m (List Int))] (== (q k m) (q k m))))`
)

// TestFindImpliesReportsEachStatusEndToEnd witnesses that the shipped path
// actually reaches all four classifications, and reports each of them.
//
// The renderer is asserted exhaustively above; this is the other half of the
// claim — that the classification is not merely renderable but PRODUCED. A pure
// renderer test alone would pass over a search that still collapsed everything
// into "proven or nothing", which is exactly the state before this change.
//
// WHAT MUTATION MAKES IT FAIL: restoring the pre-change search — `continue`ing
// past a concretely refuted candidate, and recording only `o.status == "proven"`.
// Then three of the four arms report nothing at all and the fallback fires in
// their place. Verified by reverting.
func TestFindImpliesReportsEachStatusEndToEnd(t *testing.T) {
	for _, c := range []struct {
		name    string
		defs    []string
		query   string
		abort   string // OATH_PROVE_FORCE_ABORT, when the arm needs the §7.2 path
		want    []string
		notWant []string
	}{
		{
			name:  "proven",
			defs:  []string{reportSumInt},
			query: reportFlipped,
			want:  []string{"sum-int", "provably satisfies it"},
			// The answer is a hit, so no residue row may appear beside it.
			notWant: []string{"REFUTED", "NO VERDICT"},
		},
		{
			name:  "refuted by evaluation",
			defs:  []string{reportDiffInt},
			query: reportFlipped,
			// The pre-solver pass found a concrete countermodel; it is reported as
			// a refutation carrying that environment, not skipped.
			want:    []string{"1 REFUTED", "diff-int", "countermodel (by evaluation)"},
			notWant: []string{"provably satisfies it", "NO VERDICT"},
		},
		{
			name:    "refuted by the solver",
			defs:    []string{reportSumInt},
			query:   reportBounded,
			want:    []string{"1 REFUTED", "sum-int", "countermodel (solver)"},
			notWant: []string{"provably satisfies it", "by evaluation"},
		},
		{
			name:  "no verdict — untranslatable",
			defs:  []string{reportMac},
			query: reportMacRefl,
			want: []string{
				"1 NO VERDICT", "a limit of this prover", "mac",
				"outside the provable fragment",
			},
			notWant: []string{"REFUTED", "provably satisfies it"},
		},
		{
			name:  "no verdict — attempt aborted",
			defs:  []string{reportSumInt},
			query: reportFlipped,
			abort: "prop0", // the synthetic query goal's name on a candidate with no props
			want:  []string{"1 NO VERDICT", "SPEC §7.2", "sum-int", "forced abort"},
			// The §7.2 rule is that no NEGATIVE verdict is valid here. Reporting it
			// as a refutation would be the exact promotion the rule forbids.
			notWant: []string{"REFUTED", "provably satisfies it"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			requireZ3(t)
			if c.abort != "" {
				t.Setenv("OATH_PROVE_FORCE_ABORT", c.abort)
			}
			st := newStore(t)
			for _, d := range c.defs {
				put(t, st, d)
			}
			out, err := apiFindImplies(st, c.query, findImpliesDetailed)
			if err != nil {
				t.Fatalf("find --implies: %v", err)
			}
			for _, want := range c.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			for _, unwanted := range c.notWant {
				if strings.Contains(out, unwanted) {
					t.Errorf("unexpected %q:\n%s", unwanted, out)
				}
			}
			// VACUITY GUARD: every arm above admits exactly one candidate, so an
			// arm that produced the fallback classified nothing and its assertions
			// were about an empty report.
			if strings.Contains(out, "no definition provably satisfies this") {
				t.Fatalf("no candidate was admitted, so this arm witnesses nothing:\n%s", out)
			}
		})
	}
}

// TestFindImpliesSuppressesFallbackAfterRefutation is the shipped-path half of
// the world-claim assertion.
//
// A refutation is a RESULT: `diff-int` provably does not satisfy flipped
// commutativity, and a report that answers "no definition provably satisfies
// this" has thrown that away and replaced it with a weaker sentence about the
// corpus. The control in the same test is what makes the suppression meaningful.
func TestFindImpliesSuppressesFallbackAfterRefutation(t *testing.T) {
	requireZ3(t)
	const claim = "no definition provably satisfies this"

	// Only a refuted candidate in the store, and in SUMMARY mode — the mode a
	// caller gets today, so the suppression is not a detail-mode nicety.
	st := newStore(t)
	put(t, st, reportDiffInt)
	out, err := apiFindImplies(st, reportFlipped, findImpliesSummary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 REFUTED") {
		t.Fatalf("the refutation was not reported, so nothing here is being suppressed:\n%s", out)
	}
	if strings.Contains(out, claim) {
		t.Errorf("a refutation is on record, yet the report claims nothing satisfies the query:\n%s", out)
	}

	// THE CONTROL: a query nothing is signature-compatible with. The sentence is
	// supportable there and must still be printed — without this arm the test
	// passes for a build that has simply deleted the fallback.
	bare := newStore(t)
	put(t, bare, reportDiffInt)
	none, err := apiFindImplies(bare, `(defn q [] [(a Int) (b Int) (c Int)] Int (+ a (+ b c))
  (prop assoc3 [(a Int) (b Int) (c Int)] (== (q a b c) (q c b a))))`, findImpliesSummary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none, claim) {
		t.Errorf("with no candidate admitted at all, the report must say so:\n%s", none)
	}
}
