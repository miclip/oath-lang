package main

import "testing"

// #74: once the registry is the SELECTION surface, evidence it lacks is evidence
// a caller cannot weigh. The worker must therefore RE-DERIVE spec strength, and
// the invariant is narrow in both directions: known evidence must not stay
// missing, and missing evidence must stay unknown rather than defaulting to a
// number.
func TestWorkerScoresButNeverInvents(t *testing.T) {
	st := newStore(t)
	// Has a mutable body → scorable.
	put(t, st, `(defn scorable-def [] [(x Int)] Int (+ x 1)
		(prop adds-one [(x Int)] (== (scorable-def x) (+ x 1))))`)
	// A pure projection: no operators, no literals, no branches → NO mutation
	// points. This must stay UNMEASURED, not become 0/0 or 1/1.
	put(t, st, `(data P [] (Mk Int Int))`)
	put(t, st, `(defn projection [] [(p P)] Int
		(match p ((Mk a b) a))
		(prop reads-first [(a Int) (b Int)] (== (projection (Mk a b)) a)))`)

	scanBulkScore(st, "test")

	h1, _ := st.Resolve("scorable-def")
	m1, _ := st.GetMeta(h1)
	if m1.MutantsTotal == 0 {
		t.Fatalf("scorable definition left unscored: %+v", m1.MutantsTotal)
	}

	h2, _ := st.Resolve("projection")
	m2, _ := st.GetMeta(h2)
	if m2.MutantsTotal != 0 {
		t.Fatalf("definition with no mutation points was given a score: %d", m2.MutantsTotal)
	}
	// And explain must still report that honestly rather than as zero.
	pkg, err := buildExplain(st, "projection")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.SpecStrength != nil {
		t.Fatalf("absent evidence reported as a score: %+v", pkg.SpecStrength)
	}
	found := false
	for _, l := range pkg.Limitations {
		if contains(l, "UNMEASURED") {
			found = true
		}
	}
	if !found {
		t.Fatalf("absent evidence not disclosed as UNMEASURED: %v", pkg.Limitations)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
