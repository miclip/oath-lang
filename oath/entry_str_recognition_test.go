package main

import "testing"

// Friction item 4 (publish-consumer-friction.md): a compilable CLI entry once needed
// a bare `Str` binding, because entry recognition resolved the string type by the
// name `Str` while `List` was matched structurally. A program typed entirely against
// a namespaced `michael/Str` therefore would not build unless the store ALSO bound a
// redundant bare `Str`.
//
// The string type is now recognised the same way `List` already is: by content hash,
// falling back to the canonical Str prototype when the store names no `Str`. So a
// (-> (List Str) Str) entry whose types carry the canonical hashes is recognised with
// no bare `Str` bound — and a store that DOES name a (different) `Str` still governs,
// preserving #184.
func TestEntryRecognisesStrWithoutBareName(t *testing.T) {
	protoInit()
	st, err := newStoreWithBackend(newMemBackend(), "mem")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Resolve("Str"); ok {
		t.Fatal("precondition: the store must NOT bind a bare `Str`")
	}

	// With no bare `Str`, strTypeHash falls back to the canonical prototype.
	if got := strTypeHash(st); got != protoStr {
		t.Fatalf("no bare Str: strTypeHash must fall back to the canonical prototype, got %s", got)
	}

	// (-> (List Str) Str), types carrying the canonical hashes (what a namespaced
	// michael/List / michael/Str resolve to — byte-identical, same hash).
	str := Ty{K: "data", Hash: protoStr}
	listStr := Ty{K: "data", Hash: protoList, Args: []Ty{str}}
	entry := &Ty{K: "fun", A: &listStr, B: &str}
	if !isPureEntry(st, entry) {
		t.Fatal("a (-> (List Str) Str) entry at the canonical hashes must be recognised without a bare `Str` binding")
	}

	// #184 preserved: when the store DOES name a `Str`, that binding is authoritative.
	// Bind `Str` to a DIFFERENT shape; the canonical-hash entry is then no longer the
	// active string and must not be recognised.
	other := hashDef(&Def{K: "data", Ctors: [][]Ty{{}, {*tInt(), {K: "rec"}}, {}}}) // 3 ctors
	if _, err := st.Repoint("Str", other); err != nil {
		t.Fatal(err)
	}
	if strTypeHash(st) != other {
		t.Fatalf("a bound `Str` must win over the fallback, got %s want %s", strTypeHash(st), other)
	}
	if isPureEntry(st, entry) {
		t.Fatal("with `Str` repointed to another shape, the canonical-hash entry must NOT be recognised")
	}
}
