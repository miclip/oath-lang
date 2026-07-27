package main

import (
	"strings"
	"testing"
)

// #72: attempt validity is PER PROPERTY. An environmentally aborted goal yields
// no verdict — but that is a fact about THAT property, not its siblings. Before
// this, one aborted property invalidated the whole run and nothing was recorded,
// which made a definition with a single intractable property permanently
// unrecordable (the rot* arms, 28 properties, were stuck exactly this way).
func TestAbortedPropertyDoesNotBlockSiblings(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn sibs [] [(x Int)] Int (if (< x 0) (neg x) x)
		(prop a-provable [(x Int)] (<= 0 (sibs x)))
		(prop b-aborts [(x Int)] (== (sibs x) (sibs x)))
		(prop c-provable [(x Int)] (== (sibs (sibs x)) (sibs x))))`)
	t.Setenv("OATH_PROVE_FORCE_ABORT", "b-aborts")

	out, err := apiProve(st, "sibs")
	if err != nil {
		t.Fatalf("run must SUCCEED with partial results, got: %v", err)
	}
	if !strings.Contains(out, "⚠ aborted") || !strings.Contains(out, "b-aborts") {
		t.Fatalf("aborted property not reported distinctly:\n%s", out)
	}
	// It must NOT be reported as unproven — a different claim.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "b-aborts") && strings.Contains(line, "unproven") {
			t.Fatalf("aborted property reported as unproven: %q", line)
		}
	}

	h, _ := st.Resolve("sibs")
	m, _ := st.GetMeta(h)
	proven := map[int]bool{}
	for _, pi := range m.ProvenProps {
		proven[pi] = true
	}
	if !proven[0] || !proven[2] {
		t.Fatalf("siblings with valid attempts were not recorded: proven=%v\n%s", m.ProvenProps, out)
	}
	if proven[1] {
		t.Fatalf("aborted property was recorded as proven: %v", m.ProvenProps)
	}
}

// The safety-critical half: an aborted property must never be DEMOTED. Recording
// "unproven" for it would turn an environmental abort into a verdict — precisely
// what §7.2 exists to prevent. A prior PROVEN must survive the run untouched.
func TestAbortedPropertyRetainsPriorProof(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn keeper [] [(x Int)] Int (if (< x 0) (neg x) x)
		(prop p-one [(x Int)] (<= 0 (keeper x)))
		(prop p-two [(x Int)] (== (keeper (keeper x)) (keeper x))))`)

	// A clean run proves both.
	if _, err := apiProve(st, "keeper"); err != nil {
		t.Fatal(err)
	}
	h, _ := st.Resolve("keeper")
	m, _ := st.GetMeta(h)
	if len(m.ProvenProps) != 2 {
		t.Fatalf("baseline: want 2 proven, got %v", m.ProvenProps)
	}

	// Now abort one of them and re-run: its proof must SURVIVE.
	t.Setenv("OATH_PROVE_FORCE_ABORT", "p-two")
	out, err := apiProve(st, "keeper")
	if err != nil {
		t.Fatalf("run must succeed: %v", err)
	}
	m2, _ := st.GetMeta(h)
	got := map[int]bool{}
	for _, pi := range m2.ProvenProps {
		got[pi] = true
	}
	if !got[1] {
		t.Fatalf("DEMOTED a proof on environmental grounds — proven=%v\n%s", m2.ProvenProps, out)
	}
	if !strings.Contains(out, "prior PROVEN retained") {
		t.Fatalf("retention not reported:\n%s", out)
	}
}

// SPEC §7.2 STANDING VERDICT (#72, DIVERGENCES 83a): the carry-forward reads the
// proven set as it stands at the start of the current round — INCLUDING proofs
// this run derived earlier — not a snapshot from before the run. A property
// proven in round 0 and aborted in a later round must keep its proof; losing it
// would be the same environmental demotion the rule forbids. The blind Rust
// kernel read the rule this way and the reference did not, which is how the
// ambiguity surfaced; it is now pinned normatively and tested on both sides.
func TestStandingVerdictSurvivesLaterRoundAbort(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	// Two properties: proving `base` gives `derived` a sibling lemma, so the
	// fixpoint needs more than one round and there IS a later round to abort in.
	put(t, st, `(defn rounds [] [(x Int)] Int (if (< x 0) (neg x) x)
		(prop base [(x Int)] (<= 0 (rounds x)))
		(prop derived [(x Int)] (== (rounds (rounds x)) (rounds x))))`)

	// Clean run: both prove, so both have standing verdicts in the store.
	if _, err := apiProve(st, "rounds"); err != nil {
		t.Fatal(err)
	}
	h, _ := st.Resolve("rounds")
	if m, _ := st.GetMeta(h); len(m.ProvenProps) != 2 {
		t.Fatalf("baseline: want both proven, got %v", m.ProvenProps)
	}

	// Abort BOTH: each has a standing verdict, so both must be retained. If the
	// carry-forward were dropped the definition would silently lose 2 proofs.
	t.Setenv("OATH_PROVE_FORCE_ABORT", "base,derived")
	out, err := apiProve(st, "rounds")
	if err != nil {
		t.Fatalf("run must succeed: %v", err)
	}
	m2, _ := st.GetMeta(h)
	if len(m2.ProvenProps) != 2 {
		t.Fatalf("standing verdicts lost on abort: proven=%v\n%s", m2.ProvenProps, out)
	}
	if strings.Count(out, "prior PROVEN retained") != 2 {
		t.Fatalf("retention not reported for both:\n%s", out)
	}
}

// A valid `unsat` SUPERSEDES an earlier abort on the same property within one
// round: positive evidence no environment can fake beats an attempt that
// produced none. Without the supersede, the report calls a proven property
// "aborted" and — because the abort branch is checked first — a newly proven one
// is dropped from the recorded set entirely.
//
// This case was previously flagged as UNTESTED (#72): the always-abort hook can
// never let the property succeed afterwards, so the path was unreachable.
// OATH_PROVE_FORCE_ABORT_ONCE spends the abort on the first attempt only, which
// makes the abort-then-prove sequence reachable deterministically — no timing.
func TestProofSupersedesEarlierAbort(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn supersede [] [(x Int)] Int (if (< x 0) (neg x) x)
		(prop sup-target [(x Int)] (<= 0 (supersede x)))
		(prop sup-other [(x Int)] (== (supersede (supersede x)) (supersede x))))`)
	// Prove cleanly FIRST so the store already records both. The re-run's round 0
	// then reproduces exactly that set, the fixpoint is stable immediately, and
	// round 0 IS the final round — so the abort flag it raises is the one that
	// reaches the report and the recorded set. Without this the once-abort is
	// spent in round 0 and a later clean round masks the bug entirely.
	if _, err := apiProve(st, "supersede"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OATH_PROVE_FORCE_ABORT_ONCE", "sup-target")

	out, err := apiProve(st, "supersede")
	if err != nil {
		t.Fatalf("run must succeed: %v", err)
	}
	// The property must end PROVEN and must NOT be reported as aborted.
	if strings.Contains(out, "⚠ aborted") && strings.Contains(out, "sup-target") {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "sup-target") && strings.Contains(line, "aborted") {
				t.Fatalf("proof did not supersede the earlier abort: %q\n%s", line, out)
			}
		}
	}
	h, _ := st.Resolve("supersede")
	m, _ := st.GetMeta(h)
	found := false
	for _, pi := range m.ProvenProps {
		if pi == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("superseded property was dropped from the recorded set: proven=%v\n%s", m.ProvenProps, out)
	}
}
