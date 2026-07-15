package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const kernelVersion = "oath-kernel/0.7"

// Store is the codebase: a content-addressed object database plus a mutable
// name index. Objects are immutable — a "change" to a definition is a new
// object and a repointed name; nothing is ever edited in place, so dependents
// referencing the old hash can never break.
type Store struct {
	Root  string
	defs  map[string]*Def
	metas map[string]*Meta
}

func OpenStore(root string) (*Store, error) {
	for _, d := range []string{root, filepath.Join(root, "objects"), filepath.Join(root, "meta")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{Root: root, defs: map[string]*Def{}, metas: map[string]*Meta{}}
	// Fail loudly on a corrupt name index at open time. names.json is not
	// reconstructible from objects/ (objects carry no names by design), so
	// treating unreadable bytes as an empty index would silently vanish every
	// name — worse than refusing to start.
	if b, err := os.ReadFile(s.namesPath()); err == nil {
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("corrupt name index %s: %w (restore it from version control; it is not derivable from objects/)", s.namesPath(), err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) namesPath() string { return filepath.Join(s.Root, "names.json") }

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
	if b, err := os.ReadFile(s.namesPath()); err == nil {
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
	return writeFileAtomic(s.namesPath(), b, 0o644)
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
		m.MutantsKilled, m.MutantsTotal = prev.MutantsKilled, prev.MutantsTotal
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
	if err := writeFileAtomic(filepath.Join(s.Root, "objects", h+".bin"), encodeDef(d), 0o644); err != nil {
		return "", err
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := writeFileAtomic(filepath.Join(s.Root, "meta", h+".json"), mb, 0o644); err != nil {
		return "", err
	}
	s.defs[h] = d
	mm := *m
	s.metas[h] = &mm
	return h, nil
}

// Repoint points name at h. Returns the previous hash ("" if the name is
// new or already pointed at h).
func (s *Store) Repoint(name, h string) (string, error) {
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
func (s *Store) CacheDef(h string, d *Def) { s.defs[h] = d }

func (s *Store) GetDef(h string) (*Def, error) {
	if d, ok := s.defs[h]; ok {
		return d, nil
	}
	b, err := os.ReadFile(filepath.Join(s.Root, "objects", h+".bin"))
	if err != nil {
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
	// recurse on h; a valid def stays cached, an invalid one is evicted.
	s.defs[h] = &d
	if err := checkDef(s, &d); err != nil {
		delete(s.defs, h)
		return nil, fmt.Errorf("stored object %s is not well-formed: %w", shortHash(h), err)
	}
	return &d, nil
}

func (s *Store) GetMeta(h string) (*Meta, error) {
	if m, ok := s.metas[h]; ok {
		return m, nil
	}
	b, err := os.ReadFile(filepath.Join(s.Root, "meta", h+".json"))
	if err != nil {
		return nil, fmt.Errorf("no metadata for hash %s", shortHash(h))
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	s.metas[h] = &m
	return &m, nil
}

// SetMeta rewrites a definition's metadata (names, guarantee). Metadata is
// mutable precisely because it is not part of the definition's identity.
func (s *Store) SetMeta(h string, m *Meta) error {
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := writeFileAtomic(filepath.Join(s.Root, "meta", h+".json"), mb, 0o644); err != nil {
		return err
	}
	mm := *m
	s.metas[h] = &mm
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
	Chain       string `json:"chain,omitempty"`   // tamper-evidence: SHA-256(prev chain + this entry sans chain)
}

func (s *Store) logPath() string { return filepath.Join(s.Root, "log.jsonl") }

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
	e.Verifier = kernelVersion
	e.Time = time.Now().UTC().Format(time.RFC3339)
	prior, _ := os.ReadFile(s.logPath()) // absent → empty prefix, anchor = sha256("")
	e.Seq = strings.Count(string(prior), "\n") + 1
	e.Chain = ""
	body, _ := json.Marshal(e)
	e.Chain = chainHash(chainAnchor(prior), body)
	b, _ := json.Marshal(e)
	f, err := os.OpenFile(s.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
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
	b, err := os.ReadFile(s.logPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
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
	b, err := os.ReadFile(s.logPath())
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
	entries, err := os.ReadDir(filepath.Join(s.Root, "objects"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".bin"); ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
