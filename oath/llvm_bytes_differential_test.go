package main

import (
	"os"
	"path/filepath"
	"testing"
)

// putCorpusFile loads a corpus file into a test store, so this differential
// exercises the DEFINITIONS THAT SHIPPED rather than a copy that can drift from
// them. A hand-inlined model would keep passing after examples/bytes.oath
// changed, which is the failure mode a differential exists to prevent.
func putCorpusFile(t *testing.T, st *Store, rel string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	put(t, st, string(b))
}

// The Byte/UTF-8 family must agree across eval / Go / LLVM.
//
// WHY THIS NEEDS A WITNESS AT ALL. `Byte` is an ordinary one-field datatype and
// `Bytes` is `(List Byte)`, so nothing here asks a backend for a new native
// representation -- which is exactly the claim that needs checking rather than
// assuming. The encoder is arithmetic over e-div/e-mod at four width boundaries
// and the decoder is a nested match consuming one or two bytes per codepoint; a
// backend lowering integer division differently, or getting a nested-match
// scrutinee wrong, would diverge HERE and nowhere else in the corpus.
//
// Input comes from the program's ARGUMENTS, so the compiler cannot fold the
// encoding at build time: the arithmetic runs at run time in all three.
func TestBytesUTF8ThreeWayDifferential(t *testing.T) {
	requireClang(t)
	st := newStore(t)
	for _, f := range []string{
		"examples/list.oath", "examples/str.oath", "examples/records.oath",
		"examples/ediv.oath", "examples/bytes.oath",
	} {
		putCorpusFile(t, st, f)
	}

	// Render a byte as three ASCII digits, so output is pure ASCII and a
	// disagreement shows as a digit difference rather than as mojibake that
	// looks like a terminal problem.
	put(t, st, `(defn dec3 [] [(n Int)] Str
		(SCons (+ 48 (e-mod (e-div n 100) 10))
		  (SCons (+ 48 (e-mod (e-div n 10) 10))
		    (SCons (+ 48 (e-mod n 10)) (SNil)))))`)
	put(t, st, `(defn show-bytes [] [(bs (List Byte))] Str
		(match bs
		  ((Nil) (SNil))
		  ((Cons b rest) (str-append (dec3 (byte-val b)) (show-bytes rest)))))`)

	// THE ENCODING ITSELF, byte for byte.
	put(t, st, `(defn enc [] [(args (List Str))] Str
		(match args
		  ((Nil) (SNil))
		  ((Cons s rest) (show-bytes (utf8-encode s)))))`)
	markVerified(t, st, "enc")

	// THE ROUND TRIP, through both directions.
	put(t, st, `(defn rt [] [(args (List Str))] Str
		(match args
		  ((Nil) "e")
		  ((Cons s rest) (if (utf8-roundtrips s) "y" "n"))))`)
	markVerified(t, st, "rt")

	// Every width boundary the encoder branches on, plus the input class that
	// broke the original code: non-ASCII where a byte was assumed.
	threeWay(t, st, "enc", [][]string{
		{""},
		{"a"},
		{"abc"},
		{"\u007e"}, // last EXPRESSIBLE 1-byte codepoint (U+007F is
		//                    refused by oathQuote, so the boundary is bracketed
		//                    by 7E and 80 rather than 7F and 80)
		{"\u0080"},       // first 2-byte codepoint
		{"\u00e9"},       // the accented case the webhook mis-signed
		{"\u07ff"},       // last 2-byte codepoint
		{"\u0800"},       // first 3-byte codepoint
		{"\u4e16\u754c"}, // 3-byte
		{"\uffff"},       // last 3-byte codepoint
		{"\U00010000"},   // FIRST 4-byte: the 3/4 transition, which an
		//                off-by-one at the `< 65536` branch would slip past,
		//                since the arbitrary samples either side both encode fine
		{"a\u00e9b"},   // mixed widths adjacent
		{"\U0001f512"}, // 4-byte
	})

	// The decoder's refusals, and its agreement with encode.
	threeWay(t, st, "rt", [][]string{
		{""}, {"a"}, {"abc"}, {"\u007e"}, {"\u0080"}, {"\u00e9"}, {"\u07ff"},
		{"a\u00e9b"},
		{"\u0800"},     // 3-byte: decode REFUSES, so the round trip is "n"
		{"\U0001f512"}, // 4-byte: likewise
	})
}
