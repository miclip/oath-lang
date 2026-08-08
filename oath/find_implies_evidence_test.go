package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Tests for what a REFUTATION'S EVIDENCE LINE carries (#157).
//
// THE DEFECT THESE REGRESS AGAINST: `runZ3Budget` appends `(get-info :rlimit)`
// and `(get-info :reason-unknown)` to every script — attempt-validity telemetry,
// deliberately outside the hashed core (SPEC §7.2). z3 answers them after the
// model, so a `sat` outcome's `detail` is the countermodel FOLLOWED BY two forms
// that are facts about the SOLVER RUN and bind nothing. `implyEvidenceLine`
// renders the whole string under the words "countermodel (solver):", so the
// reader is handed `(:reason-unknown "")` as part of a countermodel — a line
// that has just claimed a refutation ending in what reads as the prover
// declining to give a reason. That is the same conflation #156 removed one
// layer up, surviving inside the evidence text.
//
// The claim is therefore: THE EVIDENCE LINE FOR A SOLVER REFUTATION CARRIES THE
// COUNTERMODEL AND NOTHING THAT IS NOT PART OF IT. Two tests, because the claim
// has two halves that fail in opposite directions — dropping nothing (today) and
// dropping too much (the way a repair goes wrong).

// proveGetInfoKeys derives the telemetry vocabulary from its AUTHORITY —
// prove.go's own `(get-info ...)` calls — rather than restating the two keys
// that happen to be appended today. A third telemetry line added later is then
// covered by these tests on the day it is added, with nothing here to update.
//
// A hand-written list would be correct exactly once, and nothing would announce
// when it stopped being.
func proveGetInfoKeys(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("prove.go")
	if err != nil {
		t.Fatalf("cannot read the authority for the telemetry vocabulary: %v", err)
	}
	var keys []string
	for _, m := range regexp.MustCompile(`\(get-info :([a-zA-Z0-9_-]+)\)`).FindAllStringSubmatch(string(src), -1) {
		keys = append(keys, m[1])
	}
	// THE INSTRUMENT CHECK. If the pattern stops matching — the calls move, or
	// get built rather than written literally — every absence assertion below
	// becomes vacuously true and these tests would report success while
	// measuring nothing.
	if len(keys) == 0 {
		t.Fatal("no (get-info :...) call found in prove.go: this test derives its universe from " +
			"that call site, so it is now checking nothing — re-point it at wherever the telemetry is emitted")
	}
	return keys
}

// TestSolverTelemetryKeysMatchProveGo is what makes api.go's copy of the
// telemetry vocabulary safe to hold. The AUTHORITY is prove.go's get-info call
// site; `solverTelemetryKeys` restates it, and a restatement is correct exactly
// once unless something fails when it stops being.
//
// WHAT MUTATION MAKES IT FAIL: adding a `(get-info :X)` to prove.go without
// adding X here — the case where the renderer would silently start showing a
// third telemetry form again.
func TestSolverTelemetryKeysMatchProveGo(t *testing.T) {
	want := map[string]bool{}
	for _, k := range proveGetInfoKeys(t) {
		want[k] = true
	}
	got := map[string]bool{}
	for _, k := range solverTelemetryKeys {
		got[k] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("prove.go asks for (get-info :%s) but solverTelemetryKeys does not name it, so the "+
				"renderer will present its answer as part of the countermodel", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("solverTelemetryKeys names %q, which prove.go never asks for — this list is "+
				"derived from that call site, and cutting a form nobody requested is a guess", k)
		}
	}
}

// TestStripSolverTelemetryIsConservative pins the direction of the caution. The
// function may leave telemetry in place; it may NEVER remove anything else, and
// each arm below is a way a less careful implementation removes something else.
func TestStripSolverTelemetryIsConservative(t *testing.T) {
	for _, c := range []struct {
		name, in string
	}{
		{"unbalanced parens", `( (define-fun b0 () Int 1) (:rlimit 89)`},
		{"unterminated string", `( (define-fun b0 () Int 1) ) (:reason-unknown "oops`},
		{"nothing but telemetry", `(:rlimit 89) (:reason-unknown "")`},
		{"telemetry-shaped form inside the model", `( (:rlimit 89) (define-fun b0 () Int 1) )`},
		{"a keyword form this kernel never asked for", `( (define-fun b0 () Int 1) ) (:some-other-info 3)`},
		{"no telemetry at all", `( (define-fun b0 () Int 1) )`},
	} {
		if got := stripSolverTelemetry(c.in); got != c.in {
			t.Errorf("%s: input was modified\n in: %q\nout: %q", c.name, c.in, got)
		}
	}

	// THE CONTROL. Without it every arm above is satisfied by a function that
	// returns its input, which strips nothing and passes the whole conservatism
	// suite while leaving the defect in place.
	const real = "(\n  (define-fun b0 () Int 1)\n)\n(:rlimit 89)\n(:reason-unknown \"\")"
	if got := stripSolverTelemetry(real); got != "(\n  (define-fun b0 () Int 1)\n)" {
		t.Errorf("the recognised trailing telemetry was not removed: %q", got)
	}
	// A payload containing parentheses must not desynchronise the parse — the
	// case where a naive scan cuts into the model.
	const parens = "(\n  (define-fun b0 () Int 1)\n)\n(:reason-unknown \"(incomplete quantifiers)\")"
	if got := stripSolverTelemetry(parens); got != "(\n  (define-fun b0 () Int 1)\n)" {
		t.Errorf("a parenthesised reason-unknown payload broke the cut: %q", got)
	}
}

// TestImplyEvidenceLineTouchesOnlySolverRefutations is the scope assertion: the
// cut is applied to the one evidence kind that is z3's raw response. An
// evaluation countermodel is this kernel's own rendering of an environment, and
// an unsettled candidate's evidence is prose; parsing either would be applying a
// solver-output model to text that is not solver output.
func TestImplyEvidenceLineTouchesOnlySolverRefutations(t *testing.T) {
	// Contrived evidence that the stripper WOULD cut if it were applied — so an
	// implementation that strips unconditionally fails here.
	const bait = "(\n  (define-fun b0 () Int 1)\n)\n(:rlimit 89)"
	for _, c := range []struct {
		name string
		r    implyResult
	}{
		{"refuted by evaluation", implyResult{status: implyRefuted, evidence: bait, byEval: true}},
		{"no verdict", implyResult{status: implyUnknown, evidence: bait}},
		{"aborted attempt", implyResult{status: implyInvalidated, evidence: bait}},
	} {
		if got := implyEvidenceLine(c.r); !strings.Contains(got, "(:rlimit 89)") {
			t.Errorf("%s: evidence that is not solver output was cut anyway:\n%s", c.name, got)
		}
	}
}

// solverRefutationDetail is the SHAPE `propOutcome.detail` has for a solver
// refutation: the model s-expression, then the answers to the appended
// `(get-info ...)` commands. Whitespace is not load-bearing — implyEvidenceLine
// collapses runs of it — so this fixture pins the ORDER and the FORMS. The exact
// bytes a live z3 produces are pinned by the end-to-end test below, which is the
// authority on them; this one is the authority on what the renderer does with
// that shape.
const solverRefutationDetail = `(
  (define-fun b0 () Int 1000000)
  (define-fun b1 () Int 0)
)
(:rlimit 89)
(:reason-unknown "")`

// TestImplyEvidenceLineDropsSolverTelemetry asserts the renderer keeps the
// bindings and drops the run telemetry.
//
// WHAT MUTATIONS MAKE IT FAIL:
//   - rendering `detail` verbatim (today's behaviour): the telemetry survives.
//   - dropping a binding along with the telemetry — the count check fires, which
//     a Contains on one binding would not.
//   - dropping the countermodel entirely and printing `refuted (solver)`: the
//     bindings are gone, and a refutation stops carrying its witness.
func TestImplyEvidenceLineDropsSolverTelemetry(t *testing.T) {
	keys := proveGetInfoKeys(t)
	line := implyEvidenceLine(implyResult{
		name: "sum-int", status: implyRefuted, evidence: solverRefutationDetail,
	})

	// THE CONTROL, FIRST: the line must still be a countermodel carrying both
	// bindings. Without this the absence assertions below are satisfied by a
	// renderer that has thrown the evidence away.
	if !strings.Contains(line, "countermodel (solver)") {
		t.Fatalf("a solver refutation must still render as a countermodel:\n%s", line)
	}
	for _, want := range []string{"(define-fun b0 () Int 1000000)", "(define-fun b1 () Int 0)"} {
		if !strings.Contains(line, want) {
			t.Errorf("the countermodel lost the binding %q — a refutation's whole content is which "+
				"environment refutes it:\n%s", want, line)
		}
	}
	if n := strings.Count(line, "define-fun"); n != 2 {
		t.Errorf("got %d bindings, want 2 — a filter that keeps only the first binding still passes a "+
			"Contains on it:\n%s", n, line)
	}

	// THE CLAIM. `(:rlimit 89)` is how much work the solver spent and
	// `(:reason-unknown "")` is why it stopped; neither binds a variable, and
	// under the words "countermodel (solver):" the second one reads as a verdict
	// the line has already contradicted.
	for _, k := range keys {
		if strings.Contains(line, ":"+k) {
			t.Errorf("the evidence line carries the solver telemetry %q — that is a fact about the "+
				"RUN, not part of the countermodel:\n%s", ":"+k, line)
		}
	}
}

// TestImplyEvidenceLinePreservesUnrecognisedModelText is the OTHER direction,
// and it exists because the repair for the test above is a filter — and a filter
// written from the two forms currently in mind will silently eat model text it
// does not recognise.
//
// z3's model vocabulary is larger than `(define-fun <name> () <ty> <lit>)`: a
// model can carry uninterpreted-sort declarations, function bodies with `ite`,
// `as-array` references, and generated names with `!`. Every one of those is
// part of the countermodel, and losing one silently is worse than the defect
// being repaired: the reader still sees a countermodel, and it is now wrong.
//
// WHAT MUTATION MAKES IT FAIL: any filter keyed on a REMEMBERED LIST of model
// forms rather than on what the telemetry is. Note the shape of the input — the
// telemetry is present here too, because z3 always appends it, so the arm is a
// realistic payload and not a contrived one.
func TestImplyEvidenceLinePreservesUnrecognisedModelText(t *testing.T) {
	keys := proveGetInfoKeys(t)
	const unusual = `(
  (declare-fun k () Elem)
  (define-fun f ((x!0 Int)) Int (ite (= x!0 0) 1 0))
  (define-fun g () (Array Int Int) (_ as-array k!0))
)
(:rlimit 89)
(:reason-unknown "(incomplete quantifiers)")`

	line := implyEvidenceLine(implyResult{
		name: "candidate", status: implyRefuted, evidence: unusual,
	})

	for _, want := range []string{
		"(declare-fun k () Elem)",
		"(define-fun f ((x!0 Int)) Int (ite (= x!0 0) 1 0))",
		"(define-fun g () (Array Int Int) (_ as-array k!0))",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the renderer dropped model text it did not recognise (%q) — the countermodel is "+
				"now silently wrong, which is worse than carrying the telemetry:\n%s", want, line)
		}
	}
	// And the telemetry still goes, including a NON-EMPTY reason-unknown payload:
	// a filter keyed on the literal empty string `(:reason-unknown "")` observed
	// in one run would pass everything above while leaving this one through.
	for _, k := range keys {
		if strings.Contains(line, ":"+k) {
			t.Errorf("the evidence line carries the solver telemetry %q:\n%s", ":"+k, line)
		}
	}
	if strings.Contains(line, "incomplete quantifiers") {
		t.Errorf("the reason-unknown PAYLOAD survived, so the filter matched a literal rather than "+
			"the telemetry form:\n%s", line)
	}
}

// TestFindImpliesSolverRefutationLineIsOnlyTheCountermodel is the shipped-path
// half. The renderer test above pins what implyEvidenceLine does with a fixture;
// this one pins that the bytes a LIVE z3 produces on the existing
// solver-refuted arm are what that fixture models, and that the shipped report
// is clean.
//
// It reuses reportSumInt/reportBounded, the arm
// TestFindImpliesReportsEachStatusEndToEnd already establishes reaches a SOLVER
// refutation — chosen because the pre-solver concrete pass passes `bounded`
// through (generated Ints are small) and z3 then refutes it.
//
// WHAT MUTATION MAKES IT FAIL: rendering `detail` verbatim, which is today.
func TestFindImpliesSolverRefutationLineIsOnlyTheCountermodel(t *testing.T) {
	requireZ3(t)
	keys := proveGetInfoKeys(t)

	st := newStore(t)
	put(t, st, reportSumInt)
	out, err := apiFindImplies(st, reportBounded, findImpliesDetailed)
	if err != nil {
		t.Fatalf("find --implies: %v", err)
	}

	// THE VACUITY GUARD. Everything below is about one line; if the arm stopped
	// producing a solver refutation the assertions would be about a report that
	// never contained one.
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "countermodel (solver)") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("this arm no longer reaches a solver refutation, so it witnesses nothing:\n%s", out)
	}

	// The countermodel is retained: both binders of `q` are bound in it.
	if n := strings.Count(line, "define-fun"); n != 2 {
		t.Errorf("got %d bindings on the countermodel line, want 2 (a and b):\n%s", n, line)
	}
	// And the run telemetry is not.
	for _, k := range keys {
		if strings.Contains(line, ":"+k) {
			t.Errorf("the shipped refutation line carries the solver telemetry %q — it is presented to "+
				"the reader as part of the countermodel:\n%s", ":"+k, line)
		}
	}
}
