package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const kernelVersion = "oath-kernel/0.7"

// Store is the codebase: a content-addressed object database plus a mutable
// name index. Objects are immutable — a "change" to a definition is a new
// object and a repointed name; nothing is ever edited in place, so dependents
// referencing the old hash can never break.
type Store struct {
	Root  string       // filesystem root (fs backend) or a descriptive label
	be    backend      // the byte-level storage seam (backend.go, docs/store-drivers.md)
	mu    sync.RWMutex // guards the in-memory caches so the worker can prove in parallel
	defs  map[string]*Def
	metas map[string]*Meta
	// signer, when set, signs every journal entry this store appends with an
	// Ed25519 key (docs/registry-auth.md). A principal IS a keypair: the
	// signature over the authored fields is unforgeable, offline-verifiable
	// authorship, so the host is not the root of trust. Unset = unattributed
	// (unsigned) puts.
	signer ed25519.PrivateKey
}

// SetSigner attaches an Ed25519 private key so subsequent AppendLog calls sign
// their entries. Passing nil clears it (back to unattributed).
func (s *Store) SetSigner(priv ed25519.PrivateKey) { s.signer = priv }

// cloudBackendOpener is installed by the cloud-tagged build (backend_cloud.go).
// It stays nil in the default zero-dependency build, so OATH_BACKEND has no
// effect unless the binary was built with `-tags cloud`.
var cloudBackendOpener func() (backend, string, error)

func OpenStore(root string) (*Store, error) {
	if os.Getenv("OATH_BACKEND") == "cloud" {
		if cloudBackendOpener == nil {
			return nil, fmt.Errorf("OATH_BACKEND=cloud but this binary was built without the cloud driver (build with -tags cloud)")
		}
		be, label, err := cloudBackendOpener()
		if err != nil {
			return nil, err
		}
		return newStoreWithBackend(be, label)
	}
	be, err := openFSBackend(root)
	if err != nil {
		return nil, err
	}
	return newStoreWithBackend(be, root)
}

// newStoreWithBackend builds a Store over any backend (fs, in-memory, cloud). It
// fails loudly on a corrupt name index at open time: names.json is not
// reconstructible from objects/, so treating unreadable bytes as an empty index
// would silently vanish every name — worse than refusing to start.
func newStoreWithBackend(be backend, root string) (*Store, error) {
	s := &Store{Root: root, be: be, defs: map[string]*Def{}, metas: map[string]*Meta{}}
	b, present, err := be.readNames()
	if err != nil {
		return nil, err
	}
	if err := validateNames(b, present, root+"/names.json"); err != nil {
		return nil, err
	}
	return s, nil
}

// writeFileAtomic writes via a temp file in the same directory, fsyncs, and
// renames into place, so a crash mid-write can never leave a truncated file.
// Both names.json and the journal are non-regenerable; in-place truncation of
// either is unrecoverable outside version control.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err == nil {
		err = f.Sync()
	} else {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Names returns the mutable name → hash index.
func (s *Store) Names() map[string]string {
	m := map[string]string{}
	if b, present, err := s.be.readNames(); err == nil && present {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *Store) Resolve(name string) (string, bool) {
	h, ok := s.Names()[name]
	return h, ok
}

func (s *Store) writeNames(m map[string]string) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return s.be.writeNames(b)
}

// Put stores a definition and points its name at the new hash.
// Returns the hash and the previous hash the name pointed at ("" if new).
//
// When the object already exists, metadata MERGES instead of clobbering,
// along the naming/verdict split (#19): verdict fields are facts about the
// hash and survive (a proof of this object is still a proof of it —
// whoever re-puts it), while the previous name's naming block is preserved
// as an alias when the incoming name differs. Two structurally identical
// definitions are ONE object with several names; losing either name's
// constructor vocabulary breaks elaboration for its module's source.
func (s *Store) Put(d *Def, m *Meta) (string, string, error) {
	h, err := s.StoreObject(d, m)
	if err != nil {
		return "", "", err
	}
	prev, err := s.Repoint(m.Name, h)
	if err != nil {
		return "", "", err
	}
	return h, prev, nil
}

// StoreObject writes the object and its (merged) metadata WITHOUT touching
// the name index. Content addressing makes storage unconditional; whether a
// NAME may point at the object is a separate, policy-governed decision
// (Repoint). Returns the hash.
func (s *Store) StoreObject(d *Def, m *Meta) (string, error) {
	h := hashDef(d)
	if prev, err := s.GetMeta(h); err == nil {
		m.Guarantee = prev.Guarantee
		m.ProvenProps = prev.ProvenProps
		m.Hints = prev.Hints // author hints are hash-keyed facts; a re-put from source carries none
		m.MutantsKilled, m.MutantsTotal = prev.MutantsKilled, prev.MutantsTotal
		m.MutationCampaign = prev.MutationCampaign
		m.WaivedMutants = prev.WaivedMutants
		m.Termination = prev.Termination
		m.Confinement = prev.Confinement
		m.SpecAuthor = prev.SpecAuthor
		m.BodyAuthor = prev.BodyAuthor
		m.Aliases = prev.Aliases
		if prev.Name != m.Name {
			if m.Aliases == nil {
				m.Aliases = map[string]*AliasNaming{}
			}
			m.Aliases[prev.Name] = &AliasNaming{
				TyVarNames: prev.TyVarNames, CtorNames: prev.CtorNames,
				PropNames: prev.PropNames, ParamNames: prev.ParamNames,
			}
			delete(m.Aliases, m.Name)
		}
	}
	if err := s.be.putObject(h, encodeDef(d)); err != nil {
		return "", err
	}
	mb := encodeMeta(m)
	if err := s.be.putMeta(h, mb); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.defs[h] = d
	mm := *m
	s.metas[h] = &mm
	s.mu.Unlock()
	return h, nil
}

// Repoint points name at h. Returns the previous hash ("" if the name is
// new or already pointed at h).
// Repoint binds `name` to `h` and returns the hash it pointed at BEFORE — the
// previous binding, always, including when it already pointed at `h`.
//
// It used to collapse an unchanged binding to "", which destroyed the distinction
// between "there was no previous value" and "the previous value was the same one".
// That mattered the moment a publication signature bound the parent: a same-hash
// re-publication journalled prev="" while its envelope named the real parent, so
// the entry disagreed with the statement its author signed and the journal failed
// its own verifier. A correctly signed, correctly accepted publication produced a
// broken journal.
//
// Callers that need to know whether the binding MOVED compare prev against h.
func (s *Store) Repoint(name, h string) (string, error) {
	release, err := s.be.lock()
	if err != nil {
		return "", err
	}
	defer release()
	names := s.Names()
	prev := names[name]
	names[name] = h
	if err := s.writeNames(names); err != nil {
		return "", err
	}
	return prev, nil
}

// CacheDef registers a definition in memory only — used to evaluate
// candidate/mutant definitions without admitting them to the codebase.
func (s *Store) CacheDef(h string, d *Def) { s.mu.Lock(); s.defs[h] = d; s.mu.Unlock() }

func (s *Store) GetDef(h string) (*Def, error) {
	s.mu.RLock()
	d0, ok := s.defs[h]
	s.mu.RUnlock()
	if ok {
		return d0, nil
	}
	b, ok, err := s.be.getObject(h)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no definition with hash %s", shortHash(h))
	}
	// Identity check on the raw bytes first: the file's content IS the
	// canonical encoding, so its SHA-256 must be its own name.
	if got := hex.EncodeToString(func() []byte { s := sha256.Sum256(b); return s[:] }()); got != h {
		return nil, fmt.Errorf("object hash mismatch: file %s contains %s", shortHash(h), shortHash(got))
	}
	dp, err := decodeDef(b)
	if err != nil {
		return nil, fmt.Errorf("stored object %s: %w", shortHash(h), err)
	}
	d := *dp
	// Content addressing proves the bytes are intact, not that they encode a
	// well-formed definition. An object written directly into the store (the
	// team/hosted-store threat model) never passed the gate, and the
	// typechecker and evaluator are not total on malformed Defs — a nil Ty or
	// Body would panic them. Re-validate on load so the store is trusted
	// because it is checked, not merely because it is content-addressed.
	// Cache before checking: checkDef resolves dependency hashes through
	// GetDef, and self-reference never goes through a hash, so this cannot
	// recurse on h; a valid def stays cached, an invalid one is evicted. The
	// cache mutex is released around checkDef, which re-enters GetDef for deps.
	s.mu.Lock()
	s.defs[h] = &d
	s.mu.Unlock()
	if err := checkDef(s, &d); err != nil {
		s.mu.Lock()
		delete(s.defs, h)
		s.mu.Unlock()
		return nil, fmt.Errorf("stored object %s is not well-formed: %w", shortHash(h), err)
	}
	return &d, nil
}

// RefreshMutable drops the cached MUTABLE state so the next read observes what
// is actually on the backend right now (#70).
//
// A serve instance and a prove-worker are SEPARATE PROCESSES over one shared
// store: the worker records proofs out of band, and a long-lived reader that
// cached metadata at startup keeps serving the verdicts it saw then — for the
// registry that meant the proven count sat frozen at 73 for hours while the
// worker had actually advanced the store to 99, and only a redeploy (a fresh
// process) revealed it. That is not a display nit: the registry's entire claim
// is that clients read verdicts it re-derived, so serving a stale one is serving
// a false one.
//
// Only `metas` is dropped. `defs` is deliberately NOT: objects are
// content-addressed, so the bytes under a hash can never change — an immutable
// cache is correct by construction, and it holds the expensive part (decoded
// ASTs). Metadata is the only thing that legitimately moves under a fixed hash,
// and it is small JSON. The name index is not cached at all (Names() always
// reads through), so it was never stale.
func (s *Store) RefreshMutable() {
	s.mu.Lock()
	s.metas = map[string]*Meta{}
	s.mu.Unlock()
}

func (s *Store) GetMeta(h string) (*Meta, error) {
	s.mu.RLock()
	m0, ok := s.metas[h]
	s.mu.RUnlock()
	if ok {
		return m0, nil
	}
	b, ok, err := s.be.getMeta(h)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no metadata for hash %s", shortHash(h))
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.metas[h] = &m
	s.mu.Unlock()
	return &m, nil
}

// SetMeta rewrites a definition's metadata (names, guarantee). Metadata is
// mutable precisely because it is not part of the definition's identity.
// encodeMeta is the CANONICAL encoding for a metadata record: compact JSON, no
// trailing newline, member order from the Meta struct's declaration.
//
// It exists because there was no stable writer. Three call sites each called
// MarshalIndent while the committed store held compact JSON, so touching any
// object rewrote its metadata file with identical CONTENT and different BYTES —
// a no-op update produced a diff, and the committed corpus could not be
// reproduced by the kernel shipping with it (#100).
//
// INDENTED is canonical because it is what the corpus already contains and what
// the kernel already writes: 174 of 197 committed records, against 23 legacy
// compact stragglers. Both formats arrived in ONE commit — a corpus rebuild that
// rewrote metadata for the objects it re-put and left the rest at their older
// encoding — so the split is residue, not a decision.
//
// (An earlier reading of this had it backwards, from sampling a single file that
// happened to be compact. The direction was corrected by counting.)
//
// The general rule this is an instance of: a representation does not need to
// participate in SEMANTIC identity to require CANONICAL BYTES. Artifact hashes
// protect identity; canonical store bytes protect reproducibility,
// reviewability, and no-op cleanliness, which are different properties.
func encodeMeta(m *Meta) []byte {
	b, _ := json.MarshalIndent(m, "", "  ")
	return b
}

func (s *Store) SetMeta(h string, m *Meta) error {
	release, err := s.be.lock()
	if err != nil {
		return err
	}
	defer release()
	mb := encodeMeta(m)
	if err := s.be.putMeta(h, mb); err != nil {
		return err
	}
	mm := *m
	s.mu.Lock()
	s.metas[h] = &mm
	s.mu.Unlock()
	return nil
}

// NameOf returns the current name pointing at h, or a short hash if unnamed
// or superseded.
func (s *Store) NameOf(h string) string {
	for n, nh := range s.Names() {
		if nh == h {
			return n
		}
	}
	if m, err := s.GetMeta(h); err == nil {
		return m.Name + "@" + shortHash(h)
	}
	return "#" + shortHash(h)
}

// FindCtor resolves a constructor name to (ADT hash, constructor index),
// searching only ADTs currently pointed at by the name index.
func (s *Store) FindCtor(name string) (string, int, bool) {
	names := s.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h := names[k]
		m, err := s.GetMeta(h)
		if err != nil {
			continue
		}
		// The name k resolves through its OWN naming block: the alias entry
		// when this name is not the object's most recent one (#19).
		ctors := m.CtorNames
		if m.Name != k {
			if a, ok := m.Aliases[k]; ok {
				ctors = a.CtorNames
			}
		}
		for i, cn := range ctors {
			if cn == name {
				return h, i, true
			}
		}
	}
	return "", 0, false
}

// LogEntry is one line of the append-only submission journal: every put
// attempt is retained — including typecheck rejections, which store no
// object and would otherwise vanish — attributed to a principal and stamped
// with the verifier version that judged it. The journal is audit metadata:
// it is never hashed, so the wall-clock timestamp does not violate the
// kernel's no-clocks rule, which protects verification semantics only.
type LogEntry struct {
	Seq         int    `json:"seq"`
	Time        string `json:"time"`
	Author      string `json:"author"`
	Verifier    string `json:"verifier"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	Status      string `json:"status"` // accepted | falsified | rejected
	Hash        string `json:"hash,omitempty"`
	Prev        string `json:"prev,omitempty"` // hash the name pointed at before this repoint
	Error       string `json:"error,omitempty"`
	Guarantee   string `json:"guarantee,omitempty"`
	Termination string `json:"termination,omitempty"`
	Context     string `json:"context,omitempty"` // hash of the context slice the author built against (#4)
	Pubkey      string `json:"pubkey,omitempty"`  // hex Ed25519 public key of the signer; absent = unattributed (#14)
	Sig         string `json:"sig,omitempty"`     // hex Ed25519 signature over the entry's authored fields (docs/registry-auth.md)
	// The AUTHOR's half of the record (#83), distinct from Pubkey/Sig above, which
	// are whatever key the STORE was configured with signing the derived entry.
	//
	// EnvelopeB64 holds the EXACT canonical octets the author signed, base64-encoded
	// — persisted, never reconstructed from the fields below. Reconstruction would let
	// a future encoder change silently invalidate historical signatures: the signature
	// would still be correct and would no longer verify, the worst failure an audit
	// trail has. These octets are the historical statement; the duplicated fields are
	// only its interpretation, and VerifyLog rejects any disagreement between them.
	//
	// BASE64 IS A STORAGE REPRESENTATION, NOT PART OF THE STATEMENT. Envelope octets
	// contain LFs and a journal is one JSON object per line, so they cannot appear
	// literally; "verbatim" therefore means the exact octets are recoverable as the
	// DECODED value of this field. Verification decodes and verifies over the
	// recovered octets — never over the base64 text, never over JSON string bytes.
	// The encoding is named in the field so a reader cannot mistake one representation
	// for another.
	EnvelopeB64  string `json:"envelope_b64,omitempty"`
	AuthorPubkey string `json:"author_pubkey,omitempty"` // hex Ed25519 key that signed the envelope
	AuthorSig    string `json:"author_sig,omitempty"`    // hex signature over the DECODED envelope octets
	// RecipientSig is the SECOND signature over the SAME octets, used only by
	// transfer: custody carries obligations, so a prefix cannot be pushed onto a
	// key that never accepted it. It is a separate field rather than a second
	// entry because one statement with two signatures cannot be detached and
	// replayed against a different transfer, and two documents can.
	RecipientSig string `json:"recipient_sig,omitempty"`
	// ParentRev is the revision the author SIGNED AGAINST — their own claim about
	// what they were publishing over, preserved verbatim. Without it the author's
	// statement disappears at admission and a verifier can only reconstruct the
	// number from history, so ABA replay is undetectable offline: the envelope says
	// "revision 37" and nothing durable records that it did.
	//
	// A STRING, never a JSON number. §8.6.1 makes the revision unbounded, and a
	// float64 JSON reader corrupts values past 2^53 — a defect found in the
	// envelope fixtures, where the vector demonstrating arbitrary precision was
	// itself carried in a lossy form.
	ParentRev string `json:"parent_rev,omitempty"`
	// NameTransition records what happened to the NAME — a different dimension from
	// Status, which records what the registry concluded about the ARTIFACT. A
	// definition can be FALSIFIED and still bind its name; a request can be REJECTED
	// and belong in the journal without moving anything; a `prove` or `cross` entry
	// concerns an artifact and touches no name at all. Deriving one dimension from
	// the other is what made this fragile twice, so it is recorded directly:
	//
	//   applied    the name binding CHANGED (A -> B, or first publication)
	//   unchanged  a valid publication targeted the hash already bound (A -> A)
	//   none       no name operation occurred: rejected, blocked, pending, prove, cross
	//
	// Only `applied` versions the binding. That is what parent_rev must count: a
	// revision is a STATE version, not a publication counter. If a same-hash re-put
	// advanced it, a harmless no-op would invalidate every envelope prepared against
	// a state that never changed — duplicating the journal's event count while
	// weakening the CAS model. ABA still closes, because A -> B -> A increments twice
	// and an old envelope for the first A carries the wrong revision.
	//
	// Empty on entries written before this field existed; see nameTransitionOf.
	NameTransition string `json:"name_transition,omitempty"`
	Chain          string `json:"chain,omitempty"` // tamper-evidence: SHA-256(prev chain + this entry sans chain)
}

// signedContent is the deterministic byte string a signer signs: the entry with
// the store-assigned fields (seq, time, verifier, chain) and the signature
// itself zeroed. So the signature covers exactly the AUTHORED fields — pubkey,
// author label, name, kind, status, object hash, prior hash, verdicts, context —
// independent of where the entry lands in the log. The chain seals ordering on
// top; the signature seals authorship. (docs/registry-auth.md)
// Name-transition values. See LogEntry.NameTransition.
const (
	transitionApplied   = "applied"
	transitionUnchanged = "unchanged"
	transitionNone      = "none"
)

// repointedName reports whether this entry MOVED the name it records.
//
// Defined in one place because two things depend on agreeing about it: the
// per-name revision that anchors replay protection, and any future reader
// reconstructing a name's history. Getting it wrong is quiet — an undercounted
// revision does not fail, it just stops distinguishing an A→B→A cycle, which is
// the one thing the revision exists to do.
//
// FALSIFIED ENTRIES REPOINT. A definition whose properties fail is still stored
// and still binds its name: falsification is an honest recorded verdict, not a
// rejection (a policy with forbid_falsified turns it into "blocked" instead, and
// blocked does NOT move the name). The committed corpus contains falsified entries
// carrying `prev`, so this is observed behaviour, not a hypothetical.
//
// rejected / blocked / pending do NOT repoint: each returns before Repoint. They
// belong to the history of what HAPPENED, but an attempt that never moved the name
// must not advance a revision — otherwise an invalid attempt could invalidate an
// already-prepared legitimate envelope, or make client and registry disagree about
// the current parent.
// repointedName is valid ONLY for entries that declare their transition. Legacy
// entries cannot be classified in isolation — see nameTransitions.
func (e *LogEntry) repointedName() bool {
	return e.nameTransitionOf() == transitionApplied
}

// nameTransitions folds the journal for one name, yielding each of its entries with
// its EFFECTIVE transition.
//
// A fold is required, not a convenience. For legacy entries the obvious per-entry
// test — "prev equals hash, so it was a no-op" — CANNOT WORK, because the rule those
// entries were written under omitted `prev` whenever the name already pointed at the
// same hash. A legacy no-op therefore has no `prev` at all, and an absent `prev` is
// irrecoverably ambiguous between "the name was new" and "the name was already
// here". 526 entries in the committed corpus are in exactly that state.
//
// Testing prev==hash on legacy data does not merely miss some no-ops; it misses ALL
// of them, so every repeated publication counts as a state change. Measured on the
// committed corpus: 169 of 187 names get an inflated revision, one of them counting
// 8 revisions for a single distinct state. That turns the revision back into the
// publication counter it is explicitly not, and quietly falsifies the replay story
// for the whole existing corpus.
//
// Folding recovers the truth without needing `prev`: track what the name is bound to
// and compare each entry's hash against it. Declared transitions always win where
// present; derivation applies only to entries that predate the field.
func nameTransitions(entries []LogEntry, name string) []struct {
	Entry      LogEntry
	Transition string
	Stated     string
	Disagrees  bool
} {
	var out []struct {
		Entry      LogEntry
		Transition string
		Stated     string
		Disagrees  bool
	}
	bound := "" // what `name` is bound to as of the entries seen so far
	for _, e := range entries {
		if e.Name != name {
			continue
		}
		// SPEC §8.6.4 ENV-VERIFY-DERIVED-TRANSITION. The transition is DERIVED from
		// journal history and the stored member is only cross-checked. It used to
		// be the reverse — "declared transitions always win where present" — which
		// inverted the trust model for the one field deciding whether clause 5
		// runs at all: a store could label a transition `unchanged`, attach a
		// genuine signature over an unrelated envelope, and the entry passed every
		// clause.
		t := deriveTransition(&e, bound)
		disagrees := e.NameTransition != "" && e.NameTransition != t
		if t == transitionApplied {
			bound = e.Hash
		}
		out = append(out, struct {
			Entry      LogEntry
			Transition string
			Stated     string
			Disagrees  bool
		}{e, t, e.NameTransition, disagrees})
	}
	return out
}

// legacyTransition derives the transition of a pre-field entry, given what the name
// was bound to immediately before it.
// deriveTransition computes an entry's transition from the entry and what the name
// was bound to immediately before it. This is the AUTHORITATIVE value (§8.6.4
// ENV-VERIFY-DERIVED-TRANSITION); a stored `name_transition` is cross-checked
// against it, never trusted in its place. For entries predating the field there is
// nothing to cross-check, and the result is reported as reconstructed.
func deriveTransition(e *LogEntry, bound string) string {
	switch e.Kind {
	case "data", "func", "":
	default:
		// `prove` and `cross` entries concern an ARTIFACT and touch no name.
		return transitionNone
	}
	switch e.Status {
	case "accepted", "falsified":
		if e.Hash != "" && e.Hash == bound {
			return transitionUnchanged
		}
		return transitionApplied
	}
	return transitionNone
}

// nameTransitionOf returns the entry's name transition, deriving it only for
// LEGACY entries written before the field existed.
//
// The legacy derivation is quarantined here rather than spread through callers, so
// there is exactly one place where inference happens and it is visibly confined to
// old data. New entries state their transition and are never inferred.
//
// Legacy rules, and why each is what it is:
//   - kinds other than data/func never touched a name (`prove` and `cross` entries
//     concern an artifact), so they are `none` regardless of status. Missing this is
//     how a proof-worker entry could inflate a name's revision;
//   - accepted/falsified DID bind the name, so they are transitions. Falsified must
//     stay included: falsified entries in the committed corpus carry `prev`;
//   - a legacy entry whose prev EQUALS its hash was a no-op, so `unchanged`. Old
//     entries collapsed an unchanged binding to prev="", so this is rarely
//     detectable in practice — which is precisely why the collapse was removed.
func (e *LogEntry) nameTransitionOf() string {
	if e.NameTransition != "" {
		return e.NameTransition
	}
	switch e.Kind {
	case "data", "func", "":
	default:
		return transitionNone
	}
	switch e.Status {
	case "accepted", "falsified":
		if e.Prev != "" && e.Prev == e.Hash {
			return transitionUnchanged
		}
		return transitionApplied
	}
	return transitionNone
}

func signedContent(e *LogEntry) []byte {
	c := *e
	c.Seq = 0
	c.Time = ""
	c.Verifier = ""
	c.Chain = ""
	c.Sig = ""
	// Canonical encoder, not json.Marshal: the signature is over these bytes, so the
	// writer must produce exactly what a canonical reader reconstructs.
	b, _ := canonicalJournalLine(&c)
	return b
}

// chainHash links one journal entry to everything before it: SHA-256 of the
// previous anchor followed by a newline and the entry's compact JSON with the
// chain field empty.
func chainHash(prev string, body []byte) string {
	h := sha256.Sum256(append([]byte(prev+"\n"), body...))
	return hex.EncodeToString(h[:])
}

// chainAnchor returns the anchor for the next entry: the chain of the most
// recent chained entry, or — for a journal written before chaining existed —
// the hash of the entire legacy prefix, which retroactively seals those lines
// (any edit to them breaks the first chained entry's verification).
func chainAnchor(prior []byte) string {
	lines := strings.Split(strings.TrimRight(string(prior), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var e LogEntry
		if json.Unmarshal([]byte(lines[i]), &e) == nil && e.Chain != "" {
			return e.Chain
		}
	}
	h := sha256.Sum256(prior)
	return hex.EncodeToString(h[:])
}

func (s *Store) AppendLog(e *LogEntry) error {
	// Serialize the read-prior→append critical section: the chain hash is
	// computed from the current tail, so an interleaved append by another writer
	// would fork the chain (#14). No-op unless OATH_STORE_LOCK is set.
	release, err := s.be.lock()
	if err != nil {
		return err
	}
	defer release()
	e.Verifier = kernelVersion
	e.Time = time.Now().UTC().Format(time.RFC3339)
	prior, _ := s.be.readJournal() // absent → empty prefix, anchor = sha256("")
	e.Seq = strings.Count(string(prior), "\n") + 1
	// Sign the authored fields before chaining, so the chain seals the signature
	// too. signedContent zeroes the store-assigned fields, so the signature is
	// independent of where this entry lands (docs/registry-auth.md).
	if s.signer != nil {
		e.Pubkey = hex.EncodeToString(s.signer.Public().(ed25519.PublicKey))
		e.Sig = hex.EncodeToString(ed25519.Sign(s.signer, signedContent(e)))
	}
	e.Chain = ""
	body, _ := canonicalJournalLine(e)
	e.Chain = chainHash(chainAnchor(prior), body)
	b, _ := canonicalJournalLine(e)
	err = s.be.appendJournal(append(b, '\n'))
	return err
}

// VerifyLog replays the journal's hash chain and sequence numbers, returning
// the first inconsistency: an unparseable line, a seq gap, an unchained entry
// after a chained one, or a chain mismatch (an edited, inserted, or deleted
// line — including edits to the pre-chain legacy prefix, which the first
// chained entry seals by hashing). One honest limitation is inherent to any
// append-only log without an external anchor: deleting entries from the TAIL
// leaves a self-consistent file. The committed git history is that anchor.
func (s *Store) VerifyLog() error {
	b, err := s.be.readJournal()
	if err != nil {
		return err
	}
	var prev string
	chained := false
	pos := 0 // byte offset of the current line, for the legacy-prefix anchor
	line := 0
	for pos < len(b) {
		end := pos
		for end < len(b) && b[end] != '\n' {
			end++
		}
		raw := b[pos:end]
		line++
		var e LogEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return fmt.Errorf("journal line %d is not valid JSON: %v", line, err)
		}
		if e.Seq != line {
			return fmt.Errorf("journal line %d has seq %d: entries are missing or reordered", line, e.Seq)
		}
		// A signed entry must carry a signature valid under its own pubkey over
		// its authored fields. Unsigned (no pubkey) entries are allowed — they
		// are unattributed. A pubkey without a valid signature, or vice versa, is
		// a forged or corrupted attribution.
		if e.Pubkey != "" || e.Sig != "" {
			pub, perr := hex.DecodeString(e.Pubkey)
			sig, serr := hex.DecodeString(e.Sig)
			if perr != nil || serr != nil || len(pub) != ed25519.PublicKeySize {
				return fmt.Errorf("journal line %d has a malformed signature or pubkey", line)
			}
			if !ed25519.Verify(ed25519.PublicKey(pub), signedContent(&e), sig) {
				return fmt.Errorf("journal line %d fails signature verification: the entry was tampered with or is not signed by %s", line, e.Pubkey)
			}
		}
		// The AUTHOR's publication statement (#83). Verified against the PERSISTED
		// envelope bytes, never a re-encoding, so an old entry is checked under the
		// format it was written with rather than whatever the current encoder emits.
		if e.EnvelopeB64 != "" || e.AuthorSig != "" || e.AuthorPubkey != "" {
			if e.EnvelopeB64 == "" || e.AuthorSig == "" || e.AuthorPubkey == "" {
				return fmt.Errorf("journal line %d has a partial author record (envelope=%v key=%v sig=%v): all three or none, since any one alone attests to nothing",
					line, e.EnvelopeB64 != "", e.AuthorPubkey != "", e.AuthorSig != "")
			}
			octets, derr := decodeEnvelopeB64(e.EnvelopeB64)
			if derr != nil {
				return fmt.Errorf("journal line %d: %w", line, derr)
			}
			// An AUTHORITY statement (#66) is a different signed format in the same
			// three fields. It is routed by its FORMAT LINE — which is inside the signed
			// octets — and never by the entry's Kind, which the registry wrote and
			// nobody signed. Verifying a statement under a format its author never used
			// is indistinguishable, to a later reader, from verifying it.
			//
			// An `else` rather than an early return, and the distinction is not
			// stylistic: the chain verification for this line lives BELOW this block,
			// inside the same loop. Returning here would skip it and abandon every
			// later line — silently weakening tamper-evidence in the name of narrowing
			// one clause, which is the trap the §8.6.4 clause-5 comment below already
			// records having fallen into once.
			if authorStatementKind(octets) == "delegation" {
				del, perr := parseDelegateEnvelope(octets)
				if perr != nil {
					return fmt.Errorf("journal line %d has an unparseable delegation envelope: %w", line, perr)
				}
				if del.Pubkey != e.AuthorPubkey {
					return fmt.Errorf("journal line %d: delegation names grantor %s but the entry records %s", line, del.Pubkey, e.AuthorPubkey)
				}
				if verr := delVerify(del, e.AuthorSig); verr != nil {
					return fmt.Errorf("journal line %d: delegation signature does not verify: %w", line, verr)
				}
			} else if authorStatementKind(octets) == "transfer" {
				// TWO signatures over ONE statement. Verifying only the author's
				// would let a transfer be recorded with a forged or absent
				// recipient consent, which is the whole property the second
				// signature exists to carry.
				xe, perr := parseTransferEnvelope(octets)
				if perr != nil {
					return fmt.Errorf("journal line %d has an unparseable transfer envelope: %w", line, perr)
				}
				if xe.FromAuthority != e.AuthorPubkey {
					return fmt.Errorf("journal line %d: transfer names holder %s but the entry records %s", line, xe.FromAuthority, e.AuthorPubkey)
				}
				if xe.Namespace != e.Name {
					return fmt.Errorf("journal line %d: transfer names %q but the entry records name %q", line, xe.Namespace, e.Name)
				}
				if verr := xferVerify(xe, e.AuthorSig, e.RecipientSig); verr != nil {
					return fmt.Errorf("journal line %d: transfer signatures do not verify: %w", line, verr)
				}
			} else if authorStatementKind(octets) == "reservation" {
				res, perr := parseReserveEnvelope(octets)
				if perr != nil {
					return fmt.Errorf("journal line %d has an unparseable authority envelope: %w", line, perr)
				}
				if res.Pubkey != e.AuthorPubkey {
					return fmt.Errorf("journal line %d: reservation names key %s but the entry records %s", line, res.Pubkey, e.AuthorPubkey)
				}
				if res.Namespace != e.Name {
					return fmt.Errorf("journal line %d: reservation claims %q but the entry records name %q — the registry recorded a namespace the signer did not claim", line, res.Namespace, e.Name)
				}
				if verr := resVerify(res, e.AuthorSig); verr != nil {
					return fmt.Errorf("journal line %d: authority signature does not verify: %w", line, verr)
				}
			} else {
				// Parse and verify over the RECOVERED OCTETS. The base64 text and the JSON
				// string bytes are storage representations; neither is the statement.
				env, perr := envelopeParse(octets)
				if perr != nil {
					return fmt.Errorf("journal line %d has an unparseable author envelope: %w", line, perr)
				}
				if env.Author != e.AuthorPubkey {
					return fmt.Errorf("journal line %d: envelope names author %s but the entry records %s", line, env.Author, e.AuthorPubkey)
				}
				if verr := envelopeVerify(env, e.AuthorSig); verr != nil {
					return fmt.Errorf("journal line %d: author signature does not verify: %w", line, verr)
				}
				// The duplicated fields are an INTERPRETATION of the signed bytes, so any
				// disagreement means the registry recorded a transition the author did not
				// sign. That is precisely the substitution this record exists to prevent,
				// so it is a hard failure rather than a warning.
				// Clause 5 (§8.6.4) applies ONLY to an entry that APPLIED a transition.
				//
				// A refused attempt legitimately records the state that caused the refusal —
				// a `prev` naming the current binding while the envelope names the stale one
				// it was signed against — so checking it unscoped would make the honest
				// record of a correct refusal fail the journal. A gate rejection is worse
				// still: it carries no `hash`, while `artifact` must be 64 hex, so the clause
				// could never hold.
				//
				// Scoped with an `if` rather than a `continue`: continuing would skip to the
				// next entry and bypass this entry's CHAIN verification too, silently
				// weakening tamper-evidence in the name of narrowing one clause.
				if e.nameTransitionOf() == transitionApplied {
					parent := e.Prev
					if parent == "" {
						parent = noParent
					}
					for _, m := range []struct {
						what, signed, recorded string
					}{
						{"name", env.Name, e.Name},
						{"artifact", env.Artifact, e.Hash},
						{"parent", env.Parent, parent},
					} {
						if m.signed != m.recorded {
							return fmt.Errorf("journal line %d: the author signed %s=%q but the entry records %q — the registry recorded a transition its author did not sign",
								line, m.what, m.signed, m.recorded)
						}
					}
				}
			}
		}
		if e.Chain == "" {
			if chained {
				return fmt.Errorf("journal line %d is unchained after a chained entry", line)
			}
		} else {
			if !chained {
				h := sha256.Sum256(b[:pos])
				prev = hex.EncodeToString(h[:])
				chained = true
			}
			want := e.Chain
			e.Chain = ""
			body, _ := canonicalJournalLine(&e)
			if chainHash(prev, body) != want {
				return fmt.Errorf("journal line %d fails the hash chain: this or an earlier line was edited, inserted, or deleted", line)
			}
			prev = want
		}
		pos = end + 1
	}
	return nil
}

func (s *Store) ReadLog() []LogEntry {
	b, err := s.be.readJournal()
	if err != nil {
		return nil
	}
	var out []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e LogEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// AllHashes lists every object in the store, sorted.
func (s *Store) AllHashes() []string {
	out, err := s.be.listObjects()
	if err != nil {
		return nil
	}
	return out
}

// Canonical base64 for persisted envelope octets (SPEC §8.6.3).
//
// Dialect pinned deliberately, because "base64" alone admits several spellings of
// one byte string and this field is compared and re-encoded: STANDARD alphabet
// (RFC 4648 §4, +/), padding REQUIRED, no line breaks, no whitespace, no
// URL-safe substitutions.
//
// decodeEnvelopeB64 additionally requires that re-encoding the decoded octets
// reproduces the input string exactly. Without that, alternate spellings — stray
// whitespace, missing padding, non-canonical trailing bits — would decode to the
// same octets from different stored bytes, which is the same unique-decodability
// property the envelope encoding itself insists on. Canonical writing with lenient
// reading protects nothing.
func encodeEnvelopeB64(octets []byte) string {
	return base64.StdEncoding.EncodeToString(octets)
}

func decodeEnvelopeB64(s string) ([]byte, error) {
	octets, err := base64.StdEncoding.DecodeString(s)
	if err != nil && ruleOn("ENV-B64-DIALECT") {
		return nil, fmt.Errorf("envelope_b64 is not standard padded base64: %w", err)
	}
	if ruleOn("ENV-B64-CANONICAL") && encodeEnvelopeB64(octets) != s {
		return nil, fmt.Errorf("envelope_b64 is not canonical: it decodes, but re-encoding yields different text (whitespace, missing padding, or non-canonical trailing bits)")
	}
	return octets, nil
}

// journalFieldOrder is the NORMATIVE field order of a journal entry's compact JSON
// (SPEC §8.1). It is not a formatting preference: `chain` hashes the entry and §8.4
// signs it, both over "the entry's compact JSON", so two implementations ordering
// these differently compute different chain values and different signatures — and
// each then rejects the other's journal wholesale. An undefined order is a
// byte-level fork waiting for a second implementation.
//
// Must stay in lockstep with the LogEntry struct's declaration order, which is what
// encoding/json emits. TestJournalFieldOrderIsNormative asserts they agree, so this
// list cannot silently drift from the bytes.
var journalFieldOrder = []string{
	"seq", "time", "author", "verifier", "name", "kind", "status", "hash", "prev",
	"error", "guarantee", "termination", "context", "pubkey", "sig",
	"envelope_b64", "author_pubkey", "author_sig", "parent_rev", "name_transition", "chain",
}

// canonicalJournalLine re-encodes an entry to its canonical compact JSON.
//
// STRING ESCAPING IS PINNED, and this is why it cannot just be json.Marshal:
// member order alone is not enough for byte agreement while string VALUES still
// have several valid JSON spellings. `error` and `guarantee` are free-form, so a
// rejection message containing "<" is enough to fork two kernels — and since chain
// hashes, entry signatures and entry digests are all over these bytes, a fork there
// means the two reject each other's journals wholesale.
//
// Go's default encoder escapes "<", ">" and "&" as \u003c/\u003e/\u0026 for HTML
// safety. That is a language-specific habit no independent implementer would
// reproduce from RFC 8259, so it is disabled: the canonical form escapes ONLY what
// JSON requires — '"', '\', and control characters below U+0020 — leaving all other
// characters, including non-ASCII, as literal UTF-8.
//
// The one deliberate exception is U+2028 and U+2029, which Go escapes regardless and
// which the canonical form REQUIRES escaped. That is not an accommodation of the
// encoder: both are Unicode line terminators, and a journal is a line-delimited
// format, so a literal one inside an entry invites a reader that splits on Unicode
// line boundaries to see two records where there is one.
func canonicalJournalLine(e *LogEntry) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	// Encoder appends a newline; the canonical line is the object bytes alone. The
	// record separator is a framing concern and is NOT part of entry identity, so it
	// must not reach the chain, the signature, or the digest.
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// strictJournalLine parses a stored line and requires that re-encoding it
// reproduces the input bytes exactly.
//
// The same rule the envelope encoding lives by, applied one layer out: canonical
// WRITING with lenient READING protects nothing, because a normative field order
// would then constrain only writers while readers happily accepted reordered,
// re-spaced or duplicate-keyed entries. Since `chain` and §8.4 signatures are
// computed over these bytes, accepting a second spelling of one entry means
// accepting two different identities for it.
func strictJournalLine(raw []byte) (*LogEntry, error) {
	var e LogEntry
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("entry is not a canonical journal object: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("entry has trailing content after the JSON object")
	}
	re, err := canonicalJournalLine(&e)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(re, raw) {
		return nil, fmt.Errorf("entry is not canonical: it parses, but re-encoding yields different bytes — field order, spacing or duplicate keys differ from the normative form (SPEC §8.1)")
	}
	return &e, nil
}

// entryDigest is the durable identity of ONE PUBLICATION: SHA-256 over the exact
// canonical journal line.
//
// An artifact hash identifies CONTENT, not a publication of that content. The same
// artifact can legitimately be published repeatedly — a same-hash re-publication is
// a valid recorded no-op — so "the publication of hash X" is ambiguous the moment
// there is more than one. Selecting the first or the last match is a guess about
// what the caller meant, and it happens to be right only until it isn't.
//
// The digest commits to the whole canonical line, `chain` included, so it also fixes
// the entry's position in the log: two entries identical in content but at different
// positions have different chains and therefore different digests. An ordinal is
// useful for humans and does not survive copying between stores; the digest does.
func entryDigest(e *LogEntry) (string, error) {
	line, err := canonicalJournalLine(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:]), nil
}
