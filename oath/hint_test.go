package main

import (
	"strings"
	"testing"
	"time"
)

// A hint may only admit a PROVEN property — the soundness guard. Hinting an
// unproven property is refused at authoring time (#67).
func TestHintRejectsUnprovenLemma(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn lemdef [] [] Int 3
		(prop l-refl [(x Int)] (== x x)))`)
	put(t, st, `(defn goaldef [] [] Int 5
		(prop g-refl [(y Int)] (== y y)))`)

	// lemdef.l-refl is not proven yet → the hint must be refused.
	if _, err := apiHint(st, "goaldef", "g-refl", "lemdef.l-refl"); err == nil {
		t.Fatal("hinting an unproven property was accepted; want rejection")
	}
}

// A definition cannot hint its own siblings (already admissible) — refused.
func TestHintRejectsSelfHint(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn selfdef [] [] Int 5
		(prop a [(y Int)] (== y y))
		(prop b [(z Int)] (== z z)))`)
	if _, err := apiHint(st, "selfdef", "a", "selfdef.b"); err == nil {
		t.Fatal("self-hint was accepted; want rejection")
	}
}

// apiHint records a hint, apiHintClear removes it, and the hint survives a
// re-put of the same object (it is a hash-keyed, verdict-adjacent fact).
func TestHintRecordMergeClear(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn lemdef [] [] Int 3
		(prop l-refl [(x Int)] (== x x)))`)
	put(t, st, `(defn goaldef [] [] Int 5
		(prop g-refl [(y Int)] (== y y)))`)

	if _, err := apiProve(st, "lemdef"); err != nil {
		t.Fatalf("prove lemdef: %v", err)
	}
	if _, err := apiHint(st, "goaldef", "g-refl", "lemdef.l-refl"); err != nil {
		t.Fatalf("apiHint: %v", err)
	}

	gh, _ := st.Resolve("goaldef")
	m, _ := st.GetMeta(gh)
	if len(m.Hints[0]) != 1 {
		t.Fatalf("hint not recorded: %+v", m.Hints)
	}

	// Re-put the identical source: the hint must survive the metadata merge.
	put(t, st, `(defn goaldef [] [] Int 5
		(prop g-refl [(y Int)] (== y y)))`)
	m2, _ := st.GetMeta(gh)
	if len(m2.Hints[0]) != 1 {
		t.Fatalf("hint lost across re-put: %+v", m2.Hints)
	}

	// Clear it.
	if _, err := apiHintClear(st, "goaldef", ""); err != nil {
		t.Fatalf("apiHintClear: %v", err)
	}
	m3, _ := st.GetMeta(gh)
	if len(m3.Hints) != 0 {
		t.Fatalf("hint not cleared: %+v", m3.Hints)
	}
}

// The mechanism itself: a hinted lemma enters the canonical proof script for a
// goal even though the relevance filter would exclude it (the goal does not
// reference the lemma's definition at all). This is the whole feature — the
// hint changes the admitted lemma SET, bypassing the §7.2 footprint gate. It is
// sound because the admitted fact is already proven.
func TestHintExpandsAdmittedLemmaSet(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	// lemdef's proven property is a universally-quantified fact; when admitted as
	// a lemma it emits an `(assert (forall ...))`. goaldef does NOT reference
	// lemdef, so absent a hint the filter never surfaces it.
	put(t, st, `(defn lemdef [] [] Int 3
		(prop l-refl [(x Int)] (== x x)))`)
	put(t, st, `(defn goaldef [] [] Int 5
		(prop g-refl [(y Int)] (== y y)))`)
	if _, err := apiProve(st, "lemdef"); err != nil {
		t.Fatalf("prove lemdef: %v", err)
	}

	gh, _ := st.Resolve("goaldef")

	before, err := directAttemptScript(st, gh, 0)
	if err != nil {
		t.Fatalf("directAttemptScript before: %v", err)
	}
	if strings.Contains(before, "forall") {
		t.Fatalf("unhinted script already admits a quantified lemma:\n%s", before)
	}

	if _, err := apiHint(st, "goaldef", "g-refl", "lemdef.l-refl"); err != nil {
		t.Fatalf("apiHint: %v", err)
	}

	after, err := directAttemptScript(st, gh, 0)
	if err != nil {
		t.Fatalf("directAttemptScript after: %v", err)
	}
	if !strings.Contains(after, "forall") {
		t.Fatalf("hinted lemma did not enter the script:\n%s", after)
	}
}

// A hint that references a now-unproven target is inert — never admitted to the
// script — so it can never launder a falsehood. We build the hint while proven,
// then simulate the target regressing by clearing its ProvenProps.
func TestHintInertWhenTargetUnproven(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn lemdef [] [] Int 3
		(prop l-refl [(x Int)] (== x x)))`)
	put(t, st, `(defn goaldef [] [] Int 5
		(prop g-refl [(y Int)] (== y y)))`)
	if _, err := apiProve(st, "lemdef"); err != nil {
		t.Fatalf("prove lemdef: %v", err)
	}
	if _, err := apiHint(st, "goaldef", "g-refl", "lemdef.l-refl"); err != nil {
		t.Fatalf("apiHint: %v", err)
	}

	// Regress the target: strip its proven set.
	lh, _ := st.Resolve("lemdef")
	lm, _ := st.GetMeta(lh)
	lm.ProvenProps = nil
	if err := st.SetMeta(lh, lm); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	gh, _ := st.Resolve("goaldef")
	after, err := directAttemptScript(st, gh, 0)
	if err != nil {
		t.Fatalf("directAttemptScript: %v", err)
	}
	if strings.Contains(after, "forall") {
		t.Fatalf("inert hint (unproven target) was admitted:\n%s", after)
	}
}

// The wall-clock safety cap is host-tunable via OATH_PROVE_WALLCAP_SEC (#14
// registry worker on slow cores). It is NOT part of any recorded verdict, so a
// tuned cap changes only how long a slow machine may run before an environmental
// abort — never an outcome. Default stays 600s so local/CI/conformance are
// byte-identical.
func TestProveWallCapEnvOverride(t *testing.T) {
	if got := proveWallCap(); got != proveWallCapDefault {
		t.Fatalf("default wall cap = %v, want %v", got, proveWallCapDefault)
	}
	t.Setenv("OATH_PROVE_WALLCAP_SEC", "1800")
	if got := proveWallCap(); got != 1800*time.Second {
		t.Fatalf("override wall cap = %v, want 1800s", got)
	}
	// A malformed or non-positive value falls back to the default (never 0, which
	// would abort every attempt instantly).
	t.Setenv("OATH_PROVE_WALLCAP_SEC", "nonsense")
	if got := proveWallCap(); got != proveWallCapDefault {
		t.Fatalf("malformed override = %v, want default", got)
	}
	t.Setenv("OATH_PROVE_WALLCAP_SEC", "0")
	if got := proveWallCap(); got != proveWallCapDefault {
		t.Fatalf("zero override = %v, want default (0 would abort everything)", got)
	}
}

// SOUNDNESS (SPEC §7.2): a property is never its own lemma, and that exclusion
// is applied BEFORE hints. A hand-written self-hint in metadata — which `oath
// hint` refuses to record, but nothing stops an editor from writing — must be
// discarded by the prover, or a hint could assert the goal as its own axiom and
// "prove" anything. The blind Rust kernel surfaced this as an unpinned rule
// (DIVERGENCES 81c); it is now normative and tested on both sides.
func TestHintCannotMakePropertyItsOwnLemma(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	// A FALSE property. If a self-hint were honored it would be asserted as its
	// own lemma and would "prove" — the exact unsoundness this guards.
	put(t, st, `(defn selfhint [] [(n Int)] Int n
		(prop bogus [(n Int)] (== (selfhint n) (+ n 1))))`)
	h, _ := st.Resolve("selfhint")
	m, _ := st.GetMeta(h)
	m.Hints = map[int][]HintRef{0: {{Def: h, Prop: 0}}} // prop 0 hints ITSELF
	if err := st.SetMeta(h, m); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	if _, err := apiProve(st, "selfhint"); err != nil {
		t.Fatalf("prove: %v", err)
	}
	m2, _ := st.GetMeta(h)
	for _, pi := range m2.ProvenProps {
		if pi == 0 {
			t.Fatal("UNSOUND: a self-hinted property proved itself")
		}
	}
	// And the emitted script must not contain the goal as an admitted lemma.
	sc, err := directAttemptScript(st, h, 0)
	if err != nil {
		t.Fatalf("directAttemptScript: %v", err)
	}
	if strings.Count(sc, "(+ b0 1)") > 1 {
		t.Fatalf("self-hinted lemma leaked into the script:\n%s", sc)
	}
}
