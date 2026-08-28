package main

import (
	"bytes"
	"math/big"
	"strings"
	"testing"
)

// THE E-GRAPH RUNG (#65 rung 1).
//
// Distributivity is the first rule with no confluent orientation, so these
// tests are about a MECHANISM as much as about a rule set: whether a definition
// written factored and one written expanded land in one equivalence class while
// keeping distinct identities, whether the unsound cases are still refused, and
// whether the search that decides it stops.
//
// THE CONTROLS ARE THE POINT. A mutant that distributes over EVERY type passes
// every collapse row here and is caught only by the Float rows; a mutant that
// collapses any two same-signature arithmetic bodies passes those too and is
// caught only by the semantic control.

// egDefs puts a pair of same-signature definitions and returns them.
func egPair(t *testing.T, st *Store, lhs, rhs string) (*Def, *Def) {
	t.Helper()
	put(t, st, "(defn eg-l [] "+lhs+")")
	put(t, st, "(defn eg-r [] "+rhs+")")
	return mustDef(t, st, "eg-l"), mustDef(t, st, "eg-r")
}

func TestFindEquivDistributivity(t *testing.T) {
	cases := []struct {
		name      string
		lhs, rhs  string
		wantEquiv bool
		why       string
	}{
		// --- the rule, both directions, at both exact numeric types --------
		{"int-distribute", `[(a Int) (b Int) (c Int)] Int (* a (+ b c))`,
			`[(a Int) (b Int) (c Int)] Int (+ (* a b) (* a c))`, true,
			"Int is ℤ: no overflow, so a*(b+c) and a*b+a*c agree on every input"},
		{"rat-distribute", `[(a Rat) (b Rat) (c Rat)] Rat (* a (+ b c))`,
			`[(a Rat) (b Rat) (c Rat)] Rat (+ (* a b) (* a c))`, true,
			"Rat is ℚ, exact, so the same argument holds"},
		{"int-distribute-right", `[(a Int) (b Int) (c Int)] Int (* (+ b c) a)`,
			`[(a Int) (b Int) (c Int)] Int (+ (* b a) (* c a))`, true,
			"the operand order is normalized away before the e-graph sees it"},
		{"int-distribute-three-addends", `[(a Int) (b Int) (c Int) (d Int)] Int (* a (+ b (+ c d)))`,
			`[(a Int) (b Int) (c Int) (d Int)] Int (+ (* a b) (+ (* a c) (* a d)))`, true,
			"an AC sum of any arity distributes; the chain is flattened, not matched pairwise"},
		{"int-factor-partial", `[(a Int) (b Int) (c Int) (q Int)] Int (+ q (* a (+ b c)))`,
			`[(a Int) (b Int) (c Int) (q Int)] Int (+ (+ (* a b) (* a c)) q)`, true,
			"the addend that shares no factor is carried along unchanged"},
		{"int-factor-into-two-merged-sums",
			`[(q Int) (b Int) (c Int) (y Int) (z Int)] Int (+ (* q (+ b c)) (* q (+ y z)))`,
			`[(q Int) (b Int) (c Int) (y Int) (z Int)] Int (* q (+ b (+ c (+ y z))))`, true,
			"THE ASSOCIATIVITY RULE'S WITNESS, and it takes TWO nested sums to see. " +
				"Factoring q out of the lhs builds `(b+c) + (y+z)`, a sum whose addends are " +
				"CLASSES that contain sums — a shape no term was written as, so " +
				"insertion-time flattening cannot reach it. Only the graph rule that " +
				"flattens a sum over a class containing a sum lets it meet `b+c+y+z`. " +
				"WITH ONE nested sum this row would pass with the rule deleted, because the " +
				"nested chain sorts LAST and right-nesting then reproduces the flat chain by " +
				"accident — measured, and the reason this row is shaped the way it is"},
		{"int-distribute-compound-factor",
			`[(a Int) (b Int) (c Int)] Int (* (+ a b) (+ a c))`,
			`[(a Int) (b Int) (c Int)] Int (+ (+ (* a a) (* a c)) (+ (* b a) (* b c)))`, true,
			"a product of two sums expands to four products and factors back"},

		// --- CONTROLS: unsound or simply false collapses -------------------
		{"float-distribute-control", `[(a Float) (b Float) (c Float)] Float (* a (+ b c))`,
			`[(a Float) (b Float) (c Float)] Float (+ (* a b) (* a c))`, false,
			"binary64 rounds: a*(b+c) and a*b+a*c differ on real inputs, so this must NOT collapse"},
		{"float-factor-control", `[(a Float) (b Float) (c Float) (q Float)] Float (+ q (* a (+ b c)))`,
			`[(a Float) (b Float) (c Float) (q Float)] Float (+ (+ (* a b) (* a c)) q)`, false,
			"the inverse direction is unsound over Float for the same reason"},
		{"wrong-factor-control", `[(a Int) (b Int) (c Int)] Int (* a (+ b c))`,
			`[(a Int) (b Int) (c Int)] Int (+ (* a b) (* b c))`, false,
			"a*b + b*c is a DIFFERENT function — the rule must not collapse arithmetic wholesale"},
		{"duplicated-addend-control", `[(a Int) (b Int) (c Int)] Int (* a (+ b c))`,
			`[(a Int) (b Int) (c Int)] Int (+ (* a b) (* a b))`, false,
			"a*b + a*b is 2ab; + is not idempotent and the AC multiset must not dedup"},
		{"subtraction-control", `[(a Int) (b Int) (c Int)] Int (* a (- b c))`,
			`[(a Int) (b Int) (c Int)] Int (+ (* a b) (* a c))`, false,
			"`-` carries no distributive rule here: a*(b-c) is not a*b + a*c"},

		// --- MIXED-TYPE BODY: the de-Bruijn/type concern raised during the build,
		// made a test. An Int distributable subterm and a Float one sit in one body.
		// The Int part must distribute; the Float part must NOT. An AC node's symbol
		// is type-qualified (egACSym carries the operand kind, synthesized in the
		// subterm's OWN context), so no cross-type e-class merge can carry the Int
		// rule onto the Float subterm — even if the two shared a de Bruijn shape.
		{"mixed-int-distributes-float-carried",
			`[(a Int) (b Int) (c Int) (x Float) (y Float) (z Float)] Bool (and (== (* a (+ b c)) 0) (== (* x (+ y z)) 0.0f))`,
			`[(a Int) (b Int) (c Int) (x Float) (y Float) (z Float)] Bool (and (== (+ (* a b) (* a c)) 0) (== (* x (+ y z)) 0.0f))`, true,
			"in a body with both, the Int product distributes while the Float subterm is carried unchanged"},
		{"mixed-float-must-not-distribute",
			`[(a Int) (b Int) (c Int) (x Float) (y Float) (z Float)] Bool (and (== (* a (+ b c)) 0) (== (* x (+ y z)) 0.0f))`,
			`[(a Int) (b Int) (c Int) (x Float) (y Float) (z Float)] Bool (and (== (* a (+ b c)) 0) (== (+ (* x y) (* x z)) 0.0f))`, false,
			"THE FLAGGED CASE: a spurious de-Bruijn/type merge would distribute the Float subterm too. The type-qualified symbol prevents it, so these must NOT collapse"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			l, r := egPair(t, st, tc.lhs, tc.rhs)

			// CONTROL FOR THE MEASUREMENT: distinct identities, or an eHash
			// comparison proves nothing.
			if hashDef(l) == hashDef(r) {
				t.Fatalf("%s: the two bodies already share an identity", tc.name)
			}
			got := eHash(st, l) == eHash(st, r)
			if got != tc.wantEquiv {
				verb := "should collapse"
				if !tc.wantEquiv {
					verb = "must NOT collapse"
				}
				t.Fatalf("%s: eHash equal = %v, want %v — %s (%s)\n  lhs: %s\n  rhs: %s",
					tc.name, got, tc.wantEquiv, verb, tc.why, tc.lhs, tc.rhs)
			}
		})
	}
}

// The command surface, not just the key: a factored and an expanded definition
// find each other through `find --equiv`, and a semantically different
// arithmetic body with the same signature is not reported.
func TestFindEquivDistributivityThroughTheCommand(t *testing.T) {
	st := newStore(t)
	put(t, st, `(defn ed-factored [] [(a Int) (b Int) (c Int)] Int (* a (+ b c)))`)
	put(t, st, `(defn ed-expanded [] [(a Int) (b Int) (c Int)] Int (+ (* a b) (* a c)))`)
	put(t, st, `(defn ed-other    [] [(a Int) (b Int) (c Int)] Int (+ (* a b) (* b c)))`)

	f, e := mustDef(t, st, "ed-factored"), mustDef(t, st, "ed-expanded")
	if hashDef(f) == hashDef(e) {
		t.Fatal("the two implementations must keep distinct identities")
	}

	out, err := apiFindEquiv(st, "ed-factored")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ed-expanded") {
		t.Fatalf("find --equiv ed-factored should surface ed-expanded:\n%s", out)
	}
	if strings.Contains(out, "ed-other") {
		t.Fatalf("find --equiv ed-factored must NOT surface ed-other (a different function):\n%s", out)
	}
	// And symmetrically, since a canonical form is not a one-way match.
	back, err := apiFindEquiv(st, "ed-expanded")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(back, "ed-factored") {
		t.Fatalf("find --equiv ed-expanded should surface ed-factored:\n%s", back)
	}
}

// THE PASS MUST BE INERT WHERE NO RULE CAN FIRE, and the witness is the
// COMMITTED CORPUS rather than a synthetic term — the claim is about every body
// `find --equiv` hashes, not about the shapes a test author thought of.
//
// `force` bypasses the syntactic pre-check, so this measures the e-graph itself
// (insert → congruence → saturate → extract) rather than the skip in front of
// it. Where saturation fired nothing, extraction must return the e-normalized
// bytes EXACTLY: an extractor that re-nested an AC chain differently, or
// dropped a type annotation while rebuilding, would move every eHash in the
// corpus and nothing else in this repository would notice.
func TestEgraphIsInertWithoutArithRulesOnTheCorpus(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Skip("no committed store")
	}
	names := st.Names()
	checked, fired := 0, 0
	for _, h := range names {
		d, err := st.GetDef(h)
		if err != nil || d.K != "func" || d.Body == nil {
			continue
		}
		chk := &checkerMachine{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
		norm := eNormalize(chk, nil, d.Body)
		want := termBytes(norm)
		out, g := eCanonicalArithBudget(chk, norm, egDefaultBudget, true)
		if g == nil {
			continue // over the size bound, or not well formed: nothing to compare
		}
		checked++
		if g.fired > 0 {
			fired++
			continue
		}
		if got := termBytes(out); !bytes.Equal(got, want) {
			t.Fatalf("%s: the e-graph moved a body no rule applies to (%d vs %d bytes) — "+
				"extraction is not faithful to the e-normalized form",
				h, len(got), len(want))
		}
	}
	if checked == 0 {
		t.Fatal("no corpus definition reached the e-graph — this test measured nothing")
	}
	t.Logf("corpus: %d bodies built an e-graph, %d had a rule fire", checked, fired)
}

// egFlatten must agree with acFlatten, which is the authority for what the
// leaves of an AC chain are. It exists only because acFlatten returns copied
// VALUES and the e-graph needs POINTERS (to look up the operand type
// synthesized for each node), so a divergence between them would be a second
// notion of "the same chain".
func TestEgFlattenAgreesWithACFlatten(t *testing.T) {
	mk := func(op string, depth int, shape string) *Term {
		leaves := make([]Term, depth)
		for i := range leaves {
			leaves[i] = Term{K: "int", Int: big.NewInt(int64(i))}
		}
		tm := acNest(op, leaves, shape)
		return &tm
	}
	for _, op := range []string{"+", "*", "and"} {
		for _, shape := range []string{"left", "right", "balanced"} {
			for _, depth := range []int{2, 3, 8, 64} {
				root := mk(op, depth, shape)
				want := acFlatten(op, root.Args)
				got := egFlatten(op, root)
				if len(got) != len(want) {
					t.Fatalf("%s/%s/%d: %d leaves, acFlatten says %d", op, shape, depth, len(got), len(want))
				}
				for i := range want {
					if !bytes.Equal(termBytes(got[i]), termBytes(&want[i])) {
						t.Fatalf("%s/%s/%d: leaf %d differs from acFlatten's", op, shape, depth, i)
					}
				}
			}
		}
	}
	// CONTROL: a chain of a DIFFERENT operator is one leaf, not three — without
	// this the agreement above would hold for a flatten that descended through
	// everything.
	plus := mk("+", 4, "right")
	if got := egFlatten("*", plus); len(got) != 1 {
		t.Fatalf("a `+` chain must be a single leaf of a `*` chain, got %d leaves", len(got))
	}
}

// CONGRUENCE, tested where it can be seen: two nodes that were NOT equal become
// equal because their children were merged. This is the closure that makes the
// e-graph more than a set of rewrite results, and nothing in the rule tests
// above can distinguish a graph that rebuilds from one that does not.
func TestEgraphRebuildRestoresCongruence(t *testing.T) {
	g := newEgraph(egDefaultBudget)
	leaf := func(name string) egClass {
		c, ok := g.add(egNode{sym: "t|" + name})
		if !ok {
			t.Fatal("budget spent on a leaf")
		}
		return c
	}
	x, y := leaf("x"), leaf("y")
	fx, _ := g.add(egNode{sym: "t|f", args: []egClass{x}})
	fy, _ := g.add(egNode{sym: "t|f", args: []egClass{y}})
	if g.find(fx) == g.find(fy) {
		t.Fatal("f(x) and f(y) must start in different classes")
	}
	g.union(x, y)
	g.rebuild()
	if g.find(fx) != g.find(fy) {
		t.Fatal("after x = y, congruence must put f(x) and f(y) in one class")
	}
	// CONTROL: a node over an UNMERGED child stays where it is, so the merge
	// above is congruence rather than a rebuild that collapses everything.
	z := leaf("z")
	fz, _ := g.add(egNode{sym: "t|f", args: []egClass{z}})
	g.rebuild()
	if g.find(fz) == g.find(fx) {
		t.Fatal("f(z) must not join f(x): z was never merged with x")
	}
}

// BUDGETS. The claim is narrow and has three parts, and only the first is about
// speed: saturation STOPS, it stops at the same place every time, and stopping
// early never invents an equivalence.
//
// The input is a product of sums, whose expansion is exponential in the number
// of factors — the shape that makes equality saturation unbounded in practice.
func TestEgraphBudgetsTerminateDeterministically(t *testing.T) {
	st := newStore(t)
	// (a1+b1) * (a2+b2) * (a3+b3) * (a4+b4): 16 products when fully expanded,
	// each of which can be re-factored in several ways.
	src := `(defn eg-blowup [] [(a1 Int) (b1 Int) (a2 Int) (b2 Int) (a3 Int) (b3 Int) (a4 Int) (b4 Int)] Int ` +
		`(* (+ a1 b1) (* (+ a2 b2) (* (+ a3 b3) (+ a4 b4)))))`
	put(t, st, src)
	d := mustDef(t, st, "eg-blowup")
	chk := func() *checkerMachine {
		return &checkerMachine{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
	}
	norm := eNormalize(chk(), nil, d.Body)

	run := func(b egBudget) ([]byte, *egraph) {
		out, g := eCanonicalArithBudget(chk(), norm, b, false)
		return termBytes(out), g
	}

	// A budget small enough to be spent. It must be REACHED — otherwise this
	// row measures an unbounded run that happened to be small.
	tiny := egBudget{nodes: 24, iters: 3}
	small, gs := run(tiny)
	if gs == nil || !gs.exhausted {
		t.Fatalf("the node budget was not reached, so this test does not measure a bounded run (graph=%v)", gs)
	}
	// Sound anyway: a truncated saturation still yields a term.
	if len(small) == 0 {
		t.Fatal("a spent budget must still produce a representative")
	}
	// DETERMINISM: the same input under the same budget gives the same bytes,
	// every time. Rule order, union order and extraction ties are all decided
	// without reading a Go map's iteration order, and this is what would catch
	// a regression in that.
	for i := 0; i < 8; i++ {
		again, _ := run(tiny)
		if !bytes.Equal(small, again) {
			t.Fatalf("run %d under a spent budget produced different bytes — saturation or extraction is not deterministic", i)
		}
	}

	// THE ITERATION BUDGET BOUNDS ROUNDS INDEPENDENTLY OF THE NODE BUDGET, and
	// the only way to show that is to make one round DIFFER from many. With
	// room for 100,000 nodes the node budget cannot be what stops either run,
	// so a one-round result that differs from the multi-round result can only
	// be the round cap acting — and a one-round result that MATCHED would mean
	// this row proves nothing, since saturation would have converged before the
	// cap was ever consulted.
	// A SEPARATE, SMALLER TERM, because the blowup above spends any node budget
	// it is given and would make this a measurement of the NODE cap wearing the
	// round cap's name. This one reaches its fixpoint in three rounds inside 29
	// e-nodes, so with room for 100,000 nodes the only thing that can stop
	// either run below is the round limit.
	put(t, st, `(defn eg-rounds [] [(q Int) (b Int) (c Int) (y Int) (z Int)] Int `+
		`(+ (* q (+ b c)) (* q (+ y z))))`)
	dr := mustDef(t, st, "eg-rounds")
	chkR := func() *checkerMachine {
		return &checkerMachine{st: st, selfTyVars: dr.TyVars, selfTy: dr.Ty}
	}
	normR := eNormalize(chkR(), nil, cloneTerm(dr.Body))
	runR := func(b egBudget) ([]byte, *egraph) {
		out, g := eCanonicalArithBudget(chkR(), normR, b, false)
		return termBytes(out), g
	}
	manyRounds, gm := runR(egBudget{nodes: 100000, iters: 12})
	oneRound, g1 := runR(egBudget{nodes: 100000, iters: 1})
	if gm == nil || g1 == nil {
		t.Fatal("expected a graph")
	}
	if gm.exhausted || g1.exhausted {
		t.Fatalf("a 100,000-node budget was spent (12 rounds: %v, 1 round: %v), so the comparison "+
			"below would be measuring the NODE cap, not the round cap", gm.exhausted, g1.exhausted)
	}
	// THE ROUND CAP MUST BE OBSERVABLE, or this row asserts nothing: a term that
	// saturates in one round produces identical output either way and would
	// pass with the iteration limit deleted.
	if bytes.Equal(oneRound, manyRounds) {
		t.Fatalf("one round and twelve rounds extracted the same term, so this term saturates in a "+
			"single round and the iteration cap is untested by it (%d vs %d e-nodes)",
			len(g1.nodes), len(gm.nodes))
	}
	if g1.fired >= gm.fired || len(g1.nodes) >= len(gm.nodes) {
		t.Errorf("one round fired %d times over %d e-nodes and twelve fired %d over %d — "+
			"a capped saturation must do strictly LESS",
			g1.fired, len(g1.nodes), gm.fired, len(gm.nodes))
	}
	t.Logf("round cap: 1 round = %d e-nodes (%d fired), 12 rounds = %d e-nodes (%d fired), different terms",
		len(g1.nodes), g1.fired, len(gm.nodes), gm.fired)
	for i := 0; i < 4; i++ {
		again, _ := runR(egBudget{nodes: 100000, iters: 1})
		if !bytes.Equal(oneRound, again) {
			t.Fatal("a one-round saturation is not deterministic")
		}
	}

	// And the default budget is deterministic too.
	full, gf := run(egDefaultBudget)
	if gf == nil {
		t.Fatal("expected a graph under the default budget")
	}
	for i := 0; i < 4; i++ {
		again, _ := run(egDefaultBudget)
		if !bytes.Equal(full, again) {
			t.Fatal("the default budget run is not deterministic")
		}
	}
	t.Logf("blowup: %d nodes under the default budget (exhausted=%v)", len(gf.nodes), gf.exhausted)

	// SOUNDNESS UNDER TRUNCATION: a spent budget must not merge two different
	// functions.
	//
	// BOTH SIDES ARE HASHED UNDER THE SAME EXHAUSTED TINY BUDGET, which is the
	// only version of this that tests what it claims. Comparing them at the
	// DEFAULT budget would be a statement about a saturation that ran to
	// completion — the case truncation is not involved in at all — while the
	// hazard is that two graphs, each cut off mid-search, land on the same
	// representative by accident.
	put(t, st, `(defn eg-blowup2 [] [(a1 Int) (b1 Int) (a2 Int) (b2 Int) (a3 Int) (b3 Int) (a4 Int) (b4 Int)] Int `+
		`(* (+ a1 b1) (* (+ a2 b2) (* (+ a3 b3) (+ a4 a4)))))`)
	d2 := mustDef(t, st, "eg-blowup2")
	norm2 := eNormalize(chk(), nil, cloneTerm(d2.Body))
	out2, g2 := eCanonicalArithBudget(chk(), norm2, tiny, false)
	if g2 == nil || !g2.exhausted {
		t.Fatalf("the control definition did not spend the tiny budget, so this is not a truncation test (graph=%v)", g2 != nil)
	}
	if bytes.Equal(small, termBytes(out2)) {
		t.Fatal("two different products of sums extracted to the same term under a truncated " +
			"saturation — cutting the search short must lose equivalences, never invent one")
	}
	// CONTROL ON THE CONTROL: the two are also distinct at the default budget,
	// so the row above is about truncation rather than about a pair the engine
	// could never have merged anyway.
	if eHash(st, d) == eHash(st, d2) {
		t.Fatal("two different products of sums must not share an eHash")
	}
}

// eHash is a function of the DEFINITION, not of the store it was read from or
// of how many times it has been called. The e-graph allocates class ids in
// insertion order, so an extractor that broke ties on those ids would pass
// every collapse test above and still make this fail.
func TestEHashIsStableAcrossStoresAndRepeats(t *testing.T) {
	body := `[(a Int) (b Int) (c Int)] Int (+ (* a b) (* a c))`
	st1 := newStore(t)
	put(t, st1, "(defn eg-s1 [] "+body+")")
	d1 := mustDef(t, st1, "eg-s1")

	// A second store where OTHER definitions were put first, so nothing about
	// insertion order matches the first.
	st2 := newStore(t)
	put(t, st2, `(defn eg-filler-a [] [(x Int)] Int (+ x 1))`)
	put(t, st2, `(defn eg-filler-b [] [(x Int) (y Int)] Int (* x (+ y 2)))`)
	put(t, st2, "(defn eg-s2 [] "+body+")")
	d2 := mustDef(t, st2, "eg-s2")

	h1, h2 := eHash(st1, d1), eHash(st2, d2)
	if h1 != h2 {
		t.Fatalf("the same body hashed differently in two stores:\n  %s\n  %s", h1, h2)
	}
	for i := 0; i < 4; i++ {
		if again := eHash(st1, d1); again != h1 {
			t.Fatalf("repeat %d moved the eHash: %s vs %s", i, again, h1)
		}
	}
}

// THE PRE-CHECK MUST HAVE NO FALSE NEGATIVES, which is the only direction that
// can change an answer. eCanonicalArith skips the graph entirely when no
// distributivity or factoring match exists syntactically; if that skip were
// wrong, definitions would fail to be found equivalent for a reason nothing
// reports. Forcing the graph on the same bodies must produce the same bytes.
//
// It is CONSERVATIVE, not exact: false POSITIVES are expected and cost only a
// wasted graph — the Float row below is one, admitted by a type-blind
// pre-check and refused later by the annotation.
func TestEgraphPreCheckAgreesWithRunningTheGraph(t *testing.T) {
	st := newStore(t)
	bodies := []string{
		`[(a Int) (b Int)] Int (+ a b)`,
		`[(a Int) (b Int)] Int (* a b)`,
		`[(a Int) (b Int) (c Int)] Int (+ (* a b) c)`,
		`[(a Bool) (b Bool)] Bool (and a (or b a))`,
		`[(a Float) (b Float) (c Float)] Float (* a (+ b c))`,
		`[(a Int) (b Int) (c Int)] Int (- (* a b) (* a c))`,
		`[(a Int)] Int (if (== a 0) (+ a 1) (* a a))`,
	}
	for i, b := range bodies {
		name := "eg-pc-" + string(rune('a'+i))
		put(t, st, "(defn "+name+" [] "+b+")")
		d := mustDef(t, st, name)
		chk := func() *checkerMachine {
			return &checkerMachine{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
		}
		norm := eNormalize(chk(), nil, d.Body)
		skipped, _ := eCanonicalArithBudget(chk(), norm, egDefaultBudget, false)
		forced, _ := eCanonicalArithBudget(chk(), norm, egDefaultBudget, true)
		if !bytes.Equal(termBytes(skipped), termBytes(forced)) {
			t.Fatalf("%s: the pre-check skipped a body the e-graph would have changed:\n  %s", name, b)
		}
	}
}

// EQUAL COST IS NOT A RARE CORNER, AND IT IS WHERE TWO OF THE MECHANISMS SHOW.
// `a*b + a*c + d*b` has two DIFFERENT cheapest factorings — `a*(b+c) + d*b` and
// `b*(a+d) + a*c` — so its class holds two minimum-cost terms and the extractor
// has to choose. Three definitions, one written each way, must all land on the
// same one.
//
// TWO MUTANTS DIE HERE, measured rather than asserted:
//
//   - TIE BROKEN ON CLASS ID instead of canonical bytes. Ids are assigned in
//     insertion order, which differs between the three spellings, so the
//     expanded form and the first factoring stop agreeing.
//   - NO DISTRIBUTION. Factoring alone carries the expanded form to both
//     factorings, but neither factored form can reach the other without
//     expanding first, so the two factorings stop agreeing.
//
// Nothing else in this file distinguishes either one, because every other row
// has a single cheapest form.
func TestFindEquivEqualCostFactoringsAgree(t *testing.T) {
	st := newStore(t)
	sig := `[(a Int) (b Int) (c Int) (d Int)] Int `
	put(t, st, `(defn ec-expanded  [] `+sig+`(+ (* a b) (+ (* a c) (* d b))))`)
	put(t, st, `(defn ec-factor-a  [] `+sig+`(+ (* a (+ b c)) (* d b)))`)
	put(t, st, `(defn ec-factor-b  [] `+sig+`(+ (* b (+ a d)) (* a c)))`)

	names := []string{"ec-expanded", "ec-factor-a", "ec-factor-b"}
	hashes := map[string]string{}
	ids := map[string]string{}
	for _, n := range names {
		d := mustDef(t, st, n)
		hashes[n] = eHash(st, d)
		ids[n] = hashDef(d)
	}
	for i := 1; i < len(names); i++ {
		if ids[names[0]] == ids[names[i]] {
			t.Fatalf("%s and %s share an identity — the comparison below is vacuous", names[0], names[i])
		}
		if hashes[names[0]] != hashes[names[i]] {
			t.Fatalf("%s and %s are the same function and must share an eHash:\n  %s\n  %s",
				names[0], names[i], hashes[names[0]], hashes[names[i]])
		}
	}
	// CONTROL: one factor changed, so this is NOT the same function and must
	// not join them however the ties fall.
	put(t, st, `(defn ec-different [] `+sig+`(+ (* a b) (+ (* a c) (* d c))))`)
	if eHash(st, mustDef(t, st, "ec-different")) == hashes["ec-expanded"] {
		t.Fatal("a*b + a*c + d*c is a different function from a*b + a*c + d*b")
	}
}

// CONGRUENCE CLOSURE IS AN INVARIANT OF THE SATURATED GRAPH, and stating it
// that way is what makes it testable without pinning a node count: when
// saturation returns, no two distinct classes may hold nodes with the same
// canonical key. Two such nodes ARE the same value — same symbol, same child
// classes — so leaving them apart means the graph has stopped representing an
// equivalence it has already derived.
//
// The mutant: drop the rebuild between saturation rounds. Unions made during a
// round then never propagate upward, duplicates accumulate (measured: 151 nodes
// instead of 138 on the term below), and later rounds match against a graph
// that disagrees with its own union-find.
func TestEgraphIsCongruenceClosedAfterSaturation(t *testing.T) {
	st := newStore(t)
	// The two products' first factors are written differently and become one
	// class only after factoring — so this term forces real merging rather
	// than the congruence insertion already gives for free.
	put(t, st, `(defn cc-merge [] [(a Int) (b Int) (c Int) (q Int) (r Int)] Int `+
		`(+ (* (* a (+ b c)) q) (* (+ (* a b) (* a c)) r)))`)
	d := mustDef(t, st, "cc-merge")
	chk := &checkerMachine{st: st, selfTyVars: d.TyVars, selfTy: d.Ty}
	_, g := eCanonicalArithBudget(chk, eNormalize(chk, nil, d.Body), egDefaultBudget, false)
	if g == nil {
		t.Fatal("expected an e-graph")
	}
	if g.exhausted {
		t.Fatal("the budget was spent, so the graph was never saturated and this proves nothing")
	}
	if g.fired == 0 {
		t.Fatal("no rule fired, so nothing was ever merged and congruence is untested")
	}
	owner := map[string]egClass{}
	for i := range g.nodes {
		k := g.key(g.canon(g.nodes[i]))
		c := g.find(g.nodeCls[i])
		if prev, ok := owner[k]; ok && prev != c {
			t.Fatalf("two classes (%d, %d) hold the same node %q — the saturated graph is not congruence closed", prev, c, k)
		}
		owner[k] = c
	}
}

// THE PRE-CHECK MUST SCALE, AND IT MUST NOT PAY BEFORE IT CHECKS THE CAP.
//
// `find --equiv` hashes every definition in the store, and the portable profile
// admits terms with tens of thousands of nodes — so a survey that walks each AC
// chain once per LEVEL is quadratic on a term the kernel accepts, and doing that
// work BEFORE consulting the 2048-node cap means the cap never gets to save
// anyone. Both halves were real: found by external review, on the first version
// of this file.
//
// MEASURED BY ALLOCATION AND SCALING, not by wall-clock, following #151's
// existing idiom: a quadratic survey's cost grows ~4x per doubling and a linear
// one ~2x, so a 3x ceiling separates them without pinning a machine-specific
// number. The chains here are DELIBERATELY ABOVE the node cap, because that is
// the case where the old ordering was worst — the survey ran in full and the
// caller then threw the answer away.
func TestEgraphSurveyScalesOnDeepChainsAboveTheCap(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Skip("no committed store")
	}
	depths := []int{4000, 8000, 16000}
	for _, c := range []struct {
		op   string
		what string
	}{
		{"+", "a sum chain: the factoring pre-check"},
		{"*", "a product chain: the distributivity pre-check"},
	} {
		t.Run(c.op, func(t *testing.T) {
			var got []uint64
			for _, d := range depths {
				term := acChain(c.op, d, 1, tInt)
				// CONTROL ON THE SETUP: the series must be inside the portable
				// profile, or this measures a term the kernel would refuse
				// anyway — and it must be ABOVE egMaxTermNodes, or it measures
				// the path that was never at risk.
				if _, ok := countCanonicalNodes(&Def{K: "func", Body: term}, maxCanonicalNodes); !ok {
					t.Fatalf("setup: a depth-%d chain is outside the portable profile", d)
				}
				if n, _, _ := egSurvey(term); n <= egMaxTermNodes {
					t.Fatalf("setup: a depth-%d chain reported %d nodes, at or under the %d cap — "+
						"this test is measuring the wrong path", d, n, egMaxTermNodes)
				}
				got = append(got, acAllocBytes(func() {
					chk := &checkerMachine{st: st}
					_, g := eCanonicalArithBudget(chk, eNormalize(chk, nil, term), egDefaultBudget, false)
					if g != nil {
						t.Fatalf("a term above the cap must skip the e-graph entirely")
					}
				}))
			}
			for i := range depths {
				ratio := 0.0
				if i > 0 {
					ratio = float64(got[i]) / float64(got[i-1])
				}
				t.Logf("depth %6d  %9d KB  x%.2f  (%s)", depths[i], got[i]>>10, ratio, c.what)
			}
			for i := 1; i < len(depths); i++ {
				if ratio := float64(got[i]) / float64(got[i-1]); ratio > 3.0 {
					t.Errorf("doubling the chain from %d to %d multiplied allocation by %.2f "+
						"(%d KB -> %d KB) — the e-graph pre-check is quadratic in chain depth, so "+
						"`find --equiv` is resource-exhaustible on a term the profile admits",
						depths[i-1], depths[i], ratio, got[i-1]>>10, got[i]>>10)
				}
			}
		})
	}
}

// egRedexBlocks builds the shape the ENGINE is exhaustible on, as distinct from
// the shape the PRE-CHECK is: k summed blocks `a*(b+c)`, each over its own three
// variables, beneath a spine of Int binders that makes the operand types
// synthesizable.
//
// Distinct variables per block are load-bearing. Reusing one variable would make
// every block hash-cons to the SAME e-node, so the graph would stop growing with
// k and the measurement below would be of a constant — a vacuous witness that
// looks like a linear one.
func egRedexBlocks(k int) *Term {
	v := func(i int) Term { return Term{K: "var", Idx: i} }
	block := func(i int) Term {
		a, b, c := v(3*i), v(3*i+1), v(3*i+2)
		sum := Term{K: "prim", Op: "+", Args: []Term{b, c}}
		return Term{K: "prim", Op: "*", Args: []Term{a, sum}}
	}
	body := block(k - 1)
	for i := k - 2; i >= 0; i-- {
		body = Term{K: "prim", Op: "+", Args: []Term{block(i), body}}
	}
	out := &body
	for i := 0; i < 3*k; i++ {
		out = &Term{K: "lam", Ty: tInt(), A: out}
	}
	return out
}

// THE OTHER HALF OF THE SCALING CLAIM: below the cap, where the engine actually
// RUNS.
//
// TestEgraphSurveyScalesOnDeepChainsAboveTheCap measures the skip path — a term
// too big for the e-graph, where the only work is deciding not to do any. That
// leaves the case the budgets exist for entirely unmeasured: a term UNDER the
// cap, dense with distributivity redexes, where insertion, saturation and
// extraction all run. Equality saturation is exponential in general, so "the
// budget bounds it" is a claim about behaviour, not a property anyone can read
// off the code.
//
// TWO ASSERTIONS, AND THEY WITNESS DIFFERENT THINGS. Conflating them is the
// trap this test walked into on its first version, which asserted only the
// ratio and was described as measuring the bound:
//
//	THE BOUND      every graph holds at most `budget.nodes` e-nodes. This is
//	               the literal claim, it is checked directly, and a saturation
//	               that ignored the budget fails it on the first row.
//	THE SCALING    doubling the redex count must not square the cost. This
//	               covers the parts that scale with the TERM — survey,
//	               annotate, insert, extract — and NOT saturation, which the
//	               budget has already pinned to a constant node count.
//
// **THE RATIO DOES NOT WITNESS THE BUDGET, measured rather than assumed.** Re-run
// under a 25x budget (200,000 nodes) the ratios stay in the same band — x0.74,
// x2.32 — while absolute cost goes from 60 MB to 2.7 GB at the smallest size.
// So a missing bound is invisible to a scaling test and visible to the node
// count. Do not read the ratio rows as evidence that saturation is bounded.
//
// THE CONTROLS RULE OUT THE WAYS THIS COULD PASS WHILE MEASURING NOTHING: the
// terms must be UNDER the cap (or this is the skip path again), the e-graph must
// be BUILT (or the pre-check skipped it), a rule must FIRE (or saturation
// returned on round one), and some row must actually REACH the budget (or the
// bound was never tested).
func TestEgraphEngineCostIsBoundedBelowTheCap(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Skip("no committed store")
	}
	blocks := []int{25, 50, 100, 200}
	var got []uint64
	var nodes []int
	var exhausted []bool
	for _, k := range blocks {
		term := egRedexBlocks(k)
		n, arith, wellFormed := egSurvey(term)
		if n > egMaxTermNodes {
			t.Fatalf("setup: %d blocks is %d nodes, over the %d cap — this measures the skip path", k, n, egMaxTermNodes)
		}
		if !arith || !wellFormed {
			t.Fatalf("setup: %d blocks reported arith=%v wellFormed=%v — the pre-check would skip it", k, arith, wellFormed)
		}
		var g *egraph
		got = append(got, acAllocBytes(func() {
			chk := &checkerMachine{st: st}
			_, g = eCanonicalArithBudget(chk, eNormalize(chk, nil, term), egDefaultBudget, false)
		}))
		if g == nil {
			t.Fatalf("setup: %d blocks built no e-graph, so nothing here was measured", k)
		}
		if g.fired == 0 {
			t.Fatalf("setup: %d blocks fired no rule — saturation returned immediately and this measures insertion only", k)
		}
		nodes = append(nodes, len(g.nodes))
		exhausted = append(exhausted, g.exhausted)
	}
	for i, k := range blocks {
		ratio := 0.0
		if i > 0 {
			ratio = float64(got[i]) / float64(got[i-1])
		}
		t.Logf("%4d redex blocks  %8d KB  x%.2f  (%d e-nodes, budget spent=%v)",
			k, got[i]>>10, ratio, nodes[i], exhausted[i])
	}

	// THE BOUND.
	for i, k := range blocks {
		if nodes[i] > egDefaultBudget.nodes {
			t.Errorf("%d redex blocks produced %d e-nodes, over the %d-node budget — "+
				"saturation is not bounded by the limit it declares",
				k, nodes[i], egDefaultBudget.nodes)
		}
	}
	// AND THE BOUND MUST HAVE BEEN REACHED SOMEWHERE, or every row sat far below
	// it and the check above is satisfied by arithmetic rather than by the guard.
	spent := false
	for _, e := range exhausted {
		spent = spent || e
	}
	if !spent {
		t.Errorf("no row reached the %d-node budget, so this series never exercised the bound "+
			"it claims to measure — largest graph was %d nodes", egDefaultBudget.nodes, nodes[len(nodes)-1])
	}

	// THE SCALING of everything the budget does NOT pin.
	for i := 1; i < len(blocks); i++ {
		if ratio := float64(got[i]) / float64(got[i-1]); ratio > 3.0 {
			t.Errorf("doubling the redex count from %d to %d multiplied allocation by %.2f "+
				"(%d KB -> %d KB) — the term-proportional work (survey, annotate, insert, "+
				"extract) is superlinear in redex density on a term the pre-check admits",
				blocks[i-1], blocks[i], ratio, got[i-1]>>10, got[i]>>10)
		}
	}
}
