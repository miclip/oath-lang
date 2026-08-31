package main

import (
	"strings"
	"testing"
)

// stats-consumer demand 1: when a goal is left unproven, the prover should name the
// reused dependencies whose UNPROVEN laws are absent from the lemma library — the
// difference between "this goal is hard" and "I reused a tested dependency and lost
// its lemma", which the per-goal detail cannot draw. These test the two pure helpers
// behind the note without invoking z3.
func TestUnprovenDepLemmasNamesTestedDeps(t *testing.T) {
	st := newMemStoreForTest(t)
	// dep: a function carrying a law, left TESTED (put binds it; nothing proves it).
	if reps, err := apiPut(st, `(defn dep [] [(n Int)] Int (+ n 1)
		(prop step [(n Int)] (== (dep n) (+ n 1))))`, "t", ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("dep put: %v %+v", err, reps[0])
	}
	// user: references dep, with a goal that mentions dep.
	reps, err := apiPut(st, `(defn user [] [(n Int)] Int (dep n)
		(prop via-dep [(n Int)] (== (user n) (dep n))))`, "t", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("user put: %v %+v", err, reps[0])
	}
	uh := reps[0].Hash
	ud, _ := st.GetDef(uh)
	footprints := []map[string]bool{goalFootprint(st, uh, ud, &ud.Props[0])}

	// The goal references dep, so dep's law is admissible for its footprint and, being
	// unproven, is named.
	got := unprovenDepLemmas(st, ud, footprints, nil)
	if len(got) != 1 || !strings.HasPrefix(got[0], "dep (1 unproven law)") {
		t.Fatalf("expected the tested dependency named with its one unproven law, got %v", got)
	}

	// Once the dependency's law is proven, its lemma is available and the note is silent.
	dh, _ := st.Resolve("dep")
	dm, _ := st.GetMeta(dh)
	dm.ProvenProps = []int{0}
	if err := st.SetMeta(dh, dm); err != nil {
		t.Fatal(err)
	}
	if got := unprovenDepLemmas(st, ud, footprints, nil); len(got) != 0 {
		t.Errorf("a proven dependency must not be named: %v", got)
	}
}

// The note is filtered to the unproven goal's OWN vocabulary — a tested dependency a
// stuck goal never reaches must not be named, so the note stays silent (rather than
// misdirecting) when what remains is an intrinsic wall.
func TestUnprovenDepLemmasRelevanceFilter(t *testing.T) {
	st := newMemStoreForTest(t)
	if reps, err := apiPut(st, `(defn used [] [(n Int)] Int (+ n 1)
		(prop step [(n Int)] (== (used n) (+ n 1))))`, "t", ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("used put: %v %+v", err, reps[0])
	}
	if reps, err := apiPut(st, `(defn unused [] [(n Int)] Int (* n 2)
		(prop dbl [(n Int)] (== (unused n) (* n 2))))`, "t", ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("unused put: %v %+v", err, reps[0])
	}
	// The goal references `used` only; `unused` is in the store but not reachable.
	reps, err := apiPut(st, `(defn g [] [(n Int)] Int (used n)
		(prop via-used [(n Int)] (== (g n) (used n))))`, "t", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("g put: %v %+v", err, reps[0])
	}
	gh := reps[0].Hash
	gd, _ := st.GetDef(gh)
	footprints := []map[string]bool{goalFootprint(st, gh, gd, &gd.Props[0])}

	uh, _ := st.Resolve("unused")
	if footprints[0][uh] {
		t.Error("a definition the goal never references must not be in its footprint")
	}
	got := unprovenDepLemmas(st, gd, footprints, nil)
	if len(got) != 1 || !strings.HasPrefix(got[0], "used") {
		t.Fatalf("only the referenced tested dependency should be named, got %v", got)
	}
}

// A dependency referenced at an OLD hash after its name was repointed away has no
// live name that resolves to it, so `oath prove <name>` could not target it. The
// note must OMIT it rather than print the stale name (which now resolves to a
// different object) or an unresolvable `#hash` reference — the note only ever
// suggests proof targets the reader can actually run.
func TestUnprovenDepLemmasRebindSafe(t *testing.T) {
	st := newMemStoreForTest(t)
	if reps, err := apiPut(st, `(defn dep [] [(n Int)] Int (+ n 1)
		(prop step [(n Int)] (== (dep n) (+ n 1))))`, "t", ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("dep put: %v %+v", err, reps[0])
	}
	// user references dep at its current hash.
	reps, err := apiPut(st, `(defn user [] [(n Int)] Int (dep n)
		(prop via-dep [(n Int)] (== (user n) (dep n))))`, "t", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("user put: %v %+v", err, reps[0])
	}
	uh := reps[0].Hash
	ud, _ := st.GetDef(uh)
	oldDep, _ := st.Resolve("dep")

	// Repoint the name "dep" to a DIFFERENT object; the user's def still references the
	// old hash.
	if reps, err := apiPut(st, `(defn other [] [(n Int)] Int (+ n 99)
		(prop step [(n Int)] (== (other n) (+ n 99))))`, "t", ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("other put: %v %+v", err, reps[0])
	}
	newHash, _ := st.Resolve("other")
	if _, err := st.Repoint("dep", newHash); err != nil {
		t.Fatal(err)
	}

	fp := []map[string]bool{goalFootprint(st, uh, ud, &ud.Props[0])}
	got := unprovenDepLemmas(st, ud, fp, nil)
	// The superseded object has no resolvable name, so it is omitted — and crucially
	// the note does not name "dep", which now resolves to the OTHER object.
	for _, g := range got {
		if strings.HasPrefix(g, "dep ") {
			t.Errorf("named the stale name 'dep', which now resolves to a different object: %q", g)
		}
	}
	_ = oldDep
}

// A reused dependency whose only law mentions a helper OUTSIDE the goal's footprint
// is NOT named: proving that law would not admit it (its mentions escape the
// footprint), so suggesting it would misdirect. This is the admissibility filter the
// prover itself applies, not just reachability.
func TestUnprovenDepLemmasSkipsInadmissibleLaw(t *testing.T) {
	st := newMemStoreForTest(t)
	if reps, err := apiPut(st, `(defn helper [] [(n Int)] Int (+ n 1)
		(prop step [(n Int)] (== (helper n) (+ n 1))))`, "t", ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("helper put: %v %+v", err, reps[0])
	}
	// dep's BODY does not use helper, but its LAW does — so helper is not in a goal's
	// footprint (which follows bodies), and dep's law is inadmissible there.
	if reps, err := apiPut(st, `(defn dep [] [(n Int)] Int (+ n 2)
		(prop via-helper [(n Int)] (== (dep n) (+ (helper n) 1))))`, "t", ""); err != nil || reps[0].Status != "accepted" {
		t.Fatalf("dep put: %v %+v", err, reps[0])
	}
	reps, err := apiPut(st, `(defn user [] [(n Int)] Int (dep n)
		(prop via-dep [(n Int)] (== (user n) (dep n))))`, "t", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("user put: %v %+v", err, reps[0])
	}
	uh := reps[0].Hash
	ud, _ := st.GetDef(uh)
	fp := goalFootprint(st, uh, ud, &ud.Props[0])
	dh, _ := st.Resolve("helper")
	if fp[dh] {
		t.Fatal("precondition: helper must be outside the goal's footprint")
	}
	if got := unprovenDepLemmas(st, ud, []map[string]bool{fp}, nil); len(got) != 0 {
		t.Errorf("a dependency whose only law is inadmissible for the goal must not be named: %v", got)
	}
}
