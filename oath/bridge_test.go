package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The #68 bridge obligations (SPEC §7.4, issue-68.md §11.3).
//
// TWO CLASSES OF TEST LIVE HERE AND THEY RUN IN DIFFERENT PLACES. The byte
// assertions need no solver and run everywhere, including the Go CI job, which
// does NOT install z3. The discharge and control tests need z3 and skip cleanly
// without it — so in that job they prove nothing, and the byte pin plus
// `make check-bridge-bytes` are what actually guard this on every push. Said
// plainly because a skipped test reads as a passing one in a summary line.

// requireBridgeSolver skips unless the PINNED solver is on PATH.
//
// EVERY SOLVER ASSERTION IN THIS FILE IS ABOUT A SPECIFIC z3, AND `requireZ3`
// IS NOT ENOUGH FOR THAT. SPEC §7.2 makes an outcome a function of (script
// bytes, solver version, rlimit); this file pins the first and the third and
// then asserts the outcome, so leaving the second to whatever is installed
// asserts a fact nobody measured. It bites hardest on the one obligation
// recorded as `unknown`: a different z3 that discharged it would fail the suite
// while changing nothing about the recorded result, and a reader would be told
// the kernel had regressed.
//
// SKIP, NOT FAIL, and the choice is the same one `requireZ3` already makes: an
// unpinned z3 on a contributor's machine is a missing measurement, not a
// defect. `oath bridge-obligation --prove` REFUSES rather than skipping, because
// there the caller asked for a verdict and would otherwise get a wrong one.
func requireBridgeSolver(t *testing.T) {
	t.Helper()
	requireZ3(t)
	out, err := exec.Command("z3", "--version").Output()
	if err != nil {
		t.Skipf("z3 is on PATH but not runnable (%v); no measurement here", err)
	}
	if got := strings.TrimSpace(string(out)); got != bridgeSolverPin {
		t.Skipf("solver is %q, not the pinned %q — outcomes are a function of the "+
			"solver version, so this test would be asserting an unmeasured fact",
			got, bridgeSolverPin)
	}
}

// bridgeExpectedOrder is SPEC §7.4.9's emission order, spelled out rather than
// derived from bridgeObligations() — a test that read the order off the thing
// under test would assert only that a slice equals itself.
var bridgeExpectedOrder = []string{
	"measure-decreases", "roundtrip2-base", "roundtrip2-step",
	"transport-append-base", "transport-append-step",
	"transport-length-base", "transport-length-step",
	"transport-take-base", "transport-take-step",
	"transport-drop-base", "transport-drop-step",
}

// bridgeIsTransport reports whether an id names a TRANSPORT obligation. Keyed on
// the id prefix, which §7.4.9 fixes, so the two families' preamble check below
// cannot be satisfied by whichever preamble happens to match.
func bridgeIsTransport(id string) bool { return strings.HasPrefix(id, "transport-") }

// TestBridgeObligationsAreDistinct guards the cheapest way this could rot into
// nonsense: obligations that are secretly the same script. Every later test
// would still pass.
//
// IT ALSO CHECKS THE PREAMBLE BOTH WAYS, and that is the half worth stating.
// Asserting only that each script STARTS WITH its family's preamble is passed
// by a kernel that gave every obligation the carrier core, because the carrier
// core's first two lines are the transport preamble's first two. Asserting that
// the OTHER family's preamble is absent is what makes the check discriminate —
// the same defect §7.4.1 now names, arriving in the test rather than the code.
func TestBridgeObligationsAreDistinct(t *testing.T) {
	obs := bridgeObligations()
	if len(obs) != len(bridgeExpectedOrder) {
		t.Fatalf("expected %d obligations, got %d", len(bridgeExpectedOrder), len(obs))
	}
	seen := map[string]bool{}
	for i, o := range obs {
		if o.ID != bridgeExpectedOrder[i] {
			t.Errorf("obligation %d: id = %q, want %q (SPEC §7.4.9 fixes the order)",
				i, o.ID, bridgeExpectedOrder[i])
		}
		if seen[o.Script] {
			t.Errorf("obligation %q duplicates an earlier script", o.ID)
		}
		seen[o.Script] = true
		mine, theirs, fam := bridgeCore, bridgeTransportCore, "carrier"
		if bridgeIsTransport(o.ID) {
			mine, theirs, fam = bridgeTransportCore, bridgeCore, "transport"
		}
		if !strings.HasPrefix(o.Script, mine) {
			t.Errorf("obligation %q is a %s but does not begin with that family's preamble",
				o.ID, fam)
		}
		if strings.Contains(o.Script, theirs) {
			t.Errorf("obligation %q is a %s but carries the OTHER family's preamble", o.ID, fam)
		}
		if !strings.HasSuffix(o.Script, "(check-sat)\n") {
			t.Errorf("obligation %q does not end with (check-sat) and one LF", o.ID)
		}
	}
}

// TestBridgeTransportPreambleIsTheCoreMinusTwoThings pins the ONE structural
// relation between the two preambles that §7.4.1 and §7.4.4 both turn on: the
// transport preamble is the carrier core minus `of-seq` and minus the patterned
// `ite`-form defining equation, and NOTHING ELSE moved.
//
// Written as a line-level subsequence rather than as a string diff because the
// claim is about which LINES survive. A kernel that also, say, dropped a
// per-constructor equation would still "not contain of-seq" and would have
// stopped defining `to-seq` — the soundness requirement §7.4.1 states.
func TestBridgeTransportPreambleIsTheCoreMinusTwoThings(t *testing.T) {
	coreLines := strings.Split(strings.TrimSuffix(bridgeCore, "\n"), "\n")
	tLines := strings.Split(strings.TrimSuffix(bridgeTransportCore, "\n"), "\n")
	var dropped []string
	i := 0
	for _, cl := range coreLines {
		if i < len(tLines) && tLines[i] == cl {
			i++
			continue
		}
		dropped = append(dropped, cl)
	}
	if i != len(tLines) {
		t.Fatalf("the transport preamble is not a subsequence of the core: matched %d of %d lines",
			i, len(tLines))
	}
	if len(dropped) != 3 {
		t.Fatalf("expected exactly 3 core lines to be absent (of-seq's declaration, "+
			"of-seq's defining equation, and to-seq's patterned ite-form equation); got %d: %q",
			len(dropped), dropped)
	}
	for _, d := range dropped {
		if !strings.Contains(d, "fn_of_seq_Int") && !strings.Contains(d, ":pattern ((fn_to_seq_Int p0))") {
			t.Errorf("a line was dropped that is neither of-seq nor to-seq's patterned "+
				"equation, so the transport preamble is not the core minus those two "+
				"things: %q", d)
		}
	}
	// The two per-constructor equations are what DEFINE `to-seq`. §7.4.1 is
	// explicit that a declared-but-undefined bridge function makes every
	// obligation satisfiable-when-negated for reasons unrelated to the bridge.
	for _, must := range []string{
		"(assert (= (fn_to_seq_Int Nil_List_Int) (as seq.empty (Seq Int))))",
		"(assert (forall ((q0 Int) (q1 List_Int)) (= (fn_to_seq_Int (Cons_List_Int q0 q1))",
	} {
		if !strings.Contains(bridgeTransportCore, must) {
			t.Errorf("the transport preamble has stopped DEFINING to-seq: missing %q", must)
		}
	}
}

// TestBridgeTransportScriptsAreThreeParts pins §7.4.4's concatenation: preamble,
// then the bridged function's declaration block, then the subgoal. A kernel that
// omitted the declaration block would emit a goal mentioning an undeclared
// symbol, which z3 rejects — so this would surface as a solver error rather than
// a wrong answer, and the byte gate would catch it too. It is here because the
// ORDER of the three parts is not otherwise pinned by anything runnable.
func TestBridgeTransportScriptsAreThreeParts(t *testing.T) {
	decls := map[string]string{
		"append": bridgeDeclAppend, "length": bridgeDeclLength,
		"take": bridgeDeclTake, "drop": bridgeDeclDrop,
	}
	for _, o := range bridgeObligations() {
		if !bridgeIsTransport(o.ID) {
			continue
		}
		fn := strings.Split(strings.TrimPrefix(o.ID, "transport-"), "-")[0]
		decl, ok := decls[fn]
		if !ok {
			t.Fatalf("%s: no declaration block known for bridged function %q", o.ID, fn)
		}
		if !strings.HasPrefix(o.Script, bridgeTransportCore+decl) {
			t.Errorf("%s: script is not preamble+declaration+subgoal in that order", o.ID)
		}
		// ...and it is the RIGHT declaration block: exactly one bridged
		// function is declared, so a script cannot quietly carry another's
		// axiom and prove something about a function it does not name.
		for other, od := range decls {
			if other != fn && strings.Contains(o.Script, od) {
				t.Errorf("%s: carries %s's declaration block as well as %s's", o.ID, other, fn)
			}
		}
	}
}

// TestBridgeTakeDropClampIsWrittenOut is the byte-level half of the clamp rule.
// §7.4.4 forbids a GUARDED equation outright, so the thing to check is that no
// take/drop obligation ever states its equation under a hypothesis about the
// index, and that the clamp is present wherever the index is used.
func TestBridgeTakeDropClampIsWrittenOut(t *testing.T) {
	for _, o := range bridgeObligations() {
		if !strings.HasPrefix(o.ID, "transport-take-") && !strings.HasPrefix(o.ID, "transport-drop-") {
			continue
		}
		// A guard would have to constrain the index binder somewhere. The
		// obligations declare b0 and quantify q0 and constrain neither.
		for _, guard := range []string{"(assert (<= 0 b0", "(assert (>= b0", "(assert (and (<= 0",
			"(=> (and (<= 0 q0", "(=> (<= 0 q0"} {
			if strings.Contains(o.Script, guard) {
				t.Errorf("%s: contains %q — this is the GUARDED form §7.4.4 forbids, and "+
					"registering it would license an invalid rewrite at an out-of-range index",
					o.ID, guard)
			}
		}
		// drop uses the clamp twice per formula (offset and length), take once.
		want := 2
		if strings.HasPrefix(o.ID, "transport-drop-") {
			want = 4
		}
		n := strings.Count(o.Script, "(ite (< b0 0) 0 ") + strings.Count(o.Script, "(ite (< q0 0) 0 ")
		if strings.HasSuffix(o.ID, "-base") {
			want /= 2 // no induction hypothesis, so no q0 occurrence
		}
		if n != want {
			t.Errorf("%s: found %d clamp occurrences, want %d", o.ID, n, want)
		}
	}
}

// TestBridgeMeasureIsNotAssertedIntoTheStep is the one structural invariant
// §7.4.3 actually turns on. If the measure fact were asserted as a hypothesis
// inside the step, both subgoals would still report unsat while no longer
// implying the universal — a green result that has stopped meaning anything.
func TestBridgeMeasureIsNotAssertedIntoTheStep(t *testing.T) {
	if strings.Contains(bridgeRT2Step, "(= (seq.len (seq.extract") {
		t.Error("the step subgoal asserts the measure fact; §7.4.3 requires it be a " +
			"SEPARATE obligation, or the scheme's soundness is assumed rather than checked")
	}
}

// bridgeMeasuredOutcome records what each obligation ACTUALLY returns at
// issue-68.md §11.2's pinned budget (4M rlimit) and solver (z3 4.16.0).
//
// TEN ARE `unsat`. ONE IS NOT, AND THAT IS A RECORDED RESULT RATHER THAN A
// TOLERATED FAILURE. `transport-take-step` does not discharge, which is what
// fired §11.3's falsifier for the milestone's transport attempt. The bound
// forbids rescuing it with a larger budget, an added tactic or a supplied
// lemma, so nothing here tries.
//
// AN `unsat` IN THIS TABLE IS A SOLVER RESULT, NOT A §11.3 DISCHARGE, and the
// two must not be read as the same thing. That run reached past §11.3's bound
// (docs/experiments/issue-68-milestone-transport.md, "the two protocol
// violations"), so no transport equation is credited as discharged — including
// the three whose subgoals both come back `unsat` here. What this table is FOR
// is regression: these bytes are fixed, and it re-measures a settled fact so a
// silent change in either kernel or the solver surfaces.
//
// WHY THE TABLE IS TWO-SIDED. The old contract — "every obligation is unsat" —
// pinned three obligations from one side. This pins eleven from both: if
// `transport-take-step` ever starts discharging, this test FAILS and someone
// has to come and change the recorded outcome, which is exactly the event that
// should not pass silently. A skip, or a "known failure" the loop steps over,
// would let a real improvement disappear into a green run.
//
// AND `unknown` IS PINNED SPECIFICALLY, NOT "anything but unsat". A `sat` here
// would mean the transport equation is FALSE — a different and far worse
// finding than not being provable at this budget. Not proved is not disproved,
// and a test that accepted either would erase the distinction.
var bridgeMeasuredOutcome = map[string]string{
	"measure-decreases":     "unsat",
	"roundtrip2-base":       "unsat",
	"roundtrip2-step":       "unsat",
	"transport-append-base": "unsat",
	"transport-append-step": "unsat",
	"transport-length-base": "unsat",
	"transport-length-step": "unsat",
	"transport-take-base":   "unsat",
	"transport-take-step":   "unknown", // fired the falsifier; see docs/experiments/issue-68-milestone-transport.md
	"transport-drop-base":   "unsat",
	"transport-drop-step":   "unsat",
}

// bridgeVerdict maps a solver run to the three words this table uses. Extracted
// as a pure function so the "wall cap is not an outcome" rule has exactly one
// implementation and every caller below gets it.
func bridgeVerdict(out string, capHit bool) string {
	switch {
	case capHit:
		return "INVALID(wall-cap)"
	case strings.HasPrefix(out, "unsat"):
		return "unsat"
	case strings.HasPrefix(out, "sat"):
		return "sat"
	default:
		return "unknown"
	}
}

// TestBridgeObligationsDischarge is the obligation itself, at the budget and
// under the solver issue-68.md §11.2 pins.
func TestBridgeObligationsDischarge(t *testing.T) {
	requireBridgeSolver(t)
	obs := bridgeObligations()
	if len(obs) != len(bridgeMeasuredOutcome) {
		t.Fatalf("the outcome table covers %d obligations but %d are emitted; this test "+
			"did NOT check what it claims", len(bridgeMeasuredOutcome), len(obs))
	}
	for _, o := range obs {
		want, ok := bridgeMeasuredOutcome[o.ID]
		if !ok {
			t.Errorf("%s: no recorded outcome; a new obligation must be MEASURED and "+
				"written into the table, not left unasserted", o.ID)
			continue
		}
		out, capHit := runZ3Budget(o.Script, proveDirectRlimit)
		if capHit {
			// A wall-cap hit is an INVALID attempt, never an outcome
			// (SPEC §7.2, #29). Failing here rather than recording a
			// verdict keeps a loaded machine from looking like a
			// refutation.
			t.Fatalf("%s: wall cap fired — attempt INVALID, no verdict", o.ID)
		}
		if got := bridgeVerdict(out, capHit); got != want {
			t.Errorf("%s: got %q, recorded outcome is %q at rlimit %d under %s.\n"+
				"If this is an IMPROVEMENT (unknown -> unsat), update the table and "+
				"docs/experiments/issue-68-milestone-transport.md — do not widen the "+
				"assertion.", o.ID, got, want, proveDirectRlimit, bridgeSolverPin)
		}
	}
}

// TestBridgeSchemeIsNecessary is the control that stops the two subgoals being
// read as decoration. If the goal discharged WITHOUT the case split and the
// induction hypothesis, the scheme would not be what is doing the work, and
// §7.4.2 would be specifying machinery nothing needs.
func TestBridgeSchemeIsNecessary(t *testing.T) {
	requireBridgeSolver(t)
	naive := bridgeCore + `(declare-const s (Seq Int))
(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))
(check-sat)
`
	out, capHit := runZ3Budget(naive, proveDirectRlimit)
	if capHit {
		t.Skip("wall cap fired; this control is INVALID rather than passing")
	}
	if strings.HasPrefix(out, "unsat") {
		t.Errorf("the round-trip discharged WITHOUT the seq.len scheme (%q) — "+
			"§7.4.2's two subgoals are then unnecessary and the section overstates "+
			"what the milestone had to build", firstLine(out))
	}
}

// TestBridgeStepDiscriminates is the mutation control, and it is the one that
// decides whether TestBridgeObligationsDischarge witnesses anything.
//
// Each mutant breaks the CLAIM while leaving the scheme applicable — the measure
// still decreases in both — so a step subgoal that keeps reporting unsat would be
// passing regardless of what the bridge functions mean.
//
// NOTE the asymmetry, because it is easy to overread: the mutants are expected
// to come back `unknown`, not `sat`. They FAIL TO DISCHARGE rather than being
// refuted. For a gate whose pass condition is unsat that is the discrimination
// that matters, but not-proved is not disproved.
func TestBridgeStepDiscriminates(t *testing.T) {
	requireBridgeSolver(t)
	// A MUTANT MUST BE REWRITTEN CONSISTENTLY OR IT IS NOT A MUTANT, and getting
	// this wrong is how this control first reported a false result. `to-seq` is
	// characterised by TWO assertions — the patterned defining equation and the
	// per-constructor equation — and changing only one leaves them asserting
	// different values for the same term. The core is then CONTRADICTORY, every
	// goal is `unsat` vacuously, and the control reads as "the step discharged
	// against a broken bridge" when in fact it discharged against a broken CONTROL.
	// Hence edits are a list, with the expected replacement count asserted.
	cases := []struct {
		name  string
		edits []struct {
			old, new string
			n        int
		}
	}{
		{
			// of-seq recurses on the init rather than the tail. It has a
			// single defining equation, so one edit is the whole function.
			name: "of-seq takes init not tail",
			edits: []struct {
				old, new string
				n        int
			}{
				{"(fn_of_seq_Int (seq.extract s0 1 (- (seq.len s0) 1)))",
					"(fn_of_seq_Int (seq.extract s0 0 (- (seq.len s0) 1)))", 1},
			},
		},
		{
			// to-seq appends the head at the END. BOTH of its equations
			// must move together.
			name: "to-seq appends head at the end",
			edits: []struct {
				old, new string
				n        int
			}{
				{"(seq.++ (seq.unit (Cons_List_Int_0 p0)) (fn_to_seq_Int (Cons_List_Int_1 p0)))",
					"(seq.++ (fn_to_seq_Int (Cons_List_Int_1 p0)) (seq.unit (Cons_List_Int_0 p0)))", 1},
				{"(seq.++ (seq.unit q0) (fn_to_seq_Int q1))",
					"(seq.++ (fn_to_seq_Int q1) (seq.unit q0))", 1},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			core := bridgeCore
			for _, e := range c.edits {
				if got := strings.Count(core, e.old); got != e.n {
					t.Fatalf("mutation target %q occurs %d times, expected %d; "+
						"this control did NOT run", e.old, got, e.n)
				}
				core = strings.ReplaceAll(core, e.old, e.new)
			}
			if core == bridgeCore {
				t.Fatal("mutation produced an identical core; this control did NOT run")
			}

			// VOID GUARD: a mutant core that is self-contradictory proves
			// EVERYTHING, so an `unsat` step would say nothing about the
			// bridge. If z3 can refute the mutant core on its own, this
			// control is degenerate and must fail loudly rather than be
			// read either way.
			probe, probeCap := runZ3Budget(core+"(check-sat)\n", proveDirectRlimit)
			if probeCap {
				// An invalid probe establishes NOTHING about consistency.
				// Continuing would let the subtest pass on the step's
				// result alone — the void guard quietly not running, which
				// is the very failure it was added to prevent, one level up.
				t.Skip("consistency probe hit the wall cap; this control is INVALID, not passing")
			}
			if strings.HasPrefix(probe, "unsat") {
				t.Fatalf("the mutant core is INCONSISTENT on its own — every goal is " +
					"vacuously unsat, so this control measures nothing. Rewrite the " +
					"mutation so the function's equations stay in agreement.")
			}

			out, capHit := runZ3Budget(core+bridgeRT2Step, proveDirectRlimit)
			if capHit {
				t.Skip("wall cap fired; this control is INVALID rather than passing")
			}
			if strings.HasPrefix(out, "unsat") {
				t.Errorf("the step discharged against a BROKEN bridge (%q) — it is not "+
					"witnessing the round-trip", firstLine(out))
			}
		})
	}
}

// TestBridgeTransportStepDiscriminates is the mutation control for the
// transports, and it is what decides whether their `unsat` results witness
// anything at all.
//
// Each mutant breaks the TRANSPORT CLAIM while leaving `to-seq` intact, so the
// preamble stays consistent and the induction stays applicable. A step that
// keeps reporting unsat against a broken transport is proving something about
// neither the bridged function nor the bridge.
//
// `transport-take-step` is deliberately ABSENT: it does not discharge
// unmutated, so a mutant of it failing to discharge would discriminate nothing.
// Naming that here rather than letting the loop skip it silently, because a
// control that quietly covers three of four cases reports as a control over
// four.
func TestBridgeTransportStepDiscriminates(t *testing.T) {
	requireBridgeSolver(t)
	// A MUTANT MUST STATE ONE FALSE LAW ON BOTH SIDES OF THE STEP, and the
	// first version of this control did not — found by review, not here. A step
	// carries the induction hypothesis and the negated goal, and editing only
	// one leaves a MISMATCHED step: the hypothesis asserts one law and the goal
	// denies a different one. Such a script also fails to discharge, so the
	// control still looked green while witnessing nothing about whether the
	// real step discriminates a broken transport. Hence edits are a LIST with
	// asserted occurrence counts, exactly as the carrier control above learned
	// to do for the same class of mistake.
	type edit struct {
		old, new string
		n        int
	}
	cases := []struct {
		name   string
		script string
		edits  []edit
	}{
		{
			// append transports to the REVERSED concatenation.
			name:   "append transports to a reversed concatenation",
			script: bridgeTransportCore + bridgeDeclAppend + bridgeAppendStep,
			edits: []edit{
				{"(seq.++ (fn_to_seq_Int f1) (fn_to_seq_Int q1))",
					"(seq.++ (fn_to_seq_Int q1) (fn_to_seq_Int f1))", 1},
				{"(seq.++ (fn_to_seq_Int (Cons_List_Int f0 f1)) (fn_to_seq_Int b1))",
					"(seq.++ (fn_to_seq_Int b1) (fn_to_seq_Int (Cons_List_Int f0 f1)))", 1},
			},
		},
		{
			// length transports to TWICE seq.len.
			//
			// NOT `seq.len + 1`, which was tried and is VACUOUS: the step case
			// of that law is VALID — length(Cons h t) = 1 + (len(t)+1) and
			// len(to-seq (Cons h t)) + 1 = (1 + len(t)) + 1 are the same
			// number — so only the BASE case is false, and a mutant whose step
			// still discharges reports the opposite of what it means to.
			// Doubling breaks the step: 1 + 2L versus 2 + 2L.
			name:   "length transports to twice seq.len",
			script: bridgeTransportCore + bridgeDeclLength + bridgeLengthStep,
			edits: []edit{
				{"(seq.len (fn_to_seq_Int f1))",
					"(* 2 (seq.len (fn_to_seq_Int f1)))", 1},
				{"(seq.len (fn_to_seq_Int (Cons_List_Int f0 f1)))",
					"(* 2 (seq.len (fn_to_seq_Int (Cons_List_Int f0 f1))))", 1},
			},
		},
		{
			// drop transports to the PREFIX rather than the suffix: the clamped
			// index becomes the extract's LENGTH and the offset becomes 0,
			// which is take's equation wearing drop's name.
			name:   "drop transports to the prefix instead of the suffix",
			script: bridgeTransportCore + bridgeDeclDrop + bridgeDropStep,
			edits: []edit{
				{"(seq.extract s_tail (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0)) (- (seq.len s_tail) (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0))))",
					"(seq.extract s_tail 0 (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0)))", 1},
				{"(seq.extract s_cons (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0)) (- (seq.len s_cons) (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0))))",
					"(seq.extract s_cons 0 (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0)))", 1},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// EVERY case must edit BOTH sides. Asserted rather than trusted:
			// the defect this control was born with is a case that quietly
			// carries one edit, and a reader counting entries in a literal is
			// exactly who missed it the first time.
			if len(c.edits) != 2 {
				t.Fatalf("a transport mutant must rewrite the hypothesis AND the goal; "+
					"this case has %d edits, so this control did NOT run", len(c.edits))
			}
			mutant := c.script
			for _, e := range c.edits {
				if got := strings.Count(mutant, e.old); got != e.n {
					t.Fatalf("mutation target %q occurs %d times, expected %d; "+
						"this control did NOT run", e.old, got, e.n)
				}
				mutant = strings.Replace(mutant, e.old, e.new, e.n)
			}
			if mutant == c.script {
				t.Fatal("mutation produced an identical script; this control did NOT run")
			}
			// VOID GUARD, same shape as the carrier control's: a mutant whose
			// PREAMBLE is inconsistent proves everything, and an unsat step
			// would then say nothing. Only the preamble plus the declaration
			// block is probed — the subgoal is what is under test.
			pre := mutant[:strings.LastIndex(mutant, "(declare-const")]
			probe, probeCap := runZ3Budget(pre+"(check-sat)\n", proveDirectRlimit)
			if probeCap {
				t.Skip("consistency probe hit the wall cap; this control is INVALID, not passing")
			}
			if strings.HasPrefix(probe, "unsat") {
				t.Fatal("the mutant's axioms are INCONSISTENT on their own — every goal is " +
					"vacuously unsat, so this control measures nothing")
			}
			out, capHit := runZ3Budget(mutant, proveDirectRlimit)
			if capHit {
				t.Skip("wall cap fired; this control is INVALID rather than passing")
			}
			if strings.HasPrefix(out, "unsat") {
				t.Errorf("the step discharged against a BROKEN transport (%q) — it is not "+
					"witnessing the equation", firstLine(out))
			}
		})
	}
}

// TestBridgeClampIsLoadBearing is the two-sided control on the one design
// decision §7.4.4 calls a soundness requirement.
//
// It is a GROUND check at a single point, `drop -1 [1]`, and that is why it is
// worth more than watching the unclamped step go `unknown`: it does not report
// that the unclamped form is hard to prove, it reports that the unclamped form
// is FALSE. Both halves are `unsat` on purpose, of opposite polarities —
//
//	the SPECIFIED (clamped) equation, NEGATED  -> unsat: it holds here
//	the UNSPECIFIED (unclamped) equation, ASSERTED -> unsat: it is refuted here
//
// — so a change that broke the clamp cannot leave both green.
func TestBridgeClampIsLoadBearing(t *testing.T) {
	requireBridgeSolver(t)
	// `drop -1 (Cons 1 Nil)` is `(Cons 1 Nil)` in Oath: drop is total and
	// saturates at a negative count. `seq.extract s -1 (len s + 1)` is the
	// empty sequence, because seq.extract yields empty at a negative offset.
	const xs = "(Cons_List_Int 1 Nil_List_Int)"
	const lhs = "(fn_to_seq_Int (fn_drop_Int (- 1) " + xs + "))"
	const s = "(fn_to_seq_Int " + xs + ")"
	const c = "(ite (< (- 1) 0) 0 (ite (> (- 1) (seq.len " + s + ")) (seq.len " + s + ") (- 1)))"
	clamped := "(= " + lhs + " (seq.extract " + s + " " + c + " (- (seq.len " + s + ") " + c + ")))"
	unclamped := "(= " + lhs + " (seq.extract " + s + " (- 1) (- (seq.len " + s + ") (- 1))))"

	base := bridgeTransportCore + bridgeDeclDrop
	for _, tc := range []struct {
		name, assertion, why string
	}{
		{"clamped equation holds at k=-1", "(assert (not " + clamped + "))",
			"the form §7.4.8 specifies is WRONG at a negative index, which would make " +
				"the whole transport obligation unsound to register"},
		{"unclamped equation is refuted at k=-1", "(assert " + unclamped + ")",
			"the unclamped form is CONSISTENT here, so the clamp is not doing the work " +
				"§7.4.4 says it does and this control is decoration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, capHit := runZ3Budget(base+tc.assertion+"\n(check-sat)\n", proveDirectRlimit)
			if capHit {
				t.Skip("wall cap fired; this control is INVALID rather than passing")
			}
			if got := bridgeVerdict(out, capHit); got != "unsat" {
				t.Errorf("got %q, want unsat — %s", got, tc.why)
			}
		})
	}
}

// TestBridgeSolverPinMatchesFixture stops bridgeSolverPin becoming a second,
// silently-drifting statement of which solver this corpus is pinned to.
// `fixtures/prove/outcomes.json` is the authority — conformance.sh checks the
// whole corpus against it — and the constant exists only because the CLI is
// deliberately file-independent. If this fails, fix the CONSTANT.
func TestBridgeSolverPinMatchesFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "fixtures", "prove", "outcomes.json"))
	if err != nil {
		t.Fatalf("cannot read the pinned-solver authority: %v", err)
	}
	var doc struct {
		Solver string `json:"solver"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("cannot parse outcomes.json: %v", err)
	}
	if doc.Solver == "" {
		t.Fatal("outcomes.json has no `solver` field; this check did NOT run")
	}
	if doc.Solver != bridgeSolverPin {
		t.Errorf("bridgeSolverPin = %q but fixtures/prove/outcomes.json pins %q — "+
			"the duplicate has drifted from its authority", bridgeSolverPin, doc.Solver)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
