package main

import (
	"strings"
	"testing"
)

// `oath eval` used to print a Str result as its SCons/SNil codepoint tower —
// (SCons 99 (SCons 97 ...)) — which is unreadable. It now renders the active Str
// as text, recursively, so a Str nested in a List/Pair/record is text too, while
// every other datatype is unchanged.
func TestEvalRendersStrAsText(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st) // Str, List, Option, Pair, length

	cases := []struct{ expr, want string }{
		{`"cat"`, `"cat" : Str`},
		{`(SNil)`, `"" : Str`},
		{`(SCons 104 (SCons 105 (SCons 10 (SNil))))`, `"hi\n" : Str`}, // newline escaped
		{`(SCons 233 (SNil))`, `"é" : Str`}, // unicode codepoint
		// Non-scalar codepoints are NOT collapsed to U+FFFD — the value stays
		// structural, so distinct Str values stay distinguishable.
		{`(SCons -1 (SNil))`, `(SCons -1 SNil) : Str`},           // negative
		{`(SCons 55296 (SNil))`, `(SCons 55296 SNil) : Str`},     // surrogate U+D800
		{`(SCons 1114112 (SNil))`, `(SCons 1114112 SNil) : Str`}, // > U+10FFFF
		{`(SCons 104 (SCons 55296 (SNil)))`, `(SCons 104 (SCons 55296 SNil)) : Str`}, // one bad codepoint → whole value structural
		{`(Cons [Str] "a" (Cons [Str] "bee" (Nil [Str])))`, `(Cons "a" (Cons "bee" Nil)) : (List Str)`}, // nested
		{`(Pair [Str Int] "key" 42)`, `(Pair "key" 42) : (Pair Str Int)`},                              // nested + non-str field
		{`(Some [Int] 5)`, `(Some 5) : (Option Int)`},                                                  // non-Str datatype unchanged
		{`(+ 2 3)`, `5 : Int`},                                                                         // primitive unchanged
	}
	for _, tc := range cases {
		out, err := evalDisplay(st, tc.expr)
		if err != nil {
			t.Fatalf("eval %s: %v", tc.expr, err)
		}
		if out != tc.want {
			t.Errorf("eval %s\n  got:  %s\n  want: %s", tc.expr, out, tc.want)
		}
	}
}

// A structurally identical datatype put AFTER Str under different constructor
// names shares Str's hash and can flip the PRIMARY constructor names ctorName
// returns. The decoder resolves Str's constructors from the Str binding's OWN
// naming block, by index, so it must still render a Str value as text. (If the
// two do not merge in this store, the assertion still holds trivially.)
func TestEvalStrDecodeSurvivesAlias(t *testing.T) {
	st := newStore(t)
	strMapPrelude(t, st)
	strHashBefore, _ := st.Resolve("Str")
	put(t, st, `(data Text [] (Empty) (More Int Text))`)
	textHash, _ := st.Resolve("Text")
	merged := textHash == strHashBefore
	t.Logf("Text and Str share a hash (merged alias): %v", merged)

	out, err := evalDisplay(st, `"cat"`)
	if err != nil {
		t.Fatalf("eval \"cat\": %v", err)
	}
	// The VALUE must decode to text — the point of #P2b. (The type name may report
	// as the merged alias's primary name, which is the store's existing behaviour
	// for any aliased type and orthogonal to Str decoding.)
	if !strings.HasPrefix(out, `"cat" : `) {
		t.Errorf("aliased Str value did not decode to text (still a ctor tower?): got %s", out)
	}
}

// A datatype BOUND as Str whose binary constructor's second field is not the
// recursive self is not a string. Decoding it by arity alone would render its
// nullary constructor as "" and lose information; the (Int, rec) shape check must
// decline, so the value renders structurally.
func TestEvalDoesNotDecodeNonStrBoundAsStr(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Str [] (Empty) (More Int Int))`) // arities match Str, shape does not
	out, err := evalDisplay(st, `(Empty)`)
	if err != nil {
		t.Fatalf("eval (Empty): %v", err)
	}
	if strings.HasPrefix(out, `"`) {
		t.Errorf("a non-Str datatype bound as Str was decoded as text: %s", out)
	}
}
