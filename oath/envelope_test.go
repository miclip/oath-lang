package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

func testEnvelope() pubEnvelope {
	return pubEnvelope{
		Op:        "put",
		Name:      "double",
		Artifact:  strings.Repeat("a", 64),
		Parent:    strings.Repeat("b", 64),
		ParentRev: 3,
		Author:    strings.Repeat("c", 64),
	}
}

// The encoding must be canonical BY DEFINITION: a fixed domain separator, then a
// fixed set of keys in a fixed order. Pinning the exact bytes is the point — a
// reordering or an added field is a silent identity fork, so this test exists to
// fail loudly if anyone "tidies" envelopeEncode.
func TestEnvelopeEncodingIsExact(t *testing.T) {
	got := string(envelopeEncode(testEnvelope()))
	want := "oath-publish/1\n" +
		"op=put\n" +
		"name=double\n" +
		"artifact=" + strings.Repeat("a", 64) + "\n" +
		"parent=" + strings.Repeat("b", 64) + "\n" +
		"parent_rev=3\n" +
		"author=" + strings.Repeat("c", 64) + "\n"
	if got != want {
		t.Fatalf("canonical encoding changed.\n got: %q\nwant: %q", got, want)
	}
	if !strings.HasPrefix(got, envelopeVersion+"\n") {
		t.Fatal("encoding must be domain-separated so a future envelope shape cannot be confused with this one")
	}
}

// The LF lesson from campaign identity, enforced rather than assumed: a value
// carrying a newline would inject a line and destroy unique decodability.
func TestEnvelopeRejectsInjection(t *testing.T) {
	for _, tc := range []struct{ name, bad string }{
		{"newline", "double\nartifact=deadbeef"},
		{"carriage return", "double\rx"},
		{"nul", "double\x00x"},
		{"control char", "double\x01"},
	} {
		e := testEnvelope()
		e.Name = tc.bad
		if err := e.validate(); err == nil {
			t.Fatalf("%s in a value was accepted: the encoding would not be uniquely decodable", tc.name)
		}
	}
}

// Two DIFFERENT publications must never share canonical bytes. Each field is
// perturbed in turn, so a field accidentally dropped from the encoding is caught.
func TestEnvelopeFieldsAllBind(t *testing.T) {
	base := string(envelopeEncode(testEnvelope()))
	perturb := map[string]func(*pubEnvelope){
		"name":       func(e *pubEnvelope) { e.Name = "triple" },
		"artifact":   func(e *pubEnvelope) { e.Artifact = strings.Repeat("d", 64) },
		"parent":     func(e *pubEnvelope) { e.Parent = strings.Repeat("f", 64) },
		"parent_rev": func(e *pubEnvelope) { e.ParentRev = 4 },
		"author":     func(e *pubEnvelope) { e.Author = strings.Repeat("e", 64) },
	}
	for field, f := range perturb {
		e := testEnvelope()
		f(&e)
		if string(envelopeEncode(e)) == base {
			t.Fatalf("changing %q did not change the canonical bytes: the field is not bound by the signature", field)
		}
	}
}

// Hex case matters, because the encoding compares strings. This is the hex-case
// canonicality hole found in campaign identity, closed here at validation.
func TestEnvelopeRejectsUppercaseHash(t *testing.T) {
	e := testEnvelope()
	e.Artifact = strings.ToUpper(e.Artifact)
	if err := e.validate(); err == nil {
		t.Fatal("uppercase hex artifact accepted: ABAB… and abab… would be different bytes for the same artifact")
	}
}

func TestEnvelopeSignAndVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := testEnvelope()
	e.Author = hex.EncodeToString(pub)

	sig, err := envelopeSign(priv, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := envelopeVerify(e, sig); err != nil {
		t.Fatalf("honest envelope failed to verify: %v", err)
	}

	// The whole point: a signature detached from its envelope must not survive
	// being reattached to a different publication. Each of these is a real attack
	// — substituting content, renaming, or replaying against a newer parent.
	for _, tc := range []struct {
		name   string
		mutate func(*pubEnvelope)
	}{
		{"different artifact (content substitution)", func(e *pubEnvelope) { e.Artifact = strings.Repeat("9", 64) }},
		{"different name (published elsewhere)", func(e *pubEnvelope) { e.Name = "other" }},
		{"different parent (REPLAY / rollback)", func(e *pubEnvelope) { e.Parent = strings.Repeat("7", 64) }},
		{"REPLAY under ABA: same parent hash, later revision", func(e *pubEnvelope) { e.ParentRev = 9 }},
	} {
		bad := e
		tc.mutate(&bad)
		if err := envelopeVerify(bad, sig); err == nil {
			t.Fatalf("signature verified against a mutated envelope (%s): it could be detached and reused", tc.name)
		}
	}

	// A signature from a different key must not verify.
	_, other, _ := ed25519.GenerateKey(nil)
	otherSig, _ := envelopeSign(other, e)
	if err := envelopeVerify(e, otherSig); err == nil {
		t.Fatal("signature by a non-author key verified")
	}
}

// A first publication has no parent, and that must still be signable — otherwise
// the very first put of every name is unprotected.
func TestEnvelopeFirstPublication(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := testEnvelope()
	e.Author, e.Parent, e.ParentRev = hex.EncodeToString(pub), noParent, firstRev
	sig, err := envelopeSign(priv, e)
	if err != nil {
		t.Fatalf("first publication is not signable: %v", err)
	}
	if err := envelopeVerify(e, sig); err != nil {
		t.Fatalf("first publication failed to verify: %v", err)
	}
	// ...and must not be interchangeable with a publication that HAD a parent.
	withParent := e
	withParent.Parent, withParent.ParentRev = strings.Repeat("b", 64), 1
	if err := envelopeVerify(withParent, sig); err == nil {
		t.Fatal("a no-parent signature verified against a publication with a parent")
	}
}

// The ABA case, which a parent hash alone cannot catch: a name goes A → B → A, so
// an old envelope's parent hash matches again. The monotonic revision is what
// still distinguishes the two publications.
func TestEnvelopeSurvivesABA(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	hashA := strings.Repeat("a", 64)

	first := pubEnvelope{Op: "put", Name: "n", Artifact: strings.Repeat("1", 64),
		Parent: hashA, ParentRev: 1, Author: hex.EncodeToString(pub)}
	sig, err := envelopeSign(priv, first)
	if err != nil {
		t.Fatal(err)
	}

	// The name has since returned to hashA, so parent matches once more — but two
	// further repoints happened, so the revision does not.
	replayed := first
	replayed.ParentRev = 3
	if err := envelopeVerify(replayed, sig); err == nil {
		t.Fatal("captured envelope replayed after A→B→A: parent hash matched again and nothing else stopped it")
	}
}

// parent_rev is rendered into canonical bytes, so its textual form must be
// canonical too — otherwise "3" and "03" would be different signed bytes for the
// same revision. strconv.Itoa gives exactly one spelling per value; this test
// exists so a future "prettier" formatter cannot introduce a second one.
func TestEnvelopeRevisionIsCanonicalDecimal(t *testing.T) {
	e := testEnvelope()
	e.ParentRev = 3
	if !strings.Contains(string(envelopeEncode(e)), "parent_rev=3\n") {
		t.Fatal("revision is not rendered as a bare canonical decimal")
	}
	e.ParentRev = -1
	if err := e.validate(); err == nil {
		t.Fatal("negative revision accepted")
	}
}

// A first publication must agree with itself: claiming no parent while naming a
// revision (or vice versa) would let an envelope describe two different states.
func TestEnvelopeFirstPublicationConsistency(t *testing.T) {
	e := testEnvelope()
	e.Parent, e.ParentRev = noParent, 2
	if err := e.validate(); err == nil {
		t.Fatal("noParent with a nonzero revision accepted: inconsistent state was signable")
	}
	e.Parent, e.ParentRev = strings.Repeat("b", 64), firstRev
	if err := e.validate(); err == nil {
		t.Fatal("a parent hash with revision 0 accepted: inconsistent state was signable")
	}
}

// Round-trip: whatever the encoder emits, the parser must recover exactly. This
// is the property that lets the journal store bytes and verification read them.
func TestEnvelopeRoundTrip(t *testing.T) {
	for _, e := range []pubEnvelope{
		testEnvelope(),
		{Op: "put", Name: "a/b/c", Artifact: strings.Repeat("1", 64), Parent: noParent, ParentRev: firstRev, Author: strings.Repeat("2", 64)},
		{Op: "put", Name: "x", Artifact: strings.Repeat("3", 64), Parent: strings.Repeat("4", 64), ParentRev: 1234567, Author: strings.Repeat("5", 64)},
	} {
		got, err := envelopeParse(envelopeEncode(e))
		if err != nil {
			t.Fatalf("round-trip failed for %+v: %v", e, err)
		}
		if got != e {
			t.Fatalf("round-trip changed the envelope:\n got %+v\nwant %+v", got, e)
		}
	}
}

// The parser must be STRICT: leniency would let two different byte sequences
// yield the same fields, discarding unique decodability at the reading end.
func TestEnvelopeParseIsStrict(t *testing.T) {
	good := string(envelopeEncode(testEnvelope()))
	for _, tc := range []struct{ name, bytes string }{
		{"wrong version", strings.Replace(good, "oath-publish/1", "oath-publish/2", 1)},
		{"no version line", strings.SplitN(good, "\n", 2)[1]},
		{"reordered fields", strings.Replace(good, "op=put\nname=double\n", "name=double\nop=put\n", 1)},
		{"unknown extra field", good + "extra=1\n"},
		{"missing field", strings.Replace(good, "parent_rev=3\n", "", 1)},
		{"no trailing newline", strings.TrimSuffix(good, "\n")},
		{"non-canonical revision", strings.Replace(good, "parent_rev=3\n", "parent_rev=03\n", 1)},
		{"duplicated field", strings.Replace(good, "op=put\n", "op=put\nop=put\n", 1)},
		{"trailing junk", good + "\n"},
	} {
		if _, err := envelopeParse([]byte(tc.bytes)); err == nil {
			t.Fatalf("parser accepted %s: a second byte spelling maps to the same envelope", tc.name)
		}
	}
}

// The signature must verify against the PERSISTED bytes, not against a
// re-encoding. This test simulates the failure the design exists to prevent: an
// entry stored under one format being verified by a kernel whose encoder moved on.
func TestEnvelopeVerifiesPersistedBytes(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := testEnvelope()
	e.Author = hex.EncodeToString(pub)
	persisted := envelopeEncode(e)
	sig := hex.EncodeToString(ed25519.Sign(priv, persisted))

	parsed, err := envelopeParse(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := envelopeVerify(parsed, sig); err != nil {
		t.Fatalf("signature over persisted bytes did not verify: %v", err)
	}
	// A single altered byte must break it — the bytes are the statement.
	tampered := append([]byte(nil), persisted...)
	tampered[len(tampered)-2] ^= 0x01
	if _, err := envelopeParse(tampered); err == nil {
		if err := envelopeVerify(parsed, hex.EncodeToString(ed25519.Sign(priv, tampered))); err == nil {
			t.Fatal("tampered bytes verified against the original envelope")
		}
	}
}

// A small-order key cannot carry an authorship claim (SPEC §8.6.4a). The identity
// point is the reachable case and must be refused before any signature is checked.
func TestRejectsIdentityKey(t *testing.T) {
	e := testEnvelope()
	e.Author = strings.Repeat("00", 32)
	if err := envelopeVerify(e, strings.Repeat("aa", 64)); err == nil {
		t.Fatal("an all-zero (identity) author key was accepted: signatures under it verify for parties who do not hold it")
	}
	if err := rejectWeakKey(make([]byte, 32)); err == nil {
		t.Fatal("rejectWeakKey accepted the identity point")
	}
	if err := rejectWeakKey([]byte(strings.Repeat("\x01", 32))); err != nil {
		t.Fatal("rejectWeakKey refused an ordinary key")
	}
}
