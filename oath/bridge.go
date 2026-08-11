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
// (SPEC §7.4) and in a fixture: an emitter that does not share `Cx`'s
// declaration discipline cannot INHERIT byte-agreement with a second kernel, so
// the agreement has to be specified and then checked. That is the same trade
// §7.2 already makes for the ordinary path — it writes the naming and ordering
// rules down rather than declaring the implementation authoritative.
//
// SCOPE. The two carrier round-trips' second half, plus §11.2's four transport
// equations (append/length/take/drop) at `List Int`. §11.3's 14 rotation laws
// are NOT here, and nothing in this file registers a §9.4 bridge-registry
// entry.
//
// THE MILESTONE'S TRANSPORT ATTEMPT WAS RUN AGAINST THIS GENERATOR AND §11.3'S
// FALSIFIER FIRED. `transport-take-step` did not discharge at the pinned budget,
// and the run additionally reached past §11.3's bound, so no transport equation
// is credited as discharged — the three that returned `unsat` included.
// `docs/experiments/issue-68-milestone-transport.md` is the record; read it
// before quoting any result from `--prove`, because the solver output alone
// reads like three passes and one hard case, which is not what happened.
//
// NONE OF THAT IMPUGNS THE BYTES. SPEC §7.4 fixes what the obligations ARE and
// deliberately fixes no budget; a second kernel reproduces every digest here
// from the document alone. The verdict was spent; the artefact was not.

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

// bridgeCore is the CARRIER family's preamble (SPEC §7.4.1): the `List Int`
// datatype as this kernel spells it, then `to-seq` and `of-seq` as DEFINED
// total functions. The TRANSPORT family carries bridgeTransportCore instead.
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

// bridgeTransportCore is the TRANSPORT family's preamble (SPEC §7.4.4).
//
// It is bridgeCore minus `of-seq`, which no transport goal mentions, and minus
// the patterned `ite`-form defining equation for `to-seq`. THAT SECOND OMISSION
// IS THE ONE WORTH UNDERSTANDING, because it looks like a weakening and is not.
//
// The `ite`-form equation lets `to-seq` unfold at an argument that is not
// syntactically a constructor application. Exactly one thing in §7.4 produces
// such an argument: the round-trips apply `to-seq` to `(fn_of_seq_Int s)`. A
// transport goal never does — every `to-seq` argument it builds is a
// constructor application or a bridged-function result that unfolds to one — so
// the equation is a consequence of the two per-constructor equations that the
// goal cannot use, while its trigger `(fn_to_seq_Int p0)` matches every
// `to-seq` term the goal DOES build. Same discipline as §7.2's relevance
// filtering, which likewise admits only what a goal's footprint reaches.
//
// `to-seq` is still DEFINED and not merely declared, which is the soundness
// requirement §7.4.1 states: the two per-constructor equations pin it uniquely
// on the datatype's finite elements. Dropping a redundant consequence of a
// definition is not dropping the definition.
const bridgeTransportCore = `(declare-datatypes ((List_Int 0)) (((Nil_List_Int ) (Cons_List_Int (Cons_List_Int_0 Int) (Cons_List_Int_1 List_Int)))))
(declare-fun fn_to_seq_Int (List_Int) (Seq Int))
(assert (= (fn_to_seq_Int Nil_List_Int) (as seq.empty (Seq Int))))
(assert (forall ((q0 Int) (q1 List_Int)) (= (fn_to_seq_Int (Cons_List_Int q0 q1)) (seq.++ (seq.unit q0) (fn_to_seq_Int q1)))))
`

// The four bridged functions' declaration blocks.
//
// THESE ARE NOT HAND-WRITTEN FROM THE OATH SOURCE. Each was transcribed from
// what this kernel's own §7.2 translation emits for the corpus definition —
// `directAttemptScript` on `append`, `length`, `take`, `drop` — so a transport
// obligation talks about the same function the prover talks about, including
// the `:pattern` and the `ite` nesting. A hand-derived spelling that happened
// to be semantically equivalent would make the obligation a claim about a
// function nothing else in the kernel uses.
const (
	bridgeDeclAppend = `(declare-fun fn_append_Int (List_Int List_Int) List_Int)
(assert (forall ((p0 List_Int) (p1 List_Int)) (! (= (fn_append_Int p0 p1) (ite ((_ is Nil_List_Int) p0) p1 (Cons_List_Int (Cons_List_Int_0 p0) (fn_append_Int (Cons_List_Int_1 p0) p1)))) :pattern ((fn_append_Int p0 p1)))))
`
	bridgeDeclLength = `(declare-fun fn_length_Int (List_Int) Int)
(assert (forall ((p0 List_Int)) (! (= (fn_length_Int p0) (ite ((_ is Nil_List_Int) p0) 0 (+ 1 (fn_length_Int (Cons_List_Int_1 p0))))) :pattern ((fn_length_Int p0)))))
`
	bridgeDeclTake = `(declare-fun fn_take_Int (Int List_Int) List_Int)
(assert (forall ((p0 Int) (p1 List_Int)) (! (= (fn_take_Int p0 p1) (ite (<= p0 0) Nil_List_Int (ite ((_ is Nil_List_Int) p1) Nil_List_Int (Cons_List_Int (Cons_List_Int_0 p1) (fn_take_Int (- p0 1) (Cons_List_Int_1 p1)))))) :pattern ((fn_take_Int p0 p1)))))
`
	bridgeDeclDrop = `(declare-fun fn_drop_Int (Int List_Int) List_Int)
(assert (forall ((p0 Int) (p1 List_Int)) (! (= (fn_drop_Int p0 p1) (ite (<= p0 0) p1 (ite ((_ is Nil_List_Int) p1) Nil_List_Int (fn_drop_Int (- p0 1) (Cons_List_Int_1 p1))))) :pattern ((fn_drop_Int p0 p1)))))
`
)

// The eight transport subgoals: one base and one step per bridged function,
// structural induction over the cons-list per SPEC §7.4.4.
//
// Binder naming is §7.2's and carries no new vocabulary: `b<i>` for the
// obligation's own binders — ALL of them, including the one the induction
// replaces, which is why `b0` is declared and unused in every base and step;
// `f<i>` for the constructor's fields; `q<i>` for the binders the induction
// hypothesis generalizes, at the index they have in the obligation.
const (
	bridgeAppendBase = `(declare-const b0 List_Int)
(declare-const b1 List_Int)
(assert (not (= (fn_to_seq_Int (fn_append_Int Nil_List_Int b1)) (seq.++ (fn_to_seq_Int Nil_List_Int) (fn_to_seq_Int b1)))))
(check-sat)
`
	bridgeAppendStep = `(declare-const b0 List_Int)
(declare-const b1 List_Int)
(declare-const f0 Int)
(declare-const f1 List_Int)
(assert (forall ((q1 List_Int)) (= (fn_to_seq_Int (fn_append_Int f1 q1)) (seq.++ (fn_to_seq_Int f1) (fn_to_seq_Int q1)))))
(assert (not (= (fn_to_seq_Int (fn_append_Int (Cons_List_Int f0 f1) b1)) (seq.++ (fn_to_seq_Int (Cons_List_Int f0 f1)) (fn_to_seq_Int b1)))))
(check-sat)
`
	// length has ONE binder, so its induction hypothesis generalizes nothing
	// and carries no quantifier. That is the general rule applied, not a
	// special case — and it is the reason this obligation lands in `Int`
	// rather than in a sequence.
	bridgeLengthBase = `(declare-const b0 List_Int)
(assert (not (= (fn_length_Int Nil_List_Int) (seq.len (fn_to_seq_Int Nil_List_Int)))))
(check-sat)
`
	bridgeLengthStep = `(declare-const b0 List_Int)
(declare-const f0 Int)
(declare-const f1 List_Int)
(assert (= (fn_length_Int f1) (seq.len (fn_to_seq_Int f1))))
(assert (not (= (fn_length_Int (Cons_List_Int f0 f1)) (seq.len (fn_to_seq_Int (Cons_List_Int f0 f1))))))
(check-sat)
`
	// take/drop: `k` is take's and drop's FIRST Oath parameter, so it is b0
	// and the list is b1. The index is CLAMPED at every occurrence —
	//     c = (ite (< k 0) 0 (ite (> k (seq.len s)) (seq.len s) k))
	// — because take/drop are total in Oath at every k (take -1 xs = Nil,
	// drop -1 xs = xs, both saturating above length) and `seq.extract` is not
	// total that way at a negative offset. A GUARDED equation is the
	// alternative and is not available: an obligation is a global fact about
	// the bridged function, so a guarded one would license an invalid rewrite
	// wherever an out-of-range index is passed. NEVER emit or register the
	// guarded form.
	//
	// `s_nil`/`s_tail`/`s_cons` name the sequence where it appears more than
	// once in one formula. The induction hypotheses write the same expressions
	// longhand because a `define-fun` cannot mention a bound variable — forced,
	// not stylistic, and the same asymmetry §7.4.4 records.
	bridgeTakeBase = `(declare-const b0 Int)
(declare-const b1 List_Int)
(define-fun s_nil () (Seq Int) (fn_to_seq_Int Nil_List_Int))
(assert (not (= (fn_to_seq_Int (fn_take_Int b0 Nil_List_Int)) (seq.extract s_nil 0 (ite (< b0 0) 0 (ite (> b0 (seq.len s_nil)) (seq.len s_nil) b0))))))
(check-sat)
`
	bridgeTakeStep = `(declare-const b0 Int)
(declare-const b1 List_Int)
(declare-const f0 Int)
(declare-const f1 List_Int)
(define-fun s_tail () (Seq Int) (fn_to_seq_Int f1))
(define-fun s_cons () (Seq Int) (fn_to_seq_Int (Cons_List_Int f0 f1)))
(assert (forall ((q0 Int)) (= (fn_to_seq_Int (fn_take_Int q0 f1)) (seq.extract s_tail 0 (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0))))))
(assert (not (= (fn_to_seq_Int (fn_take_Int b0 (Cons_List_Int f0 f1))) (seq.extract s_cons 0 (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0))))))
(check-sat)
`
	bridgeDropBase = `(declare-const b0 Int)
(declare-const b1 List_Int)
(define-fun s_nil () (Seq Int) (fn_to_seq_Int Nil_List_Int))
(assert (not (= (fn_to_seq_Int (fn_drop_Int b0 Nil_List_Int)) (seq.extract s_nil (ite (< b0 0) 0 (ite (> b0 (seq.len s_nil)) (seq.len s_nil) b0)) (- (seq.len s_nil) (ite (< b0 0) 0 (ite (> b0 (seq.len s_nil)) (seq.len s_nil) b0)))))))
(check-sat)
`
	bridgeDropStep = `(declare-const b0 Int)
(declare-const b1 List_Int)
(declare-const f0 Int)
(declare-const f1 List_Int)
(define-fun s_tail () (Seq Int) (fn_to_seq_Int f1))
(define-fun s_cons () (Seq Int) (fn_to_seq_Int (Cons_List_Int f0 f1)))
(assert (forall ((q0 Int)) (= (fn_to_seq_Int (fn_drop_Int q0 f1)) (seq.extract s_tail (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0)) (- (seq.len s_tail) (ite (< q0 0) 0 (ite (> q0 (seq.len s_tail)) (seq.len s_tail) q0)))))))
(assert (not (= (fn_to_seq_Int (fn_drop_Int b0 (Cons_List_Int f0 f1))) (seq.extract s_cons (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0)) (- (seq.len s_cons) (ite (< b0 0) 0 (ite (> b0 (seq.len s_cons)) (seq.len s_cons) b0)))))))
(check-sat)
`
)

// bridgeObligation is one emitted obligation: a stable id and the script bytes.
//
// The id is part of the interchange surface — a second kernel has to agree on
// WHICH script a hash belongs to, not merely on a set of hashes — so ids are
// fixed strings here and in SPEC §7.4, never derived from a loop index.
type bridgeObligation struct {
	ID     string
	Script string
}

// bridgeObligations returns the emitted obligations in a FIXED order.
//
// Order is part of the fixture's bytes, so it is normative (SPEC §7.4.9). It
// runs scheme-soundness first, then the carrier base and step, which is also
// the order in which a failure should be read: a failing measure invalidates
// the other two as a proof of the universal, so reporting it first stops a
// reader concluding anything from two greens above a red. The transports follow
// — they do not depend on the scheme, but a `sat` over this encoding is only
// meaningful once the carriers hold, so the carriers are read first.
//
// A CARRIER script is core+subgoal; a TRANSPORT script is transport-core +
// the bridged function's declaration block + subgoal. Both are "the complete
// script"; only what that concatenates differs.
func bridgeObligations() []bridgeObligation {
	tr := func(decl, subgoal string) string { return bridgeTransportCore + decl + subgoal }
	return []bridgeObligation{
		{"measure-decreases", bridgeCore + bridgeMeasureDecreases},
		{"roundtrip2-base", bridgeCore + bridgeRT2Base},
		{"roundtrip2-step", bridgeCore + bridgeRT2Step},
		{"transport-append-base", tr(bridgeDeclAppend, bridgeAppendBase)},
		{"transport-append-step", tr(bridgeDeclAppend, bridgeAppendStep)},
		{"transport-length-base", tr(bridgeDeclLength, bridgeLengthBase)},
		{"transport-length-step", tr(bridgeDeclLength, bridgeLengthStep)},
		{"transport-take-base", tr(bridgeDeclTake, bridgeTakeBase)},
		{"transport-take-step", tr(bridgeDeclTake, bridgeTakeStep)},
		{"transport-drop-base", tr(bridgeDeclDrop, bridgeDropBase)},
		{"transport-drop-step", tr(bridgeDeclDrop, bridgeDropStep)},
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
	// STATE THE BUDGET AND THE SOLVER IN THE OUTPUT. §7.4.4 says an outcome
	// quoted without its rlimit and solver version is not comparable to
	// anything; a command that printed bare verdicts would make this kernel the
	// first violator of the rule its own specification states.
	fmt.Printf("# solver=%s rlimit=%d\n", bridgeSolverPin, proveDirectRlimit)
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
