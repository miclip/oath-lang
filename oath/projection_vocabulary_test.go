package main

import "testing"

// #194 SETTLED AS "NO CHANGE REQUIRED": a projection (`get`/printDef, the agent
// printSpec) is a NON-NORMATIVE human rendering, not source a consumer
// re-elaborates (SPEC §9). So a vocabulary whose rendering reads ambiguously is
// a cosmetic wart, not a second meaning for the stored object — the object is
// positional, and names are unhashed metadata.
//
// These tests pin the facts that make that disposition sound, for exactly the
// cases the issue measured. Each asserts three things:
//   - ADMISSION is preserved: the form is accepted (no blocklist was added);
//   - the stored object is POSITIONAL: which position the surface name resolved
//     to, and that the other position is simply unused rather than ambiguous;
//   - names are UNHASHED: respelling the vocabulary with non-colliding names
//     yields the same hash, so the object carries no trace of the collision.
// If a future change starts rejecting these, or hashing names, the settlement
// on #194 no longer describes the kernel and must be revisited deliberately.

func putOne(t *testing.T, st *Store, src string) (string, *Def, *Meta) {
	t.Helper()
	reps, err := apiPut(st, src, "author", "")
	if err != nil {
		t.Fatalf("%s: put failed: %v", src, err)
	}
	if len(reps) != 1 || reps[0].Status != "accepted" {
		t.Fatalf("%s: expected one accepted report, got %+v", src, reps)
	}
	def, err := st.GetDef(reps[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.GetMeta(reps[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	return reps[0].Hash, def, m
}

// Duplicate TERM parameters resolve RIGHTMOST: the innermost binder shadows.
func TestDuplicateTermParamsResolveRightmost(t *testing.T) {
	h, def, m := putOne(t, newMemStoreForTest(t), "(defn dup [] [(x Int) (x Int)] Int x)")
	if len(m.ParamNames) != 2 || m.ParamNames[0] != "x" || m.ParamNames[1] != "x" {
		t.Fatalf("duplicate vocabulary not stored verbatim: %v", m.ParamNames)
	}
	// Body is lam(lam(var k)); index 0 is the innermost (second, rightmost) param.
	b := def.Body
	if b == nil || b.K != "lam" || b.A == nil || b.A.K != "lam" || b.A.A == nil || b.A.A.K != "var" {
		t.Fatalf("unexpected body shape: %+v", b)
	}
	if b.A.A.Idx != 0 {
		t.Fatalf("x resolved to de Bruijn %d, want 0 (the rightmost parameter)", b.A.A.Idx)
	}
	// Control: the index discriminates — naming the FIRST param uniquely and
	// referring to it gives index 1, a different object.
	h1, _, _ := putOne(t, newMemStoreForTest(t), "(defn dup [] [(x Int) (y Int)] Int x)")
	if h1 == h {
		t.Fatal("referring to the leftmost param produced the same object; the test cannot see which position resolved")
	}
	// Names are unhashed: the rightmost-referring object is the same whatever
	// the unused leftmost binder is called.
	if h2, _, _ := putOne(t, newMemStoreForTest(t), "(defn dup [] [(y Int) (x Int)] Int x)"); h2 != h {
		t.Fatalf("respelling the unused binder changed the hash: %s vs %s", h2, h)
	}
}

// Duplicate TYPE variables resolve LEFTMOST; the other position is declared and unused.
func TestDuplicateTypeVarsResolveLeftmost(t *testing.T) {
	h, def, m := putOne(t, newMemStoreForTest(t), "(data DupC [a a] (MkD a))")
	if def.TyVars != 2 || len(m.TyVarNames) != 2 || m.TyVarNames[0] != "a" || m.TyVarNames[1] != "a" {
		t.Fatalf("want 2 type variables named [a a], got %d %v", def.TyVars, m.TyVarNames)
	}
	f := def.Ctors[0][0]
	if f.K != "var" || f.Var != 0 {
		t.Fatalf("field resolved to %+v, want type variable 0 (the leftmost)", f)
	}
	if h2, _, _ := putOne(t, newMemStoreForTest(t), "(data DupC [a b] (MkD a))"); h2 != h {
		t.Fatalf("respelling the unused type variable changed the hash: %s vs %s", h2, h)
	}
	// Control: referring to the rightmost position is a different object.
	if h3, _, _ := putOne(t, newMemStoreForTest(t), "(data DupC [b a] (MkD a))"); h3 == h {
		t.Fatal("referring to type variable 1 produced the same object; the test cannot see which position resolved")
	}
}

// A type variable spelled `Int` is admitted, and `Int` in the signature still
// means the builtin: builtins resolve before type variables, so the declared
// variable is an unused position, not a polymorphic parameter.
func TestTypeVarNamedIntLeavesAnUnusedPosition(t *testing.T) {
	st := newMemStoreForTest(t)
	h, def, m := putOne(t, st, "(defn intvar [Int] [(x Int)] Int x)")
	if def.TyVars != 1 || len(m.TyVarNames) != 1 || m.TyVarNames[0] != "Int" {
		t.Fatalf("want one type variable named Int, got %d %v", def.TyVars, m.TyVarNames)
	}
	if def.Ty == nil || def.Ty.K != "fun" || def.Ty.A.K != "int" || def.Ty.B.K != "int" {
		t.Fatalf("signature is not Int -> Int over the builtin: %+v", def.Ty)
	}
	if h2, _, _ := putOne(t, newMemStoreForTest(t), "(defn intvar [t] [(x Int)] Int x)"); h2 != h {
		t.Fatalf("respelling the unused type variable changed the hash: %s vs %s", h2, h)
	}
	// Control: the declared-but-unused position is still part of identity.
	if h3, _, _ := putOne(t, newMemStoreForTest(t), "(defn intvar [] [(x Int)] Int x)"); h3 == h {
		t.Fatal("dropping the type variable did not change the hash; TyVars is not being witnessed")
	}

	hd, dd, _ := putOne(t, newMemStoreForTest(t), "(data IntBox [Int] (MkIB Int))")
	if dd.TyVars != 1 || dd.Ctors[0][0].K != "int" {
		t.Fatalf("IntBox field should be the builtin Int with one unused type variable, got %d %+v", dd.TyVars, dd.Ctors[0][0])
	}
	if h2, _, _ := putOne(t, newMemStoreForTest(t), "(data IntBox [t] (MkIB Int))"); h2 != hd {
		t.Fatalf("respelling IntBox's unused type variable changed the hash")
	}

	// The projection is a rendering and is allowed to be lossy: it prints the
	// stored vocabulary verbatim and does not promise to re-elaborate. Assert
	// only that it renders — not that it round-trips, which is exactly what
	// #194 declined to promise.
	if s, err := printDef(st, h); err != nil || s == "" {
		t.Fatalf("printDef: %q %v", s, err)
	}
	if s, err := printSpec(st, h); err != nil || s == "" {
		t.Fatalf("printSpec: %q %v", s, err)
	}
}

// A type variable spelled like the datatype BEING DEFINED shadows it, so a
// declaration that READS as recursive is parametric. This is the most
// surprising instance of §1.4's order — a reader meeting `(data D [D] (MkD D))`
// will take the field for a recursive `D` — which is why SPEC §1.4 pins it by
// example and why it is witnessed here rather than left to the ordering rule.
func TestTypeVarNamedLikeItsOwnDatatypeShadowsIt(t *testing.T) {
	h, def, m := putOne(t, newMemStoreForTest(t), "(data SelfTV [SelfTV] (MkS SelfTV))")
	if def.TyVars != 1 || len(m.TyVarNames) != 1 || m.TyVarNames[0] != "SelfTV" {
		t.Fatalf("want one type variable named SelfTV, got %d %v", def.TyVars, m.TyVarNames)
	}
	f := def.Ctors[0][0]
	if f.K != "var" || f.Var != 0 {
		t.Fatalf("field resolved to %+v, want type variable 0 — the datatype must NOT win", f)
	}
	// The declaration is parametric: the same object as the ordinary spelling.
	if h2, _, _ := putOne(t, newMemStoreForTest(t), "(data SelfTV [t] (MkS t))"); h2 != h {
		t.Fatalf("respelling the type variable changed the hash: %s vs %s", h2, h)
	}
	// Control: a genuinely recursive field IS a different object, so this test
	// can tell "shadowed by the type variable" from "resolved to the datatype".
	h3, _, _ := putOne(t, newMemStoreForTest(t), "(data SelfTV [t] (MkS (SelfTV t)))")
	if h3 == h {
		t.Fatal("a recursive field produced the same object; the test cannot see which candidate won")
	}
}
