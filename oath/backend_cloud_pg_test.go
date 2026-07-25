//go:build cloud

package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// These run only under `-tags cloud` AND when OATH_TEST_PG_DSN points at a
// Postgres (a CI service container). Without a DSN they skip — the default and
// cloud builds still compile them, so the code can't rot. This is the harness
// the store-driver design (docs/store-drivers.md) calls for before the Postgres
// backend goes anywhere near a live journal.

func pgForTest(t *testing.T) *pgIndex {
	t.Helper()
	dsn := os.Getenv("OATH_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set OATH_TEST_PG_DSN to run the Postgres integration tests")
	}
	pg, err := openPGIndex("postgres", dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	if _, err := pg.db.Exec(`truncate names, journal, proofq restart identity`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pg
}

// pgComposite is the real cloud shape for tests: mutable state (names, journal,
// proof queue, lock) in Postgres, objects in memory — so the whole Store suite
// exercises the pg index.
type pgComposite struct {
	*memBackend
	idx *pgIndex
}

func (c *pgComposite) readNames() ([]byte, bool, error) { return c.idx.readNames() }
func (c *pgComposite) writeNames(b []byte) error        { return c.idx.writeNames(b) }
func (c *pgComposite) readJournal() ([]byte, error)     { return c.idx.readJournal() }
func (c *pgComposite) appendJournal(b []byte) error     { return c.idx.appendJournal(b) }
func (c *pgComposite) enqueueProof(h string, b []byte) error { return c.idx.enqueueProof(h, b) }
func (c *pgComposite) claimProof(now time.Time, ttl time.Duration) ([]byte, bool, error) {
	return c.idx.claimProof(now, ttl)
}
func (c *pgComposite) completeProof(h string) error { return c.idx.completeProof(h) }
func (c *pgComposite) proofDepth() int              { return c.idx.proofDepth() }
func (c *pgComposite) lock() (func(), error)        { return c.idx.lock() }

// The pg index's four operations round-trip: the whole names blob, byte-exact
// journal lines (VerifyLog anchors on these), and the proof-queue lease.
func TestPGIndexRoundTrips(t *testing.T) {
	pg := pgForTest(t)

	if err := pg.writeNames([]byte(`{"a":"h1","b":"h2"}`)); err != nil {
		t.Fatal(err)
	}
	b, ok, err := pg.readNames()
	if err != nil || !ok {
		t.Fatalf("readNames: ok=%v err=%v", ok, err)
	}
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	if m["a"] != "h1" || m["b"] != "h2" {
		t.Fatalf("names round-trip: %v", m)
	}

	_ = pg.appendJournal([]byte("line1\n"))
	_ = pg.appendJournal([]byte("line2\n"))
	if j, _ := pg.readJournal(); string(j) != "line1\nline2\n" {
		t.Fatalf("journal bytes not preserved: %q", j)
	}

	_ = pg.enqueueProof("hh", []byte(`{"hash":"hh"}`))
	got, ok, _ := pg.claimProof(time.Now(), time.Minute)
	if !ok || string(got) != `{"hash":"hh"}` {
		t.Fatalf("claim: %q ok=%v", got, ok)
	}
	if _, ok2, _ := pg.claimProof(time.Now(), time.Minute); ok2 {
		t.Fatal("re-claimed a live-leased job")
	}
	_ = pg.completeProof("hh")
	if pg.proofDepth() != 0 {
		t.Fatalf("proofDepth after complete = %d, want 0", pg.proofDepth())
	}
}

// A full Store over the Postgres index: puts land, the name binds, and the
// hash-chained journal verifies — the audit trail works over Postgres, not just
// the filesystem.
func TestPGStoreJournalChainAndRepoint(t *testing.T) {
	pg := pgForTest(t)
	st, err := newStoreWithBackend(&pgComposite{memBackend: newMemBackend(), idx: pg}, "pg-test")
	if err != nil {
		t.Fatal(err)
	}
	put(t, st, `(defn one [] [] Int 1)`)
	put(t, st, `(defn two [] [] Int 2)`)
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal over Postgres failed verification: %v", err)
	}
	if h, ok := st.Resolve("one"); !ok || h == "" {
		t.Fatal("name not bound in the Postgres index")
	}
	if got := len(st.ReadLog()); got < 2 {
		t.Fatalf("journal has %d entries, want >= 2", got)
	}
}
