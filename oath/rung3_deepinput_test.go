package main

import (
	"math/big"
	"strings"
	"testing"
)

// TestRung3DeepInputIsIterative witnesses that rung 3's proof-half body rewrite
// does NOT descend admitted structural depth on the host stack (#149): a term
// and an inferred type deep enough to overflow a recursive walker must be
// handled without crashing. crossTypeRetypeBody (the term) and retypeTyBySub
// (the inferred TyArg) are both iterative; the old recursive forms overflow at
// this depth. The size stays under the 65,536-node admission cap, so it is a
// valid input. propHashGeneral's iterative body encoding is checked too.
func TestRung3DeepInputIsIterative(t *testing.T) {
	// depth chosen so the whole term stays a VALID ADMITTED input: the app chain
	// contributes 2*depth nodes (each app plus its int child) and the base ref's
	// deep TyArg another depth, so ~3*depth must stay under the maxCanonicalNodes
	// (65,536) admission cap — ~60,000 nodes, just under it, and still an order of
	// magnitude past the ~5,000 depth #149 showed overflows a recursive walker. So
	// it witnesses the iterative property for an input the parser actually admits.
	const depth = 20000
	if 3*depth >= maxCanonicalNodes {
		t.Fatalf("fixture depth %d exceeds the admission cap; it is not a valid input", depth)
	}

	// A deeply nested data type — (List (List ... Int)) — as the base ref's TyArg,
	// so retypeTyBySub must descend an admitted-depth TYPE (the path review flagged:
	// checkDef can infer such TyArgs from stored objects, unbounded by source). Its
	// Int leaf must be retyped to Rat by the substitution.
	deepArg := Ty{K: "int"}
	for i := 0; i < depth; i++ {
		deepArg = Ty{K: "data", Hash: strings.Repeat("2", 64), Args: []Ty{deepArg}}
	}
	// A left-nested chain of `app` nodes — crossTypeRetypeBody descends via t.A,
	// so this is exactly the shape a recursive copy blows the stack on.
	base := &Term{K: "ref", Hash: strings.Repeat("0", 64), TyArgs: []Ty{deepArg}}
	cur := base
	for i := 0; i < depth; i++ {
		cur = &Term{K: "app", A: cur, B: &Term{K: "int", Int: big.NewInt(1)}}
	}

	sub := crossTypeSub{"int": Ty{K: "rat"}}
	got := crossTypeRetypeBody(cur, sub) // must not overflow

	// Structure preserved and depth intact — a copy that silently truncated would
	// pass the no-crash check while corrupting the term.
	n := 0
	for p := got; p != nil && p.K == "app"; p = p.A {
		n++
	}
	if n != depth {
		t.Fatalf("iterative copy changed depth: got %d app nodes, want %d", n, depth)
	}
	// The base ref's TyArg was retyped Int->Rat by the substitution.
	leaf := got
	for leaf.K == "app" {
		leaf = leaf.A
	}
	if leaf.K != "ref" || len(leaf.TyArgs) != 1 {
		t.Fatalf("base ref lost its TyArg: %+v", leaf)
	}
	// Descend the retyped deep data chain to its leaf — it must be Rat now.
	rt := &leaf.TyArgs[0]
	rd := 0
	for rt.K == "data" {
		rt = &rt.Args[0]
		rd++
	}
	if rd != depth || rt.K != "rat" {
		t.Fatalf("deep TyArg not retyped iteratively: depth %d (want %d), leaf %s (want rat)", rd, depth, rt.K)
	}
	// The source must be untouched (deep-copy, not in-place): its base leaf stays Int.
	sc := &base.TyArgs[0]
	for sc.K == "data" {
		sc = &sc.Args[0]
	}
	if sc.K != "int" {
		t.Fatalf("source term was mutated: base TyArg leaf is now %s", sc.K)
	}

	// propHashGeneral over a prop whose body carries a deep TYPE must likewise not
	// recurse on the host stack: it encodes the body via the iterative enc.term /
	// enc.ty, so a right-nested data chain (List (List ... Int)) as a ctor TyArg
	// is encoded without host-stack growth. (find --spec's key stays binder-only;
	// the body is encoded verbatim, which is exactly what must not overflow.)
	// depth nested data nodes is also within the cap; ty() is already iterative.
	deepTy := Ty{K: "int"}
	for i := 0; i < depth; i++ {
		deepTy = Ty{K: "data", Hash: strings.Repeat("1", 64), Args: []Ty{deepTy}}
	}
	pr := Prop{
		Binders: []Ty{{K: "int"}},
		Body:    Term{K: "self", TyArgs: []Ty{deepTy}},
	}
	_ = propHashGeneral(&pr) // must not overflow
}
