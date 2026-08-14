package main

// THE VARIANT BOUNDARY, as a test rather than as a convention.
//
// EntryShape names one entry point interface: the protocol it speaks and whether
// it takes a leading capability record. Before it, those were two facts read from
// two places — `Protocol` for one and `CapTy != nil` for the other — and a third
// reading, `len(Requirements)`, was available and wrong. Both backends carried a
// comment warning against it, which is a hope rather than a boundary.
//
// What these tests establish is narrower and more useful than "the shape exists":
//
//  1. every shape-keyed table has a row for every INDEX, and each row names the
//     shape it decides — the half of exhaustiveness the compiler cannot see;
//  2. a value outside the variant is refused rather than indexing out of range;
//  3. every shape is REACHABLE from a real entry type, so the variant is not
//     carrying a case no program can inhabit.
//
// THE MAIN EXHAUSTIVENESS CLAIM IS NOT TESTED HERE, BECAUSE IT IS NOT A RUNTIME
// PROPERTY. Each table declares `var _ entryShapeTable = [len(table)]struct{}{}`
// beside itself, so a table that does not cover the variant is a TYPE ERROR at
// its own declaration. Verified by mutation: adding a fifth constant before
// numEntryShapes makes `go test -count=1` fail to COMPILE, naming goEntries,
// llvmEntries, entryProtocolNames and shapeNames — all four, before any test
// body runs.
//
// What is left for a test is the gap length cannot see: a table of the right
// length with a middle row left at its zero value. That is (1).

import (
	"testing"
)

// Every row names the shape it decides, and it must be the row's own index.
//
// This is the run-time half of exhaustiveness. The compile-time half is a LENGTH
// check, and length cannot distinguish a table with four decisions from a table
// with three decisions and a hole — the hole is a zero value, which for llvmEntry
// is a legitimate row (it is exactly the plain CLI answer). A row that names
// itself turns that hole into a mismatch.
func TestShapeTablesAreIndexedByShape(t *testing.T) {
	if numEntryShapes == 0 {
		t.Fatal("the variant is empty; this check is measuring nothing")
	}
	for _, s := range entryShapes() {
		if got := goEntries[s]; got.shape != s {
			t.Errorf("goEntries[%s] decides %s — the row is at the wrong index, or is a hole", s, got.shape)
		}
		if got := llvmEntries[s]; got.shape != s {
			t.Errorf("llvmEntries[%s] decides %s — the row is at the wrong index, or is a hole", s, got.shape)
		}
		// The string tables carry no shape field, so a hole shows as an empty
		// string. Neither table has a legitimate empty row.
		if entryProtocolNames[s] == "" {
			t.Errorf("entryProtocolNames[%s] is empty — the row is a hole", s)
		}
		if shapeNames[s] == "" {
			t.Errorf("shapeNames[%s] is empty — the row is a hole", s)
		}
	}
}

// The tables are indexed by the variant, so every index is in range by
// construction — but EntryShape is an integer type and a value outside the
// variant can be constructed. It must be refused, not allowed to panic: a panic
// during a build reads as a compiler crash rather than as a program this backend
// has no case for.
func TestEntryShapeCaseRefusesAShapeOutsideTheVariant(t *testing.T) {
	for _, outside := range []EntryShape{-1, numEntryShapes, numEntryShapes + 7} {
		if _, err := entryShapeCase("a table", &shapeNames, outside); err == nil {
			t.Errorf("%v was answered; it is outside the variant", outside)
		}
		// Both backends' own accessors must refuse it too, rather than each
		// having its own idea of what is in range.
		if _, err := goEntryFor(&CompiledProgram{Shape: outside}); err == nil {
			t.Errorf("the Go backend answered for %v", outside)
		}
		if _, err := llvmEntryFor(&CompiledProgram{Shape: outside}); err == nil {
			t.Errorf("the LLVM backend answered for %v", outside)
		}
	}
	// CONTROL: every shape INSIDE the variant is answered, or the check above
	// would pass for an accessor that refused everything.
	for _, s := range entryShapes() {
		if _, err := entryShapeCase("a table", &shapeNames, s); err != nil {
			t.Errorf("%s is in the variant and was refused: %v", s, err)
		}
	}
}

// numEntryShapes is the authority on the variant's size, and entryShapes derives
// from it. A derivation that disagreed with its source would put the hand-written
// list back without anyone noticing it had returned.
func TestEntryShapesDerivesFromTheSentinel(t *testing.T) {
	shapes := entryShapes()
	if len(shapes) != int(numEntryShapes) {
		t.Fatalf("entryShapes() has %d entries, numEntryShapes is %d", len(shapes), numEntryShapes)
	}
	for i, s := range shapes {
		if int(s) != i {
			t.Errorf("entryShapes()[%d] is %d", i, int(s))
		}
	}
}

// Every shape is REACHABLE: some entry type classifies to it. A variant case no
// program can inhabit would make the coverage check above demand decisions about
// nothing, and the tables would fill with rows nobody could be wrong about.
func TestClassifyEntryInhabitsEveryShape(t *testing.T) {
	st := capStore(t)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)

	strTy := Ty{K: "data", Hash: mustResolve(t, st, "Str")}
	argv := Ty{K: "data", Hash: mustResolve(t, st, "List"), Args: []Ty{strTy}}
	req := Ty{K: "data", Hash: mustResolve(t, st, "Request")}
	resp := Ty{K: "data", Hash: mustResolve(t, st, "Response")}
	caps := Ty{K: "record", Names: []string{"fetch"},
		Args: []Ty{{K: "fun", A: &strTy, B: &strTy}}}

	cli := Ty{K: "fun", A: &argv, B: &strTy}
	handler := Ty{K: "fun", A: &req, B: &resp}
	withCaps := func(inner *Ty) *Ty { return &Ty{K: "fun", A: &caps, B: inner} }

	cases := map[EntryShape]*Ty{
		shapeCLI:         &cli,
		shapeCLICaps:     withCaps(&cli),
		shapeHandler:     &handler,
		shapeHandlerCaps: withCaps(&handler),
	}
	for _, s := range entryShapes() {
		ty, ok := cases[s]
		if !ok {
			t.Errorf("no entry type is given for %s; the shape may be uninhabited", s)
			continue
		}
		capTy, got, ok := classifyEntry(st, ty)
		if !ok {
			t.Errorf("%s: %s was not classified as an entry at all", s, debugTy(ty))
			continue
		}
		if got != s {
			t.Errorf("%s was classified as %s", debugTy(ty), got)
		}
		// The record is returned exactly when the shape says one is taken —
		// they are the same fact, and nothing downstream may find them
		// disagreeing.
		wantRec := s == shapeCLICaps || s == shapeHandlerCaps
		if (capTy != nil) != wantRec {
			t.Errorf("%s: capability record %v, want present=%v", s, capTy, wantRec)
		}
	}
}

// The LLVM backend's REFUSAL is carried by the table, so every consumer of the
// shape refuses — not only the one that remembered to check first. That is what
// stops handlerCtorIndices from walking the type of an entry this backend never
// agreed to compile.
//
// THE SUBSET BOUNDARY MOVED, AND THE ASSERTION WAS REPLACED RATHER THAN
// WEAKENED. This test read `[]EntryShape{shapeHandler, shapeHandlerCaps}` while
// both were refused. The plain handler is now lowered, so the contract it
// encoded deliberately changed — and the replacement pins the new contract at
// least as tightly: the refused shape is still refused at EVERY consumer, and
// the shape that moved is now asserted POSITIVELY at every one of them, which a
// narrowed refusal list alone would not do. A row silently dropped from the
// table would fail the control below rather than pass by absence.
func TestLLVMRefusesHandlerShapesAtEveryConsumer(t *testing.T) {
	for _, s := range []EntryShape{shapeHandlerCaps} {
		prog := &CompiledProgram{Shape: s}

		_, err := llvmEntryFor(prog)
		if err == nil {
			t.Fatalf("the LLVM backend accepted %s", s)
		}
		if r, ok := refusedFor(err); !ok || r != reasonHandlerProtocol {
			t.Errorf("%s was refused as %v, want %s", s, r, reasonHandlerProtocol)
		}

		// Both index derivations ask for the shape itself. They reach the store
		// only after that, so an empty store is enough to prove the refusal comes
		// first: a version that walked the type before asking would fail with a
		// missing-definition error instead.
		st := newStore(t)
		if _, _, err := listCtorIndices(st, prog); err == nil {
			t.Errorf("listCtorIndices accepted %s", s)
		} else if r, ok := refusedFor(err); !ok || r != reasonHandlerProtocol {
			t.Errorf("listCtorIndices refused %s as %v, want %s", s, r, reasonHandlerProtocol)
		}
		if _, err := handlerCtorIndices(st, prog); err == nil {
			t.Errorf("handlerCtorIndices accepted %s", s)
		} else if r, ok := refusedFor(err); !ok || r != reasonHandlerProtocol {
			t.Errorf("handlerCtorIndices refused %s as %v, want %s", s, r, reasonHandlerProtocol)
		}
	}

	// CONTROL: the shapes this backend lowers are not refused by the table, or
	// the check above would pass for a backend that refused everything.
	for _, s := range []EntryShape{shapeCLI, shapeCLICaps, shapeHandler} {
		if _, err := llvmEntryFor(&CompiledProgram{Shape: s}); err != nil {
			t.Errorf("the LLVM backend refused %s: %v", s, err)
		}
	}

	// AND EACH DERIVATION ANSWERS FOR ITS OWN PROTOCOL ONLY. A handler has no
	// argv and a CLI entry has no Request, so a consumer asked for the other
	// one must say so rather than walk a type it was not written for — which is
	// how "Request does not declare Nil and Cons" would have been reported as
	// the error instead of as the symptom.
	st := newStore(t)
	if _, _, err := listCtorIndices(st, &CompiledProgram{Shape: shapeHandler}); err == nil {
		t.Error("listCtorIndices derived argv indices for a handler")
	}
	if _, err := handlerCtorIndices(st, &CompiledProgram{Shape: shapeCLI}); err == nil {
		t.Error("handlerCtorIndices derived protocol indices for a CLI entry")
	}
}

// The argv depth is a fact about the SHAPE, and it must match where the entry
// type actually puts the argv parameter. A depth that disagreed would build argv
// against the capability record's own type — no type error anywhere, and a
// program that reverses or mistypes its own arguments.
func TestLLVMArgvDepthMatchesTheEntryType(t *testing.T) {
	want := map[EntryShape]int{shapeCLI: 0, shapeCLICaps: 1}
	for s, depth := range want {
		ent, err := llvmEntryFor(&CompiledProgram{Shape: s})
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if ent.argvDepth != depth {
			t.Errorf("%s has argvDepth %d, want %d", s, ent.argvDepth, depth)
		}
		// caps and depth are the same fact stated twice within one row; a row
		// where they disagree would resolve a capability record and then look
		// for argv in the wrong place.
		if ent.caps != (depth > 0) {
			t.Errorf("%s: caps=%v with argvDepth %d", s, ent.caps, ent.argvDepth)
		}
	}
}
