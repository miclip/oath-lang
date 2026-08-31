package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"testing"
)

// A member's terms are read by its HASH identity (§12.2), so a publication under a
// name that is NOT bound in the store — the shape of a transitive closure member,
// which is never name-bound — is still found. A name-keyed read would miss it and
// report UNSTATED for terms the journal holds.
func TestLicenseOfHashReadsByHashEvenWhenUnbound(t *testing.T) {
	st := newMemStoreForTest(t)
	reps, err := apiPut(st, `(defn foo [] [] Int 1)`, "t", "")
	if err != nil || reps[0].Status != "accepted" {
		t.Fatalf("put: %v %+v", err, reps[0])
	}
	h := reps[0].Hash
	_, priv := newKey(t)
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))

	// Journal a valid signed publication under a name that is never bound (Repoint).
	env := pubEnvelope{Op: "put", Name: "vendor/foo", Artifact: h, Parent: noParent,
		ParentRev: big.NewInt(0), Author: pub, License: "GPL-3.0-only"}
	octets := envelopeEncode(env)
	sig := hex.EncodeToString(ed25519.Sign(priv, octets))
	if err := st.AppendLog(&LogEntry{Author: pub, Name: "vendor/foo", Kind: "func", Status: "accepted",
		Hash: h, ParentRev: "0", NameTransition: transitionApplied,
		EnvelopeB64: encodeEnvelopeB64(octets), AuthorPubkey: pub, AuthorSig: sig}); err != nil {
		t.Fatal(err)
	}
	if _, bound := st.Resolve("vendor/foo"); bound {
		t.Fatal("precondition: vendor/foo must not be bound, only journaled")
	}

	lic, name, _ := licenseOfHash(st, h)
	if lic != "GPL-3.0-only" || name != "vendor/foo" {
		t.Fatalf("licenseOfHash by hash = (%q,%q), want (GPL-3.0-only, vendor/foo)", lic, name)
	}
	// And the composition reads it: an unbound GPL member imposes share-alike.
	ev := evaluateLicensingSubject(st, "app", "app-artifact-hash", "MIT", "", []string{h})
	if ev.Result.ShareAlike != triYes {
		t.Errorf("an unbound GPL member's share-alike was not read: %+v", ev.Result)
	}
	if err := st.VerifyLog(); err != nil {
		t.Errorf("the hand-built publication entry must verify: %v", err)
	}
}
