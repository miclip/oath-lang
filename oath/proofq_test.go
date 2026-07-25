package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePolicy(t *testing.T, st *Store, json string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(st.Root, "policy.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A provable, non-recursive Int function that is `tested` at put time (the put
// path never proves) — the canonical require_proven candidate.
const dblSrc = `(defn dbl [] [(x Int)] Int (+ x x)
	(prop is-double [(x Int)] (== (dbl x) (+ x x))))`

// The queue leases exactly one worker at a time and reclaims a crashed worker's
// stale lease; completion clears it. Correctness never depends on exclusion
// (proving is idempotent), but the lease avoids wasted re-proving.
func TestProofQueueLifecycle(t *testing.T) {
	st := newStore(t)
	if err := st.EnqueueProof(ProofJob{Hash: "abc123", Name: "foo", Gate: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000_000, 0)
	j, err := st.ClaimProof(now, 10*time.Minute)
	if err != nil || j == nil || j.Hash != "abc123" {
		t.Fatalf("first claim: job=%v err=%v", j, err)
	}
	// The job is now leased; a fresh claim within the TTL finds nothing.
	if j2, _ := st.ClaimProof(now.Add(time.Minute), 10*time.Minute); j2 != nil {
		t.Fatalf("claimed a live-leased job: %v", j2)
	}
	// After the TTL the lease is a crashed worker's; it is reclaimable.
	if j3, _ := st.ClaimProof(now.Add(20*time.Minute), 10*time.Minute); j3 == nil || j3.Hash != "abc123" {
		t.Fatalf("stale lease not reclaimed: %v", j3)
	}
	if err := st.CompleteProof("abc123"); err != nil {
		t.Fatal(err)
	}
	if d := st.ProofQueueDepth(); d != 0 {
		t.Fatalf("queue depth after complete = %d, want 0", d)
	}
}

// Under require_proven, a tested-but-unproven def does NOT bind its name at put
// time: it is stored, queued (gated), and journaled `pending`. The name stays
// unbound until a worker proves it.
func TestRequireProvenDefersBind(t *testing.T) {
	st := newStore(t)
	writePolicy(t, st, `{"rules":[{"names":["*"],"require_proven":true}]}`)
	reps := put(t, st, dblSrc)
	last := reps[len(reps)-1]
	if last.Status != "pending" {
		t.Fatalf("status=%q, want pending", last.Status)
	}
	if _, ok := st.Resolve("dbl"); ok {
		t.Fatal("name bound despite pending proof")
	}
	if st.ProofQueueDepth() != 1 {
		t.Fatalf("queue depth = %d, want 1 (gated job enqueued)", st.ProofQueueDepth())
	}
	var pending bool
	for _, e := range st.ReadLog() {
		if e.Name == "dbl" && e.Status == "pending" {
			pending = true
		}
	}
	if !pending {
		t.Fatal("no pending journal entry recorded")
	}
}

// A falsified def can never be proven, so require_proven blocks it outright
// rather than queuing a proof that can never land.
func TestRequireProvenBlocksFalsified(t *testing.T) {
	st := newStore(t)
	writePolicy(t, st, `{"rules":[{"names":["*"],"require_proven":true}]}`)
	reps := put(t, st, `(defn bad [] [(x Int)] Int x
		(prop impossible [(x Int)] (< (bad x) (bad x))))`)
	last := reps[len(reps)-1]
	if last.Status != "blocked" {
		t.Fatalf("status=%q, want blocked", last.Status)
	}
	if st.ProofQueueDepth() != 0 {
		t.Fatalf("queue depth = %d, want 0 (falsified def must not be queued)", st.ProofQueueDepth())
	}
}

// The gate end to end: put defers, the worker proves and binds the name, which
// then resolves to the proven object.
func TestProveWorkerBindsAfterProof(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	writePolicy(t, st, `{"rules":[{"names":["*"],"require_proven":true}]}`)
	reps := put(t, st, dblSrc)
	h := reps[len(reps)-1].Hash
	if _, ok := st.Resolve("dbl"); ok {
		t.Fatal("name bound before proof")
	}

	cmdProveWorker(st, proveWorkerOpts{once: true})

	got, ok := st.Resolve("dbl")
	if !ok || got != h {
		t.Fatalf("name not bound to proven object after worker: got=%q ok=%v want=%q", got, ok, h)
	}
	m, _ := st.GetMeta(h)
	if m.Guarantee.Level != "proven" {
		t.Fatalf("guarantee=%q, want proven", m.Guarantee.Level)
	}
	if st.ProofQueueDepth() != 0 {
		t.Fatalf("queue not drained: depth %d", st.ProofQueueDepth())
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal integrity after worker: %v", err)
	}
}

// scanBulkProve upgrades a provable def to proven and records the proof-state
// fingerprint, so a second scan with nothing changed is a no-op (the fixpoint
// gate — no re-burning the budget on already-settled defs).
func TestScanBulkProveFixpointGate(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, dblSrc)

	scanBulkProve(st, "test")
	h, _ := st.Resolve("dbl")
	if m, _ := st.GetMeta(h); m.Guarantee.Level != "proven" {
		t.Fatalf("dbl not proven after scan: %q", m.Guarantee.Level)
	}
	prev, ok, _ := st.be.getMeta(proofFixpointKey)
	if !ok {
		t.Fatal("no fixpoint marker recorded")
	}
	if string(prev) != proofStateFingerprint(st) {
		t.Fatal("fixpoint marker doesn't match current proof state — a re-scan would re-burn")
	}
}

// The upgrade role: a def that already holds its name at `tested` is re-proven
// by --scan and upgraded to `proven` in place — no name change.
func TestProveWorkerScanUpgradesTested(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	reps := put(t, st, dblSrc) // no policy: binds immediately at tested
	h := reps[len(reps)-1].Hash
	if got, ok := st.Resolve("dbl"); !ok || got != h {
		t.Fatalf("dbl not bound at put: got=%q ok=%v", got, ok)
	}
	if m, _ := st.GetMeta(h); m.Guarantee.Level != "tested" {
		t.Fatalf("pre-scan guarantee=%q, want tested", m.Guarantee.Level)
	}

	cmdProveWorker(st, proveWorkerOpts{scan: true, once: true})

	if m, _ := st.GetMeta(h); m.Guarantee.Level != "proven" {
		t.Fatalf("post-scan guarantee=%q, want proven", m.Guarantee.Level)
	}
	if got, _ := st.Resolve("dbl"); got != h {
		t.Fatal("upgrade changed the name binding")
	}
}
