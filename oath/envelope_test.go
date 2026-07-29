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
