package main

import (
	"strings"
	"testing"
)

// The spec-query surface matches definitions by the CONTENT HASH of their
// properties, not by name. Two differently-implemented functions that satisfy
// the same-shaped law converge on the same propHash (the law references `self`
// and de Bruijn binders, so it is def-independent) and are surfaced together; a
// function with a different law is not.
func TestFindMatchesByPropContentHash(t *testing.T) {
	st := newStore(t)
	// Two DIFFERENT functions (+ vs *) carrying the SAME commutativity law.
	put(t, st, `(defn op-a [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (op-a a b) (op-a b a))))`)
	put(t, st, `(defn op-b [] [(a Int) (b Int)] Int (* a b)
		(prop comm [(a Int) (b Int)] (== (op-b a b) (op-b b a))))`)
	// A third with a DIFFERENT property (trivial self-equality, not commutativity).
	put(t, st, `(defn op-c [] [(a Int)] Int (+ a 1)
		(prop refl [(a Int)] (== (op-c a) (op-c a))))`)

	// propHash is genuinely equal across the two commutative ops...
	if propHash(&mustDef(t, st, "op-a").Props[0]) != propHash(&mustDef(t, st, "op-b").Props[0]) {
		t.Fatal("commutativity should hash identically for op-a and op-b")
	}
	// ...and different from op-c's law.
	if propHash(&mustDef(t, st, "op-a").Props[0]) == propHash(&mustDef(t, st, "op-c").Props[0]) {
		t.Fatal("distinct laws must hash differently")
	}

	out, err := apiFind(st, "op-a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "op-b") {
		t.Fatalf("find op-a should surface op-b (shared commutativity):\n%s", out)
	}
	if strings.Contains(out, "op-c") {
		t.Fatalf("find op-a should NOT surface op-c (different law):\n%s", out)
	}
}

// Cross-type matching: a law that is polymorphic in its operand type (e.g.
// commutativity) matches across the types it ranges over. Commutativity over
// Int and over Rat share a generalized property hash and are surfaced together.
func TestFindCrossTypeMatch(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn plus-i [] [(a Int) (b Int)] Int (+ a b)
		(prop comm [(a Int) (b Int)] (== (plus-i a b) (plus-i b a))))`)
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop comm [(a Rat) (b Rat)] (== (plus-r a b) (plus-r b a))))`)

	// EXACT hashes differ (Int vs Rat binders)...
	if propHash(&mustDef(t, st, "plus-i").Props[0]) == propHash(&mustDef(t, st, "plus-r").Props[0]) {
		t.Fatal("exact propHash should differ across Int and Rat binders")
	}
	// ...but the GENERALIZED hashes match (both [t0,t0]).
	if propHashGeneral(&mustDef(t, st, "plus-i").Props[0]) != propHashGeneral(&mustDef(t, st, "plus-r").Props[0]) {
		t.Fatal("generalized propHash should match commutativity across Int and Rat")
	}
	out, err := apiFind(st, "plus-i")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plus-r") {
		t.Fatalf("find plus-i should cross-type match plus-r:\n%s", out)
	}
}

// Fresh-spec query: write a specification (a defn whose props are the query),
// and find proven implementations — no example, no name of the target used.
func TestFindSpecFreshQuery(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop comm [(a Rat) (b Rat)] (== (plus-r a b) (plus-r b a))))`)

	// The query is a fresh Int-commutativity spec; plus-r is Rat and never named.
	out, err := apiFindSpec(st, `(defn wanted [] [(a Int) (b Int)] Int (+ a b)
		(prop commutative [(a Int) (b Int)] (== (wanted a b) (wanted b a))))`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "plus-r") {
		t.Fatalf("fresh spec (Int commutativity) should find plus-r (Rat, cross-type):\n%s", out)
	}
	// (plus-r is only `tested` here — no Z3 in unit tests — so the "proven
	// implementation" flag is exercised by the live demo against the real corpus,
	// where rat-add/rat-mul are proven.)

	// A spec nobody satisfies returns cleanly (no false matches).
	out2, err := apiFindSpec(st, `(defn odd [] [(a Int)] Int (+ a 1)
		(prop weird [(a Int)] (== (odd (odd a)) a)))`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "plus-r") {
		t.Fatalf("an unrelated spec must not match plus-r:\n%s", out2)
	}
}

func mustDef(t *testing.T, st *Store, name string) *Def {
	t.Helper()
	h, ok := st.Resolve(name)
	if !ok {
		t.Fatalf("%s not in store", name)
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
