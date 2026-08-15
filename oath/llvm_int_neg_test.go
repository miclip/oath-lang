package main

// UNARY `neg` AT Int, IN THE LLVM BACKEND.
//
// The demand is concrete and it is in this repo's own corpus. `examples/circle.oath`
// — the tutorial's worked compiled example — spells the sign branch of `show-int`
// as `(SCons 45 (show-nat (neg n)))`, so `oath build circle --backend llvm`
// refused on `neg` while every other primitive that definition uses was already
// lowered. `(- 0 n)` denotes the same value, which is exactly why lowering `neg`
// is cheap; it is not a reason to make a user rewrite a definition to suit one
// backend.
//
// WHAT THESE TESTS HAVE TO WITNESS, since agreement on `(neg 1)` is worth almost
// nothing:
//
//	SIGN      the operand's sign varies with argv, so a lowering that dropped
//	          the flip, or applied it twice, cannot answer the whole table.
//	ZERO      negating zero is 0, not -0. The runtime's Int is sign-magnitude,
//	          which is the representation where a negative zero is constructible
//	          and where every comparison then has to ask about it. o_int_wrap
//	          forbids it; this is the outside check on that.
//	MAGNITUDE beyond 64 bits, because a lowering that negated a machine word
//	          would agree everywhere else. The operand is built at run time by
//	          multiplication, so no literal in the program carries the answer.
//
// The value is RENDERED rather than compared, and that is deliberate. An entry
// answering "yes"/"no" from `(== (neg n) k)` passes with a wrong magnitude as
// long as both sides are wrong together; printing the digits makes the whole
// value the observable. `show-int` below is circle.oath's own definition,
// unchanged, so this file also builds the thing that produced the demand.

import (
	"strings"
	"testing"
)

// negStore is llvmStore plus the arithmetic circle.oath needs to render an Int,
// and the argv plumbing that keeps every operand out of the emitter's reach.
func negStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(defn arg-at [] [(args (List Str)) (n Int)] Str
		(match args
			((Nil) "")
			((Cons h rest) (if (== n 0) h (arg-at rest (- n 1))))))`)
	put(t, st, `(defn str-append [] [(a Str) (b Str)] Str
		(match a ((SNil) b) ((SCons c rest) (SCons c (str-append rest b)))))`)
	// The only Int source in this file: the length of an argument. Nothing the
	// compiler can see, and it is a NATURAL, so every negative value below is
	// produced by an operation rather than written down.
	put(t, st, `(defn str-len [] [(s Str)] Int
		(match s ((SNil) 0) ((SCons c rest) (+ 1 (str-len rest)))))`)
	// circle.oath's show-nat and show-int, spelled exactly as the corpus has
	// them. show-int is the definition whose `(neg n)` this whole file exists
	// for; copying it rather than paraphrasing it is what makes these tests
	// evidence about the program that asked.
	put(t, st, `(defn show-nat [] [(n Int)] Str
		(if (< n 10)
			(SCons (+ 48 n) (SNil))
			(str-append (show-nat (/ n 10)) (SCons (+ 48 (% n 10)) (SNil)))))`)
	put(t, st, `(defn show-int [] [(n Int)] Str
		(if (< n 0) (SCons 45 (show-nat (neg n))) (show-nat n)))`)
	return st
}

// THE REFERENCE, FIXED FIRST AND WITHOUT EITHER BACKEND. `oath eval` is what
// both compilers must agree with; if this table ever fails, the LANGUAGE moved
// and the backends are the second thing to look at.
func TestIntNegationInTheInterpreter(t *testing.T) {
	st := negStore(t)
	for _, tc := range []struct{ expr, want string }{
		{`(neg 1)`, "-1"},
		{`(neg -1)`, "1"},
		// ZERO HAS ONE SPELLING. A sign-magnitude runtime is where this can go
		// wrong, and the reference is unambiguous about the answer.
		{`(neg 0)`, "0"},
		// Beyond 64 bits in both directions: Int is ℤ, and the LLVM runtime's
		// bignum is the reason that is true of the compiled program too.
		{`(neg 9223372036854775808)`, "-9223372036854775808"},
		{`(neg -9223372036854775809)`, "9223372036854775809"},
		// `neg` composed with the binary operations it shares a type with, since
		// the lowering has to agree with them and not merely with itself.
		{`(+ (neg 7) 7)`, "0"},
		{`(* (neg 3) (neg 4))`, "12"},
		{`(- 0 (neg 5))`, "5"},
		// The equivalence the refusal used to rely on, asserted rather than
		// assumed: this is the claim that made `(- 0 x)` an adequate workaround,
		// and it is the claim the lowering must not break.
		{`(== (neg 12345678901234567890) (- 0 12345678901234567890))`, "true"},
	} {
		got, err := apiEval(st, tc.expr)
		if err != nil {
			t.Errorf("eval %s: %v", tc.expr, err)
			continue
		}
		// The rendered TYPE is kept in the comparison: an expression that quietly
		// typed as Rat would still print a plausible number.
		want := tc.want + " : Int"
		if tc.want == "true" {
			want = "true : Bool"
		}
		if got != want {
			t.Errorf("eval %s = %q, want %q", tc.expr, got, want)
		}
	}
}

// THE THREE-WAY GATE. The interpreter is the reference; two identically wrong
// lowerings agree with each other and witness nothing.
func TestIntNegationAgreesThreeWays(t *testing.T) {
	requireClang(t)
	requireGoToolchain(t)
	st := negStore(t)

	// A NON-NEGATIVE OPERAND, including zero when no argument is given. This is
	// the entry that catches a negative zero: `oath eval` answers "0" and a
	// runtime that built one would print "-0".
	put(t, st, `(defn neg-len [] [(args (List Str))] Str
		(show-int (neg (str-len (arg-at args 0)))))`)
	markVerified(t, st, "neg-len")

	// AN OPERAND WHOSE SIGN VARIES WITH THE INPUT. The difference of two argument
	// lengths is negative, positive or zero depending on argv, so one entry
	// exercises all three cases and a lowering that only ever flips one way
	// cannot pass the table.
	put(t, st, `(defn neg-diff [] [(args (List Str))] Str
		(show-int (neg (- (str-len (arg-at args 0)) (str-len (arg-at args 1))))))`)
	markVerified(t, st, "neg-diff")

	// BEYOND 64 BITS, BUILT AT RUN TIME. The multiplier is a literal but the
	// multiplicand is an argument length, so the product appears nowhere in the
	// program and a lowering that negated a machine word would disagree here and
	// nowhere above.
	put(t, st, `(defn neg-big [] [(args (List Str))] Str
		(show-int (neg (* 9223372036854775808 (str-len (arg-at args 0))))))`)
	markVerified(t, st, "neg-big")

	// `neg` UNDER A CALL BOUNDARY, which is the shape circle.oath actually has:
	// show-int applies it to its own parameter, so the operand arrives in a
	// register the caller filled rather than one the same block computed.
	put(t, st, `(defn signed [] [(args (List Str))] Str
		(show-int (- (str-len (arg-at args 0)) (str-len (arg-at args 1)))))`)
	markVerified(t, st, "signed")

	vectors := map[string][][]string{
		"neg-len":  {nil, {""}, {"a"}, {"abc"}, {"üü"}, {"a b c"}},
		"neg-diff": {nil, {"", ""}, {"abc", "a"}, {"a", "abc"}, {"ab", "ab"}, {"üüü", ""}},
		"neg-big":  {nil, {""}, {"a"}, {"ab"}, {"abcdefg"}},
		"signed":   {nil, {"", ""}, {"abc", "a"}, {"a", "abc"}, {"ab", "ab"}},
	}

	// EACH TABLE MUST DISCRIMINATE BEFORE IT IS RUN THROUGH threeWay, because
	// threeWay asserts AGREEMENT and nothing else. For `neg` the property that
	// matters is not two distinct answers but a NEGATIVE one: a table whose every
	// vector answers "0" agrees perfectly across all three paths while never once
	// requiring the sign to be flipped, and it looks exactly like a thorough
	// table. So the interpreter's own answers are collected first and a leading
	// "-" is required somewhere in each.
	//
	// EVERY table is checked before ANY of them is compiled: threeWay fails
	// fatally, so an interleaved loop would leave the later tables unmeasured.
	entries := []string{"neg-len", "neg-diff", "neg-big", "signed"}
	for _, entry := range entries {
		signs := map[bool]int{}
		for _, args := range vectors[entry] {
			lit, ok := oathList(args)
			if !ok {
				t.Fatalf("%s: args %v cannot be written as an Oath literal", entry, args)
			}
			signs[strings.HasPrefix(evalDenotation(t, st, "("+entry+" "+lit+")"), "-")]++
		}
		if signs[true] == 0 {
			t.Errorf("%s: no vector produces a negative result, so agreement across the "+
				"three paths would not require the sign to be flipped at all", entry)
		}
	}
	// neg-diff and signed must also produce a POSITIVE result, or the sign check
	// above is satisfied by an entry that is negative everywhere — which would
	// pass a lowering that negated unconditionally.
	for _, entry := range []string{"neg-diff", "signed"} {
		pos := 0
		for _, args := range vectors[entry] {
			lit, _ := oathList(args)
			got := evalDenotation(t, st, "("+entry+" "+lit+")")
			if !strings.HasPrefix(got, "-") && got != "0" {
				pos++
			}
		}
		if pos == 0 {
			t.Errorf("%s: every vector is negative or zero, so a lowering that flipped "+
				"the sign unconditionally would agree everywhere", entry)
		}
	}

	for _, entry := range entries {
		threeWay(t, st, entry, vectors[entry])
	}
}

// THE LOWERING THAT WAS SELECTED, asserted in the IR.
//
// The three-way test above would pass on a backend that rewrote `(neg n)` into
// `(- 0 n)` before emitting, which is a correct rewrite — so "a `neg` lowering
// exists" is left unwitnessed by agreement alone. The same distinction
// TestStrEqualitySelectsItsOwnLowering and TestBooleanPrimitivesEmit draw.
//
// This one CAN name the call, because unlike an instruction choice the runtime
// entry point is the interface between the emitter and the C runtime: if it is
// renamed, this test is supposed to be updated with it.
func TestIntNegationSelectsItsOwnLowering(t *testing.T) {
	st := negStore(t)
	put(t, st, `(defn only-neg [] [(args (List Str))] Str
		(show-int (neg (str-len (arg-at args 0)))))`)
	markVerified(t, st, "only-neg")
	prog, err := planProgram(st, "only-neg")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("the LLVM backend refused a program whose only unary operation is neg: %v", err)
	}
	if !strings.Contains(ir, "call ptr @o_int_neg") {
		t.Error("no call to o_int_neg in a program that negates — `neg` reached the " +
			"binary table or was rewritten, and the unary lowering is unwitnessed")
	}
}

// `neg` AT Rat IS STILL REFUSED, FROM THE INSIDE.
//
// TestLLVMPrimitiveBoundary's neg-on-rat row asserts the refusal; this asserts
// what it is a refusal ABOUT. `neg`'s typing rule admits Int, Rat and Float, so
// the guard that keeps this backend's lowering at Int is the operand type and
// not the operator name — and a refusal naming the wrong operation would leave
// a user looking for a Rat problem in the wrong place.
func TestIntNegationRefusesRatByName(t *testing.T) {
	st := negStore(t)
	put(t, st, `(defn neg-rat [] [(args (List Str))] Str
		(let (x Rat (neg 1/2)) "yes"))`)
	markVerified(t, st, "neg-rat")
	prog, err := planProgram(st, "neg-rat")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	if _, err := emitLLVM(st, prog); err == nil {
		t.Fatal("the LLVM backend accepted `neg` at Rat, which it cannot lower")
	} else if !strings.Contains(err.Error(), `"neg"`) {
		t.Errorf("the refusal does not name neg, so it is not evidence about the "+
			"operand-type guard: %v", err)
	}
}
