package main

import (
	"encoding/json"
	"os"
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

// TestBridgeObligationsAreDistinct guards the cheapest way this could rot into
// nonsense: three obligations that are secretly the same script. Every later
// test would still pass.
func TestBridgeObligationsAreDistinct(t *testing.T) {
	obs := bridgeObligations()
	if len(obs) != 3 {
		t.Fatalf("expected 3 obligations, got %d", len(obs))
	}
	want := []string{"measure-decreases", "roundtrip2-base", "roundtrip2-step"}
	seen := map[string]bool{}
	for i, o := range obs {
		if o.ID != want[i] {
			t.Errorf("obligation %d: id = %q, want %q (SPEC §7.4.4 fixes the order)", i, o.ID, want[i])
		}
		if seen[o.Script] {
			t.Errorf("obligation %q duplicates an earlier script", o.ID)
		}
		seen[o.Script] = true
		if !strings.HasPrefix(o.Script, bridgeCore) {
			t.Errorf("obligation %q does not begin with the §7.4.1 core", o.ID)
		}
		if !strings.HasSuffix(o.Script, "(check-sat)\n") {
			t.Errorf("obligation %q does not end with (check-sat) and one LF", o.ID)
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

// TestBridgeObligationsDischarge is the obligation itself, at the budget and
// under the solver issue-68.md §11.2 pins.
func TestBridgeObligationsDischarge(t *testing.T) {
	requireZ3(t)
	for _, o := range bridgeObligations() {
		out, capHit := runZ3Budget(o.Script, proveDirectRlimit)
		if capHit {
			// A wall-cap hit is an INVALID attempt, never an outcome
			// (SPEC §7.2, #29). Failing here rather than recording a
			// verdict keeps a loaded machine from looking like a
			// refutation.
			t.Fatalf("%s: wall cap fired — attempt INVALID, no verdict", o.ID)
		}
		if !strings.HasPrefix(out, "unsat") {
			t.Errorf("%s: got %q, want unsat at rlimit %d", o.ID, firstLine(out), proveDirectRlimit)
		}
	}
}

// TestBridgeSchemeIsNecessary is the control that stops the two subgoals being
// read as decoration. If the goal discharged WITHOUT the case split and the
// induction hypothesis, the scheme would not be what is doing the work, and
// §7.4.2 would be specifying machinery nothing needs.
func TestBridgeSchemeIsNecessary(t *testing.T) {
	requireZ3(t)
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
	requireZ3(t)
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
