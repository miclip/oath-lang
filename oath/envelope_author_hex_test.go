package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

// SPEC 8.6.1 ENV-AUTHOR-HEX: author is 64 LOWERCASE hex.
//
// validate() checked length and hex.DecodeString, which accepts uppercase, so
// the reference kernel admitted an envelope the specification forbids while the
// independent Rust kernel refused it (is_lower_hex64). A divergence on
// ADMISSIBILITY that no fixture could catch, because the committed journal
// holds no author envelopes and conformance never reaches this path.
//
// The spelling is COMPARED, not decoded -- env.Author != e.AuthorPubkey, and the
// lineage derivation keys on the string -- so one uppercase character makes a
// correctly signed publication unrecognisable rather than merely unusual.
func TestEnvelopeAuthorMustBeLowercaseHex(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	lower := hex.EncodeToString(pub)
	upper := strings.ToUpper(lower)
	if lower == upper {
		t.Fatal("this key has no hex letters, so the case rule is not exercised")
	}
	mk := func(author string) pubEnvelope {
		return pubEnvelope{Op: "put", Name: "n", Artifact: strings.Repeat("c", 64),
			Parent: noParent, ParentRev: firstRev(), Author: author, License: noLicense}
	}
	if err := mk(lower).validate(); err != nil {
		t.Fatalf("a lowercase author was refused, so the rule is too strong: %v", err)
	}
	if err := mk(upper).validate(); err == nil {
		t.Fatal("an uppercase hex author was admitted; 8.6.1 requires lowercase")
	}
	// Control: the refusal must be about CASE, not about length or alphabet,
	// which the length check above would already have caught.
	if err := mk(lower[:63] + "g").validate(); err == nil {
		t.Fatal("a non-hex character was admitted")
	}
}
