package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
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
// digest, so a second scan with nothing changed is a no-op (the OUTER fixpoint
// gate — no re-burning the budget on already-settled defs). The digest is now
// carried inside the versioned scan marker; it is read through
// proofScanMarkerDigest, which is the gate's own authority on it and also the one
// that keeps a legacy plain marker working.
func TestScanBulkProveFixpointGate(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, dblSrc)

	scanBulkProve(st, "test")
	h, _ := st.Resolve("dbl")
	if m, _ := st.GetMeta(h); m.Guarantee.Level != "proven" {
		t.Fatalf("dbl not proven after scan: %q", m.Guarantee.Level)
	}
	prev, ok := proofScanMarkerDigest(st)
	if !ok {
		t.Fatal("no fixpoint marker recorded")
	}
	if prev != proofStateFingerprint(st) {
		t.Fatal("fixpoint marker doesn't match current proof state — a re-scan would re-burn")
	}
}

// Several independent provable defs (same dependency level) prove CONCURRENTLY
// under scanBulkProve. Run with -race to catch data races in the parallel path.
func TestScanBulkProveParallel(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn dbl [] [(x Int)] Int (+ x x)
		(prop p [(x Int)] (== (dbl x) (+ x x))))`)
	put(t, st, `(defn trip [] [(x Int)] Int (+ x (+ x x))
		(prop q [(x Int)] (== (trip x) (+ x (+ x x)))))`)
	put(t, st, `(defn quad [] [(x Int)] Int (+ (+ x x) (+ x x))
		(prop r [(x Int)] (== (quad x) (+ (+ x x) (+ x x)))))`)

	scanBulkProve(st, "test")

	for _, n := range []string{"dbl", "trip", "quad"} {
		h, _ := st.Resolve(n)
		if m, _ := st.GetMeta(h); m.Guarantee.Level != "proven" {
			t.Fatalf("%s not proven after parallel scan: %q", n, m.Guarantee.Level)
		}
	}
	if err := st.VerifyLog(); err != nil {
		t.Fatalf("journal after parallel scan: %v", err)
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

// ---------------------------------------------------------------------------
// The delta scan (#140).
//
// WHAT THESE TESTS ESTABLISH, and why there are two corpus-backed populations
// rather than one. #140's own acceptance note is explicit that the two classes
// test DIFFERENT claims, and that either alone is misleading:
//
//	application-class  a leaf/app definition, whose dependents closure holds none
//	                   of the heavy tail. Establishes that the delta AVOIDS
//	                   irrelevant work. Fails if the pass still walks the tail.
//	foundation-class   `List`-class, whose closure legitimately holds most of the
//	                   tail. Establishes that the pass stays BOUNDED and SCOPED
//	                   when the delta is large. Fails if the plan exceeds the full
//	                   pass, or is not closed under the dependent-of relation (a
//	                   pass that cannot finish the job in one traversal).
//
// Testing only the first validates the case the optimization already handles.
// Testing only the second says nothing about whether it does anything at all.
//
// The universe is the COMMITTED CORPUS, read-only, opened through the filesystem
// backend explicitly (OpenStore consults OATH_BACKEND and would silently describe
// a different store). Nothing here writes to it, and nothing here proves: the
// claim is about the PLAN, so the instrument measures the plan.
//
// The expected closures are computed by an INDEPENDENT breadth-first walk in this
// file rather than by calling reverseDepClosure. A test that asked the
// implementation what the answer should be would agree with a direct-only walk,
// with a broken walk, and with any other walk — which is the whole class of defect
// these controls exist to catch.

// openCorpus opens the committed store read-only. Mirrors the census test's
// reasoning: the filesystem backend explicitly, because the claim is about the
// store at this path and not about whatever OATH_BACKEND points to.
func openCorpus(t *testing.T) *Store {
	t.Helper()
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Skipf("committed store unavailable: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Skipf("committed store unavailable: %v", err)
	}
	return st
}

// corpusTail is the heavy tail: LIVE objects that are functions with properties
// and are not fully proven — the definitions that burn the full deterministic
// budget on every pass that attempts them. Live, because `meta/` accumulates a
// record for every object ever stored and a directory walk answers a question
// about the store's HISTORY while looking like one about the corpus.
func corpusTail(t *testing.T, st *Store) map[string]bool {
	t.Helper()
	tail := map[string]bool{}
	for _, h := range st.Names() {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil || isFullyProven(m, d) || m.Guarantee.Level == "falsified" {
			continue
		}
		tail[h] = true
	}
	if len(tail) == 0 {
		t.Fatal("no heavy tail in the committed corpus — this test's premise is gone, not satisfied")
	}
	return tail
}

// expectedDependents walks the reverse dependency graph itself: seed, then
// everything that mentions it, then everything that mentions THOSE, to a fixpoint.
// `transitive` false stops after one step, which is the direct-only control.
// testLemmaEdges is the TEST'S OWN account of which objects can feed h's lemma
// set: AST dependencies plus author hints (#67), which are admitted from outside
// the dependency closure. It deliberately does NOT call the production
// lemmaInputHashes — it reads the def and the meta itself, so the two are
// independent statements of the same claim and a disagreement fails a test
// rather than cancelling out. That independence is the whole point: the hint
// edge was originally missing from the planner AND from this file's equivalence
// check at the same time, so a check that borrowed the planner's notion of
// "lemma input" was green over the exact defect it existed to catch.
func testLemmaEdges(st *Store, h string) []string {
	seen := map[string]bool{}
	var out []string
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
	}
	if m, err := st.GetMeta(h); err == nil {
		for _, refs := range m.Hints {
			for _, r := range refs {
				add(r.Def)
			}
		}
	}
	sort.Strings(out)
	return out
}

func expectedDependents(t *testing.T, st *Store, seed string, transitive bool) map[string]bool {
	t.Helper()
	deps := map[string][]string{}
	for _, h := range st.AllHashes() {
		deps[h] = testLemmaEdges(st, h)
	}
	out := map[string]bool{seed: true}
	frontier := []string{seed}
	for len(frontier) > 0 {
		var next []string
		for h, ds := range deps {
			if out[h] {
				continue
			}
			for _, d := range ds {
				for _, f := range frontier {
					if d == f {
						out[h] = true
						next = append(next, h)
					}
				}
			}
		}
		if !transitive {
			break
		}
		frontier = next
	}
	return out
}

// markerAsIs is the marker a completed scan would have recorded for the store as
// it stands: nothing changed since, so a plan built from it is empty.
func markerAsIs(st *Store) *proofScanMarker {
	// Hints included deliberately: a marker that omits them reads as "every hinted
	// definition just changed", so every test built on this helper would seed those
	// definitions for a reason the scenario never created.
	hints := hintSnapshot(st)
	return &proofScanMarker{
		Version: proofScanMarkerVersion,
		Config:  proveConfigLine(),
		Proven:  provenSnapshot(st),
		Hints:   hints,
		Digest:  proofStateFingerprint(st),
	}
}

// resolveOne resolves a corpus name to its hash, skipping if the corpus no longer
// carries it. Skip rather than fail: the corpus is allowed to evolve, and a test
// that hard-fails on a renamed definition asserts a fact about the corpus that
// this test was never measuring.
func resolveOne(t *testing.T, st *Store, name string) string {
	t.Helper()
	h, ok := st.Resolve(name)
	if !ok {
		t.Skipf("%q is not live in the committed corpus", name)
	}
	return h
}

// APPLICATION-CLASS. Editing a leaf definition — one nothing else depends on — must
// produce a plan that attempts that definition and nothing else, and in particular
// re-attempts NONE of the heavy tail. This is the case #140 measured as the median
// (0% of tail) and the motivating scenario: the daily tick stops paying the whole
// heavy tail to discover one thing.
//
// FAILS UNDER: a plan that returns Full (the delta is not scoping at all), a plan
// that seeds the closure with more than the changed definition, or any regression
// that lets the tail back into the attempt set.
func TestDeltaPlanApplicationClassAvoidsTail(t *testing.T) {
	st := openCorpus(t)
	tail := corpusTail(t, st)
	all := st.AllHashes()

	// Choose the representative from the corpus rather than hardcoding a name:
	// any live definition that nothing depends on, computed by this file's own
	// walk. Hardcoding would pin a fact about the corpus that the claim does not
	// contain, and it would rot.
	var seedName, seedHash string
	names := st.Names()
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		h := names[n]
		if len(expectedDependents(t, st, h, true)) == 1 {
			seedName, seedHash = n, h
			break
		}
	}
	if seedHash == "" {
		t.Skip("no leaf definition in the committed corpus")
	}

	// The premise: this definition just changed, so it is a hash the last scan
	// never saw. Everything else is exactly as the last scan left it.
	m := markerAsIs(st)
	delete(m.Proven, seedHash)
	plan := planProofScanFrom(st, m, true)

	if plan.Full {
		t.Fatalf("a leaf change forced a FULL pass (%s) — the delta is not scoping", plan.Reason)
	}
	want := expectedDependents(t, st, seedHash, true)
	if len(plan.Attempt) != len(want) {
		t.Fatalf("attempt set = %d definition(s), independently computed closure = %d (seed %q)",
			len(plan.Attempt), len(want), seedName)
	}
	for h := range want {
		if !plan.Attempt[h] {
			t.Fatalf("closure member %s missing from the attempt set (seed %q)", shortHash(h), seedName)
		}
	}

	// THE CLAIM: the IRRELEVANT tail is avoided. A full pass re-attempts all of it.
	//
	// The seed itself is excluded from the claim rather than from the SAMPLE, and
	// the difference matters: the definition that changed is not irrelevant work,
	// and a leaf that happens to be an honest unproven exhibit (the committed corpus
	// keeps several deliberately) is still a legitimate application-class case.
	// Picking a fully-proven leaf instead would choose the flattering sample and
	// weaken the claim to one about the leaves that happen to prove.
	var reattempted []string
	for h := range plan.Attempt {
		if tail[h] && h != seedHash {
			reattempted = append(reattempted, shortHash(h))
		}
	}
	if len(reattempted) > 0 {
		t.Fatalf("a leaf change re-attempts %d of the %d heavy-tail definitions besides itself: %v (seed %q)",
			len(reattempted), len(tail), reattempted, seedName)
	}
	avoided := len(tail)
	if tail[seedHash] {
		avoided--
	}
	if avoided < len(tail)-1 {
		t.Fatalf("internal: avoided %d of %d", avoided, len(tail))
	}
	if len(plan.Attempt) >= len(all) {
		t.Fatalf("attempt set (%d) is not smaller than the corpus (%d) — no work was avoided",
			len(plan.Attempt), len(all))
	}
	t.Logf("application-class %q (in tail: %v): attempt=%d of %d objects, heavy-tail definitions avoided %d of %d",
		seedName, tail[seedHash], len(plan.Attempt), len(all), avoided, len(tail))
}

// FOUNDATION-CLASS. `List` is the measured worst case: its transitive dependents
// closure holds most of the heavy tail. The delta pass is then nearly the same job
// as a full pass, which is why #140 had to WITHDRAW "the timeout stops being
// load-bearing". What must still hold is that the plan is SCOPED (never more than
// the full pass, and excluding what does not depend on the seed) and BOUNDED in the
// sense that matters for a single traversal: the attempt set is CLOSED under the
// dependent-of relation, so a definition that newly proves mid-pass has every
// beneficiary already scheduled at a later topo level. Without that, a delta pass
// would settle the marker while leaving definitions that could have proven
// un-attempted — under-proving behind a marker that reads as settled.
//
// FAILS UNDER: a direct-only closure (the transitively-but-not-directly dependent
// definitions go missing), an attempt set that is not dependent-closed, or a plan
// that exceeds the corpus.
func TestDeltaPlanFoundationClassIsScopedAndClosed(t *testing.T) {
	st := openCorpus(t)
	tail := corpusTail(t, st)
	seedHash := resolveOne(t, st, "List")

	m := markerAsIs(st)
	delete(m.Proven, seedHash)
	plan := planProofScanFrom(st, m, true)
	if plan.Full {
		t.Fatalf("a foundation change forced a FULL pass (%s); it should still be a scoped delta", plan.Reason)
	}

	// SCOPED: never more than the full pass would do, and never anything outside
	// the independently computed closure.
	want := expectedDependents(t, st, seedHash, true)
	for h := range plan.Attempt {
		if !want[h] {
			t.Fatalf("attempt set holds %s, which does not transitively depend on List", shortHash(h))
		}
	}
	if len(plan.Attempt) > len(st.AllHashes()) {
		t.Fatalf("attempt set (%d) exceeds the corpus (%d)", len(plan.Attempt), len(st.AllHashes()))
	}
	// And the corpus genuinely holds definitions outside it, or "scoped" is vacuous.
	outside := 0
	for _, h := range st.AllHashes() {
		if !plan.Attempt[h] {
			outside++
		}
	}
	if outside == 0 {
		t.Fatal("every object is in List's closure — this test cannot distinguish scoped from full")
	}

	// THE DIRECT-ONLY CONTROL. Definitions that depend on List only through an
	// intermediary must be present. `oath dependents` is direct and does not
	// recurse; #140 records that using it put the measured numbers at roughly half
	// size, in the flattering direction.
	direct := expectedDependents(t, st, seedHash, false)
	transitiveOnly := 0
	for h := range want {
		if direct[h] {
			continue
		}
		transitiveOnly++
		if !plan.Attempt[h] {
			t.Fatalf("%s depends on List transitively but not directly, and is NOT in the attempt set — the closure is direct-only", shortHash(h))
		}
	}
	if transitiveOnly == 0 {
		t.Fatal("no transitively-but-not-directly dependent definition exists — the direct-only control cannot fire")
	}

	// CLOSED UNDER dependent-of: the property that makes ONE traversal sufficient.
	deps := map[string][]string{}
	for _, h := range st.AllHashes() {
		deps[h] = testLemmaEdges(st, h)
	}
	for h, ds := range deps {
		for _, d := range ds {
			if plan.Attempt[d] && !plan.Attempt[h] {
				t.Fatalf("%s is attempted but its dependent %s is not — a mid-pass proof would have no beneficiary scheduled",
					shortHash(d), shortHash(h))
			}
		}
	}

	// The measurement #140 recorded, reported rather than asserted: a foundational
	// edit legitimately re-attempts most of the tail, which is why the task timeout
	// stays load-bearing.
	hits := 0
	for h := range plan.Attempt {
		if tail[h] {
			hits++
		}
	}
	if hits == 0 {
		t.Fatal("List's closure re-attempts none of the heavy tail — the foundation-class premise no longer holds")
	}
	t.Logf("foundation-class List: attempt=%d of %d objects (%d outside), tail re-attempted=%d of %d, transitive-only dependents=%d",
		len(plan.Attempt), len(st.AllHashes()), outside, hits, len(tail), transitiveOnly)
}

// THE FULL-CORPUS-TRAVERSAL CONTROL, stated as its own test so the mutation it
// catches is named: with the store exactly as the last scan left it, the plan must
// attempt NOTHING. A planner that ignores the delta and returns the corpus fails
// here even though every proof verdict it would produce is identical — which is the
// point, because "still correct, just as slow" is precisely the regression a
// verdict-based test cannot see.
func TestDeltaPlanUnchangedStoreAttemptsNothing(t *testing.T) {
	st := openCorpus(t)
	plan := planProofScanFrom(st, markerAsIs(st), true)
	if plan.Full {
		t.Fatalf("unchanged store forced a FULL pass: %s", plan.Reason)
	}
	if len(plan.Attempt) != 0 {
		t.Fatalf("unchanged store plans %d attempt(s), want 0: %s", len(plan.Attempt), plan.Reason)
	}
}

// THE CONFIG CONTROL. A goal that aborted under a smaller wall-cap, an older
// kernel, or with instantiation off has NO dependency-graph relationship to the
// change that lets it succeed, so no closure over the store can find it. A config
// change must therefore force a full pass — and this is the half most likely to be
// "optimized away" by someone who sees the store diff is empty.
func TestDeltaPlanConfigChangeForcesFullPass(t *testing.T) {
	st := openCorpus(t)
	m := markerAsIs(st)
	m.Config = "cfg:kernel=older;rlimit=1;wallcap=1s;instantiate=false;\n"
	plan := planProofScanFrom(st, m, true)
	if !plan.Full {
		t.Fatalf("a config change planned a DELTA (%d attempt(s)): %s", len(plan.Attempt), plan.Reason)
	}
	// The control's control: same store, same config, no full pass. Without this
	// the test above passes for a planner that always returns Full.
	if p := planProofScanFrom(st, markerAsIs(st), true); p.Full {
		t.Fatalf("an unchanged config also forced a full pass — the config comparison is not discriminating: %s", p.Reason)
	}
}

// A legacy PLAIN-DIGEST marker (and any unreadable or wrong-version one) must force
// a full pass: its bytes carry no per-hash state to diff, so a delta computed from
// it would silently be a delta against nothing.
func TestLegacyMarkerForcesFullPass(t *testing.T) {
	st := newStore(t)
	put(t, st, dblSrc)
	if err := st.be.putMeta(proofFixpointKey, []byte(proofStateFingerprint(st))); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadProofScanMarker(st); ok {
		t.Fatal("a plain-digest marker parsed as a versioned one")
	}
	if plan := planProofScan(st); !plan.Full {
		t.Fatalf("legacy marker planned a delta: %s", plan.Reason)
	}
	// But the OUTER gate must still honour it: a store settled under the old marker
	// is still settled, and forcing one full pass per deployment purely to re-record
	// the marker would re-burn the whole heavy tail for nothing.
	got, ok := proofScanMarkerDigest(st)
	if !ok || got != proofStateFingerprint(st) {
		t.Fatalf("legacy digest not honoured by the outer gate: got=%q ok=%v", got, ok)
	}
	// A wrong-version marker is the same case.
	b, _ := json.Marshal(proofScanMarker{Version: proofScanMarkerVersion + 1, Config: proveConfigLine(),
		Proven: provenSnapshot(st), Digest: proofStateFingerprint(st)})
	if err := st.be.putMeta(proofFixpointKey, b); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadProofScanMarker(st); ok {
		t.Fatal("a future-version marker was accepted for diffing")
	}
	if plan := planProofScan(st); !plan.Full {
		t.Fatalf("future-version marker planned a delta: %s", plan.Reason)
	}
}

// Proven-property GROWTH seeds the closure, not just new hashes. This is the
// enabling direction §7.2 describes: X gains a proven property, so every definition
// whose transitive dependency closure contains X gains a candidate lemma. Set
// growth, not length growth — a length test would miss a set that changed
// size-neutrally.
func TestDeltaPlanProvenGrowthSeedsDependents(t *testing.T) {
	st := openCorpus(t)
	// A live function with at least one proven property, chosen from the corpus.
	var seed string
	names := st.Names()
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		h := names[n]
		m, err := st.GetMeta(h)
		if err != nil || len(m.ProvenProps) == 0 {
			continue
		}
		if len(expectedDependents(t, st, h, true)) > 1 {
			seed = h
			break
		}
	}
	if seed == "" {
		t.Skip("no proven definition with dependents in the committed corpus")
	}
	m := markerAsIs(st)
	m.Proven[seed] = nil // as if it had just gained its proven properties
	plan := planProofScanFrom(st, m, true)
	if plan.Full {
		t.Fatalf("proven growth forced a full pass: %s", plan.Reason)
	}
	want := expectedDependents(t, st, seed, true)
	for h := range want {
		if !plan.Attempt[h] {
			t.Fatalf("%s benefits from the new lemma but is not attempted", shortHash(h))
		}
	}
	if len(plan.Attempt) != len(want) {
		t.Fatalf("attempt set = %d, independently computed closure = %d", len(plan.Attempt), len(want))
	}
	// provenSetMoved is the predicate underneath; assert every direction.
	if !provenSetMoved([]int{0, 2}, []int{0, 1, 2}) {
		t.Fatal("provenSetMoved missed an added index")
	}
	// A SHRINK must seed: the solver is non-monotone in its axiom set (§7.2), so
	// removing a lemma can make a dependent goal newly provable. Seeding on growth
	// alone banked the shrink as settled and skipped that work forever.
	if !provenSetMoved([]int{0, 1, 2}, []int{2, 0}) {
		t.Fatal("provenSetMoved ignored a SHRINKING proven set — a full pass would have retried its dependents")
	}
	if !provenSetMoved([]int{0}, []int{1}) {
		t.Fatal("provenSetMoved missed a size-neutral change")
	}
	if provenSetMoved(nil, nil) {
		t.Fatal("provenSetMoved reported a change for two empty sets")
	}
	if provenSetMoved([]int{2, 0}, []int{0, 2}) {
		t.Fatal("provenSetMoved reported a change for the same set in a different order")
	}
}

// End to end through the store: a completed scan records a versioned marker, and
// the NEXT scan's plan attempts only what changed since. This is the one test that
// exercises the marker WRITE and the marker READ against each other; the planning
// tests above supply their marker directly.
func TestDeltaScanMarkerRoundTrip(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, dblSrc)
	scanBulkProve(st, "test")

	mk, ok := loadProofScanMarker(st)
	if !ok {
		t.Fatal("no versioned marker recorded by a completed scan")
	}
	if mk.Config != proveConfigLine() {
		t.Fatalf("marker config = %q, want %q", mk.Config, proveConfigLine())
	}
	if mk.Digest != proofStateFingerprint(st) {
		t.Fatal("marker digest doesn't match the post-scan proof state — a re-scan would re-burn")
	}
	for _, h := range st.AllHashes() {
		if _, ok := mk.Proven[h]; !ok {
			t.Fatalf("marker snapshot omits %s — the next scan would read it as NEW", shortHash(h))
		}
	}
	if plan := planProofScan(st); plan.Full || len(plan.Attempt) != 0 {
		t.Fatalf("settled store plans full=%v attempt=%d, want a no-op: %s", plan.Full, len(plan.Attempt), plan.Reason)
	}

	// Publish an independent definition: only it is attempted next time.
	reps := put(t, st, `(defn trip [] [(x Int)] Int (+ x (+ x x))
		(prop q [(x Int)] (== (trip x) (+ x (+ x x)))))`)
	h := reps[len(reps)-1].Hash
	plan := planProofScan(st)
	if plan.Full {
		t.Fatalf("a new independent definition forced a full pass: %s", plan.Reason)
	}
	if !plan.Attempt[h] {
		t.Fatal("the newly published definition is not in the attempt set")
	}
	dbl, _ := st.Resolve("dbl")
	if plan.Attempt[dbl] {
		t.Fatal("the already-settled `dbl` is re-attempted — the delta is not scoping")
	}
}

// ---------------------------------------------------------------------------
// EXECUTION, not just planning.
//
// The delta is TWO mechanisms — planProofScan decides the re-attempt set, and the
// level loop in scanBulkProve enforces it — and the population tests above measure
// only the first. Deleting the enforcement leaves every one of them green while the
// scan walks the whole corpus, which is exactly the regression the optimization
// exists to prevent. So these tests run the scan and observe which definitions it
// actually attempted.
//
// Proving is stubbed through proveHashFn: the claim is about the ATTEMPT SET, and
// running z3 over 348 objects to measure it would be paying the cost the change
// exists to avoid. The stub records and returns success without touching metadata,
// so the post-scan proof state is unchanged and the marker settles.
//
// The store is a COPY of the committed corpus in a temp dir. scanBulkProve writes —
// the journal, the marker — and the committed store is append-only evidence, not a
// scratch space.

// proveRecorder swaps in a recording stub for proveHashFn and restores it. `fail`
// names hashes the stub should error on; everything else succeeds without changing
// any metadata.
type proveRecorder struct {
	mu       sync.Mutex
	attempts []string
	fail     map[string]bool
}

func (r *proveRecorder) install(t *testing.T) {
	t.Helper()
	prev := proveHashFn
	t.Cleanup(func() { proveHashFn = prev })
	proveHashFn = func(st *Store, h, display string) (string, error) {
		r.mu.Lock()
		r.attempts = append(r.attempts, h)
		shouldFail := r.fail[h]
		r.mu.Unlock()
		if shouldFail {
			return "", fmt.Errorf("injected prove failure for %s", display)
		}
		return "", nil
	}
}

func (r *proveRecorder) set() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]bool, len(r.attempts))
	for _, h := range r.attempts {
		out[h] = true
	}
	return out
}

func (r *proveRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = nil
}

// copyCorpus copies the committed store into a temp dir and opens it read-write.
// scanBulkProve appends to the journal and writes the marker; the committed store
// is append-only evidence and must never be a test's scratch space.
func copyCorpus(t *testing.T) *Store {
	t.Helper()
	src := "../codebase"
	if _, err := os.Stat(filepath.Join(src, "names.json")); err != nil {
		t.Skipf("committed store unavailable: %v", err)
	}
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying the corpus: %v", err)
	}
	be, err := openFSBackend(dst)
	if err != nil {
		t.Fatal(err)
	}
	st, err := newStoreWithBackend(be, dst)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// eligible is the set scanBulkProve would attempt under a FULL pass: functions with
// properties that are neither fully proven nor falsified. Computed here rather than
// read off the implementation, so "the scan attempted exactly the eligible members
// of the plan" is a claim and not a restatement.
func eligible(t *testing.T, st *Store) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, h := range st.AllHashes() {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		m, err := st.GetMeta(h)
		if err != nil || isFullyProven(m, d) || m.Guarantee.Level == "falsified" {
			continue
		}
		out[h] = true
	}
	return out
}

// seedMarker writes a marker describing the store as it stands MINUS `seed`, i.e.
// "seed has just changed". Written into the copy, so the next scan plans a delta.
//
// The Digest is deliberately NOT the current fingerprint. It stands for the digest
// of the previous state, which necessarily differs once a definition has changed —
// and if it did not, scanBulkProve's OUTER gate would declare the store settled and
// return before the plan is ever consulted. Getting this wrong is how the first
// draft of these tests "passed" a scan that attempted nothing at all.
func seedMarker(t *testing.T, st *Store, seed string) {
	t.Helper()
	m := markerAsIs(st)
	delete(m.Proven, seed)
	m.Digest = "digest-of-the-previous-state"
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.be.putMeta(proofFixpointKey, b); err != nil {
		t.Fatal(err)
	}
}

// APPLICATION-CLASS, EXECUTED. A leaf change must make the scan attempt the leaf
// and nothing else — measured at the prove seam, not at the planner.
//
// FAILS UNDER: deleting the `!plan.Attempt[h]` filter from the level loop, which
// every planning test above survives.
func TestDeltaScanApplicationClassAttemptsOnlyTheDelta(t *testing.T) {
	st := copyCorpus(t)
	tail := corpusTail(t, st)
	elig := eligible(t, st)

	var seedName, seedHash string
	names := st.Names()
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		h := names[n]
		if elig[h] && len(expectedDependents(t, st, h, true)) == 1 {
			seedName, seedHash = n, h
			break
		}
	}
	if seedHash == "" {
		t.Skip("no eligible leaf definition in the committed corpus")
	}
	seedMarker(t, st, seedHash)

	rec := &proveRecorder{}
	rec.install(t)
	scanBulkProve(st, "test")

	got := rec.set()
	if !got[seedHash] {
		t.Fatalf("the changed leaf %q was never attempted", seedName)
	}
	var extra []string
	for h := range got {
		if h != seedHash {
			extra = append(extra, shortHash(h))
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		t.Fatalf("a leaf change attempted %d definition(s) besides itself: %v", len(extra), extra)
	}
	// The saving, stated against what a full pass would have done.
	var tailAttempted int
	for h := range got {
		if tail[h] && h != seedHash {
			tailAttempted++
		}
	}
	if tailAttempted > 0 {
		t.Fatalf("%d heavy-tail definitions attempted for a leaf change", tailAttempted)
	}
	if len(elig) <= 1 {
		t.Fatal("the corpus holds no work to avoid — this test cannot discriminate")
	}
	t.Logf("application-class %q: attempted %d of %d eligible definitions (%d in the heavy tail avoided)",
		seedName, len(got), len(elig), len(tail)-1)
}

// FOUNDATION-CLASS, EXECUTED. A `List`-class change legitimately attempts most of
// the corpus — but it must attempt EXACTLY the eligible members of its dependents
// closure, no more, and it must then SETTLE: a completed large delta records a
// marker whose digest matches the post-scan state, so the next scan is a no-op.
// That last half is the failure the terraform comments record twice — a pass that
// commits nothing leaves the fingerprint unsettled and re-attempts the same doomed
// level forever.
//
// FAILS UNDER: deleting the execution filter (definitions outside the closure get
// attempted), or failing to record the marker after a completed pass (the second
// scan re-attempts everything).
func TestDeltaScanFoundationClassAttemptsClosureAndSettles(t *testing.T) {
	st := copyCorpus(t)
	seedHash := resolveOne(t, st, "List")
	elig := eligible(t, st)
	seedMarker(t, st, seedHash)

	want := expectedDependents(t, st, seedHash, true)
	wantEligible := map[string]bool{}
	for h := range want {
		if elig[h] {
			wantEligible[h] = true
		}
	}
	if len(wantEligible) == 0 {
		t.Fatal("List's closure holds no eligible definition — the premise is gone")
	}
	// And the corpus must hold eligible work OUTSIDE the closure, or "scoped" is
	// vacuous and this test would pass with the filter deleted.
	outside := 0
	for h := range elig {
		if !want[h] {
			outside++
		}
	}
	if outside == 0 {
		t.Fatal("no eligible definition outside List's closure — this test cannot discriminate")
	}

	rec := &proveRecorder{}
	rec.install(t)
	scanBulkProve(st, "test")

	got := rec.set()
	for h := range wantEligible {
		if !got[h] {
			t.Fatalf("%s is eligible and in List's closure but was NOT attempted", shortHash(h))
		}
	}
	for h := range got {
		if !want[h] {
			t.Fatalf("%s was attempted but does not transitively depend on List — the execution filter is not enforcing the plan", shortHash(h))
		}
	}
	if len(got) != len(wantEligible) {
		t.Fatalf("attempted %d definitions, eligible closure is %d", len(got), len(wantEligible))
	}

	// IT SETTLES. The stub changed no metadata, so a completed pass must record a
	// marker matching the post-scan state, and the next scan must do nothing.
	mk, ok := loadProofScanMarker(st)
	if !ok {
		t.Fatal("a completed foundation-class delta recorded no versioned marker")
	}
	if mk.Digest != proofStateFingerprint(st) {
		t.Fatal("the recorded digest does not match the post-scan state — the next scan would re-run the whole delta")
	}
	rec.reset()
	scanBulkProve(st, "test")
	if n := len(rec.set()); n != 0 {
		t.Fatalf("the scan after a settled foundation-class delta attempted %d definition(s), want 0", n)
	}
	t.Logf("foundation-class List: attempted %d of %d eligible definitions (%d eligible outside the closure), settled in one pass",
		len(got), len(elig), outside)
}

// EQUIVALENCE — the claim that makes the delta safe rather than merely faster.
//
// A definition can newly prove only if its candidate lemma set grew, and §7.2 draws
// candidates from its TRANSITIVE dependency closure (plus its own banked proven
// properties, the self-lemma fixpoint). So for every definition the delta EXCLUDES,
// every hash in that closure — itself included — must have exactly the proven-property
// set the previous marker recorded. If that holds, the excluded definition faces a
// bit-identical lemma library, the prover is deterministic, and a full pass could not
// have proven anything the delta pass cannot. The two passes have the same possible
// proven set.
//
// This is decidable from the artifact: it compares two recorded sets and does not
// depend on running the prover. It is asserted over the whole committed corpus for
// both a foundation-class and an application-class change.
//
// FAILS UNDER: any attempt set that omits a transitive dependent — the omitted
// definition lands in the excluded set with a dependency whose proven set moved.
func TestDeltaExcludesOnlyDefinitionsWithUnchangedLemmaInput(t *testing.T) {
	st := openCorpus(t)
	for _, seedName := range []string{"List", "length", "Pair"} {
		seedHash, ok := st.Resolve(seedName)
		if !ok {
			continue
		}
		t.Run(seedName, func(t *testing.T) {
			m := markerAsIs(st)
			delete(m.Proven, seedHash)
			plan := planProofScanFrom(st, m, true)
			if plan.Full {
				t.Fatalf("expected a delta, got a full pass: %s", plan.Reason)
			}
			assertLemmaInputUnchanged(t, st, m, plan.Attempt)
		})
	}
	// The application-class end of the same claim: the leaf change, whose excluded
	// set is nearly the entire corpus.
	names := st.Names()
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		h := names[n]
		if len(expectedDependents(t, st, h, true)) != 1 {
			continue
		}
		t.Run("leaf/"+n, func(t *testing.T) {
			m := markerAsIs(st)
			delete(m.Proven, h)
			plan := planProofScanFrom(st, m, true)
			assertLemmaInputUnchanged(t, st, m, plan.Attempt)
		})
		break
	}
}

// assertLemmaInputUnchanged fails the test if checkLemmaInputUnchanged reports a
// violation.
func assertLemmaInputUnchanged(t *testing.T, st *Store, prev *proofScanMarker, attempt map[string]bool) {
	t.Helper()
	if err := checkLemmaInputUnchanged(st, prev, attempt); err != nil {
		t.Fatal(err)
	}
}

// checkLemmaInputUnchanged is the equivalence check itself. It returns an ERROR
// rather than calling t.Fatalf so the mutation control below can drive it with a
// deliberately incomplete attempt set and assert that it complains — a failing
// subtest would mark its parent failed whatever the parent concluded, so an
// expected-failure control cannot be expressed with t.Run.
func checkLemmaInputUnchanged(st *Store, prev *proofScanMarker, attempt map[string]bool) error {
	deps := map[string][]string{}
	for _, h := range st.AllHashes() {
		deps[h] = testLemmaEdges(st, h)
	}
	memo := map[string]map[string]bool{}
	var closure func(string) map[string]bool
	closure = func(h string) map[string]bool {
		if c, ok := memo[h]; ok {
			return c
		}
		out := map[string]bool{h: true}
		memo[h] = out // cycles are impossible (a hash embeds its dependencies); guard anyway
		for _, d := range deps[h] {
			for x := range closure(d) {
				out[x] = true
			}
		}
		return out
	}
	now := provenSnapshot(st)
	excluded := 0
	for _, h := range st.AllHashes() {
		if attempt[h] {
			continue
		}
		excluded++
		for x := range closure(h) {
			was, known := prev.Proven[x]
			if !known {
				return fmt.Errorf("excluded %s draws lemmas from %s, which the previous marker never saw — its lemma input changed",
					shortHash(h), shortHash(x))
			}
			if !sameIntSet(was, now[x]) {
				return fmt.Errorf("excluded %s draws lemmas from %s, whose proven set moved %v -> %v — a full pass could prove something this delta cannot",
					shortHash(h), shortHash(x), was, now[x])
			}
		}
	}
	if excluded == 0 {
		return fmt.Errorf("nothing was excluded — the equivalence claim is vacuous here")
	}
	return nil
}

func sameIntSet(a, b []int) bool {
	return !provenSetMoved(a, b)
}

// THE OMITTED-DEPENDENT CONTROL for the equivalence check. Drop one transitive
// dependent from the attempt set and the check must catch it: that definition is now
// excluded while a hash in its dependency closure has moved. Without this the
// equivalence test could be passing vacuously.
func TestEquivalenceCatchesAnOmittedTransitiveDependent(t *testing.T) {
	st := openCorpus(t)
	seedHash := resolveOne(t, st, "List")
	m := markerAsIs(st)
	delete(m.Proven, seedHash)
	plan := planProofScanFrom(st, m, true)

	// Pick a member that depends on List only through an intermediary — the exact
	// class a direct-only closure would drop.
	direct := expectedDependents(t, st, seedHash, false)
	var victim string
	for h := range plan.Attempt {
		if !direct[h] && h != seedHash {
			if victim == "" || h < victim {
				victim = h
			}
		}
	}
	if victim == "" {
		t.Skip("no transitively-but-not-directly dependent definition to omit")
	}
	broken := map[string]bool{}
	for h := range plan.Attempt {
		broken[h] = true
	}
	delete(broken, victim)

	if err := checkLemmaInputUnchanged(st, m, broken); err == nil {
		t.Fatalf("omitting %s from the attempt set did NOT fail the equivalence check — the check is vacuous",
			shortHash(victim))
	} else {
		t.Logf("omitting %s is caught: %v", shortHash(victim), err)
	}
	// And the control's control: the intact set passes, so the failure above is
	// attributable to the omission and not to a check that always complains.
	if err := checkLemmaInputUnchanged(st, m, plan.Attempt); err != nil {
		t.Fatalf("the intact attempt set failed the equivalence check: %v", err)
	}
}

// A scan that FAILS must leave the PREVIOUS marker byte-for-byte alone (#181,
// preserved through the format change), and the failed definition must still be in
// the next scan's plan. Recording a marker after a failure would let the next scan
// see a settled state and skip the failed definition forever — the fail-open
// direction, and the one this design must not have.
//
// The failure is INJECTED at the prove seam. The previous version of this test
// induced no failure at all and asserted that a marker WAS written, which is the
// opposite of the behaviour under test.
func TestFailedScanLeavesTheMarkerAndRetries(t *testing.T) {
	st := copyCorpus(t)
	seedHash := resolveOne(t, st, "List")
	seedMarker(t, st, seedHash)
	before, ok, err := st.be.getMeta(proofFixpointKey)
	if err != nil || !ok {
		t.Fatalf("no marker to preserve: ok=%v err=%v", ok, err)
	}
	before = append([]byte(nil), before...)

	planBefore := planProofScan(st)
	if planBefore.Full {
		t.Fatalf("expected a delta pass: %s", planBefore.Reason)
	}
	// Fail on one eligible member of the delta.
	elig := eligible(t, st)
	var victim string
	for h := range planBefore.Attempt {
		if elig[h] && (victim == "" || h < victim) {
			victim = h
		}
	}
	if victim == "" {
		t.Skip("no eligible definition in the delta to fail")
	}

	rec := &proveRecorder{fail: map[string]bool{victim: true}}
	rec.install(t)
	scanBulkProve(st, "test")

	if !rec.set()[victim] {
		t.Fatal("the failing definition was never attempted")
	}
	after, ok, err := st.be.getMeta(proofFixpointKey)
	if err != nil || !ok {
		t.Fatalf("the marker vanished after a failed scan: ok=%v err=%v", ok, err)
	}
	if string(after) != string(before) {
		t.Fatal("a FAILED scan rewrote the marker — the failed definition would never be retried")
	}
	// RETRIED: the next plan still contains it.
	planAfter := planProofScan(st)
	if planAfter.Full {
		t.Fatalf("unexpected full pass after a failed delta: %s", planAfter.Reason)
	}
	if !planAfter.Attempt[victim] {
		t.Fatalf("%s failed and is NOT in the next scan's plan", shortHash(victim))
	}
	if len(planAfter.Attempt) != len(planBefore.Attempt) {
		t.Fatalf("the plan changed after a failed scan: %d -> %d", len(planBefore.Attempt), len(planAfter.Attempt))
	}
}

// TestHintedConsumerIsSeededWhenItsHintTargetNewlyProves is the #140 delta's
// hint edge, witnessed on the committed corpus rather than argued.
//
// THE DEFECT IT PINS. `loadLemmaLibrary` admits an author hint (#67) whose
// target is currently PROVEN, and does so precisely for targets OUTSIDE the
// goal's dependency closure. So when an inert hint's target newly proves, the
// hinted definition's candidate lemma set GROWS with no AST edge to carry that
// fact. A delta graph built from dependencies alone never seeds it, excludes it,
// and then records the state as settled — after which no scan retries it. The
// failure is silent and permanent, in the one direction a delta pass must not
// have, and a full pass does not share it.
//
// WHY IT DISCRIMINATES. The subtest asserts the CONTROL first: the target must
// not be in the hinter's transitive dependency closure. Without that, a hinter
// that also happens to depend on its target would be seeded by the plain
// dependency edge and the test would pass while the hint edge was missing.
// Deleting the Hints branch of lemmaInputHashes must fail this test.
func TestHintedConsumerIsSeededWhenItsHintTargetNewlyProves(t *testing.T) {
	st := openCorpus(t)
	name := map[string]string{}
	for n, h := range st.Names() {
		name[h] = n
	}
	depClosure := func(h string) map[string]bool {
		out := map[string]bool{}
		var walk func(string)
		walk = func(x string) {
			d, err := st.GetDef(x)
			if err != nil {
				return
			}
			for _, dep := range sortedDepHashes(d) {
				if !out[dep] {
					out[dep] = true
					walk(dep)
				}
			}
		}
		walk(h)
		return out
	}

	type pair struct{ hinter, target string }
	var pairs []pair
	for _, h := range st.AllHashes() {
		m, err := st.GetMeta(h)
		if err != nil || len(m.Hints) == 0 {
			continue
		}
		clo := depClosure(h)
		for _, refs := range m.Hints {
			for _, r := range refs {
				if r.Def == h || clo[r.Def] {
					continue // in-closure: the plain dependency edge already carries it
				}
				pairs = append(pairs, pair{h, r.Def})
			}
		}
	}
	if len(pairs) == 0 {
		t.Skip("no out-of-closure hint in the committed corpus — nothing to witness here")
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].hinter != pairs[j].hinter {
			return pairs[i].hinter < pairs[j].hinter
		}
		return pairs[i].target < pairs[j].target
	})

	seen := map[string]bool{}
	for _, p := range pairs {
		key := p.hinter + "/" + p.target
		if seen[key] {
			continue
		}
		seen[key] = true
		label := name[p.hinter] + "-hints-" + name[p.target]
		t.Run(label, func(t *testing.T) {
			// CONTROL: the hint target is genuinely outside the dependency closure,
			// so only a hint edge can carry it.
			if depClosure(p.hinter)[p.target] {
				t.Fatalf("%s is in %s's dependency closure — this case cannot witness the hint edge",
					shortHash(p.target), shortHash(p.hinter))
			}
			// The previous marker saw the target with one FEWER proven property, i.e.
			// this pass is the one in which the hint stops being inert.
			m := markerAsIs(st)
			was, ok := m.Proven[p.target]
			if !ok || len(was) == 0 {
				t.Skipf("hint target %s has no proven property to have newly gained", shortHash(p.target))
			}
			m.Proven[p.target] = was[:len(was)-1]

			plan := planProofScanFrom(st, m, true)
			if plan.Full {
				t.Fatalf("expected a delta, got a full pass: %s", plan.Reason)
			}
			if !plan.Attempt[p.target] {
				t.Fatalf("the newly-proven hint target %s was not itself seeded", shortHash(p.target))
			}
			if !plan.Attempt[p.hinter] {
				t.Fatalf("%s (%s) hints at %s (%s), whose proven set just grew, but the delta does NOT attempt it — "+
					"the hinted lemma is admitted from outside the dependency closure, so no dependency edge carries this and "+
					"the marker would record the state as settled with the proof permanently missed",
					shortHash(p.hinter), name[p.hinter], shortHash(p.target), name[p.target])
			}
			// And the equivalence check must AGREE, on the same scenario. It reads
			// testLemmaEdges, so it sees the hint edge independently of the planner:
			// with the planner's hint branch deleted this complains too, which is what
			// stops the check from going green over the defect it exists to catch.
			assertLemmaInputUnchanged(t, st, m, plan.Attempt)
		})
	}
}

// TestDeltaPassReachesTheSameProvenSetAsAFullPass is the #140 equivalence claim
// measured rather than argued.
//
// WHAT WAS MISSING. Every other equivalence test here is STRUCTURAL: it shows
// that excluded definitions face an identical lemma library and then leans on the
// kernel's determinism to conclude the proven sets must match. That is a sound
// argument and it is not a measurement — it never runs a full pass, never runs a
// delta pass, and never compares what they actually proved. So it cannot witness
// a defect in the delta's own reasoning, which is exactly the class the hint edge
// turned out to be (see TestHintedConsumerIsSeededWhenItsHintTargetNewlyProves).
//
// This runs BOTH passes with the REAL prover over two independently built stores
// and diffs the resulting proven sets. It uses a small arithmetic chain rather
// than the committed corpus for one reason: a full pass over the corpus burns the
// heavy tail, which is the cost #140 exists to avoid and would make this test
// unrunnable. So the UNIVERSE here is a small store, stated plainly.
//
// WHAT IT ESTABLISHES: for a NEW-DEFINITION delta, a full pass and a delta pass
// reach byte-identical proven sets while the delta demonstrably skips work — the
// never-proving `shrink` is attempted by one and not the other. That is the
// equivalence claim measured end to end with real z3, once.
//
// WHAT IT DOES NOT: it does not cover the TRANSITIVE-UNLOCK scenario, where an
// existing definition newly proves and a dependent then becomes provable because
// of it. That path is still argued structurally rather than measured, and the
// dependency graph here is too shallow to exercise it. It also says nothing about
// corpus-scale graphs. Both remain covered by the structural checks above, which
// is a weaker kind of evidence and is why this test exists at all.
func TestDeltaPassReachesTheSameProvenSetAsAFullPass(t *testing.T) {
	requireZ3(t)
	base := []string{
		`(defn dbl [] [(n Int)] Int (* 2 n)
		   (prop dbl-nonneg [(n Int)] (if (<= 0 n) (<= 0 (dbl n)) true)))`,
		`(defn quad [] [(n Int)] Int (dbl (dbl n))
		   (prop quad-is-4n [(n Int)] (== (quad n) (* 4 n))))`,
		// The heavy-tail analogue, and the reason this comparison is not vacuous.
		// Its property is FALSE for large x but survives 200 random cases, so the
		// definition stays `tested` rather than `falsified` — it remains eligible
		// forever and never proves, exactly like the corpus tail a full pass
		// re-attempts every time. It depends on nothing in the chain below, so a
		// correct delta must SKIP it while a full pass must attempt it.
		`(defn shrink [] [(x Int)] Int (if (<= 0 x) x (- 0 x))
		   (prop bounded-wrongly [(x Int)] (<= (shrink x) 400)))`,
	}
	// The change both passes must react to: a new definition on top of the chain.
	change := `(defn oct [] [(n Int)] Int (dbl (quad n))
		   (prop oct-is-8n [(n Int)] (== (oct n) (* 8 n))))`

	build := func() *Store {
		st := newStore(t)
		for _, src := range base {
			put(t, st, src)
		}
		return st
	}
	stFull, stDelta := build(), build()

	// Settle both to the same state, so the only difference below is WHICH pass runs.
	scanBulkProve(stFull, "test")
	scanBulkProve(stDelta, "test")
	if !sameProvenSnapshot(provenSnapshot(stFull), provenSnapshot(stDelta)) {
		t.Fatal("the two stores did not settle identically — content addressing should make them equal, so this test's premise is broken")
	}
	// The legacy digest is captured BEFORE the change, which is what a real
	// legacy-format marker holds: the digest of the last state that settled. Taking
	// it AFTER would make the OUTER digest gate short-circuit ("proof state
	// unchanged") and the full pass would never run at all — the first draft of this
	// test did exactly that and compared a real delta against a pass that did
	// nothing, which is why the assertion below checks that the full pass PROVED
	// the change rather than only that it PLANNED to.
	legacyDigest := proofStateFingerprint(stFull)

	put(t, stFull, change)
	put(t, stDelta, change)

	// stFull is forced onto the FULL path by a legacy-form marker, which
	// loadProofScanMarker rejects. That is the real production trigger for a full
	// pass, not a test-only backdoor.
	if err := stFull.be.putMeta(proofFixpointKey, []byte(legacyDigest)); err != nil {
		t.Fatalf("writing a legacy marker: %v", err)
	}
	planFull := planProofScan(stFull)
	planDelta := planProofScan(stDelta)

	// CONTROLS. Without these the two passes could be doing the same thing and the
	// equality below would hold vacuously.
	if !planFull.Full {
		t.Fatalf("the full-path store did not plan a full pass: %s", planFull.Reason)
	}
	if planDelta.Full {
		t.Fatalf("the delta-path store planned a FULL pass, so nothing is being compared: %s", planDelta.Reason)
	}
	eligibleFull := eligible(t, stFull)
	if len(planDelta.Attempt) >= len(eligibleFull) {
		t.Fatalf("the delta attempts %d of %d eligible definitions — it skipped nothing, so this comparison is vacuous",
			len(planDelta.Attempt), len(eligibleFull))
	}
	// Specifically: the never-proving definition must be attempted by the full pass
	// and skipped by the delta. Without this the delta could be skipping something
	// incidental and the saving would be unmeasured.
	tailHash, ok := stFull.Resolve("shrink")
	if !ok {
		t.Fatal("the tail definition is not resolvable")
	}
	if !eligibleFull[tailHash] {
		t.Fatal("the tail definition is not eligible — it must have proven, so it no longer models the tail")
	}
	if planDelta.Attempt[tailHash] {
		t.Fatal("the delta attempts the never-proving tail definition, which nothing it changed can affect")
	}

	scanBulkProve(stFull, "test")
	scanBulkProve(stDelta, "test")

	full, delta := provenSnapshot(stFull), provenSnapshot(stDelta)
	if !sameProvenSnapshot(full, delta) {
		for h, fp := range full {
			dp, ok := delta[h]
			if !ok || !sameIntSet(fp, dp) {
				t.Errorf("%s: full pass proved %v, delta pass proved %v", shortHash(h), fp, dp)
			}
		}
		t.Fatal("the delta pass did NOT reach the same proven set as the full pass")
	}
	// And the change itself must actually have been proven by both, or the stores
	// agree only because neither proved anything new.
	newHash, ok := stDelta.Resolve("oct")
	if !ok {
		t.Fatal("the new definition is not resolvable")
	}
	if len(delta[newHash]) == 0 {
		t.Fatal("the delta pass proved nothing new — the comparison is between two empty results")
	}
	// And the FULL pass must have actually run. Planning a full pass is not running
	// one: the outer digest gate can still short-circuit before the plan is used,
	// in which case an empty full-pass result would agree with a delta that also
	// proved nothing and the test would pass having compared nothing.
	if len(full[newHash]) == 0 {
		t.Fatal("the full pass proved nothing for the change — it never ran, so nothing was compared")
	}
	t.Logf("full and delta agree on %d objects; delta attempted %d of %d eligible",
		len(full), len(planDelta.Attempt), len(eligibleFull))
}

// sameProvenSnapshot compares two hash -> proven-property-set maps.
func sameProvenSnapshot(a, b map[string][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for h, av := range a {
		bv, ok := b[h]
		if !ok || !sameIntSet(av, bv) {
			return false
		}
	}
	return true
}

// TestConcurrentPublicationIsNotRecordedAsScanned pins the marker's membership
// rule against a real race in the deployed worker.
//
// THE RACE. `scanBulkProve` plans, then walks dependency levels, then records the
// marker. A definition published by another process AFTER the plan is not
// attempted by either path — the delta filter skips it because it is not in
// Attempt, and a full pass skips it because the levels were already computed. If
// the marker is then recorded over the LIVE store, that definition's empty proven
// set is banked as though it had been scanned. The next scan sees a KNOWN hash
// rather than a new one, so it is never seeded; and because the digest also
// covered it, the outer gate reports "proof state unchanged". The definition is
// silently never proven, permanently.
//
// The registry worker runs against a store that accepts concurrent puts, so this
// is a live window, not a theoretical one.
//
// WHAT MAKES IT DISCRIMINATE: the assertions are about the definition being
// ABSENT from the marker and PRESENT in the next plan. Recording over the live
// store (the pre-fix behaviour) puts it in the marker and out of the next plan,
// failing both.
func TestConcurrentPublicationIsNotRecordedAsScanned(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn twice [] [(n Int)] Int (* 2 n)
	   (prop twice-nonneg [(n Int)] (if (<= 0 n) (<= 0 (twice n)) true)))`)

	// The plan is taken here — this is the snapshot the scan will honour.
	plan := planProofScan(st)

	// ...and the race lands here, between planning and recording.
	put(t, st, `(defn thrice [] [(n Int)] Int (* 3 n)
	   (prop thrice-nonneg [(n Int)] (if (<= 0 n) (<= 0 (thrice n)) true)))`)
	raced, ok := st.Resolve("thrice")
	if !ok {
		t.Fatal("the concurrently published definition is not resolvable")
	}
	if plan.Attempt[raced] {
		t.Fatal("the plan already contains the raced definition — it was not published after planning, so this test does not model the race")
	}

	recordProofScanMarker(st, plan, nil)

	marker, ok := loadProofScanMarker(st)
	if !ok {
		t.Fatal("the marker did not round-trip")
	}
	if _, present := marker.Proven[raced]; present {
		t.Fatal("the marker records a definition the scan never attempted — the next scan will treat it as settled and it will never be proven")
	}
	// CONTROL: what WAS in the baseline must still be recorded, or the fix has
	// simply thrown the marker away and every scan becomes a full pass.
	base, ok := st.Resolve("twice")
	if !ok {
		t.Fatal("the baseline definition is not resolvable")
	}
	if _, present := marker.Proven[base]; !present {
		t.Fatal("the marker dropped a definition that WAS in the baseline — the delta would degrade to a full pass every time")
	}

	// And the consequence that matters: the next scan must seed the raced hash.
	next := planProofScan(st)
	if next.Full {
		t.Fatalf("expected a delta on the next scan, got a full pass: %s", next.Reason)
	}
	if !next.Attempt[raced] {
		t.Fatal("the next scan does not attempt the definition published during the last one")
	}
	// The digest must ALSO be stale with respect to the live store, or the outer
	// gate short-circuits before the plan above is ever consulted.
	if marker.Digest == proofStateFingerprint(st) {
		t.Fatal("the recorded digest matches the live store, so the outer gate would report a settled corpus and the raced definition would never be reached")
	}
}

// TestHintOnlyChangeReArmsTheScan pins the third member of the same family: state
// that changes what the prover can do WITHOUT moving a hash or a proven set.
//
// `oath hint` (#67) records an author hint into an object's METADATA. The AST is
// untouched, so the hash does not move; no proof runs, so ProvenProps does not
// move. Before this was tracked, that made a hint a complete no-op for the
// worker: the fingerprint did not change, so the OUTER gate reported a settled
// corpus and returned before planning; and if some unrelated publication happened
// to re-arm the scan, the delta could not seed the definition whose lemma input
// had just changed. The hint would take effect only on a full pass that nothing
// would ever schedule.
//
// This asserts both gates: the fingerprint must move, and the plan must attempt
// the hinted definition.
func TestHintOnlyChangeReArmsTheScan(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn base [] [(n Int)] Int (* 2 n)
	   (prop base-nonneg [(n Int)] (if (<= 0 n) (<= 0 (base n)) true)))`)
	put(t, st, `(defn other [] [(n Int)] Int (+ n 1)
	   (prop other-gt [(n Int)] (< n (other n))))`)
	baseHash, ok := st.Resolve("base")
	if !ok {
		t.Fatal("base is not resolvable")
	}
	otherHash, ok := st.Resolve("other")
	if !ok {
		t.Fatal("other is not resolvable")
	}

	before := proofStateFingerprint(st)
	marker := markerAsIs(st)

	// The hint-only change: `other`'s property 0 may use `base`'s property 0, which
	// is outside its dependency closure — `other` does not mention `base` at all.
	m, err := st.GetMeta(otherHash)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if m.Hints == nil {
		m.Hints = map[int][]HintRef{}
	}
	m.Hints[0] = []HintRef{{Def: baseHash, Prop: 0}}
	if err := st.SetMeta(otherHash, m); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	// GATE 1, the outer digest. If this does not move, scanBulkProve returns before
	// the planner is ever consulted and nothing below can matter.
	if proofStateFingerprint(st) == before {
		t.Fatal("a hint changed nothing in the fingerprint — the outer gate would report a settled corpus and the hint would never take effect")
	}

	// GATE 2, the delta plan.
	plan := planProofScanFrom(st, marker, true)
	if plan.Full {
		t.Fatalf("expected a delta, got a full pass: %s", plan.Reason)
	}
	if !plan.Attempt[otherHash] {
		t.Fatal("the definition whose hint changed is not attempted — its lemma input moved with no hash or proven-set change to carry it")
	}
	// CONTROL: an unrelated definition must NOT be dragged in, or the "delta" is
	// just a full pass wearing a different name.
	if plan.Attempt[baseHash] {
		t.Fatal("the hint TARGET was attempted; only the hinted consumer's lemma input changed, so this delta is over-broad")
	}
}

// TestHintFreeStoreFingerprintsAsItDidBeforeHints protects the legacy migration
// path. `proofScanMarkerDigest` deliberately honours a legacy bare-digest marker
// so an existing deployment does not re-burn the heavy tail purely to re-record
// its marker in the new format. That only works if a store WITHOUT hints
// fingerprints byte-identically to the pre-hint kernel — an unconditional hints
// section would change every digest and quietly defeat it.
//
// The expected value is recomputed here from the OLD format rather than by
// calling the production helper, so this is an independent statement of the
// format rather than a restatement of whatever the code currently does.
func TestHintFreeStoreFingerprintsAsItDidBeforeHints(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn plain [] [(n Int)] Int (+ n 1)
	   (prop plain-gt [(n Int)] (< n (plain n))))`)
	if len(hintSnapshot(st)) != 0 {
		t.Fatal("this store was supposed to have no hints — the premise is gone")
	}

	h := sha256.New()
	fmt.Fprint(h, proveConfigLine())
	for _, hash := range st.AllHashes() {
		m, err := st.GetMeta(hash)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s:%v;", hash, m.ProvenProps)
	}
	legacy := hex.EncodeToString(h.Sum(nil))

	if got := proofStateFingerprint(st); got != legacy {
		t.Fatalf("a hint-free store no longer fingerprints as it did before hints were tracked:\n  now    %s\n  legacy %s\n"+
			"every existing deployment would reject its legacy marker and re-derive the whole corpus", got, legacy)
	}
	// CONTROL: adding a hint MUST change it, or the field is not tracked at all.
	hashes := st.AllHashes()
	m, err := st.GetMeta(hashes[0])
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	m.Hints = map[int][]HintRef{0: {{Def: hashes[0], Prop: 0}}}
	if err := st.SetMeta(hashes[0], m); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if proofStateFingerprint(st) == legacy {
		t.Fatal("adding a hint left the fingerprint unchanged — hints are not tracked")
	}
}

// TestHintedConsumerIsScheduledAfterItsHintTarget pins the SCHEDULING half of the
// hint edge. Planning and execution are two mechanisms, and fixing only the first
// leaves the defect intact: the delta correctly ATTEMPTS a hinted consumer whose
// target just proved, but if `topoFuncLevels` orders by AST dependencies alone the
// consumer can land in the same level as its target and be proven concurrently
// with it — without the lemma. The marker then banks the target's new proof, the
// next scan sees no movement, and the consumer is never retried.
func TestHintedConsumerIsScheduledAfterItsHintTarget(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn giver [] [(n Int)] Int (* 2 n)
	   (prop giver-nonneg [(n Int)] (if (<= 0 n) (<= 0 (giver n)) true)))`)
	put(t, st, `(defn taker [] [(n Int)] Int (+ n 1)
	   (prop taker-gt [(n Int)] (< n (taker n))))`)
	giver, ok := st.Resolve("giver")
	if !ok {
		t.Fatal("giver is not resolvable")
	}
	taker, ok := st.Resolve("taker")
	if !ok {
		t.Fatal("taker is not resolvable")
	}

	levelOf := func() map[string]int {
		out := map[string]int{}
		for lv, hashes := range topoFuncLevels(st) {
			for _, h := range hashes {
				out[h] = lv
			}
		}
		return out
	}
	// CONTROL: with no hint the two are independent, so nothing forces an order.
	// Without this the assertion below could hold for an unrelated reason.
	before := levelOf()
	if before[taker] > before[giver] {
		t.Skip("these definitions are already ordered without a hint — this case cannot witness the hint edge")
	}

	m, err := st.GetMeta(taker)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	m.Hints = map[int][]HintRef{0: {{Def: giver, Prop: 0}}}
	if err := st.SetMeta(taker, m); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	after := levelOf()
	if after[taker] <= after[giver] {
		t.Fatalf("taker (level %d) is not scheduled after its hint target giver (level %d) — "+
			"they can be proven concurrently, so the hinted lemma is not available when the consumer runs",
			after[taker], after[giver])
	}
}

// TestConcurrentProofOnAnUnattemptedHashIsNotBanked is the VALUE half of the
// mid-scan race (TestConcurrentPublicationIsNotRecordedAsScanned is the MEMBERSHIP
// half). Another worker proving something for a hash this pass did not attempt
// must not have that proof banked: the plan excluding its dependents was computed
// before the proof existed, so banking it consumes the movement that would have
// seeded them, and the digest then matches the live store so the outer gate
// reports a settled corpus.
func TestConcurrentProofOnAnUnattemptedHashIsNotBanked(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn alpha [] [(n Int)] Int (+ n 1)
	   (prop alpha-gt [(n Int)] (< n (alpha n))))`)
	put(t, st, `(defn beta [] [(n Int)] Int (* 3 n)
	   (prop beta-nonneg [(n Int)] (if (<= 0 n) (<= 0 (beta n)) true)))`)
	alpha, _ := st.Resolve("alpha")
	beta, _ := st.Resolve("beta")

	// A delta that attempts alpha only.
	marker := markerAsIs(st)
	delete(marker.Proven, alpha)
	plan := planProofScanFrom(st, marker, true)
	if plan.Full {
		t.Fatalf("expected a delta: %s", plan.Reason)
	}
	if plan.Attempt[beta] {
		t.Skip("beta is in the delta — it cannot model an unattempted hash here")
	}

	// ...and beta is proven by someone else while this scan runs.
	m, err := st.GetMeta(beta)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	m.ProvenProps = []int{0}
	if err := st.SetMeta(beta, m); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	recordProofScanMarker(st, plan, nil)
	rec, ok := loadProofScanMarker(st)
	if !ok {
		t.Fatal("the marker did not round-trip")
	}
	if len(rec.Proven[beta]) != 0 {
		t.Fatal("the marker banked a proof landed by another worker on a hash this scan never attempted — its dependents were excluded before that proof existed and will never be retried")
	}
	// The consequence that matters: the next scan must still see beta move.
	next := planProofScanFrom(st, rec, true)
	if next.Full {
		t.Fatalf("expected a delta on the next scan: %s", next.Reason)
	}
	if !next.Attempt[beta] {
		t.Fatal("the next scan does not attempt the hash proven during the last one")
	}
}

// TestHintCycleForcesAFullPass pins the cycle policy. A hint cycle has no
// topological order, so at least one consumer would run before its target and the
// marker would then bank that target's proof, losing the retry. The delta is
// DECLINED rather than scheduled cleverly — a full pass is what existed before
// and it retries everything, which is precisely the guarantee a cycle removes.
func TestHintCycleForcesAFullPass(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn ping [] [(n Int)] Int (+ n 1)
	   (prop ping-gt [(n Int)] (< n (ping n))))`)
	put(t, st, `(defn pong [] [(n Int)] Int (+ n 2)
	   (prop pong-gt [(n Int)] (< n (pong n))))`)
	ping, _ := st.Resolve("ping")
	pong, _ := st.Resolve("pong")

	// CONTROL: without a cycle this scenario plans a delta, so the assertion below
	// is about the cycle and not about the scenario.
	marker := markerAsIs(st)
	delete(marker.Proven, ping)
	if plan := planProofScanFrom(st, marker, true); plan.Full {
		t.Skipf("this scenario already forces a full pass without a cycle: %s", plan.Reason)
	}
	if lemmaInputCycle(st) {
		t.Fatal("the store has a cycle before one was created")
	}

	for _, pair := range [][2]string{{ping, pong}, {pong, ping}} {
		m, err := st.GetMeta(pair[0])
		if err != nil {
			t.Fatalf("GetMeta: %v", err)
		}
		m.Hints = map[int][]HintRef{0: {{Def: pair[1], Prop: 0}}}
		if err := st.SetMeta(pair[0], m); err != nil {
			t.Fatalf("SetMeta: %v", err)
		}
	}
	if !lemmaInputCycle(st) {
		t.Fatal("a mutual hint pair was not detected as a cycle")
	}

	marker2 := markerAsIs(st)
	delete(marker2.Proven, ping)
	plan := planProofScanFrom(st, marker2, true)
	if !plan.Full {
		t.Fatal("a hint cycle did not force a full pass — the delta cannot order the cycle, so a consumer would run without its lemma and the marker would bank the target's proof")
	}
	if !plan.Cyclic {
		t.Fatal("the plan does not mark the scan cyclic, so it would settle afterwards")
	}

	// AND IT MUST NOT SETTLE. A full pass does not repair an unorderable graph:
	// recording the marker banks the hint target's proof, the digest then matches
	// the live store, and the consumer that ran without the lemma is never retried.
	// So a cyclic scan leaves the previous marker in place.
	sentinel := []byte(`{"version":2,"config":"sentinel","proven":{},"digest":"sentinel"}`)
	if err := st.be.putMeta(proofFixpointKey, sentinel); err != nil {
		t.Fatalf("seeding the sentinel marker: %v", err)
	}
	scanBulkProve(st, "test")
	got, _, err := st.be.getMeta(proofFixpointKey)
	if err != nil {
		t.Fatalf("reading the marker back: %v", err)
	}
	if string(got) != string(sentinel) {
		t.Fatal("a cyclic scan recorded a marker — the next scan will see a settled digest and never retry the consumer that ran without its hinted lemma")
	}
}

// TestLateConcurrentProofOnAnAttemptedHashIsNotBanked closes the last window in
// the same family. The earlier two cases cover a hash PUBLISHED mid-scan and a
// hash PROVEN mid-scan that this pass did not attempt. This one is the hash the
// scan DID attempt: if another worker proves it after this scan's dependent
// levels have already run, a final whole-store read would bank that proof as
// though the scan had produced it — and the dependents that ran without the new
// lemma would never be retried, because the digest now matches the live store.
//
// The marker is therefore built from what the scan PRODUCED plus the plan's
// baseline, and never from a fresh read. The scan produced nothing here (the
// prover is stubbed to a no-op), so the baseline must survive intact.
func TestLateConcurrentProofOnAnAttemptedHashIsNotBanked(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn solo [] [(n Int)] Int (+ n 1)
	   (prop solo-gt [(n Int)] (< n (solo n))))`)
	solo, ok := st.Resolve("solo")
	if !ok {
		t.Fatal("solo is not resolvable")
	}

	plan := planProofScan(st)
	if !plan.Full && !plan.Attempt[solo] {
		t.Skip("solo is not attempted by this plan — it cannot model an attempted hash")
	}

	// The race: another worker proves it AFTER this scan's levels have run.
	m, err := st.GetMeta(solo)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	m.ProvenProps = []int{0}
	if err := st.SetMeta(solo, m); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	// This scan produced nothing, so the marker must record the baseline.
	recordProofScanMarker(st, plan, map[string][]int{})
	rec, ok := loadProofScanMarker(st)
	if !ok {
		t.Fatal("the marker did not round-trip")
	}
	if len(rec.Proven[solo]) != 0 {
		t.Fatal("the marker banked a proof this scan did not produce — dependents that ran before it will never be retried")
	}
	// CONTROL: a proof the scan DID produce must be banked, or the marker never
	// advances and every scan repeats the same work forever.
	recordProofScanMarker(st, plan, map[string][]int{solo: {0}})
	rec2, ok := loadProofScanMarker(st)
	if !ok {
		t.Fatal("the marker did not round-trip")
	}
	if len(rec2.Proven[solo]) != 1 {
		t.Fatal("the marker dropped a proof this scan DID produce — the delta would never settle")
	}
}

// TestIncompleteGraphReadRefusesToSettle pins the second cause of NoSettle.
//
// `lemmaInputHashes` reads a definition and its metadata to build the
// lemma-input graph. A FAILED read is not "this definition has no dependencies",
// but that is what dropping the error silently means — and the consequence is the
// worst kind: the closure excludes a real beneficiary, the scan settles, and
// because definition BYTES are immutable and never enter the fingerprint, a later
// successful read does not re-arm anything. The definition is simply never
// attempted again.
//
// So an incomplete read makes the plan refuse to record a marker, exactly as a
// hint cycle does. Repeating a pass is recoverable; banking a false "settled" is
// not.
func TestIncompleteGraphReadRefusesToSettle(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn head [] [(n Int)] Int (+ n 1)
	   (prop head-gt [(n Int)] (< n (head n))))`)
	put(t, st, `(defn tail [] [(n Int)] Int (head (head n))
	   (prop tail-gt [(n Int)] (< n (tail n))))`)
	head, ok := st.Resolve("head")
	if !ok {
		t.Fatal("head is not resolvable")
	}

	// CONTROL: intact, this plans a delta and IS allowed to settle.
	marker := markerAsIs(st)
	delete(marker.Proven, head)
	if plan := planProofScanFrom(st, marker, true); plan.NoSettle {
		t.Fatalf("an intact store refuses to settle (%s) — the assertion below would hold for the wrong reason", plan.NoSettleReason)
	}

	// Now break one object read, as a remote or partially-available backend would.
	if err := os.Remove(filepath.Join(st.Root, "objects", head+".bin")); err != nil {
		t.Fatalf("removing an object: %v", err)
	}
	st2, err := OpenStore(st.Root) // fresh store: no cached definition
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	if _, err := st2.GetDef(head); err == nil {
		t.Skip("the definition is still readable — this backend does not model the failure")
	}

	marker2 := markerAsIs(st2)
	delete(marker2.Proven, head)
	plan := planProofScanFrom(st2, marker2, true)
	if !plan.NoSettle {
		t.Fatal("a plan built from an incomplete graph read is allowed to settle — a dropped edge can exclude a beneficiary, and immutable definition bytes never re-arm the scan")
	}
	if plan.NoSettleReason == "" {
		t.Fatal("NoSettle is set with no reason, so the operator log line says nothing")
	}
}

// TestScanProducedShrinkageIsBanked pins the other half of "bank what the scan
// produced". A proof attempt can leave a proven set that is smaller or the same
// size but different — the solver is non-monotone in its axiom set — and that is
// still this scan's own work. Recording the baseline instead leaves the marker's
// digest stale the moment it is written, so the next scheduled scan re-derives
// the same dependents closure for nothing, which is the cost this whole change
// exists to avoid.
func TestScanProducedShrinkageIsBanked(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn lone [] [(n Int)] Int (+ n 1)
	   (prop lone-gt [(n Int)] (< n (lone n))))`)
	lone, ok := st.Resolve("lone")
	if !ok {
		t.Fatal("lone is not resolvable")
	}
	m, err := st.GetMeta(lone)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	m.ProvenProps = []int{0}
	if err := st.SetMeta(lone, m); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	plan := planProofScan(st)
	// The scan produced a SHRUNK set for this hash.
	recordProofScanMarker(st, plan, map[string][]int{lone: {}})
	rec, ok := loadProofScanMarker(st)
	if !ok {
		t.Fatal("the marker did not round-trip")
	}
	if len(rec.Proven[lone]) != 0 {
		t.Fatalf("the marker banked %v instead of the shrunk set this scan produced — its digest is stale on write and the next scan repeats the work",
			rec.Proven[lone])
	}
}

// TestFailedObjectListingRefusesToSettle is the third cause of NoSettle, and the
// most deceptive: `Store.AllHashes` reports a failed enumeration as an EMPTY
// store. So a transient listing failure — a network blip on the cloud backend —
// does not look like an error to anything downstream, it looks like a corpus with
// nothing in it. The closure then comes back as "just the seeds, and complete",
// and settling on that records a moved proof state with every dependent omitted.
//
// TWO GUARDS CATCH THIS AND EITHER ALONE SUFFICES, which was measured rather than
// assumed: removing the plan-level listing check leaves the test passing (the
// closure's own check fires), removing the closure's check leaves it passing (the
// plan-level one fires), and only removing BOTH fails it. Neither is dead code —
// they cover different paths, since a plan that returns early (no marker, changed
// config) never reaches the closure at all.
func TestFailedObjectListingRefusesToSettle(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn only [] [(n Int)] Int (+ n 1)
	   (prop only-gt [(n Int)] (< n (only n))))`)
	only, ok := st.Resolve("only")
	if !ok {
		t.Fatal("only is not resolvable")
	}

	// CONTROL: with the listing intact this plans a delta and may settle.
	marker := markerAsIs(st)
	delete(marker.Proven, only)
	if plan := planProofScanFrom(st, marker, true); plan.NoSettle {
		t.Fatalf("an intact store refuses to settle (%s) — the assertion below would hold for the wrong reason", plan.NoSettleReason)
	}

	objects := filepath.Join(st.Root, "objects")
	if err := os.Chmod(objects, 0o000); err != nil {
		t.Skipf("cannot make the objects directory unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(objects, 0o755) })
	st2, err := OpenStore(st.Root)
	if err != nil {
		t.Skipf("the store cannot be reopened at all: %v", err)
	}
	if _, err := st2.be.listObjects(); err == nil {
		t.Skip("the listing still succeeds — this platform does not model the failure")
	}

	plan := planProofScanFrom(st2, markerAsIs(st2), true)
	if !plan.NoSettle {
		t.Fatal("a plan built while the object listing was failing is allowed to settle — an empty listing is indistinguishable from an empty store, so the delta would omit every dependent and record the result as settled")
	}
}
