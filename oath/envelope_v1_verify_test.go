package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

// A journal holding a valid `oath-publish/1` publication must verify.
//
// WHY THIS NEEDED A WITNESS. `VerifyLog` recovers the envelope OCTETS and its
// own comment says it verifies over them — but it called `envelopeVerify`,
// which RE-ENCODES under the current format. `/2` appends a `license` line that
// `/1` does not carry, so the bytes checked were bytes no author ever signed
// and a perfectly good historical publication failed. SPEC §8.6.1 requires a
// historical statement be checked against the octets its author signed.
//
// The committed journal happens to hold no signed entries at all, so nothing in
// the corpus or the conformance suite could have caught this — which is why the
// entry here is constructed rather than sampled.
func TestVerifyLogAcceptsV1Envelope(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubHex := hex.EncodeToString(pub)
	artifact := strings.Repeat("b", 64)

	env := pubEnvelope{Op: "put", Name: "n", Artifact: artifact,
		Parent: noParent, ParentRev: firstRev(), Author: pubHex, License: noLicense}

	// Sign the `/1` OCTETS, which is what a kernel of that era produced. Not
	// envelopeSign, which signs a re-encoding under the CURRENT version.
	raw := envelopeEncodeAs(env, envelopeVersionV1)
	if strings.Contains(string(raw), "license=") {
		t.Fatalf("the /1 encoding carries a license line, so this test cannot see the defect:\n%s", raw)
	}
	sig := hex.EncodeToString(ed25519.Sign(priv, raw))

	st := newMemStoreForTest(t)
	if err := st.AppendLog(&LogEntry{
		Author: pubHex, Name: "n", Kind: "func", Status: "accepted", Hash: artifact,
		EnvelopeB64: encodeEnvelopeB64(raw), AuthorPubkey: pubHex, AuthorSig: sig,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("a valid /1 publication failed to verify: %v", err)
	}

	// CONTROL: the same entry with a signature over the /2 re-encoding must be
	// REFUSED. Without this, a VerifyLog that skipped signature checking
	// entirely would pass the assertion above.
	bad := hex.EncodeToString(ed25519.Sign(priv, envelopeEncodeAs(env, envelopeVersion)))
	st2 := newMemStoreForTest(t)
	if err := st2.AppendLog(&LogEntry{
		Author: pubHex, Name: "n", Kind: "func", Status: "accepted", Hash: artifact,
		EnvelopeB64: encodeEnvelopeB64(raw), AuthorPubkey: pubHex, AuthorSig: bad,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st2.VerifyLog(); err == nil {
		t.Fatal("a signature over the /2 re-encoding verified against /1 octets; the check is not reading the stored bytes")
	}
}
