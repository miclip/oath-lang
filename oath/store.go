package main

import (
	"crypto/ed25519"
	"crypto/sha256"
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
	mb, _ := json.MarshalIndent(m, "", "  ")
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
	if prev == h {
		prev = ""
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
func (s *Store) SetMeta(h string, m *Meta) error {
	release, err := s.be.lock()
	if err != nil {
		return err
	}
	defer release()
	mb, _ := json.MarshalIndent(m, "", "  ")
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
	// Envelope holds the EXACT canonical bytes the author signed — persisted, never
	// reconstructed from the fields below. Reconstruction would let a future encoder
	// change silently invalidate historical signatures: the signature would still be
	// correct and would no longer verify, the worst failure mode an audit trail has.
	// These bytes are the historical statement; the duplicated fields are only its
	// interpretation, and VerifyLog rejects any disagreement between them.
	Envelope     string `json:"envelope,omitempty"`
	AuthorPubkey string `json:"author_pubkey,omitempty"` // hex Ed25519 key that signed Envelope
	AuthorSig    string `json:"author_sig,omitempty"`    // hex signature over Envelope's exact bytes
	// Transition records what happened to the NAME, which is a different dimension
	// from Status (what the registry concluded about the artifact). A definition can
	// be FALSIFIED and still become the value bound to a name; a request can be
	// REJECTED and belong in the journal without moving anything. Inferring one from
	// the other is inherently fragile — a new status reopens the ABA hole the moment
	// someone forgets to add it to an allowlist. Set at the Repoint site itself, so
	// it cannot be forgotten by a future code path.
	//
	// "applied" = the name now points at Hash. Empty on historical entries written
	// before this field existed, which is why repointedName still falls back to a
	// status allowlist for them.
	Transition string `json:"transition,omitempty"`
	Chain      string `json:"chain,omitempty"` // tamper-evidence: SHA-256(prev chain + this entry sans chain)
}

// signedContent is the deterministic byte string a signer signs: the entry with
// the store-assigned fields (seq, time, verifier, chain) and the signature
// itself zeroed. So the signature covers exactly the AUTHORED fields — pubkey,
// author label, name, kind, status, object hash, prior hash, verdicts, context —
// independent of where the entry lands in the log. The chain seals ordering on
// top; the signature seals authorship. (docs/registry-auth.md)
// transitionApplied marks a journal entry whose name now points at its Hash.
const transitionApplied = "applied"

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
func (e *LogEntry) repointedName() bool {
	// The explicit record wins wherever it exists: it is written at the Repoint call
	// site, so it states what happened rather than inferring it.
	if e.Transition != "" {
		return e.Transition == transitionApplied
	}
	// Historical entries predate the field. The allowlist reproduces the behaviour
	// they were written under, and must not be "tidied": falsified entries in the
	// committed corpus DO carry prev, so dropping falsified here would silently
	// undercount every pre-existing name's revision.
	switch e.Status {
	case "accepted", "falsified":
		return true
	}
	return false
}

func signedContent(e *LogEntry) []byte {
	c := *e
	c.Seq = 0
	c.Time = ""
	c.Verifier = ""
	c.Chain = ""
	c.Sig = ""
	b, _ := json.Marshal(&c)
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
	body, _ := json.Marshal(e)
	e.Chain = chainHash(chainAnchor(prior), body)
	b, _ := json.Marshal(e)
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
		if e.Envelope != "" || e.AuthorSig != "" || e.AuthorPubkey != "" {
			if e.Envelope == "" || e.AuthorSig == "" || e.AuthorPubkey == "" {
				return fmt.Errorf("journal line %d has a partial author record (envelope=%v key=%v sig=%v): all three or none, since any one alone attests to nothing",
					line, e.Envelope != "", e.AuthorPubkey != "", e.AuthorSig != "")
			}
			env, perr := envelopeParse([]byte(e.Envelope))
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
			body, _ := json.Marshal(e)
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
