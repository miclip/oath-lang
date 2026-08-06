package main

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

// linearSpine builds a Term chain exactly n nodes long. Each link is one node
// with exactly one child, so node count and depth coincide — which is what makes
// it the right shape for testing a LINEAR spine, the case the profile must treat
// as work rather than as syntactic nesting.
func linearSpine(n int) *Term {
	if n <= 0 {
		return nil
	}
	t := &Term{K: "int", Int: big.NewInt(0)}
	for i := 1; i < n; i++ {
		t = &Term{K: "prim", Op: "neg", Args: []Term{*t}}
	}
	return t
}

// defWithNodes builds a Def containing exactly n canonical nodes: one for the
// declared type plus a spine of n-1.
func defWithNodes(n int) *Def {
	return &Def{K: "func", Ty: &Ty{K: "int"}, Body: linearSpine(n - 1)}
}

func TestCountCanonicalNodesIsExact(t *testing.T) {
	// A hand-checkable case first, so the counter is not merely
	// self-consistent with the builder it is tested against.
	//   Ty{int}                                  1
	//   Body prim(+) with two int args           3
	//   Prop binder Ty{int}                      1
	//   Prop body prim(==) with two int args     3
	d := &Def{
		K: "func", Ty: &Ty{K: "int"},
		Body: &Term{K: "prim", Op: "+", Args: []Term{
			{K: "int", Int: big.NewInt(1)}, {K: "int", Int: big.NewInt(2)}}},
		Props: []Prop{{
			Binders: []Ty{{K: "int"}},
			Body: Term{K: "prim", Op: "==", Args: []Term{
				{K: "int", Int: big.NewInt(1)}, {K: "int", Int: big.NewInt(1)}}},
		}},
	}
	got, ok := countCanonicalNodes(d, maxCanonicalNodes)
	if !ok {
		t.Fatal("a tiny definition must be within the limit")
	}
	if want := 8; got != want {
		t.Fatalf("node count = %d, want %d — the counter is not visiting what it claims", got, want)
	}

	for _, n := range []int{1, 2, 10, 1000} {
		got, ok := countCanonicalNodes(defWithNodes(n), maxCanonicalNodes)
		if !ok || got != n {
			t.Errorf("defWithNodes(%d) counted %d (ok=%v), want %d", n, got, ok, n)
		}
	}
}

// TestCanonicalNodeAdmissionBoundary is the boundary the profile promises:
// exactly at the limit is admitted, one over is refused.
func TestCanonicalNodeAdmissionBoundary(t *testing.T) {
	if err := admitDef(defWithNodes(maxCanonicalNodes)); err != nil {
		t.Fatalf("exactly %d nodes must be ADMITTED, got %v", maxCanonicalNodes, err)
	}
	err := admitDef(defWithNodes(maxCanonicalNodes + 1))
	if err == nil {
		t.Fatalf("%d nodes must be REFUSED", maxCanonicalNodes+1)
	}
	var rl *resourceLimitErr
	if !errors.As(err, &rl) {
		t.Fatalf("refusal must be a typed resource limit, got %T: %v", err, err)
	}
	if rl.what != "canonical structure" || rl.limit != maxCanonicalNodes {
		t.Fatalf("refusal blames the wrong quantity: %+v", rl)
	}
	// It must NOT read as malformed input. An author whose program is fine
	// should not be told to fix their syntax or their types.
	for _, wrong := range []string{"syntax", "type", "parse", "malformed", "unexpected"} {
		if strings.Contains(strings.ToLower(err.Error()), wrong) {
			t.Errorf("resource refusal mentions %q, which misattributes the cause: %v", wrong, err)
		}
	}
}

// TestCountCanonicalNodesIsIterative is the substrate claim, and the reason the
// counter exists as a traversal rather than as four lines of recursion: a
// structure far deeper than any host stack tolerates must be COUNTABLE. A
// recursive counter would overflow while measuring whether a structure is too
// deep to walk — the check crashing on exactly the inputs it exists to refuse.
//
// 200,000 is an order of magnitude past the depth measured to throw a host
// exception out of oathCheck on wasm.
func TestCountCanonicalNodesIsIterative(t *testing.T) {
	deep := defWithNodes(200000)
	// Far over the limit, so this also exercises early exit.
	if _, ok := countCanonicalNodes(deep, maxCanonicalNodes); ok {
		t.Fatal("200,000 nodes must exceed the limit")
	}
	// And countable in full when the limit allows it — the traversal itself
	// must not be what fails.
	n, ok := countCanonicalNodes(deep, 1<<30)
	if !ok || n != 200000 {
		t.Fatalf("counted %d (ok=%v) over a 200,000-node spine, want an exact full count", n, ok)
	}
}

// TestCountCanonicalNodesEarlyExit pins the cost contract: refusing a structure
// far over the limit must cost at most limit+1 visits, or the admission check
// becomes its own denial of service. Measured by node count rather than by
// wall-clock, which would be flaky under load.
func TestCountCanonicalNodesEarlyExit(t *testing.T) {
	// A cheap proxy for "did not walk the whole thing": with a small limit the
	// counter must report failure, and it must do so without the exact count,
	// which is the documented signal that it abandoned the walk.
	n, ok := countCanonicalNodes(defWithNodes(100000), 10)
	if ok {
		t.Fatal("100,000 nodes must exceed a limit of 10")
	}
	if n != 0 {
		t.Fatalf("abandoned counts must report 0, got %d — callers must not read a partial count as exact", n)
	}
}

// TestCorpusFitsPortableProfile is the compatibility direction, and it is the
// half that a limit chosen from a crash threshold would fail. Every committed
// object must be comfortably admitted; a profile that refuses the corpus is a
// profile that changed the language.
func TestCorpusFitsPortableProfile(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Fatalf("could not open the committed store: %v", err)
	}
	names := st.Names()
	if len(names) < 100 {
		t.Fatalf("store resolved only %d names; this check did not run", len(names))
	}
	seen := map[string]bool{}
	worst, worstName := 0, ""
	for name, h := range names {
		if seen[h] {
			continue
		}
		seen[h] = true
		d, err := st.GetDef(h)
		if err != nil {
			t.Errorf("%s (%s) did not load under the profile: %v", name, shortHash(h), err)
			continue
		}
		n, ok := countCanonicalNodes(d, maxCanonicalNodes)
		if !ok {
			t.Errorf("%s exceeds the portable profile — the profile would change the language", name)
			continue
		}
		if n > worst {
			worst, worstName = n, name
		}
	}
	t.Logf("worst corpus definition: %s at %d nodes, %.1f%% of the %d-node profile",
		worstName, worst, 100*float64(worst)/float64(maxCanonicalNodes), maxCanonicalNodes)
	if worst*4 > maxCanonicalNodes {
		t.Errorf("the corpus uses more than a quarter of the profile (worst %d of %d) — "+
			"headroom this thin means ordinary growth will hit the limit", worst, maxCanonicalNodes)
	}
}

// TestGuardsAreDistinguished pins WHICH guard refuses each shape. They are all
// "an error" to a host, and collapsing them into one expected string would let
// the wrong guard satisfy the test — a nesting bound could silently start doing
// the node budget's job, or a syntax error could stand in for a resource
// refusal, and the suite would stay green while the claim quietly changed.
//
// Four distinct dispositions, one row each:
//
//	nested syntax over the limit    -> RESOURCE_LIMIT, syntax nesting
//	canonical structure over limit  -> RESOURCE_LIMIT, canonical structure
//	malformed source                -> an ordinary syntax error, NOT a resource limit
//	a long linear spine             -> ADMITTED (the open case; see #149)
func TestGuardsAreDistinguished(t *testing.T) {
	limitOf := func(err error) *resourceLimitErr {
		var rl *resourceLimitErr
		if errors.As(err, &rl) {
			return rl
		}
		return nil
	}

	// 1. Nesting guard.
	_, err := parseForms(strings.Repeat("(", maxSyntaxNesting+1) + strings.Repeat(")", maxSyntaxNesting+1))
	rl := limitOf(err)
	if rl == nil || rl.what != "syntax nesting" {
		t.Errorf("deep nesting must be refused by the NESTING guard, got %v", err)
	}

	// 2. Node-budget guard. Reached only through a structure that is shallow in
	// syntax, so this cannot be the nesting guard wearing a different label.
	err = admitDef(defWithNodes(maxCanonicalNodes + 1))
	rl = limitOf(err)
	if rl == nil || rl.what != "canonical structure" {
		t.Errorf("an oversized structure must be refused by the NODE guard, got %v", err)
	}

	// 3. Malformed source stays an ordinary syntax error. A resource refusal
	// here would tell an author their program is too big when it is broken.
	_, err = parseForms("(a (b")
	if err == nil {
		t.Error("unclosed input must be an error")
	} else if limitOf(err) != nil {
		t.Errorf("malformed source must NOT be a resource refusal, got %v", err)
	}

	// 4. THE OPEN CASE, asserted as it actually is rather than as we want it.
	// A long linear spine is shallow in syntax and well under the node budget,
	// so both guards correctly admit it — and the recursive walkers downstream
	// then overflow the host stack on wasm. This assertion documents the
	// boundary between what the profile claims and what it does not, and it
	// MUST BE INVERTED when the traversal substrate lands.
	spine := defWithNodes(5000)
	if err := admitDef(spine); err != nil {
		t.Errorf("a 5,000-node spine is inside the profile and must be admitted, got %v", err)
	}
	if _, err := parseForms(`(defn s [] [] Str "` + strings.Repeat("a", 5000) + `")`); err != nil {
		t.Errorf("a 5,000-rune literal is one syntax node and must parse, got %v", err)
	}
}

// TestEvalBoundaryIsExact. `eval` admits a bare Term, and the profile documents
// its limit as EXACTLY maxCanonicalNodes. An earlier version wrapped the term in
// Def{Ty: tInt(), Body: term}, so the synthetic type spent one node and eval's
// real limit was 65,535 — a documented boundary that was off by one.
//
// Found by external review of a fix written during external review, which is
// where confidence is highest and scrutiny lowest.
func TestEvalBoundaryIsExact(t *testing.T) {
	// linearSpine(n) is exactly n nodes, so these are the boundary itself.
	if err := admitTerm(linearSpine(maxCanonicalNodes)); err != nil {
		t.Errorf("a term of exactly %d nodes must be ADMITTED, got %v", maxCanonicalNodes, err)
	}
	if err := admitTerm(linearSpine(maxCanonicalNodes + 1)); err == nil {
		t.Errorf("a term of %d nodes must be REFUSED", maxCanonicalNodes+1)
	}
	// And the Def boundary is unchanged, so the two agree on the same number.
	if err := admitDef(defWithNodes(maxCanonicalNodes)); err != nil {
		t.Errorf("the Def boundary moved: %v", err)
	}
}
