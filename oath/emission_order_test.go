package main

// #168: a compiler run must be a function of its inputs.
//
// Both backends used to walk the dependency closure in Go map iteration order,
// so definitions with no edge between them came out in a different order run to
// run. The generated program was a permutation of itself and the artifact digest
// moved with it — measured in docs/experiments/issue-116-reproducibility as
// 27/3 over 30 Go emissions and 25/5 over 30 LLVM emissions, meaning the most
// common output appeared 27 (resp. 25) times and a second output made up the
// rest.
//
// These tests witness the repair from both ends: the emitted BYTES do not move
// across repeated emissions, and the control below shows that this exact program
// is one on which the old algorithm does move.

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// emissionFixture is a program whose entry depends on four definitions with no
// dependency edge between them. That matters: a walk that orders siblings by map
// iteration has 4! = 24 orders available here, so the fixture can actually
// exhibit the defect. A chain of dependencies could not, and a test built on one
// would pass whether or not the bug was fixed.
//
// The four must be structurally DISTINCT, which the first draft of this fixture
// was not: four identity functions on Str are one object under four names, since
// identity is the canonical AST hash. The closure then held a single sibling and
// the control below correctly refused to certify it.
//
// wrap-two and its alias wrap-two-again are that collapse used deliberately, to
// keep the second cause in the universe: an aliased hash reaches the emitters
// through Store.NameOf, which is what builds the emitted function's name.
func emissionFixture(t *testing.T) *Store {
	t.Helper()
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(defn wrap-one [] [(s Str)] Str (match s ((SNil) "one") ((SCons c r) s)))`)
	put(t, st, `(defn wrap-two [] [(s Str)] Str (match s ((SNil) "two") ((SCons c r) r)))`)
	put(t, st, `(defn wrap-two-again [] [(s Str)] Str (match s ((SNil) "two") ((SCons c r) r)))`)
	put(t, st, `(defn wrap-three [] [(s Str)] Str (match s ((SNil) "three") ((SCons c r) s)))`)
	put(t, st, `(defn wrap-four [] [(s Str)] Str (match s ((SNil) "four") ((SCons c r) r)))`)
	put(t, st, `(defn fanout [] [(args (List Str))] Str
		(match args
			((Nil) (wrap-one "none"))
			((Cons h t)
				(match t
					((Nil) (wrap-two h))
					((Cons h2 t2)
						(match t2
							((Nil) (wrap-three h2))
							((Cons h3 t3) (wrap-four h3))))))))`)
	markVerified(t, st, "fanout")
	return st
}

// mapOrderWalk is the algorithm BOTH backends carried before #168, kept here as
// the control. It is deliberately not shared with anything: its only job is to
// show that the fixture is a program on which sibling order is undefined, so
// that the byte-identity assertions below are witnessing determinism rather than
// witnessing a program that has only one possible order anyway.
func mapOrderWalk(t *testing.T, st *Store, entry string) []string {
	t.Helper()
	seen := map[string]bool{}
	var order []string
	var walk func(h string)
	walk = func(h string) {
		if seen[h] {
			return
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			t.Fatal(err)
		}
		if d.K != "func" {
			return
		}
		for dep := range collectDepsBody(d) { // the defect: map iteration order
			walk(dep)
		}
		order = append(order, h)
	}
	walk(entry)
	return order
}

// emissionN is the repetition count for the byte-identity assertions.
//
// What N buys, stated against the measured rates rather than assumed: the old Go
// emitter's outputs split 27/3 over 30 runs, so the majority output had rate
// p ≈ 0.9. A still-broken emitter with that distribution passes N repetitions
// only if every draw lands in one class, with probability p^(N-1) + (1-p)^(N-1);
// at N = 100 that is about 3e-5. The LLVM split of 25/5 gives p ≈ 0.83 and about
// 1e-8. Those rates were measured on the experiment's program, not on this
// fixture, so treat them as the order of magnitude N is worth and not as a bound
// for this test — the control above is what actually establishes that this
// fixture can vary at all.
const emissionN = 100

// The Go backend must emit byte-identical source for byte-identical inputs.
func TestGoEmissionIsByteIdenticalAcrossRuns(t *testing.T) {
	st := emissionFixture(t)
	var first string
	for i := 0; i < emissionN; i++ {
		// Re-planned each iteration so the whole build front-end is in the
		// universe, not just the emitter.
		prog, err := planProgram(st, "fanout")
		if err != nil {
			t.Fatalf("planProgram: %v", err)
		}
		src, err := emitProgram(st, prog)
		if err != nil {
			t.Fatalf("emitProgram: %v", err)
		}
		if i == 0 {
			first = src
			continue
		}
		if src != first {
			t.Fatalf("Go emission %d of %d differs from the first: the backend is not a "+
				"function of its inputs\n%s", i+1, emissionN, firstDiff(first, src))
		}
	}
}

// The LLVM backend must too. Textual IR only: emitLLVM is the whole of this
// backend's own output, and the experiment already established that clang and ld
// are deterministic downstream (the artifact digest tracked the source digest
// 14/14 paired runs), so requiring clang here would add a tool dependency
// without adding a claim.
func TestLLVMEmissionIsByteIdenticalAcrossRuns(t *testing.T) {
	st := emissionFixture(t)
	var first string
	for i := 0; i < emissionN; i++ {
		prog, err := planProgram(st, "fanout")
		if err != nil {
			t.Fatalf("planProgram: %v", err)
		}
		ir, err := emitLLVM(st, prog)
		if err != nil {
			t.Fatalf("emitLLVM: %v", err)
		}
		if i == 0 {
			first = ir
			continue
		}
		if ir != first {
			t.Fatalf("LLVM emission %d of %d differs from the first: the backend is not a "+
				"function of its inputs\n%s", i+1, emissionN, firstDiff(first, ir))
		}
	}
}

// The control. If this fails, the two tests above have stopped witnessing
// anything: it would mean the fixture admits only one emission order, so they
// would pass over a backend that still ordered siblings by map iteration.
//
// The freedom is asserted STRUCTURALLY — two definitions that are dependencies
// of a common parent and of neither each other — rather than by watching the map
// walk vary. Go leaves iteration order unspecified; it does not promise that
// repeated iterations DIFFER, so a control that demanded variety would be
// asserting a runtime property rather than a property of the fixture. The
// observed variety is logged instead, because it is informative and not a claim.
func TestEmissionFixtureCanExhibitMapOrdering(t *testing.T) {
	st := emissionFixture(t)
	h, ok := st.Resolve("fanout")
	if !ok {
		t.Fatal("fanout not in store")
	}
	order, err := emissionOrder(st, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Reachability over the emitted definitions, so "no edge between them" is
	// checked as "neither depends on the other, however indirectly".
	reaches := map[string]map[string]bool{}
	var reach func(x string) map[string]bool
	reach = func(x string) map[string]bool {
		if r, done := reaches[x]; done {
			return r
		}
		r := map[string]bool{}
		reaches[x] = r // cycles are impossible here, but do not hang if that changes
		d, err := st.GetDef(x)
		if err != nil {
			t.Fatal(err)
		}
		for dep := range collectDepsBody(d) {
			r[dep] = true
			for y := range reach(dep) {
				r[y] = true
			}
		}
		return r
	}
	// Only EMITTED definitions count. Datatypes are dependencies too and are
	// pairwise independent, so counting them would report freedom this backend
	// cannot express — a control that passes over a fixture with nothing to
	// permute, which is the failure it exists to prevent.
	emitted := map[string]bool{}
	for _, x := range order {
		emitted[x] = true
	}
	free := 0
	for _, parent := range order {
		d, err := st.GetDef(parent)
		if err != nil {
			t.Fatal(err)
		}
		var kids []string
		for dep := range collectDepsBody(d) {
			if emitted[dep] {
				kids = append(kids, dep)
			}
		}
		sort.Strings(kids)
		for i := range kids {
			for j := i + 1; j < len(kids); j++ {
				if !reach(kids[i])[kids[j]] && !reach(kids[j])[kids[i]] {
					free++
				}
			}
		}
	}
	if free == 0 {
		t.Fatalf("no definition in this fixture has two dependencies that are independent of "+
			"each other, so its emission order is forced and the byte-identity tests above are "+
			"not evidence of determinism (closure of %d)", len(order))
	}
	distinct := map[string]bool{}
	for i := 0; i < emissionN; i++ {
		distinct[strings.Join(mapOrderWalk(t, st, h), ",")] = true
	}
	t.Logf("%d independent sibling pairs; the pre-#168 walk produced %d distinct orders in %d draws",
		free, len(distinct), emissionN)

	// And the repaired walk must be the thing that collapses that freedom.
	fixed := map[string]bool{}
	for i := 0; i < emissionN; i++ {
		order, err := emissionOrder(st, h, nil)
		if err != nil {
			t.Fatal(err)
		}
		fixed[strings.Join(order, ",")] = true
	}
	if len(fixed) != 1 {
		t.Fatalf("emissionOrder produced %d distinct orders over %d draws; it is not a "+
			"function of the definitions", len(fixed), emissionN)
	}
}

// The other half of the control: the fixture must still contain an ALIASED
// definition inside the entry's closure, or the byte-identity tests stop
// witnessing the NameOf half of the repair.
//
// Ordering was the cause the issue named. It was not the only one — the emitted
// function name is built from Store.NameOf, which returned whichever alias Go's
// map iteration reached first — and a fixture where every hash has exactly one
// name would hide that completely while looking like a stronger test.
func TestEmissionFixtureContainsAnAliasedDefinition(t *testing.T) {
	st := emissionFixture(t)
	h, ok := st.Resolve("fanout")
	if !ok {
		t.Fatal("fanout not in store")
	}
	order, err := emissionOrder(st, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	aliased := ""
	for _, x := range order {
		n := 0
		for _, nh := range st.Names() {
			if nh == x {
				n++
			}
		}
		if n > 1 {
			aliased = x
		}
	}
	if aliased == "" {
		t.Fatal("no definition in the emission closure carries more than one name, so the " +
			"byte-identity tests cannot witness NameOf's contribution to emission stability")
	}
	first := st.NameOf(aliased)
	for i := 0; i < emissionN; i++ {
		if got := st.NameOf(aliased); got != first {
			t.Fatalf("NameOf returned %q then %q for one hash; the emitted function name "+
				"built from it is not a function of the store's contents", first, got)
		}
	}
}

// Dependencies before dependents, so the order is usable as an emission order
// and not merely a stable one. Sorting the hashes would also be deterministic
// and would be wrong.
func TestEmissionOrderIsDependencyFirst(t *testing.T) {
	st := emissionFixture(t)
	h, ok := st.Resolve("fanout")
	if !ok {
		t.Fatal("fanout not in store")
	}
	order, err := emissionOrder(st, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, x := range order {
		pos[x] = i
	}
	for _, x := range order {
		d, err := st.GetDef(x)
		if err != nil {
			t.Fatal(err)
		}
		for dep := range collectDepsBody(d) {
			if i, emitted := pos[dep]; emitted && i > pos[x] {
				t.Errorf("%s is emitted at %d but depends on %s at %d",
					st.NameOf(x), pos[x], st.NameOf(dep), i)
			}
		}
	}
	if pos[h] != len(order)-1 {
		t.Errorf("the entry is at position %d of %d; it depends on everything else, "+
			"so it must come last", pos[h], len(order))
	}
}

// A definition a backend lowers natively is PRUNED along with everything
// reachable only through it — not filtered out of a finished order.
//
// This is the difference that decides whether the consolidation is behaviour-
// preserving. The Go backend lowers `set-add` at its call sites, so the
// sorted-list helper it is defined in terms of must not be emitted either.
// Filtering one fixed order would emit that helper as dead code, or, if the
// filter also dropped its name, leave the emitted program calling a function
// nobody wrote.
func TestEmissionOrderPrunesNativeSubtrees(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(defn only-via-native [] [(x Int)] Int (+ x 1))`)
	put(t, st, `(defn lowered-natively [] [(x Int)] Int (only-via-native x))`)
	put(t, st, `(defn reached-directly [] [(x Int)] Int (+ x 2))`)
	put(t, st, `(defn top [] [(args (List Str))] Str
		(if (== (lowered-natively (reached-directly 1)) 4) "yes" "no"))`)

	names := func(hs []string) map[string]bool {
		m := map[string]bool{}
		for _, h := range hs {
			m[st.NameOf(h)] = true
		}
		return m
	}
	h, ok := st.Resolve("top")
	if !ok {
		t.Fatal("top not in store")
	}
	nativeHash, _ := st.Resolve("lowered-natively")

	full, err := emissionOrder(st, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := names(full)
	for _, want := range []string{"top", "lowered-natively", "only-via-native", "reached-directly"} {
		if !got[want] {
			t.Fatalf("with nothing native, %s must be emitted; got %v", want, sortedKeys(got))
		}
	}

	pruned, err := emissionOrder(st, h, map[string]bool{nativeHash: true})
	if err != nil {
		t.Fatal(err)
	}
	got = names(pruned)
	if got["lowered-natively"] {
		t.Error("a natively-lowered definition was emitted")
	}
	if got["only-via-native"] {
		t.Error("a helper reachable only through a natively-lowered definition was emitted; " +
			"the order was filtered rather than pruned, and the artifact carries dead code")
	}
	if !got["reached-directly"] {
		t.Error("pruning removed a definition that is reachable without going through the " +
			"natively-lowered one")
	}
	if !got["top"] {
		t.Error("the entry itself was pruned")
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// firstDiff reports the first differing line, so a failure names what moved
// instead of dumping two programs.
func firstDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			return "first difference at line " + strconv.Itoa(i+1) + ":\n  A: " + la[i] + "\n  B: " + lb[i]
		}
	}
	return "identical prefixes; lengths " + strconv.Itoa(len(la)) + " and " + strconv.Itoa(len(lb)) + " lines"
}
