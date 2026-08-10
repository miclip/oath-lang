package main

// Bridge obligations (#68 NARROW milestone, issue-68.md §11.2/§11.3).
//
// WHAT THIS IS. §11.3 requires the milestone to BUILD THE GENERATOR that emits
// the datatype<->Seq bridge obligations, rather than to hand-write them: a
// hand-run script demonstrates that a proof exists, and the milestone asks for a
// kernel that PRODUCES the obligation. This file is that generator for ONE
// obligation, the second carrier round-trip.
//
// WHY IT DOES NOT REUSE THE ORDINARY TRANSLATION PATH. Every other script this
// kernel emits comes from translating an Oath term: `Cx` accumulates
// declarations by first touch while walking a `Def` body, which is what SPEC
// §7.2's script-stability rules govern. `to-seq` and `of-seq` are NOT Oath
// terms. `Seq` is an SMT sort with no Oath spelling, so — as §11.3 puts it — the
// obligation "lives BELOW the property language" and there is no body to walk.
// Routing it through `Cx` would mean inventing a fake `Def` whose translation
// happened to produce these bytes, which is more machinery and less honest than
// emitting them directly.
//
// The cost of that choice is real and is why the bytes are pinned normatively
// (SPEC §7.3) and in a fixture: an emitter that does not share `Cx`'s
// declaration discipline cannot INHERIT byte-agreement with a second kernel, so
// the agreement has to be specified and then checked. That is the same trade
// §7.2 already makes for the ordinary path — it writes the naming and ordering
// rules down rather than declaring the implementation authoritative.
//
// SCOPE. Deliberately one obligation. §11.2's four transport equations
// (append/take/drop/length) and §11.3's 14 rotation laws are NOT here, and
// nothing in this file registers a §9.4 bridge-registry entry. Proving this
// obligation is not the milestone passing; see issue-68.md §11.3.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// bridgeSolverPin is the solver issue-68.md §11.2 pins for these obligations.
//
// THE AUTHORITY IS `fixtures/prove/outcomes.json`'s `solver` field, not this
// constant — that is what `conformance.sh` checks the corpus against, and two
// independently maintained spellings of one fact are exactly what this
// repository keeps having to un-duplicate. The copy exists only because this
// command is deliberately file-independent (see the early dispatch in main.go),
// and it is GATED rather than trusted: TestBridgeSolverPinMatchesFixture fails
// if it ever drifts from the fixture.
const bridgeSolverPin = "Z3 version 4.16.0 - 64 bit"

// bridgeCore is the shared preamble: the `List Int` datatype as this kernel
// spells it, then `to-seq` and `of-seq` as DEFINED total functions.
//
// The two function encodings are deliberately different shapes, and the
// difference is forced by §11.3 rather than chosen:
//
//   - `to-seq` recurses structurally on the datatype, so it gets the same
//     treatment an ordinary total Oath function gets: a patterned defining
//     equation plus one equation per constructor. Those constructor equations
//     are what pin it uniquely on the datatype's finite elements, which is what
//     makes the definitional extension conservative.
//   - `of-seq` recurses on `seq.len`, a measure over the SEQUENCE sort. It has
//     no constructors to case on, so it carries the defining equation alone.
//     Its uniqueness comes from well-founded recursion on that measure, which
//     is why `bridgeMeasureDecreases` below is an obligation and not a comment.
//
// §11.3 is explicit that neither may be left uninterpreted: a declared-but-
// undefined `of-seq` is an arbitrary function, both round-trips are then
// satisfiable-when-negated, and the gate would return DECLINE for every
// possible encoding — measuring the obligation's own defect instead of the
// bridge.
//
// The `as` annotation on `seq.empty` is required, not decoration: z3 rejects the
// bare form as sort-ambiguous (issue-68.md §9.7b, checked on 4.16.0).
const bridgeCore = `(declare-datatypes ((List_Int 0)) (((Nil_List_Int ) (Cons_List_Int (Cons_List_Int_0 Int) (Cons_List_Int_1 List_Int)))))
(declare-fun fn_to_seq_Int (List_Int) (Seq Int))
(assert (forall ((p0 List_Int)) (! (= (fn_to_seq_Int p0) (ite ((_ is Nil_List_Int) p0) (as seq.empty (Seq Int)) (seq.++ (seq.unit (Cons_List_Int_0 p0)) (fn_to_seq_Int (Cons_List_Int_1 p0))))) :pattern ((fn_to_seq_Int p0)))))
(assert (= (fn_to_seq_Int Nil_List_Int) (as seq.empty (Seq Int))))
(assert (forall ((q0 Int) (q1 List_Int)) (= (fn_to_seq_Int (Cons_List_Int q0 q1)) (seq.++ (seq.unit q0) (fn_to_seq_Int q1)))))
(declare-fun fn_of_seq_Int ((Seq Int)) List_Int)
(assert (forall ((s0 (Seq Int))) (! (= (fn_of_seq_Int s0) (ite (= (seq.len s0) 0) Nil_List_Int (Cons_List_Int (seq.nth s0 0) (fn_of_seq_Int (seq.extract s0 1 (- (seq.len s0) 1)))))) :pattern ((fn_of_seq_Int s0)))))
`

// The `seq.len` induction scheme, as two subgoals.
//
// This is the scheme §11.3 names as the one exemption to its no-further-tactic
// bound — the milestone is allowed to build exactly this and nothing else. It is
// the sequence-sorted analogue of §7.2's recursion induction: induct along the
// function's OWN recursion, with the induction hypothesis at the argument the
// recursive call is made on.
//
//	BASE  seq.len s = 0                        |- to-seq(of-seq s) = s
//	STEP  seq.len s > 0, IH at the tail        |- to-seq(of-seq s) = s
//
// Both `unsat` on the negation give the universal, PROVIDED the measure strictly
// decreases — which is a side condition on the scheme's soundness, not on this
// goal, and is discharged separately by bridgeMeasureDecreases.
const bridgeRT2Base = `(declare-const s (Seq Int))
(assert (= (seq.len s) 0))
(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))
(check-sat)
`

const bridgeRT2Step = `(declare-const s (Seq Int))
(assert (> (seq.len s) 0))
(define-fun ih_tail () (Seq Int) (seq.extract s 1 (- (seq.len s) 1)))
(assert (= (fn_to_seq_Int (fn_of_seq_Int ih_tail)) ih_tail))
(assert (not (= (fn_to_seq_Int (fn_of_seq_Int s)) s)))
(check-sat)
`

// bridgeMeasureDecreases witnesses that the scheme above is well-founded.
//
// It is emitted as an obligation rather than asserted as a lemma on purpose. If
// it were asserted into the step subgoal it would be a hand-supplied lemma,
// which §11.3's bound forbids; as a separate obligation it is a check on the
// SCHEME, and the scheme is what the exemption covers. If this ever fails, the
// two subgoals above stop implying the universal even if both still say `unsat`.
const bridgeMeasureDecreases = `(declare-const s (Seq Int))
(assert (> (seq.len s) 0))
(assert (not (= (seq.len (seq.extract s 1 (- (seq.len s) 1))) (- (seq.len s) 1))))
(check-sat)
`

// bridgeObligation is one emitted obligation: a stable id and the script bytes.
//
// The id is part of the interchange surface — a second kernel has to agree on
// WHICH script a hash belongs to, not merely on a set of hashes — so ids are
// fixed strings here and in SPEC §7.3, never derived from a loop index.
type bridgeObligation struct {
	ID     string
	Script string
}

// bridgeObligations returns the emitted obligations in a FIXED order.
//
// Order is part of the fixture's bytes, so it is normative (SPEC §7.3). It runs
// scheme-soundness first, then base, then step, which is also the order in which
// a failure should be read: a failing measure invalidates the other two as a
// proof of the universal, so reporting it first stops a reader concluding
// anything from two greens above a red.
func bridgeObligations() []bridgeObligation {
	return []bridgeObligation{
		{"measure-decreases", bridgeCore + bridgeMeasureDecreases},
		{"roundtrip2-base", bridgeCore + bridgeRT2Base},
		{"roundtrip2-step", bridgeCore + bridgeRT2Step},
	}
}

// bridgeManifest is the cross-kernel comparison surface: one `id<TAB>sha256`
// line per obligation, in emission order.
//
// It hashes the CORE SCRIPT ONLY — no rlimit header, no trailing get-info lines
// — matching how SPEC §7.2 and `prove/scripts.txt` already treat a script's
// identity. Runner options are outside the hashed bytes precisely so the same
// goal at any budget hashes the same, and a bridge obligation is not special.
func bridgeManifest() string {
	var b strings.Builder
	b.WriteString("# id\tsha256(script)\n")
	for _, o := range bridgeObligations() {
		sum := sha256.Sum256([]byte(o.Script))
		fmt.Fprintf(&b, "%s\t%s\n", o.ID, hex.EncodeToString(sum[:]))
	}
	return b.String()
}

// cmdBridgeObligation is the CLI surface. `--emit <id>` writes one script's
// exact bytes to stdout (what a second kernel is compared against); with no
// arguments it writes the manifest; `--prove` runs each obligation at the
// budget §11.2 pins and reports the verdict.
func cmdBridgeObligation(args []string) {
	prove, emit := false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--prove":
			prove = true
		case "--emit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "bridge-obligation: --emit needs an id")
				os.Exit(2)
			}
			emit = args[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "bridge-obligation: unknown argument %q\n", args[i])
			os.Exit(2)
		}
	}
	// --prove and --emit are mutually exclusive, and the combination is
	// REFUSED rather than resolved by precedence. An earlier draft let --emit
	// win, so `--prove --emit <id>` printed a script and exited 0 with no
	// solver ever run — automation reading that exit code would record a
	// discharge that never happened. Refusing costs a keystroke; the silent
	// reading costs a false green.
	if prove && emit != "" {
		fmt.Fprintln(os.Stderr, "bridge-obligation: --prove and --emit are mutually exclusive")
		os.Exit(2)
	}
	if emit != "" {
		for _, o := range bridgeObligations() {
			if o.ID == emit {
				fmt.Print(o.Script)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "bridge-obligation: no such obligation %q\n", emit)
		os.Exit(2)
	}
	if !prove {
		fmt.Print(bridgeManifest())
		return
	}
	// THE SOLVER PIN IS PART OF THE CLAIM, NOT THE ENVIRONMENT. issue-68.md
	// §11.2 pins the budget AND z3 4.16.0, because §7.2 records an outcome as a
	// function of (script bytes, solver version, rlimit) — a discharge under a
	// different z3 answers a different question, and §11.2 says an unavailable
	// pinned solver is "an amendment naming the new version, not a silent
	// substitution". So this refuses rather than proving under whatever is on
	// PATH: reporting `unsat` from an unpinned solver is precisely the false
	// green the pin exists to prevent.
	out, err := exec.Command("z3", "--version").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bridge-obligation: --prove needs %q on PATH; z3 not runnable: %v\n",
			bridgeSolverPin, err)
		os.Exit(2)
	}
	if got := strings.TrimSpace(string(out)); got != bridgeSolverPin {
		fmt.Fprintf(os.Stderr,
			"bridge-obligation: solver is %q but this obligation is pinned to %q.\n"+
				"Refusing: an outcome under a different solver is not comparable, and\n"+
				"substituting one silently is what issue-68.md §11.2 forbids.\n", got, bridgeSolverPin)
		os.Exit(2)
	}
	// §11.2 pins the budget AND the solver. proveDirectRlimit is that 4M
	// budget; the solver pin is checked by the caller's environment, not
	// here, because this kernel does not choose which z3 is on PATH.
	bad := false
	for _, o := range bridgeObligations() {
		out, capHit := runZ3Budget(o.Script, proveDirectRlimit)
		verdict := "unknown"
		switch {
		case capHit:
			verdict = "INVALID(wall-cap)" // never an outcome, SPEC §7.2/#29
		case strings.HasPrefix(out, "unsat"):
			verdict = "unsat"
		case strings.HasPrefix(out, "sat"):
			verdict = "sat"
		}
		if verdict != "unsat" {
			bad = true
		}
		fmt.Printf("%s\t%s\n", o.ID, verdict)
	}
	if bad {
		// A non-unsat obligation is a FAILURE of the obligation, and the
		// exit code has to say so: a gate that prints a verdict and exits
		// 0 is read as green by every caller that checks status.
		os.Exit(1)
	}
}
