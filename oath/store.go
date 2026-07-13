package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
	return &Store{Root: root, defs: map[string]*Def{}, metas: map[string]*Meta{}}, nil
}

func (s *Store) namesPath() string { return filepath.Join(s.Root, "names.json") }

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
	return os.WriteFile(s.namesPath(), b, 0o644)
}

// Put stores a definition and points its name at the new hash.
// Returns the hash and the previous hash the name pointed at ("" if new).
func (s *Store) Put(d *Def, m *Meta) (string, string, error) {
	h := hashDef(d)
	db, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(s.Root, "objects", h+".json"), db, 0o644); err != nil {
		return "", "", err
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(s.Root, "meta", h+".json"), mb, 0o644); err != nil {
		return "", "", err
	}
	names := s.Names()
	prev := names[m.Name]
	names[m.Name] = h
	if err := s.writeNames(names); err != nil {
		return "", "", err
	}
	s.defs[h] = d
	mm := *m
	s.metas[h] = &mm
	if prev == h {
		prev = ""
	}
	return h, prev, nil
}

func (s *Store) GetDef(h string) (*Def, error) {
	if d, ok := s.defs[h]; ok {
		return d, nil
	}
	b, err := os.ReadFile(filepath.Join(s.Root, "objects", h+".json"))
	if err != nil {
		return nil, fmt.Errorf("no definition with hash %s", shortHash(h))
	}
	var d Def
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	s.defs[h] = &d
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
	if err := os.WriteFile(filepath.Join(s.Root, "meta", h+".json"), mb, 0o644); err != nil {
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
		for i, cn := range m.CtorNames {
			if cn == name {
				return h, i, true
			}
		}
	}
	return "", 0, false
}

// AllHashes lists every object in the store, sorted.
func (s *Store) AllHashes() []string {
	entries, err := os.ReadDir(filepath.Join(s.Root, "objects"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
