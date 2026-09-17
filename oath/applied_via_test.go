package main

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
)

// The defect this guards is silent and total: applied_via is chosen by the STORE,
// so if it were inside the signed content a signer would sign it empty, the store
// would persist it populated, and every honest signed entry would fail its own
// verification (SPEC §8.4). The control is the mutation — flip the field AFTER
// signing and the signature must still verify, because it is not covered.
func TestAppliedViaIsExcludedFromTheEntrySignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	e := LogEntry{Seq: 1, Author: "a", Name: "n", Status: "accepted", Hash: "h"}
	sig := ed25519.Sign(priv, signedContent(&e))

	for _, v := range []string{"", appliedViaNone, appliedViaAdvisory, appliedViaTxnCAS} {
		e.AppliedVia = v
		if !ed25519.Verify(pub, signedContent(&e), sig) {
			t.Fatalf("applied_via=%q changed the signed content: the member is inside the signature", v)
		}
	}
	// Control: a field that IS authored must break it, or the test above would
	// pass for a signedContent that zeroed everything.
	e.Name = "other"
	if ed25519.Verify(pub, signedContent(&e), sig) {
		t.Fatal("mutating an AUTHORED field left the signature valid: signedContent is not discriminating")
	}
}

// Absent is a fourth state. Collapsing it into "none" would fabricate a positive
// claim — the weakest one — on behalf of every entry written before the member
// existed (SPEC §8.6.3).
func TestAbsentAppliedViaIsNotNone(t *testing.T) {
	m := appliedViaCensus([]LogEntry{{}, {AppliedVia: appliedViaNone}})
	if m[""] != 1 || m[appliedViaNone] != 1 {
		t.Fatalf("absent and none were conflated: %v", m)
	}
	if got := appliedViaLabel(""); !strings.Contains(got, "NOT RECORDED") {
		t.Fatalf("absent rendered as a claim: %q", got)
	}
	// The rendering must never assert the mechanism as fact.
	for _, v := range []string{appliedViaNone, appliedViaAdvisory, appliedViaTxnCAS} {
		if got := appliedViaLabel(v); !strings.Contains(got, "the store states") {
			t.Fatalf("applied_via=%q rendered without naming the speaker: %q", v, got)
		}
	}
}

// A DEFINED member carrying an undefined value survives the §8.2.1 canonical
// re-encode unchanged, so the unknown-member rule cannot see it. Only an explicit
// check can.
func TestVerifyLogRejectsAnUndefinedAppliedVia(t *testing.T) {
	if !validAppliedVia("") || !validAppliedVia(appliedViaTxnCAS) {
		t.Fatal("a defined value was rejected")
	}
	if validAppliedVia("transaction") || validAppliedVia("TRANSACTIONAL-CAS") {
		t.Fatal("an undefined value was accepted: near-misses are exactly what a store would write by mistake")
	}
}

// The regime is NOT a property of the backend type: the fs lock is a no-op unless
// OATH_STORE_LOCK is set, so answering from the type would stamp advisory-lock on
// writes where no lock was ever taken.
func TestFsAppliedViaTracksWhetherTheLockIsActuallyTaken(t *testing.T) {
	f := &fsBackend{root: t.TempDir()}
	t.Setenv("OATH_STORE_LOCK", "")
	if got := f.appliedVia(); got != appliedViaNone {
		t.Fatalf("lock disabled but reported %q — a claim of coordination that did not happen", got)
	}
	t.Setenv("OATH_STORE_LOCK", "1")
	if got := f.appliedVia(); got != appliedViaAdvisory {
		t.Fatalf("lock enabled but reported %q", got)
	}
}

// No backend in this repo makes the compare, the name update and the append one
// operation — AppendLog takes its own lock independently of the name write. This
// pins that none of them CLAIMS otherwise; it is the check that fails loudly if a
// future driver is labelled transactional-cas without earning it.
func TestNoBackendClaimsTransactionalCAS(t *testing.T) {
	for name, be := range map[string]backend{"fs": &fsBackend{root: t.TempDir()}, "mem": newMemBackend()} {
		if got := be.appliedVia(); got == appliedViaTxnCAS {
			t.Fatalf("%s claims transactional-cas; SPEC §8.6.3 requires compare+update+append to be ONE operation", name)
		}
	}
}

// Member order is normative (§8.2.1): applied_via sits after name_transition and
// before chain. Two implementations ordering it differently compute different
// chains and signatures for the same logical entry.
func TestAppliedViaCanonicalMemberPosition(t *testing.T) {
	b, err := canonicalJournalLine(&LogEntry{Seq: 1, NameTransition: transitionApplied, AppliedVia: appliedViaAdvisory, Chain: "c"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	nt, av, ch := strings.Index(s, `"name_transition"`), strings.Index(s, `"applied_via"`), strings.Index(s, `"chain"`)
	if nt < 0 || av < 0 || ch < 0 || !(nt < av && av < ch) {
		t.Fatalf("member order is wrong (name_transition=%d applied_via=%d chain=%d): %s", nt, av, ch, s)
	}
	// Absent must be OMITTED, not emitted empty — an emitted "" would change the
	// bytes of every historical entry's re-encoding.
	b2, _ := canonicalJournalLine(&LogEntry{Seq: 1})
	if strings.Contains(string(b2), "applied_via") {
		t.Fatalf("empty applied_via was emitted; historical entries would no longer re-encode: %s", b2)
	}
	var round LogEntry
	if err := json.Unmarshal(b, &round); err != nil || round.AppliedVia != appliedViaAdvisory {
		t.Fatalf("round-trip lost the member: %v %q", err, round.AppliedVia)
	}
}

// A later entry that moves no name must not lend its declaration to the write
// that actually bound the name. The mechanism is a fact about ONE write, so
// selecting the wrong entry attributes the wrong one — silently, since both
// entries are honest about themselves.
//
// The prove entry here leaves name_transition EMPTY on purpose: that is what the
// proof path writes, and it is the case a check against the stored member waves
// through. Only the DERIVED transition (§8.6.4) excludes it.
func TestBindingAppliedViaIgnoresEntriesThatMoveNoName(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The two writes must land under DIFFERENT regimes, or the assertion cannot
	// tell which entry was read. Toggling the lock makes AppendLog stamp them
	// differently for real, in the store — mutating a ReadLog() copy would not,
	// since ReadLog returns values.
	t.Setenv("OATH_STORE_LOCK", "")
	if err := st.AppendLog(&LogEntry{Author: "a", Name: "n", Kind: "func", Status: "accepted",
		Hash: "h1", NameTransition: transitionApplied}); err != nil {
		t.Fatal(err)
	}
	// Kind "prove": moves no name, and states NO transition at all — which is what
	// the proof path writes, and the case a check against the STORED member waves
	// through.
	t.Setenv("OATH_STORE_LOCK", "1")
	if err := st.AppendLog(&LogEntry{Author: "a", Name: "n", Kind: "prove", Status: "accepted",
		Hash: "h1"}); err != nil {
		t.Fatal(err)
	}

	entries := st.ReadLog()
	if len(entries) != 2 || entries[0].AppliedVia == entries[1].AppliedVia {
		t.Fatalf("control failed: the two writes did not record different mechanisms (%+v)", entries)
	}
	want, decoy := entries[0].AppliedVia, entries[1].AppliedVia
	got := bindingAppliedVia(st, "n", "h1")
	if got == decoy {
		t.Fatalf("read the PROVE entry's mechanism %q; the name was bound by a write declaring %q", decoy, want)
	}
	if got != want {
		t.Fatalf("bindingAppliedVia returned %q, want the BINDING entry's %q", got, want)
	}
	if v := bindingAppliedVia(st, "absent", "h1"); v != "" {
		t.Fatalf("unbound name yielded %q", v)
	}
}

// The declaration is never attested: §8.4 excludes it from the signed content, so
// a SIGNED entry must not be rendered as signing it either. This pins the
// rendering against the wording the issue originally specified.
func TestAppliedViaIsNeverRenderedAsSigned(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &explainPkg{Provenance: explainProv{AppliedVia: appliedViaAdvisory}}
	var joined string
	for _, l := range explainLimitations(st, p, &Meta{}) {
		joined += l + "\n"
	}
	if !strings.Contains(joined, "registry-RECORDED") {
		t.Fatalf("mechanism not rendered at all:\n%s", joined)
	}
	for _, banned := range []string{"attested", "signed:", "(signed)"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("rendering claims %q for a member no signature covers:\n%s", banned, joined)
		}
	}
}

// §8.5's verification worker repoints a name by journaling a `put`-kind
// `accepted` entry. §8.6.2's kind list omitted `put`, so that write derived as
// `none`: the binding moved and the revision did not, which lapses ABA replay
// protection for every name bound through the async proof gate.
//
// Nothing available to either kernel could catch this. No conformance vector
// reaches it, and the committed corpus contains no `put` entry at all — it is
// published directly rather than through the gate — so the defect lives only
// where the worker runs. Found by reading §8.5 and §8.6.2 together.
func TestPutKindRepointAppliesATransition(t *testing.T) {
	for _, tc := range []struct {
		name   string
		e      LogEntry
		bound  string
		expect string
	}{
		{"worker repoint moves the binding",
			LogEntry{Name: "n", Kind: "put", Status: "accepted", Hash: "h2", Prev: "h1"}, "h1", transitionApplied},
		{"worker repoint to the SAME hash is still a no-op",
			LogEntry{Name: "n", Kind: "put", Status: "accepted", Hash: "h1"}, "h1", transitionUnchanged},
		{"a blocked put moves nothing",
			LogEntry{Name: "n", Kind: "put", Status: "blocked", Hash: "h2"}, "h1", transitionNone},
		// Controls: the kinds that must STILL apply nothing, or widening the list
		// would inflate every name's revision instead of correcting one case.
		{"prove concerns an artifact",
			LogEntry{Name: "n", Kind: "prove", Status: "accepted", Hash: "h2"}, "h1", transitionNone},
		{"cross concerns an artifact",
			LogEntry{Name: "n", Kind: "cross", Status: "accepted", Hash: "h2"}, "h1", transitionNone},
	} {
		if got := deriveTransition(&tc.e, tc.bound); got != tc.expect {
			t.Errorf("%s: derived %q, want %q", tc.name, got, tc.expect)
		}
	}
}
