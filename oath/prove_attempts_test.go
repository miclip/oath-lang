package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// #139. prove/scripts.txt pinned the DIRECT attempt only, while conformance.sh's
// oracle mode concluded "outcomes are determined: f(script bytes, solver,
// rlimit), all three pinned" — a sentence true only for goals that never reach
// induction. On this corpus the direct attempt is 447 of 2904 emitted scripts.
// prove/attempts.txt pins the rest; these tests are what make it evidence.
//
// WHAT THEY DO NOT ESTABLISH. Both fixtures fix the RECORDED lemma state, which
// §7.2 makes a parameter of script identity, so a cold fixpoint's intermediate
// smaller-lemma-set scripts are pinned by neither. These tests close the
// STRATEGY dimension; the lemma-state dimension stays with the empirical
// re-derivation. Recorded here because the first version of this file claimed
// the whole emitted set — the same over-wide claim, one level up.

// theStrategies is the declared vocabulary, pinned so that a strategy which
// stops being REACHED fails here rather than quietly shrinking the fixture.
//
// Without this, a bug that made enumeration stop after the direct attempt would
// pass every other test in this file: the fixture would be regenerated from the
// same bug and would agree with it perfectly. That is the shape of a gate
// measuring its own implementation instead of its claim, so the vocabulary is
// asserted against the SPEC's strategy list rather than against whatever the
// walk happens to produce.
var theStrategies = []string{
	"direct",              // §7.2 direct proof
	"direct-fallback",     // the full-budget retry (#50)
	"induction",           // structural induction on a datatype binder
	"lemma-free",          // the lemma-free first attempt (#53)
	"lexicographic",       // lexicographic induction on an ordered pair (#17)
	"recursion-induction", // induction on the callee's own recursion (#57)
}

// corpusStore and readFixtureRows FAIL on a missing input rather than skipping.
//
// Both codebase/ and fixtures/ are committed, so their absence is a defect and
// never a legitimate environment. Skipping was the first thing written here and
// it is the fail-open shape this repo keeps finding: deleting
// fixtures/prove/attempts.txt made every test in this file report `ok`, so the
// pin covering 2904 scripts could be removed and the suite would applaud. A
// check that cannot tell a missing input from a satisfied claim is worse than no
// check — `make check-fixtures` caught the same deletion, which is why the
// contrast was visible at all.
func corpusStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("the committed store is unreadable, so nothing below was "+
			"measured: %v", err)
	}
	return st
}

// allAttempts walks the corpus exactly as the fixture generator does.
func allAttempts(t *testing.T, st *Store) []string {
	t.Helper()
	names := st.Names()
	var keys []string
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	var rows []string
	for _, name := range keys {
		h := names[name]
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || len(d.Props) == 0 {
			continue
		}
		for pi := range d.Props {
			ats, err := scriptAttempts(st, h, pi)
			if err != nil {
				continue
			}
			for _, a := range ats {
				sum := sha256.Sum256([]byte(a.text))
				rows = append(rows, fmt.Sprintf("%s\t%d\t%s\t%s\t%s",
					name, pi, a.strategy, a.detail, hex.EncodeToString(sum[:])))
			}
		}
	}
	return rows
}

func readFixtureRows(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the fixture this test compares against is missing, so the pin "+
			"it provides is absent and its silence is not evidence: %v", err)
	}
	defer f.Close()
	var rows []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		rows = append(rows, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestEnumerationRunsNoSolver is the INSTRUMENT check, and it runs first because
// nothing else in this file means anything until it passes.
//
// The claim attempts.txt makes is that it pins every script the prover runs. Its
// universe is therefore every solver call site inside proveOneInner — which is a
// property of the SOURCE, not of one execution. A dynamic check ("no z3 process
// appeared") would only establish that the sites reached on this corpus were
// recorded, and a site behind a branch no corpus definition takes would stay
// invisible for exactly as long as it stayed unreached.
//
// So this reads the function and requires that every runZ3/runZ3Budget call is
// inside c.solve. Adding a strategy that calls the solver directly fails here
// with the new call named, which is the only moment at which the omission is
// cheap to fix.
func TestEnumerationRunsNoSolver(t *testing.T) {
	fset := gotoken.NewFileSet()
	file, err := parser.ParseFile(fset, "prove.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.FuncDecl
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if ok && fn.Name.Name == "proveOneInner" {
			body = fn
		}
	}
	if body == nil {
		// A rename must not silently disarm this check: an absent subject is a
		// failure, not a pass over zero call sites.
		t.Fatal("proveOneInner not found in prove.go — this check has no subject " +
			"and would otherwise pass by inspecting nothing")
	}
	var direct []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		// PRUNE the subtree under a c.solve(...) call. A strategy whose budget
		// differs passes its runner as a closure — `func(sc string) (string,
		// bool) { return runZ3Budget(sc, directRlimit()) }` — and that closure
		// legitimately names the solver: it runs only if solve decides to run
		// it, which enumeration never does. Flagging it would make the check
		// unsatisfiable for any strategy that is not on the default budget, and
		// the usual repair for an unsatisfiable check is to weaken it.
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "solve" {
			return false
		}
		if id, ok := call.Fun.(*ast.Ident); ok &&
			(id.Name == "runZ3" || id.Name == "runZ3Budget") {
			direct = append(direct, fmt.Sprintf("%s at %s",
				id.Name, fset.Position(call.Pos())))
		}
		return true
	})
	if len(direct) != 0 {
		t.Errorf("proveOneInner calls the solver outside c.solve, so those scripts "+
			"are absent from prove/attempts.txt and their bytes are pinned by "+
			"nothing:\n  %s", strings.Join(direct, "\n  "))
	}

	// THE CONTROL. The check above passes trivially if the walk finds no calls
	// at all — a mis-parsed file, a renamed runner, a subject with an empty
	// body. Requiring that c.solve IS called proves the walk reached real code
	// and that the discrimination is live.
	solves := 0
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "solve" {
			solves++
		}
		return true
	})
	if solves < len(theStrategies) {
		t.Errorf("found %d c.solve call(s) but %d strategies are declared — the walk "+
			"is not seeing the whole function, so its silence about direct runZ3 "+
			"calls is not evidence", solves, len(theStrategies))
	}
}

// TestAttemptsFixtureMatchesTheProver is the pin itself: any change to any
// strategy's emitted bytes fails here. Reverting it and mutating, say, the
// lexicographic hypothesis order shows it failing on the exact subgoal.
func TestAttemptsFixtureMatchesTheProver(t *testing.T) {
	st := corpusStore(t)
	got := allAttempts(t, st)
	want := readFixtureRows(t, "../fixtures/prove/attempts.txt")
	if len(got) != len(want) {
		t.Fatalf("attempt count: prover emits %d, fixture pins %d — regenerate with "+
			"`oath fixtures` and commit codebase/ and fixtures/ together", len(got), len(want))
	}
	bad := 0
	for i := range got {
		if got[i] != want[i] {
			if bad < 10 {
				t.Errorf("row %d:\n  prover  %s\n  fixture %s", i, got[i], want[i])
			}
			bad++
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more diverging rows", bad-10)
	}
}

// TestEnumerationReachesEveryStrategy is the second control, and it guards the
// failure the fixture cannot see. If enumeration stopped early, the fixture
// would shrink and TestAttemptsFixtureMatchesTheProver would still pass after a
// regeneration — agreement between a broken prover and a fixture derived from
// it. Pinning the vocabulary makes an unreached strategy a failure.
func TestEnumerationReachesEveryStrategy(t *testing.T) {
	st := corpusStore(t)
	seen := map[string]int{}
	for _, row := range allAttempts(t, st) {
		f := strings.Split(row, "\t")
		seen[f[2]]++
	}
	for _, s := range theStrategies {
		if seen[s] == 0 {
			t.Errorf("strategy %q emitted no script on the corpus — either the "+
				"enumeration stops before it, or nothing witnesses it any more", s)
		}
	}
	for s := range seen {
		found := false
		for _, k := range theStrategies {
			if k == s {
				found = true
			}
		}
		if !found {
			t.Errorf("strategy %q is emitted but not declared in theStrategies — a "+
				"new strategy's scripts are normative under §7.2 and must be named "+
				"here deliberately", s)
		}
	}
}

// TestAttemptsAgreeWithTheDirectScriptFixture keeps the two fixtures from
// drifting. prove/scripts.txt remains the artifact SPEC §7.2 and the oracle name;
// attempts.txt's `direct` rows are the same scripts by a different route, so a
// change that updated one and not the other would leave the specification citing
// a hash the prover no longer emits.
func TestAttemptsAgreeWithTheDirectScriptFixture(t *testing.T) {
	var direct []string
	for _, row := range readFixtureRows(t, "../fixtures/prove/attempts.txt") {
		f := strings.Split(row, "\t")
		if f[2] == "direct" {
			direct = append(direct, fmt.Sprintf("%s\t%s\t%s", f[0], f[1], f[4]))
		}
	}
	pinned := readFixtureRows(t, "../fixtures/prove/scripts.txt")
	if len(direct) != len(pinned) {
		t.Fatalf("attempts.txt has %d direct rows; scripts.txt pins %d",
			len(direct), len(pinned))
	}
	if len(pinned) == 0 {
		t.Fatal("no rows read from either fixture — this check would certify agreement " +
			"between two empty sets")
	}
	for i := range direct {
		if direct[i] != pinned[i] {
			t.Errorf("row %d:\n  attempts.txt %s\n  scripts.txt  %s", i, direct[i], pinned[i])
		}
	}
}

// TestFallbackReusesTheDirectScriptBytes witnesses a claim §7.2 has only made in
// prose: the full-budget direct fallback (#50) "is byte-identical to the reduced
// attempt, so no new direct-attempt script hash is introduced". Nothing checked
// it, and a future edit that gave the fallback its own lemma set or model flag
// would break the direct-attempt pin silently — scripts.txt would still contain
// one hash per property while two different scripts were being run.
func TestFallbackReusesTheDirectScriptBytes(t *testing.T) {
	st := corpusStore(t)
	names := st.Names()
	var keys []string
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	pairs := 0
	for _, name := range keys {
		h := names[name]
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" {
			continue
		}
		for pi := range d.Props {
			ats, err := scriptAttempts(st, h, pi)
			if err != nil {
				continue
			}
			var direct, fallback string
			for _, a := range ats {
				switch a.strategy {
				case "direct":
					direct = a.text
				case "direct-fallback":
					fallback = a.text
				}
			}
			if fallback == "" {
				continue // not induction-eligible: no fallback exists
			}
			pairs++
			if direct != fallback {
				t.Errorf("%s prop %d: the full-budget fallback emits different bytes "+
					"from the direct attempt, so scripts.txt pins only one of the two "+
					"scripts actually run", name, pi)
			}
		}
	}
	if pairs == 0 {
		t.Fatal("no property produced both a direct attempt and a fallback — the " +
			"comparison never ran and its silence is not evidence")
	}
}
