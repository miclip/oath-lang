package main

// The canonical PUBLICATION ENVELOPE (#83): what an author signs when publishing
// to a registry, and the thing an auditor re-derives to check that signature
// years later.
//
// WHY NOT SIGN THE JOURNAL ENTRY ITSELF. The journal's authored fields already
// have a canonical encoding (signedContent), so signing those was the obvious
// move — and it is wrong, because a journal entry mixes two different kinds of
// claim. `name`, `hash` and `prev` are the AUTHOR's: this content, under this
// name, replacing this parent. `status`, `guarantee` and `termination` are the
// REGISTRY's findings, derived independently from content bytes. An author
// signature over the registry's verdicts would have the author attesting to
// conclusions they do not control and cannot predict — a policy block turns an
// honest publication into a signature mismatch. So the envelope covers exactly
// the author's half, and the registry's half stays re-derivable by anyone, which
// is the stronger guarantee anyway.
//
// WHY NOT SIGN THE RAW REQUEST BODY. `oath serve` authenticates a signature over
// the whole JSON-RPC body, which is right for deciding whether to accept a
// REQUEST. It can never become transferable evidence: a request body is not a
// reconstructible artifact, so an auditor holding only the journal cannot rebuild
// the bytes that were signed. The two layers are complementary — the body
// signature says who may publish, this envelope says who authored.
//
// CANONICAL BY DEFINITION, the same discipline as campaignEncode: a domain
// separator, then a FIXED set of keys in a FIXED order, each LF-terminated. There
// is no field ordering to get wrong and no optional key to omit, so two
// descriptions are byte-equal iff they are equal.
//
// The LF lesson from campaign identity applies directly and is enforced rather
// than assumed: every value is validated to contain no LF or control characters.
// Without that, a value carrying a newline injects a line and the encoding stops
// being uniquely decodable — the defect that mattered there was decodability, not
// collision, and it is cheaper to exclude the character than to reason about it.
//
// WHAT `parent` BUYS. Binding the hash the name currently points at makes
// publication a compare-and-swap. A captured envelope cannot be replayed to roll
// a name back to an earlier version, because its parent no longer matches — and
// this needs NO server-side nonce table, which is why it is preferred to a nonce
// or a timestamp window. It also closes lost-update races between two agents
// publishing the same name concurrently, for free.

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
)

// envelopeVersion is the domain separator. It is part of the signed bytes, so a
// future envelope shape cannot have its signatures confused with this one's.
const envelopeVersion = "oath-publish/1"

// noParent marks a first publication — a name that pointed at nothing. Empty
// would also be unambiguous here (the encoding is fixed-key), but an explicit
// sentinel makes "I checked, and there was nothing" distinguishable from a field
// someone forgot to populate, which is the distinction an auditor cares about.
const noParent = "-"

// pubEnvelope is an author's publication intent: this artifact, under this name,
// replacing this parent, by this key.
type pubEnvelope struct {
	Op       string // "put"
	Name     string // full (namespaced) name the artifact is published under
	Artifact string // content hash of the definition
	Parent   string // hash the name pointed at before, or noParent
	Author   string // hex Ed25519 public key of the signer
}

// envelopeEncode renders the canonical signed bytes. Callers must have validated
// the envelope first; this function panics on an invalid value rather than
// emitting bytes that cannot be uniquely decoded, because a silently-corrupt
// canonical encoding is the one failure that would be discovered years later by
// someone unable to verify a signature they should have been able to trust.
func envelopeEncode(e pubEnvelope) []byte {
	if err := e.validate(); err != nil {
		panic("envelopeEncode on an invalid envelope: " + err.Error())
	}
	var b strings.Builder
	b.WriteString(envelopeVersion)
	b.WriteByte('\n')
	// Fixed order, fixed count. Do not sort, do not omit, do not add.
	for _, kv := range [][2]string{
		{"op", e.Op},
		{"name", e.Name},
		{"artifact", e.Artifact},
		{"parent", e.Parent},
		{"author", e.Author},
	} {
		b.WriteString(kv[0])
		b.WriteByte('=')
		b.WriteString(kv[1])
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// envelopeSafe reports whether a value can appear in the encoding without
// destroying unique decodability. Rejects LF (which would inject a line), CR
// (which some readers normalise into LF), and any other control character.
func envelopeSafe(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (e pubEnvelope) validate() error {
	for _, f := range []struct{ k, v string }{
		{"op", e.Op}, {"name", e.Name}, {"artifact", e.Artifact},
		{"parent", e.Parent}, {"author", e.Author},
	} {
		if f.v == "" {
			return fmt.Errorf("envelope field %q is empty", f.k)
		}
		if !envelopeSafe(f.v) {
			return fmt.Errorf("envelope field %q contains a newline or control character, which would break canonical encoding", f.k)
		}
	}
	if e.Op != "put" {
		return fmt.Errorf("envelope op %q is not a known operation", e.Op)
	}
	if !isHash(e.Artifact) {
		return fmt.Errorf("envelope artifact %q is not a content hash", e.Artifact)
	}
	if e.Parent != noParent && !isHash(e.Parent) {
		return fmt.Errorf("envelope parent %q is neither a content hash nor %q", e.Parent, noParent)
	}
	if len(e.Author) != ed25519.PublicKeySize*2 {
		return fmt.Errorf("envelope author %q is not a 32-byte hex public key", e.Author)
	}
	if _, err := hex.DecodeString(e.Author); err != nil {
		return fmt.Errorf("envelope author is not valid hex: %w", err)
	}
	return nil
}

// isHash reports whether s is a 32-byte lowercase hex content hash. Case matters:
// the encoding compares strings, so ABAB… and abab… would be different bytes for
// the same artifact — the hex-case canonicality hole found in campaign identity.
func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// envelopeSign returns the hex signature of the canonical encoding.
func envelopeSign(priv ed25519.PrivateKey, e pubEnvelope) (string, error) {
	if err := e.validate(); err != nil {
		return "", err
	}
	return hex.EncodeToString(ed25519.Sign(priv, envelopeEncode(e))), nil
}

// envelopeVerify re-derives the canonical bytes and checks the signature against
// the envelope's OWN author key. The caller is responsible for deciding whether
// that key is authorized — verifying a signature establishes who signed, never
// whether they were permitted to.
func envelopeVerify(e pubEnvelope, sigHex string) error {
	if err := e.validate(); err != nil {
		return err
	}
	pub, err := hex.DecodeString(e.Author)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("envelope author is not a usable public key")
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("publication signature is not a %d-byte hex signature", ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), envelopeEncode(e), sig) {
		return fmt.Errorf("publication signature does not verify: the envelope was altered in transit, or it was not signed by %s", e.Author)
	}
	return nil
}
