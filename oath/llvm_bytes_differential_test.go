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

	// The decoder's agreement with encode, now across all four widths.
	threeWay(t, st, "rt", [][]string{
		{""}, {"a"}, {"abc"}, {"\u007e"}, {"\u0080"}, {"\u00e9"}, {"\u07ff"},
		{"a\u00e9b"},
		{"\u0800"}, {"\u4e16\u754c"}, {"\uffff"}, // 3-byte, incl. its boundaries
		{"\U00010000"}, {"\U0001f512"}, {"\U0010ffff"}, // 4-byte, incl. both ends
		{"a\u07ff\u0800\U00010000"}, // every width in one string
	})

	// THE REFUSAL BRANCHES, which the round trip cannot reach.
	//
	// `rt` only ever feeds the decoder bytes the ENCODER produced, so it
	// witnesses agreement on VALID input and says nothing about the branches
	// that reject. Those branches are the new code, and they are where a
	// backend that lowered a comparison or a nested match differently would
	// diverge -- so they need an input path of their own.
	//
	// The bytes come from the ARGUMENT's codepoints, one byte each, so a
	// hostile sequence is ordinary run-time data rather than a literal the
	// compiler could fold. That is what lets a malformed sequence -- which is
	// by definition not the encoding of any string -- be written as one.
	put(t, st, `(defn as-bytes [] [(s Str)] (List Byte)
		(match s
		  ((SNil) (Nil [Byte]))
		  ((SCons c cs) (Cons [Byte] (MkByte c) (as-bytes cs)))))`)
	put(t, st, `(defn show-codes [] [(s Str)] Str
		(match s
		  ((SNil) (SNil))
		  ((SCons c cs) (str-append (dec3 c) (show-codes cs)))))`)
	// Reports the REFUSAL and, when it accepts, the codepoints it produced --
	// so a backend that accepts the right sequences for the wrong reason, or
	// decodes to the wrong scalar, is caught too. A bare y/n would not see it.
	put(t, st, `(defn dec [] [(args (List Str))] Str
		(match args
		  ((Nil) "e")
		  ((Cons s rest)
		    (match (utf8-decode (as-bytes s))
		      ((None) "refused")
		      ((Some x) (show-codes x))))))`)
	markVerified(t, st, "dec")

	threeWay(t, st, "dec", [][]string{
		{""},                         // empty decodes to empty
		{"\u0041\u0042"},             // plain ASCII
		{"\u00c3\u00a9"},             // C3 A9 -> U+00E9, valid
		{"\u00e2\u0082\u00ac"},       // E2 82 AC -> U+20AC, valid 3-byte
		{"\u00f0\u009f\u0094\u0092"}, // F0 9F 94 92 -> U+1F512, valid 4-byte
		{"\u00c0\u0080"},             // OVERLONG NUL -- the classic filter bypass
		{"\u00e0\u0080\u00af"},       // overlong 3-byte spelling of "/"
		{"\u00f0\u0080\u0080\u00af"}, // overlong 4-byte spelling of "/"
		{"\u00ed\u00a0\u0080"},       // ED A0 80 -- an encoded surrogate
		{"\u00f4\u0090\u0080\u0080"}, // above U+10FFFF
		{"\u00f5\u0080\u0080\u0080"}, // lead byte beyond the legal range
		{"\u00c3\u0028"},             // lead followed by a NON-continuation
		{"\u0080"},                   // a lone continuation byte
		{"\u00e2\u0082"},             // TRUNCATED 3-byte sequence
		{"\u00f0\u009f\u0094"},       // truncated 4-byte sequence
		{"\u0041\u00c0\u0080\u0042"}, // a refusal must reject the WHOLE
		//                        input, not decode the valid prefix around it

		// CONTINUATION-BYTE BOUNDARIES, in sequences that are otherwise valid.
		// Without these the refusal rows miss a backend that lowers `<=` as
		// `<`: every other vector here keeps its continuations away from 0x80
		// and 0xBF, so the off-by-one changes no verdict and the rows agree.
		// Found by mutating the comparison and watching these rows stay green.
		{"\u00c2\u0080"},             // C2 80 -> U+0080, continuation is exactly 0x80
		{"\u00df\u00bf"},             // DF BF -> U+07FF, continuation is exactly 0xBF
		{"\u00e0\u00a0\u0080"},       // E0 A0 80 -> U+0800, trailing 0x80
		{"\u00ef\u00bf\u00bf"},       // EF BF BF -> U+FFFF, both at 0xBF
		{"\u00f4\u008f\u00bf\u00bf"}, // F4 8F BF BF -> U+10FFFF, the last scalar
		{"\u00c2\u007e"},             // C2 7E -- BELOW the continuation range. 0x7F would
		//                    be the adjacent byte, but oathQuote refuses DEL,
		//                    so the range is bracketed at 7E as it is in `enc`
		{"\u00c2\u00c0"}, // C2 C0 -- one ABOVE it
	})
}
