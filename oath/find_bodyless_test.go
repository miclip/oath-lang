package main

import (
	"strings"
	"testing"
)

// #65 rung 4: a spec query is JUST its property — you have no implementation, which
// is why you are querying. A body-less query must produce the SAME result as one
// with a placeholder body, because the find matches on the property's content hash,
// not the body.
func TestFindSpecBodilessMatchesWithBody(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop comm [(a Rat) (b Rat)] (== (plus-r a b) (plus-r b a))))`)

	bodiless, err := apiFindSpec(st, `(defn wanted [] [(a Int) (b Int)] Int
		(prop commutative [(a Int) (b Int)] (== (wanted a b) (wanted b a))))`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodiless, "plus-r") {
		t.Fatalf("a body-less commutativity spec should find plus-r:\n%s", bodiless)
	}

	withBody, err := apiFindSpec(st, `(defn wanted [] [(a Int) (b Int)] Int (+ a b)
		(prop commutative [(a Int) (b Int)] (== (wanted a b) (wanted b a))))`)
	if err != nil {
		t.Fatal(err)
	}
	if bodiless != withBody {
		t.Errorf("body-less and with-body queries must be identical:\n--- body-less ---\n%s\n--- with body ---\n%s", bodiless, withBody)
	}
}

// The same for proof-implication, which also elaborates the query and so needs the
// synthesized body.
func TestFindImpliesBodilessWorks(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn add2 [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (add2 a b) (add2 b a))))`)

	out, err := apiFindImplies(st, `(defn wanted [] [(a Int) (b Int)] Int
		(prop flipped [(a Int) (b Int)] (== (wanted b a) (wanted a b))))`, findImpliesSummary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "add2") {
		t.Fatalf("a body-less flipped-commutativity implication should find add2:\n%s", out)
	}
}

// ensureQueryBody reuses a PARAMETER whose type is the return type as the body — a
// bare reference that never routes through head-form dispatch, so it is immune to
// the function's name (a query may be named `if` or `+`) and handles a polymorphic
// return. It leaves a query that already has a body untouched, and requires an
// explicit body when no parameter has the return type.
func TestEnsureQueryBody(t *testing.T) {
	parse := func(src string) sx {
		fs, err := parseForms(src)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		return fs[0]
	}
	st := newStore(t)

	// Body-less: the first parameter whose type is the return type becomes the body.
	got, err := ensureQueryBody(st, parse(`(defn f [] [(a Int) (b Int)] Int (prop p [(a Int) (b Int)] (== (f a b) (f b a))))`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Kids) != 7 || !got.Kids[5].isSym("a") {
		t.Errorf("expected the body to be parameter a, got %+v", got.Kids)
	}

	// A polymorphic return reuses a parameter of that type — no inhabitant needed.
	poly, err := ensureQueryBody(st, parse(`(defn g [a] [(x a) (y a)] a (prop p [] true))`))
	if err != nil {
		t.Fatal(err)
	}
	if !poly.Kids[5].isSym("x") {
		t.Errorf("polymorphic return should reuse parameter x, got %+v", poly.Kids[5])
	}

	// A parameter named `if` is a fine reference — `if` is special only in head
	// position, and a bare reference is never a head. (This is why not synthesizing
	// a self-call is what makes a query named `if`/`+` work.)
	ifp, err := ensureQueryBody(st, parse(`(defn h [] [(if Int)] Int (prop p [] true))`))
	if err != nil {
		t.Fatal(err)
	}
	if !ifp.Kids[5].isSym("if") {
		t.Errorf("a parameter named `if` should be usable as a bare reference, got %+v", ifp.Kids[5])
	}

	// A query that already has a body is unchanged.
	orig := parse(`(defn f [] [(a Int)] Int (+ a 1) (prop p [(a Int)] (== (f a) (f a))))`)
	same, err := ensureQueryBody(st, orig)
	if err != nil {
		t.Fatal(err)
	}
	if len(same.Kids) != len(orig.Kids) {
		t.Errorf("a query that already has a body must be unchanged (%d vs %d kids)", len(same.Kids), len(orig.Kids))
	}

	// No parameter has the return type → require an explicit body.
	if _, err := ensureQueryBody(st, parse(`(defn q [] [(a Int)] Bool (prop p [] true))`)); err == nil {
		t.Error("a query whose return type matches no parameter should require an explicit body")
	}
	if _, err := ensureQueryBody(st, parse(`(defn c [] [] Int (prop p [] (== 1 1)))`)); err == nil {
		t.Error("a nullary query should require an explicit body")
	}

	// A return-typed parameter shadowed by a later parameter of the same name must
	// NOT be selected — the bare reference resolves to the visible (last) binding.
	if _, err := ensureQueryBody(st, parse(`(defn wanted [] [(a Int) (a Str)] Int (prop p [] true))`)); err == nil {
		t.Error("a shadowed return-typed parameter must not be selected")
	}
	// But a duplicate of the SAME type is fine: the visible binding still has the
	// return type.
	if dup, err := ensureQueryBody(st, parse(`(defn wanted [] [(a Int) (a Int)] Int (prop p [] true))`)); err != nil || !dup.Kids[5].isSym("a") {
		t.Errorf("a same-typed duplicate parameter should be usable: err=%v", err)
	}

	// isPropForm matches a property CLAUSE shape, not every list headed by `prop`.
	if isPropForm(parse(`(prop x)`)) || isPropForm(parse(`(prop x y)`)) {
		t.Error("a call to a function named prop must not be classified as a property clause")
	}
	if !isPropForm(parse(`(prop law [(x Int)] (== x x))`)) {
		t.Error("a well-formed property clause must be recognized")
	}
}
