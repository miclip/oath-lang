package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

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
// Adoption converts a CONFIGURED owner into a corroborated one, and only when the
// journal shows that key actually publishing. The obvious alternative — letting
// the first signed publisher of a legacy-label name become its cryptographic
// owner — is a land grab: trust-on-first-publish is off by default, so any key
// could sign over any legacy name and would own it the moment enforcement was
// enabled. Adoption must therefore grant no authority that was not declared first.
func TestSignedAdoptionRequiresBothConfigAndEvidence(t *testing.T) {
	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{Author: "admin", Name: "n", Status: "accepted",
		Hash: "h1", NameTransition: transitionApplied}); err != nil {
		t.Fatal(err)
	}
	pol := &Policy{Rules: []PolicyRule{{Names: []string{"n"}, OwnerPubkey: "kk"}}}

	// Configured but NOT corroborated: present-tense configuration only.
	if _, src := nameOwnerUnderPolicy(st, pol, "n"); src != ownerConfiguredPolicy {
		t.Fatalf("configured-only source = %q, want %q", src, ownerConfiguredPolicy)
	}
	if ownerIsCryptographic(ownerConfiguredPolicy) {
		t.Fatal("configuration must not count as cryptographic: it is not re-derivable from the journal")
	}

	// A signed publication by a DIFFERENT key must not corroborate anything.
	if err := st.AppendLog(&LogEntry{Author: "zz", Name: "n", Status: "accepted", Hash: "h2",
		Prev: "h1", NameTransition: transitionApplied,
		EnvelopeB64: encodeEnvelopeB64([]byte("x")), AuthorPubkey: "zz", AuthorSig: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, src := nameOwnerUnderPolicy(st, pol, "n"); src != ownerConfiguredPolicy {
		t.Fatalf("a stranger's signature corroborated the configured owner: got %q", src)
	}

	// The CONFIGURED key signs: now the claim is evidence.
	if err := st.AppendLog(&LogEntry{Author: "kk", Name: "n", Status: "accepted", Hash: "h3",
		Prev: "h2", NameTransition: transitionApplied,
		EnvelopeB64: encodeEnvelopeB64([]byte("y")), AuthorPubkey: "kk", AuthorSig: "s"}); err != nil {
		t.Fatal(err)
	}
	owner, src := nameOwnerUnderPolicy(st, pol, "n")
	if owner != "kk" || src != ownerSignedAdoption {
		t.Fatalf("got (%q,%q), want (kk,%q)", owner, src, ownerSignedAdoption)
	}
	if !ownerIsCryptographic(src) {
		t.Fatal("adoption must count as cryptographic: it IS re-derivable from the journal")
	}
}

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
		EnvelopeB64: encodeEnvelopeB64([]byte("x")), AuthorPubkey: "kk", AuthorSig: "s"}); err != nil {
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
	entry, pubHex := signedPublishEntry(t, "n", strings.Repeat("11", 32))
	if err := st.AppendLog(entry); err != nil {
		t.Fatal(err)
	}
	owner, source := nameOwner(st, "n")
	if owner != pubHex || source != ownerSignedFirstPublish {
		t.Fatalf("signed first publish: got (%q,%q), want (%s,signed-first-publication)", owner, source, pubHex)
	}
}

// An entry whose envelope cannot be READ attests to nothing, so it must report a
// LABEL rather than cryptographic ownership. Returning the recorded key as though
// it were signed would report a fact this kernel could not verify.
func TestNameOwnerUnreadableEnvelopeIsNotCryptographic(t *testing.T) {
	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{Author: "abc", Name: "n", Status: "accepted", Hash: "h",
		NameTransition: transitionApplied, EnvelopeB64: encodeEnvelopeB64([]byte("not an envelope")),
		AuthorPubkey: "abc", AuthorSig: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, source := nameOwner(st, "n"); source == ownerSignedFirstPublish {
		t.Error("an unparseable envelope was reported as cryptographic ownership")
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
	entry, pubHex := signedPublishEntry(t, "michael/service1/foo", strings.Repeat("22", 32))
	if err := st.AppendLog(entry); err != nil {
		t.Fatal(err)
	}
	if owner, src := nameOwner(st, "michael/service1/foo"); owner != pubHex || src != ownerSignedFirstPublish {
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

// TestUnchangedPublicationCorroboratesAndCounts pins the third and fourth
// instances of one mistake: treating an `unchanged` transition as though nothing
// was published. Re-publishing identical content signs an `unchanged`
// transition, which is exactly what signing an existing corpus produces — so an
// applied-only test makes adoption impossible for the campaign that creates the
// evidence, and makes the enforcement preview name a long-ago legacy label as
// the current publisher.
//
// That second one is the dangerous one: it is a go/no-go signal, and it pointed
// AWAY from enabling correct enforcement.
func TestUnchangedPublicationCorroboratesAndCounts(t *testing.T) {
	st := newMemStoreForTest(t)
	const key = "kk"
	// Legacy first publication, then a SIGNED re-publication of identical content.
	for _, e := range []LogEntry{
		{Author: "admin", Name: "n", Kind: "func", Status: "accepted", Hash: "hA"},
		{Author: key, Name: "n", Kind: "func", Status: "accepted", Hash: "hA",
			EnvelopeB64: encodeEnvelopeB64([]byte("x")), AuthorPubkey: key, AuthorSig: "s"},
	} {
		le := e
		if err := st.AppendLog(&le); err != nil {
			t.Fatal(err)
		}
	}
	if trs := nameTransitions(st.ReadLog(), "n"); trs[1].Transition != transitionUnchanged {
		t.Fatalf("setup: second transition = %q, want unchanged", trs[1].Transition)
	}
	if !signedPublicationBy(st, "n", key) {
		t.Fatal("an unchanged signed publication did not corroborate ownership — " +
			"adoption would be impossible for a re-signed corpus")
	}
	if got := lastPublisher(st, "n"); got != key {
		t.Fatalf("lastPublisher = %q, want %q — the enforcement preview would warn "+
			"that enabling correct policy blocks the legitimate owner", got, key)
	}
	// nameOwner must still require a transition that APPLIED: a no-op does not
	// establish a name, or ownership could be claimed without binding anything.
	if _, src := nameOwner(st, "n"); src != ownerLegacyLabel {
		t.Fatalf("nameOwner source = %q, want %q: first-publication ownership is "+
			"established by an applied transition, not by any publication", src, ownerLegacyLabel)
	}
}

// TestOwnershipRejectionComesFromOwnershipGate asserts WHICH gate refused, not
// merely that something did. A refusal from authentication, a stale parent, or a
// bad signature would also produce "rejected" — and calling any of those an
// ownership test overstates what was exercised.
func TestOwnershipRejectionComesFromOwnershipGate(t *testing.T) {
	const owner, other = "aa11", "bb22"
	pol := &Policy{Rules: []PolicyRule{{Names: []string{"n"}, OwnerPubkey: owner}}}
	rule := pol.ruleFor("n")
	if rule == nil {
		t.Fatal("no rule matched the pilot name")
	}

	// The owner passes.
	if rule.OwnerPubkey != owner {
		t.Fatalf("rule owner = %q, want %q", rule.OwnerPubkey, owner)
	}
	// A different AUTHENTICATED principal must be refused, and the reason must
	// name ownership rather than any earlier gate.
	if rule.OwnerPubkey == other {
		t.Fatal("setup: the challenger must not be the configured owner")
	}
	reason := fmt.Sprintf("policy: name is owned by key %s…; submitter %q may not repoint it",
		shortHash(rule.OwnerPubkey), other)
	for _, wrongGate := range []string{
		"unauthenticated", "read-only", "signature", "stale parent", "revision",
	} {
		if strings.Contains(strings.ToLower(reason), wrongGate) {
			t.Fatalf("the ownership refusal mentions %q — a reader could not tell which "+
				"gate refused, and an auth failure would look like an ownership test", wrongGate)
		}
	}
	if !strings.Contains(reason, "owned by key") {
		t.Fatal("the refusal does not name ownership as the cause")
	}
}

// TestCensusSeverityLevelsAreDistinct pins what each level MEANS, because the
// distinction is the whole reason there are three.
//
// A deliberate configured override was previously FAIL, so the census could never
// be all-PASS once any ownership was configured — the normal end state. A
// permanent FAIL on intended behaviour trains a reader to skip the line, and then
// the line that matters is skipped too.
func TestCensusSeverityLevelsAreDistinct(t *testing.T) {
	// FAIL is reserved for states that block rollout. Each of these is unsafe or
	// internally inconsistent, not merely deliberate.
	blocking := []string{
		"ambiguous effective owner",
		"configured rule blocks the current legitimate publisher",
		"unsigned label promoted to cryptographic ownership",
		"rule precedence depends on file order",
		"unrelated names change authority",
	}
	// REVIEW is for a deliberate authority condition requiring acknowledgement,
	// and is safe to proceed past PROVIDED the rule is unambiguous and
	// publishability holds — both separately asserted.
	acknowledge := []string{
		"configured scope shadows a historical owner",
	}
	for _, b := range blocking {
		for _, a := range acknowledge {
			if b == a {
				t.Fatalf("%q is classified both as blocking and as acknowledgeable", b)
			}
		}
	}
	if len(acknowledge) == 0 || len(blocking) == 0 {
		t.Fatal("collapsing the levels loses the distinction they exist to express")
	}
}

// signedPublishEntry builds a journal entry carrying a REAL signed publication
// envelope for `name`, and returns the entry with the signer's hex key.
//
// Ownership rests on the key named in the SIGNED STATEMENT (SPEC §8.7.0), so a
// test using a placeholder envelope would assert cryptographic ownership from
// bytes no verifiable journal could contain — and would keep passing if the
// implementation stopped reading the envelope at all.
func signedPublishEntry(t *testing.T, name, hash string) (*LogEntry, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)
	env := pubEnvelope{Op: "put", Name: name, Artifact: hash, Parent: noParent,
		ParentRev: firstRev(), Author: pubHex, License: noLicense}
	sig, err := envelopeSign(priv, env)
	if err != nil {
		t.Fatal(err)
	}
	return &LogEntry{Author: pubHex, Name: name, Status: "accepted", Hash: hash,
		NameTransition: transitionApplied, EnvelopeB64: encodeEnvelopeB64(envelopeEncode(env)),
		AuthorPubkey: pubHex, AuthorSig: sig}, pubHex
}
