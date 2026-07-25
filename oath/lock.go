package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cross-process store mutex for the mutable index/journal (#14). The v1 hosted
// store is the filesystem store over a gcsfuse-mounted bucket, written by BOTH
// the serve instance (on put) and the proof worker (on prove/bind). gcsfuse
// rename is not atomic, so two writers racing the single mutable files —
// names.json (Repoint) and log.jsonl (AppendLog) — can lose an update or, worse,
// break the journal's hash chain. This serializes those short read-modify-write
// critical sections behind an exclusive lock file.
//
// The lock is created with O_CREATE|O_EXCL, which gcsfuse backs with a
// generation precondition (ifGenerationMatch=0) — an atomic single-winner
// create. A crashed holder's lock is broken by mtime staleness (the critical
// sections are milliseconds; the TTL is generous). It is OPT-IN via
// OATH_STORE_LOCK so single-process local use (and the CI corpus runs) pay
// nothing; the Cloud Run serve + worker set it.
//
// This narrows — does not eliminate — the shared-writer window. The complete fix
// is the DB-backed store, whose single transactional writer removes the shared
// mutable files entirely (docs/registry-verification.md).

const (
	storeLockTTL     = 30 * time.Second
	storeLockTimeout = 30 * time.Second
	storeLockPoll    = 40 * time.Millisecond
)

func storeLockEnabled() bool { return os.Getenv("OATH_STORE_LOCK") != "" }

// lockMutable acquires the store's write lock and returns a release func. When
// locking is disabled it is a no-op. The returned release is always safe to
// call (idempotent enough for a defer).
func (s *Store) lockMutable() (func(), error) {
	if !storeLockEnabled() {
		return func() {}, nil
	}
	path := filepath.Join(s.Root, ".lock")
	deadline := time.Now().Add(storeLockTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("store lock: %w", err)
		}
		// Held by someone else: break it if the holder is stale (crashed).
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > storeLockTTL {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("store lock: timed out after %s waiting for %s", storeLockTimeout, path)
		}
		time.Sleep(storeLockPoll)
	}
}
