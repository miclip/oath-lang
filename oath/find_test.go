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
