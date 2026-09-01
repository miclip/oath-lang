package main

import (
	"strings"
	"testing"
)

// Type aliases (effects/generic-consumer demand 2). A (type Name ty) form binds a
// batch-scoped, identity-transparent alias: a def using it hashes IDENTICALLY to one
// spelling the type inline, because the alias expands to the same canonical Ty before
// hashing. It is surface sugar — no stored object, no journal entry, no new core type.

func aliasStore(t *testing.T) *Store {
	t.Helper()
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	return st
}

// The defining property: alias and inline produce the SAME object hash.
func TestTypeAliasPreservesIdentity(t *testing.T) {
	st := aliasStore(t)
	inline := put(t, st, `(defn f-inline [] [(cap {emit (-> Str Str) env (-> Str Str)})] Str
		((. cap emit) "x") (prop p [(cap {emit (-> Str Str) env (-> Str Str)})] (== (f-inline cap) ((. cap emit) "x"))))`)
	aliased := put(t, st, `(type Cap {emit (-> Str Str) env (-> Str Str)})
		(defn f-alias [] [(cap Cap)] Str
		((. cap emit) "x") (prop p [(cap Cap)] (== (f-alias cap) ((. cap emit) "x"))))`)
	if inline[0].Status != "accepted" || aliased[0].Status != "accepted" {
		t.Fatalf("both must elaborate: inline=%s alias=%s", inline[0].Status, aliased[0].Status)
	}
	if inline[0].Hash != aliased[0].Hash {
		t.Errorf("alias must be identity-transparent (identical hash):\n inline %s\n alias  %s", inline[0].Hash, aliased[0].Hash)
	}
}

// An alias may reference an earlier alias; the fully-expanded form is still identical
// to writing the whole type inline.
func TestTypeAliasChainsAndStaysTransparent(t *testing.T) {
	st := aliasStore(t)
	chained := put(t, st, `(type Fn (-> Str Str))
		(type Rec {first Fn second Fn})
		(defn g-chain [] [(p Rec)] Str ((. p first) "hi") (prop q [(p Rec)] (== (g-chain p) ((. p first) "hi"))))`)
	inline := put(t, st, `(defn g-inline [] [(p {first (-> Str Str) second (-> Str Str)})] Str
		((. p first) "hi") (prop q [(p {first (-> Str Str) second (-> Str Str)})] (== (g-inline p) ((. p first) "hi"))))`)
	if chained[0].Hash != inline[0].Hash {
		t.Errorf("chained alias must expand transparently: chain %s vs inline %s", chained[0].Hash, inline[0].Hash)
	}
}

// PARAMETRIC aliases (the generics case). A polymorphic def using (Eq a) and a
// monomorphic binder using (Eq Int) both expand identity-transparently, so the def
// hashes identically to one with the dictionary record spelled inline.
func TestParametricAliasPreservesIdentity(t *testing.T) {
	st := aliasStore(t)
	inline := put(t, st, `(defn cnt-inline [a] [(eqd {eq (-> a a Bool)}) (x a) (xs (List a))] Int
		(match xs ((Nil) 0) ((Cons h t) (if ((. eqd eq) x h) (+ 1 (cnt-inline [a] eqd x t)) (cnt-inline [a] eqd x t))))
		(prop nn [(eqd {eq (-> Int Int Bool)}) (x Int) (xs (List Int))] (<= 0 (cnt-inline [Int] eqd x xs))))`)
	aliased := put(t, st, `(type Eq [a] {eq (-> a a Bool)})
		(defn cnt-alias [a] [(eqd (Eq a)) (x a) (xs (List a))] Int
		(match xs ((Nil) 0) ((Cons h t) (if ((. eqd eq) x h) (+ 1 (cnt-alias [a] eqd x t)) (cnt-alias [a] eqd x t))))
		(prop nn [(eqd (Eq Int)) (x Int) (xs (List Int))] (<= 0 (cnt-alias [Int] eqd x xs))))`)
	if inline[0].Status != "accepted" || aliased[0].Status != "accepted" {
		t.Fatalf("both must elaborate: inline=%s alias=%s", inline[0].Status, aliased[0].Status)
	}
	if inline[0].Hash != aliased[0].Hash {
		t.Errorf("parametric alias must be identity-transparent:\n inline %s\n alias  %s", inline[0].Hash, aliased[0].Hash)
	}
}

// A bare-type-variable body: (Id Int) must expand to exactly Int.
func TestParametricAliasIdentityFunction(t *testing.T) {
	st := aliasStore(t)
	viaAlias := put(t, st, `(type Id [a] a)
		(defn id1 [] [(x (Id Int))] Int x (prop p [(x Int)] (== (id1 x) x)))`)
	direct := put(t, st, `(defn id2 [] [(x Int)] Int x (prop p [(x Int)] (== (id2 x) x)))`)
	if viaAlias[0].Hash != direct[0].Hash {
		t.Errorf("(Id Int) must expand to Int: alias %s vs direct %s", viaAlias[0].Hash, direct[0].Hash)
	}
}

// A parametric alias may be applied inside another alias; the chain stays transparent.
func TestParametricAliasChains(t *testing.T) {
	st := aliasStore(t)
	chained := put(t, st, `(type Pair [a b] {fst a snd b})
		(type IntStr (Pair Int Str))
		(defn g1 [] [(p IntStr)] Int 0 (prop q [] (== 0 0)))`)
	inline := put(t, st, `(defn g2 [] [(p {fst Int snd Str})] Int 0 (prop q [] (== 0 0)))`)
	if chained[0].Hash != inline[0].Hash {
		t.Errorf("chained parametric alias must expand transparently: %s vs %s", chained[0].Hash, inline[0].Hash)
	}
}

func TestParametricAliasArityErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"bare-use", "(type Eq [a] {eq (-> a a Bool)})\n(defn f [] [(e Eq)] Int 0 (prop p [] (== 0 0)))", "takes 1 type argument"},
		{"too-few", "(type Pair [a b] {fst a snd b})\n(defn f [] [(p (Pair Int))] Int 0 (prop p2 [] (== 0 0)))", "got 1"},
		{"too-many", "(type Id [a] a)\n(defn f [] [(x (Id Int Int))] Int x (prop p [] (== 0 0)))", "got 2"},
		{"open-body", "(type Bad [a] {x (-> a b Bool)})", `unknown type "b"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := aliasStore(t)
			_, err := apiPut(st, c.src, "t", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected error containing %q; got %v", c.want, err)
			}
		})
	}
}

// A non-record type aliases fine — the feature is general.
func TestTypeAliasNonRecord(t *testing.T) {
	st := aliasStore(t)
	r := put(t, st, `(type Handler (-> Str Str))
		(defn h [] [(f Handler)] Str (f "x") (prop p [(f Handler)] (== (h f) (f "x"))))`)
	if r[0].Status != "accepted" {
		t.Fatalf("function-type alias should elaborate; got %s", r[0].Status)
	}
}

// `oath publish` accepts aliases too (see publish_type_alias_test.go). What survives
// is the INVARIANT that made the old refusal necessary: an alias declares no published
// name, so every publish path must filter it out before asking for one. declaredName is
// where that assumption would silently produce a name, so it refuses instead.
func TestTypeAliasHasNoPublishedName(t *testing.T) {
	forms, err := parseForms(`(type Cap {emit (-> Str Str)})`)
	if err != nil {
		t.Fatal(err)
	}
	if !isTypeAliasForm(forms[0]) {
		t.Fatal("a (type ...) form must be recognised as an alias, or the publish paths will treat it as a definition")
	}
	_, derr := declaredName(forms[0])
	if derr == nil || !strings.Contains(derr.Error(), "has no published name") {
		t.Fatalf("declaredName must refuse an alias rather than invent a name for it; got %v", derr)
	}
}

// `oath resolve` ACCEPTS aliases: the same source that put accepts elaborates here, and
// the alias contributes no name of its own to the dependency set.
func TestTypeAliasAcceptedInResolve(t *testing.T) {
	st := aliasStore(t)
	direct, err := elaborateInto(st, `(type Cap {emit (-> Str Str)})
		(defn svc [] [(cap Cap)] Str ((. cap emit) "r") (prop p [(cap Cap)] (== (svc cap) ((. cap emit) "r"))))`)
	if err != nil {
		t.Fatalf("resolve must accept a (type ...) alias: %v", err)
	}
	if direct["Str"] == "" {
		t.Errorf("Str is referenced by the alias body and must be pinned; got %v", direct)
	}
	if _, ok := direct["Cap"]; ok {
		t.Errorf("the alias name is not a dependency; got %v", direct)
	}
}

func TestTypeAliasRejections(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"builtin", `(type Int {x (-> Str Str)})`, "builtin"},
		{"shadow-data", `(type List {x (-> Str Str)})`, "data type"},
		{"duplicate", "(type A (-> Str Str))\n(type A (-> Str Str))", "already defined"},
		{"ground-only", `(type Bad {x a})`, "unknown type"},
		{"malformed", `(type OnlyName)`, "must be (type Name ty)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := aliasStore(t)
			_, err := apiPut(st, c.src, "t", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected error containing %q; got %v", c.want, err)
			}
		})
	}
}
