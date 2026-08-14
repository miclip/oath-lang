package main

// `==` AT Str, IN THE LLVM BACKEND (#173).
//
// The webhook's build error named this: `header-first` (examples/http.oath:89)
// compares a header name against a wanted name, and this backend lowered `==`
// for Int only. It is a COMPILED use, so nothing about it is discharged by the
// property layer.
//
// WHAT IS ACTUALLY BEING CLAIMED decides what these tests have to witness. The
// language's `==` is structural equality over the SCons/SNil spine; this backend
// stores a Str as packed UTF-8 and compares bytes. Those coincide only because
// the packing is injective, which is a property of the RUNTIME's construction
// sites rather than of UTF-8. So byte-level agreement on ASCII proves very
// little, and the cases below are chosen for where the two descriptions could
// come apart:
//
//	empty vs empty          the length-first branch, and memcmp's n == 0 case
//	non-ASCII               one codepoint is several bytes, so a codepoint-wise
//	                        and a byte-wise comparison have different loops
//	prefix pairs            equal bytes as far as the shorter runs
//	a RUNTIME-BUILT value   an arena buffer against an IR constant — the two
//	                        allocation classes this runtime has
//	a TAIL VIEW             an interior pointer that is not NUL-terminated at
//	                        its own end, so slen is the only length there is
//	(SCons 97 (SNil))       the constructor spelling against the literal one:
//	                        one value, two syntaxes, and the interpreter says
//	                        they are equal
//
// The refusal test at the bottom is the control. Without it, "== is lowered"
// would be satisfied by lowering `==` at EVERY type as a byte compare, which
// would agree with the interpreter on every case above and be wrong everywhere
// else.

import (
	"strings"
	"testing"
)

// strEqStore adds the definitions that put a Str on the other side of `==`
// without letting the compiler fold it: str-append allocates, str-drop1 hands
// back a view into its argument, and first-char rebuilds a one-codepoint Str
// through the constructor.
func strEqStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(defn str-append [] [(a Str) (b Str)] Str
		(match a ((SNil) b) ((SCons c rest) (SCons c (str-append rest b)))))`)
	put(t, st, `(defn str-drop1 [] [(s Str)] Str
		(match s ((SNil) (SNil)) ((SCons c rest) rest)))`)
	put(t, st, `(defn first-char [] [(s Str)] Str
		(match s ((SNil) (SNil)) ((SCons c rest) (SCons c (SNil)))))`)
	put(t, st, `(defn arg-at [] [(args (List Str)) (n Int)] Str
		(match args
			((Nil) "")
			((Cons h rest) (if (== n 0) h (arg-at rest (- n 1))))))`)
	return st
}

// THE REFERENCE, FIXED FIRST AND WITHOUT EITHER BACKEND.
//
// `oath eval` is what both backends must agree with, so what it answers is the
// claim — not a shared expectation the two lowerings happen to meet. This test
// needs no clang and no compilation: if it ever disagrees with the table below,
// the language changed and the backends are the second thing to look at.
func TestStrEqualityInTheInterpreter(t *testing.T) {
	st := strEqStore(t)
	for _, tc := range []struct {
		expr string
		want string
	}{
		{`(== "" "")`, "true"},
		{`(== "" (SNil))`, "true"},
		{`(== "abc" "abc")`, "true"},
		{`(== (SCons 97 (SNil)) "a")`, "true"},
		// PROVENANCE-BLINDNESS, spelled at each encoding width. The reference
		// compares the codepoint sequence and knows nothing about how either
		// side was written, so a lowering that compared interned pointers, or
		// that let a literal differ from the same codepoints assembled by
		// SCons, would disagree here and nowhere in a literal-only suite.
		{`(== "abc" (SCons 97 (SCons 98 (SCons 99 (SNil)))))`, "true"},
		{`(== "é" (SCons 233 (SNil)))`, "true"},    // two bytes
		{`(== "✓" (SCons 10003 (SNil)))`, "true"},  // three
		{`(== "😀" (SCons 128512 (SNil)))`, "true"}, // four, astral
		{`(== "héllo" "héllo")`, "true"},
		{`(== "✓" "✓")`, "true"},
		{`(== "a" "b")`, "false"},
		{`(== "a" "ab")`, "false"},
		{`(== "ab" "a")`, "false"},
		{`(== "" "a")`, "false"},
		{`(== "héllo" "hello")`, "false"},
		// TWO STRINGS WHOSE UTF-8 SHARES A LEADING BYTE, so a comparison that
		// stopped at the first byte of a multi-byte scalar would call them
		// equal: U+00E9 and U+00EB are 0xC3 0xA9 and 0xC3 0xAB.
		{`(== "é" "ë")`, "false"},
	} {
		// The RENDERED type is kept in the comparison rather than trimmed off.
		// `==` is polymorphic and these operands are Str only because the
		// literals say so; an expression that quietly typed as something else
		// would still answer true or false, and the assertion would not notice.
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

// THE THREE-WAY GATE. The interpreter is the reference; two identically wrong
// lowerings agree with each other and prove nothing.
func TestStrEqualityAgreesThreeWays(t *testing.T) {
	requireClang(t)
	st := strEqStore(t)

	// Comparing two ARGUMENTS: neither side is known to the compiler, so this is
	// the shape header-first has.
	put(t, st, `(defn eq-args [] [(args (List Str))] Str
		(if (== (arg-at args 0) (arg-at args 1)) "yes" "no"))`)
	markVerified(t, st, "eq-args")

	// A RUNTIME-BUILT value against a LITERAL: an arena buffer on the left, an
	// IR constant on the right.
	put(t, st, `(defn eq-built [] [(args (List Str))] Str
		(if (== (str-append "key: " (arg-at args 0)) "key: host") "yes" "no"))`)
	markVerified(t, st, "eq-built")

	// PROVENANCE-BLINDNESS AS THE COMPILED BACKEND CAN ACTUALLY TEST IT, which
	// is NOT the interpreter's spelling of it.
	//
	// `(== "abc" (SCons 97 (SCons 98 (SCons 99 (SNil)))))` discriminates in the
	// reference and CANNOT discriminate here: strLiteral folds a constant SCons
	// chain, so both sides reach the comparison as the same IR constant and a
	// pointer-comparing lowering would answer true by accident. The compiled
	// discriminator is a buffer o_str_cons BUILT AT RUNTIME against an IR
	// literal — two different allocation classes, never the same pointer — and
	// that is what eq-cat is. Its cases run the codepoint width up to an astral
	// scalar, because the runtime encoder and the compile-time folder are
	// separate implementations of one packing and only agree if both are
	// canonical.
	put(t, st, `(defn eq-cat [] [(args (List Str))] Str
		(if (== (str-append (arg-at args 0) (arg-at args 1)) (arg-at args 2)) "yes" "no"))`)
	markVerified(t, st, "eq-cat")

	// A TAIL VIEW against an argument. The view points into the middle of its
	// parent's buffer and carries no terminator of its own.
	put(t, st, `(defn eq-view [] [(args (List Str))] Str
		(if (== (str-drop1 (arg-at args 0)) (arg-at args 1)) "yes" "no"))`)
	markVerified(t, st, "eq-view")

	// BOTH OPERANDS INTERIOR POINTERS, into DIFFERENT backing buffers, at
	// DIFFERENT byte offsets.
	//
	// eq-view above already puts a view on one side, so the shared code path —
	// the length test, then memcmp — is exercised without this, and NO MUTATION
	// WAS FOUND THAT THIS VECTOR ALONE CATCHES. That is said plainly because the
	// usual justification does not apply and pretending otherwise would be worse
	// than omitting the test: it is here because a view is the only Str in this
	// runtime whose bytes are not owned by the value holding them, and this is
	// the single configuration where that is true of BOTH operands at once.
	// Multibyte prefixes make the offsets differ (2 and 4 bytes against 1), so
	// two equal values sit at unequal addresses inside unequal parents.
	//
	// What it guards is the FUTURE, not the present. o_str_eq's correctness rests
	// on a lifetime argument written at o_str_tail, and the comment there names
	// the change that would invalidate it. If a Str ever gains a buffer that is
	// copied, rewound or reused, this is the shape that breaks first.
	put(t, st, `(defn eq-views [] [(args (List Str))] Str
		(if (== (str-drop1 (arg-at args 0)) (str-drop1 (arg-at args 1))) "yes" "no"))`)
	markVerified(t, st, "eq-views")

	// THE BARE (SNil) CONSTRUCTOR AS AN OPERAND, in each order, against a value
	// the compiler cannot see.
	//
	// `""` and `(SNil)` are one value written two ways. The interpreter table
	// pins that the reference agrees; that is not evidence about a LOWERING, in
	// the same way a `prop` is not evidence about one. The compiled side needs
	// its own witness, and it needs both orders because the two operands are
	// synthesised independently — a guard that examined only the first would pass
	// one of these and fail the other.
	put(t, st, `(defn eq-snil-right [] [(args (List Str))] Str
		(if (== (arg-at args 0) (SNil)) "yes" "no"))`)
	markVerified(t, st, "eq-snil-right")
	put(t, st, `(defn eq-snil-left [] [(args (List Str))] Str
		(if (== (SNil) (arg-at args 0)) "yes" "no"))`)
	markVerified(t, st, "eq-snil-left")

	// THE CONSTRUCTOR SPELLING AGAINST THE LITERAL ONE, with the codepoint
	// coming from the input so nothing folds: first-char rebuilds (SCons c
	// (SNil)) at run time, and it must equal the one-character literal.
	put(t, st, `(defn eq-first [] [(args (List Str))] Str
		(if (== (first-char (arg-at args 0)) (arg-at args 1)) "yes" "no"))`)
	markVerified(t, st, "eq-first")

	// THE FOLDED CONSTRUCTOR, as a control on the one above: (SCons 97 (SNil))
	// written literally is the same value as "a", and this backend folds it to
	// the same constant. If folding ever stopped agreeing with the interpreter
	// the comparison would answer no.
	put(t, st, `(defn eq-literal-cons [] [(args (List Str))] Str
		(if (== (SCons 97 (SNil)) "a") "yes" "no"))`)
	markVerified(t, st, "eq-literal-cons")

	vectors := map[string][][]string{
		"eq-args": {
			nil,                // both missing → "" == ""
			{""},               // "" == ""
			{"", ""},           // the empty pair, explicitly
			{"a", "a"},         // ASCII, equal
			{"a", "b"},         // ASCII, unequal, same length
			{"a", "ab"},        // a prefix
			{"ab", "a"},        // the other direction
			{"", "a"},          // empty against non-empty
			{"héllo", "héllo"}, // non-ASCII, equal
			{"héllo", "hello"}, // non-ASCII against its ASCII neighbour
			{"é", "ë"},         // one shared leading byte
			{"✓", "✓"},         // three-byte scalar
			{"a name with spaces and ü", "a name with spaces and ü"},
			{"X-GitHub-Event", "X-GitHub-Event"}, // the webhook's own shape
			{"X-GitHub-Event", "X-Hub-Signature-256"},
		},
		"eq-built": {
			nil,
			{"host"},
			{"hosts"},
			{""},
			{"hóst"},
		},
		"eq-cat": {
			nil,
			{"ab", "c", "abc"},                   // one byte per scalar
			{"ab", "c", "abd"},                   // and the near miss
			{"h", "\u00f3st", "h\u00f3st"},       // two bytes
			{"\u2713", "ok", "\u2713ok"},         // three
			{"\U0001F600", "ok", "\U0001F600ok"}, // four, astral
			{"\U0001F600", "ok", "\U0001F600OK"}, // the astral near miss
			{"", "", ""},                         // both halves empty
			{"a", "", "a"},                       // an empty tail
			{"", "a", "a"},                       // an empty head
			{"abc", "", "abcd"},                  // a proper prefix of the expected value
			// NO NORMALIZATION. `==` compares CODEPOINTS, so the precomposed
			// U+00E9 and the decomposed "e" + U+0301 are different values that
			// most readers would call the same character — and they become equal
			// only when the combining mark is appended to the right base.
			{"\u00e9", "", "e\u0301"},
			{"e", "\u0301", "e\u0301"},
		},
		"eq-view": {
			nil,
			{"abc", "bc"},
			{"abc", "abc"},
			{"", ""},
			{"a", ""},
			{"ünder", "nder"}, // dropping a two-byte scalar
			{"✓ok", "ok"},
		},
		"eq-views": {
			nil,
			{"abc", "xbc"},          // equal views, SAME offset
			{"éxy", "axy"},          // equal views, offsets 2 and 1
			{"\U0001F600xy", "axy"}, // equal views, offsets 4 and 1
			{"\U0001F600xy", "éxy"}, // equal views, offsets 4 and 2
			{"éxy", "axz"},          // unequal, same length
			{"abc", "xy"},           // unequal, different lengths
			{"a", "b"},              // two EMPTY views
			{"é", "\U0001F600"},     // two empty views, offsets 2 and 4
			{"\U0001F600✓", "a✓"},   // equal multibyte remainders
			{"\U0001F600✓", "aé"},   // unequal multibyte remainders
		},
		"eq-snil-right": {
			nil,            // no argument at all → "" == (SNil)
			{""},           // the empty argument
			{"a"},          // non-empty, one byte
			{"é"},          // non-empty, two bytes
			{"\U0001F600"}, // non-empty, four bytes
		},
		"eq-snil-left": {
			nil,
			{""},
			{"a"},
			{"é"},
			{"\U0001F600"},
		},
		"eq-first": {
			nil,
			{"abc", "a"},
			{"abc", "b"},
			{"", ""},
			{"ünder", "ü"},
			{"✓ok", "✓"},
		},
	}

	// EACH TABLE MUST DISCRIMINATE BEFORE IT IS RUN THROUGH threeWay, because
	// threeWay asserts AGREEMENT and nothing else: a table whose every vector
	// answers "no" agrees perfectly across all three paths while never once
	// exercising the equal case, and it looks exactly like a thorough table.
	// So the interpreter's own answers are collected first and both outcomes
	// are required. This is the CONTROL for the measurement, not a second
	// assertion about semantics — it says the vectors can tell the two apart.
	for _, entry := range []string{
		"eq-args", "eq-built", "eq-cat", "eq-view", "eq-views", "eq-first",
		"eq-snil-right", "eq-snil-left",
	} {
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
				"comparison ever having to be right", entry, seen)
		}
		threeWay(t, st, entry, vectors[entry])
	}
	threeWay(t, st, "eq-literal-cons", [][]string{nil, {"ignored"}})
}

// THE LOWERING THAT WAS SELECTED, asserted in the IR.
//
// The three-way tests above would pass if `==` at Str were routed through the
// INT comparison and that comparison happened to be right — it would not be, but
// the assertion is cheap and it is what distinguishes "the Str path ran" from
// "some path ran". It is also the control for the hoisting this change did: the
// operand-type synthesis moved out of the Int branch so both guards read it, and
// the failure mode of that refactor is an Int comparison arriving at o_str_eq.
func TestStrEqualitySelectsItsOwnLowering(t *testing.T) {
	st := strEqStore(t)
	// NEITHER PROGRAM MAY CONTAIN THE OTHER'S COMPARISON, or the absence half of
	// each assertion is vacuous — the emitted IR is the whole dependency closure,
	// so a helper doing `(== n 0)` on an index would put o_int_eq into a program
	// that compares only strings. Both entries destructure the argument list by
	// matching, which needs no Int at all.
	put(t, st, `(defn cmp-str [] [(args (List Str))] Str
		(match args
			((Nil) "no")
			((Cons a rest)
				(match rest ((Nil) "no") ((Cons b r2) (if (== a b) "yes" "no"))))))`)
	markVerified(t, st, "cmp-str")
	put(t, st, `(defn str-len [] [(s Str)] Int
		(match s ((SNil) 0) ((SCons c rest) (+ 1 (str-len rest)))))`)
	put(t, st, `(defn cmp-int [] [(args (List Str))] Str
		(match args
			((Nil) "no")
			((Cons a rest)
				(match rest
					((Nil) "no")
					((Cons b r2) (if (== (str-len a) (str-len b)) "yes" "no"))))))`)
	markVerified(t, st, "cmp-int")

	// THE BARE CONSTRUCTOR SPELLING OF THE EMPTY Str, on each side in turn.
	//
	// `""` and `(SNil)` are one value written two ways, and the guard selects on
	// the SYNTHESISED type rather than on the syntax — but that is a claim about
	// the checker, and a guard that rests on a neighbouring component's behaviour
	// is exactly what this file refuses to take on trust elsewhere. The
	// interpreter table already pins `(== "" (SNil))`; this pins that the
	// compiled backend lowers it, in both argument orders, since the two operands
	// are synthesised independently.
	put(t, st, `(defn cmp-snil-right [] [(args (List Str))] Str
		(match args ((Nil) "no") ((Cons a rest) (if (== a (SNil)) "yes" "no"))))`)
	markVerified(t, st, "cmp-snil-right")
	put(t, st, `(defn cmp-snil-left [] [(args (List Str))] Str
		(match args ((Nil) "no") ((Cons a rest) (if (== (SNil) a) "yes" "no"))))`)
	markVerified(t, st, "cmp-snil-left")

	ir := func(name string) string {
		t.Helper()
		prog, err := planProgram(st, name)
		if err != nil {
			t.Fatal(err)
		}
		out, err := emitLLVM(st, prog)
		if err != nil {
			t.Fatalf("emitLLVM(%s): %v", name, err)
		}
		return out
	}

	strIR := ir("cmp-str")
	if !strings.Contains(strIR, "call i32 @o_str_eq") {
		t.Error("a Str comparison emitted no o_str_eq call")
	}
	if strings.Contains(strIR, "call i32 @o_int_eq") {
		t.Error("a Str comparison reached the Int comparison, which would compare " +
			"two Str values as sign-magnitude integers")
	}

	// The other direction: the Int lowering must not have been widened to
	// everything. cmp-int compares two lengths and no strings.
	intIR := ir("cmp-int")
	if !strings.Contains(intIR, "call i32 @o_int_eq") {
		t.Error("an Int comparison emitted no o_int_eq call")
	}
	if strings.Contains(intIR, "call i32 @o_str_eq") {
		t.Error("an Int comparison reached the Str comparison, which would compare " +
			"two magnitudes as packed UTF-8")
	}

	for _, name := range []string{"cmp-snil-right", "cmp-snil-left"} {
		if !strings.Contains(ir(name), "call i32 @o_str_eq") {
			t.Errorf("%s emitted no o_str_eq call: the empty Str written as the bare "+
				"(SNil) constructor did not reach the Str lowering", name)
		}
	}
}

// WHAT MAKES IT REFUSE, which is the half that says this is a SUBSET and not a
// byte compare wearing `==`'s name.
//
// `==` is polymorphic over every first-order type in the language. This change
// lowered exactly one instance of it. A `(List Str)` comparison and an Option
// comparison must still be refused BY NAME, because a backend that answered them
// with the Str lowering would be comparing a constructor spine's bytes — which
// are not bytes at all.
func TestLLVMStillRefusesEqualityAtOtherTypes(t *testing.T) {
	st := strEqStore(t)
	put(t, st, `(data Option [a] (None) (Some a))`)
	put(t, st, `(defn eq-list [] [(args (List Str))] Str
		(if (== args (Nil [Str])) "yes" "no"))`)
	markVerified(t, st, "eq-list")
	put(t, st, `(defn eq-option [] [(args (List Str))] Str
		(if (== (Some [Str] "a") (None [Str])) "yes" "no"))`)
	markVerified(t, st, "eq-option")

	for _, name := range []string{"eq-list", "eq-option"} {
		prog, err := planProgram(st, name)
		if err != nil {
			t.Fatalf("planProgram(%s): %v", name, err)
		}
		if _, err := emitLLVM(st, prog); err == nil {
			t.Fatalf("the LLVM backend accepted %s, which compares a datatype it "+
				"cannot lower equality for", name)
		} else {
			r, ok := refusedFor(err)
			if !ok {
				t.Fatalf("%s was refused, but not as a backend subset boundary: %v", name, err)
			}
			if r != reasonPrim {
				t.Errorf("%s was refused with reason %q, want %q", name, r, reasonPrim)
			}
			if !strings.Contains(err.Error(), `"=="`) {
				t.Errorf("%s: the refusal does not name the operation: %v", name, err)
			}
		}
	}
}

// THE FALSIFIER'S OWN SHAPE, run end to end: header-first over a header list,
// which is the definition #173's build error named. It is not the same as
// eq-args — the comparison is inside a recursive search over a list of pairs,
// so a lowering that worked only at the top of a function body would fail here.
func TestLLVMLowersHeaderFirstShape(t *testing.T) {
	requireClang(t)
	st := strEqStore(t)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Option [a] (None) (Some a))`)
	put(t, st, `(defn hdr-first [] [(hs (List (Pair Str Str))) (name Str)] (Option Str)
		(match hs
			((Nil) (None [Str]))
			((Cons h rest)
				(match h
					((Pair k v) (if (== k name) (Some [Str] v) (hdr-first rest name)))))))`)
	// The entry pairs consecutive arguments into headers and looks one up, so
	// every input the CLI can carry reaches the search.
	put(t, st, `(defn pairs [] [(args (List Str))] (List (Pair Str Str))
		(match args
			((Nil) (Nil [(Pair Str Str)]))
			((Cons k rest)
				(match rest
					((Nil) (Nil [(Pair Str Str)]))
					((Cons v rest2) (Cons [(Pair Str Str)] (Pair [Str Str] k v) (pairs rest2)))))))`)
	put(t, st, `(defn lookup [] [(args (List Str))] Str
		(match args
			((Nil) "<no name>")
			((Cons name rest)
				(match (hdr-first (pairs rest) name)
					((None) "<absent>")
					((Some v) v)))))`)
	markVerified(t, st, "lookup")

	threeWay(t, st, "lookup", [][]string{
		nil,
		{"X-GitHub-Event"},
		{"X-GitHub-Event", "X-GitHub-Event", "push"},
		{"X-GitHub-Event", "x-github-event", "push"}, // case is not folded
		{"X-GitHub-Event", "Host", "example", "X-GitHub-Event", "ping"},
		{"X-GitHub-Event", "X-GitHub-Event", "first", "X-GitHub-Event", "second"}, // first wins
		{"Ünicode", "Ünicode", "yes"},
		{"", "", "empty-name"},
	})
}
