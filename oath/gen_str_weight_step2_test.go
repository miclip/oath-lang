package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// #162 STEP 2 — BLAST RADIUS, DIVERSITY, AND STABILITY OF THE RESOLVED DESIGN.
//
// Step 1 answered falsifier 1 at two sites: weighted generation detects both
// `config.oath` defects inside the real 200-case schedule. That says nothing
// about what it costs everywhere else, which is falsifier 2 (diversity
// regression) and falsifier 3 (branch relevance must be stable). Step 2
// measures those, over the whole corpus, under the RESOLVED design —
// `strWeightsForOwner`: transitive closure, every Str value weighted, off
// entirely for owners whose closure carries no literals.
//
// EVERY POPULATION AND BUDGET IS NAMED WHERE IT IS USED, because a blast-radius
// number is unreadable without knowing what it ranged over. The four:
//
//	CORPUS       unique OBJECTS reached by a live name in the copied store,
//	             K=func, at least one property. Objects, not names: a verdict
//	             is a fact about a hash, and names alias.
//	UNRELATED    the subset of CORPUS whose transitive closure yields ZERO
//	             literals — the conservative "branches on nothing we weight"
//	             population, where the design must be a no-op.
//	MUTANTS      every mutant `genMutants` produces for every CORPUS object.
//	             Generated ONCE and scored by both arms, so the two campaigns
//	             differ in the generator and in nothing else.
//	CHAIN        a three-definition scratch corpus, built in a temp store, for
//	             the condition-3 controls. Three links so the literal sits two
//	             hops from the owner.
//
// STORES ARE ALWAYS COPIES. Nothing here writes, but `oath fixtures` and
// `make verify` have both taught this repository that "this only reads" is a
// claim about the code as currently written.

// step2Store copies the committed store into a temp directory and opens the
// copy. TestStep2StoreCopyIsFaithful is what makes the copy usable as evidence.
func step2Store(t *testing.T) (*Store, string) {
	t.Helper()
	dst := t.TempDir()
	if err := copyTree("../codebase", dst); err != nil {
		t.Fatalf("copying the committed store: %v", err)
	}
	be, err := openFSBackend(dst)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	st, err := newStoreWithBackend(be, dst)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	return st, dst
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
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
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// step2Corpus is the CORPUS population: unique objects reached by a live name,
// K=func, with at least one property, in a deterministic order.
//
// PER-OBJECT, NOT PER-NAME. `Interval`/`Run` and `rot`/`rot-f` each resolve to
// one object today, so a per-name walk would score those twice and report a
// population larger than the set of things that exist.
func step2Corpus(t *testing.T, st *Store) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	for _, h := range st.Names() {
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		out = append(out, h)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("the CORPUS population is empty; every measurement below would be vacuous")
	}
	return out
}

func TestStep2StoreCopyIsFaithful(t *testing.T) {
	st, dir := step2Store(t)
	live := openCommittedStore(t)
	corpus := step2Corpus(t, st)
	if len(corpus) != len(step2Corpus(t, live)) {
		t.Fatalf("the copy at %s holds a different CORPUS than the committed store", dir)
	}
	for _, h := range corpus {
		a, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("copy is missing %s: %v", shortHash(h), err)
		}
		b, err := live.GetDef(h)
		if err != nil {
			t.Fatalf("committed store is missing %s: %v", shortHash(h), err)
		}
		if hashDef(a) != hashDef(b) {
			t.Fatalf("%s differs between the copy and the committed store", shortHash(h))
		}
	}
	t.Logf("CORPUS: %d unique live func objects with properties, copy verified against the committed store", len(corpus))
}

// TestStep2CanonicalStrHashMatchesTheCorpus pins the derived Str identity
// against the store's own binding. Extraction no longer consults the name — the
// whole point of the change — so without this the derivation could quietly name
// a datatype nothing in the corpus uses, and every weighted run would silently
// become an unweighted one.
func TestStep2CanonicalStrHashMatchesTheCorpus(t *testing.T) {
	st, _ := step2Store(t)
	byName, ok := st.Resolve("Str")
	if !ok {
		t.Fatal("Str is not bound in the copied store")
	}
	if got := canonicalStrHash(); got != byName {
		t.Fatalf("the derived Str identity %s is not what the corpus binds as Str (%s); extraction is "+
			"weighting a datatype the corpus does not use", shortHash(got), shortHash(byName))
	}
	// CONTROL: the derivation must be sensitive to the declaration it encodes,
	// or "it matches" would be a fact about hashDef rather than about Str.
	wrong := hashDef(&Def{K: "data", Ctors: [][]Ty{{}, {{K: "rat"}, {K: "rec"}}}})
	if wrong == byName {
		t.Fatal("a Str-shaped declaration over Rat hashes the same as Str; the derivation is not " +
			"discriminating and the match above is meaningless")
	}
}

// ---------------------------------------------------------------------------
// FALSIFIER 2, first half: verdict transitions across the whole CORPUS.

type step2Transition struct {
	object, prop string
	from, to     PropOutcome
}

func TestStep2CorpusVerdictTransitions(t *testing.T) {
	st, _ := step2Store(t)
	corpus := step2Corpus(t, st)

	start := time.Now()
	var moved []step2Transition
	counts := map[string]int{}
	props, weightedObjects, unweightedObjects := 0, 0, 0

	for _, h := range corpus {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", shortHash(h), err)
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("%s metadata: %v", shortHash(h), err)
		}
		w, err := strWeightsForOwner(st, h)
		if err != nil {
			t.Fatalf("%s population: %v", shortHash(h), err)
		}
		if w == nil {
			unweightedObjects++
		} else {
			weightedObjects++
		}
		base := caseSeedBase(h)
		for pi := range d.Props {
			props++
			name := metaPropName(m, pi)
			b := runProp(st, h, &d.Props[pi], name, base, pi, propCases, propFuel).Outcome
			a := runPropWeighted(st, h, &d.Props[pi], name, base, pi, propCases, propFuel, w).Outcome
			counts[fmt.Sprintf("%s -> %s", b, a)]++
			if a != b {
				moved = append(moved, step2Transition{shortHash(h), name, b, a})
			}
		}
	}

	t.Logf("CORPUS SWEEP: %d objects (%d weighted, %d unweighted), %d properties, "+
		"budget %d cases / %d fuel per arm, %s",
		len(corpus), weightedObjects, unweightedObjects, props, propCases, propFuel, time.Since(start).Round(time.Millisecond))
	for _, k := range sortedKeys(counts) {
		t.Logf("  %-32s %d", k, counts[k])
	}
	if len(moved) == 0 {
		t.Logf("  NO VERDICT MOVED across the whole CORPUS")
	}
	for _, tr := range moved {
		t.Logf("  MOVED %s.%s: %s -> %s", tr.object, tr.prop, tr.from, tr.to)
	}

	// CONTROL: the sweep must be capable of observing a move at all. Without
	// this, "no verdict moved" is equally consistent with the weighting being
	// wired to nothing — which is exactly what a nil population everywhere
	// would look like.
	if weightedObjects == 0 {
		t.Fatal("no CORPUS object drew a non-empty literal population, so the weighted arm IS the " +
			"baseline arm and this sweep measures nothing")
	}
	if err := step2SweepDiscriminates(t, st); err != nil {
		t.Fatalf("the sweep cannot observe a verdict move even where one exists: %v", err)
	}
}

// step2SweepDiscriminates checks that the comparison used by the sweep reports
// a difference where one is known to exist — Step 1's pre-repair `finds-head`,
// which passes unweighted and falsifies weighted.
func step2SweepDiscriminates(t *testing.T, st *Store) error {
	t.Helper()
	h := protoFindsHeadHash
	d, err := st.GetDef(h)
	if err != nil {
		return err
	}
	pi := 1
	w, err := strWeightsForOwner(st, h)
	if err != nil {
		return err
	}
	base := caseSeedBase(h)
	b := runProp(st, h, &d.Props[pi], "finds-head", base, pi, propCases, propFuel).Outcome
	a := runPropWeighted(st, h, &d.Props[pi], "finds-head", base, pi, propCases, propFuel, w).Outcome
	if b == PropPassed && a == PropFalsified {
		return nil
	}
	return fmt.Errorf("the known-moving property reports %s -> %s, not passed -> falsified", b, a)
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// FALSIFIER 2, second half: the UNRELATED population must be untouched.

// TestStep2UnrelatedPopulationIsUntouched derives the conservative unrelated
// population — CORPUS objects whose TRANSITIVE closure yields no literals at
// all — and requires two things there: byte-identical generated streams, and
// unchanged verdicts.
//
// "CONSERVATIVE" IS THE LOAD-BEARING WORD. It is not "definitions that look
// unrelated to strings"; it is the set where the design provably cannot act,
// because the population it would draw from is empty. Anything with a single
// literal anywhere in its transitive closure is excluded, which makes this
// population smaller than the honest notion of "unrelated" and its no-op
// result correspondingly stronger.
func TestStep2UnrelatedPopulationIsUntouched(t *testing.T) {
	st, _ := step2Store(t)
	corpus := step2Corpus(t, st)

	var unrelated []string
	for _, h := range corpus {
		w, err := strWeightsForOwner(st, h)
		if err != nil {
			t.Fatalf("%s population: %v", shortHash(h), err)
		}
		if w == nil {
			unrelated = append(unrelated, h)
		}
	}
	if len(unrelated) == 0 {
		t.Skip("no CORPUS object has a literal-free transitive closure; the UNRELATED population is empty")
	}

	streams, verdicts, cases := 0, 0, 0
	for _, h := range unrelated {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", shortHash(h), err)
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("%s metadata: %v", shortHash(h), err)
		}
		w, err := strWeightsForOwner(st, h)
		if err != nil {
			t.Fatalf("%s population: %v", shortHash(h), err)
		}
		base := caseSeedBase(h)
		for pi := range d.Props {
			for c := 0; c < propCases; c++ {
				cases++
				a, err := genPropCase(st, &d.Props[pi], base, pi, c)
				if err != nil {
					continue // the unweighted arm cannot build this case either; nothing moved
				}
				b, err := genPropCaseWeighted(st, &d.Props[pi], base, pi, c, w)
				if err != nil {
					t.Errorf("%s prop %d case %d: the weighted arm failed where the unweighted arm did not: %v",
						shortHash(h), pi, c, err)
					continue
				}
				if !step2SameEnv(st, a, b) {
					streams++
					if streams <= 5 {
						t.Errorf("UNRELATED %s prop %d case %d: the generated stream MOVED where the "+
							"population is empty", shortHash(h), pi, c)
					}
				}
			}
			name := metaPropName(m, pi)
			bo := runProp(st, h, &d.Props[pi], name, base, pi, propCases, propFuel).Outcome
			ao := runPropWeighted(st, h, &d.Props[pi], name, base, pi, propCases, propFuel, w).Outcome
			if ao != bo {
				verdicts++
				t.Errorf("UNRELATED %s.%s: verdict moved %s -> %s where the population is empty",
					shortHash(h), name, bo, ao)
			}
		}
	}
	t.Logf("UNRELATED: %d of %d CORPUS objects have a literal-free transitive closure; "+
		"%d generated cases compared; %d stream differences, %d verdict moves",
		len(unrelated), len(corpus), cases, streams, verdicts)

	// TWO-WAY CONTROL. Byte-identical streams are the expected result and are
	// also what a comparison that cannot see anything would report. So the same
	// comparison must report a DIFFERENCE at a site with a non-empty
	// population.
	h := protoFindsHeadHash
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("control site: %v", err)
	}
	w, err := strWeightsForOwner(st, h)
	if err != nil || w == nil {
		t.Fatalf("the control site has no population (%v)", err)
	}
	base, differs := caseSeedBase(h), false
	for c := 0; c < propCases && !differs; c++ {
		a, _ := genPropCase(st, &d.Props[1], base, 1, c)
		b, _ := genPropCaseWeighted(st, &d.Props[1], base, 1, c, w)
		differs = !step2SameEnv(st, a, b)
	}
	if !differs {
		t.Error("CONTROL FAILED: the stream comparison reports no difference at a site with a 19-literal " +
			"population, so the byte-identical result above establishes nothing")
	}
}

func step2SameEnv(st *Store, a, b []Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if printValue(st, a[i]) != printValue(st, b[i]) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// FALSIFIER 2, third half and the sharpest: the mutation campaign.

type mutantState string

const (
	mutantKilled    mutantState = "killed"
	mutantLived     mutantState = "survived"
	mutantNoVerdict mutantState = "indeterminate"
)

func step2MutantState(killer string, indet bool) mutantState {
	switch {
	case killer != "":
		return mutantKilled
	case indet:
		return mutantNoVerdict
	default:
		return mutantLived
	}
}

// TestStep2MutationCampaignDiversity runs the complete existing campaign twice
// over ONE mutant population and reports every transition.
//
// #162's falsifier 2 is that weighting narrows the alphabet enough to lose
// coverage elsewhere, and the campaign is where that would show: a mutant the
// specification previously distinguished and no longer does is a measured loss,
// not a judgment. So ANY killed -> not-killed transition fails this test.
//
// The reverse direction is reported and does not fail: survived -> killed is
// the campaign distinguishing a mutant it used to miss, which is the effect the
// design is for.
func TestStep2MutationCampaignDiversity(t *testing.T) {
	// OPT-IN, and the reason is proportionality rather than flakiness: the two
	// arms take about four minutes, and this is a one-off measurement for a
	// THROWAWAY prototype. Charging every `go test ./...` and every CI push
	// four minutes for it would tax work that has nothing to do with #162. The
	// recorded result is in the Step 2 commit message; re-run it with
	//
	//	OATH_STEP2_CAMPAIGN=1 go test -run TestStep2MutationCampaign -v ./oath
	//
	// before believing any statement about diversity, because a skipped test
	// establishes nothing and this skip says so out loud.
	if os.Getenv("OATH_STEP2_CAMPAIGN") != "1" {
		t.Skip("set OATH_STEP2_CAMPAIGN=1 to run the full two-arm mutation campaign (~4 min); " +
			"skipped, so nothing here is currently evidence about diversity")
	}
	st, _ := step2Store(t)
	corpus := step2Corpus(t, st)

	// The budgets are the campaign's own, read from the engine rather than
	// restated: a hand-copied 60/500000 here would silently stop being the
	// campaign's budget the day either constant moved.
	if mutantCases != 60 || mutantFuel != 500_000 {
		t.Fatalf("the campaign budget has moved to %d cases / %d fuel; Step 2's recorded figures are "+
			"against 60 / 500000 and must be re-measured", mutantCases, mutantFuel)
	}

	start := time.Now()
	counts := map[string]int{}
	var regressions []string
	total, weightedPops := 0, 0

	for _, h := range corpus {
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("%s: %v", shortHash(h), err)
		}
		m, err := st.GetMeta(h)
		if err != nil {
			t.Fatalf("%s metadata: %v", shortHash(h), err)
		}
		// ONE population, scored twice. genMutants is called once so the two
		// arms cannot differ in which mutants they saw.
		mutants := genMutants(st, d)
		for _, mu := range mutants {
			total++
			// The engine's own staging step, and NOT an optimisation: a mutant
			// is evaluated by its hash, so without this its `self` reference
			// resolves to nothing and every property reports "no verdict". The
			// first draft omitted it and the campaign returned 3318 of 3319
			// mutants indeterminate in BOTH arms — a perfectly stable,
			// perfectly meaningless comparison, caught only because the control
			// below requires the baseline to have killed something. Mutants are
			// cached, never admitted to the store.
			st.CacheDef(mu.hash, mu.def)
			before := step2MutantState(mutantKiller(st, m, mu))
			w, err := strWeightsForDef(st, mu.hash, mu.def)
			if err != nil {
				t.Fatalf("%s mutant %s population: %v", shortHash(h), mu.desc, err)
			}
			if w != nil {
				weightedPops++
			}
			after := step2MutantState(mutantKillerWeighted(st, m, mu, w))
			counts[fmt.Sprintf("%s -> %s", before, after)]++
			if before == mutantKilled && after != mutantKilled {
				regressions = append(regressions,
					fmt.Sprintf("%s [%s] %s -> %s", shortHash(h), mu.desc, before, after))
			}
		}
	}

	t.Logf("MUTANTS: %d mutants over %d CORPUS objects (%d drew a non-empty population), "+
		"budget %d cases / %d fuel per arm, %s",
		total, len(corpus), weightedPops, mutantCases, mutantFuel, time.Since(start).Round(time.Millisecond))
	for _, k := range sortedKeys(counts) {
		t.Logf("  %-36s %d", k, counts[k])
	}

	// CONTROL: the campaign must have killed something, or "nothing regressed"
	// is a statement about an empty set.
	killedBefore := 0
	for k, v := range counts {
		if strings.HasPrefix(k, string(mutantKilled)+" ->") {
			killedBefore += v
		}
	}
	if killedBefore == 0 {
		t.Fatal("the baseline campaign killed no mutant at all; a zero-regression result would be vacuous")
	}
	if weightedPops == 0 {
		t.Fatal("no mutant drew a non-empty population, so both arms ran the same generator and this " +
			"campaign compares nothing")
	}

	for _, r := range regressions {
		t.Errorf("DIVERSITY REGRESSION: %s", r)
	}
	if len(regressions) == 0 {
		t.Logf("  NO mutant killed by the baseline survives the weighted arm (%d killed baseline)", killedBefore)
	}
}

// ---------------------------------------------------------------------------
// FALSIFIER 3: branch relevance must be STABLE — a definition's literal set
// cannot change without its hash changing.
//
// #162 states this as the risk that "the generator stops being a function of
// identity", and answers it by naming the canonical dependency closure. That is
// an argument, not a check. The claim decomposes into two halves that need
// different instruments, and only doing both closes it:
//
//	READS   extraction consults NOTHING but canonical bytes. Audited
//	        mechanically, at the store backend, in
//	        TestStep2ExtractionReadsOnlyCanonicalBytes.
//	MOVES   changing a literal anywhere in the closure moves BOTH the literal
//	        set and the owner's hash; changing anything that is not canonical
//	        bytes moves NEITHER. Firing and non-firing controls below.

// recordingBackend counts what a Store actually asks its backend for. It
// embeds the interface so an unlisted method keeps working, and only the three
// READ paths that could carry non-canonical state are intercepted.
type recordingBackend struct {
	backend
	mu      sync.Mutex
	objects []string
	metas   []string
	names   int
}

func (r *recordingBackend) getObject(h string) ([]byte, bool, error) {
	r.mu.Lock()
	r.objects = append(r.objects, h)
	r.mu.Unlock()
	return r.backend.getObject(h)
}

func (r *recordingBackend) getMeta(h string) ([]byte, bool, error) {
	r.mu.Lock()
	r.metas = append(r.metas, h)
	r.mu.Unlock()
	return r.backend.getMeta(h)
}

func (r *recordingBackend) readNames() ([]byte, bool, error) {
	r.mu.Lock()
	r.names++
	r.mu.Unlock()
	return r.backend.readNames()
}

func (r *recordingBackend) reset() {
	r.mu.Lock()
	r.objects, r.metas, r.names = nil, nil, 0
	r.mu.Unlock()
}

// TestStep2ExtractionReadsOnlyCanonicalBytes is the mechanical half of
// condition 3.
//
// THE AUDIT IS OF THE READ LOG, NOT OF A SECOND TRAVERSAL. Re-deriving the
// expected closure here would be a hand-written copy of the thing under test,
// and it would agree with itself by construction. Instead each read is checked
// against the canonical dependency edges of the objects already read: a read is
// legitimate iff it is the owner, or some previously-read definition NAMES it
// via `sortedDepHashes`. That is exactly "hash-addressed dependencies,
// recursively", stated as a property of the log.
func TestStep2ExtractionReadsOnlyCanonicalBytes(t *testing.T) {
	_, dir := step2Store(t)
	fs, err := openFSBackend(dir)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	rec := &recordingBackend{backend: fs}
	// A FRESH store, so nothing is already cached: an audit over a warm cache
	// would observe no reads at all and pass for the wrong reason.
	st, err := newStoreWithBackend(rec, dir)
	if err != nil {
		t.Fatalf("opening the recording store: %v", err)
	}

	owner := protoFindsHeadHash
	rec.reset()
	w, err := strWeightsForOwner(st, owner)
	if err != nil {
		t.Fatalf("extraction: %v", err)
	}
	if w == nil {
		t.Fatal("the audited extraction produced no population; there is nothing to audit")
	}
	objects, metas, names := rec.objects, rec.metas, rec.names
	t.Logf("EXTRACTION READS at %s: %d objects, %d metadata, %d name-index reads",
		shortHash(owner), len(objects), len(metas), names)

	if names != 0 {
		t.Errorf("extraction read the NAME INDEX %d times; names are mutable store state, so the "+
			"literal set could change without any hash changing", names)
	}
	if len(metas) != 0 {
		t.Errorf("extraction read METADATA for %v; metadata is not part of identity, so the literal "+
			"set could change without any hash changing", metas)
	}
	if len(objects) == 0 {
		t.Fatal("extraction read no objects at all; the recorder is not wired to the store under test")
	}

	// Every read must be the owner or named by something already read.
	reachable := map[string]bool{owner: true}
	for _, h := range objects {
		if !reachable[h] {
			t.Errorf("extraction read %s, which is neither the owner nor named by any object read "+
				"before it — that read is not a hash-addressed dependency", shortHash(h))
			continue
		}
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatalf("re-reading %s: %v", shortHash(h), err)
		}
		for _, dep := range sortedDepHashes(d) {
			reachable[dep] = true
		}
	}

	// TWO-WAY CONTROL: the recorder must be able to SEE a name or metadata
	// read. Without this, three zeros are equally consistent with a recorder
	// wired to nothing.
	rec.reset()
	if _, ok := st.Resolve("Str"); !ok {
		t.Fatal("Str is not bound in the copied store")
	}
	if _, err := st.GetMeta(owner); err != nil {
		t.Fatalf("reading metadata: %v", err)
	}
	if rec.names == 0 && len(rec.metas) == 0 {
		t.Error("CONTROL FAILED: a deliberate name lookup and metadata read registered nothing, so " +
			"the zero counts above are a fact about the recorder rather than about extraction")
	}
	t.Logf("CONTROL: a deliberate lookup registers %d name reads, %d metadata reads", rec.names, len(rec.metas))
}

// step2Chain builds the CHAIN population in a fresh temp store: a three-link
// chain whose only literal lives at the far end.
//
//	delimiter-caller -> delimiter-relay -> delimiter-source, which holds `lit`
//
// Three links, not two, so the DIRECT reading provably fails here: the caller's
// direct dependency `delimiter-relay` carries no literal at all, so a
// non-transitive extraction returns an empty population. That makes the chain a
// firing control for transitivity itself and not only for propagation.
func step2Chain(t *testing.T, lit int64, extra string) (*Store, string) {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	src := fmt.Sprintf(`
(defn delimiter-source [] [(n Int)] Int (+ n %d))
(defn delimiter-relay [] [(n Int)] Int (delimiter-source n))
(defn delimiter-caller [] [(n Int)] Int %s)
`, lit, extra)
	if _, err := apiPut(st, src, "step2-control", ""); err != nil {
		t.Fatalf("building the CHAIN: %v", err)
	}
	h, ok := st.Resolve("delimiter-caller")
	if !ok {
		t.Fatal("delimiter-caller is not bound in the CHAIN store")
	}
	return st, h
}

func step2Lits(t *testing.T, st *Store, h string) []int64 {
	t.Helper()
	w, err := strWeightsForOwner(st, h)
	if err != nil {
		t.Fatalf("extraction at %s: %v", shortHash(h), err)
	}
	if w == nil {
		return nil
	}
	return w.lits
}

func TestStep2LiteralSetTracksIdentity(t *testing.T) {
	const callerBody = "(delimiter-relay n)"

	baseStore, baseHash := step2Chain(t, 61, callerBody)
	baseLits := step2Lits(t, baseStore, baseHash)
	if len(baseLits) != 1 || baseLits[0] != 61 {
		t.Fatalf("CHAIN baseline population is %v, want [61]; the control chain is not what it claims", baseLits)
	}
	// The chain's whole point: the literal is two hops away, so the direct
	// reading cannot see it. If this ever returns non-nil the chain has
	// collapsed and the transitive firing control below is testing nothing.
	if direct, err := strLiteralClosure(baseStore, baseHash); err != nil || direct != nil {
		t.Fatalf("the CHAIN's DIRECT population is %v (err %v), want nil; the chain is too short to "+
			"firing-control transitivity", direct, err)
	}
	t.Logf("CHAIN: caller %s, transitive population %v, direct population nil", shortHash(baseHash), baseLits)

	// --- FIRING CONTROL A: a literal in the OWNER ------------------------
	ownerStore, ownerHash := step2Chain(t, 61, "(+ (delimiter-relay n) 7)")
	ownerLits := step2Lits(t, ownerStore, ownerHash)
	if ownerHash == baseHash {
		t.Error("FIRING CONTROL A: adding a literal to the owner did not change the owner's hash")
	}
	if sameInt64s(ownerLits, baseLits) {
		t.Errorf("FIRING CONTROL A: adding literal 7 to the owner left the population at %v", ownerLits)
	}
	t.Logf("FIRING A (owner literal): hash %s -> %s, population %v -> %v",
		shortHash(baseHash), shortHash(ownerHash), baseLits, ownerLits)

	// --- FIRING CONTROL B: a literal in a TRANSITIVE dependency ----------
	// The owner's own source text is character-for-character identical to the
	// baseline's; only the far end of the chain changed. Both the population
	// and the owner's hash must move anyway — the hash because content
	// addressing propagates a dependency's identity into its callers, which is
	// precisely the mechanism condition 3 rests on.
	depStore, depHash := step2Chain(t, 62, callerBody)
	depLits := step2Lits(t, depStore, depHash)
	if depHash == baseHash {
		t.Error("FIRING CONTROL B: changing a literal two hops away did not change the owner's hash, " +
			"so a definition's literal set CAN change without its identity changing")
	}
	if sameInt64s(depLits, baseLits) {
		t.Errorf("FIRING CONTROL B: changing the transitive literal to 62 left the population at %v", depLits)
	}
	t.Logf("FIRING B (transitive dependency literal): hash %s -> %s, population %v -> %v",
		shortHash(baseHash), shortHash(depHash), baseLits, depLits)

	// --- NON-FIRING CONTROL: metadata only -------------------------------
	// Metadata is not part of identity, so perturbing it must move neither.
	m, err := baseStore.GetMeta(baseHash)
	if err != nil {
		t.Fatalf("reading CHAIN metadata: %v", err)
	}
	before := *m
	m.Name = m.Name + "-perturbed"
	m.PropNames = append(m.PropNames, "invented-name")
	if err := baseStore.SetMeta(baseHash, m); err != nil {
		t.Fatalf("writing CHAIN metadata: %v", err)
	}
	after, err := baseStore.GetMeta(baseHash)
	if err != nil {
		t.Fatalf("re-reading CHAIN metadata: %v", err)
	}
	if after.Name == before.Name && len(after.PropNames) == len(before.PropNames) {
		t.Fatal("NON-FIRING CONTROL is inert: the metadata perturbation did not take, so its " +
			"no-change result says nothing")
	}
	if lits := step2Lits(t, baseStore, baseHash); !sameInt64s(lits, baseLits) {
		t.Errorf("NON-FIRING CONTROL: a metadata-only change moved the population from %v to %v", baseLits, lits)
	}
	if d, err := baseStore.GetDef(baseHash); err != nil || hashDef(d) != baseHash {
		t.Errorf("NON-FIRING CONTROL: a metadata-only change moved the owner's identity (err %v)", err)
	}
	t.Logf("NON-FIRING (metadata only): population %v unchanged, hash %s unchanged", baseLits, shortHash(baseHash))
}

func sameInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStep2NameRepointCannotMoveTheLiteralSet is the name half of the
// non-firing control, and it is the one that discharges Step 1's recorded
// caveat directly.
//
// Step 1's extraction found the Str datatype by resolving the NAME "Str", which
// made the weighting a function of `names.json` — mutable state that moves with
// no hash changing anywhere. This repoints that name in a copied store and
// requires the extraction, and the generated stream, to be untouched.
func TestStep2NameRepointCannotMoveTheLiteralSet(t *testing.T) {
	clean, dir := step2Store(t)
	owner := protoFindsHeadHash
	wantLits := step2Lits(t, clean, owner)
	if len(wantLits) == 0 {
		t.Fatal("the control owner has no population; a no-change result would be vacuous")
	}
	d, err := clean.GetDef(owner)
	if err != nil {
		t.Fatalf("reading the owner: %v", err)
	}
	wantStream := step2StreamDigest(t, clean, d, owner, 1)

	// Repoint "Str" at a different datatype, on disk, then open a FRESH store
	// so no cached name index is involved.
	namesPath := filepath.Join(dir, "names.json")
	raw, err := os.ReadFile(namesPath)
	if err != nil {
		t.Fatalf("reading names.json: %v", err)
	}
	var names map[string]string
	if err := json.Unmarshal(raw, &names); err != nil {
		t.Fatalf("parsing names.json: %v", err)
	}
	other, ok := names["List"]
	if !ok || other == names["Str"] {
		t.Fatal("cannot find a distinct datatype to repoint Str at")
	}
	original := names["Str"]
	names["Str"] = other
	out, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		t.Fatalf("encoding names.json: %v", err)
	}
	if err := os.WriteFile(namesPath, out, 0o644); err != nil {
		t.Fatalf("writing names.json: %v", err)
	}

	be, err := openFSBackend(dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	moved, err := newStoreWithBackend(be, dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}

	// CONTROL that the perturbation is real: the OLD name-based lookup must
	// now return something else. Without this the test could pass because the
	// edit never landed.
	if got := strTypeHash(moved); got == original {
		t.Fatal("the repoint did not take: strTypeHash still resolves Str to its original object, so " +
			"the no-change result below establishes nothing")
	}
	if canonicalStrHash() != original {
		t.Fatalf("the derived Str identity moved with the name; extraction is still name-dependent")
	}

	if lits := step2Lits(t, moved, owner); !sameInt64s(lits, wantLits) {
		t.Errorf("repointing the name Str moved the population from %v to %v", wantLits, lits)
	}
	d2, err := moved.GetDef(owner)
	if err != nil {
		t.Fatalf("reading the owner from the repointed store: %v", err)
	}
	if got := step2StreamDigest(t, moved, d2, owner, 1); got != wantStream {
		t.Error("repointing the name Str changed the GENERATED STREAM; weighting is still a function " +
			"of mutable name state rather than of identity")
	}
	t.Logf("NON-FIRING (name repoint): Str rebound to %s, population %v and the %d-case stream both unchanged",
		shortHash(other), wantLits, propCases)
}

// step2StreamDigest renders a property's whole weighted case stream as one
// comparable string.
func step2StreamDigest(t *testing.T, st *Store, d *Def, owner string, pi int) string {
	t.Helper()
	w, err := strWeightsForOwner(st, owner)
	if err != nil {
		t.Fatalf("extraction: %v", err)
	}
	base := caseSeedBase(owner)
	var b strings.Builder
	for c := 0; c < propCases; c++ {
		env, err := genPropCaseWeighted(st, &d.Props[pi], base, pi, c, w)
		if err != nil {
			fmt.Fprintf(&b, "%d:err\n", c)
			continue
		}
		for _, v := range env {
			b.WriteString(printValue(st, v))
			b.WriteByte('\x1f')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestStep2UnreadableClosureFails checks the last condition-3 obligation: an
// extraction that cannot read its whole closure must FAIL rather than return a
// smaller population.
//
// This is not defensive coding. A truncated closure is a DIFFERENT
// distribution, not a partial one — dropping literals raises every survivor's
// share of the 1/4 arm — so a silent partial read would let the generator
// weight differently for the same canonical bytes, which is precisely what
// condition 3 forbids.
func TestStep2UnreadableClosureFails(t *testing.T) {
	st, dir := step2Chain2(t)
	owner, ok := st.Resolve("delimiter-caller")
	if !ok {
		t.Fatal("delimiter-caller is not bound")
	}
	if lits := step2Lits(t, st, owner); len(lits) == 0 {
		t.Fatal("the CHAIN has no population before damage; the control cannot distinguish failure " +
			"from an already-empty result")
	}

	// Remove the far end of the chain — the object that actually holds the
	// literal — and reopen so nothing is cached.
	src, ok := st.Resolve("delimiter-source")
	if !ok {
		t.Fatal("delimiter-source is not bound")
	}
	if err := os.Remove(filepath.Join(dir, "objects", src+".bin")); err != nil {
		t.Fatalf("removing the dependency: %v", err)
	}
	be, err := openFSBackend(dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	damaged, err := newStoreWithBackend(be, dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}

	// --- PATH 1: entry by hash. The STORE refuses, not this code. ---------
	// `GetDef` re-validates on load and `checkDef` resolves dependency hashes
	// transitively, so the OWNER's own load fails before extraction walks
	// anything. That is the stronger enforcement — the invariant belongs to the
	// store, which owns object admission — but it means the traversal's own
	// guard is not what fired here, and an earlier draft of this control
	// reported success while testing exactly that prerequisite.
	w, err := strWeightsForOwner(damaged, owner)
	if err == nil {
		t.Fatalf("extraction SUCCEEDED over an unreadable closure and returned %v; a truncated "+
			"population is a different distribution, not a smaller one", w)
	}
	if !strings.Contains(err.Error(), shortHash(src)) {
		t.Errorf("the failure does not name the unreadable dependency %s: %v", shortHash(src), err)
	}
	t.Logf("UNREADABLE CLOSURE, by hash: refused at load by the store — %v", err)

	// --- PATH 2: entry with the def IN HAND. The traversal must refuse. ---
	// This is the mutant path (`strWeightsForDef`): the owner never goes
	// through `GetDef`, so no well-formedness check has run and the store's
	// enforcement is simply absent. Reached here by decoding the owner's raw
	// canonical bytes, which is what makes the traversal's own guard the thing
	// under test rather than a duplicate of the store's.
	raw, err := os.ReadFile(filepath.Join(dir, "objects", owner+".bin"))
	if err != nil {
		t.Fatalf("reading the owner's canonical bytes: %v", err)
	}
	inHand, err := decodeDefRaw(raw)
	if err != nil {
		t.Fatalf("decoding the owner's canonical bytes: %v", err)
	}
	w2, err := strLiteralPopulationOf(damaged, owner, inHand, true)
	if err == nil {
		t.Fatalf("extraction with the def in hand SUCCEEDED over an unreadable closure and returned "+
			"%v; the mutant path has no store check in front of it, so this is the only guard there is", w2)
	}
	// Match this function's OWN message, not merely "an error happened": the
	// point of path 2 is to establish which code refused.
	if !strings.Contains(err.Error(), "an incomplete population is a DIFFERENT distribution") {
		t.Errorf("the def-in-hand path failed, but not in the traversal's guard — so that guard is "+
			"still unwitnessed: %v", err)
	}
	t.Logf("UNREADABLE CLOSURE, def in hand: refused by the traversal — %v", err)
}

// step2Chain2 is step2Chain with the store's directory returned, so a test can
// damage it on disk.
func step2Chain2(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	src := `
(defn delimiter-source [] [(n Int)] Int (+ n 61))
(defn delimiter-relay [] [(n Int)] Int (delimiter-source n))
(defn delimiter-caller [] [(n Int)] Int (delimiter-relay n))
`
	if _, err := apiPut(st, src, "step2-control", ""); err != nil {
		t.Fatalf("building the CHAIN: %v", err)
	}
	return st, dir
}
