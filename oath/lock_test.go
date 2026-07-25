package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// With locking on, concurrent AppendLog calls must serialize: every entry lands
// with a distinct sequential seq and the hash chain verifies. Without the lock,
// interleaved read-prior→append would fork the chain.
func TestStoreLockSerializesConcurrentAppend(t *testing.T) {
	t.Setenv("OATH_STORE_LOCK", "1")
	st := newStore(t)
	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = st.AppendLog(&LogEntry{Author: "w", Name: "d", Status: "accepted"})
		}(i)
	}
	wg.Wait()

	if err := st.VerifyLog(); err != nil {
		t.Fatalf("chain broke under concurrent append: %v", err)
	}
	if got := len(st.ReadLog()); got != n {
		t.Fatalf("recorded %d entries, want %d (a lost update)", got, n)
	}
}

// A crashed holder's lock is broken once it ages past the TTL, so the store
// never wedges permanently.
func TestStoreLockStaleBreak(t *testing.T) {
	t.Setenv("OATH_STORE_LOCK", "1")
	st := newStore(t)

	release, err := st.be.lock()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: never release, and age the lock past the TTL.
	lockPath := filepath.Join(st.Root, ".lock")
	old := time.Now().Add(-2 * storeLockTTL)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	release2, err := st.be.lock() // must break the stale lock, not time out
	if err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
	release2()
	release()
}

// Disabled by default: with no env set, lockMutable is a no-op even if a stray
// .lock file exists, so local single-process use pays nothing.
func TestStoreLockDisabledIsNoOp(t *testing.T) {
	st := newStore(t)
	if err := os.WriteFile(filepath.Join(st.Root, ".lock"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := st.be.lock()
	if err != nil {
		t.Fatalf("disabled lock returned error: %v", err)
	}
	release()
}
