package main

import "testing"

// Specificity, not document order. The old ruleFor returned the first matching
// rule, so a ["*"] rule placed above a specific one silently shadowed it — a
// policy could be correct as written and wrong as ordered, with the shadowed rule
// still visibly present in the file.
func TestRuleForPicksMostSpecific(t *testing.T) {
	pol := &Policy{Rules: []PolicyRule{
		{Names: []string{"*"}, RequireTotal: true},                       // catch-all, listed FIRST
		{Names: []string{"michael/*"}, OwnerPubkey: "aa"},                // namespace
		{Names: []string{"michael/service1/*"}, OwnerPubkey: "bb"},       // deeper namespace
		{Names: []string{"michael/service1/webhook"}, OwnerPubkey: "cc"}, // exact
	}}
	for _, tc := range []struct {
		name, wantOwner string
		wantTotal       bool
	}{
		{"michael/service1/webhook", "cc", false}, // exact beats every prefix
		{"michael/service1/other", "bb", false},   // deeper prefix beats shallower
		{"michael/thing", "aa", false},            // namespace beats catch-all
		{"unrelated", "", true},                   // only the catch-all matches
	} {
		r := pol.ruleFor(tc.name)
		if r == nil {
			t.Fatalf("%s: no rule matched", tc.name)
		}
		if r.OwnerPubkey != tc.wantOwner {
			t.Fatalf("%s: owner=%q, want %q (document order was preferred over specificity)", tc.name, r.OwnerPubkey, tc.wantOwner)
		}
		if r.RequireTotal != tc.wantTotal {
			t.Fatalf("%s: matched the wrong rule entirely", tc.name)
		}
	}
}

// A prefix pattern governs names UNDER the namespace, not the bare name that
// happens to share its first segment. Folding them together would let a namespace
// claim silently capture a definition its owner never reasoned about.
func TestPrefixDoesNotCaptureBareName(t *testing.T) {
	pol := &Policy{Rules: []PolicyRule{{Names: []string{"michael/*"}, OwnerPubkey: "aa"}}}
	if r := pol.ruleFor("michael"); r != nil {
		t.Fatal(`"michael/*" captured the bare name "michael": a namespace and a same-prefixed definition are different things`)
	}
	if r := pol.ruleFor("michaelson"); r != nil {
		t.Fatal(`"michael/*" matched "michaelson": prefix matching must respect the separator, not raw string prefixes`)
	}
	if r := pol.ruleFor("michael/x"); r == nil || r.OwnerPubkey != "aa" {
		t.Fatal(`"michael/*" failed to match "michael/x"`)
	}
	if r := pol.ruleFor("michael/a/b/c"); r == nil || r.OwnerPubkey != "aa" {
		t.Fatal(`"michael/*" must match arbitrarily deep names beneath it`)
	}
}

// Pre-existing behaviour must be unchanged: exact names and "*" worked before and
// are what every existing policy is written against.
func TestRuleForBackCompat(t *testing.T) {
	exact := &Policy{Rules: []PolicyRule{{Names: []string{"sort", "reverse"}, ForbidFalsified: true}}}
	for _, n := range []string{"sort", "reverse"} {
		if r := exact.ruleFor(n); r == nil || !r.ForbidFalsified {
			t.Fatalf("exact name %q no longer matches", n)
		}
	}
	if r := exact.ruleFor("other"); r != nil {
		t.Fatal("a name listed nowhere must have no rule")
	}
	star := &Policy{Rules: []PolicyRule{{Names: []string{"*"}, RequireTotal: true}}}
	if r := star.ruleFor("anything"); r == nil || !r.RequireTotal {
		t.Fatal(`"*" no longer matches everything`)
	}
	var nilPol *Policy
	if r := nilPol.ruleFor("x"); r != nil {
		t.Fatal("a nil policy must yield no rule")
	}
}

func TestPatternSpecificityOrdering(t *testing.T) {
	name := "a/b/c"
	exact := patternSpecificity(name, name)
	deep := patternSpecificity("a/b/*", name)
	shallow := patternSpecificity("a/*", name)
	star := patternSpecificity("*", name)
	if !(exact > deep && deep > shallow && shallow > star) {
		t.Fatalf("specificity is not strictly ordered: exact=%d deep=%d shallow=%d star=%d", exact, deep, shallow, star)
	}
	if patternSpecificity("z/*", name) != -1 {
		t.Fatal("a non-matching pattern must score -1, not 0 — 0 is the catch-all's score")
	}
}
