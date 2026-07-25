package main

import (
	"encoding/json"
	"time"
)

// The proof queue is the durable work list for the verification worker pool
// (#14). Proving is the one guarantee the put path never recomputes — Z3 is too
// heavy to run inside a submission — so a `require_proven` name defers its
// repoint, enqueues the object here, and a worker (`oath prove-worker`) drains
// the queue out of band, re-earns the proof, and only then binds the name.
//
// The key property that keeps this simple: proving hash H is DETERMINISTIC
// (seeds derive from the content hash; the solver is pinned) and its verdict is
// hash-keyed metadata. So the queue never needs perfect mutual exclusion — a
// double-claimed job just re-proves to the identical verdict, wasting CPU, never
// corrupting state. Leases exist only to avoid that waste, and a crashed
// worker's stale lease is safely reclaimable. Concurrency-hard exactly-once
// dispatch is a property of the hosted Postgres/Cloud-Tasks queue
// (docs/registry-verification.md), not a correctness requirement here.
//
// On the filesystem store a job is `<store>/proofq/<hash>.job`; claiming renames
// it to `<hash>.lease` (atomic single-winner); completing removes it.

type ProofJob struct {
	Hash      string `json:"hash"` // object to prove
	Name      string `json:"name"` // name whose repoint waits on this proof (if Gate)
	Submitter string `json:"submitter,omitempty"`
	Enqueued  string `json:"enqueued"` // RFC3339 UTC
	Gate      bool   `json:"gate"`     // true: a deferred name-bind is gated on the outcome
}

// EnqueueProof records a proof job. Enqueuing the same hash twice overwrites the
// prior job harmlessly (same work). No-op-safe: a job for an object that is
// already fully proven simply proves to the same verdict when drained. The
// lease/dispatch mechanics live in the backend (fs: rename+mtime; Postgres: FOR
// UPDATE SKIP LOCKED) — see backend.go / docs/store-drivers.md.
func (s *Store) EnqueueProof(j ProofJob) error {
	if j.Enqueued == "" {
		j.Enqueued = time.Now().UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(&j)
	return s.be.enqueueProof(j.Hash, b)
}

// ClaimProof leases one job to a worker (a crashed worker's stale lease, older
// than leaseTTL, is reclaimable). Returns (nil, nil) when the queue is empty.
func (s *Store) ClaimProof(now time.Time, leaseTTL time.Duration) (*ProofJob, error) {
	b, ok, err := s.be.claimProof(now, leaseTTL)
	if err != nil || !ok {
		return nil, err
	}
	var j ProofJob
	if json.Unmarshal(b, &j) != nil || j.Hash == "" {
		return nil, nil
	}
	return &j, nil
}

// CompleteProof clears a finished job.
func (s *Store) CompleteProof(hash string) error { return s.be.completeProof(hash) }

// ProofQueueDepth reports pending + leased jobs, for status output.
func (s *Store) ProofQueueDepth() int { return s.be.proofDepth() }
