package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// backend is the byte-level storage seam (#14). Everything Store does to
// persistent storage reduces to this surface; the higher-level logic — hashing,
// JSON, the journal chain, the metadata merge, the proof-queue job shape — stays
// in Store, so swapping backends is a driver change, not a kernel change. See
// docs/store-drivers.md.
//
// Content-addressed objects are immutable: putObject with the same hash writes
// identical bytes, so it needs no coordination. The contended state is the name
// index and the journal, whose read-modify-write must be serialized — hence
// lock(). A backend that is itself transactional (Postgres) may return a no-op
// lock and rely on its own transactions instead.
type backend interface {
	getObject(hash string) ([]byte, bool, error)
	putObject(hash string, b []byte) error
	listObjects() ([]string, error)

	getMeta(hash string) ([]byte, bool, error)
	putMeta(hash string, b []byte) error

	readNames() ([]byte, bool, error) // (bytes, present, err); absent index is not an error
	writeNames(b []byte) error

	readJournal() ([]byte, error) // exact bytes; VerifyLog anchors byte offsets on these
	appendJournal(line []byte) error

	enqueueProof(hash string, b []byte) error
	claimProof(now time.Time, ttl time.Duration) ([]byte, bool, error)
	completeProof(hash string) error
	proofDepth() int

	// lock serializes the mutable read-modify-write sections. The returned
	// release is always safe to call.
	lock() (func(), error)
}

// ---------------------------------------------------------------------------
// fsBackend — the reference filesystem store (SPEC §8.1). Behaviour-preserving
// extraction of the original Store I/O.
// ---------------------------------------------------------------------------

type fsBackend struct{ root string }

func openFSBackend(root string) (*fsBackend, error) {
	for _, d := range []string{root, filepath.Join(root, "objects"), filepath.Join(root, "meta")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return &fsBackend{root: root}, nil
}

func (f *fsBackend) objPath(h string) string  { return filepath.Join(f.root, "objects", h+".bin") }
func (f *fsBackend) metaPath(h string) string { return filepath.Join(f.root, "meta", h+".json") }
func (f *fsBackend) namesPath() string        { return filepath.Join(f.root, "names.json") }
func (f *fsBackend) logPath() string          { return filepath.Join(f.root, "log.jsonl") }
func (f *fsBackend) proofqDir() string        { return filepath.Join(f.root, "proofq") }

func readOptional(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func (f *fsBackend) getObject(h string) ([]byte, bool, error) { return readOptional(f.objPath(h)) }
func (f *fsBackend) putObject(h string, b []byte) error {
	return writeFileAtomic(f.objPath(h), b, 0o644)
}
func (f *fsBackend) getMeta(h string) ([]byte, bool, error) { return readOptional(f.metaPath(h)) }
func (f *fsBackend) putMeta(h string, b []byte) error {
	return writeFileAtomic(f.metaPath(h), b, 0o644)
}
func (f *fsBackend) readNames() ([]byte, bool, error) { return readOptional(f.namesPath()) }
func (f *fsBackend) writeNames(b []byte) error        { return writeFileAtomic(f.namesPath(), b, 0o644) }

func (f *fsBackend) listObjects() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(f.root, "objects"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".bin") {
			out = append(out, strings.TrimSuffix(n, ".bin"))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *fsBackend) readJournal() ([]byte, error) {
	b, err := os.ReadFile(f.logPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

func (f *fsBackend) appendJournal(line []byte) error {
	fh, err := os.OpenFile(f.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	_, err = fh.Write(line)
	return err
}

func (f *fsBackend) enqueueProof(hash string, b []byte) error {
	if err := os.MkdirAll(f.proofqDir(), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(f.proofqDir(), hash+".job"), b, 0o644)
}

func (f *fsBackend) claimProof(now time.Time, ttl time.Duration) ([]byte, bool, error) {
	ents, err := os.ReadDir(f.proofqDir())
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var fresh bool
		switch {
		case strings.HasSuffix(name, ".job"):
			fresh = true
		case strings.HasSuffix(name, ".lease"):
			info, statErr := os.Stat(filepath.Join(f.proofqDir(), name))
			if statErr != nil || now.Sub(info.ModTime()) < ttl {
				continue
			}
		default:
			continue
		}
		src := filepath.Join(f.proofqDir(), name)
		b, rerr := os.ReadFile(src)
		if rerr != nil {
			continue
		}
		hash := strings.TrimSuffix(strings.TrimSuffix(name, ".job"), ".lease")
		lease := filepath.Join(f.proofqDir(), hash+".lease")
		if fresh {
			if os.Rename(src, lease) != nil {
				continue
			}
		}
		_ = os.Chtimes(lease, now, now)
		return b, true, nil
	}
	return nil, false, nil
}

func (f *fsBackend) completeProof(hash string) error {
	_ = os.Remove(filepath.Join(f.proofqDir(), hash+".lease"))
	_ = os.Remove(filepath.Join(f.proofqDir(), hash+".job"))
	return nil
}

func (f *fsBackend) proofDepth() int {
	ents, err := os.ReadDir(f.proofqDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".job") || strings.HasSuffix(e.Name(), ".lease") {
			n++
		}
	}
	return n
}

// lock is the cross-process file mutex (see lock.go for the rationale). Opt-in
// via OATH_STORE_LOCK so local single-process use pays nothing.
func (f *fsBackend) lock() (func(), error) { return fsFileLock(f.root) }

// ---------------------------------------------------------------------------
// memBackend — an in-memory backend, so the whole store test suite can run
// backend-agnostically and prove the seam is faithful (docs/store-drivers.md).
// ---------------------------------------------------------------------------

type memBackend struct {
	mu      sync.Mutex
	objects map[string][]byte
	meta    map[string][]byte
	names   []byte
	journal []byte
	queue   map[string][]byte // hash -> job
	leases  map[string]time.Time
}

func newMemBackend() *memBackend {
	return &memBackend{
		objects: map[string][]byte{}, meta: map[string][]byte{},
		queue: map[string][]byte{}, leases: map[string]time.Time{},
	}
}

func cp(b []byte) []byte { return append([]byte(nil), b...) }

func (m *memBackend) getObject(h string) ([]byte, bool, error) {
	b, ok := m.objects[h]
	return cp(b), ok, nil
}
func (m *memBackend) putObject(h string, b []byte) error { m.objects[h] = cp(b); return nil }
func (m *memBackend) getMeta(h string) ([]byte, bool, error) {
	b, ok := m.meta[h]
	return cp(b), ok, nil
}
func (m *memBackend) putMeta(h string, b []byte) error { m.meta[h] = cp(b); return nil }
func (m *memBackend) readNames() ([]byte, bool, error) { return cp(m.names), m.names != nil, nil }
func (m *memBackend) writeNames(b []byte) error        { m.names = cp(b); return nil }
func (m *memBackend) readJournal() ([]byte, error)     { return cp(m.journal), nil }
func (m *memBackend) appendJournal(line []byte) error {
	m.journal = append(m.journal, line...)
	return nil
}

func (m *memBackend) listObjects() ([]string, error) {
	out := make([]string, 0, len(m.objects))
	for h := range m.objects {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

func (m *memBackend) enqueueProof(hash string, b []byte) error { m.queue[hash] = cp(b); return nil }

func (m *memBackend) claimProof(now time.Time, ttl time.Duration) ([]byte, bool, error) {
	hashes := make([]string, 0, len(m.queue))
	for h := range m.queue {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	for _, h := range hashes {
		if lease, held := m.leases[h]; held && now.Sub(lease) < ttl {
			continue
		}
		m.leases[h] = now
		return cp(m.queue[h]), true, nil
	}
	return nil, false, nil
}

func (m *memBackend) completeProof(hash string) error {
	delete(m.queue, hash)
	delete(m.leases, hash)
	return nil
}

func (m *memBackend) proofDepth() int { return len(m.queue) }

func (m *memBackend) lock() (func(), error) { m.mu.Lock(); return m.mu.Unlock, nil }

var _ backend = (*fsBackend)(nil)
var _ backend = (*memBackend)(nil)

// migrateBackend copies an entire store from one backend to another: the
// immutable objects and their metadata, the name index, and the journal —
// byte-for-byte and in order, so the migrated journal's hash chain still
// verifies (docs/store-drivers.md). Content addressing makes it idempotent and
// safe to re-run. The proof queue is intentionally NOT migrated: jobs are
// transient work items the destination's `prove-worker --scan` regenerates.
// Returns the object count copied.
func migrateBackend(src, dst backend) (int, error) {
	hashes, err := src.listObjects()
	if err != nil {
		return 0, err
	}
	for _, h := range hashes {
		ob, ok, err := src.getObject(h)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		if err := dst.putObject(h, ob); err != nil {
			return 0, err
		}
		if mb, ok, err := src.getMeta(h); err == nil && ok {
			if err := dst.putMeta(h, mb); err != nil {
				return 0, err
			}
		}
	}
	if nb, ok, err := src.readNames(); err == nil && ok {
		if err := dst.writeNames(nb); err != nil {
			return 0, err
		}
	}
	jb, err := src.readJournal()
	if err != nil {
		return 0, err
	}
	for _, line := range bytes.SplitAfter(jb, []byte("\n")) {
		if len(line) == 0 {
			continue // trailing split artifact
		}
		if err := dst.appendJournal(line); err != nil {
			return 0, err
		}
	}
	return len(hashes), nil
}

// errCorruptNames is returned by OpenStore when the name index exists but does
// not parse — losing every name silently is worse than refusing to open.
func validateNames(b []byte, present bool, where string) error {
	if !present {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return fmt.Errorf("corrupt name index %s: not valid JSON (restore it from version control; it is not derivable from objects/)", where)
	}
	return nil
}
