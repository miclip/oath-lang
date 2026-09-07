package main

import (
	"strconv"
	"testing"
	"time"
)

// THE ATTEMPT CACHE (#98). The strategy sequence re-derives the same SMT problem
// several times for one goal: a datatype's base constructor makes the
// lexicographic base subgoal byte-identical to the structural-induction one, and
// a goal with no admissible lemmas makes the lemma-free probe byte-identical to
// the direct attempt. Each duplicate is a fresh z3 subprocess spending the full
// budget on a question already answered.
//
// The reuse is licensed by SPEC §7.2's own claim — an outcome is a pure function
// of (script bytes, solver version, rlimit) — and NOT by anything weaker. So the
// key is (bytes, effective rlimit), and only VALID attempts are stored. The
// controls below witness each half separately, because a cache that got either
// wrong would still look like a working cache: keying on bytes alone deletes the
// full-budget fallback, and caching an environmental abort turns one unlucky
// moment into a suppressed verdict.
//
// They count SOLVER RUNS via z3Seq rather than wall time. A dedup that "looks
// fast" is not evidence — proveOneInner is slow for many reasons — and z3Seq
// advances once per attempt that reaches runZ3Budget, including invalid ones,
// which is exactly the population the cache is supposed to shrink.

// trivialScript is answered `unsat` by z3 in milliseconds and consumes almost no
// rlimit, so these tests exercise the seam without the corpus's cost. Its BYTES
// are what matters here; the goal it encodes does not.
const trivialScript = "(assert (not true))\n(check-sat)\n"

// a second script, differing in bytes, to witness that the cache discriminates
// rather than answering everything from one entry.
const trivialScript2 = "(assert (not (= 1 1)))\n(check-sat)\n"

// The three solver-backed controls below use kernel_test.go's `requireZ3`, which
// SKIPS when z3 is absent. That is the repo's convention and it is not free: the
// `go-kernel` CI job installs no solver, so in CI those three do not run and the
// cache's coverage there rests on the two controls that need no solver — the
// per-goal reset and the enumeration duplicate. Both of those FAIL rather than
// skip on a missing input.

// solverRuns counts the attempts that actually reached runZ3Budget while f ran.
func solverRuns(f func()) int64 {
	before := z3Seq.Load()
	f()
	return z3Seq.Load() - before
}

// TestSolveDeduplicatesAtOneBudget is the cache's positive claim: the second
// identical problem is answered without a solver.
func TestSolveDeduplicatesAtOneBudget(t *testing.T) {
	requireZ3(t)
	c := &smtCtx{}
	var o1, o2 string
	runs := solverRuns(func() {
		o1, _ = c.solve("direct", "", trivialScript, 4_000_000)
		o2, _ = c.solve("induction", "b", trivialScript, 4_000_000)
	})
	if runs != 1 {
		t.Errorf("two identical attempts at one budget ran the solver %d time(s), want 1", runs)
	}
	if c.solveHits != 1 {
		t.Errorf("solveHits = %d, want 1 — the second attempt was not served from the cache", c.solveHits)
	}
	if o1 != o2 {
		t.Errorf("the cached answer differs from the solver's: %q vs %q", o1, o2)
	}

	// THE DISCRIMINATION CONTROL. Everything above passes for a cache that
	// answers EVERY question from its first entry. A different script must still
	// reach the solver.
	runs = solverRuns(func() { c.solve("direct", "", trivialScript2, 4_000_000) })
	if runs != 1 {
		t.Errorf("a DIFFERENT script ran the solver %d time(s), want 1 — the cache "+
			"is not keyed on the script bytes", runs)
	}
}

// TestSolveKeepsDistinctBudgetsApart is the half that a bytes-only key would
// fail, and it is the one that would cost a verdict rather than time. The
// reduced direct attempt and the full-budget fallback run byte-identical scripts
// (SPEC §7.2 puts the runner's budget outside the hashed script) precisely so the
// second can answer where the first ran out; collapsing them deletes the
// fallback while every test that only counts solver runs gets faster and greener.
func TestSolveKeepsDistinctBudgetsApart(t *testing.T) {
	requireZ3(t)
	c := &smtCtx{}
	runs := solverRuns(func() {
		c.solve("direct", "", trivialScript, 4_000_000)
		c.solve("direct-fallback", "", trivialScript, 400_000_000)
	})
	if runs != 2 {
		t.Errorf("identical bytes at DIFFERENT budgets ran the solver %d time(s), "+
			"want 2 — the full-budget fallback was served the reduced attempt's "+
			"answer, which is the outcome §7.2 makes the fallback exist to reach", runs)
	}
	if c.solveHits != 0 {
		t.Errorf("solveHits = %d, want 0", c.solveHits)
	}
}

// TestSolveKeysOnTheCLAMPEDBudget follows from the same rule read the other way.
// Under survivor adjudication (#146) `rlimitCap` collapses two nominal budgets to
// one effective budget, and at that point the two attempts ARE the same problem —
// the kernel already says so, in the `redundantFallback` short-circuit. A key
// built from the NOMINAL budget would keep them apart and spend the cap twice on
// the diagnostic path the cap exists to make cheap.
func TestSolveKeysOnTheCLAMPEDBudget(t *testing.T) {
	requireZ3(t)
	c := &smtCtx{rlimitCap: 1_000_000}
	runs := solverRuns(func() {
		c.solve("direct", "", trivialScript, 4_000_000)
		c.solve("direct-fallback", "", trivialScript, 400_000_000)
	})
	if runs != 1 {
		t.Errorf("two nominal budgets clamped to one ran the solver %d time(s), "+
			"want 1 — the key reads the nominal budget, not the one spent", runs)
	}
}

// TestSolveRetriesAfterAnInvalidAttempt is the soundness half. An environmental
// abort is NOT an outcome (SPEC §7.2), so it must not be remembered: a duplicate
// arriving after one has to run for real. Caching it would let a single wall-cap
// hit — a busy machine, nothing more — suppress every later attempt at the same
// problem and turn a provable goal into `invalidated`.
//
// The abort is injected through the search deadline, which short-circuits execZ3
// BEFORE it spawns z3. That is fault injection at the host boundary rather than a
// race against the clock, so the invalid attempt is deterministic — and it still
// travels the whole runZ3Budget path, publishing telemetry and advancing z3Seq
// exactly as a real abort does.
func TestSolveRetriesAfterAnInvalidAttempt(t *testing.T) {
	requireZ3(t)
	c := &smtCtx{}

	setSearchWallDeadline(time.Now().Add(-time.Hour))
	out, invalid := c.solve("direct", "", trivialScript, 4_000_000)
	clearSearchWallDeadline()
	if !invalid {
		t.Fatal("the injected deadline did not produce an invalid attempt — the " +
			"fault injector is broken, so nothing below is measuring the retry")
	}
	if out != "" {
		t.Errorf("an invalid attempt returned %q, want empty", out)
	}
	if len(c.solved) != 0 {
		t.Fatalf("an invalid attempt was cached (%d entr(ies)) — a later duplicate "+
			"would be served an environmental abort as if it were an outcome", len(c.solved))
	}

	// The retry must reach the solver, and must reach a real answer.
	var got string
	runs := solverRuns(func() { got, _ = c.solve("direct-fallback", "", trivialScript, 4_000_000) })
	if runs != 1 {
		t.Errorf("the attempt after an abort ran the solver %d time(s), want 1", runs)
	}
	if got == "" {
		t.Fatalf("the retry returned no answer; without one the caching claim below " +
			"is untested")
	}
	// And THAT answer is the one a third duplicate gets.
	runs = solverRuns(func() { c.solve("induction", "b", trivialScript, 4_000_000) })
	if runs != 0 || c.solveHits != 1 {
		t.Errorf("after a VALID attempt the duplicate ran the solver %d time(s) "+
			"(solveHits=%d), want 0 run and 1 hit — a valid outcome must be cached "+
			"even when an invalid one preceded it", runs, c.solveHits)
	}
}

// TestSolveCacheIsResetPerGoal pins the BOUND, and the header says bound rather
// than soundness deliberately.
//
// Reuse is licensed by the script bytes being the whole SMT problem, and that
// does NOT stop holding at a goal boundary: admitted lemmas are emitted into the
// script as `(assert …)` lines, so a changed lemma state is changed bytes and
// therefore a different key. An entry carried into a second goal would still
// return the right answer. What is scoped is the OPTIMIZATION — a process-wide
// cache would need a lifecycle, a story for the parallel scan, and a reporting
// decision, and none of that is bought here.
//
// So what this test protects is that the bound is STRUCTURAL: it holds however a
// caller allocates contexts, rather than because callers happen to hand over a
// fresh one. A regression here is unbounded memory and a scope nobody can state,
// not a wrong verdict.
func TestSolveCacheIsResetPerGoal(t *testing.T) {
	st := corpusStore(t)
	h, ok := st.Resolve("append")
	if !ok {
		t.Fatal("the corpus has no `append`, so this check has no subject")
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.GetMeta(h)
	if err != nil {
		t.Fatal(err)
	}
	c := newSmtCtx(st, d, h)
	c.enumerate = true // no solver runs; the reset is what is under test
	loadLemmaLibrary(c, st, d, h, m, 0)

	// A sentinel no script builder can produce, so its survival can only mean the
	// cache was carried across goals.
	sentinel := solveKey{script: ";; sentinel — not a goal script\n", rlimit: 7}
	c.solved = map[solveKey]string{sentinel: "unsat"}
	c.proveOneInner(d, h, m, &d.Props[0], 0)
	if _, alive := c.solved[sentinel]; alive {
		t.Error("proveOneInner did not reset the attempt cache, so the map now " +
			"outlives the goal that filled it and grows for as long as the context " +
			"does — the bound this cache is scoped by, not a wrong answer: a carried " +
			"entry would still be correct, since a changed lemma state is changed " +
			"script bytes and so a different key")
	}
	// THE CONTROL: the walk must have populated the cache, or the assertion above
	// is satisfied by a proveOneInner that did nothing at all.
	if len(c.solved) == 0 {
		t.Error("the cache is empty after enumerating a goal — the reset above is " +
			"passing over an empty map and witnesses nothing")
	}
}

// TestEnumerationSuppressesTheStructuralDuplicate is the corpus-level control,
// and it is a DECIDABLE claim about `append`'s first property rather than a
// judgement about the corpus as a whole.
//
// `append` recurses on a list, so the lexicographic strategy's BASE case for the
// nil constructor emits the same declarations, the same assertions and the same
// subgoal as structural induction's nil case — byte-identical scripts at the same
// full budget. That pair is a genuine duplicate and must not survive. The direct
// and direct-fallback pair is also byte-identical and must survive, because their
// budgets differ. Both halves are asserted here: a cache that suppressed
// everything would pass the first and fail the second.
//
// Enumeration must agree with execution on this, or `prove/attempts.txt` would
// pin a script at a budget no run ever spends.
func TestEnumerationSuppressesTheStructuralDuplicate(t *testing.T) {
	st := corpusStore(t)
	h, ok := st.Resolve("append")
	if !ok {
		t.Fatal("the corpus has no `append`, so this check has no subject")
	}
	ats, err := scriptAttempts(st, h, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ats) == 0 {
		t.Fatal("no attempts enumerated — nothing below is measuring anything")
	}

	byText := map[string][]string{}
	for _, a := range ats {
		byText[a.text] = append(byText[a.text], a.strategy)
	}
	sawFallbackPair := false
	for _, strats := range byText {
		if len(strats) == 1 {
			continue
		}
		// The ONLY legitimate survivor: identical bytes at two different budgets.
		if len(strats) == 2 &&
			((strats[0] == "direct" && strats[1] == "direct-fallback") ||
				(strats[0] == "direct-fallback" && strats[1] == "direct")) {
			sawFallbackPair = true
			continue
		}
		t.Errorf("append[0] enumerates one script under %v — identical bytes at one "+
			"budget, which is a solver run the cache is supposed to have removed", strats)
	}
	if !sawFallbackPair {
		t.Error("append[0] no longer enumerates the direct/direct-fallback pair — " +
			"either the reduced-budget direct attempt is gone, or the cache keyed on " +
			"bytes alone and swallowed the full-budget retry the fixture pins")
	}
	// THE CONTROL. The loop above is satisfied by an enumeration that produced no
	// duplicates because the strategies never ran. Require that a duplicate was
	// actually SUPPRESSED, which is the behaviour under test.
	if c := len(ats); c < 4 {
		t.Errorf("append[0] enumerated only %d attempt(s); the strategy chain is not "+
			"being walked, so the absence of duplicates is not evidence", c)
	}
}

// TestSplitConsumedNamesAReusedAttempt covers the diagnostic half. The
// OATH_PROVE_SPLIT line reads its `consumed=` figure from a package-level cell
// that only the RUNNER publishes, so a reused attempt would otherwise print the
// previous attempt's consumption as though it were its own — a plausible integer
// standing in for a measurement that was never taken. There is no value that
// could carry the distinction, which is why the field became a string.
func TestSplitConsumedNamesAReusedAttempt(t *testing.T) {
	requireZ3(t)
	c := &smtCtx{}
	c.solve("direct", "", trivialScript, 4_000_000)
	ran := c.splitConsumed()
	if ran == "reused" {
		t.Fatalf("an attempt that RAN reported %q — the flag is stuck on, so the "+
			"assertion below cannot distinguish anything", ran)
	}
	if _, err := strconv.ParseInt(ran, 10, 64); err != nil {
		t.Errorf("an attempt that ran reported %q, want the solver's own counter", ran)
	}
	if got := (&smtCtx{}).splitConsumed(); got == "reused" {
		t.Errorf("a fresh context reports %q before any attempt", got)
	}

	// The duplicate is served from the cache: no counter was published for it.
	c.solve("induction", "b", trivialScript, 4_000_000)
	if got := c.splitConsumed(); got != "reused" {
		t.Errorf("a REUSED attempt reported %q — the diagnostic is attributing an "+
			"earlier attempt's consumption to one that ran no solver", got)
	}

	// And the flag CLEARS: a later attempt that runs must not inherit it.
	c.solve("direct-fallback", "", trivialScript2, 4_000_000)
	if got := c.splitConsumed(); got == "reused" {
		t.Error("an attempt that ran after a reused one still reports `reused` — " +
			"the flag is latched, so every later line is wrong")
	}
}
