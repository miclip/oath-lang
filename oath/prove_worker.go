package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"sort"
	"sync"
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
		// SCORING FIRST, deliberately. The two passes are independent — mutation
		// needs no proofs — so the order is a scheduling decision, and the
		// original order got it backwards: proving regularly consumes the whole
		// task timeout on futile attempts, so scoring sat behind the pass least
		// likely to finish and never ran. Scoring is bounded (once per object,
		// hash-keyed) and completes; proving takes whatever remains. Putting the
		// terminating pass first means a timeout costs progress on the unbounded
		// work rather than losing the bounded work entirely.
		scanBulkScore(st, o.author)
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
	// PARALLELISM. Proving is the bottleneck and each z3 goal is a separate
	// subprocess, so independent proofs run concurrently across cores. Two defs
	// are safe to prove at once iff neither is the other's (transitive)
	// dependency — exactly the defs that share a dependency LEVEL. So prove level
	// by level (a barrier between levels keeps lemmas ready), fanning out within a
	// level up to GOMAXPROCS. The Store cache is mutex-guarded and metadata writes
	// serialize on the store lock, so concurrent apiProveHash on distinct defs is
	// safe; the deterministic verdict is unaffected (only wall-clock changes).
	levels := topoFuncLevels(st)
	conc := runtime.GOMAXPROCS(0)
	who := author
	if who == "" {
		who = "prove-worker"
	}
	var out sync.Mutex // serializes the count, log line, journal append, failures
	proved := 0
	// #181: a genuine apiProveHash error (e.g. the run-stability fixpoint not
	// converging within the bound — a MUST-fail per SPEC §7.2) must NOT be swept
	// under "scan complete". Collect them; if any occurred, the fixpoint
	// fingerprint is NOT recorded, so the scan is not falsely marked settled.
	var failures []string
	for lv := range levels {
		sem := make(chan struct{}, conc)
		var wg sync.WaitGroup
		// Results for this level, appended in canonical order after the barrier.
		var pending []LogEntry
		for _, h := range levels[lv] {
			d, err := st.GetDef(h)
			if err != nil || d.K != "func" || len(d.Props) == 0 {
				continue
			}
			m, err := st.GetMeta(h)
			if err != nil || isFullyProven(m, d) || m.Guarantee.Level == "falsified" {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(h, name string, d *Def, before int) {
				defer wg.Done()
				defer func() { <-sem }()
				if _, err := apiProveHash(st, h, name); err != nil {
					// Genuine failure; an ABORTED property is no longer one of these
					// (#72 records per-property partial results instead). Record it so
					// the scan does not falsely report completion (#181).
					out.Lock()
					failures = append(failures, fmt.Sprintf("%s #%s: %v", name, shortHash(h), err))
					out.Unlock()
					return
				}
				m2, err := st.GetMeta(h)
				if err != nil || len(m2.ProvenProps) <= before {
					return
				}
				mark := "·"
				if isFullyProven(m2, d) {
					mark = "✓"
				}
				out.Lock()
				proved++
				fmt.Printf("%s %-16s #%s  %s\n", mark, name, shortHash(h), guaranteeString(m2.Guarantee))
				// BUFFERED, not appended here. Journalling inside the goroutine writes in
				// completion order, so two runs over identical inputs produce journals
				// that differ in ORDER — and since each entry chains the previous, the
				// whole chain downstream differs too. Verdicts are unaffected (defs
				// within a level are independent), but `log.jsonl` stops being
				// byte-reproducible after a parallel scan, which is the one property an
				// append-only audit trail should not lose to a scheduling detail.
				pending = append(pending, LogEntry{Author: who, Name: name, Kind: "prove",
					Status: "accepted", Hash: h, Guarantee: guaranteeString(m2.Guarantee),
					NameTransition: transitionNone})
				out.Unlock()
			}(h, m.Name, d, len(m.ProvenProps))
		}
		wg.Wait() // barrier: level lv fully settled before lv+1 (its lemmas)

		// Append in a canonical order — by name — so the journal is a function of the
		// corpus rather than of goroutine scheduling. The barrier already exists for
		// lemma correctness; ordering here costs nothing beyond it.
		sort.Slice(pending, func(i, j int) bool { return pending[i].Name < pending[j].Name })
		for i := range pending {
			_ = st.AppendLog(&pending[i])
		}
	}
	// #181: if any definition failed to prove (a genuine error, e.g. the
	// run-stability fixpoint not converging within the bound), the scan is NOT
	// settled. Do not record the fixpoint fingerprint — recording it would let the
	// next scan see an unchanged state and skip the failed def forever — and report
	// the failures rather than "scan complete".
	if len(failures) > 0 {
		fmt.Printf("prove-worker: scan did NOT complete — %d definition(s) advanced, %d FAILED; fingerprint NOT recorded so the next scan retries:\n", proved, len(failures))
		for _, f := range failures {
			fmt.Printf("  ✗ %s\n", f)
		}
		return
	}
	// Record the new fixpoint. Newly-landed proofs move the fingerprint, so the
	// NEXT scan re-runs and lets any def that needed them as lemmas prove; once a
	// scan proves nothing new, the fingerprint is stable and scans become no-ops.
	_ = st.be.putMeta(proofFixpointKey, []byte(proofStateFingerprint(st)))
	fmt.Printf("prove-worker: scan complete — %d definition(s) advanced (%d-way)\n", proved, conc)
}

// topoFuncLevels groups every object hash by dependency LEVEL: level 0 has no
// dependencies among the corpus, and a def's level is one past its deepest
// dependency. Defs in the same level cannot depend on each other, so a whole
// level can be proven concurrently once the levels below it are settled.
func topoFuncLevels(st *Store) [][]string {
	order := topoFuncOrder(st) // dependencies before dependents
	level := make(map[string]int, len(order))
	maxLevel := -1
	for _, h := range order {
		lv := 0
		if d, err := st.GetDef(h); err == nil {
			for _, dep := range sortedDepHashes(d) {
				if l := level[dep] + 1; l > lv {
					lv = l
				}
			}
		}
		level[h] = lv
		if lv > maxLevel {
			maxLevel = lv
		}
	}
	if maxLevel < 0 {
		return nil
	}
	out := make([][]string, maxLevel+1)
	for _, h := range order {
		out[level[h]] = append(out[level[h]], h)
	}
	return out
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
	// The prover CONFIG and the KERNEL are part of the state. A goal that aborted
	// under a smaller wall-cap (or a different rlimit) can newly prove under a
	// larger one, and a new kernel can prove — or newly RECORD — what the old one
	// could not, so either change must re-arm the scan exactly as a new proof or
	// object does. Both lessons were learned the hard way: raising the registry
	// worker's OATH_PROVE_WALLCAP_SEC was a silent no-op until the config went in
	// here, and #72 (which banks per-property verdicts an aborted sibling used to
	// discard) changes what a scan RECORDS while touching neither the store nor
	// the config — so without the kernel version the corpus would sit unchanged
	// behind a matching fingerprint, exactly the same trap one layer up.
	fmt.Fprintf(h, "cfg:kernel=%s;rlimit=%d;wallcap=%ds;\n", kernelVersion, effectiveRlimit(), int64(proveWallCap()/time.Second))
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

	// TRANSPORT INVARIANT — read this before "simplifying" the worker's deployment.
	//
	// This binds a name, so the worker is NOT purely an evidence producer and the
	// comfortable version of the invariant would be false. The precise one:
	//
	//	The worker COMPLETES a name-bind that was already ADMITTED. It is never
	//	itself an admission path, and its journal writes must never be routed
	//	through the hosted publication API without a signed principal.
	//
	// Admission — including the freeze on creating unowned names (legacy.go) — is
	// decided at PUT time, before an object is ever enqueued. A `require_proven`
	// name defers its repoint because Z3 is too heavy to run inside a submission,
	// not because the decision was postponed. By the time a job exists, the
	// question "may this principal create this name" has been answered.
	//
	// Two ways that could quietly break, both of which look like cleanups:
	//
	//   - routing the worker's writes through the hosted API to avoid giving it
	//     store access. Its entries are unsigned, so the hosted surface would
	//     refuse them and the freeze would surface as an outage — or, worse,
	//     someone would relax the freeze to make the worker work again;
	//   - letting anything enqueue a gate job outside the put path. That would
	//     make the queue an admission channel that never ran an admission check.
	//
	// If the worker ever needs hosted writes it gets a dedicated SIGNING PRINCIPAL,
	// or a separate evidence-ingestion endpoint whose authority semantics cannot
	// bind names. Not a loosened publication API.
	//
	// Pinned by TestWorkerIsNotAnAdmissionPath.
	//
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

// scanBulkScore records a spec-strength score for every definition that has
// properties and no score yet (#74).
//
// This exists because `explain` made a synchronization obligation visible that
// was previously harmless: once the registry is the SELECTION surface, evidence
// it lacks is evidence a caller cannot weigh, and "spec strength UNMEASURED" on
// an artifact that is in fact 3/3 is a product defect rather than a publishing
// omission. The invariant to hold is narrow — missing evidence must stay
// unknown, but KNOWN evidence must not stay missing.
//
// The score is COMPUTED here, never accepted from a publisher. That is not
// incidental: a client-supplied mutation score is exactly the publisher-asserts
// model this registry exists to replace, and a publisher could simply claim a
// perfect one. `put` therefore transmits source, not verdicts, and the registry
// re-derives — the same rule that already governs proofs. Mutation is seeded
// from each mutant's own content hash, so the re-derivation is deterministic and
// a second run reproduces the first.
//
// Scored ONCE per object: the result is hash-keyed metadata, so a settled corpus
// re-scans as a no-op. Definitions with no mutation points (pure projections and
// constructors) record nothing and are re-attempted each scan, which is cheap
// because generating zero mutants is cheap.
func scanBulkScore(st *Store, author string) {
	scored, skipped := 0, 0
	for _, h := range st.AllHashes() {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil || m.MutantsTotal > 0 {
			skipped++
			continue
		}
		if _, err := apiMutate(st, m.Name); err != nil {
			continue // outside the scorable set; leave it UNMEASURED, honestly
		}
		if m2, err := st.GetMeta(h); err == nil && m2.MutantsTotal > 0 {
			scored++
			fmt.Printf("· %-16s #%s  spec strength %d/%d\n", m2.Name, shortHash(h),
				m2.MutantsKilled+len(m2.WaivedMutants), m2.MutantsTotal)
		}
	}
	fmt.Printf("prove-worker: scoring complete — %d newly scored, %d already scored\n", scored, skipped)
}
