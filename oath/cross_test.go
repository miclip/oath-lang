package main

import "testing"

// N-version cross-check (#20): mutation testing kills spec WEAKNESS but is blind
// to spec MISALIGNMENT — a spec tight around the wrong function passes every
// axis. Independent redundancy catches it: two authors, one brief, cross-bind
// each spec to the other's body. The honest sum-abs and the adversary
// sum-of-squares share every folklore property (non-negative, nil-zero); only
// the defining-equation property falsifies, in BOTH directions — exactly where
// the lie is signed.
func TestCrossDetectsMisalignment(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(defn sabs [] [(xs (List Int))] Int
		(match xs ((Nil) 0) ((Cons h t) (+ (if (< h 0) (neg h) h) (sabs t))))
		(prop nil-zero [] (== (sabs (Nil [Int])) 0))
		(prop non-negative [(xs (List Int))] (<= 0 (sabs xs)))
		(prop cons-defeq [(h Int) (t (List Int))]
			(== (sabs (Cons [Int] h t)) (+ (if (< h 0) (neg h) h) (sabs t)))))`)
	put(t, st, `(defn sumsq [] [(xs (List Int))] Int
		(match xs ((Nil) 0) ((Cons h t) (+ (* h h) (sumsq t))))
		(prop nil-zero [] (== (sumsq (Nil [Int])) 0))
		(prop non-negative [(xs (List Int))] (<= 0 (sumsq xs)))
		(prop cons-defeq [(h Int) (t (List Int))]
			(== (sumsq (Cons [Int] h t)) (+ (* h h) (sumsq t)))))`)

	r, err := crossCheck(st, "sabs", "sumsq")
	if err != nil {
		t.Fatalf("crossCheck: %v", err)
	}
	if r.Agree() {
		t.Fatalf("sabs vs sumsq: expected DISAGREE, got AGREE")
	}
	// The folklore props pass both ways; only cons-defeq falsifies, in both
	// directions.
	for _, d := range []crossDir{r.aOnB, r.bOnA} {
		cs := d.contradictions()
		if len(cs) != 1 || cs[0].Name != "cons-defeq" {
			t.Fatalf("%s-on-%s: want exactly cons-defeq falsified, got %v", d.spec, d.body, cs)
		}
	}
}

// Cross-check AGREEs when two DIFFERENT bodies compute the same function:
// independent processes converged, no flag.
func TestCrossAgreesOnConvergence(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn dblA [] [(x Int)] Int (+ x x)
		(prop defeq [(x Int)] (== (dblA x) (+ x x))))`)
	put(t, st, `(defn dblB [] [(x Int)] Int (* 2 x)
		(prop defeq [(x Int)] (== (dblB x) (* 2 x))))`)
	r, err := crossCheck(st, "dblA", "dblB")
	if err != nil {
		t.Fatalf("crossCheck: %v", err)
	}
	if !r.Agree() {
		t.Fatalf("dblA vs dblB: expected AGREE (same function, different bodies), got DISAGREE")
	}
}

// Cross rejects vacuous and ill-typed pairings: a definition against itself
// (same object) and two different signatures.
func TestCrossGuards(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn f [] [(x Int)] Int (+ x 1)
		(prop p [(x Int)] (== (f x) (+ x 1))))`)
	put(t, st, `(defn g [] [(x Int) (y Int)] Int (+ x y)
		(prop p [(x Int) (y Int)] (== (g x y) (+ x y))))`)
	if _, err := crossCheck(st, "f", "f"); err == nil {
		t.Fatalf("cross f f: expected same-object rejection")
	}
	if _, err := crossCheck(st, "f", "g"); err == nil {
		t.Fatalf("cross f g: expected signature-mismatch rejection")
	}
}
