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

// A non-record type aliases fine — the feature is general.
func TestTypeAliasNonRecord(t *testing.T) {
	st := aliasStore(t)
	r := put(t, st, `(type Handler (-> Str Str))
		(defn h [] [(f Handler)] Str (f "x") (prop p [(f Handler)] (== (h f) (f "x"))))`)
	if r[0].Status != "accepted" {
		t.Fatalf("function-type alias should elaborate; got %s", r[0].Status)
	}
}

// Aliases are a put-time source convenience. The source-rewriting/dependency-classifying
// paths — `oath publish` and `oath resolve` — refuse a (type ...) form with guidance
// (expand inline; the objects are identical) rather than a generic "unknown form".
func TestTypeAliasRefusedInPublish(t *testing.T) {
	forms, err := parseForms(`(type Cap {emit (-> Str Str)})`)
	if err != nil {
		t.Fatal(err)
	}
	_, derr := declaredName(forms[0])
	if derr == nil || !strings.Contains(derr.Error(), "not supported in oath publish") {
		t.Fatalf("expected a publish-refusal naming the workaround; got %v", derr)
	}
}

func TestTypeAliasRefusedInResolve(t *testing.T) {
	st := aliasStore(t)
	_, err := elaborateInto(st, `(type Cap {emit (-> Str Str)})
		(defn svc [] [(cap Cap)] Str ((. cap emit) "r") (prop p [(cap Cap)] (== (svc cap) ((. cap emit) "r"))))`)
	if err == nil || !strings.Contains(err.Error(), "not supported in oath resolve") {
		t.Fatalf("expected a resolve-refusal naming the workaround; got %v", err)
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
