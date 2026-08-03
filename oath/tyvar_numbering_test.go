package main

import "testing"

// SPEC §1.3: a TYPE variable's index is the 0-based POSITION of the parameter in
// its declaring definition's parameter list, left to right — NOT a de Bruijn
// index. Blind round 10 could not determine this from §1 and had to infer it.
//
// This checks the rule against the WHOLE COMMITTED CORPUS rather than against one
// convenient type: for every parameterized datatype the store holds, rebuild its
// declaration under positional numbering and require the stored hash back. Under
// the other reading (innermost-first, i.e. reversed) a two-parameter datatype
// hashes differently, so the reversed control must FAIL — otherwise the corpus
// contains no type that can tell the conventions apart and this test witnesses
// nothing.
func TestTypeVariableNumberingIsPositional(t *testing.T) {
	st, err := OpenStore("../codebase")
	if err != nil {
		t.Skip("committed store unavailable")
	}
	checked, discriminating := 0, 0
	for _, h := range st.AllHashes() {
		d, gerr := st.GetDef(h)
		if gerr != nil || d == nil || d.K != "data" || d.TyVars < 1 {
			continue
		}
		if got := hashDef(d); got != h {
			t.Errorf("%s: re-hashing the stored declaration gives %s", h[:12], got[:12])
			continue
		}
		checked++
		if d.TyVars < 2 {
			continue
		}
		// Reverse the parameter numbering: var i -> var (TyVars-1-i).
		rev := &Def{K: "data", TyVars: d.TyVars, Ctors: make([][]Ty, len(d.Ctors))}
		for ci, fs := range d.Ctors {
			rev.Ctors[ci] = make([]Ty, len(fs))
			for fi := range fs {
				rev.Ctors[ci][fi] = *renumberTyVars(&fs[fi], d.TyVars)
			}
		}
		// Counted, NOT asserted per datatype. A type whose parameters appear in no
		// field — `(data Phantom [a b] (Phantom))` — necessarily hashes the same
		// reversed, and that is valid rather than a violation of the convention.
		// What must hold is that the CORPUS contains at least one type able to
		// tell the two readings apart, which is checked after the loop.
		if hashDef(rev) != h {
			discriminating++
		}
	}
	if checked == 0 {
		t.Fatal("no parameterized datatype in the corpus — this test measured nothing")
	}
	if discriminating == 0 {
		t.Fatal("no datatype with 2+ parameters — nothing in the corpus can tell the two conventions apart")
	}
	t.Logf("%d parameterized datatypes reproduce under positional numbering; "+
		"%d have 2+ parameters and reject the reversed reading", checked, discriminating)
}

// THE ELABORATOR, which the corpus test above does not reach. That one re-hashes
// a STORED declaration, so it is independent of how source parameter NAMES were
// mapped to indices: regress lookupTyVar to innermost-first and the stored bytes
// change while the re-hash still agrees with them. It measures the encoder's
// determinism, not the convention.
//
// This measures the convention: elaborate `[a b]` from SOURCE and require that
// `a` became 0 and `b` became 1, in that order.
func TestElaboratorNumbersTypeParametersLeftToRight(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	d, err := st.GetDef(mustResolve(t, st, "Pair"))
	if err != nil {
		t.Fatal(err)
	}
	if d.TyVars != 2 || len(d.Ctors) != 1 || len(d.Ctors[0]) != 2 {
		t.Fatalf("unexpected shape: tyvars=%d ctors=%v", d.TyVars, d.Ctors)
	}
	// `a` is written first, so it must be index 0; `b` second, index 1.
	for i, f := range d.Ctors[0] {
		if f.K != "var" || f.Var != i {
			t.Fatalf("field %d elaborated to %s var %d — SPEC §1.3 requires the "+
				"0-based POSITION of the parameter, left to right, so this must be var %d",
				i, f.K, f.Var, i)
		}
	}

	// A three-parameter case, so "left to right" is distinguishable from any
	// convention that merely happens to agree on two.
	put(t, st, `(data Tri [x y z] (Tri z x y))`)
	d3, err := st.GetDef(mustResolve(t, st, "Tri"))
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 0, 1} // z, x, y
	for i, f := range d3.Ctors[0] {
		if f.K != "var" || f.Var != want[i] {
			t.Fatalf("Tri field %d is var %d, want var %d (x=0 y=1 z=2 by position)",
				i, f.Var, want[i])
		}
	}
}

func renumberTyVars(t *Ty, n int) *Ty {
	if t == nil {
		return nil
	}
	c := *t
	if c.K == "var" {
		c.Var = n - 1 - c.Var
	}
	if c.A != nil {
		c.A = renumberTyVars(c.A, n)
	}
	if c.B != nil {
		c.B = renumberTyVars(c.B, n)
	}
	if len(c.Args) > 0 {
		args := make([]Ty, len(c.Args))
		for i := range c.Args {
			args[i] = *renumberTyVars(&c.Args[i], n)
		}
		c.Args = args
	}
	return &c
}
