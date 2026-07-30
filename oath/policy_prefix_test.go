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

// Trust on first publish: the first principal to publish a name owns it, and the
// derivation must report whether that owner is a KEY or a bare LABEL.
func TestNameOwnerDerivation(t *testing.T) {
	st := newMemStoreForTest(t)
	// An UNSIGNED first publication: the owner is a label the store wrote down.
	if err := st.AppendLog(&LogEntry{Author: "claude-main", Name: "n", Status: "accepted",
		Hash: "h1", NameTransition: transitionApplied}); err != nil {
		t.Fatal(err)
	}
	owner, source := nameOwner(st, "n")
	if owner != "claude-main" || source != ownerLegacyLabel {
		t.Fatalf("unsigned first publish: got (%q,%q), want (claude-main,legacy-label)", owner, source)
	}
	// A later signed entry must NOT change ownership — first publish decides.
	if err := st.AppendLog(&LogEntry{Author: "kk", Name: "n", Status: "accepted", Hash: "h2",
		Prev: "h1", NameTransition: transitionApplied,
		Envelope: "x", AuthorPubkey: "kk", AuthorSig: "s"}); err != nil {
		t.Fatal(err)
	}
	if owner, _ := nameOwner(st, "n"); owner != "claude-main" {
		t.Fatalf("ownership moved to a later publisher: got %q — trust on FIRST publish", owner)
	}
	// A name never published has no owner, which must be distinguishable from
	// "owned by nobody in particular".
	if owner, _ := nameOwner(st, "absent"); owner != "" {
		t.Fatalf("an unpublished name reported owner %q", owner)
	}
	// An entry that did NOT apply a transition must not establish ownership: a
	// rejected attempt is not a claim.
	st2 := newMemStoreForTest(t)
	if err := st2.AppendLog(&LogEntry{Author: "squatter", Name: "m", Status: "rejected", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	if owner, _ := nameOwner(st2, "m"); owner != "" {
		t.Fatalf("a REJECTED attempt established ownership as %q — squatting by failed submission", owner)
	}
}

// A signed first publication yields KEY ownership.
func TestNameOwnerFromSignedEntry(t *testing.T) {
	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{Author: "abc", Name: "n", Status: "accepted", Hash: "h",
		NameTransition: transitionApplied, Envelope: "e", AuthorPubkey: "abc", AuthorSig: "s"}); err != nil {
		t.Fatal(err)
	}
	owner, source := nameOwner(st, "n")
	if owner != "abc" || source != ownerSignedFirstPublish {
		t.Fatalf("signed first publish: got (%q,%q), want (abc,signed-first-publication)", owner, source)
	}
}

// TrustOnFirstPublish must be OPT-IN: without it, derived ownership imposes
// nothing, or enabling prefix rules would retroactively freeze existing names.
func TestTrustOnFirstPublishIsOptIn(t *testing.T) {
	off := &Policy{Rules: []PolicyRule{{Names: []string{"*"}}}}
	if r := off.ruleFor("n"); r == nil || r.TrustOnFirstPublish {
		t.Fatal("trust_on_first_publish must default to false")
	}
}

// An ambiguous policy must be REJECTED at load, not silently resolved. A policy
// that enforces something while the operator believes it enforces what they wrote
// is worse than one that refuses to load.
func TestPolicyRejectsAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name string
		pol  Policy
	}{
		{"duplicate pattern across rules", Policy{Rules: []PolicyRule{
			{Names: []string{"michael/*"}, OwnerPubkey: "aa"},
			{Names: []string{"michael/*"}, OwnerPubkey: "bb"},
		}}},
		{"degenerate empty-prefix pattern", Policy{Rules: []PolicyRule{{Names: []string{"/*"}}}}},
		{"empty names list", Policy{Rules: []PolicyRule{{Names: nil}}}},
		{"empty pattern", Policy{Rules: []PolicyRule{{Names: []string{""}}}}},
		{"star used as a glob", Policy{Rules: []PolicyRule{{Names: []string{"mich*el"}}}}},
		{"star inside a prefix", Policy{Rules: []PolicyRule{{Names: []string{"a*b/*"}}}}},
	} {
		if err := tc.pol.validate(); err == nil {
			t.Fatalf("%s: accepted — an ambiguous or malformed policy must not load", tc.name)
		}
	}
	// A well-formed nested policy must still load: nesting is not ambiguity.
	ok := Policy{Rules: []PolicyRule{
		{Names: []string{"*"}},
		{Names: []string{"michael/*"}},
		{Names: []string{"michael/service1/*"}},
		{Names: []string{"michael/service1/webhook"}},
	}}
	if err := ok.validate(); err != nil {
		t.Fatalf("a validly nested policy was rejected: %v", err)
	}
}

// TOFU establishes ownership of an EXACT NAME only. Publishing one child must not
// capture the namespace, or a single publication becomes a land grab.
func TestTofuDoesNotCaptureNamespace(t *testing.T) {
	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{Author: "kk", Name: "michael/service1/foo",
		Status: "accepted", Hash: "h", NameTransition: transitionApplied,
		Envelope: "e", AuthorPubkey: "kk", AuthorSig: "s"}); err != nil {
		t.Fatal(err)
	}
	if owner, src := nameOwner(st, "michael/service1/foo"); owner != "kk" || src != ownerSignedFirstPublish {
		t.Fatalf("exact name not owned: (%q,%q)", owner, src)
	}
	// Nothing else in or above that namespace is owned by publishing one child.
	for _, other := range []string{"michael/service1/bar", "michael/service1", "michael", "michael/other/foo"} {
		if owner, _ := nameOwner(st, other); owner != "" {
			t.Fatalf("publishing michael/service1/foo captured %q (owner %q): TOFU must be exact-name only", other, owner)
		}
	}
}

// A FALSIFIED but applied first publication establishes the name; a rejected one
// does not. Both directions matter (rule 3).
func TestOwnershipFromFalsifiedButApplied(t *testing.T) {
	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{Author: "who", Name: "n", Status: "falsified",
		Hash: "h", NameTransition: transitionApplied}); err != nil {
		t.Fatal(err)
	}
	if owner, _ := nameOwner(st, "n"); owner != "who" {
		t.Fatal("a falsified-but-APPLIED first publication must establish the name: it still binds it")
	}
}

// Configured policy names a key but is NOT historical evidence — it must not be
// treated as cryptographic ownership.
func TestConfiguredPolicyIsNotCryptographicEvidence(t *testing.T) {
	if ownerIsCryptographic(ownerConfiguredPolicy) {
		t.Fatal("an operator-editable policy file must not count as cryptographic ownership evidence")
	}
	if ownerIsCryptographic(ownerLegacyLabel) {
		t.Fatal("a legacy label must not count as cryptographic ownership evidence")
	}
	if !ownerIsCryptographic(ownerSignedFirstPublish) {
		t.Fatal("a signed first publication IS re-verifiable from the journal")
	}
}
