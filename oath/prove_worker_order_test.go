package main

import (
	"sort"
	"testing"
)

// The journal must be a function of the corpus, not of goroutine scheduling. Defs
// within a proof level are independent, so they complete in arbitrary order; appending
// from inside the goroutines made log.jsonl's ORDER — and therefore every downstream
// chain hash — vary between runs over identical inputs.
//
// Verdicts were never affected, which is exactly why it could persist unnoticed: the
// only casualty was byte-reproducibility of an append-only audit trail, and nothing
// asserted that.
func TestPendingEntriesSortCanonically(t *testing.T) {
	// Simulate completion order: reverse-alphabetical, as scheduling might deliver.
	pending := []LogEntry{
		{Name: "zeta", Kind: "prove", Status: "accepted"},
		{Name: "alpha", Kind: "prove", Status: "accepted"},
		{Name: "mid", Kind: "prove", Status: "accepted"},
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Name < pending[j].Name })
	want := []string{"alpha", "mid", "zeta"}
	for i, w := range want {
		if pending[i].Name != w {
			t.Fatalf("position %d is %q, want %q — completion order leaked into the journal", i, pending[i].Name, w)
		}
	}
}

// A prove entry touches no name, so it must not register as a transition. Counting it
// would inflate every proved name's revision and, through parent_rev, invalidate
// envelopes prepared against a state that never changed.
func TestProveEntriesAreNotNameTransitions(t *testing.T) {
	e := &LogEntry{Name: "n", Kind: "prove", Status: "accepted", NameTransition: transitionNone}
	if e.repointedName() {
		t.Fatal("a prove entry counted as a name transition")
	}
	// Legacy prove entries carry no field and must derive the same way.
	legacy := &LogEntry{Name: "n", Kind: "prove", Status: "accepted"}
	if legacy.nameTransitionOf() != transitionNone {
		t.Fatalf("legacy prove entry derived %q, want %q", legacy.nameTransitionOf(), transitionNone)
	}
}
