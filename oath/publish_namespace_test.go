package main

import (
	"strings"
	"testing"
)

// --namespace is a PREFIX applied at publication time, not an override of the
// declared name. `reverse` under `alice` becomes `alice/reverse`, so the source
// never carries the prefix through its own self-references.
func TestApplyNamespacePrefixesRatherThanOverrides(t *testing.T) {
	for _, c := range []struct{ declared, ns, want string }{
		{"reverse", "alice", "alice/reverse"},
		{"reverse", "alice/*", "alice/reverse"}, // both spellings, one meaning
		{"reverse", "", "reverse"},              // no namespace: unchanged
		{"bob/reverse", "", "bob/reverse"},      // source-declared prefix honoured
	} {
		got, err := applyNamespace(c.declared, c.ns)
		if err != nil {
			t.Fatalf("applyNamespace(%q,%q): %v", c.declared, c.ns, err)
		}
		if got != c.want {
			t.Errorf("applyNamespace(%q,%q) = %q, want %q", c.declared, c.ns, got, c.want)
		}
	}
}

// ONE SOURCE OF TRUTH. A source declaring a namespace plus a --namespace flag is
// ambiguous, and guessing which wins is how a publication lands under a name
// nobody chose — permanently.
func TestApplyNamespaceRefusesTwoSourcesOfTruth(t *testing.T) {
	_, err := applyNamespace("bob/reverse", "alice")
	if err == nil {
		t.Fatal("a definition declaring bob/ was accepted under --namespace alice")
	}
	if !strings.Contains(err.Error(), "ONE source of truth") {
		t.Errorf("refusal does not explain the ambiguity: %v", err)
	}
	// A namespace that is not a valid pattern is refused rather than concatenated.
	if _, err := applyNamespace("reverse", "key"); err == nil {
		t.Error("a protocol root was accepted as a publication namespace")
	}
}
