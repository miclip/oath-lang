package main

import (
	"crypto/sha256"
	"encoding/hex"
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
		scanBulkProve(st, o.author)
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

// proofFixpointKey is a reserved (non-hash) meta key holding the proof-state
// fingerprint at the last completed scan. It is never a definition hash, so it
// never collides with a real object.
const proofFixpointKey = "proof-scan-fixpoint"

// scanBulkProve upgrades tested definitions to proven — correctly and without
// wasted work (the background-upgrade role of #14).
//
// Two properties make it converge instead of grinding:
//
//   - DEPENDENCY ORDER. A proof draws its lemma library from the definition's
//     transitive dependencies' proven properties (§7.2). Proving in topological
//     order (dependencies first) means every provable definition has its lemmas
//     ready on its FIRST attempt — no def is wasted attempting a proof that can
//     only succeed after a sibling proves later.
//   - FIXPOINT GATING. The genuinely-unprovable definitions (non-theorems, or
//     SMT-incomplete fragments) burn the full deterministic budget every time
//     they are attempted. Re-attempting them on every scheduled scan is pure
//     waste: a tested def can only newly prove if the store's proof state has
//     changed. So the scan is skipped entirely when the proof-state fingerprint
//     matches the last scan's — the lemma-growth-gating principle (#24) lifted to
//     the whole store. New puts or new proofs move the fingerprint and re-arm it.
func scanBulkProve(st *Store, author string) {
	fp := proofStateFingerprint(st)
	if prev, ok, _ := st.be.getMeta(proofFixpointKey); ok && string(prev) == fp {
		fmt.Println("prove-worker: proof state unchanged since last scan — nothing to prove")
		return
	}
	order := topoFuncOrder(st)
	proved := 0
	for _, h := range order {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil || isFullyProven(m, d) || m.Guarantee.Level == "falsified" {
			continue
		}
		before := len(m.ProvenProps)
		if _, err := apiProveHash(st, h, m.Name); err != nil {
			continue // a strategy was environmentally aborted — leave it tested
		}
		if m2, err := st.GetMeta(h); err == nil && len(m2.ProvenProps) > before {
			proved++
			mark := "·"
			if isFullyProven(m2, d) {
				mark = "✓"
			}
			fmt.Printf("%s %-16s #%s  %s\n", mark, m2.Name, shortHash(h), guaranteeString(m2.Guarantee))
			who := author
			if who == "" {
				who = "prove-worker"
			}
			_ = st.AppendLog(&LogEntry{Author: who, Name: m2.Name, Kind: "prove", Status: "accepted",
				Hash: h, Guarantee: guaranteeString(m2.Guarantee)})
		}
	}
	// Record the new fixpoint. Newly-landed proofs move the fingerprint, so the
	// NEXT scan re-runs and lets any def that needed them as lemmas prove; once a
	// scan proves nothing new, the fingerprint is stable and scans become no-ops.
	_ = st.be.putMeta(proofFixpointKey, []byte(proofStateFingerprint(st)))
	fmt.Printf("prove-worker: scan complete — %d definition(s) advanced\n", proved)
}

// topoFuncOrder returns every object hash with dependencies BEFORE dependents
// (post-order DFS over the dependency graph). Content addressing forbids cycles
// — a def's hash embeds its dependencies — so the visiting guard is belt-and-
// suspenders.
func topoFuncOrder(st *Store) []string {
	order := make([]string, 0)
	state := map[string]int8{} // 0 unseen, 1 visiting, 2 done
	var visit func(h string)
	visit = func(h string) {
		if state[h] != 0 {
			return
		}
		state[h] = 1
		if d, err := st.GetDef(h); err == nil {
			for _, dep := range sortedDepHashes(d) {
				visit(dep)
			}
		}
		state[h] = 2
		order = append(order, h)
	}
	for _, h := range st.AllHashes() {
		visit(h)
	}
	return order
}

// proofStateFingerprint hashes the proof state of the whole store: every object
// and its set of proven property indices. It changes exactly when a new object
// enters or a proof lands — the only events that can let a tested def newly
// prove.
func proofStateFingerprint(st *Store) string {
	h := sha256.New()
	for _, hash := range st.AllHashes() { // AllHashes is sorted → stable
		m, err := st.GetMeta(hash)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s:%v;", hash, m.ProvenProps)
	}
	return hex.EncodeToString(h.Sum(nil))
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
