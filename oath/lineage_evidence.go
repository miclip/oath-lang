package main

import (
	"crypto/ed25519"
	"fmt"
)

// Lineage evidence (#82): whether the write that ESTABLISHED a name's current
// props lineage, and separately its current body lineage, was key-signed by its
// author — DERIVED from the journal, never stored.
//
// Meta carries spec_author/body_author as plain LABELS inherited across
// revisions (attributeAuthorship), so a label cannot say whether a key stood
// behind it: two bearer tokens and two Ed25519 keys produce the same pair of
// distinct strings. The journal can. A lineage's content last changed at exactly
// one entry — the last APPLIED transition of the name whose object differs from
// the one it replaced in that lineage — and whether that entry carries a valid
// author envelope is a fact a third party re-derives from the published journal
// plus the immutable objects. Storing a copy in meta would duplicate an authority
// the journal already is, correct exactly once.
//
// WHY THE ANSWER IS STABLE AS THE JOURNAL GROWS. The fold replays §8's ordered,
// append-only journal through derivedTransitionsByIndex (the single authority for
// what moved a name) and compares IMMUTABLE objects. Appending an entry cannot
// change any earlier entry's transition — derivation depends only on what
// precedes it — and cannot change an object. So a later append changes the
// result only by being a new applied transition that itself changes a lineage,
// which is a genuine new establishing write. No-op republication (`unchanged`),
// rejected/blocked/pending attempts, `prove`/`cross` entries and other names'
// entries cannot move it.

// Lineage-evidence outcomes. Deliberately TWO: nothing in the journal can
// establish a definite "not key-signed". An entry without an author envelope may
// predate envelopes (#83) while its author label was itself a key, may be a
// worker's unsigned repoint of a signed pending submission, or may be a bearer
// write; the journal does not distinguish these. Absence of evidence is
// UNKNOWN, and must never be reported as a negative.
const (
	lineageKeySigned = "KEY_SIGNED" // a valid author envelope signed this exact transition
	lineageUnknown   = "UNKNOWN"    // the establishing write's tier cannot be recovered; see Reason
)

// Reasons attached to an UNKNOWN outcome. Named so no caller has to parse prose,
// and so "unknown because unsigned-era" is never confused with "unknown because
// history is missing".
const (
	lineageReasonNoEnvelope        = "establishing entry carries no author envelope"
	lineageReasonBadEnvelope       = "establishing entry's author envelope does not verify for this transition"
	lineageReasonNotBound          = "name is not bound"
	lineageReasonNoHistory         = "journal history does not reach the current binding"
	lineageReasonMissingObject     = "an object in the lineage is not in the store"
	lineageReasonInconsistent      = "an applied entry's recorded prev disagrees with the replayed binding"
	lineageReasonUnreadableJournal = "a journal line does not parse, so the history may be incomplete"
)

// lineageTier is the derived evidence for ONE lineage.
type lineageTier struct {
	Tier   string `json:"tier"`                       // lineageKeySigned | lineageUnknown
	Seq    int    `json:"establishing_seq,omitempty"` // the establishing entry's seq; 0 when it could not be identified
	Pubkey string `json:"pubkey,omitempty"`           // the author key that signed it, only when Tier is KEY_SIGNED
	Reason string `json:"reason,omitempty"`           // why UNKNOWN; empty when KEY_SIGNED
	// Principal is the author label the ESTABLISHING ENTRY records — the
	// attribution for this lineage under THIS name. Meta's spec/body authors are
	// per-hash and an alias publication of the same object overwrites them, so a
	// claim pairing a lineage's key with its label must read the label here.
	Principal string `json:"principal,omitempty"`
}

// lineageEvidenceOf derives both lineages for the name's CURRENT binding in st.
func lineageEvidenceOf(st *Store, name string) (props, body lineageTier) {
	cur, ok := st.Resolve(name)
	if !ok {
		u := lineageTier{Tier: lineageUnknown, Reason: lineageReasonNotBound}
		return u, u
	}
	return lineageEvidenceAt(st, name, cur)
}

// lineageEvidenceAt derives both lineages for `name` bound to `h`, the binding
// a caller has ALREADY resolved. Resolving the name a second time would let a
// concurrent repoint pair this evidence with another binding's metadata; if the
// journal has moved past h, the replay no longer reaches it and both lineages
// come back UNKNOWN rather than describing the newer binding.
func lineageEvidenceAt(st *Store, name, h string) (props, body lineageTier) {
	// readLogWhole, not ReadLog: a line that does not parse is DROPPED by the
	// lossy reader, and this replay walks POSITIONS. A hole would let a later
	// signed entry look like a first transition and yield KEY_SIGNED on a
	// history never seen — a positive verdict from absent evidence, which is
	// the one failure this derivation exists to refuse.
	entries, whole := st.readLogWhole()
	if !whole {
		u := lineageTier{Tier: lineageUnknown, Reason: lineageReasonUnreadableJournal}
		return u, u
	}
	// getDefFromBackend, NOT GetDef: presence here is a claim about the STORE,
	// and GetDef answers from an in-memory cache that outlives a deleted object.
	//
	// TWO LIMITS OF THIS PRESENCE CHECK, stated because a positive verdict rests
	// on it and neither is closed here:
	//   1. It re-reads the requested object from the backend, but `checkDef`
	//      resolves that object's DEPENDENCIES through the cached `GetDef`. A
	//      dependency deleted after it was cached therefore still counts, so
	//      "present" means the top-level bytes are on the backend and the
	//      closure re-validates against memory. Closing it needs a
	//      cache-bypassing walk of the validation graph.
	//   2. The binding is identified by HASH. A concurrent repoint A->B->A
	//      between the caller's resolve and this read ends at the same hash and
	//      is accepted, so the evidence can describe the later return to A while
	//      the caller's other metadata was read before it. Closing it needs a
	//      binding GENERATION, not a hash.
	// Both are pre-existing shapes rather than consequences of this derivation,
	// and both would make a KEY_SIGNED slightly stronger than it is today.
	return lineageEvidence(entries, name, h, st.getDefFromBackend)
}

// lineageEvidence is the pure derivation: the journal, the name, the hash it is
// currently bound to, and a lookup for immutable objects.
//
// `current` is passed rather than taken from the fold so that a journal that
// does not reach the store's actual binding is reported as missing history
// instead of silently describing a different binding.
func lineageEvidence(entries []LogEntry, name, current string, getDef func(string) (*Def, error)) (props, body lineageTier) {
	unknown := func(reason string) lineageTier { return lineageTier{Tier: lineageUnknown, Reason: reason} }
	dt := derivedTransitionsByIndex(entries)

	bound, rev := "", 0 // the name's binding and applied-transition count so far
	var propsAt, bodyAt *LogEntry
	var propsParent, bodyParent string
	var propsRev, bodyRev int
	inconsistent := false
	for i := range entries {
		e := &entries[i]
		if e.Name != name || dt[i] != transitionApplied {
			continue
		}
		parent, parentRev := bound, rev
		bound, rev = e.Hash, rev+1
		// A recorded prev is the store's statement of what this write replaced.
		// If it disagrees with the replay — including a FIRST observed transition
		// that records a predecessor — the journal is missing or contradicting
		// history, so the replayed parents and revisions that every later
		// judgement rests on are not trustworthy. Sticky: a later write cannot
		// repair what the fold got wrong about earlier state.
		if e.Prev != "" && e.Prev != parent {
			inconsistent = true
		}
		if parent == "" {
			// First publication establishes both lineages — but only as evidence
			// about an object that exists, like every other establishing write.
			if d, err := getDef(e.Hash); err != nil || d == nil {
				propsAt, bodyAt = nil, nil
				continue
			}
			propsAt, bodyAt = e, e
			propsParent, bodyParent, propsRev, bodyRev = parent, parent, parentRev, parentRev
			continue
		}
		prevDef, perr := getDef(parent)
		newDef, nerr := getDef(e.Hash)
		if perr != nil || nerr != nil || prevDef == nil || newDef == nil {
			// Cannot tell whether this transition changed either lineage, so
			// neither establishing write is known from here on — until a later
			// transition that CAN be compared changes that lineage itself.
			propsAt, bodyAt = nil, nil
			continue
		}
		if !sameProps(newDef, prevDef) {
			propsAt, propsParent, propsRev = e, parent, parentRev
		}
		if !sameBody(newDef, prevDef) {
			bodyAt, bodyParent, bodyRev = e, parent, parentRev
		}
	}
	if bound == "" || bound != current {
		u := unknown(lineageReasonNoHistory)
		return u, u
	}
	if inconsistent {
		u := unknown(lineageReasonInconsistent)
		return u, u
	}
	judge := func(at *LogEntry, parent string, parentRev int) lineageTier {
		if at == nil {
			// The binding IS reached (checked above) and every first publication
			// whose object exists sets both, so an unset lineage can only mean a
			// needed object was absent.
			return unknown(lineageReasonMissingObject)
		}
		if at.EnvelopeB64 == "" && at.AuthorSig == "" && at.AuthorPubkey == "" {
			return lineageTier{Tier: lineageUnknown, Seq: at.Seq, Reason: lineageReasonNoEnvelope, Principal: at.Author}
		}
		if key, err := publicationSignedFor(at, parent, parentRev); err == nil {
			return lineageTier{Tier: lineageKeySigned, Seq: at.Seq, Pubkey: key, Principal: at.Author}
		}
		return lineageTier{Tier: lineageUnknown, Seq: at.Seq, Reason: lineageReasonBadEnvelope, Principal: at.Author}
	}
	return judge(propsAt, propsParent, propsRev), judge(bodyAt, bodyParent, bodyRev)
}

// publicationSignedFor verifies that e's author envelope is a valid PUBLICATION
// statement signing exactly this transition: this name, this artifact, the
// parent the fold says it replaced, at the revision the fold says it had. Any
// weaker check would let a genuine signature over a DIFFERENT statement stand as
// evidence for this one — the substitution §8.6.4 clause 5 exists to refuse.
//
// Only the author's envelope counts. The entry-level Pubkey/Sig pair is the
// STORE signing its own record: it proves the entry was not altered, and says
// nothing about who authored the content.
func publicationSignedFor(e *LogEntry, parent string, parentRev int) (string, error) {
	if e.EnvelopeB64 == "" || e.AuthorSig == "" || e.AuthorPubkey == "" {
		return "", fmt.Errorf("partial author record")
	}
	// Canonical spellings only (§8.6.1 ENV-AUTHOR-HEX, §8.6.3). hex.DecodeString
	// accepts uppercase, so without this two spellings of ONE key would count as
	// two distinct signers. The envelope's author must equal author_pubkey below,
	// so checking the recorded key covers both.
	if !isHash(e.AuthorPubkey) || !isLowerHex(e.AuthorSig, 2*ed25519.SignatureSize) {
		return "", fmt.Errorf("author key or signature is not canonical lowercase hex")
	}
	octets, err := decodeEnvelopeB64(e.EnvelopeB64)
	if err != nil {
		return "", err
	}
	// envelopeParse accepts only the publication formats, so a reservation,
	// delegation or transfer statement in these fields fails here.
	env, err := envelopeParse(octets)
	if err != nil {
		return "", err
	}
	if env.Author != e.AuthorPubkey {
		return "", fmt.Errorf("envelope author disagrees with the entry")
	}
	// Over the PERSISTED octets, never a re-encoding: an oath-publish/1
	// statement re-encoded as the current format carries a line its author
	// never signed.
	if err := envelopeVerifyOver(env, octets, e.AuthorSig); err != nil {
		return "", err
	}
	if parent == "" {
		parent = noParent
	}
	if env.Name != e.Name || env.Artifact != e.Hash || env.Parent != parent {
		return "", fmt.Errorf("envelope signs a different transition")
	}
	if env.ParentRev == nil || env.ParentRev.Cmp(revOf(parentRev)) != 0 {
		return "", fmt.Errorf("envelope signs a different revision")
	}
	return env.Author, nil
}

// sameProps and sameBody are THE definitions of "this lineage did not change",
// shared with attributeAuthorship so the label meta inherits and the evidence
// derived here cannot disagree about which write established a lineage.
func sameProps(a, b *Def) bool { return jsonEq(a.Props, b.Props) }
func sameBody(a, b *Def) bool  { return jsonEq(a.Body, b.Body) && jsonEq(a.Ctors, b.Ctors) }

// isLowerHex reports whether s is exactly n lowercase hex characters.
func isLowerHex(s string, n int) bool {
	if len(s) != n {
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
