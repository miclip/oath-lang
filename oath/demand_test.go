package main

import (
	"strings"
	"testing"
	"time"
)

// #75: demand telemetry must record enough to be a coverage request and nothing
// that identifies who asked or what they were working on. The privacy guarantee
// is structural — the registry retains strictly less than it receives — so the
// test is on what reaches storage, not on intent.
func TestDemandRecordsShapeNotProse(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)

	// A property name is agent-authored text: an agent projecting a real task
	// writes what the task is about. It must not persist.
	recordMiss(st, "abc123", "(-> (List t0) (List t0))", time.Now())
	b, ok, err := st.be.getMeta(demandKey)
	if err != nil || !ok {
		t.Fatal("no demand record written")
	}
	stored := string(b)
	for _, prose := range []string{"verify-webhook", "northwind", "AURORA", "one-off"} {
		if strings.Contains(strings.ToLower(stored), strings.ToLower(prose)) {
			t.Fatalf("caller prose reached storage: %q in %s", prose, stored)
		}
	}
	if !strings.Contains(stored, "abc123") || !strings.Contains(stored, "(List t0)") {
		t.Fatalf("shape not recorded: %s", stored)
	}
	// No principal, and no timestamp finer than a day — either would allow
	// re-identifying a session against a request log.
	if strings.Contains(stored, ":") && strings.Count(stored, ":") > 12 {
		t.Fatalf("suspiciously time-like data in record: %s", stored)
	}
}

// Repeat misses aggregate rather than accumulating separate entries — a count is
// the signal, a list of occurrences would be a log of individual callers.
func TestDemandAggregates(t *testing.T) {
	st := newStore(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		recordMiss(st, "same-hash", "(-> t0 t0)", now)
	}
	d := loadDemand(st)
	if len(d) != 1 {
		t.Fatalf("want 1 aggregated record, got %d", len(d))
	}
	if d["same-hash"].Count != 5 {
		t.Fatalf("count = %d, want 5", d["same-hash"].Count)
	}
}

// An empty hash is not a signal. A query that failed to elaborate never asked
// the registry anything, and recording it would inflate demand with syntax
// errors.
func TestDemandIgnoresEmptyHash(t *testing.T) {
	st := newStore(t)
	recordMiss(st, "", "(-> t0 t0)", time.Now())
	if len(loadDemand(st)) != 0 {
		t.Fatal("empty hash recorded as a demand signal")
	}
}
