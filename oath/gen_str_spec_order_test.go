package main

import (
	"fmt"
	"testing"
)

// SPEC §4's `Str` ENTRY IS DERIVABLE — the witness for #163's repair.
//
// SCOPE, SO THE TITLE IS NOT OVERREAD: this witnesses that the rewritten entry
// determines the generated VALUE and the DRAW COUNT. What it cannot witness is
// listed under "the value alone is not enough" below.
//
// §4 says draw order is NORMATIVE, so a reader must be able to reconstruct what
// a kernel does for a `Str` rather than merely the set of values it can produce.
// #163's repair rewrote §4's `Str` entry to delegate to the `Data` rule and to
// state that `Data` WINS. This asserts the rewritten text is worth the paper, to
// the extent a black-box comparison can:
//
//	CLAIM     an implementation of §4's `Str` entry, written from the
//	          specification text and nothing else, produces the same VALUE and
//	          consumes the same NUMBER OF DRAWS as `genValue` does for `Str`.
//	UNIVERSE  every size in the clamped range the tester can reach (negative
//	          sizes included, since §4 clamps rather than rejecting them),
//	          across many independent rng seeds.
//
// THE VALUE ALONE IS NOT ENOUGH, AND THE RNG STATE IS NOT AS STRONG AS IT LOOKS.
// Two different draw sequences can land on the same string, so equal values
// would not establish anything about ORDER — the property §4 calls normative.
// The rng is splitmix64 over one uint64 of state that advances by a CONSTANT per
// call, so an equal final state establishes an equal DRAW COUNT and nothing
// about which primitive or which modulus each draw used. Stated exactly, and no
// more than this:
//
//	value equality over 4000 seeds per size    constrains every modulus whose
//	                                           outcome CHANGES the value —
//	                                           `below(2)` at positive size picks
//	                                           SNil vs SCons, and `below(7)` there
//	                                           would shift that split
//	draw-count equality                        constrains the NUMBER of draws,
//	                                           which is what catches a derivation
//	                                           that skips the forced size-0
//	                                           selection
//	neither                                    constrains the modulus at a FORCED
//	                                           selection. `below(1)` and `below(7)`
//	                                           at size 0 are indistinguishable from
//	                                           outside: both consume one draw and
//	                                           the outcome cannot change the value.
//	                                           Only the draw's EXISTENCE is
//	                                           observable — which is exactly what
//	                                           §4's "single-candidate selection is
//	                                           not skipped" clause requires.
//
// Review caught this file claiming "the same draws in the same order". It does
// not establish that, and the weaker statement is the one that is true.
//
// WHAT IT DOES NOT CLAIM. It shows the text and this kernel agree. It cannot
// show the text is unambiguous to a reader who has not seen this kernel — that
// is what a blind round is for, and one was run against the rewritten entry.
// Nor does it validate the PRECEDENCE decision itself: that `Data` wins for
// `Str` is a choice recorded on #163, and this only checks the consequence.

// genIntFromSpecText implements §4's `Int` rule, verbatim:
//
//	draw below(4); on 0, draw below(5) into boundary table [-2,-1,0,1,2];
//	otherwise draw intIn(-20,20).
//
// It exists because §4's rewritten `Str` entry delegates the `SCons` head to
// this rule by name, so a derivation of the `Str` entry is incomplete without
// it. Written from the sentence, NOT copied from gen.go — copying would make
// the comparison below a tautology.
func genIntFromSpecText(r *rng) int64 {
	if r.below(4) == 0 {
		boundary := []int64{-2, -1, 0, 1, 2}
		return boundary[r.below(5)]
	}
	return r.intIn(-20, 20)
}

// genStrFromSpecText implements §4's rewritten `Str` entry: the `Data` rule
// applied to `Str`, whose declaration §3 fixes as
// `(data Str [] (SNil) (SCons Int Str))`.
//
//	size clamps to a minimum of 0 on entry;
//	the constructor selection ALWAYS consumes one below(k) draw;
//	at size 0 the only candidate is the non-recursive SNil, so k = 1;
//	at positive size both candidates are live in declaration order
//	  [SNil, SCons], so k = 2;
//	SNil stops; SCons generates its Int field then its Str field,
//	  left to right, both at size-1.
//
// Returns the codepoints of the generated `Str`, outermost first.
func genStrFromSpecText(size int, r *rng) []int64 {
	if size < 0 {
		size = 0
	}
	k := 2
	if size <= 0 {
		k = 1
	}
	idx := r.below(k)
	if idx == 0 {
		return nil // SNil
	}
	head := genIntFromSpecText(r)
	return append([]int64{head}, genStrFromSpecText(size-1, r)...)
}

func TestSpecStrEntryDerivesTheKernelDrawOrder(t *testing.T) {
	be, err := openFSBackend("../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	st, err := newStoreWithBackend(be, "../codebase")
	if err != nil {
		t.Fatalf("opening the committed store: %v", err)
	}
	spine, err := newStrSpine(st)
	if err != nil {
		t.Fatalf("%v; the comparison has no subject", err)
	}
	strTy := Ty{K: "data", Hash: spine.hash}

	// PIN THE DECLARATION ORDER THE TEXT NAMES. The rewritten §4 entry says
	// "declaration order [SNil, SCons]", and that sentence is only correct while
	// the committed Str object really declares them in that order. If it ever
	// moves, the SPEC text is wrong and this fails rather than silently
	// comparing two implementations that are wrong together.
	strDef, err := st.GetDef(spine.hash)
	if err != nil {
		t.Fatalf("reading the Str datatype: %v", err)
	}
	if len(strDef.Ctors) != 2 || len(strDef.Ctors[0]) != 0 || len(strDef.Ctors[1]) != 2 {
		t.Fatalf("Str no longer declares [SNil, SCons] in that order (ctor arities %v); "+
			"SPEC §4's Str entry names that order and must be re-read", strDef.Ctors)
	}
	if spine.snilIdx != 0 || spine.sconsIdx != 1 {
		t.Fatalf("Str's constructor indices moved (SNil=%d, SCons=%d, want 0 and 1); "+
			"SPEC §4's Str entry names declaration order [SNil, SCons]", spine.snilIdx, spine.sconsIdx)
	}

	// --- CONTROLS ON THE COMPARISON ------------------------------------------
	// A "they agree" verdict from a comparison that cannot report disagreement
	// is not evidence. Two deliberately wrong derivations must be caught, one
	// per dimension the comparison claims to cover.
	//
	// VALUE control: skipping the size-0 selection draw (the "single-candidate
	// selection is not skipped" clause §4 spells out) desynchronises the stream.
	// DRAW control: a derivation that produces an identical value while taking a
	// different number of draws — SCons generating its tail BEFORE its head.
	//
	// Each control SWEEPS seeds rather than trying one. A single seed proves
	// nothing here: at any size the first draw can select `SNil`, after which
	// every derivation agrees trivially — so a one-seed control would report
	// "indistinguishable" for a mutant it simply never exercised. The controls
	// require the difference to be observed at least once.
	{
		differed := 0
		for s := 0; s < 2000; s++ {
			r1, r2 := rng{s: uint64(s)}, rng{s: uint64(s)}
			a := genStrFromSpecText(3, &r1)
			b := genStrSkippingSingleCandidateDraw(3, &r2)
			if r1.s != r2.s || !sameCps(a, b) {
				differed++
			}
		}
		if differed == 0 {
			t.Fatal("DRAW control failed: skipping the size-0 selection draw was indistinguishable " +
				"over 2000 seeds, so this comparison cannot detect a wrong draw count")
		}
	}
	{
		differed := 0
		for s := 0; s < 2000; s++ {
			r1, r2 := rng{s: uint64(s)}, rng{s: uint64(s)}
			a := genStrFromSpecText(4, &r1)
			b := genStrTailBeforeHead(4, &r2)
			if !sameCps(a, b) || r1.s != r2.s {
				differed++
			}
		}
		if differed == 0 {
			t.Fatal("ORDER control failed: generating SCons's fields right-to-left was " +
				"indistinguishable over 2000 seeds")
		}
	}

	// --- THE COMPARISON ------------------------------------------------------
	// Sizes span the clamped range: negative (which §4 clamps to 0), 0, and
	// every size the case schedule reaches (c mod 8, so 0..7), plus one beyond.
	sizes := []int{-3, -1, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	const seeds = 4000
	compared, nonEmpty := 0, 0
	maxLen := 0

	for _, size := range sizes {
		for s := 0; s < seeds; s++ {
			seed := uint64(s)*0x9E3779B97F4A7C15 + uint64(size+8)
			rKernel := rng{s: seed}
			rSpec := rng{s: seed}

			got, err := genValue(st, &strTy, size, &rKernel)
			if err != nil {
				t.Fatalf("size %d seed %d: the kernel failed to generate a Str: %v", size, seed, err)
			}
			gotCps, ok := spine.codepoints(got)
			if !ok {
				t.Fatalf("size %d seed %d: the kernel produced a value that is not a Str spine", size, seed)
			}
			wantCps := genStrFromSpecText(size, &rSpec)

			if !sameCps(gotCps, wantCps) {
				t.Fatalf("VALUE MISMATCH at size %d seed %d: SPEC §4's text derives %v, the kernel "+
					"produced %v. Either §4's Str entry or the generator is wrong; they were "+
					"written to agree.", size, seed, wantCps, gotCps)
			}
			// THE SECOND DIMENSION: equal values over unequal draw COUNTS would mean
			// the text under-determines how much of the stream a Str consumes,
			// which desynchronises every later binder of the same property.
			if rKernel.s != rSpec.s {
				t.Fatalf("DRAW-COUNT MISMATCH at size %d seed %d: the text's derivation left the rng "+
					"at %016x and the kernel left it at %016x, so the two took a different NUMBER of "+
					"draws even though they produced the same value %v.", size, seed, rSpec.s, rKernel.s, gotCps)
			}
			compared++
			if len(gotCps) > 0 {
				nonEmpty++
			}
			if len(gotCps) > maxLen {
				maxLen = len(gotCps)
			}
		}
	}

	// VACUITY CONTROLS. Agreement over nothing but empty strings would say
	// almost nothing: SNil is one draw and reaching it proves no recursion.
	if compared != len(sizes)*seeds {
		t.Fatalf("compared %d generations, expected %d", compared, len(sizes)*seeds)
	}
	if nonEmpty == 0 {
		t.Fatal("every generated Str was empty; the agreement says nothing about the SCons path")
	}
	if maxLen < 2 {
		t.Fatalf("the longest generated Str was %d codepoint(s); the agreement says nothing about "+
			"the recursive tail", maxLen)
	}
	fmt.Printf("\n=== SPEC §4's Str entry vs the kernel: %d generations over sizes %v ===\n",
		compared, sizes)
	fmt.Printf("  identical value AND identical draw count (rng state) in every case\n")
	fmt.Printf("  non-empty: %d/%d   longest generated Str: %d codepoints\n\n", nonEmpty, compared, maxLen)
}

// genStrSkippingSingleCandidateDraw is a DELIBERATELY WRONG derivation: it omits
// the selection draw when there is only one candidate — the exact reading §4
// closes with "single-candidate selection is not skipped". It exists only as the
// comparison's control.
func genStrSkippingSingleCandidateDraw(size int, r *rng) []int64 {
	if size < 0 {
		size = 0
	}
	if size <= 0 {
		return nil // wrong: no below(1) draw taken
	}
	if r.below(2) == 0 {
		return nil
	}
	head := genIntFromSpecText(r)
	return append([]int64{head}, genStrSkippingSingleCandidateDraw(size-1, r)...)
}

// genStrTailBeforeHead is a DELIBERATELY WRONG derivation: it generates SCons's
// fields right-to-left, contradicting §4's "fields generated left-to-right".
// Control only.
func genStrTailBeforeHead(size int, r *rng) []int64 {
	if size < 0 {
		size = 0
	}
	k := 2
	if size <= 0 {
		k = 1
	}
	if r.below(k) == 0 {
		return nil
	}
	tail := genStrTailBeforeHead(size-1, r)
	head := genIntFromSpecText(r)
	return append([]int64{head}, tail...)
}

func sameCps(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
