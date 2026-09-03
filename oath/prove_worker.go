package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
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

// proofFixpointKey is a reserved (non-hash) meta key holding the SCAN MARKER
// recorded at the last completed scan. It is never a definition hash, so it
// never collides with a real object.
const proofFixpointKey = "proof-scan-fixpoint"

// proofScanMarkerVersion is the marker's wire version. Bumping it must force a
// full pass, exactly as an unreadable or legacy marker does: an older writer
// recorded a snapshot whose MEANING this reader cannot vouch for, and the
// fail-open direction here is under-proving — a def that should have been
// re-attempted silently never is, behind a marker that looks settled.
const proofScanMarkerVersion = 2

// proofScanMarker is what a completed scan records, and it is the whole reason a
// delta pass is possible: the previous design collapsed the store's proof state
// into one opaque digest, so the only surviving bit was "something, somewhere,
// moved" (#140). The per-hash proven-property sets are the state that digest was
// computed FROM; keeping them turns the outer no-op gate into a diff.
//
// The Digest field stays because it answers a DIFFERENT question from the
// snapshot: it is the outer gate ("has anything at all moved?"), settled with one
// string comparison and no graph work. The snapshot answers the inner one ("what
// moved, and who could benefit?"). Deriving the digest from the snapshot would be
// the cheaper-looking choice and the wrong one — the digest also covers config,
// and proofStateFingerprint is the authority on both halves.
type proofScanMarker struct {
	Version int `json:"version"`
	// Config is proveConfigLine() as it stood at the recorded scan. Compared
	// STRING-WISE rather than folded into the digest, because a config change and
	// a store change demand different responses: the store half is delta-able, the
	// config half is not (see planProofScan).
	Config string `json:"config"`
	// Proven maps every object hash to its proven-property indices at the recorded
	// scan. Sets, not counts: growth is set inclusion, and a count comparison would
	// call a set that changed size-neutrally "unchanged".
	Proven map[string][]int `json:"proven"`
	// Hints maps every object hash to a digest of its author hints (#67) at the
	// recorded scan. Hints are MUTABLE proof-relevant state that moves NEITHER the
	// hash (they live in meta, not in the AST) nor ProvenProps — so without this
	// field `oath hint` is invisible to both gates: the fingerprint does not move,
	// so the outer gate reports a settled corpus, and the delta cannot seed the
	// definition whose lemma input just changed. Version 2 exists for this field;
	// a version-1 marker is rejected and forces one full pass, which is correct.
	Hints map[string]string `json:"hints,omitempty"`
	// Digest is proofStateFingerprint() at the recorded scan — the outer gate.
	Digest string `json:"digest"`
}

// loadProofScanMarker reads the recorded marker. It reports ok=false for an
// ABSENT, unparseable, legacy-plain-digest, or wrong-version marker — every case
// in which the recorded state cannot be diffed — and the caller must then run a
// full pass. A legacy marker is a bare hex digest, which is not valid JSON, so it
// lands here without needing to be recognised by shape.
func loadProofScanMarker(st *Store) (*proofScanMarker, bool) {
	b, ok, err := st.be.getMeta(proofFixpointKey)
	if err != nil || !ok {
		return nil, false
	}
	var m proofScanMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false // legacy plain digest, or corrupt
	}
	if m.Version != proofScanMarkerVersion || m.Proven == nil {
		return nil, false
	}
	return &m, true
}

// proofScanMarkerDigest returns the recorded digest whatever the marker's form —
// including the LEGACY plain-digest marker, whose bytes ARE the digest. The outer
// no-op gate must keep working across the format change: a store that is settled
// under the old marker is still settled, and forcing one full pass on every
// deployment purely to re-record the marker would re-burn the whole heavy tail for
// nothing.
func proofScanMarkerDigest(st *Store) (string, bool) {
	b, ok, err := st.be.getMeta(proofFixpointKey)
	if err != nil || !ok {
		return "", false
	}
	if m, ok := loadProofScanMarker(st); ok {
		return m.Digest, true
	}
	return string(b), true
}

// provenSnapshot is the store half of the marker: every object hash mapped to its
// proven-property indices. Its universe is st.AllHashes() — the same one
// proofStateFingerprint walks — so the snapshot and the digest cannot describe
// different populations.
// hintDigestOf renders one object's author hints as a stable string. Rendering
// rather than comparing structurally: the marker is JSON on disk and a map of
// maps round-trips awkwardly, while a digest compares in one operation and cannot
// silently ignore a field someone adds to HintRef later.
func hintDigestOf(st *Store, h string) string {
	m, err := st.GetMeta(h)
	if err != nil || len(m.Hints) == 0 {
		return ""
	}
	props := make([]int, 0, len(m.Hints))
	for pi := range m.Hints {
		props = append(props, pi)
	}
	sort.Ints(props) // map iteration is unordered; the digest must not be
	var b strings.Builder
	for _, pi := range props {
		refs := append([]HintRef(nil), m.Hints[pi]...)
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Def != refs[j].Def {
				return refs[i].Def < refs[j].Def
			}
			return refs[i].Prop < refs[j].Prop
		})
		fmt.Fprintf(&b, "%d:", pi)
		for _, r := range refs {
			fmt.Fprintf(&b, "%s#%d,", r.Def, r.Prop)
		}
		b.WriteByte(';')
	}
	return b.String()
}

// hintSnapshot is the hint half of the scan state, keyed like provenSnapshot.
// Objects with no hints are omitted so the common case costs nothing.
func hintSnapshot(st *Store) map[string]string {
	out := map[string]string{}
	for _, h := range st.AllHashes() {
		if d := hintDigestOf(st, h); d != "" {
			out[h] = d
		}
	}
	return out
}

func provenSnapshot(st *Store) map[string][]int {
	out := make(map[string][]int)
	for _, h := range st.AllHashes() {
		m, err := st.GetMeta(h)
		if err != nil {
			continue
		}
		out[h] = append([]int(nil), m.ProvenProps...)
	}
	return out
}

// provenSetMoved reports whether a proven-property set CHANGED in either
// direction. SET comparison, not length: §7.2 admits a lemma per proven PROPERTY,
// so a length test would miss a set that changed size-neutrally.
//
// EITHER DIRECTION, and the shrink half is not conservatism. §7.2 records that a
// budget-limited solver is NON-MONOTONE in its axiom set, severely enough to
// decide verdicts rather than merely speed — the corpus witness is
// `q-peek.peek-is-head`, which discharges with no lemmas and does NOT terminate
// once its twelve legitimately relevant lemmas are admitted. So REMOVING a lemma
// can make a dependent goal newly provable, exactly as adding one can. An
// earlier draft seeded on growth alone; a shrink then moved the digest (opening
// the outer gate), seeded nothing, and the marker banked the shrink as settled —
// permanently skipping work the full pass it replaced would have retried.
func provenSetMoved(was, now []int) bool {
	if len(was) != len(now) {
		return true
	}
	had := make(map[int]bool, len(was))
	for _, i := range was {
		had[i] = true
	}
	for _, i := range now {
		if !had[i] {
			return true
		}
	}
	return false
}

// lemmaInputCycle reports whether the lemma-input graph contains a cycle.
//
// AST dependencies cannot cycle — a definition's hash embeds its dependencies —
// but a HINT is metadata attached after the fact, so `A hints B` and `B hints A`
// is constructible. A cycle has no topological order, so topoFuncLevels cannot
// place every consumer after its target: at least one hint edge comes out
// reversed, that consumer runs without the lemma, and the marker then banks the
// target's proof so no later scan ever retries it.
//
// The response is to DECLINE the delta and take a full pass, not to schedule the
// cycle cleverly. A full pass re-attempts everything, so a reversed edge inside
// one pass is simply retried by the next — which is the behaviour that existed
// before the delta and is exactly the guarantee the cycle removes. Cycles are
// pathological (the committed corpus has none), so the cost falls only on stores
// that actually build one.
func lemmaInputCycle(st *Store) bool {
	state := map[string]int8{} // 0 unseen, 1 visiting, 2 done
	var visit func(string) bool
	visit = func(h string) bool {
		switch state[h] {
		case 1:
			return true // back edge
		case 2:
			return false
		}
		state[h] = 1
		deps, _ := lemmaInputHashes(st, h)
		for _, dep := range deps {
			if visit(dep) {
				return true
			}
		}
		state[h] = 2
		return false
	}
	for _, h := range st.AllHashes() {
		if visit(h) {
			return true
		}
	}
	return false
}

// reverseDepClosure returns every hash that transitively DEPENDS ON some seed,
// seeds included. This is the enabling direction of the closure query #138 names:
// a `tested` definition can only newly prove if its candidate lemma set grew, and
// §7.2 draws candidates from the TRANSITIVE dependency closure — so if X gains a
// proven property, the definitions that can benefit are exactly those whose
// closure contains X.
//
// TRANSITIVE, not direct, and this is the measurement error #140 records against
// itself: `oath dependents` walks a definition's own AST and does not recurse, so
// a direct-only answer comes out roughly half-size. Under-scoping here is
// silent — the missed definitions simply never prove — which is the one failure
// direction a delta pass must not have.
//
// The returned set is CLOSED under the dependent-of relation (a reachability set
// always is), and that property is what keeps the single-pass fixpoint argument
// alive: if an attempted definition newly proves DURING the pass, everything that
// could benefit from it is already in the set, so a later topo level picks it up.
// lemmaInputHashes is the set of objects whose proven properties can enter h's
// candidate lemma set. It is deliberately NOT the AST dependency list, and the
// difference is a correctness matter rather than a refinement.
//
// loadLemmaLibrary draws candidates from TWO places: the relevance filter over
// h's dependency closure, and h's author HINTS (#67) — which exist precisely to
// admit proven properties lying OUTSIDE that closure, because the filter would
// never surface them. So a graph built from sortedDepHashes alone models one of
// the two edge kinds, and the omission is silent in the fail-open direction: an
// inert hint whose target newly proves grows the hinted definition's lemma set,
// yet that definition is never seeded, is excluded from the delta, and the
// marker then records the state as SETTLED — so no later scan ever retries it.
// A full pass would have. The committed corpus witnesses this rather than it
// being hypothetical: `t-insert` hints at `count-append`, which is not in
// `t-insert`'s dependency closure.
//
// ANY FUTURE SOURCE OF LEMMA CANDIDATES MUST BE ADDED HERE, never at a call
// site. This is the one place both the delta graph and its equivalence check
// read, so the two cannot drift into disagreeing about what "lemma input" means
// — which is how the hint edge came to be missing from both at once.
func lemmaInputHashes(st *Store, h string) ([]string, bool) {
	ok := true
	seen := map[string]bool{}
	out := []string{}
	add := func(x string) {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	if d, err := st.GetDef(h); err == nil {
		for _, dep := range sortedDepHashes(d) {
			add(dep)
		}
	} else {
		// A failed read is NOT "no dependencies". Treating it as such drops edges,
		// which excludes beneficiaries from the closure — and since definition bytes
		// are immutable and do not enter the fingerprint, a later successful read
		// never re-arms the scan. The caller must refuse to settle instead.
		ok = false
	}
	// Hints live in META, not in the AST, and are hash-keyed facts about h.
	// Union over every property: the delta only needs to know THAT h's input can
	// move, not which goal the hint serves, and the union is the safe direction.
	if m, err := st.GetMeta(h); err == nil {
		for _, refs := range m.Hints {
			for _, r := range refs {
				add(r.Def)
			}
		}
	} else {
		ok = false // same reasoning: a missing hint edge silently under-seeds
	}
	sort.Strings(out) // map iteration over Hints is unordered; keep this stable
	return out, ok
}

func reverseDepClosure(st *Store, seeds map[string]bool) (map[string]bool, bool) {
	complete := true
	dependents := map[string][]string{}
	// listObjects directly, not AllHashes: AllHashes returns nil on a listing
	// error, so an enumeration failure would look exactly like an empty store and
	// the closure would come back as "just the seeds, and complete". On the cloud
	// backend that is a transient network failure, and settling on it records a
	// moved proof state with every dependent omitted.
	all, err := st.be.listObjects()
	if err != nil {
		complete = false
	}
	for _, h := range all {
		deps, read := lemmaInputHashes(st, h)
		if !read {
			complete = false
		}
		for _, dep := range deps {
			dependents[dep] = append(dependents[dep], h)
		}
	}
	out := make(map[string]bool, len(seeds))
	var stack []string
	for h := range seeds {
		if !out[h] {
			out[h] = true
			stack = append(stack, h)
		}
	}
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, d := range dependents[h] {
			if !out[d] {
				out[d] = true
				stack = append(stack, d)
			}
		}
	}
	return out, complete
}

// proveHashFn is the seam scanBulkProve proves THROUGH, rather than calling
// apiProveHash directly. It exists for one reason: the delta is two mechanisms —
// planProofScan decides the re-attempt set, and the level loop ENFORCES it — and a
// test that only exercises the planner passes with the enforcement deleted. The
// seam is what lets a test observe exactly which definitions a real scan attempted,
// and inject a failure on one, without running z3 over the whole corpus.
//
// Production always holds apiProveHash. Tests swap it and restore it; nothing else
// writes to it.
var proveHashFn = apiProveHash

// proofScanPlan is what one scan will traverse.
type proofScanPlan struct {
	Full    bool            // traverse every definition
	Reason  string          // why, for the operator-facing log line
	Attempt map[string]bool // when !Full, exactly the hashes to attempt
	// Baseline is the proof state AS OF PLANNING, and it is what the marker is
	// recorded over — never the store as it stands when the scan ends.
	//
	// A scan attempts what the plan named. Anything PUBLISHED AFTER the plan was
	// taken is therefore not attempted, by either path: the delta filter skips it
	// because it is not in Attempt, and a full pass skips it because topoFuncLevels
	// was already computed. Recording the marker over the live store would then
	// bank that definition's (empty) proven set as if it had been scanned — so the
	// next scan sees it as KNOWN rather than NEW, does not seed it, and the digest
	// matches, so the outer gate reports a settled corpus. The definition is never
	// proven, and nothing ever says so. Restricting the marker to the baseline
	// leaves such a hash ABSENT, which is exactly what makes the next scan treat it
	// as new.
	Baseline map[string][]int
	// BaselineHints is the hint half of the same snapshot, needed for the same
	// reason: an unattempted hash banks its baseline hint state, not the live one.
	BaselineHints map[string]string
	// NoSettle marks a scan that must NOT record a marker, whatever else it does.
	// Two causes today, and they are one rule: the scan cannot establish that it
	// saw the whole graph correctly, so recording "this state is settled" would be
	// a claim it has not earned — and because the missed work leaves no trace in
	// the fingerprint, nothing would ever re-arm it.
	//
	//   a hint CYCLE          the graph has no topological order, so at least one
	//                         consumer runs before its target
	//   an INCOMPLETE READ    a GetDef/GetMeta failure dropped edges, so the
	//                         closure may exclude a real beneficiary
	//
	// The cost is a repeated pass while the condition holds, which is the correct
	// direction: repeating work is recoverable, banking a false "settled" is not.
	NoSettle       bool
	NoSettleReason string
	// Cyclic marks a lemma-input graph containing a hint cycle. Such a graph has
	// NO topological order, so no single pass can place every consumer after its
	// target — topoFuncOrder breaks the cycle arbitrarily and one consumer runs
	// without its lemma. A full pass does not repair that: recording the marker
	// afterwards banks the target's proof, the digest then matches the live store,
	// and the consumer is never retried. So a cyclic scan REFUSES TO SETTLE — it
	// leaves the previous marker in place and re-runs next time, which cannot
	// under-prove. The cost is repeated full passes for as long as the cycle
	// exists; iterating one scan to a fixpoint would be the cheaper fix and is not
	// built, because no store in this project has ever had a hint cycle.
	Cyclic bool
}

// planProofScan decides the re-attempt set for this scan (#140).
//
//	if the marker is missing/legacy/unreadable   -> full pass
//	if the prover CONFIG changed                 -> full pass
//	otherwise                                    -> new hashes, plus the
//	                                                transitive reverse-dependency
//	                                                closure of every hash whose
//	                                                proven-property set grew
//
// THE CONFIG HALF MUST FORCE A FULL PASS, and that is not an oversight left to
// tidy away later. A goal that aborted under a smaller wall-cap — or under an
// older kernel, or with instantiation off — has NO dependency-graph relationship
// to the change that lets it succeed, so no closure over the store can find it.
// Only the store half of the state is delta-able.
//
// The closure is seeded with the NEW hashes as well as the grown ones. In a
// well-formed store that changes nothing (a dependent's hash embeds its
// dependencies, so anything depending on a new object is itself new), but it makes
// the attempt set closed under the dependent-of relation unconditionally, which is
// what the within-pass fixpoint argument in reverseDepClosure rests on. It errs
// toward attempting more, which is the safe direction: the failure this design must
// not have is under-proving behind a marker that reads as settled.
func planProofScan(st *Store) proofScanPlan {
	marker, ok := loadProofScanMarker(st)
	return planProofScanFrom(st, marker, ok)
}

// planProofScanFrom is planProofScan over an EXPLICIT marker. Split out so the
// plan can be measured against the committed corpus without writing a marker into
// it: the corpus store is the claim's population and it is read-only, and a test
// that had to write its premise into the artifact under measurement would be
// measuring its own setup.
func planProofScanFrom(st *Store, marker *proofScanMarker, ok bool) proofScanPlan {
	now := provenSnapshot(st) // the baseline: taken ONCE, before any decision
	nowHints := hintSnapshot(st)
	// Cyclic is computed FIRST and carried on every path, deliberately. It is not
	// one more reason to take a full pass — it is a reason never to SETTLE — so a
	// scan that is already full for some other reason (no marker, changed config)
	// must still refuse to record. Testing it after those checks let a cyclic store
	// with a changed config settle, which is the same permanent miss by a different
	// route.
	cyclic := lemmaInputCycle(st)
	noSettle, noSettleWhy := false, ""
	// The baseline snapshots above go through AllHashes, which reports a failed
	// listing as an empty store. Check the listing itself once, here, so a
	// transient enumeration failure cannot masquerade as a corpus with nothing in
	// it — which would make every marker entry look absent and the plan settle over
	// a store it never saw.
	if _, err := st.be.listObjects(); err != nil {
		noSettle, noSettleWhy = true, "the object listing failed, so the plan cannot see the whole store"
	}
	if cyclic {
		noSettle, noSettleWhy = true, "a hint cycle makes the lemma-input graph unorderable"
	}
	if !ok || marker == nil {
		return proofScanPlan{Full: true, Cyclic: cyclic, NoSettle: noSettle, NoSettleReason: noSettleWhy,
			Baseline: now, BaselineHints: nowHints, Reason: "no delta marker (first scan, or a legacy marker)"}
	}
	if marker.Config != proveConfigLine() {
		return proofScanPlan{Full: true, Cyclic: cyclic, NoSettle: noSettle, NoSettleReason: noSettleWhy,
			Baseline: now, BaselineHints: nowHints, Reason: "prover config changed (kernel/rlimit/wallcap/instantiate)"}
	}
	if cyclic {
		return proofScanPlan{Full: true, Cyclic: true, NoSettle: true, NoSettleReason: noSettleWhy,
			Baseline: now, BaselineHints: nowHints,
			Reason: "a hint cycle makes the lemma-input graph unorderable; taking a full pass and NOT settling"}
	}
	seeds := map[string]bool{}
	newHashes := 0
	grown := 0
	for h, props := range now {
		was, known := marker.Proven[h]
		if !known {
			seeds[h] = true
			newHashes++
			continue
		}
		if provenSetMoved(was, props) {
			seeds[h] = true
			grown++
		}
	}
	// A hint change moves neither the hash nor the proven set, so it seeds here or
	// it is invisible. The hinted definition is itself the consumer, so seeding the
	// hash whose hints moved is the whole requirement; reverseDepClosure then pulls
	// in anything downstream of it.
	hintChanged := 0
	for h, d := range nowHints {
		if marker.Hints[h] != d {
			seeds[h] = true
			hintChanged++
		}
	}
	for h := range marker.Hints {
		if _, still := nowHints[h]; !still {
			seeds[h] = true // hints REMOVED: the lemma set shrank, re-attempt to re-derive
			hintChanged++
		}
	}
	attempt, complete := reverseDepClosure(st, seeds)
	if !complete {
		noSettle, noSettleWhy = true, "a definition or its metadata could not be read while building the lemma-input graph"
	}
	return proofScanPlan{
		Full:           false,
		NoSettle:       noSettle,
		NoSettleReason: noSettleWhy,
		Baseline:       now,
		BaselineHints:  nowHints,
		Reason:         fmt.Sprintf("delta: %d new, %d proof-state-moved, %d hint-changed -> %d definition(s) in the dependents closure", newHashes, grown, hintChanged, len(attempt)),
		Attempt:        attempt,
	}
}

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
	if prev, ok := proofScanMarkerDigest(st); ok && prev == fp {
		fmt.Println("prove-worker: proof state unchanged since last scan — nothing to prove")
		return
	}
	// The OUTER gate above says something moved. The INNER one says what, and who
	// could benefit (#140): a full pass re-attempts every non-theorem in the
	// corpus, each burning its whole deterministic budget, to discover one new
	// definition. Both gates are kept — the digest is one string comparison and
	// costs nothing, and the plan is only reached when it has already failed.
	plan := planProofScan(st)
	fmt.Printf("prove-worker: %s\n", plan.Reason)
	if !plan.Full && len(plan.Attempt) == 0 {
		// Reachable: the digest moved for a reason no definition can benefit from —
		// metadata that is not a proven-property set (a mutation score, a waiver)
		// does not enter the fingerprint, but a def whose proven set SHRANK would.
		// Nothing to attempt is a complete answer; the marker is still re-recorded
		// below so the next scan's diff starts from here.
		fmt.Println("prove-worker: nothing in the delta can newly prove")
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
	// produced is what THIS SCAN established: for every hash it proved, the proven
	// set as observed immediately after its own prove call. The marker is built
	// from this plus the plan's baseline, and never by re-reading the store at the
	// end — a final whole-store read cannot distinguish this scan's work from a
	// proof another worker landed after our dependents had already run, and banking
	// the latter consumes the very movement that would have re-seeded them.
	produced := map[string][]int{}
	for lv := range levels {
		sem := make(chan struct{}, conc)
		var wg sync.WaitGroup
		// Results for this level, appended in canonical order after the barrier.
		var pending []LogEntry
		for _, h := range levels[lv] {
			if !plan.Full && !plan.Attempt[h] {
				continue // outside the delta: its lemma set did not grow (#140)
			}
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
			go func(h, name string, d *Def, before []int) {
				defer wg.Done()
				defer func() { <-sem }()
				if _, err := proveHashFn(st, h, name); err != nil {
					// Genuine failure; an ABORTED property is no longer one of these
					// (#72 records per-property partial results instead). Record it so
					// the scan does not falsely report completion (#181).
					out.Lock()
					failures = append(failures, fmt.Sprintf("%s #%s: %v", name, shortHash(h), err))
					out.Unlock()
					return
				}
				m2, err := st.GetMeta(h)
				if err != nil || !provenSetMoved(before, m2.ProvenProps) {
					return
				}
				// CHANGED, not GREW. The solver is non-monotone in its axiom set, so an
				// attempt can legitimately leave a set that is smaller or the same size
				// but different. That is still this scan's own work and must be banked,
				// or the marker's digest is stale the moment it is written and the next
				// scheduled scan re-derives the same dependents closure for nothing.
				grew := len(m2.ProvenProps) > len(before)
				mark := "·"
				if isFullyProven(m2, d) {
					mark = "✓"
				}
				out.Lock()
				produced[h] = append([]int(nil), m2.ProvenProps...)
				if !grew {
					// Banked, but not reported as an advance: the operator-facing count
					// means "newly proven", and a size-neutral change is not that.
					out.Unlock()
					return
				}
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
			}(h, m.Name, d, append([]int(nil), m.ProvenProps...))
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
	// Record the new marker. Newly-landed proofs move the digest, so the NEXT scan
	// re-runs and lets any def that needed them as lemmas prove; once a scan proves
	// nothing new, the digest is stable and scans become no-ops. The SNAPSHOT is
	// what makes the next scan's re-arm a delta rather than a full pass, and it is
	// taken over the whole store, not over the attempted subset: a definition
	// outside the delta still has a proven-property set, and omitting it would make
	// it read as NEW to the next scan.
	if plan.NoSettle {
		// Deliberately NOT recording: see proofScanPlan.NoSettle. Settling here is
		// the one thing that turns a missed proof into a permanent one.
		fmt.Printf("prove-worker: scan complete — %d definition(s) advanced (%d-way); marker NOT recorded (%s), so the next scan re-runs\n",
			proved, conc, plan.NoSettleReason)
		return
	}
	recordProofScanMarker(st, plan, produced)
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
		// lemmaInputHashes, not sortedDepHashes: SCHEDULING must respect hint edges
		// for the same reason PLANNING does. A hinted consumer whose target proves
		// during this very pass has to run AFTER it, or it is attempted without the
		// lemma that was the whole point of the hint — and the marker then banks the
		// target's new proof, so the next scan sees no movement and never retries.
		// Fixing the planner alone left exactly that hole.
		deps, _ := lemmaInputHashes(st, h) // scheduling only; settling is decided in the plan
		for _, dep := range deps {
			if l := level[dep] + 1; l > lv {
				lv = l
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
		// Hint edges are included here too, so the ORDER and the LEVELS agree about
		// what "depends on" means. Content addressing forbids AST cycles, but a hint
		// is metadata added after the fact and CAN form one (A hints B, B hints A);
		// the visiting guard above already breaks that, degrading to an arbitrary
		// but terminating order rather than looping.
		deps, _ := lemmaInputHashes(st, h)
		for _, dep := range deps {
			visit(dep)
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
// proveConfigLine is the ONE spelling of the outcome-affecting prover config, and
// it exists as a function because two things now depend on it: the fingerprint
// hashes it, and the scan marker stores it verbatim so a later scan can tell a
// config change from a store change. Two hand-written copies could disagree
// without either being wrong when it was written, and the disagreement would be
// invisible from inside either one — the delta pass would keep running while the
// config that forces a full one had moved.
//
// The prover CONFIG and the KERNEL are part of the proof state. A goal that
// aborted under a smaller wall-cap (or a different rlimit) can newly prove under a
// larger one, and a new kernel can prove — or newly RECORD — what the old one
// could not, so either change must re-arm the scan exactly as a new proof or
// object does. Both lessons were learned the hard way: raising the registry
// worker's OATH_PROVE_WALLCAP_SEC was a silent no-op until the config went in
// here, and #72 (which banks per-property verdicts an aborted sibling used to
// discard) changes what a scan RECORDS while touching neither the store nor the
// config — so without the kernel version the corpus would sit unchanged behind a
// matching fingerprint, exactly the same trap one layer up.
// The deterministic-instantiation preview is the same kind of knob: it is
// outcome-affecting (it can newly prove a composed-recursion goal), so enabling it
// must re-arm the scan just like a larger wall-cap. Left out, a settled store
// would sit behind a matching fingerprint and the opt-in would silently no-op for
// bulk proving.
//
// None of these is reachable by a dependency-graph closure, which is why a change
// here forces a FULL pass rather than a delta one (planProofScan).
func proveConfigLine() string {
	return fmt.Sprintf("cfg:kernel=%s;rlimit=%d;wallcap=%ds;instantiate=%t;\n",
		kernelVersion, effectiveRlimit(), int64(proveWallCap()/time.Second), instantiationEnabled())
}

// recordProofScanMarker snapshots the settled state: the config, every object's
// proven-property set, and the digest. Written only after a scan completes without
// failures — an incomplete scan must not leave a marker, or the definitions it
// never reached read as settled to the next one.
func recordProofScanMarker(st *Store, plan proofScanPlan, produced map[string][]int) {
	// CURRENT values, BASELINE membership. The values must be current or the proofs
	// this scan just landed would not be banked; the membership must be the
	// baseline or a definition published mid-scan is recorded as scanned when it
	// was not. Both halves matter, and the digest is taken over the same restricted
	// map — a digest covering the live store would let the OUTER gate report
	// "unchanged" on the very next scan and hide the unattempted definition a
	// second time.
	rec := make(map[string][]int, len(plan.Baseline))
	for h, base := range plan.Baseline {
		// THIS SCAN'S OWN WORK, or the baseline. Never a fresh read of the store.
		// A hash this scan proved banks what the scan observed right after proving
		// it; everything else banks what was there when the plan was taken. So a
		// proof landed by another worker at ANY point during the scan — before the
		// plan, after it, or after this hash's dependents had already run — stays
		// unbanked, and the next scan still sees it move and seeds its dependents.
		if v, ok := produced[h]; ok {
			rec[h] = v
			continue
		}
		rec[h] = base
		// A hash in the baseline but absent now cannot happen in an append-only
		// store; if it ever does, omitting it makes the next scan treat it as new,
		// which is the safe direction.
	}
	// Hints are never PRODUCED by a scan — only by `oath hint` — so the baseline is
	// the whole truth here. Re-reading them at the end would bank a hint added
	// mid-scan as though this pass had honoured it.
	recHints := map[string]string{}
	for h, v := range plan.BaselineHints {
		if _, inBaseline := plan.Baseline[h]; inBaseline {
			recHints[h] = v
		}
	}
	b, err := json.Marshal(proofScanMarker{
		Version: proofScanMarkerVersion,
		Config:  proveConfigLine(),
		Proven:  rec,
		Hints:   recHints,
		Digest:  fingerprintOfSnapshot(rec, recHints),
	})
	if err != nil {
		return
	}
	_ = st.be.putMeta(proofFixpointKey, b)
}

// fingerprintOfSnapshot is proofStateFingerprint over an EXPLICIT proof-state map
// rather than over the live store. The two agree byte-for-byte when the map is
// provenSnapshot(st) — same config line, same sorted hashes, same %v rendering —
// which is what lets the marker's digest describe the state the scan actually
// settled instead of the store as it stands afterwards.
func fingerprintOfSnapshot(snap map[string][]int, hints map[string]string) string {
	h := sha256.New()
	fmt.Fprint(h, proveConfigLine())
	hashes := make([]string, 0, len(snap))
	for hash := range snap {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes) // AllHashes is sorted; match it or the digests diverge
	for _, hash := range hashes {
		fmt.Fprintf(h, "%s:%v;", hash, snap[hash])
	}
	writeHintDigests(h, hints)
	return hex.EncodeToString(h.Sum(nil))
}

func proofStateFingerprint(st *Store) string {
	h := sha256.New()
	fmt.Fprint(h, proveConfigLine())
	for _, hash := range st.AllHashes() { // AllHashes is sorted → stable
		m, err := st.GetMeta(hash)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s:%v;", hash, m.ProvenProps)
	}
	// Hints are outcome-affecting and mutable without any hash or proven-set
	// movement, so they belong in the fingerprint for the same reason the config
	// does: without them `oath hint` is a silent no-op behind a matching digest.
	writeHintDigests(h, hintSnapshot(st))
	return hex.EncodeToString(h.Sum(nil))
}

// writeHintDigests appends a hint snapshot to a fingerprint in sorted order, so
// the two fingerprint functions cannot disagree about ordering.
//
// AN EMPTY SNAPSHOT WRITES NOTHING, and that is a migration requirement rather
// than a micro-optimization. A store with no hints must fingerprint EXACTLY as it
// did before hints were tracked, or the legacy bare-digest marker can never match
// and every existing deployment re-burns the whole heavy tail on its first scan
// after the upgrade — the precise cost proofScanMarkerDigest's legacy path exists
// to avoid. A store that DOES carry hints takes that full pass, correctly: its
// hint state was never tracked before, so it has genuinely never been scanned
// under the state this marker records.
func writeHintDigests(h io.Writer, hints map[string]string) {
	if len(hints) == 0 {
		return
	}
	keys := make([]string, 0, len(hints))
	for k := range hints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprint(h, "\nhints:")
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s;", k, hints[k])
	}
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
	//	through the hosted publication API without a signed publication.
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
