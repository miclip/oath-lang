package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

const gateSrc = `(defn dbl [] [(n Int)] Int (+ n n)
  (prop twice [(x Int)] (== (dbl x) (* 2 x))))`

// artifactHashOf derives the hash the registry will compute, the same way a client
// must: elaborate locally, then hash. If this and the server ever disagree, the
// gate rejects — which is the cross-kernel determinism guarantee acting as an
// enforced precondition rather than an assumption.
func artifactHashOf(t *testing.T, st *Store, src string) string {
	t.Helper()
	forms, err := parseForms(src)
	if err != nil {
		t.Fatal(err)
	}
	def, _, err := elabFunc(st, forms[0])
	if err != nil {
		t.Fatal(err)
	}
	return hashDef(def)
}

// The gate must accept an honest statement and PERSIST it verbatim, and must
// reject every statement that does not describe the transition being requested —
// without moving the name.
func TestAuthorStatementGate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)

	build := func(t *testing.T) (*Store, pubEnvelope, string) {
		t.Helper()
		st := newMemStoreForTest(t)
		h := artifactHashOf(t, st, gateSrc)
		env := pubEnvelope{Op: "put", Name: "dbl", Artifact: h,
			Parent: noParent, ParentRev: firstRev(), Author: pubHex, License: noLicense}
		sig, err := envelopeSign(priv, env)
		if err != nil {
			t.Fatal(err)
		}
		return st, env, sig
	}

	t.Run("honest statement is accepted and persisted verbatim", func(t *testing.T) {
		st, env, sig := build(t)
		raw := string(envelopeEncode(env))
		reps, err := apiPutSigned(st, gateSrc, pubHex, "", &pubAuth{Bytes: raw, Sig: sig, Pubkey: pubHex})
		if err != nil {
			t.Fatalf("honest signed publication failed: %v (%+v)", err, reps)
		}
		if reps[0].Status != "accepted" {
			t.Fatalf("status = %q, want accepted (%s)", reps[0].Status, reps[0].Error)
		}
		var found *LogEntry
		for i, e := range st.ReadLog() {
			if e.Status == "accepted" {
				found = &st.ReadLog()[i]
			}
		}
		if found == nil {
			t.Fatal("no accepted entry journalled")
		}
		octets, derr := decodeEnvelopeB64(found.EnvelopeB64)
		if derr != nil {
			t.Fatalf("persisted envelope does not decode: %v", derr)
		}
		if string(octets) != raw {
			t.Fatalf("envelope octets were not persisted verbatim:\n got %q\nwant %q", octets, raw)
		}
		if found.AuthorPubkey != pubHex || found.AuthorSig != sig {
			t.Fatal("author key/signature not persisted")
		}
		// And the whole record must now verify as evidence.
		if err := st.VerifyLog(); err != nil {
			t.Fatalf("journal with a signed publication does not verify: %v", err)
		}
	})

	// Each case is a real attack or a real mistake, and none may move the name.
	for _, tc := range []struct {
		name    string
		mutate  func(*pubEnvelope)
		reSign  bool // re-sign after mutating: tests the FIELD check, not the signature
		wantErr string
	}{
		{"signature does not verify", func(e *pubEnvelope) { e.Artifact = strings.Repeat("9", 64) }, false, "does not verify"},
		{"signed a different name", func(e *pubEnvelope) { e.Name = "other" }, true, "signed name"},
		{"signed a different artifact", func(e *pubEnvelope) { e.Artifact = strings.Repeat("8", 64) }, true, "submitted content hashes to"},
		{"claims a parent on a fresh name", func(e *pubEnvelope) { e.Parent, e.ParentRev = strings.Repeat("7", 64), revOf(2) }, true, "currently points at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, env, sig := build(t)
			bad := env
			tc.mutate(&bad)
			raw := string(envelopeEncode(bad))
			if tc.reSign {
				var err error
				if sig, err = envelopeSign(priv, bad); err != nil {
					t.Fatal(err)
				}
			}
			reps, _ := apiPutSigned(st, gateSrc, pubHex, "", &pubAuth{Bytes: raw, Sig: sig, Pubkey: pubHex})
			if len(reps) == 0 || reps[0].Status != "rejected" {
				t.Fatalf("statement was not rejected: %+v", reps)
			}
			if !strings.Contains(reps[0].Error, tc.wantErr) {
				t.Fatalf("error %q does not mention %q", reps[0].Error, tc.wantErr)
			}
			if _, ok := st.Resolve("dbl"); ok {
				t.Fatal("the name MOVED on a rejected statement")
			}
		})
	}

	// A statement signed for a different key must not be accepted just because it
	// verifies against the key it names — the signer must be the AUTHENTICATED
	// principal, or a caller could replay someone else's statement.
	t.Run("envelope key is not the authenticated principal", func(t *testing.T) {
		st, env, sig := build(t)
		other, _, _ := ed25519.GenerateKey(nil)
		reps, _ := apiPutSigned(st, gateSrc, pubHex, "",
			&pubAuth{Bytes: string(envelopeEncode(env)), Sig: sig, Pubkey: hex.EncodeToString(other)})
		if len(reps) == 0 || reps[0].Status != "rejected" {
			t.Fatalf("statement accepted for a key that did not authenticate: %+v", reps)
		}
		if !strings.Contains(reps[0].Error, "authenticated as") {
			t.Fatalf("unexpected error: %q", reps[0].Error)
		}
	})
}

// The revision must count entries that MOVED the name, not merely accepted ones,
// and must NOT be advanced by attempts that never repointed.
//
// Both halves are load-bearing. Undercounting (missing a falsified repoint) lets
// an A→B→A cycle reuse a revision, reopening the ABA hole. Overcounting (advancing
// on a rejected attempt) would let one invalid submission invalidate an already
// prepared legitimate envelope, and make client and registry disagree about the
// current parent.
func TestNameRevisionCountsRepointsOnly(t *testing.T) {
	for _, tc := range []struct {
		status string
		moves  bool
	}{
		{"accepted", true},
		{"falsified", true}, // still binds the name — observed in the committed corpus
		{"rejected", false},
		{"blocked", false},
		{"pending", false},
	} {
		e := &LogEntry{Status: tc.status}
		if got := e.repointedName(); got != tc.moves {
			t.Fatalf("status %q: repointedName()=%v, want %v", tc.status, got, tc.moves)
		}
	}
}

// End-to-end: a rejected attempt must leave the parent/revision a later honest
// envelope was prepared against completely untouched.
func TestRejectedAttemptDoesNotDisturbRevision(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	st := newMemStoreForTest(t)

	h := artifactHashOf(t, st, gateSrc)
	honest := pubEnvelope{Op: "put", Name: "dbl", Artifact: h,
		Parent: noParent, ParentRev: firstRev(), Author: pubHex, License: noLicense}
	sig, err := envelopeSign(priv, honest)
	if err != nil {
		t.Fatal(err)
	}

	// A bogus attempt lands in the journal as rejected...
	bogus := honest
	bogus.Name = "elsewhere"
	bogusRaw := string(envelopeEncode(bogus))
	bogusSig, _ := envelopeSign(priv, bogus)
	_, _ = apiPutSigned(st, gateSrc, pubHex, "", &pubAuth{Bytes: bogusRaw, Sig: bogusSig, Pubkey: pubHex})

	if p, r := nameRevision(st, "dbl"); p != noParent || r != 0 {
		t.Fatalf("a rejected attempt disturbed the transition: parent=%s rev=%d", p, r)
	}
	// ...and the envelope prepared BEFORE it must still be accepted.
	reps, err := apiPutSigned(st, gateSrc, pubHex, "", &pubAuth{Bytes: string(envelopeEncode(honest)), Sig: sig, Pubkey: pubHex})
	if err != nil || len(reps) == 0 || reps[0].Status != "accepted" {
		t.Fatalf("a previously prepared envelope was invalidated by an unrelated rejected attempt: %+v (%v)", reps, err)
	}
}

// The bug the blind Rust implementation predicted from the spec alone: a same-hash
// re-publication is accepted, and the journal then fails its OWN verifier, because
// Repoint collapsed an unchanged binding to prev="" while the envelope named the
// real parent. A correctly signed, correctly accepted publication produced a broken
// journal.
func TestSameHashRepublicationKeepsJournalValid(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	st := newMemStoreForTest(t)

	publish := func() []putReport {
		t.Helper()
		parent, rev := nameRevision(st, "dbl")
		env := pubEnvelope{Op: "put", Name: "dbl", Artifact: artifactHashOf(t, st, gateSrc),
			Parent: parent, ParentRev: revOf(rev), Author: pubHex, License: noLicense}
		sig, err := envelopeSign(priv, env)
		if err != nil {
			t.Fatal(err)
		}
		reps, err := apiPutSigned(st, gateSrc, pubHex, "", &pubAuth{
			Bytes: string(envelopeEncode(env)), Sig: sig, Pubkey: pubHex})
		if err != nil {
			t.Fatalf("publish failed: %v", err)
		}
		return reps
	}

	publish()
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("first publication broke the journal: %v", err)
	}
	ownerBefore, srcBefore := nameOwner(st, "dbl")
	_, revBefore := nameRevision(st, "dbl")

	// Republish IDENTICAL content: a valid publication of the hash already bound.
	reps := publish()
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("same-hash re-publication broke the journal: %v", err)
	}
	if reps[0].Status != "accepted" {
		t.Fatalf("a no-op re-publication should still be accepted, got %q", reps[0].Status)
	}

	// It is a recorded no-op, so it must version nothing and own nothing.
	if _, rev := nameRevision(st, "dbl"); rev != revBefore {
		t.Fatalf("a no-op advanced the revision %d -> %d: revision must version the BINDING, not count publications", revBefore, rev)
	}
	owner, src := nameOwner(st, "dbl")
	if owner != ownerBefore || src != srcBefore {
		t.Fatalf("a no-op changed ownership from (%q,%q) to (%q,%q): re-publishing an unchanged artifact must not acquire its name", ownerBefore, srcBefore, owner, src)
	}
	// And the entry must say so rather than leaving it to inference.
	log := st.ReadLog()
	last := log[len(log)-1]
	if last.NameTransition != transitionUnchanged {
		t.Fatalf("no-op recorded name_transition=%q, want %q", last.NameTransition, transitionUnchanged)
	}
	if last.Prev != last.Hash {
		t.Fatalf("no-op recorded prev=%q, want the real prior binding %q", last.Prev, last.Hash)
	}
}

// An envelope stays valid across everything that leaves the name state unchanged:
// rejected attempts, prove/cross events, and same-hash re-publications. It becomes
// invalid only once an applied transition changes the binding. That is clean CAS.
func TestEnvelopeSurvivesNonTransitions(t *testing.T) {
	st := newMemStoreForTest(t)
	// Bind the name for real: nameRevision reads the journal AND resolves the name,
	// so a journal-only fixture would describe a store that cannot exist.
	if _, err := st.Repoint("n", "h1"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendLog(&LogEntry{Name: "n", Kind: "func", Status: "accepted",
		Hash: "h1", NameTransition: transitionApplied}); err != nil {
		t.Fatal(err)
	}
	_, rev := nameRevision(st, "n")
	if rev != 1 {
		t.Fatalf("setup: revision %d after one applied transition, want 1", rev)
	}

	for _, e := range []*LogEntry{
		{Name: "n", Kind: "func", Status: "rejected", Hash: "h9", NameTransition: transitionNone},
		{Name: "n", Kind: "func", Status: "blocked", Hash: "h9", NameTransition: transitionNone},
		{Name: "n", Kind: "prove", Status: "accepted", Hash: "h1", NameTransition: transitionNone},
		{Name: "n", Kind: "cross", Status: "accepted", Hash: "h1", NameTransition: transitionNone},
		{Name: "n", Kind: "func", Status: "accepted", Hash: "h1", Prev: "h1", NameTransition: transitionUnchanged},
	} {
		if err := st.AppendLog(e); err != nil {
			t.Fatal(err)
		}
		if _, got := nameRevision(st, "n"); got != rev {
			t.Fatalf("%s/%s advanced the revision to %d (was %d): an envelope prepared against the old state would be invalidated by an event that changed nothing",
				e.Kind, e.Status, got, rev)
		}
	}
	// A real change DOES advance it.
	if _, err := st.Repoint("n", "h2"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendLog(&LogEntry{Name: "n", Kind: "func", Status: "accepted",
		Hash: "h2", Prev: "h1", NameTransition: transitionApplied}); err != nil {
		t.Fatal(err)
	}
	if _, got := nameRevision(st, "n"); got != rev+1 {
		t.Fatalf("an applied transition did not advance the revision: %d, want %d", got, rev+1)
	}
}

// Legacy entries have no name_transition, so it is derived — and the derivation must
// exclude kinds that never touched a name. Missing that is how a proof-worker entry
// inflates a name's revision.
func TestLegacyTransitionDerivationExcludesNonPutKinds(t *testing.T) {
	for _, tc := range []struct {
		kind, status, prev, hash, want string
	}{
		{"func", "accepted", "", "h", transitionApplied},
		{"data", "accepted", "", "h", transitionApplied},
		{"func", "falsified", "", "h", transitionApplied},
		{"func", "accepted", "h", "h", transitionUnchanged},
		{"prove", "accepted", "", "h", transitionNone},
		{"cross", "accepted", "", "h", transitionNone},
		{"cross", "falsified", "", "h", transitionNone},
		{"func", "rejected", "", "h", transitionNone},
		{"func", "blocked", "", "h", transitionNone},
	} {
		e := &LogEntry{Kind: tc.kind, Status: tc.status, Prev: tc.prev, Hash: tc.hash}
		if got := e.nameTransitionOf(); got != tc.want {
			t.Fatalf("legacy %s/%s: derived %q, want %q", tc.kind, tc.status, got, tc.want)
		}
	}
}

// The legacy revision derivation must FOLD, not test prev==hash per entry.
//
// Pre-amendment entries omitted `prev` when the name already pointed at the same
// hash, so a legacy no-op carries no prev at all — and an absent prev is ambiguous
// between "new name" and "already here". A per-entry test therefore misses every
// legacy no-op and counts each re-publication as a state change. Measured on the
// committed corpus when this was wrong: 169 of 187 names inflated.
func TestLegacyNoOpRequiresFolding(t *testing.T) {
	// A legacy history: first publication, then six identical re-puts, all with the
	// prev field ABSENT exactly as the old rule wrote them.
	entries := []LogEntry{{Seq: 1, Name: "n", Kind: "func", Status: "accepted", Hash: "hA"}}
	for i := 2; i <= 7; i++ {
		entries = append(entries, LogEntry{Seq: i, Name: "n", Kind: "func", Status: "accepted", Hash: "hA"})
	}
	got := nameTransitions(entries, "n")
	if len(got) != 7 {
		t.Fatalf("fold returned %d entries, want 7", len(got))
	}
	if got[0].Transition != transitionApplied {
		t.Fatalf("first publication: %q, want applied", got[0].Transition)
	}
	for i := 1; i < 7; i++ {
		if got[i].Transition != transitionUnchanged {
			t.Fatalf("entry %d re-published the SAME hash and was classified %q — a per-entry test on an absent prev cannot see this, which is why the derivation must fold", i+1, got[i].Transition)
		}
	}

	// A genuine A→B→A cycle must still count three applied transitions, or ABA
	// protection lapses.
	aba := []LogEntry{
		{Seq: 1, Name: "m", Kind: "func", Status: "accepted", Hash: "hA"},
		{Seq: 2, Name: "m", Kind: "func", Status: "accepted", Hash: "hB"},
		{Seq: 3, Name: "m", Kind: "func", Status: "accepted", Hash: "hA"},
	}
	applied := 0
	for _, tr := range nameTransitions(aba, "m") {
		if tr.Transition == transitionApplied {
			applied++
		}
	}
	if applied != 3 {
		t.Fatalf("A→B→A counted %d applied transitions, want 3: an old envelope for the first A must carry a stale revision", applied)
	}

	// Declared transitions always win over derivation.
	declared := []LogEntry{{Seq: 1, Name: "d", Kind: "func", Status: "accepted",
		Hash: "hA", NameTransition: transitionUnchanged}}
	if got := nameTransitions(declared, "d"); got[0].Transition != transitionUnchanged {
		t.Fatalf("declared transition was overridden by derivation: %q", got[0].Transition)
	}
}
