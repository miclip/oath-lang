package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"strings"
	"testing"
)

// The claim under test (#130):
//
//	a surviving mutant is `proof-refuted` exactly when some property recorded
//	PROVEN for the original is REFUTED against the mutant body.
//
// Both directions are asserted, because a checker that only looks for
// refutations is satisfied by `return "proof-refuted"`. The control cases below
// are the half that makes the witness discriminate.
//
// The fixture is chosen so the generator provably cannot reach the distinguishing
// input: `genValue` draws Int from [-20,20], so a guard at 48 is unsatisfiable in
// every generated case. That is the real shape from the corpus (`hex-nibble`
// scores 11/53 while PROVEN), reproduced small enough to assert exactly.
const adjudFixture = `(defn over48 [] [(c Int)] Int
  (if (<= 48 c) 1 0)
  (prop big-is-one [(c Int)] (if (<= 48 c) (== (over48 c) 1) true))
  (prop small-is-zero [] (== (over48 0) 0)))`

func adjudSetup(t *testing.T) (*Store, string, *Meta, *Def) {
	t.Helper()
	requireZ3(t)
	st := newStore(t)
	put(t, st, adjudFixture)
	if _, err := apiProve(st, "over48"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	h, ok := st.Resolve("over48")
	if !ok {
		t.Fatal("over48 did not resolve")
	}
	m, err := st.GetMeta(h)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	// SETUP ASSERTION, not decoration: every case below is vacuous if the
	// property never proved, and a silently-unproven fixture would make the
	// whole file pass while measuring nothing.
	if len(m.ProvenProps) == 0 {
		t.Fatalf("fixture did not prove: ProvenProps=%v", m.ProvenProps)
	}
	d, err := st.GetDef(h)
	if err != nil {
		t.Fatalf("GetDef: %v", err)
	}
	return st, h, m, d
}

// mutantNamed finds a generated mutant by its description.
func mutantNamed(t *testing.T, st *Store, d *Def, desc string) mutantDef {
	t.Helper()
	for _, mu := range genMutants(st, d) {
		if mu.desc == desc {
			st.CacheDef(mu.hash, mu.def)
			return mu
		}
	}
	var have []string
	for _, mu := range genMutants(st, d) {
		have = append(have, mu.desc)
	}
	t.Fatalf("no mutant %q; generated: %v", desc, have)
	return mutantDef{}
}

// The positive case: a mutant that generated testing cannot distinguish, and
// that the proof refutes outright.
func TestAdjudicateRefutesSurvivorTestingMissed(t *testing.T) {
	st, _, m, d := adjudSetup(t)
	mu := mutantNamed(t, st, d, "literal 48 → 49")

	// First establish it really IS a survivor. Without this the test could pass
	// on a mutant the generator already kills, which would witness nothing.
	if killer := firstKiller(st, m, mu); killer != "" {
		t.Fatalf("mutant was killed by %q — not a survivor, so it cannot witness this claim", killer)
	}

	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "proof-refuted" {
		t.Fatalf("kind=%q reason=%q, want proof-refuted", v.kind, v.reason)
	}
	if v.prop != "big-is-one" {
		t.Fatalf("prop=%q, want big-is-one", v.prop)
	}
}

// CONTROL 1 — the instrument must NOT refute a mutant the proven properties do
// not separate. `(<= 48 c)` → `(<= 47 c)` changes behaviour at c = 47 only, and
// no proven property here observes it.
func TestAdjudicateLeavesUnobservedMutantUnadjudicated(t *testing.T) {
	st, _, m, d := adjudSetup(t)
	mu := mutantNamed(t, st, d, "literal 48 → 47")
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "unadjudicated" {
		t.Fatalf("kind=%q, want unadjudicated — a mutant no proven property separates must never be reported as refuted", v.kind)
	}
	if !strings.Contains(v.reason, "still holds") {
		t.Fatalf("reason=%q, want the honest 'every proven property still holds' wording", v.reason)
	}
}

// CONTROL 2 — with nothing proven there is nothing to appeal to, and the verdict
// must say so rather than defaulting to a disposition it did not establish. This
// is the direction the instrument would lie in if it lied.
func TestAdjudicateWithoutProofsIsHonest(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, adjudFixture) // deliberately NOT proved
	h, _ := st.Resolve("over48")
	m, _ := st.GetMeta(h)
	d, _ := st.GetDef(h)
	if len(m.ProvenProps) != 0 {
		t.Fatalf("fixture was proved despite not calling apiProve: %v", m.ProvenProps)
	}
	mu := mutantNamed(t, st, d, "literal 48 → 49")
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "unadjudicated" || !strings.Contains(v.reason, "no proven property") {
		t.Fatalf("kind=%q reason=%q, want unadjudicated/no proven property", v.kind, v.reason)
	}
}

// CONTROL 3 — an attempt that never reached a verdict must NOT be reported as
// "every proven property still holds". This is the exact direction the
// instrument would lie in, and it is not reachable by ordinary means: a
// wall-clock safety cap needs a race against the solver. OATH_PROVE_FORCE_ABORT
// is the repo's existing fault injection for that path (it can only ever
// SUPPRESS a verdict, never fabricate one).
//
// Found by review: the first version of adjudicateSurvivor matched "unknown" by
// name, so "invalidated" fell through and a solver that gave up was recorded as
// a clean result. The switch now fails closed on anything but an explicit
// "proven", and this test is what holds it there.
func TestAdjudicateDoesNotTreatAbortedProofAsClean(t *testing.T) {
	st, _, m, d := adjudSetup(t)
	// The mutant no proven property separates — so WITHOUT the abort this
	// returns "every proven property still holds". That baseline is asserted
	// first: otherwise a broken fixture would make the abort case pass for the
	// wrong reason.
	mu := mutantNamed(t, st, d, "literal 48 → 47")
	if v := adjudicateSurvivor(st, m, mu.hash, mu.def); !strings.Contains(v.reason, "still holds") {
		t.Fatalf("baseline reason=%q, want 'still holds' — the abort case below would witness nothing", v.reason)
	}

	t.Setenv("OATH_PROVE_FORCE_ABORT", "big-is-one")
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind != "unadjudicated" {
		t.Fatalf("kind=%q, want unadjudicated", v.kind)
	}
	if strings.Contains(v.reason, "still holds") {
		t.Fatalf("an ABORTED proof attempt was reported as a clean result: %q", v.reason)
	}
	if !strings.Contains(v.reason, "did not reach a verdict") {
		t.Fatalf("reason=%q, want the did-not-reach-a-verdict wording", v.reason)
	}
}

// A mutant that DESTROYS termination must never be refuted. prove.go asserts a
// defining equation only for a function classified total, so a non-total mutant
// stays uninterpreted and z3 can "refute" any property using arbitrary values
// for it — the same fabrication as the missing-metadata bug, reached by a
// different route.
//
// The fixture is a real corpus shape — `show-nat`'s measure recursion on
// `(/ n 10)`. Termination there depends on the ARITHMETIC rather than on
// structural descent, so the catalogue's `/ → *` mutant recurses on `(* n 10)`
// and diverges. On the committed corpus `show-nat` has exactly two such mutants
// (`/ → *` and `literal 10 → 0`), both of which survive generation.
//
// A structural fixture does NOT exercise this: the first attempt used
// `take-n (- n 1) t`, whose recursion descends on the string, so `- → +` stays
// total. The setup assertion below is what caught that.
func TestAdjudicateRefusesNonTotalMutants(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(defn digits10 [] [(n Int)] Int
		(if (<= n 0) 0 (+ 1 (digits10 (/ n 10))))
		(prop zero-is-zero [] (== (digits10 0) 0)))`)
	if _, err := apiProve(st, "digits10"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	h, _ := st.Resolve("digits10")
	m, _ := st.GetMeta(h)
	d, _ := st.GetDef(h)
	if len(m.ProvenProps) == 0 {
		t.Fatal("fixture did not prove — nothing to adjudicate against")
	}

	mu := mutantNamed(t, st, d, "/ → *")
	// SETUP ASSERTION: the whole point is that this mutant is NOT total. If the
	// classifier ever calls it total, this test silently stops testing anything.
	if term := terminationOf(st, mu.def, mu.hash); isTotal(term) {
		t.Fatalf("fixture mutant classified %q (total) — it no longer exercises the non-total path", term)
	}
	v := adjudicateSurvivor(st, m, mu.hash, mu.def)
	if v.kind == "proof-refuted" {
		t.Fatalf("a NON-TOTAL mutant was proof-refuted by %q — the refutation is against an uninterpreted function, not this body", v.prop)
	}
	if !strings.Contains(v.reason, "not provably total") {
		t.Fatalf("reason=%q, want the non-total explanation", v.reason)
	}
	// And `zero-is-zero` genuinely HOLDS on this mutant — at n = 0 the guard
	// returns 0 before recursing at all — so "refuted" would have been false as
	// well as unfounded.
}

// THE SOUNDNESS CONTROL, and it is the one that matters most: adjudicating the
// ORIGINAL body as though it were a mutant must NEVER return proof-refuted. The
// original satisfies its proven properties by construction — that is what
// "proven" means — so a refutation there is the instrument fabricating evidence.
//
// This is not hypothetical. Before the metadata fix, a recursive mutant reached
// the prover as an UNINTERPRETED function: with no defining equation asserted,
// z3 may choose any values for it, so almost any property is trivially
// "refutable". `str-take` reported 6 proof-refuted survivors that way, and every
// one was false. The bug looked like extra power and was the opposite.
//
// A general control beats a per-fixture one here: it is expressible for every
// definition in the corpus without knowing anything about its body.
func TestAdjudicateNeverRefutesTheOriginal(t *testing.T) {
	requireZ3(t)
	for _, src := range []string{
		adjudFixture,
		`(defn dbl [] [(n Nat)] Nat
			(match n ((Z) (Z)) ((S m) (S (S (dbl m)))))
			(prop step [(n Nat)] (== (dbl (S n)) (S (S (dbl n)))))
			(prop z-is-z [] (== (dbl (Z)) (Z))))`,
		`(defn len2 [] [(xs NatList)] Nat
			(match xs ((LNil) (Z)) ((LCons y ys) (S (len2 ys))))
			(prop cons-grows [(y Nat) (ys NatList)] (== (len2 (LCons y ys)) (S (len2 ys)))))`,
	} {
		st := newStore(t)
		put(t, st, `(data Nat [] (Z) (S Nat))`)
		put(t, st, `(data NatList [] (LNil) (LCons Nat NatList))`)
		put(t, st, src)
		reps := put(t, st, src)
		name := reps[len(reps)-1].Name
		if _, err := apiProve(st, name); err != nil {
			t.Fatalf("%s: apiProve: %v", name, err)
		}
		h, _ := st.Resolve(name)
		m, _ := st.GetMeta(h)
		d, _ := st.GetDef(h)
		if len(m.ProvenProps) == 0 {
			t.Fatalf("%s did not prove — the control below would be vacuous", name)
		}
		if v := adjudicateSurvivor(st, m, h, d); v.kind == "proof-refuted" {
			t.Fatalf("%s: the ORIGINAL body was reported proof-refuted by its own proven property %q — the adjudicator is fabricating refutations", name, v.prop)
		}
	}
}

// RECURSION. Every case above uses a nonrecursive fixture, and that is exactly
// why the first implementation shipped a defect: the prover asserts a function's
// defining equation only for one classified TOTAL, reads that classification via
// GetMeta, and GetMeta fails for a cached mutant. So every recursive mutant was
// modeled as an uninterpreted function and came back inconclusive — on most of
// the corpus, silently, while the nonrecursive tests all passed.
//
// The lesson is the repo's own: a witness must derive its universe from the
// CLAIM ("a surviving mutant", any shape) rather than from the fixture that
// happened to be convenient.
func TestAdjudicateHandlesRecursiveMutants(t *testing.T) {
	requireZ3(t)
	st := newStore(t)
	put(t, st, `(data Nat [] (Z) (S Nat))`)
	// double is structurally recursive, and `two-is-four` pins a concrete value
	// the generator does reach — so the ORIGINAL proves, giving adjudication
	// something to appeal to.
	put(t, st, `(defn double [] [(n Nat)] Nat
		(match n ((Z) (Z)) ((S m) (S (S (double m)))))
		(prop z-is-z [] (== (double (Z)) (Z)))
		(prop one-is-two [] (== (double (S (Z))) (S (S (Z))))))`)
	if _, err := apiProve(st, "double"); err != nil {
		t.Fatalf("apiProve: %v", err)
	}
	h, _ := st.Resolve("double")
	m, _ := st.GetMeta(h)
	d, _ := st.GetDef(h)
	if len(m.ProvenProps) == 0 {
		t.Skip("fixture did not prove; nothing to adjudicate against")
	}
	if m.Termination == "" || !isTotal(m.Termination) {
		t.Fatalf("fixture termination=%q, want a total classification", m.Termination)
	}

	// Every mutant of a recursive body must reach the prover with a termination
	// classification. Without one it is modeled as an UNINTERPRETED function,
	// which does not weaken the result — it corrupts it, because an unconstrained
	// function makes almost any property refutable.
	var checked int
	for _, mu := range genMutants(st, d) {
		st.CacheDef(mu.hash, mu.def)
		mm := mutantMeta(st, m, mu.def, mu.hash)
		if mm.Termination == "" {
			t.Fatalf("%s: mutant carries no termination classification — it would reach the prover uninterpreted", mu.desc)
		}
		// And it must be the MUTANT's classification, not the original's. A
		// mutation that destroys structural descent must not inherit "structural"
		// and get its defining equation asserted.
		if want := terminationOf(st, mu.def, mu.hash); mm.Termination != want {
			t.Fatalf("%s: termination %q, want the mutant's own %q", mu.desc, mm.Termination, want)
		}
		if len(mm.ProvenProps) != 0 {
			t.Fatalf("%s: mutant inherited proven properties %v — the original's self-lemmas would axiomatize the body under test", mu.desc, mm.ProvenProps)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no mutants generated — this test asserted nothing")
	}
}

// The score is the thing that must NOT move. Adjudication adds a reading; if it
// ever folded into killed/total it would change `analyses/*.json`, which
// oathrs/conformance.sh requires byte-identical across kernels.
func TestAdjudicationDoesNotChangeTheScore(t *testing.T) {
	st, h, _, _ := adjudSetup(t)
	plain, err := apiMutateHashOpt(st, h, false)
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	mPlain, _ := st.GetMeta(h)
	killedPlain, totalPlain := mPlain.MutantsKilled, mPlain.MutantsTotal

	adj, err := apiMutateHashOpt(st, h, true)
	if err != nil {
		t.Fatalf("mutate --prove: %v", err)
	}
	mAdj, _ := st.GetMeta(h)
	if mAdj.MutantsKilled != killedPlain || mAdj.MutantsTotal != totalPlain {
		t.Fatalf("score moved: %d/%d -> %d/%d", killedPlain, totalPlain, mAdj.MutantsKilled, mAdj.MutantsTotal)
	}
	scoreLine := func(s string) string {
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "generated mutation score:") {
				return l
			}
		}
		return ""
	}
	if scoreLine(plain) == "" {
		t.Fatalf("no score line in plain output:\n%s", plain)
	}
	if scoreLine(plain) != scoreLine(adj) {
		t.Fatalf("score line differs:\n plain: %q\n --prove: %q", scoreLine(plain), scoreLine(adj))
	}
	// And the adjudication must actually have run — otherwise this test passes
	// trivially by comparing two identical unadjudicated reports.
	if !strings.Contains(adj, "proof-refuted") {
		t.Fatalf("--prove produced no proof-refuted survivor, so the equality above witnesses nothing:\n%s", adj)
	}
	if strings.Contains(plain, "proof-refuted") {
		t.Fatalf("the default path adjudicated; it must stay prover-free:\n%s", plain)
	}
}

// firstKiller CALLS the scoring loop's kill test rather than reproducing it. It
// used to be a third copy of the predicate, which meant a test asking "is this
// mutant a survivor?" could answer differently from the engine that scored it.
func firstKiller(st *Store, m *Meta, mu mutantDef) string {
	killer, _ := mutantKiller(st, m, mu)
	return killer
}

// TestBudgetClampsToTheContextCap asserts every outcome of the clamp as a pure
// function. The interesting one is the third: a cap ABOVE the nominal budget
// must not RAISE it, or setting a generous cap would silently strengthen an
// attempt the strategy chain deliberately weakened (`directRlimit` is reduced on
// purpose, and prove.go clamps it so a reduced attempt is never stronger than
// the full fallback that subsumes it).
func TestBudgetClampsToTheContextCap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cap     int64
		nominal int64
		want    int64
	}{
		{"no cap is inert", 0, 400_000_000, 400_000_000},
		{"cap below nominal clamps", 400_000, 400_000_000, 400_000},
		{"cap above nominal does NOT raise", 400_000_000, 4_000_000, 4_000_000},
		{"cap equal to nominal is a no-op", 4_000_000, 4_000_000, 4_000_000},
		{"negative cap is inert, never inverted", -1, 4_000_000, 4_000_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &smtCtx{rlimitCap: tc.cap}
			if got := c.budget(tc.nominal); got != tc.want {
				t.Errorf("budget(%d) with cap %d = %d, want %d",
					tc.nominal, tc.cap, got, tc.want)
			}
		})
	}
}

// TestEveryStrategyAttemptRoutesThroughTheCap is the EXCLUSIVITY witness for the
// adjudication cap (#146), and it is a property of the SOURCE rather than of one
// execution — the same reason TestEnumerationRunsNoSolver is written this way.
//
// The claim is not "the cap is applied at the sites I know about". It is that
// EVERY solver attempt the strategy chain can make derives its budget from
// `c.budget`. A strategy that reached the solver on its own would run at the
// full budget during adjudication and BREAK NO OUTPUT TEST: the only symptom is
// a sweep that takes hours and reports verdicts the stated cap says it could not
// have reached. That is a claim silently becoming false, which is precisely what
// this repo keeps catching late.
//
// THE CONTRACT IT PINS CHANGED WITH THE ATTEMPT CACHE, AND THE REPLACEMENT IS
// STRICTLY TIGHTER. Strategies used to pass their own runner closure, so the
// only checkable thing was that each closure named `c.budget`; a strategy could
// still declare one budget and spend another because nothing compared them.
// `smtCtx.solve` now takes a NOMINAL budget and is the sole solver call site, so
// this pins three things instead of one: the seam clamps, the runner spends what
// the clamp returned, and the DEDUP KEY records that same value. The third is
// new and load-bearing — a key naming a different budget from the run would let
// a reduced attempt's answer be served to a full-budget one, which is a wrong
// verdict rather than a slow sweep.
func TestEveryStrategyAttemptRoutesThroughTheCap(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := parser.ParseFile(fset, "prove.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	fns := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			fns[fn.Name.Name] = fn
		}
	}
	body := fns["proveOneInner"]
	if body == nil {
		t.Fatal("proveOneInner not found in prove.go — this check has no subject " +
			"and would otherwise pass by inspecting nothing")
	}

	// PART 1 — the strategy chain reaches the solver ONLY through the seam.
	// A direct runZ3Budget call would carry its own budget past the clamp.
	var bypass []string
	solves := 0
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok &&
			(id.Name == "runZ3" || id.Name == "runZ3Budget") {
			bypass = append(bypass, fmt.Sprintf("%s at %s — it does not pass through c.budget",
				id.Name, fset.Position(call.Pos())))
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "solve" {
			solves++
			// The seam's budget argument is the 4th. A strategy that omitted it
			// would not compile, so what is checked here is only that the shape
			// this test reasons about is the shape in the file.
			if len(call.Args) != 4 {
				bypass = append(bypass, fmt.Sprintf(
					"c.solve with %d arguments at %s — this check reads the 4th as the "+
						"nominal budget and can no longer locate it",
					len(call.Args), fset.Position(call.Pos())))
			}
		}
		return true
	})
	if len(bypass) != 0 {
		t.Errorf("solver attempts in proveOneInner bypass smtCtx.budget, so survivor "+
			"adjudication would silently run them at the FULL proof budget:\n  %s",
			strings.Join(bypass, "\n  "))
	}
	// THE CONTROL for part 1. All of the above passes trivially over zero call
	// sites — a renamed seam, a mis-parse, a subject whose body moved. Requiring
	// that solve calls were actually SEEN proves the walk reached real code.
	if solves == 0 {
		t.Error("found no c.solve call in proveOneInner — the walk is not seeing " +
			"the strategy chain, so its silence is not evidence")
	}

	// PART 2 — the seam clamps, spends the clamped value, and KEYS ON IT.
	solve := fns["solve"]
	if solve == nil {
		t.Fatal("smtCtx.solve not found — it is the only path from a strategy to " +
			"the solver; its absence voids this check")
	}
	// The identifier assigned from c.budget(...). Everything below asks whether
	// that same identifier reaches the runner and the cache key: naming one
	// variable is what makes the three uses incapable of disagreeing.
	clamped := ""
	ast.Inspect(solve.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "budget" {
			if id, ok := as.Lhs[0].(*ast.Ident); ok {
				clamped = id.Name
			}
		}
		return true
	})
	if clamped == "" {
		t.Fatal("smtCtx.solve does not assign c.budget(...) to a variable, so every " +
			"strategy escapes the adjudication cap and the dedup key cannot be " +
			"shown to name the budget actually spent")
	}

	// The runner spends exactly the clamped value.
	runs := 0
	ast.Inspect(solve.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || (id.Name != "runZ3" && id.Name != "runZ3Budget") {
			return true
		}
		runs++
		if len(call.Args) != 2 {
			t.Errorf("%s in smtCtx.solve takes %d arguments; this check reads the "+
				"2nd as the budget", id.Name, len(call.Args))
			return true
		}
		arg, ok := call.Args[1].(*ast.Ident)
		if !ok || arg.Name != clamped {
			t.Errorf("smtCtx.solve runs the solver at an expression other than the "+
				"clamped budget %q at %s — the cap is then advisory, and the dedup "+
				"key describes a budget the run did not spend",
				clamped, fset.Position(call.Args[1].Pos()))
		}
		return true
	})
	if runs != 1 {
		t.Errorf("smtCtx.solve contains %d solver calls, want exactly 1 — the "+
			"single-seam argument this test rests on is what makes the cap and the "+
			"cache key exclusive", runs)
	}

	// The cache key records that same value. A key built from the NOMINAL budget
	// would collapse the reduced direct attempt and the full-budget fallback into
	// one entry, deleting the fallback silently.
	keyed := false
	ast.Inspect(solve.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "solveKey" {
			return true
		}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			k, ok := kv.Key.(*ast.Ident)
			if !ok || k.Name != "rlimit" {
				continue
			}
			if v, ok := kv.Value.(*ast.Ident); ok && v.Name == clamped {
				keyed = true
			}
		}
		return true
	})
	if !keyed {
		t.Errorf("the solveKey built in smtCtx.solve does not take its rlimit from "+
			"the clamped budget %q — two attempts at different budgets would share "+
			"a cache entry, and the full-budget direct fallback would be served the "+
			"reduced attempt's answer", clamped)
	}
}

// TestAdjudicationSummarySeparatesTheResidue witnesses the reporting half of the
// cap: because attempts are bounded, "no verdict" is the ONLY disposition a
// larger budget could move, and merging it with the settled ones would report
// the instrument's reach as the specification's silence.
func TestAdjudicationSummarySeparatesTheResidue(t *testing.T) {
	verdicts := []survivorVerdict{
		{kind: "proof-refuted", prop: "digits"},
		{kind: "unadjudicated", why: whyHolds, reason: "every proven property still holds on the mutant"},
		{kind: "unadjudicated", why: whyHolds, reason: "every proven property still holds on the mutant"},
		{kind: "unadjudicated", why: whyNoVerdict, reason: "1 proven property did not reach a verdict"},
		{kind: "unadjudicated", why: whyNonTotal, reason: "mutant is not provably total"},
	}
	descs := []string{"a", "b", "c", "d", "e"}
	var b strings.Builder
	renderAdjudication(&b, verdicts, descs, true)
	out := b.String()

	for _, want := range []string{
		"    1 proof-refuted",
		"    4 unadjudicated",
		"        2 every proven property still holds (settled)",
		"        1 NO VERDICT within the attempt budget (unresolved)",
		"        1 mutant not provably total (settled)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q\n--- got ---\n%s", want, out)
		}
	}

	// THE CONTROL that makes the separation meaningful: a population with NO
	// unresolved survivors must not print an unresolved line at all. Without
	// this the test passes for a renderer that always prints every label with a
	// zero beside it, which communicates the opposite of what it should.
	settled := []survivorVerdict{
		{kind: "unadjudicated", why: whyHolds, reason: "holds"},
	}
	var b2 strings.Builder
	renderAdjudication(&b2, settled, []string{"a"}, true)
	if strings.Contains(b2.String(), "NO VERDICT") {
		t.Errorf("a fully settled population reported an unresolved residue:\n%s", b2.String())
	}
}
