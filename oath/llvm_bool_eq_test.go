package main

// `==` AT Bool, IN THE LLVM BACKEND.
//
// This is the THIRD instance of one polymorphic operation, after Int and Str.
// `==` is structural equality at every type; what a backend chooses is how to
// compute it over its own representation, and at Bool there is no payload to
// walk — the two truth values decide it.
//
// WHAT MAKES THAT WORTH TESTING AT ALL, given how small the lowering is:
//
//	THE OPERAND TYPE SELECTS IT. Three lowerings now sit behind one operator
//	name, and each is guarded by what its operands synthesise to. A guard that
//	drifted would not produce a compile error — it would produce the WRONG
//	lowering, and an Int comparison performed on a Bool's tag agrees with the
//	reference on every value a small table happens to contain.
//
//	THE OPERANDS MUST BE UNKNOWN TO THE COMPILER. Every compiled vector below
//	derives both Bools from argv, in a function BODY. A property would be
//	evaluated by the interpreter, which is the reference rather than the thing
//	under test, and a literal spelling risks passing because canon.go rewrote
//	the expression rather than because the backend lowered it.
//
// The interpreter table is fixed FIRST and without either backend, so that the
// compiled halves below are a comparison rather than an invention.

import (
	"strings"
	"testing"
)

// boolEqStore is llvmStore plus the argv plumbing that puts a Bool the compiler
// cannot see on each side of the operator.
func boolEqStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(defn arg-at [] [(args (List Str)) (n Int)] Str
		(match args
			((Nil) "")
			((Cons h rest) (if (== n 0) h (arg-at rest (- n 1))))))`)
	// The Bool source: true iff the argument is non-empty.
	put(t, st, `(defn nonempty [] [(s Str)] Bool
		(match s ((SNil) false) ((SCons c rest) true)))`)
	// A Bool crossing a function boundary in both directions, so at least one
	// entry compares operands the same basic block did not compute.
	put(t, st, `(defn agree [] [(a Bool) (b Bool)] Bool (== a b))`)
	put(t, st, `(defn str-len [] [(s Str)] Int
		(match s ((SNil) 0) ((SCons c rest) (+ 1 (str-len rest)))))`)
	return st
}

// THE REFERENCE. `oath eval` is what both backends must agree with, and this
// needs no toolchain: if it ever disagrees with the table, the LANGUAGE changed.
func TestBooleanEqualityInTheInterpreter(t *testing.T) {
	st := boolEqStore(t)
	for _, tc := range []struct{ expr, want string }{
		// The whole two-by-two table, so nothing below rests on a partial one.
		// The mixed rows are the ones an operator lowered as its own negation
		// would get wrong, and a table without them is satisfied by `!=`.
		{`(== true true)`, "true"},
		{`(== true false)`, "false"},
		{`(== false true)`, "false"},
		{`(== false false)`, "true"},
		// Operands that are themselves Bool operations, since `==` is lowered as
		// an expression and not as a statement.
		{`(== (not false) (and true true))`, "true"},
		{`(== (or false false) (not true))`, "true"},
		{`(== (not true) (and true true))`, "false"},
		// Operands that are COMPARISONS. `<` and `==` at Int both produce Bool,
		// and a lowering that only accepted the constructor spelling of a Bool
		// would fail here.
		{`(== (< 1 2) (== 3 3))`, "true"},
		{`(== (< 2 1) (== 3 3))`, "false"},
		// `==` at Bool over `==` at Bool: the operator applied to its own
		// results, which is the shape that catches a lowering correct only at
		// the top of an `if` condition.
		{`(== (== true false) (== false true))`, "true"},
	} {
		got, err := apiEval(st, tc.expr)
		if err != nil {
			t.Errorf("eval %s: %v", tc.expr, err)
			continue
		}
		// The RENDERED type is kept in the comparison rather than trimmed off: an
		// expression that quietly typed as something else would still answer true
		// or false and the assertion would not notice.
		if got != tc.want+" : Bool" {
			t.Errorf("eval %s = %q, want %q", tc.expr, got, tc.want+" : Bool")
		}
	}
}

// THE THREE-WAY GATE. The interpreter is the reference; two identically wrong
// lowerings agree with each other and prove nothing.
func TestBooleanEqualityAgreesThreeWays(t *testing.T) {
	requireClang(t)
	requireGoToolchain(t)
	st := boolEqStore(t)

	// Both operands unknown to the compiler and both derived from a different
	// argument, so the two-by-two table is reachable from argv alone.
	put(t, st, `(defn eq-bool [] [(args (List Str))] Str
		(if (== (nonempty (arg-at args 0)) (nonempty (arg-at args 1))) "yes" "no"))`)
	markVerified(t, st, "eq-bool")

	// OPERANDS THAT ARE THEMSELVES Bool OPERATIONS. A lowering that worked only
	// on operands produced by a call would pass the entry above and fail this.
	put(t, st, `(defn eq-nested [] [(args (List Str))] Str
		(if (== (not (nonempty (arg-at args 0)))
		        (or (nonempty (arg-at args 1)) (nonempty (arg-at args 2))))
			"yes" "no"))`)
	markVerified(t, st, "eq-nested")

	// A Bool ACROSS A CALL: compared inside a callee, from arguments the caller
	// computed. The operands arrive in registers this function did not fill.
	put(t, st, `(defn eq-thru [] [(args (List Str))] Str
		(if (agree (nonempty (arg-at args 0)) (nonempty (arg-at args 1))) "yes" "no"))`)
	markVerified(t, st, "eq-thru")

	// OPERANDS THAT ARE Int COMPARISONS, which is the spelling that would break
	// if the Bool guard were reading the operator name instead of the operand
	// type: both sides here are results of `==` and `<=` at Int.
	put(t, st, `(defn eq-cmp [] [(args (List Str))] Str
		(if (== (== (str-len (arg-at args 0)) 0)
		        (<= (str-len (arg-at args 1)) 1))
			"yes" "no"))`)
	markVerified(t, st, "eq-cmp")

	pair := [][]string{
		nil,             // false, false — no argument at all
		{"", ""},        // false, false — explicitly
		{"x", ""},       // true,  false
		{"", "y"},       // false, true
		{"x", "y"},      // true,  true
		{"ü", "✓"},      // the same rows with multi-byte arguments
		{"x"},           // true, false — the second argument MISSING rather than empty
		{"a b", "c\td"}, // arguments carrying separators
	}
	triple := [][]string{
		nil,
		{"", "", ""},
		{"x", "", ""},
		{"", "x", ""},
		{"", "", "x"},
		{"x", "y", ""},
		{"x", "", "z"},
		{"", "y", "z"},
		{"x", "y", "z"},
	}
	vectors := map[string][][]string{
		"eq-bool":   pair,
		"eq-nested": triple,
		"eq-thru":   pair,
		"eq-cmp":    {nil, {"", ""}, {"a", ""}, {"", "b"}, {"a", "b"}, {"abc", "b"}, {"", "bcd"}},
	}

	// EACH TABLE MUST DISCRIMINATE BEFORE IT IS RUN THROUGH threeWay, because
	// threeWay asserts AGREEMENT and nothing else: a table whose every vector
	// answers "no" agrees perfectly across all three paths while never once
	// exercising the equal case, and it looks exactly like a thorough table.
	// This is the CONTROL for the measurement, not a second assertion about
	// semantics.
	//
	// EVERY table is checked before ANY of them is compiled: threeWay fails
	// fatally, so an interleaved loop would stop at the first entry and leave
	// the remaining tables unmeasured.
	entries := []string{"eq-bool", "eq-nested", "eq-thru", "eq-cmp"}
	for _, entry := range entries {
		seen := map[string]int{}
		for _, args := range vectors[entry] {
			lit, ok := oathList(args)
			if !ok {
				t.Fatalf("%s: args %v cannot be written as an Oath literal", entry, args)
			}
			seen[evalDenotation(t, st, "("+entry+" "+lit+")")]++
		}
		if seen["yes"] == 0 || seen["no"] == 0 {
			t.Errorf("%s: the vectors do not discriminate — interpreter answers were %v, "+
				"and threeWay only checks agreement, so one-sided vectors pass without the "+
				"operator ever having to be right", entry, seen)
		}
	}
	for _, entry := range entries {
		threeWay(t, st, entry, vectors[entry])
	}
}

// THE LOWERING THAT WAS SELECTED, asserted in the IR.
//
// The three-way test would pass on a backend that rewrote `(== a b)` at Bool
// into `(if a b (not b))`, provided the rewrite were correct — agreement with
// the reference is the claim, so "a Bool equality lowering exists" is left
// unwitnessed by it. The same distinction TestBooleanPrimitivesEmit draws.
//
// WHAT THIS ASSERTS IS DELIBERATELY NEGATIVE, and it is the mistake worth
// catching: a program whose only equality is at Bool must not reach the Int
// comparison. `o_int_eq` on a Bool's tag would agree with the reference on
// every vector above and would be reading a representation the language does
// not fix.
func TestBooleanEqualityDoesNotUseTheIntComparison(t *testing.T) {
	st := boolEqStore(t)
	put(t, st, `(defn only-eq-bool [] [(args (List Str))] Str
		(match args
			((Nil) "no")
			((Cons a rest)
				(match rest
					((Nil) "no")
					((Cons b r2) (if (== (nonempty a) (nonempty b)) "yes" "no"))))))`)
	markVerified(t, st, "only-eq-bool")

	prog, err := planProgram(st, "only-eq-bool")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("the LLVM backend refused a program whose only equality is at Bool: %v", err)
	}
	// The program destructures its argument list by matching and performs no Int
	// operation at all, so an Int comparison in the emitted IR could only have
	// come from the Bool equality.
	if strings.Contains(ir, "call i32 @o_int_eq") {
		t.Error("emitted an Int comparison in a program that performs no Int operation — " +
			"`==` at Bool was lowered as a comparison on the tag")
	}
	if strings.Contains(ir, "call i32 @o_str_eq") {
		t.Error("emitted a Str comparison in a program that compares no strings — " +
			"`==` at Bool selected the Str lowering")
	}
}
