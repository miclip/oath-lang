package main

import (
	"fmt"
	"time"
)

// The verification worker pool (#14). Proving is the one guarantee the put path
// never recomputes — Z3 is too heavy to run inside a submission — so it happens
// here, out of band, draining the proof queue (proofq.go). Two roles:
//
//   - UPGRADE (Gate=false): re-prove `tested` definitions and upgrade the index
//     to `proven`. This is what turns "the registry's proven badge" into a claim
//     the registry EARNED by re-proving, not one a publisher asserted.
//   - GATE (Gate=true): a require_proven name whose repoint was deferred at put
//     time. When the proof lands, the worker binds the name; if the object can
//     never be fully proven, the name stays put and the attempt is journaled.
//
// When the worker holds a signing key (--key), its journal entries are signed:
// the `proven` verdict then carries an authenticated "the registry re-proved
// this", distinct from a publisher's unverified claim (docs/registry-auth.md).
//
// Every verdict is deterministic and idempotent (seeds derive from the content
// hash, the solver is pinned, the proof set is hash-keyed metadata), so a
// re-run — or a double-claimed job — converges to the identical result.

type proveWorkerOpts struct {
	scan     bool          // seed the queue from every tested-but-unproven func first
	once     bool          // drain the queue once and exit (default loops)
	interval time.Duration // idle poll interval when looping
	leaseTTL time.Duration // a lease older than this is a crashed worker's, reclaimable
	author   string        // journal author for proof events (defaults to the signer's pubkey)
}

func cmdProveWorker(st *Store, o proveWorkerOpts) {
	if err := z3Available(); err != nil {
		fail(fmt.Errorf("prove-worker needs z3 on PATH: %w", err))
	}
	if o.leaseTTL == 0 {
		o.leaseTTL = 10 * time.Minute
	}
	if o.interval == 0 {
		o.interval = 5 * time.Second
	}
	if o.scan {
		n := seedProofQueue(st)
		fmt.Printf("seeded %d tested-but-unproven definition(s) into the proof queue\n", n)
	}
	for {
		worked := 0
		for {
			job, err := st.ClaimProof(time.Now(), o.leaseTTL)
			if err != nil {
				fail(err)
			}
			if job == nil {
				break
			}
			processProofJob(st, job, o.author)
			worked++
		}
		if o.once {
			fmt.Printf("prove-worker: drained (%d job(s) this pass); queue depth %d\n", worked, st.ProofQueueDepth())
			return
		}
		if worked == 0 {
			time.Sleep(o.interval)
		}
	}
}

// seedProofQueue enqueues every function that is verified (tested) but not yet
// fully proven and not falsified — the background upgrade work list.
func seedProofQueue(st *Store) int {
	n := 0
	for _, h := range st.AllHashes() {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil || isFullyProven(m, d) || m.Guarantee.Level == "falsified" {
			continue
		}
		if st.EnqueueProof(ProofJob{Hash: h, Name: m.Name, Gate: false}) == nil {
			n++
		}
	}
	return n
}

// processProofJob proves one queued object and resolves its consequence: for an
// upgrade job, that is simply the recorded verdict; for a gate job, it binds the
// deferred name iff the object is now fully proven and still clears policy.
func processProofJob(st *Store, job *ProofJob, author string) {
	defer st.CompleteProof(job.Hash)

	d, err := st.GetDef(job.Hash)
	if err != nil {
		fmt.Printf("⚠ %-16s proof skipped: %v\n", job.Name, err)
		return
	}
	if _, err := apiProveHash(st, job.Hash, job.Name); err != nil {
		fmt.Printf("⚠ %-16s proof errored: %v\n", job.Name, err)
		return
	}
	m, _ := st.GetMeta(job.Hash)
	proven := isFullyProven(m, d)

	// Record the proof event. Signed when the worker holds a key, so the verdict
	// is an authenticated "the verifier re-proved this".
	who := author
	if who == "" {
		who = "prove-worker"
	}
	_ = st.AppendLog(&LogEntry{
		Author: who, Name: job.Name, Kind: "prove", Status: "accepted",
		Hash: job.Hash, Guarantee: guaranteeString(m.Guarantee),
	})
	fmt.Printf("%s %-16s #%s  %s\n", proofMark(proven), job.Name, shortHash(job.Hash), guaranteeString(m.Guarantee))

	if !job.Gate {
		return // upgrade job: the recorded verdict is the whole point
	}

	// Gate job: decide the deferred name-bind against the CURRENT world.
	if cur, ok := st.Resolve(job.Name); ok && cur == job.Hash {
		return // already bound (idempotent re-run)
	}
	if !proven {
		reason := "proof did not complete: not all properties proven"
		fmt.Printf("⛔ %-16s name not bound — %s (re-queue to retry a transient timeout)\n", job.Name, reason)
		_ = st.AppendLog(&LogEntry{
			Author: who, Name: job.Name, Kind: "put", Status: "blocked",
			Hash: job.Hash, Error: reason, Guarantee: guaranteeString(m.Guarantee),
		})
		return
	}
	// Re-run the synchronous policy against the now-proven object; the world may
	// have moved since the put deferred.
	pol, err := LoadPolicy(st.Root)
	if err != nil {
		fail(err)
	}
	specAuthor, bodyAuthor := attributeAuthorship(st, job.Name, d, job.Submitter)
	if ok, reason := evalPolicy(st, pol, job.Name, job.Hash, d, specAuthor, bodyAuthor); !ok {
		fmt.Printf("⛔ %-16s proven, but name not bound: %s\n", job.Name, reason)
		_ = st.AppendLog(&LogEntry{
			Author: who, Name: job.Name, Kind: "put", Status: "blocked",
			Hash: job.Hash, Error: reason, Guarantee: guaranteeString(m.Guarantee),
		})
		return
	}
	prev, err := st.Repoint(job.Name, job.Hash)
	if err != nil {
		fail(err)
	}
	if mm, err := st.GetMeta(job.Hash); err == nil {
		mm.SpecAuthor, mm.BodyAuthor = specAuthor, bodyAuthor
		_ = st.SetMeta(job.Hash, mm)
	}
	_ = st.AppendLog(&LogEntry{
		Author: job.Submitter, Name: job.Name, Kind: "put", Status: "accepted",
		Hash: job.Hash, Prev: prev, Guarantee: guaranteeString(m.Guarantee),
		Termination: m.Termination,
	})
	fmt.Printf("✓ %-16s name bound after proof (was %s)\n", job.Name, orWord(shortHash(prev), "unbound"))
}

func proofMark(proven bool) string {
	if proven {
		return "✓"
	}
	return "·"
}
