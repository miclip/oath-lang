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
