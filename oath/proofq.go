package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func (s *Store) proofqDir() string { return filepath.Join(s.Root, "proofq") }

// EnqueueProof records a proof job. Enqueuing the same hash twice overwrites the
// prior job harmlessly (same work). No-op-safe: a job for an object that is
// already fully proven simply proves to the same verdict when drained.
func (s *Store) EnqueueProof(j ProofJob) error {
	if err := os.MkdirAll(s.proofqDir(), 0o755); err != nil {
		return err
	}
	if j.Enqueued == "" {
		j.Enqueued = time.Now().UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(&j)
	return writeFileAtomic(filepath.Join(s.proofqDir(), j.Hash+".job"), b, 0o644)
}

// ClaimProof leases one job to a worker: a fresh `.job`, or a `.lease` whose
// mtime is older than leaseTTL (a crashed worker's abandoned job). Returns
// (nil, nil) when the queue is empty. Claiming renames `.job` → `.lease` — an
// atomic single-winner operation — and stamps the lease mtime to `now`.
func (s *Store) ClaimProof(now time.Time, leaseTTL time.Duration) (*ProofJob, error) {
	ents, err := os.ReadDir(s.proofqDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Deterministic scan order so a stuck job doesn't starve behind directory
	// iteration order, and so tests are reproducible.
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
			info, err := os.Stat(filepath.Join(s.proofqDir(), name))
			if err != nil || now.Sub(info.ModTime()) < leaseTTL {
				continue // still held by a live worker
			}
		default:
			continue
		}
		srcPath := filepath.Join(s.proofqDir(), name)
		b, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		var j ProofJob
		if json.Unmarshal(b, &j) != nil || j.Hash == "" {
			continue
		}
		leasePath := filepath.Join(s.proofqDir(), j.Hash+".lease")
		if fresh {
			// Atomic hand-off: whoever wins the rename owns the job.
			if err := os.Rename(srcPath, leasePath); err != nil {
				continue // lost the race (another worker took it) — try the next
			}
		}
		// Stamp the lease start so a slow-but-alive worker keeps ownership and a
		// crashed one's lease ages out.
		_ = os.Chtimes(leasePath, now, now)
		return &j, nil
	}
	return nil, nil
}

// CompleteProof clears a finished job (both the lease and any lingering .job).
func (s *Store) CompleteProof(hash string) error {
	_ = os.Remove(filepath.Join(s.proofqDir(), hash+".lease"))
	_ = os.Remove(filepath.Join(s.proofqDir(), hash+".job"))
	return nil
}

// ProofQueueDepth reports pending + leased jobs, for status output.
func (s *Store) ProofQueueDepth() int {
	ents, err := os.ReadDir(s.proofqDir())
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
