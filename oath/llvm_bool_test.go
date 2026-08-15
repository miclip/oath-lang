package main

// THE BOOLEAN PRIMITIVES `and`, `or` AND `not`, IN THE LLVM BACKEND (#173).
//
// The webhook's next build error after Str equality names these. `webhook.oath`
// uses all three in COMPILED bodies — `(and (not (== b 34)) (<= 32 b))` inside
// take-while, `(or (== got want) (str-prefix ...))` in path-is and
// media-type-is — so nothing about them is discharged by the property layer,
// which evaluates through the interpreter.
//
// WHAT IS ACTUALLY BEING CLAIMED decides what these tests have to witness, and
// for these three operations it is NOT the truth table. The truth table is the
// easy half: any lowering anyone would write gets it right, and a table-only
// suite is satisfied by `br i1` short-circuiting, which is what a C-family
// backend author reaches for by reflex and what every other language in that
// family means by `&&`.
//
//	OATH'S `and` AND `or` ARE STRICT. BOTH OPERANDS EVALUATE, ALWAYS.
//
// eval.go computes `args[0].Bool && args[1].Bool` on values already forced by
// the argument loop; compile.go emits a func literal precisely so Go's own `&&`
// cannot short-circuit ("Oath's and/or are NOT short-circuiting: both operands
// evaluate"); and canon.go's AC rules drop `and`'s `true` leaves and `or`'s
// `false` leaves but perform NO annihilation, because `and false X` may not
// discard X. So strictness is a decided property of the language in three
// separate places, and a backend that short-circuits is not an optimisation of
// it — it is a different language whose programs happen to agree whenever the
// discarded operand terminates.
//
// The witness therefore has to be an operand whose EVALUATION IS OBSERVABLE and
// whose VALUE IS NOT NEEDED. Division by zero is that operand: `(and false (< (/
// 1 0) 1))` is `false` under short-circuiting and a runtime FAILURE under the
// language, and the two are distinguishable from outside the process. `(or true
// (< (/ 1 0) 1))` is the same discriminator on the other operator.
//
// Each strictness case is spelled TWICE, with a literal divisor and with one
// derived from the argument list, and they catch different mutations:
//
//	literal   `(/ 1 0)` — catches a COMPILE-TIME fold of `and false X` to
//	          `false`, which is a legal peephole in a short-circuit language
//	          and is unsound here. Nothing folds Int arithmetic today (the Go
//	          artifact in refusal_exit_test.go reaches iquo at run time with
//	          the same literal divisor), so this form does exercise the
//	          runtime — but it is exactly the form a future folder would eat.
//	derived   the divisor comes back from a function over the input, so no
//	          folder can see it is zero. This is the pure RUNTIME
//	          short-circuit: a `br` that skips the second operand's block.
//
// A short-circuiting lowering passes the literal pair or the derived pair only
// by also being wrong about the other, which is why both are here rather than
// one standing in for both.

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// boolStore is llvmStore plus the definitions that put a Bool the COMPILER
// CANNOT SEE on each side of an operator. Every Bool below is derived from argv,
// so a lowering that folded the operators away at compile time would have
// nothing to fold.
func boolStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(defn arg-at [] [(args (List Str)) (n Int)] Str
		(match args
			((Nil) "")
			((Cons h rest) (if (== n 0) h (arg-at rest (- n 1))))))`)
	// The only Bool source in this file: true iff the argument is non-empty.
	put(t, st, `(defn nonempty [] [(s Str)] Bool
		(match s ((SNil) false) ((SCons c rest) true)))`)
	// A Bool crossing a function boundary, in both directions: taken as a
	// parameter, combined, returned. A lowering correct only for operands
	// synthesised in the same basic block would fail here and nowhere above.
	put(t, st, `(defn both [] [(a Bool) (b Bool)] Bool (and a b))`)
	put(t, st, `(defn either [] [(a Bool) (b Bool)] Bool (or a b))`)
	// ZERO, THE LONG WAY ROUND. Every branch returns 0, but the value comes back
	// from a call over runtime input, so no constant folder can see it.
	put(t, st, `(defn zero-of [] [(s Str)] Int
		(match s ((SNil) 0) ((SCons c rest) 0)))`)
	return st
}

// THE REFERENCE, FIXED FIRST AND WITHOUT EITHER BACKEND.
//
// `oath eval` is what both backends must agree with. This test needs no clang
// and no compilation: if it ever disagrees with the table below, the LANGUAGE
// changed and the backends are the second thing to look at.
func TestBooleanPrimitivesInTheInterpreter(t *testing.T) {
	st := boolStore(t)
	for _, tc := range []struct {
		expr string
		want string
	}{
		// The whole truth table for each operator, so nothing below rests on a
		// partial one. `and` and `or` differ on exactly the two mixed rows, and
		// a suite that omitted them would be satisfied by either operator
		// lowered as the other.
		{`(and false false)`, "false"},
		{`(and false true)`, "false"},
		{`(and true false)`, "false"},
		{`(and true true)`, "true"},
		{`(or false false)`, "false"},
		{`(or false true)`, "true"},
		{`(or true false)`, "true"},
		{`(or true true)`, "true"},
		{`(not false)`, "true"},
		{`(not true)`, "false"},
		// Nesting and mixing, since the operators are lowered as expressions and
		// not as statements: an operand that is itself an operator application.
		{`(not (and true false))`, "true"},
		{`(and (not false) (or false true))`, "true"},
		{`(or (and true false) (not true))`, "false"},
		// The operands do not have to be Bool LITERALS. `<` and `==` at Int both
		// produce Bool, and a lowering that only accepted the constructor
		// spelling of a Bool would fail here.
		{`(and (< 1 2) (== 3 3))`, "true"},
		{`(or (< 2 1) (== 3 4))`, "false"},
	} {
		// The RENDERED type is kept in the comparison rather than trimmed off:
		// an expression that quietly typed as something else would still answer
		// true or false and the assertion would not notice.
		got, err := apiEval(st, tc.expr)
		if err != nil {
			t.Errorf("eval %s: %v", tc.expr, err)
			continue
		}
		if got != tc.want+" : Bool" {
			t.Errorf("eval %s = %q, want %q", tc.expr, got, tc.want+" : Bool")
		}
	}
}

// STRICTNESS IN THE REFERENCE, which is what makes the compiled witnesses below
// a comparison rather than an invention.
//
// This is the sentence the whole file exists to defend, stated where no backend
// is involved: `oath eval` FAILS on both of these, and a short-circuiting
// reading of either would answer a Bool instead.
func TestBooleanPrimitivesAreStrictInTheInterpreter(t *testing.T) {
	st := boolStore(t)
	for _, tc := range []struct {
		expr string
		// What a SHORT-CIRCUITING implementation would have answered. Recorded
		// so the assertion below cannot be satisfied by an expression that fails
		// for some unrelated reason — a typo that failed to typecheck would look
		// exactly like strictness.
		shortCircuit string
	}{
		{`(and false (< (/ 1 0) 1))`, "false"},
		{`(or true (< (/ 1 0) 1))`, "true"},
		// The operand order reversed, because a lowering may evaluate either
		// side first and only one order is exercised above.
		{`(and (< (/ 1 0) 1) false)`, "false"},
		{`(or (< (/ 1 0) 1) true)`, "true"},
		// `%` as well as `/`, so the witness does not rest on one helper.
		{`(and false (< (% 1 0) 1))`, "false"},
	} {
		got, err := apiEval(st, tc.expr)
		if err == nil {
			t.Errorf("eval %s = %q, want a failure: the discarded operand was not "+
				"evaluated, so the interpreter short-circuited. Oath's and/or are strict.",
				tc.expr, got)
			continue
		}
		if !strings.Contains(err.Error(), "by zero") {
			t.Errorf("eval %s failed without naming the by-zero condition (%v) — the "+
				"failure is not evidence that the second operand ran", tc.expr, err)
			continue
		}
		if got == tc.shortCircuit {
			t.Errorf("eval %s answered the short-circuiting value %q", tc.expr, tc.shortCircuit)
		}
	}
}

// THE THREE-WAY GATE. The interpreter is the reference; two identically wrong
// lowerings agree with each other and prove nothing.
func TestBooleanPrimitivesAgreeThreeWays(t *testing.T) {
	requireClang(t)
	st := boolStore(t)

	// One operator per entry first, each with both operands unknown to the
	// compiler, so a failure names which operator diverged.
	put(t, st, `(defn bool-and [] [(args (List Str))] Str
		(if (and (nonempty (arg-at args 0)) (nonempty (arg-at args 1))) "yes" "no"))`)
	markVerified(t, st, "bool-and")
	put(t, st, `(defn bool-or [] [(args (List Str))] Str
		(if (or (nonempty (arg-at args 0)) (nonempty (arg-at args 1))) "yes" "no"))`)
	markVerified(t, st, "bool-or")
	put(t, st, `(defn bool-not [] [(args (List Str))] Str
		(if (not (nonempty (arg-at args 0))) "yes" "no"))`)
	markVerified(t, st, "bool-not")

	// NESTED, so the operators are exercised as EXPRESSIONS whose operands are
	// themselves operator applications. A lowering that worked only at the top
	// of an `if` condition would pass the three above and fail this.
	put(t, st, `(defn bool-nested [] [(args (List Str))] Str
		(if (not (and (nonempty (arg-at args 0))
		              (or (nonempty (arg-at args 1)) (nonempty (arg-at args 2)))))
			"yes" "no"))`)
	markVerified(t, st, "bool-nested")

	// A Bool ACROSS A CALL: computed in one function, consumed in another, and
	// the operator applied inside the callee. This is the shape webhook.oath has
	// — `take-while` receives a `(-> Int Bool)` and applies it — and none of the
	// entries above has it.
	put(t, st, `(defn bool-thru [] [(args (List Str))] Str
		(if (both (nonempty (arg-at args 0))
		          (either (nonempty (arg-at args 1)) (nonempty (arg-at args 2))))
			"yes" "no"))`)
	markVerified(t, st, "bool-thru")

	// THE OPERANDS AS COMPARISONS RATHER THAN AS CALLS, which is the spelling in
	// webhook.oath's take-while predicate: `(and (not (== b 34)) (<= 32 b))`.
	put(t, st, `(defn str-len [] [(s Str)] Int
		(match s ((SNil) 0) ((SCons c rest) (+ 1 (str-len rest)))))`)
	put(t, st, `(defn bool-cmp [] [(args (List Str))] Str
		(if (and (not (== (str-len (arg-at args 0)) 0))
		         (<= (str-len (arg-at args 0)) 3))
			"yes" "no"))`)
	markVerified(t, st, "bool-cmp")

	// Two arguments give and/or their whole truth table; nonempty maps "" to
	// false and anything else to true.
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
		"bool-and":    pair,
		"bool-or":     pair,
		"bool-not":    {nil, {""}, {"x"}, {"ü"}, {"", "ignored"}},
		"bool-nested": triple,
		"bool-thru":   triple,
		"bool-cmp":    {nil, {""}, {"a"}, {"abc"}, {"abcd"}, {"üü"}, {"üüü"}},
	}

	// EACH TABLE MUST DISCRIMINATE BEFORE IT IS RUN THROUGH threeWay, because
	// threeWay asserts AGREEMENT and nothing else: a table whose every vector
	// answers "no" agrees perfectly across all three paths while never once
	// exercising the true case, and it looks exactly like a thorough table. So
	// the interpreter's own answers are collected first and both outcomes are
	// required. This is the CONTROL for the measurement, not a second assertion
	// about semantics.
	//
	// EVERY table is checked before ANY of them is compiled, rather than
	// interleaving the two per entry. threeWay fails fatally, so an interleaved
	// loop stops at the first entry and leaves the remaining tables unmeasured —
	// which is precisely the state this file is in until the lowering lands.
	entries := []string{
		"bool-and", "bool-or", "bool-not", "bool-nested", "bool-thru", "bool-cmp",
	}
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

// STRICTNESS, IN THE COMPILED BACKENDS. THE POINT OF THE WHOLE FILE.
//
// Every entry has a CONTROL invocation of the SAME binary that must succeed, so
// "it exited 70" is never satisfied by an artifact that is broken for an
// unrelated reason — and the control is checked FIRST for the same reason.
//
// THE THREE PATHS ARE NOT REQUIRED TO FAIL IDENTICALLY, and that is not papered
// over: the interpreter reports an error, and both compiled artifacts exit 70,
// the status this project's backends use for every host-side refusal. What is
// asserted is what the LANGUAGE determines — that the operation is PERFORMED and
// that its failure names the condition — rather than a host framing no
// specification fixes.
func TestBooleanPrimitivesAreStrictInBothBackends(t *testing.T) {
	requireClang(t)
	requireGoToolchain(t)
	st := boolStore(t)

	for _, tc := range []struct {
		name, def string
		// What a SHORT-CIRCUITING lowering would print instead of failing. It is
		// asserted as an ABSENCE on stdout, so this test cannot be satisfied by
		// a program that fails before reaching the operator at all.
		shortCircuit string
	}{{
		// The literal spelling. `false` is not `and`'s identity element and
		// `true` is not `or`'s, so canon.go's unit rules do not touch either —
		// these are the two annihilation shapes, and annihilation is exactly
		// what strictness forbids.
		name: "and-false-literal",
		def: `(defn and-lit [] [(args (List Str))] Str
			(match args
				((Nil) (if (and false (< (/ 1 0) 1)) "yes" "no"))
				((Cons h t) "given")))`,
		shortCircuit: "no",
	}, {
		name: "or-true-literal",
		def: `(defn or-lit [] [(args (List Str))] Str
			(match args
				((Nil) (if (or true (< (/ 1 0) 1)) "yes" "no"))
				((Cons h t) "given")))`,
		shortCircuit: "yes",
	}, {
		// The derived spelling: the divisor comes back from a call over the
		// input, so no compile-time fold can see that it is zero, and the only
		// way to avoid the division is to skip the operand AT RUN TIME.
		name: "and-false-derived",
		def: `(defn and-dyn [] [(args (List Str))] Str
			(match args
				((Nil) (if (and false (< (/ 1 (zero-of "")) 1)) "yes" "no"))
				((Cons h t) "given")))`,
		shortCircuit: "no",
	}, {
		name: "or-true-derived",
		def: `(defn or-dyn [] [(args (List Str))] Str
			(match args
				((Nil) (if (or true (< (/ 1 (zero-of "")) 1)) "yes" "no"))
				((Cons h t) "given")))`,
		shortCircuit: "yes",
	}, {
		// THE OTHER OPERAND ORDER. A lowering may evaluate either side first,
		// and a `br` on the FIRST operand is the shape above; a lowering that
		// tested the second would pass all four cases above.
		name: "and-false-second",
		def: `(defn and-rev [] [(args (List Str))] Str
			(match args
				((Nil) (if (and (< (/ 1 (zero-of "")) 1) false) "yes" "no"))
				((Cons h t) "given")))`,
		shortCircuit: "no",
	}, {
		name: "or-true-second",
		def: `(defn or-rev [] [(args (List Str))] Str
			(match args
				((Nil) (if (or (< (/ 1 (zero-of "")) 1) true) "yes" "no"))
				((Cons h t) "given")))`,
		shortCircuit: "yes",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			st := st // the store is shared and read-only from here; put below is per-case
			name := strings.SplitN(strings.TrimPrefix(tc.def, "(defn "), " ", 2)[0]
			put(t, st, tc.def)
			markVerified(t, st, name)

			// THE INTERPRETER FIRST, since it is the reference and it needs no
			// toolchain: if IT short-circuits, the two assertions below are
			// asserting something the language does not say.
			if got, err := apiEval(st, "("+name+" (Nil [Str]))"); err == nil {
				t.Fatalf("the interpreter answered %q instead of failing — the reference "+
					"short-circuited, so this test is not measuring the backends", got)
			} else if !strings.Contains(err.Error(), "by zero") {
				t.Fatalf("the interpreter failed without naming the by-zero condition: %v", err)
			}

			check := func(what, bin string) {
				t.Helper()
				// CONTROL FIRST: the same binary, on input that must succeed.
				code, out, errb := runArtifact(t, bin, []string{"x"})
				if code != 0 {
					t.Fatalf("%s: control run exited %d (%s) — the refusal below would be "+
						"evidence about a broken artifact rather than about strictness",
						what, code, strings.TrimSpace(errb))
				}
				if got := trimFrame(out); got != "given" {
					t.Fatalf("%s: control run printed %q, want %q", what, got, "given")
				}

				code, out, errb = runArtifact(t, bin, nil)
				if code == 0 {
					t.Errorf("%s: the artifact succeeded and printed %q — the discarded "+
						"operand was not evaluated, so this lowering short-circuits. "+
						"Oath's and/or are STRICT: both operands evaluate, always.",
						what, trimFrame(out))
					return
				}
				if code != exitHostRefusal {
					t.Errorf("%s: exit %d, want %d — the exit code is the machine-readable "+
						"half of a refusal, and both backends must give a supervisor the "+
						"same answer: %s", what, code, exitHostRefusal, strings.TrimSpace(errb))
				}
				if !strings.Contains(errb, "division by zero") {
					t.Errorf("%s: failed without naming the by-zero condition, so the "+
						"failure is not evidence that the operand ran: %q", what, errb)
				}
				if got := trimFrame(out); got != "" {
					t.Errorf("%s: a refusal wrote to stdout: %q", what, got)
					if got == tc.shortCircuit {
						t.Errorf("%s: and it wrote the short-circuiting answer %q",
							what, tc.shortCircuit)
					}
				}
			}

			// THE GO BACKEND IS RUN BEFORE THE LLVM ARTIFACT IS BUILT, not after.
			// buildLLVM fails fatally, so building both up front would leave the
			// Go half of this claim unmeasured for as long as the LLVM lowering
			// is absent — and the Go half is the one that can be checked today.
			goBin, _ := buildProgram(t, st, name)
			check("the Go backend", goBin)
			check("the LLVM backend", buildLLVM(t, st, name))
		})
	}
}

// runArtifact runs a compiled artifact and returns its exit status, stdout and
// stderr. A non-zero exit is a RESULT here rather than a harness failure, which
// is what distinguishes this from threeWay's runner: everything the strictness
// tests assert is on the failing path.
func runArtifact(t *testing.T, bin string, args []string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("%s %v did not run at all: %v", bin, args, err)
		}
	}
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

// THE LOWERING THAT WAS SELECTED, asserted in the IR.
//
// The three-way tests would pass on a backend that rewrote `and` into nested
// `if`s, or `not` into `(== b false)`, provided the rewrite were correct. That
// is not a defect in them — agreement with the reference is the claim — but it
// leaves "an operator lowering exists" unwitnessed, and the same distinction
// TestStrEqualitySelectsItsOwnLowering draws for `==`.
//
// WHAT THIS ASSERTS IS DELIBERATELY WEAK: that each program EMITS, and that a
// program whose only Bool operation is `not` does not reach the Int comparison.
// It deliberately does not name an instruction, because the instruction is the
// lowering's own choice and pinning one here would make this test an obstacle to
// the implementation rather than a check on it.
func TestBooleanPrimitivesEmit(t *testing.T) {
	st := boolStore(t)
	put(t, st, `(defn only-not [] [(args (List Str))] Str
		(match args ((Nil) "no") ((Cons h t) (if (not (nonempty h)) "yes" "no"))))`)
	markVerified(t, st, "only-not")
	put(t, st, `(defn only-and [] [(args (List Str))] Str
		(match args
			((Nil) "no")
			((Cons a rest)
				(match rest
					((Nil) "no")
					((Cons b r2) (if (and (nonempty a) (nonempty b)) "yes" "no"))))))`)
	markVerified(t, st, "only-and")
	put(t, st, `(defn only-or [] [(args (List Str))] Str
		(match args
			((Nil) "no")
			((Cons a rest)
				(match rest
					((Nil) "no")
					((Cons b r2) (if (or (nonempty a) (nonempty b)) "yes" "no"))))))`)
	markVerified(t, st, "only-or")

	for _, name := range []string{"only-not", "only-and", "only-or"} {
		prog, err := planProgram(st, name)
		if err != nil {
			t.Fatalf("planProgram(%s): %v", name, err)
		}
		ir, err := emitLLVM(st, prog)
		if err != nil {
			t.Errorf("the LLVM backend refused %s: %v", name, err)
			continue
		}
		// NEITHER PROGRAM CONTAINS AN Int OPERATION: nonempty matches, and the
		// argument list is destructured by matching, so no index arithmetic
		// reaches the emitter. A Bool operator lowered as an integer comparison
		// would put o_int_eq into the closure and this would see it.
		if strings.Contains(ir, "call i32 @o_int_eq") {
			t.Errorf("%s emitted an Int comparison, and it performs no Int operation — "+
				"a Bool operator was lowered as a comparison on the tag", name)
		}
	}
}
