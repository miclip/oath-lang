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
			Parent: noParent, ParentRev: firstRev, Author: pubHex}
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
		if found.Envelope != raw {
			t.Fatalf("envelope was not persisted verbatim:\n got %q\nwant %q", found.Envelope, raw)
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
		{"claims a parent on a fresh name", func(e *pubEnvelope) { e.Parent, e.ParentRev = strings.Repeat("7", 64), 2 }, true, "currently points at"},
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
		Parent: noParent, ParentRev: firstRev, Author: pubHex}
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

	if p, r := nameRevision(st, "dbl"); p != noParent || r != firstRev {
		t.Fatalf("a rejected attempt disturbed the transition: parent=%s rev=%d", p, r)
	}
	// ...and the envelope prepared BEFORE it must still be accepted.
	reps, err := apiPutSigned(st, gateSrc, pubHex, "", &pubAuth{Bytes: string(envelopeEncode(honest)), Sig: sig, Pubkey: pubHex})
	if err != nil || len(reps) == 0 || reps[0].Status != "accepted" {
		t.Fatalf("a previously prepared envelope was invalidated by an unrelated rejected attempt: %+v (%v)", reps, err)
	}
}
