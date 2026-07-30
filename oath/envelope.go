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
// WHAT `parent` BUYS, AND WHAT IT DOES NOT. Binding the hash the name currently
// points at expresses a compare-and-swap, so a captured envelope cannot be
// replayed to roll a name back — its parent no longer matches — with no
// server-side nonce table, which is why this beats a nonce or a timestamp window.
//
// Two limits, stated because overclaiming them would be worse than not having
// them:
//
//   1. ABA. A hash alone is not monotonic. If a name moves A → B → A, an old
//      envelope naming parent=A becomes valid AGAIN, and the replay defence
//      silently lapses. True ABA has never occurred in the corpus — the many
//      repeated hashes there are idempotent re-puts of identical content, not a
//      return to an earlier different value — but nothing FORBIDS it: revert a
//      change and republish and you have it. Rather than assert an invariant the
//      store does not enforce, the envelope also binds `parent_rev`, a per-name
//      monotonic revision. Per-name rather than the global journal head on
//      purpose: binding the head would make any concurrent publication of an
//      UNRELATED name invalidate this envelope.
//
//   2. The CAS is expressed here, NOT enforced by storage. Acceptance is not
//      atomic: the compare (Resolve) and the update (Repoint) are separate
//      operations, and StoreObject does an unlocked read-modify-write, so two
//      publishers can both read parent=A, both validate, and both proceed. So
//      this closes REPLAY (an old envelope is rejected) but does NOT close
//      concurrent lost updates. That needs an atomic compare-and-set in the
//      backend; until then the protocol describes a guarantee the implementation
//      does not yet keep, and saying otherwise would be the kind of unearned
//      claim this project exists to avoid.

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// envelopeVersion is the format this kernel EMITS. It is part of the signed bytes, so
// a signature made under one shape can never be read under another — which is what
// makes adding a field a version bump rather than a compatibility hazard.
const envelopeVersion = "oath-publish/2"

// envelopeVersionV1 is still READ. Historical entries were signed under it and their
// signatures must keep verifying: §8.6.1 requires verification to use the format
// recorded with the entry, not whatever the current encoder emits. A kernel that
// stopped reading /1 would invalidate correct signatures rather than reject bad ones.
const envelopeVersionV1 = "oath-publish/1"

// noLicense marks a publication that asserts no licensing terms. Explicit rather than
// empty, for the same reason noParent is: "the publisher said nothing" and "a field
// nobody populated" are different facts, and only the first is a statement.
const noLicense = "-"

// firstRev is the revision of a name that has never been published.
func firstRev() *big.Int { return big.NewInt(0) }

// revOf lifts the kernel's local counter into the envelope's arbitrary-precision
// form. Local counts are bounded by the journal that produced them; envelope values
// are not.
func revOf(n int) *big.Int { return big.NewInt(int64(n)) }

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
	// ParentRev is how many state transitions the name had ALREADY undergone before
	// this publication: a per-name monotonic counter. It is what makes the replay
	// defence survive ABA, since a revision never repeats even when a hash does.
	//
	// ARBITRARY PRECISION, not a machine word. §8.6.1 forbids a machine-word limit
	// because a bounded parser would declare a valid historical statement MALFORMED
	// rather than merely unsupported — an envelope is a permanent record, and a
	// verifier that cannot read one cannot say anything about it. This also matches
	// the kernel's existing stance on numbers, where Int is ℤ rather than a width.
	//
	// The kernel's own counter (nameRevision) stays a machine int: it counts entries
	// in a journal it holds, so it is bounded by what physically exists. The
	// asymmetry is deliberate — the WIRE format must accept any valid statement, the
	// local counter need only count real events.
	ParentRev *big.Int
	Author    string // hex Ed25519 public key of the signer
	// License is the terms the PUBLISHER asserts for this publication — an SPDX
	// expression such as "MIT", "MIT OR Apache-2.0", or "GPL-3.0 WITH
	// Classpath-exception-2.0" — or noLicense.
	//
	// IT IS AN ASSERTION, NOT A DERIVED FACT (DESIGN.md, "What belongs inside
	// identity"). The publisher states terms; the registry may later derive
	// compatibility across a dependency closure, and those are different claims that
	// must never be reported as one.
	//
	// It lives HERE, in the signed publication, rather than in the artifact graph. A
	// license is a claim about a publication, not a property of the bytes: making it a
	// dependency would fork identity on every relicensing, make "MIT OR Apache-2.0"
	// inexpressible as one artifact, and put a legal assertion inside structural
	// identity. Content identity must not change when only human claims change.
	//
	// Deliberately NOT validated as SPDX syntax. Validating it here would make this
	// kernel an authority on licence expressions; it is only the notary for what the
	// author said. The semantics layer that interprets it is a separate, fallible
	// model.
	License string
}

// envelopeEncode renders the canonical signed bytes. Callers must have validated
// the envelope first; this function panics on an invalid value rather than
// emitting bytes that cannot be uniquely decoded, because a silently-corrupt
// canonical encoding is the one failure that would be discovered years later by
// someone unable to verify a signature they should have been able to trust.
func envelopeEncode(e pubEnvelope) []byte { return envelopeEncodeAs(e, envelopeVersion) }

// envelopeEncodeAs renders under a SPECIFIC format version, so historical bytes can be
// reproduced for verification rather than only re-signed under the current shape.
func envelopeEncodeAs(e pubEnvelope, version string) []byte {
	if err := e.validate(); err != nil {
		panic("envelopeEncode on an invalid envelope: " + err.Error())
	}
	var b strings.Builder
	b.WriteString(version)
	b.WriteByte('\n')
	// Fixed order, fixed count PER VERSION. Do not sort, do not omit, do not add.
	kvs := [][2]string{
		{"op", e.Op},
		{"name", e.Name},
		{"artifact", e.Artifact},
		{"parent", e.Parent},
		{"parent_rev", e.ParentRev.String()},
		{"author", e.Author},
	}
	if version != envelopeVersionV1 {
		kvs = append(kvs, [2]string{"license", e.License})
	}
	for _, kv := range kvs {
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
		{"parent", e.Parent}, {"author", e.Author}, {"license", e.License},
	} {
		if f.v == "" {
			return fmt.Errorf("envelope field %q is empty", f.k)
		}
		if ruleOn("8.6.1/value-characters") && !envelopeSafe(f.v) {
			return fmt.Errorf("envelope field %q contains a newline or control character, which would break canonical encoding", f.k)
		}
	}
	if e.Op != "put" {
		return fmt.Errorf("envelope op %q is not a known operation", e.Op)
	}
	if ruleOn("8.6.1/lowercase-hex") && !isHash(e.Artifact) {
		return fmt.Errorf("envelope artifact %q is not a content hash", e.Artifact)
	}
	if e.Parent != noParent && !isHash(e.Parent) {
		return fmt.Errorf("envelope parent %q is neither a content hash nor %q", e.Parent, noParent)
	}
	if e.ParentRev == nil {
		return fmt.Errorf("envelope parent_rev is absent")
	}
	if e.ParentRev.Sign() < 0 {
		return fmt.Errorf("envelope parent_rev %s is negative", e.ParentRev)
	}
	// A first publication must agree with itself: no parent hash and no prior
	// revisions are the same statement, and allowing them to disagree would let an
	// envelope claim a name was fresh while pointing at a parent (or vice versa).
	if ruleOn("8.6.1/parent-consistency") && (e.Parent == noParent) != (e.ParentRev.Sign() == 0) {
		return fmt.Errorf("envelope is inconsistent: parent=%q with parent_rev=%s — a first publication has both, a repoint has neither", e.Parent, e.ParentRev)
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
	if ruleOn("8.6.4a/small-order") {
		if err := rejectWeakKey(pub); err != nil {
			return err
		}
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("publication signature is not a %d-byte hex signature", ed25519.SignatureSize)
	}
	if ruleOn("8.6.4a/signature-valid") && !ed25519.Verify(ed25519.PublicKey(pub), envelopeEncode(e), sig) {
		return fmt.Errorf("publication signature does not verify: the envelope was altered in transit, or it was not signed by %s", e.Author)
	}
	return nil
}

// envelopeParse decodes persisted envelope bytes back into fields.
//
// WHY A PARSER EXISTS AT ALL, when there is already an encoder: the journal
// persists the EXACT bytes the author signed, and verification must read those
// bytes rather than re-encode from the entry's fields. Reconstruction would mean
// a future encoder change silently invalidates every historical signature — the
// signature would still be correct and would no longer verify, which is the worst
// possible failure for an audit trail. The stored bytes are the historical signed
// statement; these parsed fields are only its interpretation.
//
// That also fixes the version question. The version lives in the bytes, so an old
// entry is verified under the format it was written with. This function accepts
// only oath-publish/1; a future format gets its own branch, and neither can be
// mistaken for the other because the separator is inside the signed material.
//
// STRICT ON PURPOSE. Unknown keys, missing keys, duplicates, reordering and
// trailing bytes are all errors. A lenient parser would let two different byte
// sequences produce the same fields, which is exactly the unique-decodability
// property the encoding exists to guarantee — leniency here would give it away at
// the other end.
func envelopeParse(b []byte) (pubEnvelope, error) {
	var e pubEnvelope
	s := string(b)
	if ruleOn("8.6.1/trailing-lf") && !strings.HasSuffix(s, "\n") {
		return e, fmt.Errorf("envelope does not end with a newline: it is truncated or was re-wrapped in transit")
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) == 0 {
		return e, fmt.Errorf("envelope is empty")
	}
	// Dispatch on the RECORDED format, not the current one. §8.6.1 requires historical
	// verification to use the format an entry was written under; a kernel that only
	// read its newest shape would reject correct old signatures, which is a worse
	// failure than accepting a bad one because it destroys evidence rather than
	// admitting it.
	var want []string
	switch {
	case lines[0] == envelopeVersion:
		want = []string{"op", "name", "artifact", "parent", "parent_rev", "author", "license"}
	case lines[0] == envelopeVersionV1:
		want = []string{"op", "name", "artifact", "parent", "parent_rev", "author"}
	case !ruleOn("8.6.1/version-tag"):
		want = []string{"op", "name", "artifact", "parent", "parent_rev", "author", "license"}
	default:
		return e, fmt.Errorf("unsupported envelope format %q: this kernel verifies %q and %q",
			lines[0], envelopeVersion, envelopeVersionV1)
	}
	body := lines[1:]
	if ruleOn("8.6.1/field-count") && len(body) != len(want) {
		return e, fmt.Errorf("envelope has %d fields, expected exactly %d: %v", len(body), len(want), want)
	}
	vals := make([]string, len(want))
	for i, line := range body {
		// Bounded explicitly rather than trusting the count check above. A parser that
		// PANICS on malformed input is worse than one that rejects it: an unchecked
		// index is a denial of service on attacker-supplied bytes, and relying on an
		// earlier check to make it unreachable couples two rules that should stand
		// alone. Found by the conformance harness — disabling the field-count rule made
		// this crash instead of fail.
		if i >= len(want) {
			break
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			return e, fmt.Errorf("envelope line %d is not key=value", i+2)
		}
		if ruleOn("8.6.1/field-order") && k != want[i] {
			return e, fmt.Errorf("envelope field %d is %q, expected %q: field order is part of the canonical encoding", i+1, k, want[i])
		}
		vals[i] = v
	}
	if len(body) < len(want) {
		return e, fmt.Errorf("envelope has %d fields, expected %d", len(body), len(want))
	}
	// Arbitrary precision: a bounded parse would reject a valid historical statement.
	rev, ok := new(big.Int).SetString(vals[4], 10)
	if !ok {
		return e, fmt.Errorf("envelope parent_rev %q is not a decimal integer", vals[4])
	}
	// Reject any non-canonical spelling ("03", "+3", " 3"), which would parse to the
	// same value from different bytes.
	if ruleOn("8.6.1/canonical-decimal") && rev.String() != vals[4] {
		return e, fmt.Errorf("envelope parent_rev %q is not canonical decimal (would be %q)", vals[4], rev.String())
	}
	e = pubEnvelope{Op: vals[0], Name: vals[1], Artifact: vals[2], Parent: vals[3],
		ParentRev: rev, Author: vals[5]}
	if len(vals) > 6 {
		e.License = vals[6]
	} else {
		// A /1 envelope asserted no terms — it could not. Recorded as the explicit
		// sentinel so a reader cannot mistake "the format had no field" for "the
		// publisher chose to say nothing"; both are absences, but only one was a choice.
		e.License = noLicense
	}
	if err := e.validate(); err != nil {
		return pubEnvelope{}, err
	}
	// Belt and braces: the bytes we were given must be exactly what this envelope
	// encodes. Anything else means the parser accepted a spelling the encoder
	// would never emit, so the two halves have drifted.
	// Re-encode under the format the bytes DECLARE, or a /1 envelope would be re-encoded
	// as /2 and fail its own canonicality check.
	if ruleOn("8.6.1/reencode") && !bytesEqual(envelopeEncodeAs(e, lines[0]), b) {
		return pubEnvelope{}, fmt.Errorf("envelope bytes are not canonical: they parse, but re-encoding produces different bytes")
	}
	return e, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rejectWeakKey refuses public keys that cannot carry an authorship claim.
//
// SPEC §8.6.4a requires small-order `A` to be rejected: with such a key the
// verification equation degenerates and signatures verify for parties who do not
// hold it, so an "author" would be unforgeable in name only.
//
// The exploitable case is concrete rather than theoretical: with A = the IDENTITY
// point (encoded 0100…00 — NOT all-zeros, which is y=0 and has order 4), the choice
// R = identity, S = 0 satisfies [S]B = R + [k]A for ANY message, with no private key.
// Go's crypto/ed25519 accepts it. An earlier partial version of this check blocked
// only the all-zero encoding, so it refused a key nobody could exploit while
// accepting the one anybody could — the appearance of protection rather than partial
// protection.
//
// A BLOCKLIST, not arithmetic. The eight points of order dividing 8 have fixed
// encodings, so membership is a byte comparison — no curve code, and the default
// build stays dependency-free (CLAUDE.md). Go's crypto/ed25519 does NOT reject these
// itself: it decodes them happily and merely fails the equation for signatures not
// crafted for them, which is not the same as refusing the key.
//
// THE CONSTANTS ARE DERIVED, NOT TRANSCRIBED. scripts/derive-small-order.py computes
// them from p, d and L and verifies each is on the curve with [8]P = identity and an
// order dividing 8, and that the base point is NOT among them so the derivation
// cannot pass vacuously. Re-running it reproduces this list exactly.
//
// That discipline is not ceremony. A recalled constant in the middle of a signature
// check is an unverified claim guarding a security property, and this project has
// already been bitten once by a misremembered RFC 8032 vector — where the right
// response was an independent oracle rather than adjusting code to match.
//
// Non-canonical encodings of these points are deliberately absent: they are refused
// one layer down, where decoding requires a canonical point encoding (§8.6.4a), so
// listing them here would duplicate a rule rather than add one.
// DERIVED by scripts/derive-small-order.py from p, d and L — NOT transcribed.
// Every entry verified on-curve with [8]P = identity and order dividing 8;
// the base point is verified NOT to be among them, so the derivation cannot
// pass vacuously. Re-run the script to reproduce this list.
var smallOrderEncodings = [][32]byte{
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // order 4
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80}, // order 4
	{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // order 1
	{0x26, 0xe8, 0x95, 0x8f, 0xc2, 0xb2, 0x27, 0xb0, 0x45, 0xc3, 0xf4, 0x89, 0xf2, 0xef, 0x98, 0xf0, 0xd5, 0xdf, 0xac, 0x05, 0xd3, 0xc6, 0x33, 0x39, 0xb1, 0x38, 0x02, 0x88, 0x6d, 0x53, 0xfc, 0x05}, // order 8
	{0x26, 0xe8, 0x95, 0x8f, 0xc2, 0xb2, 0x27, 0xb0, 0x45, 0xc3, 0xf4, 0x89, 0xf2, 0xef, 0x98, 0xf0, 0xd5, 0xdf, 0xac, 0x05, 0xd3, 0xc6, 0x33, 0x39, 0xb1, 0x38, 0x02, 0x88, 0x6d, 0x53, 0xfc, 0x85}, // order 8
	{0xc7, 0x17, 0x6a, 0x70, 0x3d, 0x4d, 0xd8, 0x4f, 0xba, 0x3c, 0x0b, 0x76, 0x0d, 0x10, 0x67, 0x0f, 0x2a, 0x20, 0x53, 0xfa, 0x2c, 0x39, 0xcc, 0xc6, 0x4e, 0xc7, 0xfd, 0x77, 0x92, 0xac, 0x03, 0x7a}, // order 8
	{0xc7, 0x17, 0x6a, 0x70, 0x3d, 0x4d, 0xd8, 0x4f, 0xba, 0x3c, 0x0b, 0x76, 0x0d, 0x10, 0x67, 0x0f, 0x2a, 0x20, 0x53, 0xfa, 0x2c, 0x39, 0xcc, 0xc6, 0x4e, 0xc7, 0xfd, 0x77, 0x92, 0xac, 0x03, 0xfa}, // order 8
	{0xec, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, // order 2
}

func rejectWeakKey(pub []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("public key is %d bytes, expected %d", len(pub), ed25519.PublicKeySize)
	}
	var k [32]byte
	copy(k[:], pub)
	for _, bad := range smallOrderEncodings {
		if k == bad {
			return fmt.Errorf("public key %s… has small order: such a key cannot carry an authorship claim, since signatures under it verify for parties who do not hold it (SPEC §8.6.4a)", hex.EncodeToString(pub[:6]))
		}
	}
	return nil
}

// equal compares two envelopes by VALUE.
//
// Necessary because ParentRev is a *big.Int: Go's `==` on pubEnvelope compares the
// POINTER, so two envelopes with identical revisions compare unequal while printing
// identically — a failure that looks like a bug in whatever is being tested rather
// than in the comparison. Anything checking envelope equality must use this.
func (e pubEnvelope) equal(o pubEnvelope) bool {
	if e.ParentRev == nil || o.ParentRev == nil {
		return e.ParentRev == o.ParentRev && e.Op == o.Op && e.Name == o.Name &&
			e.Artifact == o.Artifact && e.Parent == o.Parent && e.Author == o.Author &&
			e.License == o.License
	}
	return e.Op == o.Op && e.Name == o.Name && e.Artifact == o.Artifact &&
		e.Parent == o.Parent && e.Author == o.Author && e.License == o.License &&
		e.ParentRev.Cmp(o.ParentRev) == 0
}

// checkPublication decides whether an author statement authorises the transition
// being requested — the three store-side MUSTs of SPEC §8.6.4, in one place.
//
// Extracted so the put path and the conformance-vector generator share ONE
// implementation. Previously this logic lived inline in the put path, which meant
// the vectors asserting it were written by hand against a second reading of the
// spec; a vector generator that re-implements the rule it is meant to witness can
// agree with itself while both are wrong.
//
// Arguments are the store's OWN findings, not the author's claims: artifactHash is
// recomputed from submitted content, curParent/curRev are read from the store.
// Returns nil when the statement authorises the transition.
func checkPublication(env pubEnvelope, sigHex, principal, name, artifactHash, curParent string, curRev int) error {
	// The signing key must be the AUTHENTICATED principal, not the key the envelope
	// names. Verifying against the envelope's own author would always succeed, so any
	// caller could present any valid statement made by anyone.
	if ruleOn("8.6.4/principal-binding") && env.Author != principal {
		return fmt.Errorf("envelope is signed for %s but the request authenticated as %s", shortHash(env.Author), shortHash(principal))
	}
	if err := envelopeVerify(env, sigHex); err != nil {
		return err
	}
	if ruleOn("8.6.4/name-match") && env.Name != name {
		return fmt.Errorf("author statement does not match the requested transition: signed name %q, publishing %q", env.Name, name)
	}
	// RECOMPUTED, not taken from the statement. This makes agreement between the
	// client's and the store's elaboration an enforced precondition rather than an
	// assumption — identity is a pure function of content, so a disagreement here is
	// a real divergence and not a formality.
	if ruleOn("8.6.4/artifact-recompute") && env.Artifact != artifactHash {
		return fmt.Errorf("author statement does not match the requested transition: signed artifact %s, submitted content hashes to %s", shortHash(env.Artifact), shortHash(artifactHash))
	}
	if ruleOn("8.6.4/parent-current") && env.Parent != curParent {
		return fmt.Errorf("author statement does not match the requested transition: signed parent %s, but %q currently points at %s — the name moved since the envelope was made, or this is a replay", shortHash(env.Parent), name, shortHash(curParent))
	}
	if ruleOn("8.6.4/revision-current") && env.ParentRev.Cmp(revOf(curRev)) != 0 {
		return fmt.Errorf("author statement does not match the requested transition: signed parent_rev %s, but %q is at revision %d — a replay whose parent hash happens to match again (ABA)", env.ParentRev, name, curRev)
	}
	return nil
}
