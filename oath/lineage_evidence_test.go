package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lineage evidence (#82) is derived from the journal plus immutable objects.
// These tests build journals directly, so each case states exactly which entry
// should establish which lineage and why.

func lhash(n int) string { return fmt.Sprintf("%064x", n) }

// ldef is a definition whose props lineage is p and body lineage is b.
func ldef(p, b int64) *Def {
	return &Def{K: "func",
		Body:  &Term{K: "int", Int: big.NewInt(b)},
		Props: []Prop{{Body: Term{K: "int", Int: big.NewInt(p)}}}}
}

type ljournal struct {
	t    *testing.T
	es   []LogEntry
	defs map[string]*Def
}

func newLJournal(t *testing.T) *ljournal { return &ljournal{t: t, defs: map[string]*Def{}} }

func (j *ljournal) obj(h string, d *Def) { j.defs[h] = d }

func (j *ljournal) getDef(h string) (*Def, error) {
	if d, ok := j.defs[h]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("object %s not in store", h)
}

func (j *ljournal) add(e LogEntry) *LogEntry {
	e.Seq = len(j.es) + 1
	if e.Kind == "" {
		e.Kind = "func"
	}
	j.es = append(j.es, e)
	return &j.es[len(j.es)-1]
}

// unsigned records a write with no author envelope — a bearer write, or any
// entry from before envelopes existed.
func (j *ljournal) unsigned(name, h, label string) *LogEntry {
	return j.add(LogEntry{Author: label, Name: name, Status: "accepted", Hash: h})
}

// signed records a publication carrying an honest author envelope for the
// transition parent@rev -> h, signed by priv.
func (j *ljournal) signed(name, h, parent string, rev int64, priv ed25519.PrivateKey) *LogEntry {
	j.t.Helper()
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	env := pubEnvelope{Op: "put", Name: name, Artifact: h, Parent: parent,
		ParentRev: big.NewInt(rev), Author: pub, License: noLicense}
	sig, err := envelopeSign(priv, env)
	if err != nil {
		j.t.Fatal(err)
	}
	return j.add(LogEntry{Author: pub, Name: name, Status: "accepted", Hash: h,
		EnvelopeB64: encodeEnvelopeB64(envelopeEncode(env)), AuthorPubkey: pub, AuthorSig: sig})
}

func (j *ljournal) eval(name, current string) (lineageTier, lineageTier) {
	return lineageEvidence(j.es, name, current, j.getDef)
}

func lkey(t *testing.T) (ed25519.PrivateKey, string) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv, hex.EncodeToString(pub)
}

func wantTier(t *testing.T, what string, got lineageTier, tier string, seq int, key, reason string) {
	t.Helper()
	want := lineageTier{Tier: tier, Seq: seq, Pubkey: key, Reason: reason}
	// Principal is the establishing entry's recorded label; it is asserted
	// where it is the subject (TestLineagePrincipalIsTheEstablishingEntrysLabel
	// and the alias test), not in every outcome check.
	got.Principal = ""
	if got != want {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

func TestLineageFirstSignedPublicationEstablishesBoth(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.signed("f", lhash(1), noParent, 0, k1)
	pr, bo := j.eval("f", lhash(1))
	wantTier(t, "props", pr, lineageKeySigned, 1, p1, "")
	wantTier(t, "body", bo, lineageKeySigned, 1, p1, "")
}

// The props lineage is INHERITED across a body-only change, so its establishing
// write is the earlier signed one even though the current binding came from an
// unsigned write. This is the case a label cannot answer.
func TestLineageInheritedAcrossBodyOnlyChange(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(1, 2)) // same props, new body
	j.signed("f", lhash(1), noParent, 0, k1)
	j.unsigned("f", lhash(2), "bearer-bob")
	pr, bo := j.eval("f", lhash(2))
	wantTier(t, "props", pr, lineageKeySigned, 1, p1, "")
	wantTier(t, "body", bo, lineageUnknown, 2, "", lineageReasonNoEnvelope)
}

// Props and body established at DIFFERENT revisions by DIFFERENT keys.
func TestLineageDifferentEstablishingRevisions(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	k2, p2 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(2, 1)) // props change only
	j.obj(lhash(3), ldef(2, 3)) // body change only
	j.signed("f", lhash(1), noParent, 0, k1)
	j.signed("f", lhash(2), lhash(1), 1, k2)
	j.signed("f", lhash(3), lhash(2), 2, k1)
	pr, bo := j.eval("f", lhash(3))
	wantTier(t, "props", pr, lineageKeySigned, 2, p2, "")
	wantTier(t, "body", bo, lineageKeySigned, 3, p1, "")
}

// A first or pre-signature-era entry has no author envelope. The entry-level
// Pubkey/Sig pair is the STORE's signature over its own record and is not author
// evidence, so it must not lift the outcome. And the answer is UNKNOWN, never a
// negative: nothing here says the author lacked a key.
func TestLineagePreSignatureEntryIsUnknownNotNegative(t *testing.T) {
	j := newLJournal(t)
	j.obj(lhash(1), ldef(1, 1))
	e := j.unsigned("f", lhash(1), "alice")
	e.Pubkey, e.Sig = hex.EncodeToString(make([]byte, 32)), "00" // store-tier fields only
	pr, bo := j.eval("f", lhash(1))
	wantTier(t, "props", pr, lineageUnknown, 1, "", lineageReasonNoEnvelope)
	wantTier(t, "body", bo, lineageUnknown, 1, "", lineageReasonNoEnvelope)
}

// Re-publishing the bound hash is an `unchanged` transition: it changes no
// lineage, signed or not. Nor do refused attempts, proof entries, or another
// name's writes.
func TestLineageNoOpAndUnrelatedEntriesDoNotMoveIt(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	k2, _ := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(9), ldef(9, 9))
	j.signed("f", lhash(1), noParent, 0, k1)
	j.unsigned("f", lhash(1), "bearer-bob")        // no-op, unsigned
	j.signed("f", lhash(1), lhash(1), 1, k2)       // no-op, signed by another key
	j.add(LogEntry{Name: "f", Status: "rejected"}) // refused
	j.add(LogEntry{Name: "f", Status: "blocked", Hash: lhash(9)})
	j.add(LogEntry{Name: "f", Kind: "prove", Status: "accepted", Hash: lhash(1)})
	j.unsigned("g", lhash(9), "bearer-bob") // another name
	pr, bo := j.eval("f", lhash(1))
	wantTier(t, "props", pr, lineageKeySigned, 1, p1, "")
	wantTier(t, "body", bo, lineageKeySigned, 1, p1, "")

	// No-ops do not advance the revision either: a real change signed at
	// revision 1 after them is exactly the transition that happened.
	j.obj(lhash(2), ldef(2, 1))
	j.signed("f", lhash(2), lhash(1), 1, k2)
	pr, _ = j.eval("f", lhash(2))
	wantTier(t, "props after no-ops", pr, lineageKeySigned, len(j.es), hex.EncodeToString(k2.Public().(ed25519.PublicKey)), "")
}

// A -> B -> A: the lineage is re-established by the return to A, not inherited
// from A's first appearance — a hash is not a lineage position. And replaying
// A's original envelope on the return is not evidence: it signed parent "-" at
// revision 0, which is not the transition that happened.
func TestLineageABARepoint(t *testing.T) {
	for _, replay := range []bool{false, true} {
		j := newLJournal(t)
		k1, _ := lkey(t)
		k2, _ := lkey(t)
		j.obj(lhash(1), ldef(1, 1))
		j.obj(lhash(2), ldef(2, 2))
		first := j.signed("f", lhash(1), noParent, 0, k1)
		j.signed("f", lhash(2), lhash(1), 1, k2)
		back := j.unsigned("f", lhash(1), "bearer-bob")
		want := lineageReasonNoEnvelope
		if replay {
			back.EnvelopeB64, back.AuthorPubkey, back.AuthorSig = first.EnvelopeB64, first.AuthorPubkey, first.AuthorSig
			want = lineageReasonBadEnvelope
		}
		pr, bo := j.eval("f", lhash(1))
		wantTier(t, fmt.Sprintf("props (replay=%v)", replay), pr, lineageUnknown, 3, "", want)
		wantTier(t, fmt.Sprintf("body (replay=%v)", replay), bo, lineageUnknown, 3, "", want)
	}
}

// The same replay with the parent matching but the REVISION stale: A -> B -> A
// -> B, replaying the envelope for the first A -> B. Name, artifact and parent
// all agree; only parent_rev distinguishes it.
func TestLineageABAStaleRevisionIsNotEvidence(t *testing.T) {
	j := newLJournal(t)
	k1, _ := lkey(t)
	k2, _ := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(2, 2))
	j.signed("f", lhash(1), noParent, 0, k1)
	ab := j.signed("f", lhash(2), lhash(1), 1, k2)
	j.signed("f", lhash(1), lhash(2), 2, k1)
	again := j.unsigned("f", lhash(2), "")
	again.EnvelopeB64, again.AuthorPubkey, again.AuthorSig = ab.EnvelopeB64, ab.AuthorPubkey, ab.AuthorSig
	pr, bo := j.eval("f", lhash(2))
	wantTier(t, "props", pr, lineageUnknown, 4, "", lineageReasonBadEnvelope)
	wantTier(t, "body", bo, lineageUnknown, 4, "", lineageReasonBadEnvelope)
}

// A genuine signature over a DIFFERENT artifact is not evidence for this one.
func TestLineageEnvelopeForAnotherArtifact(t *testing.T) {
	j := newLJournal(t)
	k1, _ := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	e := j.signed("f", lhash(1), noParent, 0, k1)
	e.Hash = lhash(2)
	j.obj(lhash(2), ldef(2, 2))
	pr, _ := j.eval("f", lhash(2))
	wantTier(t, "props", pr, lineageUnknown, 1, "", lineageReasonBadEnvelope)
}

// A genuine signature naming the right artifact at the right revision but a
// DIFFERENT parent is not evidence for this transition.
func TestLineageEnvelopeForAnotherParent(t *testing.T) {
	j := newLJournal(t)
	k1, _ := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(2, 2))
	j.signed("f", lhash(1), noParent, 0, k1)
	j.signed("f", lhash(2), lhash(5), 1, k1) // signed against a parent that was never bound
	pr, bo := j.eval("f", lhash(2))
	wantTier(t, "props", pr, lineageUnknown, 2, "", lineageReasonBadEnvelope)
	wantTier(t, "body", bo, lineageUnknown, 2, "", lineageReasonBadEnvelope)
}

// A historical oath-publish/1 envelope is verified over its PERSISTED octets,
// under the format it was written in. Re-encoding it as the current format
// would add a license line the author never signed, and a valid historical
// signature would read as UNKNOWN.
func TestLineageV1EnvelopeVerifiesOverPersistedOctets(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	env := pubEnvelope{Op: "put", Name: "f", Artifact: lhash(1), Parent: noParent,
		ParentRev: big.NewInt(0), Author: p1, License: noLicense}
	v1 := envelopeEncodeAs(env, envelopeVersionV1)
	j.add(LogEntry{Author: p1, Name: "f", Status: "accepted", Hash: lhash(1),
		EnvelopeB64: encodeEnvelopeB64(v1), AuthorPubkey: p1,
		AuthorSig: hex.EncodeToString(ed25519.Sign(k1, v1))})
	pr, bo := j.eval("f", lhash(1))
	wantTier(t, "props", pr, lineageKeySigned, 1, p1, "")
	wantTier(t, "body", bo, lineageKeySigned, 1, p1, "")
}

// envelopeVerify is an error-returning path: an invalid envelope must be
// refused, not crash the caller through the panicking encoder.
func TestEnvelopeVerifyRefusesInvalidWithoutPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("envelopeVerify panicked on an invalid envelope: %v", r)
		}
	}()
	if err := envelopeVerify(pubEnvelope{Op: "put"}, strings.Repeat("00", 64)); err == nil {
		t.Fatal("an invalid envelope verified")
	}
}

// A signature that does not verify, and a verifying envelope whose key the
// entry records differently, are both refused rather than read as evidence.
func TestLineageForgedOrMisattributedEnvelope(t *testing.T) {
	for _, tc := range []string{"forged signature", "entry names another key"} {
		j := newLJournal(t)
		k1, _ := lkey(t)
		k2, p2 := lkey(t)
		j.obj(lhash(1), ldef(1, 1))
		e := j.signed("f", lhash(1), noParent, 0, k1)
		switch tc {
		case "forged signature":
			octets, _ := decodeEnvelopeB64(e.EnvelopeB64)
			e.AuthorSig = hex.EncodeToString(ed25519.Sign(k2, octets)) // right bytes, wrong signer
		case "entry names another key":
			e.AuthorPubkey = p2
		}
		pr, _ := j.eval("f", lhash(1))
		wantTier(t, tc, pr, lineageUnknown, 1, "", lineageReasonBadEnvelope)
	}
}

func TestLineageMissingHistory(t *testing.T) {
	j := newLJournal(t)
	k1, _ := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.signed("f", lhash(1), noParent, 0, k1)
	// The store says f is bound to something the journal never recorded.
	pr, bo := j.eval("f", lhash(7))
	wantTier(t, "props", pr, lineageUnknown, 0, "", lineageReasonNoHistory)
	wantTier(t, "body", bo, lineageUnknown, 0, "", lineageReasonNoHistory)
	// A name the journal has never seen.
	pr, _ = j.eval("nobody", lhash(1))
	wantTier(t, "props (unseen name)", pr, lineageUnknown, 0, "", lineageReasonNoHistory)
}

// An object the comparison needs is absent, so which lineage the transition
// changed is unknowable — until a later comparable transition changes a lineage
// itself, which re-establishes THAT lineage only.
func TestLineageMissingObject(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.signed("f", lhash(1), noParent, 0, k1)
	j.signed("f", lhash(2), lhash(1), 1, k1) // lhash(2) never stored
	pr, bo := j.eval("f", lhash(2))
	wantTier(t, "props", pr, lineageUnknown, 0, "", lineageReasonMissingObject)
	wantTier(t, "body", bo, lineageUnknown, 0, "", lineageReasonMissingObject)

	j.obj(lhash(3), ldef(3, 1))
	j.signed("f", lhash(3), lhash(2), 2, k1) // compares against the missing object
	pr, _ = j.eval("f", lhash(3))
	wantTier(t, "props after comparing to a missing object", pr, lineageUnknown, 0, "", lineageReasonMissingObject)

	j.obj(lhash(4), ldef(4, 1)) // props change only, comparable
	j.signed("f", lhash(4), lhash(3), 3, k1)
	pr, bo = j.eval("f", lhash(4))
	wantTier(t, "props", pr, lineageKeySigned, 4, p1, "")
	wantTier(t, "body", bo, lineageUnknown, 0, "", lineageReasonMissingObject)
}

// A signed first publication whose object is absent is not evidence about any
// lineage: there is nothing to have established. Covers both the only entry and
// a first publication a later comparable change builds on.
func TestLineageFirstPublicationObjectMissing(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	j.signed("f", lhash(1), noParent, 0, k1) // lhash(1) never stored
	pr, bo := j.eval("f", lhash(1))
	wantTier(t, "props", pr, lineageUnknown, 0, "", lineageReasonMissingObject)
	wantTier(t, "body", bo, lineageUnknown, 0, "", lineageReasonMissingObject)

	j.obj(lhash(2), ldef(2, 2))
	j.signed("f", lhash(2), lhash(1), 1, k1) // cannot be compared to the absent first object
	pr, _ = j.eval("f", lhash(2))
	wantTier(t, "props after first object missing", pr, lineageUnknown, 0, "", lineageReasonMissingObject)

	// Once the object is present, the same journal is KEY_SIGNED — the missing
	// object was the only reason.
	j2 := newLJournal(t)
	j2.obj(lhash(1), ldef(1, 1))
	j2.signed("f", lhash(1), noParent, 0, k1)
	pr, _ = j2.eval("f", lhash(1))
	wantTier(t, "control", pr, lineageKeySigned, 1, p1, "")
}

// A recorded prev that disagrees with the replay means history is missing or
// contradicted, so no lineage is judged from it — whether the disagreement is a
// first observed transition claiming a predecessor, or a later repoint.
func TestLineageInconsistentPrev(t *testing.T) {
	for _, tc := range []string{"first transition records a prev", "later prev disagrees"} {
		j := newLJournal(t)
		k1, _ := lkey(t)
		j.obj(lhash(1), ldef(1, 1))
		j.obj(lhash(2), ldef(2, 2))
		j.obj(lhash(3), ldef(3, 3))
		switch tc {
		case "first transition records a prev":
			e := j.unsigned("f", lhash(1), "alice")
			e.Prev = lhash(7) // a predecessor the journal never shows
		case "later prev disagrees":
			j.unsigned("f", lhash(1), "alice")
			e := j.unsigned("f", lhash(2), "alice")
			e.Prev = lhash(7)
		}
		// A later, honestly signed, comparable change does not repair it.
		last := j.es[len(j.es)-1].Hash
		j.signed("f", lhash(3), last, int64(len(j.es)), k1)
		pr, bo := j.eval("f", lhash(3))
		wantTier(t, tc+" props", pr, lineageUnknown, 0, "", lineageReasonInconsistent)
		wantTier(t, tc+" body", bo, lineageUnknown, 0, "", lineageReasonInconsistent)
	}
	// Control: the same journal with prev recorded CORRECTLY is judged normally.
	j := newLJournal(t)
	k1, p1 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(2, 2))
	j.unsigned("f", lhash(1), "alice")
	e := j.signed("f", lhash(2), lhash(1), 1, k1)
	e.Prev = lhash(1)
	pr, _ := j.eval("f", lhash(2))
	wantTier(t, "control", pr, lineageKeySigned, 2, p1, "")
}

// SPEC §8.6.6: the declared type and type-variable count belong to NEITHER
// lineage, and a component a kind does not carry is absent — so a func -> data
// change moves the body lineage (body and constructors both differ) and, with
// no props on either side, leaves props where it was.
func TestLineageComponentsExcludeTypeAndTyVars(t *testing.T) {
	j := newLJournal(t)
	k1, p1 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	retyped := ldef(1, 1)
	retyped.Ty, retyped.TyVars = &Ty{K: "bool"}, 3
	j.obj(lhash(2), retyped)
	j.signed("f", lhash(1), noParent, 0, k1)
	j.unsigned("f", lhash(2), "bearer-bob")
	pr, bo := j.eval("f", lhash(2))
	wantTier(t, "props after type-only change", pr, lineageKeySigned, 1, p1, "")
	wantTier(t, "body after type-only change", bo, lineageKeySigned, 1, p1, "")

	j2 := newLJournal(t)
	fn := &Def{K: "func", Body: &Term{K: "int", Int: big.NewInt(1)}}
	data := &Def{K: "data", Ctors: [][]Ty{{}}}
	j2.obj(lhash(1), fn)
	j2.obj(lhash(2), data)
	j2.signed("g", lhash(1), noParent, 0, k1)
	j2.unsigned("g", lhash(2), "bearer-bob")
	pr, bo = j2.eval("g", lhash(2))
	wantTier(t, "props across a kind change (absent both sides)", pr, lineageKeySigned, 1, p1, "")
	wantTier(t, "body across a kind change", bo, lineageUnknown, 2, "", lineageReasonNoEnvelope)
}

// The journal is NOT verified first (§8.6.6), so duplicate seqs are possible
// input. Transitions are associated by POSITION: a rejected entry sharing a seq
// with a later accepted one must not be replayed as the applied transition.
// Here the rejected entry (seq 2) and the real repoint (also seq 2) collide; a
// seq-keyed fold would treat the rejected B as applied, making an envelope
// signed against parent B at revision 2 look like the transition that happened.
func TestLineageDuplicateSeqDoesNotMisassociateTransitions(t *testing.T) {
	j := newLJournal(t)
	k1, _ := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(2, 2))
	j.obj(lhash(3), ldef(3, 3))
	j.signed("f", lhash(1), noParent, 0, k1)
	j.add(LogEntry{Name: "f", Status: "rejected", Hash: lhash(2)})
	forged := j.signed("f", lhash(3), lhash(2), 2, k1) // signed as if B had been applied
	j.es[1].Seq, forged.Seq = 2, 2
	pr, _ := j.eval("f", lhash(3))
	if pr.Tier == lineageKeySigned {
		t.Fatalf("a rejected entry sharing a seq was replayed as applied: %+v", pr)
	}
	wantTier(t, "props", pr, lineageUnknown, 2, "", lineageReasonBadEnvelope)
}

// Two spellings of ONE key must not read as two signers: an uppercase author /
// author_pubkey, or an uppercase signature, is not canonical (§8.6.1, §8.6.3)
// and is not evidence, even though hex decoding accepts it and it verifies.
func TestLineageNoncanonicalKeySpellingIsNotEvidence(t *testing.T) {
	for _, tc := range []string{"uppercase key", "uppercase signature"} {
		j := newLJournal(t)
		k1, p1 := lkey(t)
		j.obj(lhash(1), ldef(1, 1))
		// The octets are built as a FOREIGN writer would have left them, not via
		// envelopeEncode. This kernel now refuses a non-canonical author at
		// admission (ENV-AUTHOR-HEX), and envelopeEncode PANICS on an invalid
		// envelope — so routing through it would test this kernel's encoder
		// rather than the lineage path's treatment of journal bytes it did not
		// create. That is the case that matters: the journal is external data.
		env := pubEnvelope{Op: "put", Name: "f", Artifact: lhash(1), Parent: noParent,
			ParentRev: big.NewInt(0), Author: p1, License: noLicense}
		octets := envelopeEncode(env)
		key := p1
		if tc == "uppercase key" {
			key = strings.ToUpper(p1)
			octets = bytes.ReplaceAll(octets, []byte(p1), []byte(key))
		}
		// Signed over the octets AS STORED, so the refusal is about the spelling
		// and not about a signature that never matched.
		sig := hex.EncodeToString(ed25519.Sign(k1, octets))
		if tc == "uppercase signature" {
			sig = strings.ToUpper(sig)
		}
		j.add(LogEntry{Author: key, Name: "f", Status: "accepted", Hash: lhash(1),
			EnvelopeB64: encodeEnvelopeB64(octets), AuthorPubkey: key, AuthorSig: sig})
		pr, _ := j.eval("f", lhash(1))
		wantTier(t, tc, pr, lineageUnknown, 1, "", lineageReasonBadEnvelope)
	}
}

// Principal is the label the ESTABLISHING entry records, per lineage — not
// the latest writer's.
func TestLineagePrincipalIsTheEstablishingEntrysLabel(t *testing.T) {
	j := newLJournal(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(1, 2))
	j.unsigned("f", lhash(1), "alice")
	j.unsigned("f", lhash(2), "bob")
	pr, bo := j.eval("f", lhash(2))
	if pr.Principal != "alice" || bo.Principal != "bob" {
		t.Fatalf("principals = %q/%q, want alice/bob", pr.Principal, bo.Principal)
	}
}

// STABILITY: appending anything that is not a lineage-changing applied
// transition of this name leaves the answer identical. The control shows the
// comparison discriminates — a real change does move it.
func TestLineageStableUnderLaterAppends(t *testing.T) {
	j := newLJournal(t)
	k1, _ := lkey(t)
	k2, p2 := lkey(t)
	j.obj(lhash(1), ldef(1, 1))
	j.obj(lhash(2), ldef(1, 2))
	j.obj(lhash(9), ldef(9, 9))
	j.signed("f", lhash(1), noParent, 0, k1)
	j.signed("f", lhash(2), lhash(1), 1, k2)
	pr0, bo0 := j.eval("f", lhash(2))
	for i := 0; i < 50; i++ {
		j.unsigned("f", lhash(2), "bearer-bob")
		j.add(LogEntry{Name: "f", Status: "rejected"})
		j.add(LogEntry{Name: "f", Status: "pending", Hash: lhash(9)})
		j.unsigned(fmt.Sprintf("other%d", i), lhash(9), "")
	}
	pr1, bo1 := j.eval("f", lhash(2))
	if pr1 != pr0 || bo1 != bo0 {
		t.Fatalf("answer moved under non-lineage appends: %+v/%+v -> %+v/%+v", pr0, bo0, pr1, bo1)
	}
	if bo0.Seq != 2 || bo0.Pubkey != p2 {
		t.Fatalf("body should be established by the second write, got %+v", bo0)
	}
	// Control: a real body change does move it.
	j.obj(lhash(3), ldef(1, 3))
	j.unsigned("f", lhash(3), "bearer-bob")
	pr2, bo2 := j.eval("f", lhash(3))
	if pr2 != pr0 {
		t.Fatalf("props moved on a body-only change: %+v -> %+v", pr0, pr2)
	}
	if bo2 == bo0 || bo2.Tier != lineageUnknown {
		t.Fatalf("body did not move on a real body change: %+v", bo2)
	}
}

// THE COMMITTED CORPUS. Its journal carries no author envelope, so every live
// name's lineages must come back UNKNOWN — never a negative, never a silent
// default. Conditional on that measurement so it stays true once signed
// publications land; what it always pins is that the outcome is one of the two
// named values.
func TestLineageCommittedCorpus(t *testing.T) {
	root := filepath.Join("..", "codebase")
	if _, err := os.Stat(filepath.Join(root, "log.jsonl")); err != nil {
		t.Skip("committed store not present")
	}
	be, err := openFSBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := newStoreWithBackend(be, root)
	if err != nil {
		t.Fatal(err)
	}
	anyEnvelope := false
	for _, e := range st.ReadLog() {
		if e.EnvelopeB64 != "" || e.AuthorPubkey != "" {
			anyEnvelope = true
		}
	}
	names := st.Names()
	if len(names) == 0 {
		t.Fatal("committed store resolved no names")
	}
	for n := range names {
		pr, bo := lineageEvidenceOf(st, n)
		for _, lt := range []lineageTier{pr, bo} {
			if lt.Tier != lineageUnknown && lt.Tier != lineageKeySigned {
				t.Fatalf("%s: unnamed outcome %+v", n, lt)
			}
			if !anyEnvelope && lt.Tier != lineageUnknown {
				t.Fatalf("%s: %+v, but the journal carries no author envelope", n, lt)
			}
		}
	}
}
