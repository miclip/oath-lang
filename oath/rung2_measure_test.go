package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// MEASUREMENT ONLY — #65 rung 2 (cross-type proof-implication). Nothing here is
// wired into `find --implies`; this file exists to decide whether the rung is
// worth implementing, by measuring what it would find that the shipped
// exact-signature filter cannot.
//
// The CLAIM under test: re-typing a query property's BINDERS to a candidate's
// signature (using the same primitive-leaf anti-unification propHashGeneral
// already uses) admits candidates the exact-signature filter rejects, and some
// of those candidates PROVABLY satisfy the query.
//
// The UNIVERSE the claim quantifies over is (query property, candidate
// definition) pairs whose signatures are equal UP TO PRIMITIVE LEAVES and
// unequal exactly. That set is derived from the claim, not from
// apiFindImplies's loop: the shipped filter's population is the hypothesis
// being tested, so it cannot also be the population being measured.
//
// SCOPE, deliberately narrower than rung 2's full statement: only Prop.Binders
// are re-typed. Term.TyArgs (ref/self/ctor instantiations) and Term.Ty
// (lam/let annotations) inside a property BODY are left alone — that is rung 3.
// The consequence is not unsoundness but rejection: a body-embedded type that
// disagrees with the re-typed binders fails checkDef, and the candidate is
// dropped. Those rejections are counted separately, because their count IS the
// size of the rung-3 residue.

// The compatibility relation and the binder re-typing SHIPPED (api.go) after
// this measurement returned. They are called here rather than re-implemented:
// two copies of one relation is exactly the drift this repo keeps paying for,
// and a measurement of a shipped mechanism must measure the shipped mechanism.
// Re-running this file therefore re-measures what `find --implies` now does.
type rung2Sub = crossTypeSub

func rung2Compatible(qTy, cTy *Ty) (rung2Sub, bool) { return crossTypeCompatible(qTy, cTy) }

func rung2Retype(binders []Ty, sub rung2Sub) []Ty { return crossTypeRetypeBinders(binders, sub) }

// bodyCarriesTypes reports whether a property body embeds a type anywhere —
// a ref/self/ctor instantiation or a lam/let annotation. These are exactly what
// rung 3 would have to thread the substitution through, and what this
// measurement deliberately leaves alone.
func bodyCarriesTypes(t *Term) bool {
	if t == nil {
		return false
	}
	if len(t.TyArgs) > 0 || t.Ty != nil {
		return true
	}
	for _, k := range []*Term{t.A, t.B, t.C} {
		if bodyCarriesTypes(k) {
			return true
		}
	}
	for i := range t.Args {
		if bodyCarriesTypes(&t.Args[i]) {
			return true
		}
	}
	for i := range t.Arms {
		if bodyCarriesTypes(&t.Arms[i]) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The discriminating synthetic case.
// ---------------------------------------------------------------------------

// rung2Attempt appends one query property to a candidate definition and proves
// it about that candidate — the same operation apiFindImplies performs, with
// the binders optionally re-typed first. Returns whether it proved, and how
// many z3 processes ran (via the PATH shim when installed; -1 when not).
func rung2Attempt(st *Store, cand *Def, h string, m *Meta, p Prop) (bool, string) {
	aug := *cand
	aug.Props = append(append([]Prop{}, cand.Props...), p)
	if err := checkDef(st, &aug); err != nil {
		return false, "ill-typed"
	}
	pi := len(cand.Props)
	c := newSmtCtx(st, &aug, h)
	loadLemmaLibrary(c, st, &aug, h, m, -1)
	o := c.proveOne(&aug, h, m, &aug.Props[pi], pi)
	return o.status == "proven", o.status
}

// TestRung2SyntheticDelta is the CONTROL: it establishes that the measurement
// can distinguish the two modes at all, before any corpus number is believed.
//
// Two mutations must break it, and both are asserted rather than assumed:
//   - if exact-signature matching already found the Rat definition, the delta
//     would be zero and the whole rung would be measuring nothing;
//   - if re-typing admitted anything whose signature merely LOOKS similar, the
//     mode would be unsound rather than more complete — so a Bool-signature
//     definition carrying a shaped-alike law must stay rejected.
func TestRung2SyntheticDelta(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	// The candidate: commutative addition over Rat. The query below is over
	// Int, so no exact signature match exists anywhere in this store.
	put(t, st, `(defn plus-r [] [(a Rat) (b Rat)] Rat (+ a b)
		(prop comm [(a Rat) (b Rat)] (== (plus-r a b) (plus-r b a))))`)
	// A decoy with a DIFFERENT signature shape (three args), which must not be
	// admitted by compatibility.
	put(t, st, `(defn pick3 [] [(a Rat) (b Rat) (c Rat)] Rat (+ a (+ b c))
		(prop assoc [(a Rat) (b Rat) (c Rat)] (== (pick3 a b c) (pick3 c b a))))`)

	// The query: Int commutativity, stated as a fresh spec with a dummy body.
	qd, _, err := elabFuncSrc(t, st, `(defn wanted [] [(a Int) (b Int)] Int (+ a b)
		(prop q [(a Int) (b Int)] (== (wanted a b) (wanted b a))))`)
	if err != nil {
		t.Fatal(err)
	}
	qsig := tyBytes(qd.Ty)

	names := st.Names()
	var exactProven, retypedProven []string
	for k, h := range names {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" {
			continue
		}
		m, _ := st.GetMeta(h)
		if bytes.Equal(tyBytes(d.Ty), qsig) {
			if ok, _ := rung2Attempt(st, d, h, m, qd.Props[0]); ok {
				exactProven = append(exactProven, k)
			}
			continue
		}
		sub, compat := rung2Compatible(qd.Ty, d.Ty)
		if !compat {
			continue
		}
		p := Prop{Binders: rung2Retype(qd.Props[0].Binders, sub), Body: qd.Props[0].Body}
		if ok, _ := rung2Attempt(st, d, h, m, p); ok {
			retypedProven = append(retypedProven, k)
		}
	}
	sort.Strings(exactProven)
	sort.Strings(retypedProven)

	// CONTROL: the shipped mode finds nothing here. If this ever fails, the
	// delta below is not a delta.
	if len(exactProven) != 0 {
		t.Fatalf("exact-signature mode should find NOTHING for an Int query in a Rat-only store, got %v", exactProven)
	}
	// THE CLAIM: re-typing finds plus-r.
	if len(retypedProven) != 1 || retypedProven[0] != "plus-r" {
		t.Fatalf("re-typed mode should prove exactly [plus-r], got %v", retypedProven)
	}
	// CONTROL: the three-argument decoy is not signature-compatible at all.
	if _, compat := rung2Compatible(qd.Ty, mustDef(t, st, "pick3").Ty); compat {
		t.Fatal("a 3-argument signature must not be compatible with a 2-argument query")
	}
	t.Logf("synthetic delta: exact=%v retyped=%v", exactProven, retypedProven)
}

// The compatibility relation's own controls — leaf-sharing preserved, differently
// shaped signatures rejected — moved to TestCrossTypeCompatibilityShape in
// find_test.go when the relation shipped. They are not duplicated here: the
// relation now has one home, and a second copy of its controls would be free to
// disagree with it.

// ---------------------------------------------------------------------------
// The corpus scan.
// ---------------------------------------------------------------------------

// openCorpusCopy opens a COPY of the committed store. The committed
// `codebase/` is never opened here: `find` writes demand signal on a miss and
// prove paths write verdicts, and a measurement must not mutate the thing it
// measures.
func openCorpusCopy(t *testing.T) *Store {
	t.Helper()
	dst := t.TempDir()
	if out, err := exec.Command("cp", "-R", "../codebase/.", dst).CombinedOutput(); err != nil {
		t.Fatalf("copy corpus: %v: %s", err, out)
	}
	st, err := OpenStore(dst)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return st
}

// shapeAlreadyMatches reports whether the candidate already states this law up
// to operand types — i.e. whether `find`/`find --spec` surfaces the pair today
// without any proof. The key is propHashGeneral, the shipped cross-type
// discovery key, read from the shipped function rather than re-derived.
func shapeAlreadyMatches(q *Prop, cand *Def) bool {
	want := propHashGeneral(q)
	for i := range cand.Props {
		if propHashGeneral(&cand.Props[i]) == want {
			return true
		}
	}
	return false
}

type rung2Cand struct {
	name string
	hash string
	def  *Def
	meta *Meta
}

func rung2Corpus(t *testing.T, st *Store) []rung2Cand {
	t.Helper()
	names := st.Names()
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []rung2Cand
	for _, k := range keys {
		h := names[k]
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" {
			continue
		}
		m, _ := st.GetMeta(h)
		out = append(out, rung2Cand{k, h, d, m})
	}
	return out
}

// TestRung2CorpusCensus counts the population WITHOUT running a solver: how
// many (query property, candidate) pairs each mode admits. It sizes the proof
// workload and, on its own, bounds the yield from above — if the delta
// population is empty, no amount of proving can produce a hit.
func TestRung2CorpusCensus(t *testing.T) {
	st := openCorpusCopy(t)
	corpus := rung2Corpus(t, st)

	var (
		queries         int
		exactPairs      int
		compatPairs     int
		deltaPairs      int
		deltaTyped      int
		deltaIllTyped   int
		bodyTyped       int
		deltaShapeKnown int
		deltaShapeNew   int
		deltaByQuery    = map[string]int{}
	)
	for _, q := range corpus {
		for pi := range q.def.Props {
			queries++
			p := q.def.Props[pi]
			if bodyCarriesTypes(&p.Body) {
				bodyTyped++
			}
			qsig := tyBytes(q.def.Ty)
			for _, c := range corpus {
				if c.hash == q.hash {
					continue
				}
				if bytes.Equal(tyBytes(c.def.Ty), qsig) {
					exactPairs++
					compatPairs++
					continue
				}
				sub, ok := rung2Compatible(q.def.Ty, c.def.Ty)
				if !ok {
					continue
				}
				compatPairs++
				deltaPairs++
				rp := Prop{Binders: rung2Retype(p.Binders, sub), Body: p.Body}
				aug := *c.def
				aug.Props = append(append([]Prop{}, c.def.Props...), rp)
				if err := checkDef(st, &aug); err != nil {
					deltaIllTyped++
					continue
				}
				deltaTyped++
				deltaByQuery[fmt.Sprintf("%s#%d", q.name, pi)]++
				// CALIBRATION the rung's own statement omits: `find` and
				// `find --spec` ALREADY match cross-type, by propHashGeneral.
				// So a delta pair whose candidate states the same law up to
				// operand types is surfaced today by the shape surface — rung 2
				// would only be adding a second route to it. The pairs that
				// decide the rung's value are the ones the shape surface misses.
				if shapeAlreadyMatches(&p, c.def) {
					deltaShapeKnown++
				} else {
					deltaShapeNew++
				}
			}
		}
	}
	t.Logf("corpus: %d live-named func defs, %d query properties (%d with body-embedded types)",
		len(corpus), queries, bodyTyped)
	t.Logf("pairs admitted — exact-signature: %d ; signature-compatible: %d ; DELTA (compatible, not exact): %d",
		exactPairs, compatPairs, deltaPairs)
	t.Logf("delta pairs that TYPECHECK after re-typing binders: %d ; rejected as ill-typed (rung-3 residue): %d",
		deltaTyped, deltaIllTyped)
	t.Logf("of the typed delta pairs — already surfaced cross-type by `find` (propHashGeneral): %d ; NOT surfaced by any shipped surface: %d",
		deltaShapeKnown, deltaShapeNew)

	keys := make([]string, 0, len(deltaByQuery))
	for k := range deltaByQuery {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if deltaByQuery[keys[i]] != deltaByQuery[keys[j]] {
			return deltaByQuery[keys[i]] > deltaByQuery[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for i, k := range keys {
		if i >= 25 {
			t.Logf("  ... and %d more query properties with a nonempty typed delta", len(keys)-i)
			break
		}
		t.Logf("  %-40s %d typed delta candidates", k, deltaByQuery[k])
	}
	if deltaTyped == 0 {
		t.Log("YIELD BOUND: zero typed delta candidates — rung 2 cannot find anything on this corpus")
	}
}

// TestRung2CorpusYield PROVES the delta pairs. Long-running: set
// OATH_RUNG2_YIELD=1. Every proof attempt here is a goal the shipped
// exact-signature mode cannot reach, so a `proven` outcome is by construction a
// MISSED IMPLICATION — a definition that provably satisfies a corpus law and
// that `find --implies` does not surface today.
func TestRung2CorpusYield(t *testing.T) {
	if os.Getenv("OATH_RUNG2_YIELD") == "" {
		t.Skip("set OATH_RUNG2_YIELD=1 (long-running: one z3 goal per delta pair)")
	}
	requireZ3(t)
	st := openCorpusCopy(t)
	corpus := rung2Corpus(t, st)

	type hit struct{ query, cand, method string }
	var hits []hit
	attempts, illTyped := 0, 0
	outcomes := map[string]int{}
	start := time.Now()
	// Progress goes to stderr, not t.Logf: `go test -v` buffers a test's logs
	// until it returns, and a scan whose per-pair cost is the thing in question
	// must be observable WHILE it runs, not only after.
	fmt.Fprintf(os.Stderr, "rung2 yield scan: wallcap=%s\n", proveWallCap())
	for _, q := range corpus {
		for pi := range q.def.Props {
			p := q.def.Props[pi]
			qsig := tyBytes(q.def.Ty)
			for _, c := range corpus {
				if c.hash == q.hash || bytes.Equal(tyBytes(c.def.Ty), qsig) {
					continue
				}
				sub, ok := rung2Compatible(q.def.Ty, c.def.Ty)
				if !ok {
					continue
				}
				rp := Prop{Binders: rung2Retype(p.Binders, sub), Body: p.Body}
				aug := *c.def
				aug.Props = append(append([]Prop{}, c.def.Props...), rp)
				if err := checkDef(st, &aug); err != nil {
					illTyped++
					continue
				}
				attempts++
				t0 := time.Now()
				proved, status := rung2Attempt(st, c.def, c.hash, c.meta, rp)
				outcomes[status]++
				fmt.Fprintf(os.Stderr, "  [%3d] %-34s -> %-18s %-12s %s\n",
					attempts, fmt.Sprintf("%s#%d", q.name, pi), c.name, status,
					time.Since(t0).Round(time.Millisecond))
				if proved {
					// PRECISE, because this label went stale the moment rung 2 shipped. Before
					// the widening these pairs were unreachable; NOW `find --implies`
					// finds them, which is the whole point. What the measurement shows is
					// what the FORMER exact-signature baseline missed — a claim that stays
					// true — not a claim about what today's surfaces can reach.
					label := "MISSED BY THE EXACT-SIGNATURE BASELINE"
					if shapeAlreadyMatches(&rp, c.def) {
						label = "already surfaced cross-type by `find`"
					}
					hits = append(hits, hit{fmt.Sprintf("%s#%d", q.name, pi), c.name, label})
				}
			}
		}
	}
	t.Logf("delta proof attempts: %d (ill-typed skipped: %d) in %s", attempts, illTyped, time.Since(start).Round(time.Millisecond))
	t.Logf("PER-GOAL WALL CAP IN FORCE: %s (default is %s)", proveWallCap(), 600*time.Second)
	// The four outcome classes are NOT interchangeable and must not be summed
	// into "did not prove". `refuted` is a semantic fact about the candidate —
	// a countermodel exists, the candidate genuinely does not satisfy the law.
	// `unknown` is the solver declining. `invalidated` is the wall cap firing,
	// which prove.go treats as an INVALID ATTEMPT rather than an outcome (an
	// earlier kernel recorded cap hits as unknown and the blind Rust kernel
	// caught it, #17 epilogue). Only `refuted` says anything about the world;
	// the other two say something about the budget.
	keysO := make([]string, 0, len(outcomes))
	for k := range outcomes {
		keysO = append(keysO, k)
	}
	sort.Strings(keysO)
	for _, k := range keysO {
		note := ""
		switch k {
		case "refuted":
			note = "  (semantic: countermodel found — the candidate does NOT satisfy the law)"
		case "unknown":
			note = "  (solver declined — NOT a disproof)"
		case "invalidated":
			note = "  (wall cap fired — an invalid attempt, NOT an outcome; would need the full budget)"
		}
		t.Logf("  outcome %-12s %3d%s", k, outcomes[k], note)
	}
	t.Logf("MISSED IMPLICATIONS at this budget (provable, unreachable by exact-signature find --implies): %d", len(hits))
	if proveWallCap() < 600*time.Second {
		t.Logf("THIS IS A LOWER BOUND, not the yield. %d attempt(s) hit the cap and %d returned unknown; "+
			"under the default 600s budget some of those could still prove. `no proof` is not `disproof`.",
			outcomes["invalidated"], outcomes["unknown"])
	}
	seen := map[string]bool{}
	for _, h := range hits {
		k := h.query + " -> " + h.cand
		if seen[k] {
			continue
		}
		seen[k] = true
		t.Logf("  %-40s -> %-18s [%s]", h.query, h.cand, h.method)
	}
}

// ---------------------------------------------------------------------------
// Latency and solver-call counting on one real query.
// ---------------------------------------------------------------------------

// installZ3Counter puts a counting shim ahead of the real z3 on PATH and
// returns a reader for the count. Counting the SUBPROCESSES is the measurement
// the claim wants: `solve` is the internal seam but it records nothing outside
// enumeration mode, and enumeration walks every script the sequence COULD emit
// rather than the ones a run actually executes.
func installZ3Counter(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("z3")
	if err != nil {
		t.Skip("z3 not on PATH")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	shim := filepath.Join(dir, "z3")
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\nexec %q \"$@\"\n", counter, real)
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, err := os.ReadFile(counter)
		if err != nil {
			return 0
		}
		return strings.Count(string(b), "x")
	}
}

// rung2CostOrder is the ALTERNATING SCHEDULE, and it is fixed. A single
// A-then-B pair cannot distinguish "cross-type costs more" from "the second run
// is warmer": the store caches decoded objects and the OS caches the files
// behind them, so whichever mode runs first pays for both. Alternating means
// each mode holds a cold slot, and the per-run times are reported individually
// so a warm/cold spread is visible rather than averaged away.
//
// ARM NUMBERS ARE 1-BASED INDEXES INTO THIS SLICE and are stable across
// invocations, which is what makes running the schedule in pieces the SAME
// schedule rather than a re-ordered one. Selecting a subset changes WHEN an arm
// runs, never WHICH MODE it is.
var rung2CostOrder = []bool{false, true, true, false, false, true}

// rung2Arm is one recorded arm. It is persisted so a run that dies — Go's
// 10-minute default test timeout kills the whole binary, and one baseline arm
// alone has measured 6m13s — resumes at the next arm instead of restarting the
// schedule.
type rung2Arm struct {
	Arm     int      `json:"arm"`
	Cross   bool     `json:"cross"`
	Cands   int      `json:"candidates"`
	Z3      int      `json:"z3_calls"`
	WallMS  int64    `json:"wall_ms"`
	Proven  []string `json:"proven"`
	Query   string   `json:"query"`
	WallCap string   `json:"wall_cap"`
	// Arms measured against a different corpus or a different prover are not
	// repetitions of one experiment. Without this a rerun after either moves
	// silently medians two revisions together into one number.
	Rev      string `json:"rev"`
	BatchPos int    `json:"batch_pos"` // 0 = first arm in its process; its store was COLD
}

// rung2Rev identifies WHAT was measured: the corpus the arms ran against and
// the prover that ran them. Arms carrying different values are two experiments,
// and a median over them is a composite wearing one number.
//
// It reads the committed names.json and the prover source rather than asking
// git, so an uncommitted change to either is still a different revision — which
// is the case most likely to happen during an experiment and least likely to be
// noticed.
func rung2Rev(t *testing.T) string {
	h := sha256.New()
	for _, p := range []string{"../codebase/names.json", "prove.go", "api.go"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("cannot identify the measured revision: %v", err)
		}
		fmt.Fprintf(h, "%s:%x\n", p, sha256.Sum256(b))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func rung2StatePath() string {
	if p := os.Getenv("OATH_RUNG2_COST_STATE"); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "oath-rung2-cost.json")
}

func rung2LoadState(t *testing.T, path string) map[int]rung2Arm {
	t.Helper()
	out := map[int]rung2Arm{}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("read state %s: %v", path, err)
		}
		return out
	}
	var arms []rung2Arm
	if err := json.Unmarshal(b, &arms); err != nil {
		t.Fatalf("parse state %s: %v (delete it or set OATH_RUNG2_RESET=1)", path, err)
	}
	for _, a := range arms {
		out[a.Arm] = a
	}
	return out
}

func rung2SaveState(t *testing.T, path string, state map[int]rung2Arm) {
	t.Helper()
	keys := make([]int, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	arms := make([]rung2Arm, 0, len(keys))
	for _, k := range keys {
		arms = append(arms, state[k])
	}
	b, err := json.MarshalIndent(arms, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write state %s: %v", path, err)
	}
}

// rung2ParseArms parses "3", "1,3", "2-4", "1-3,5" into 1-based arm numbers.
// An empty spec means "every arm not already recorded" — so a bare re-invocation
// RESUMES, while an explicit selection RE-RUNS and overwrites. "none" runs
// nothing and only reports what is on disk.
func rung2ParseArms(t *testing.T, spec string, done map[int]rung2Arm) []int {
	t.Helper()
	n := len(rung2CostOrder)
	spec = strings.TrimSpace(spec)
	if strings.EqualFold(spec, "none") {
		return nil
	}
	if spec == "" || strings.EqualFold(spec, "remaining") {
		var out []int
		for i := 1; i <= n; i++ {
			if _, ok := done[i]; !ok {
				out = append(out, i)
			}
		}
		return out
	}
	seen := map[int]bool{}
	var out []int
	add := func(i int) {
		if i < 1 || i > n {
			t.Fatalf("OATH_RUNG2_ARMS: arm %d out of range 1..%d", i, n)
		}
		if !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.EqualFold(part, "all") {
			for i := 1; i <= n; i++ {
				add(i)
			}
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err1 := strconv.Atoi(strings.TrimSpace(lo))
			b, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 != nil || err2 != nil {
				t.Fatalf("OATH_RUNG2_ARMS: bad range %q", part)
			}
			if b < a {
				t.Fatalf("OATH_RUNG2_ARMS: descending range %q", part)
			}
			for i := a; i <= b; i++ {
				add(i)
			}
			continue
		}
		a, err := strconv.Atoi(part)
		if err != nil {
			t.Fatalf("OATH_RUNG2_ARMS: bad arm %q", part)
		}
		add(a)
	}
	sort.Ints(out)
	return out
}

// TestRung2RealQueryCost runs ONE real query end to end in both modes and
// reports solver calls and wall clock for each. The query is a real corpus law
// posed as a fresh spec; the two modes differ only in which candidates they
// admit, so the difference in cost is attributable to the candidate set alone.
//
// THE SCHEDULE RUNS IN PIECES. Six arms in one process exceed Go's 10-minute
// test timeout on this corpus, so each invocation runs a SELECTED SUBSET and
// appends to a state file; the report is assembled from every arm recorded so
// far, whichever invocation produced it.
//
//	OATH_RUNG2_COST=1 go test -run TestRung2RealQueryCost -timeout 30m   # arms not yet done
//	OATH_RUNG2_ARMS=1        ...                                         # just arm 1
//	OATH_RUNG2_ARMS=3-6      ...                                         # resume after a kill
//	OATH_RUNG2_ARMS=2 OATH_RUNG2_RESET=1 ...                             # discard state first
//
// WHAT SPLITTING COSTS, stated rather than smoothed over: warming is per
// PROCESS. Six arms in one process warmed arms 2..6; one arm per process makes
// every arm cold. That is not the same experiment — it removes the warm/cold
// confound the alternation was built to expose, by making every slot cold. Each
// arm therefore records its position within its own invocation (BatchPos), and
// the report prints cold/warm per arm so a comparison across differently-batched
// arms is visible rather than assumed away.
func TestRung2RealQueryCost(t *testing.T) {
	if os.Getenv("OATH_RUNG2_COST") == "" {
		t.Skip("set OATH_RUNG2_COST=1 (runs a real query against the corpus, one or more arms)")
	}
	statePath := rung2StatePath()
	if os.Getenv("OATH_RUNG2_RESET") != "" {
		if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("reset state: %v", err)
		}
		t.Logf("state reset: %s", statePath)
	}
	state := rung2LoadState(t, statePath)

	src := os.Getenv("OATH_RUNG2_QUERY")
	if src == "" {
		src = `(defn wanted [] [(a Int) (b Int)] Int (+ a b)
			(prop q [(a Int) (b Int)] (== (wanted a b) (wanted b a))))`
	}
	srcNorm := strings.Join(strings.Fields(src), " ")
	capNow := proveWallCap().String()
	revNow := rung2Rev(t)
	// Arms recorded under a different query or a different wall cap are not
	// repetitions of one measurement, and a median over them would be a
	// composite of two experiments wearing one number.
	for _, a := range state {
		if a.Query != srcNorm {
			t.Fatalf("state %s holds arm %d measured under a DIFFERENT query (%q); set OATH_RUNG2_RESET=1 or a fresh OATH_RUNG2_COST_STATE", statePath, a.Arm, a.Query)
		}
		if a.WallCap != capNow {
			t.Fatalf("state %s holds arm %d measured under wall cap %s, now %s; set OATH_RUNG2_RESET=1 or a fresh OATH_RUNG2_COST_STATE", statePath, a.Arm, a.WallCap, capNow)
		}
		if a.Rev != "" && a.Rev != revNow {
			t.Fatalf("state %s holds arm %d measured at %s, now %s — a median across revisions is a composite of two experiments wearing one number; set OATH_RUNG2_RESET=1 or a fresh OATH_RUNG2_COST_STATE", statePath, a.Arm, a.Rev, revNow)
		}
	}

	todo := rung2ParseArms(t, os.Getenv("OATH_RUNG2_ARMS"), state)
	fmt.Fprintf(os.Stderr, "rung2 cost: state=%s wallcap=%s arms-to-run=%v already-recorded=%d/%d\n",
		statePath, capNow, todo, len(state), len(rung2CostOrder))

	if len(todo) > 0 {
		count := installZ3Counter(t)
		st := openCorpusCopy(t)
		corpus := rung2Corpus(t, st)
		qd, _, err := elabFuncSrc(t, st, src)
		if err != nil {
			t.Fatal(err)
		}
		qsig := tyBytes(qd.Ty)

		run := func(cross bool) (int, int, time.Duration, []string) {
			before := count()
			start := time.Now()
			cands, proven := 0, []string{}
			for _, c := range corpus {
				exact := bytes.Equal(tyBytes(c.def.Ty), qsig)
				var p Prop
				switch {
				case exact:
					p = qd.Props[0]
				case cross:
					sub, ok := rung2Compatible(qd.Ty, c.def.Ty)
					if !ok {
						continue
					}
					p = Prop{Binders: rung2Retype(qd.Props[0].Binders, sub), Body: qd.Props[0].Body}
				default:
					continue
				}
				cands++
				t0 := time.Now()
				ok, status := rung2Attempt(st, c.def, c.hash, c.meta, p)
				fmt.Fprintf(os.Stderr, "    cross=%-5v %-18s %-12s %s\n", cross, c.name, status, time.Since(t0).Round(time.Millisecond))
				if ok {
					proven = append(proven, c.name)
				}
			}
			return cands, count() - before, time.Since(start), proven
		}

		for batchPos, arm := range todo {
			cross := rung2CostOrder[arm-1]
			mode := "EXACT"
			if cross {
				mode = "CROSS"
			}
			fmt.Fprintf(os.Stderr, "  == arm %d/%d %s (batch position %d) ==\n", arm, len(rung2CostOrder), mode, batchPos)
			c, z, d, p := run(cross)
			sort.Strings(p)
			state[arm] = rung2Arm{
				Arm: arm, Cross: cross, Cands: c, Z3: z,
				WallMS: d.Milliseconds(), Proven: p,
				Query: srcNorm, WallCap: capNow, Rev: revNow, BatchPos: batchPos,
			}
			// Persisted after EACH arm, not at the end: the failure this exists
			// to survive kills the process, so an arm that is not on disk when
			// it finishes is an arm that will be paid for twice.
			rung2SaveState(t, statePath, state)
		}
	}

	// ---- report, over every arm recorded so far ----
	t.Logf("query: %s", srcNorm)
	t.Logf("PER-GOAL WALL CAP IN FORCE: %s (default is %s)", capNow, 600*time.Second)
	t.Logf("state file: %s", statePath)
	for arm := 1; arm <= len(rung2CostOrder); arm++ {
		mode := "EXACT"
		if rung2CostOrder[arm-1] {
			mode = "CROSS"
		}
		a, ok := state[arm]
		if !ok {
			t.Logf("  arm %d/%d  %s  NOT YET RUN", arm, len(rung2CostOrder), mode)
			continue
		}
		warm := "warm"
		if a.BatchPos == 0 {
			warm = "COLD"
		}
		t.Logf("  arm %d/%d  %s  candidates=%d z3-calls=%d wall=%s (%s store, batch position %d) proven=%v",
			arm, len(rung2CostOrder), mode, a.Cands, a.Z3,
			(time.Duration(a.WallMS) * time.Millisecond).Round(time.Millisecond), warm, a.BatchPos, a.Proven)
	}

	summarize := func(cross bool) (time.Duration, int, int, []string, int, bool) {
		var ds []time.Duration
		var z, c, n int
		var pr []string
		want := 0
		for i, x := range rung2CostOrder {
			if x != cross {
				continue
			}
			want++
			a, ok := state[i+1]
			if !ok {
				continue
			}
			ds = append(ds, time.Duration(a.WallMS)*time.Millisecond)
			// A mode's candidate set, solver-call count and proven set are
			// deterministic; if they are not, the repetitions are not measuring
			// one thing and the median is meaningless. Say so rather than
			// reporting the last arm.
			if n > 0 && (a.Z3 != z || a.Cands != c || strings.Join(a.Proven, ",") != strings.Join(pr, ",")) {
				t.Errorf("NON-DETERMINISTIC mode cross=%v: arm %d has candidates/z3/proven %d/%d/%v, earlier arm had %d/%d/%v",
					cross, a.Arm, a.Cands, a.Z3, a.Proven, c, z, pr)
			}
			z, c, pr = a.Z3, a.Cands, a.Proven
			n++
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		if n == 0 {
			return 0, 0, 0, nil, 0, false
		}
		return ds[len(ds)/2], z, c, pr, n, n == want
	}
	em, ez, ec, ep, en, eFull := summarize(false)
	cm, cz, cc, cp, cn, cFull := summarize(true)
	// A "median" over one or two arms is an observation wearing a statistic's
	// name, so the label says which it is.
	label := func(full bool, n int) string {
		if full {
			return fmt.Sprintf("MEDIAN wall (%d/3 arms)", n)
		}
		return fmt.Sprintf("PARTIAL wall — NOT a median (%d/3 arms)", n)
	}
	if en > 0 {
		t.Logf("EXACT  : candidates=%d z3-calls/run=%d %s=%s proven=%v", ec, ez, label(eFull, en), em.Round(time.Millisecond), ep)
	}
	if cn > 0 {
		t.Logf("CROSS  : candidates=%d z3-calls/run=%d %s=%s proven=%v", cc, cz, label(cFull, cn), cm.Round(time.Millisecond), cp)
	}
	// The widening factor is the ratio of medians, and it is reported only when
	// BOTH modes have all three arms — a median over one or two arms is an
	// observation, not a median, and dividing two of those produces a number
	// that looks exactly like the real one.
	switch {
	case eFull && cFull && em > 0:
		t.Logf("WIDENING FACTOR (cross median / exact median): %.3fx  (%s / %s)",
			cm.Seconds()/em.Seconds(), cm.Round(time.Millisecond), em.Round(time.Millisecond))
	case en+cn < len(rung2CostOrder):
		var missing []int
		for arm := 1; arm <= len(rung2CostOrder); arm++ {
			if _, ok := state[arm]; !ok {
				missing = append(missing, arm)
			}
		}
		t.Logf("SCHEDULE INCOMPLETE: arms %v not yet run — no widening factor. Resume with OATH_RUNG2_COST=1 OATH_RUNG2_ARMS=%s",
			missing, strings.Trim(strings.Join(strings.Fields(fmt.Sprint(missing)), ","), "[]"))
	}
}

// elabFuncSrc elaborates a single (defn ...) form the way apiFindImplies does.
func elabFuncSrc(t *testing.T, st *Store, src string) (*Def, *Meta, error) {
	t.Helper()
	forms, err := parseForms(src)
	if err != nil {
		return nil, nil, err
	}
	if len(forms) != 1 {
		return nil, nil, fmt.Errorf("expected one form, got %d", len(forms))
	}
	d, m, err := elabFunc(st, forms[0])
	if err != nil {
		return nil, nil, err
	}
	if err := checkDef(st, d); err != nil {
		return nil, nil, err
	}
	return d, m, nil
}

// TestRung3UpperBound bounds ONE HALF of rung 3's population from above.
//
// SCOPE, because the number is easy to overread. Rung 3 threads type
// generalization through a property body's type arguments, and that reaches TWO
// surfaces: the PROOF path (`--implies`, whose cross-type candidates are
// rejected by checkDef today) and the HASH path (`propHashGeneral`, where a
// body-embedded type keeps a law same-type-only even when nothing is
// ill-typed). This measures the PROOF half only. The hash half is a different
// population — pairs that typecheck fine and simply never collide — and nothing
// here counts it.
//
// THE CENSUS COUNTS WHAT `checkDef` REFUSED; THAT IS NOT RUNG 3'S POPULATION.
// #65's rung 3 proposes threading type generalization through a property BODY's
// own type arguments. It can only unblock a pair whose body carries such
// arguments. A pair rejected for anything else — an incompatible numeric
// literal, a monomorphic reference — stays rejected however that threading is
// implemented.
//
// Reading the rejection total as the rung's population is the exact substitution
// this repo keeps having to correct: a number the IMPLEMENTATION produces, read
// as a fact about the CLAIM. Sizing a prototype from it would overstate the
// reachable set by whatever fraction fails for unrelated reasons — which is the
// thing this test measures instead of assuming.
// WHY AN UPPER BOUND AND NOT THE POPULATION. Carrying body type arguments is
// NECESSARY for rung 3 to help and is not SUFFICIENT: a pair can carry them and
// still fail for a reason the threading cannot repair — a substitution mapping
// Int to a candidate's type VARIABLE leaves binders non-concrete, and retyped
// Bool binders feeding `if`/`not` still require Bool. Deciding which of the
// counted pairs actually become well-typed means APPLYING the substitution,
// which is implementing the rung. So this counts the set rung 3 could not
// exceed, and the true figure is somewhere at or below it — possibly zero.
func TestRung3UpperBound(t *testing.T) {
	st := openCorpusCopy(t)
	corpus := rung2Corpus(t, st)

	illTyped, withBodyTypes := 0, 0
	for _, q := range corpus {
		for pi := range q.def.Props {
			p := q.def.Props[pi]
			qsig := tyBytes(q.def.Ty)
			for _, c := range corpus {
				if c.hash == q.hash || bytes.Equal(tyBytes(c.def.Ty), qsig) {
					continue
				}
				sub, ok := rung2Compatible(q.def.Ty, c.def.Ty)
				if !ok {
					continue
				}
				rp := Prop{Binders: rung2Retype(p.Binders, sub), Body: p.Body}
				aug := *c.def
				aug.Props = append(append([]Prop{}, c.def.Props...), rp)
				if err := checkDef(st, &aug); err == nil {
					continue
				}
				illTyped++
				if bodyCarriesTypes(&p.Body) {
					withBodyTypes++
				}
			}
		}
	}
	if illTyped == 0 {
		t.Fatal("no ill-typed delta pairs at all, so this test measures nothing")
	}
	t.Logf("ill-typed delta pairs (the census's number): %d", illTyped)
	t.Logf("  query body carries type arguments — RUNG 3 UPPER BOUND: %d", withBodyTypes)
	t.Logf("  rejected for other reasons — rung 3 certainly cannot help: %d", illTyped-withBodyTypes)
	t.Logf("  NOTE: the bound is not the population. Carrying body types is")
	t.Logf("  necessary, not sufficient; deciding the rest requires implementing")
	t.Logf("  the substitution and re-running checkDef.")
	// PINNED, so prose cannot outlive the measurement. These exact figures are
	// quoted in docs/experiments/issue-65-rungs.md, which sizes rung 3 from
	// them. A corpus change that moves them must fail HERE — a logged number
	// nothing asserts is a number the documentation can silently outlive, which
	// is the drift check-doc-numbers exists to stop for prose and this stops for
	// a test's own output.
	const (
		wantIllTyped      = 247
		wantWithBodyTypes = 21
	)
	if illTyped != wantIllTyped || withBodyTypes != wantWithBodyTypes {
		t.Errorf("the rung-3 population moved: ill-typed %d (want %d), "+
			"upper bound %d (want %d).\nIf that is a legitimate corpus change, "+
			"update BOTH these constants and the figures in "+
			"docs/experiments/issue-65-rungs.md, which sizes the rung from them.",
			illTyped, wantIllTyped, withBodyTypes, wantWithBodyTypes)
	}

	if withBodyTypes >= illTyped {
		t.Errorf("every ill-typed pair carries body types, which would make this "+
			"bound vacuous and the census number safe to read as the population; "+
			"got %d of %d", withBodyTypes, illTyped)
	}
}

// TestRung3ProofHalfConnects measures rung 3's PROOF half directly: of the delta
// pairs that were ill-typed under binder-only re-typing (the census's 247), how
// many become WELL-TYPED once the substitution is also threaded through the
// property BODY (crossTypeRetypeBody). Those are the pairs rung 3 newly lets
// REACH the prover — the real population, versus TestRung3UpperBound's necessary-
// but-not-sufficient ceiling of 21.
//
// checkDef is still the gate: this counts newly well-typed augmentations, not
// newly proven ones. A pair that typechecks may still be refuted or unknown.
func TestRung3ProofHalfConnects(t *testing.T) {
	st := openCorpusCopy(t)
	corpus := rung2Corpus(t, st)

	illUnderBinders, wellUnderBody := 0, 0
	type pair struct{ q, c string }
	var connected []pair
	for _, q := range corpus {
		for pi := range q.def.Props {
			p := q.def.Props[pi]
			qsig := tyBytes(q.def.Ty)
			for _, c := range corpus {
				if c.hash == q.hash || bytes.Equal(tyBytes(c.def.Ty), qsig) {
					continue
				}
				sub, ok := rung2Compatible(q.def.Ty, c.def.Ty)
				if !ok {
					continue
				}
				// binder-only (the census's path)
				bp := Prop{Binders: crossTypeRetypeBinders(p.Binders, sub), Body: p.Body}
				ba := *c.def
				ba.Props = append(append([]Prop{}, c.def.Props...), bp)
				if checkDef(st, &ba) == nil {
					continue // already well-typed under binders alone — not a rung-3 pair
				}
				illUnderBinders++
				// binders + body (rung 3)
				rp := Prop{Binders: crossTypeRetypeBinders(p.Binders, sub),
					Body: *crossTypeRetypeBody(&p.Body, sub)}
				ra := *c.def
				ra.Props = append(append([]Prop{}, c.def.Props...), rp)
				if checkDef(st, &ra) == nil {
					wellUnderBody++
					if len(connected) < 12 {
						connected = append(connected, pair{q.name, c.name})
					}
				}
			}
		}
	}
	t.Logf("ill-typed under binder-only re-typing (census's residue): %d", illUnderBinders)
	t.Logf("  now WELL-TYPED with body re-typing — rung 3's proof-half yield: %d", wellUnderBody)
	for _, pr := range connected {
		t.Logf("    %s -> %s", pr.q, pr.c)
	}
	// The bound must hold: newly-well-typed <= ill-typed.
	if wellUnderBody > illUnderBinders {
		t.Errorf("more pairs well-typed (%d) than were ill-typed (%d): impossible", wellUnderBody, illUnderBinders)
	}
	// PIN the measured value, not just the bound (CLAUDE.md's authority rule: a
	// figure the docs hard-code must be derived, not restated). The proof half
	// connects ZERO pairs on this corpus — no ill-typed delta pair is repaired by
	// body re-typing, because the 21 that carry body types all fail the
	// binder-concreteness obstacle too. If a corpus change makes this non-zero
	// that is a real event: update docs/experiments/issue-65-rungs.md, which
	// records 0, rather than relaxing this assertion.
	if wellUnderBody != 0 {
		t.Errorf("rung 3's proof half now connects %d pair(s); the experiment doc records 0. "+
			"A corpus change made body re-typing productive — update the doc, do not loosen this.", wellUnderBody)
	}
}
