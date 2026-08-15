package main

// THE #78 CRYPTO BOUNDARY IN THE LLVM BACKEND: `hmac-sha256` AND `bytes-eq-ct`.
//
// This is what `gh-webhook` refused on. The Go backend lowers both to the host's
// library — `crypto/hmac` and `crypto/subtle`, commented there as the trusted
// boundary — and this backend cannot: it emits textual IR, hands it to clang and
// links nothing but libc, which is the property that makes "two independent
// backends" a fact about the artifacts rather than a packaging detail. So the
// runtime spells SHA-256, HMAC and the constant-time compare out by hand, and
// ONE CONTRACT IS SATISFIED BY OPPOSITE MEANS. That is the arrangement, not a
// tension: a primitive names an OPERATION, and how a backend realises it is the
// backend's business, exactly as a capability kind's provider is.
//
// WHICH DECIDES WHAT THESE TESTS HAVE TO BE. Two implementations of a FIXED
// algorithm agreeing with each other is weak evidence — they can share a
// misreading of the padding rule and agree forever. The published answer is
// free, so correctness is checked against it: the FIPS 180-2 SHA-256 examples
// against the compress function directly, and the RFC 4231 HMAC-SHA256 vectors
// through the language, with the interpreter as the reference for the three-way
// gate on top.
//
//	THE CONSTANT-TIME PROPERTY IS NOT WITNESSED BY ANY TEST IN THIS FILE, AND NO
//	TEST IN THIS FILE COULD WITNESS IT.
//
// `bytes-eq-ct` returns exactly what memcmp returns for every input; only the
// timing differs. A memcmp lowering passes the three-way gate, the differential
// ratchet and every property in the corpus. There is no mutation of the RESULT
// that detects it, so the evidence is an ARGUMENT ABOUT THE EMITTED CODE, made
// at the function in llvm.go, and timing here is ARGUED rather than MEASURED.
// What the last two tests below pin is not the timing but the two things the
// argument rests on and that could silently change under it: the `volatile`
// accumulator in the source, and the absence of a memcmp call in the compiled
// function. Both are controlled — the second by injecting the defect and
// watching the scanner see it — so neither is a probe that cannot fail.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cryptoStore is llvmStore plus the plumbing a byte-oriented program needs:
// byte-list builders, and hex so a digest can leave a CLI program as a Str.
//
// EVERY OPERAND IS BUILT AT RUNTIME OR TAKEN FROM argv. `rep` and `up-from`
// recurse on a counter, `str-bytes` walks an argument, and the entries dispatch
// on argv — so nothing here is a constant a folder could evaluate at compile
// time and none of the digests below can be sitting in the binary already.
func cryptoStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	// n copies of one byte, and n bytes counting up — between them they build
	// every RFC 4231 key and message that is not text.
	put(t, st, `(defn rep [] [(n Int) (b Int)] (List Int)
		(if (== n 0) (Nil [Int]) (Cons [Int] b (rep (- n 1) b))))`)
	put(t, st, `(defn up-from [] [(n Int) (from Int)] (List Int)
		(if (== n 0) (Nil [Int]) (Cons [Int] from (up-from (- n 1) (+ from 1)))))`)
	// A Str's CODEPOINTS as a byte list. Sound only while every codepoint is
	// below 256, which is why the vectors below are ASCII — and it is also what
	// makes the out-of-range tests easy to trigger, since any other scalar is
	// outside 0..255 by construction.
	put(t, st, `(defn str-bytes [] [(s Str)] (List Int)
		(match s ((SNil) (Nil [Int])) ((SCons c rest) (Cons [Int] c (str-bytes rest)))))`)
	put(t, st, `(defn str-cat [] [(a Str) (b Str)] Str
		(match a ((SNil) b) ((SCons c r) (SCons c (str-cat r b)))))`)
	put(t, st, `(defn hex-nib [] [(n Int)] Str (SCons (if (< n 10) (+ 48 n) (+ 87 n)) (SNil)))`)
	put(t, st, `(defn hex-byte [] [(n Int)] Str (str-cat (hex-nib (/ n 16)) (hex-nib (% n 16))))`)
	put(t, st, `(defn hex-of [] [(bs (List Int))] Str
		(match bs ((Nil) "") ((Cons h t) (str-cat (hex-byte h) (hex-of t)))))`)
	return st
}

// macVectors is the entry every HMAC assertion below runs, selecting a vector by
// name from argv so the choice is made at run time.
const macVectors = `(defn mac-vector [] [(args (List Str))] Str
	(match args
		((Nil) "no vector named")
		((Cons name rest)
			(if (== name "empty") (hex-of (hmac-sha256 (Nil [Int]) (Nil [Int])))
			(if (== name "tc1") (hex-of (hmac-sha256 (rep 20 11) (str-bytes "Hi There")))
			(if (== name "tc2") (hex-of (hmac-sha256 (str-bytes "Jefe") (str-bytes "what do ya want for nothing?")))
			(if (== name "tc3") (hex-of (hmac-sha256 (rep 20 170) (rep 50 221)))
			(if (== name "tc4") (hex-of (hmac-sha256 (up-from 25 1) (rep 50 205)))
			(if (== name "tc5") (hex-of (hmac-sha256 (rep 20 12) (str-bytes "Test With Truncation")))
			(if (== name "tc6") (hex-of (hmac-sha256 (rep 131 170) (str-bytes "Test Using Larger Than Block-Size Key - Hash Key First")))
			(if (== name "tc7") (hex-of (hmac-sha256 (rep 131 170) (str-bytes "This is a test using a larger than block-size key and a larger than block-size data. The key needs to be hashed before being used by the HMAC algorithm.")))
			(if (== name "block55") (hex-of (hmac-sha256 (str-bytes "Jefe") (rep 55 97)))
			(if (== name "block56") (hex-of (hmac-sha256 (str-bytes "Jefe") (rep 56 97)))
			(if (== name "block64") (hex-of (hmac-sha256 (str-bytes "Jefe") (rep 64 97)))
				"unknown vector"))))))))))))))`

// THE PUBLISHED ANSWERS. RFC 4231 §4, HMAC-SHA-256, full 32-byte output.
//
// Test cases 6 and 7 are the reason the list runs this long: their key is 131
// bytes, which is the ONLY clause of RFC 2104 that hashes the key before use.
// Every shorter key takes the other branch, so an implementation that skipped
// the clause entirely passes 1 through 5.
var rfc4231 = map[string]string{
	"tc1": "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
	"tc2": "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
	"tc3": "773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe",
	"tc4": "82558a389a443c0ea4cc819899f2083a85f0faa3e578f8077a2e3ff46729665b",
	"tc5": "a3b6167473100ee06e0c796c2955552bfa6f7c0a6a8aef8b93f860aab0cd20c5",
	"tc6": "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54",
	"tc7": "9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2",
}

// THE HMAC VECTORS, THROUGH THE LANGUAGE, AGAINST THE PUBLISHED ANSWER AND
// ACROSS ALL THREE PATHS.
//
// The published constant is the reference here, and `threeWay` supplies the rest:
// it binds both backends to the INTERPRETER, so with the interpreter pinned to
// RFC 4231 all three are pinned to it. Asserting the constant against the
// compiled output directly would be the same statement with a weaker failure
// message — a disagreement would not say WHICH path had drifted.
func TestHmacVectorsAgreeThreeWaysAndMatchRFC4231(t *testing.T) {
	requireClang(t)
	st := cryptoStore(t)
	put(t, st, macVectors)
	markVerified(t, st, "mac-vector")

	for name, want := range rfc4231 {
		lit, ok := oathList([]string{name})
		if !ok {
			t.Fatalf("%s cannot be written as an Oath literal", name)
		}
		if got := evalDenotation(t, st, "(mac-vector "+lit+")"); got != want {
			t.Errorf("RFC 4231 %s: the INTERPRETER answers %s, want %s. The published "+
				"vector is the reference — this is a language-level defect, not a "+
				"backend one", name, got, want)
		}
	}

	// THE CONTROL FOR THE TABLE ITSELF. A dispatch that fell through to
	// "unknown vector" for every name would make the loop above compare the same
	// string to seven different constants and fail loudly — but a dispatch that
	// answered the FIRST vector for every name would agree with itself across all
	// three paths and quietly test one case seven times.
	seen := map[string]bool{}
	for name := range rfc4231 {
		lit, _ := oathList([]string{name})
		seen[evalDenotation(t, st, "(mac-vector "+lit+")")] = true
	}
	if len(seen) != len(rfc4231) {
		t.Errorf("the %d vector names produce %d distinct digests, so the entry is not "+
			"dispatching on its argument and these cases are not independent",
			len(rfc4231), len(seen))
	}

	// AND THE THREE PATHS, over the published vectors plus the block-boundary
	// messages. The boundary cases are here rather than only in the SHA-256
	// driver because they reach the padding through HMAC's inner hash, which is
	// the way a compiled Oath program actually reaches it: an inner hash over
	// ipad(64) || msg puts the message end at 119, 120 and 128 bytes, i.e. 55, 56
	// and 0 modulo the block.
	threeWay(t, st, "mac-vector", [][]string{
		nil,
		{"empty"},
		{"tc1"}, {"tc2"}, {"tc3"}, {"tc4"}, {"tc5"}, {"tc6"}, {"tc7"},
		{"block55"}, {"block56"}, {"block64"},
		{"unknown"},
	})
}

// ---------- SHA-256 against the published examples, directly ----------

// THE HASH IS NOT A PRIMITIVE, SO IT IS TESTED WHERE IT LIVES.
//
// Oath exposes `hmac-sha256`, never a bare SHA-256, so the published SHA-256
// vectors cannot be run through the language at all. They can be run against the
// emitted runtime, which is what this does: a driver that #includes rt.c, reads a
// message on stdin and prints its digest. That is the same technique the arena
// tests use, and it is what makes the empty message and the exact 55/56/64-byte
// padding boundary reachable — through HMAC the message length is always offset
// by the 64-byte ipad, so those inputs are not expressible from outside.
func TestSha256MatchesPublishedVectors(t *testing.T) {
	requireClang(t)

	driver := `#include "rt.c"
int main(void) {
  size_t cap = 1u << 21, n = 0;
  unsigned char *buf = (unsigned char *)malloc(cap);
  if (!buf) return 2;
  for (;;) {
    if (n == cap) { fputs("input larger than the driver's buffer\n", stderr); return 3; }
    size_t got = fread(buf + n, 1, cap - n, stdin);
    n += got;
    if (got == 0) break;
  }
  unsigned char d[32];
  o_sha256(buf, n, d);
  for (int i = 0; i < 32; i++) printf("%02x", d[i]);
  printf("\n");
  return 0;
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.c"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "driver")
	cc := exec.Command("clang", "-O1", "-o", bin, "driver.c")
	cc.Dir = dir
	if b, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("the driver did not compile, so nothing below ran: %v\n%s", err, b)
	}

	rep := func(b byte, n int) []byte {
		out := make([]byte, n)
		for i := range out {
			out[i] = b
		}
		return out
	}
	for _, tc := range []struct {
		name, want, source string
		msg                []byte
	}{
		// PUBLISHED, AND CITED BY THE MESSAGE RATHER THAN BY A SECTION NUMBER.
		// A remembered appendix reference is exactly the kind of precision that
		// is wrong without anything noticing; the MESSAGE identifies the vector
		// unambiguously and is checkable against any copy of the standard.
		{"empty", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"the empty message — the most widely republished SHA-256 constant there is", nil},
		{"abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
			"NIST's one-block SHA-256 example (FIPS 180-2 Appendix B)", []byte("abc")},
		{"nist-448-bit", "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
			"NIST's two-block SHA-256 example — 56 bytes, the length that does NOT fit its own block",
			[]byte("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")},
		{"nist-896-bit", "cf5b16a778af8380036ce59e7b0492370b249b11e8f07a51afac45037afee9d1",
			"NIST's 896-bit SHA-256 example — 112 bytes, two full blocks and a padding block",
			[]byte("abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu")},
		{"million-a", "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0",
			"FIPS 180-2's long-message example — one million 'a', where a 32-bit bit-length would overflow",
			rep('a', 1000000)},
		// NOT PUBLISHED, AND SAID SO. The padding boundary NIST publishes as a
		// worked example is the 56-byte case above; 55, 63, 64 and 65 need
		// vectors of their own, and these were taken from two implementations
		// that are not the one under test — the system `shasum -a 256` (Apple's
		// libcrypto) and Go's crypto/sha256 — which agreed. Calling them
		// published would be the overclaim; leaving the lengths untested would be
		// the gap. (Every PUBLISHED row above was confirmed the same way, which
		// is why a wrong constant here is a failing test rather than a silent
		// wrong answer.)
		{"55a", "9f4390f8d30c2dd92ec9f095b65e2b9ae9b0a925a5258e241c9f1e910f734318",
			"cross-checked, not published — 55 bytes, the last length whose padding fits one block", rep('a', 55)},
		{"56a", "b35439a4ac6f0948b6d6f9e3c6af0f5f590ce20f1bde7090ef7970686ec6738a",
			"cross-checked, not published — 56 bytes, the first length needing a second block", rep('a', 56)},
		{"63a", "7d3e74a05d7db15bce4ad9ec0658ea98e3f06eeecf16b4c6fff2da457ddc2f34",
			"cross-checked, not published — one byte short of a full block", rep('a', 63)},
		{"64a", "ffe054fe7ae0cb6dc65c3af9b61d5209f439851db43d0ba5997337df154668eb",
			"cross-checked, not published — exactly one block, so the padding block is all padding", rep('a', 64)},
		{"65a", "635361c48bb9eab14198e76ea8ab7f1a41685d6ad62aa9146d301d4f17eb0ae0",
			"cross-checked, not published — one byte into the second block", rep('a', 65)},
	} {
		cmd := exec.Command(bin)
		cmd.Stdin = bytes.NewReader(tc.msg)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: the driver failed: %v — %s", tc.name, err, errb.String())
		}
		if got := strings.TrimSpace(out.String()); got != tc.want {
			t.Errorf("SHA-256 of %s (%d bytes) = %s, want %s\n  vector: %s",
				tc.name, len(tc.msg), got, tc.want, tc.source)
		}
	}

	// THE DRIVER'S OWN CONTROL. Every assertion above is "the digest equals this
	// constant", which a driver that printed a fixed string would also satisfy if
	// the constants happened to be that string — they do not, but a driver that
	// printed NOTHING would fail every case with an identical message and look
	// like a broken hash rather than a broken harness. One byte changed must move
	// the digest.
	one := exec.Command(bin)
	one.Stdin = strings.NewReader("abd")
	got, err := one.Output()
	if err != nil {
		t.Fatalf("the control run failed: %v", err)
	}
	if strings.TrimSpace(string(got)) == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Error("the driver answers the digest of \"abc\" for \"abd\", so it is not hashing " +
			"its input and the vectors above are not evidence")
	}
}

// ---------- bytes-eq-ct, as a VALUE ----------

// THE VALUE CONTRACT, WHICH IS ALL THAT IS TESTABLE HERE.
//
// Length is not secret and a mismatch answers false rather than erroring, which
// is the reference's behaviour and the one the corpus depends on: malformed hex
// decodes to the empty list, and the empty list must simply not match a 32-byte
// digest. Everything below is argv-driven so no comparison is decided at compile
// time.
func TestBytesEqCtAgreesThreeWays(t *testing.T) {
	requireClang(t)
	st := cryptoStore(t)
	// The two operands are the two arguments, converted through their codepoints.
	// A missing argument is the EMPTY byte list, which is how the empty/empty and
	// empty/non-empty cases are reached without a literal.
	put(t, st, `(defn ct-eq [] [(args (List Str))] Str
		(match args
			((Nil) (if (bytes-eq-ct (Nil [Int]) (Nil [Int])) "yes" "no"))
			((Cons a rest)
				(match rest
					((Nil) (if (bytes-eq-ct (str-bytes a) (Nil [Int])) "yes" "no"))
					((Cons b r2) (if (bytes-eq-ct (str-bytes a) (str-bytes b)) "yes" "no"))))))`)
	markVerified(t, st, "ct-eq")
	// THE ACTUAL USE: two digests over the same secret, compared. Fixed length on
	// both sides, so this is the case the constant-time argument is about — and
	// the one where a wrong digest and a wrong comparison are indistinguishable
	// from the outside, which is why the HMAC vectors are pinned separately.
	put(t, st, `(defn mac-eq [] [(args (List Str))] Str
		(match args
			((Nil) "none")
			((Cons secret rest)
				(match rest
					((Nil) "none")
					((Cons a r2)
						(match r2
							((Nil) "none")
							((Cons b r3)
								(if (bytes-eq-ct (hmac-sha256 (str-bytes secret) (str-bytes a))
								                 (hmac-sha256 (str-bytes secret) (str-bytes b)))
									"match" "mismatch"))))))))`)
	markVerified(t, st, "mac-eq")

	vectors := map[string][][]string{
		"ct-eq": {
			nil,            // empty against empty
			{""},           // the empty argument against the empty list
			{"a"},          // non-empty against empty — differing lengths
			{"abc", "abc"}, // equal
			{"abc", "abd"}, // differing in the LAST byte, same length
			{"abc", "bbc"}, // differing in the FIRST byte, same length
			{"abc", "ab"},  // a proper prefix — differing lengths, equal where they overlap
			{"ab", "abc"},  // the other direction
			{"", ""},       // both empty, explicitly
			{"", "a"},      // empty against non-empty
			{"ª", "ª"},     // codepoint 170: a byte above 0x7F, equal
			{"ª", "«"},     // and its neighbour
			{"ª", "a"},     // a high byte against a low one, same length
		},
		"mac-eq": {
			nil,
			{"s3cret", "payload", "payload"},  // the accepting path
			{"s3cret", "payload", "payloae"},  // one byte of the message
			{"s3cret", "payload", "payload "}, // a longer message
			{"s3cret", "", ""},                // both messages empty
			{"", "payload", "payload"},        // an empty key
		},
	}

	// EACH TABLE MUST DISCRIMINATE BEFORE IT IS RUN. `threeWay` asserts agreement
	// and nothing else, so a table whose every vector answers "no" agrees across
	// all three paths while never exercising the equal case — and looks exactly
	// like a thorough table.
	for _, entry := range []string{"ct-eq", "mac-eq"} {
		seen := map[string]int{}
		for _, args := range vectors[entry] {
			lit, ok := oathList(args)
			if !ok {
				t.Fatalf("%s: args %v cannot be written as an Oath literal", entry, args)
			}
			seen[evalDenotation(t, st, "("+entry+" "+lit+")")]++
		}
		if len(seen) < 2 {
			t.Errorf("%s: the vectors do not discriminate — interpreter answers were %v, "+
				"and threeWay only checks agreement, so one-sided vectors pass without the "+
				"comparison ever having to be right", entry, seen)
		}
		threeWay(t, st, entry, vectors[entry])
	}
}

// AN ELEMENT OUTSIDE 0..255 IS A RUNTIME REFUSAL IN BOTH BACKENDS, AND IN THE
// INTERPRETER IT IS AN ERROR (SPEC §1).
//
// The failure mode of being permissive here is accepting a forged payload: a
// digest computed over silently truncated input verifies against a message
// nobody sent. So neither backend may reduce modulo 256, and the interpreter is
// checked in the same table — it is the reference for this too, and its
// behaviour is where the ORDERING below was derived from rather than guessed.
//
// THE ORDER IS PART OF THE CONTRACT, and the `range-before-length` case is the
// one that pins it: its operands have DIFFERENT LENGTHS, so a lowering that
// compared lengths first would answer `false` where all three must refuse. That
// case is the reason both operands are converted in full before anything is
// compared.
func TestByteListRangeIsRefusedByBothBackends(t *testing.T) {
	requireClang(t)
	st := cryptoStore(t)
	// `(bytes-eq-ct (str-bytes h) (str-bytes h))` over an argument whose codepoint
	// is above 255. Every Unicode scalar outside Latin-1 is such a value, so the
	// trigger is one character of argv.
	put(t, st, `(defn ct-arg [] [(args (List Str))] Str
		(match args
			((Nil) "none")
			((Cons h t) (if (bytes-eq-ct (str-bytes h) (str-bytes h)) "same" "differ"))))`)
	markVerified(t, st, "ct-arg")
	put(t, st, `(defn mac-arg [] [(args (List Str))] Str
		(match args
			((Nil) "none")
			((Cons h t) (hex-of (hmac-sha256 (str-bytes h) (Nil [Int]))))))`)
	markVerified(t, st, "mac-arg")
	// A NEGATIVE element, and operands of DIFFERENT LENGTHS. The left is one
	// element long and the right is empty, so a length-first lowering answers
	// false; the reference refuses, because it converts both operands in full
	// first.
	put(t, st, `(defn ct-neg [] [(args (List Str))] Str
		(match args
			((Nil) "none")
			((Cons h t)
				(match h
					((SNil) "empty")
					((SCons c rest)
						(if (bytes-eq-ct (Cons [Int] (- 0 c) (Nil [Int])) (Nil [Int])) "same" "differ"))))))`)
	markVerified(t, st, "ct-neg")
	// AN ELEMENT TOO LARGE FOR A MACHINE WORD, not merely above 255. Int is ℤ in
	// this backend now (#166), so an element can be a multi-limb magnitude, and a
	// range check that read only the low limb would accept 2^64 as the byte 0.
	put(t, st, `(defn ct-huge [] [(args (List Str))] Str
		(match args
			((Nil) "none")
			((Cons h t)
				(match h
					((SNil) "empty")
					((SCons c rest)
						(if (bytes-eq-ct (Cons [Int] (* c 100000000000000000000) (Nil [Int]))
						                 (Cons [Int] 1 (Nil [Int]))) "same" "differ"))))))`)
	markVerified(t, st, "ct-huge")

	for _, tc := range []struct {
		entry            string
		trigger, control []string
	}{
		{"ct-arg", []string{"✓"}, []string{"ab"}},
		{"mac-arg", []string{"✓"}, []string{"ab"}},
		{"ct-neg", []string{"A"}, nil},
		{"ct-huge", []string{"A"}, nil},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			// THE INTERPRETER FIRST, because it is the reference: if it does not
			// refuse, the backends refusing would be the divergence.
			lit, ok := oathList(tc.trigger)
			if !ok {
				t.Fatalf("trigger %v cannot be written as an Oath literal", tc.trigger)
			}
			if out, err := apiEval(st, "("+tc.entry+" "+lit+")"); err == nil {
				t.Fatalf("the interpreter did not refuse the out-of-range element; it answered %q", out)
			} else if !strings.Contains(err.Error(), "out of range 0..255") {
				t.Fatalf("the interpreter refused for another reason: %v", err)
			}

			// THE CONTROL'S EXPECTATION IS THE INTERPRETER'S ANSWER, not a
			// constant written down here. A constant would be one more digest
			// this file asserts on its own authority; the reference already
			// knows, and using it makes the control a fourth three-way check
			// rather than a hardcoded string.
			clit, ok := oathList(tc.control)
			if !ok {
				t.Fatalf("control %v cannot be written as an Oath literal", tc.control)
			}
			wantOut := evalDenotation(t, st, "("+tc.entry+" "+clit+")")

			goBin, _ := buildProgram(t, st, tc.entry)
			llBin := buildLLVM(t, st, tc.entry)
			for _, b := range []struct{ label, bin string }{{"go", goBin}, {"llvm", llBin}} {
				// CONTROL FIRST. Without it a binary that refused everything —
				// or failed to start — would satisfy the trigger assertion.
				cmd := exec.Command(b.bin, tc.control...)
				var cout, cerr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &cout, &cerr
				if err := cmd.Run(); err != nil {
					t.Fatalf("%s control: the artifact failed on input that must succeed: %v — %s",
						b.label, err, cerr.String())
				}
				if got := trimFrame(cout.String()); got != wantOut {
					t.Fatalf("%s control: printed %q, want %q — the refusal below would be "+
						"evidence about a broken program rather than about the boundary",
						b.label, got, wantOut)
				}

				cmd = exec.Command(b.bin, tc.trigger...)
				var out, errb bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errb
				err := cmd.Run()
				if err == nil {
					t.Errorf("%s: the artifact did not refuse; it printed %q", b.label, out.String())
					continue
				}
				if code := cmd.ProcessState.ExitCode(); code != exitHostRefusal {
					t.Errorf("%s: exit %d, want %d", b.label, code, exitHostRefusal)
				}
				if !strings.Contains(errb.String(), "byte list element out of range 0..255") {
					t.Errorf("%s: refused without naming the condition: %q", b.label, errb.String())
				}
			}
		})
	}
}

// ---------- the lowering that was SELECTED ----------

// THE CALLS ARE IN THE IR, which distinguishes "the crypto path ran" from "some
// path ran". The three-way tests above would also pass if these primitives were
// somehow routed through another lowering that happened to be right — they would
// not be, but the assertion is cheap and it is the control for the guard that
// selects them.
func TestCryptoPrimitivesSelectTheirOwnLowering(t *testing.T) {
	st := cryptoStore(t)
	put(t, st, `(defn mac-then-eq [] [(args (List Str))] Str
		(match args
			((Nil) "none")
			((Cons h t)
				(if (bytes-eq-ct (hmac-sha256 (str-bytes h) (str-bytes h)) (str-bytes h))
					"same" "differ"))))`)
	markVerified(t, st, "mac-then-eq")
	prog, err := planProgram(st, "mac-then-eq")
	if err != nil {
		t.Fatal(err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("emitLLVM: %v", err)
	}
	// The constructor indices are the last two arguments of the HMAC call and
	// they are DERIVED, so they are asserted: this store's List is (Nil) then
	// (Cons a (List a)), which is 0 and 1. A backend that hardcoded them would
	// pass this and fail on a store that declares them the other way round —
	// which is why byteListCtors reads the declaration, and why the assertion
	// here is that the emitted call carries THIS store's answer rather than that
	// it carries any two integers.
	for _, want := range []string{
		"call ptr @o_hmac_sha256(ptr %", "i32 0, i32 1)",
		"call i32 @o_bytes_eq_ct(ptr %",
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("the emitted IR contains no %q, so the crypto lowering was not selected", want)
		}
	}
}

// ---------- what the constant-time ARGUMENT rests on ----------

// NEITHER OF THE TWO CHECKS BELOW MEASURES TIMING, AND NOTHING IN THIS REPO
// DOES. They pin the two things the argument at `o_bytes_eq_ct` rests on and
// that could change under it silently: the `volatile` accumulator that forbids a
// compiler from recognising the loop as memcmp, and the absence of a memcmp call
// in the function actually emitted. A green run here means the argument's
// premises still hold, not that the code was observed to be constant-time.
//
// NEITHER SUBSUMES THE OTHER, MEASURED RATHER THAN ASSUMED. Replacing the body
// with the obvious leaking loop — `if (a[i] != b[i]) return 0;` — is caught by
// the source check and NOT by the assembly one, because clang -O1 compiles that
// shape to an ordinary early-exit loop rather than to a bcmp call. The same
// mutation leaves every value test in this file green, which is the point.
func TestConstantTimeCompareKeepsItsVolatileAccumulator(t *testing.T) {
	body := cFunctionBody(t, llvmRuntimeC, "int o_bytes_eq_ct(")
	if !strings.Contains(body, "volatile unsigned char acc") {
		t.Error("o_bytes_eq_ct no longer accumulates into a volatile object. Without it a " +
			"compiler may prove the loop equivalent to memcmp and call it, and libc memcmp " +
			"stops at the first differing byte — which is exactly the timing oracle this " +
			"primitive exists to remove, and which no value test in this file can see")
	}
	// NO EARLY EXIT AFTER THE LOOP STARTS. The one permitted early return is the
	// length mismatch, which happens BEFORE any byte is read and is deliberate:
	// length is not secret. A `return` or a `break` inside the loop would be the
	// defect, so the tail of the function is checked rather than the whole of it.
	i := strings.Index(body, "for (int i = 0")
	if i < 0 {
		t.Fatal("o_bytes_eq_ct has no counted loop, so this scan is reading nothing")
	}
	if strings.Contains(body[i:], "break") || strings.Count(body[i:], "return") != 1 {
		t.Errorf("o_bytes_eq_ct leaves its comparison loop somewhere other than its end:\n%s", body[i:])
	}
	// THE SCAN'S OWN CONTROL. It must see the defect when the defect is there,
	// or its silence on the real runtime is not evidence.
	if b := cFunctionBody(t, strings.Replace(llvmRuntimeC,
		"volatile unsigned char acc = 0;", "unsigned char acc = 0;", 1), "int o_bytes_eq_ct("); strings.Contains(b, "volatile unsigned char acc") {
		t.Fatal("the scanner still reports a volatile accumulator after it was removed, so " +
			"it is not reading the function it names")
	}
}

// AND THE COMPILED FORM, which is the half that matters: `volatile` is the
// MECHANISM, and "no memcmp call in the emitted function" is the PROPERTY it was
// chosen to buy. Asserting only the source would pass on a toolchain that
// ignored the mechanism.
func TestConstantTimeCompareEmitsNoMemcmpCall(t *testing.T) {
	requireClang(t)
	asm := compileToAsm(t, llvmRuntimeC)
	fn := asmFunction(t, asm, "o_bytes_eq_ct")
	for _, bad := range []string{"memcmp", "bcmp"} {
		if strings.Contains(fn, bad) {
			t.Errorf("the compiled o_bytes_eq_ct calls %s, so the comparison exits at the "+
				"first differing byte and its timing carries the position of that byte:\n%s", bad, fn)
		}
	}

	// THE CONTROL, AND IT IS THE WHOLE REASON THE ASSERTION ABOVE IS EVIDENCE. A
	// scan for an absent string passes on an empty slice, a misspelled symbol
	// name, or a compiler that inlined the function away. So the defect is
	// INJECTED — the loop is replaced by the memcmp a careless rewrite would
	// reach for — and the same scan must find it.
	leaky := strings.Replace(llvmRuntimeC,
		`  volatile unsigned char acc = 0;
  for (int i = 0; i < na; i++) acc = (unsigned char)(acc | (unsigned char)(a[i] ^ b[i]));
  o_u32 d = (o_u32)acc;
  return (int)(1u & ((d - 1u) >> 8));`,
		`  return memcmp(a, b, (size_t)na) == 0;`, 1)
	if leaky == llvmRuntimeC {
		t.Fatal("the injected control did not change the runtime source, so the scan below " +
			"would be testing the same function twice and proving nothing")
	}
	if got := asmFunction(t, compileToAsm(t, leaky), "o_bytes_eq_ct"); !strings.Contains(got, "memcmp") && !strings.Contains(got, "bcmp") {
		t.Fatalf("a memcmp lowering compiles to a function this scan reads as clean, so the "+
			"assertion above cannot fail and is not evidence:\n%s", got)
	}
}

// cFunctionBody slices one function out of the runtime source by its signature.
// Narrow on purpose: it reads the ONE function it names and fails if it cannot
// find it, rather than searching the file for a reassuring substring.
func cFunctionBody(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("the runtime does not define %q, so this test is reading nothing", sig)
	}
	body := src[i:]
	if j := strings.Index(body, "\n}"); j > 0 {
		body = body[:j]
	}
	return body
}

// compileToAsm compiles the runtime to assembly at the SAME optimisation level
// llvmBuild uses. A different -O would answer a question about a build nobody
// ships.
func compileToAsm(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "rt.s")
	cc := exec.Command("clang", "-O1", "-S", "-o", out, "rt.c")
	cc.Dir = dir
	if b, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("the runtime did not compile to assembly: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// asmFunction slices one function out of an assembly listing.
//
// The label is `_name:` under Mach-O and `name:` under ELF, and the end is
// `.cfi_endproc` under both. AN UNRECOGNISED LISTING IS A FAILURE, not an empty
// slice: a scan for an absent call trivially passes on nothing, which is the
// shape this repo keeps finding — a check that cannot tell "the defect is
// absent" from "I read the wrong thing".
func asmFunction(t *testing.T, asm, name string) string {
	t.Helper()
	for _, label := range []string{"\n_" + name + ":", "\n" + name + ":"} {
		i := strings.Index(asm, label)
		if i < 0 {
			continue
		}
		body := asm[i+1:]
		j := strings.Index(body, ".cfi_endproc")
		if j < 0 {
			t.Fatalf("%s has no .cfi_endproc, so this scan cannot tell where the function "+
				"ends and would be reading the rest of the listing", name)
		}
		return body[:j]
	}
	t.Fatalf("the assembly listing has no label for %s, so this scan is reading nothing. "+
		"An absent symbol is indistinguishable from an absent call, which is the whole "+
		"defect this control exists to prevent", name)
	return ""
}

// A CRYPTO OPERAND'S TAIL IS CHECKED AS A WHOLE TYPE, NOT BY DATATYPE HASH.
//
// byteListCtors admits whatever datatype this store binds to List, and decides
// by SHAPE rather than by name — one nullary constructor and one binary
// constructor carrying (Int, the list itself). "The list itself" is the part a
// hash comparison gets wrong: `(data List [a] (Nil) (Cons a (List Bool)))` is a
// legal declaration whose cons tail has the SAME datatype hash and DIFFERENT
// arguments, so at (List Int) it describes a value whose head is an octet and
// whose tail carries Bools.
//
// Such a value is well typed. `(Cons 1 (Cons true (Nil)))` checks at (List Int)
// under that declaration, and a hash-only test lets it through emission to be
// discovered by o_bug inside the emitted program — the one disposition this
// backend does not permit, since unsupported shapes are refused BY NAME at
// compile time.
//
// THE CONTROL IS THE POINT. The ordinary declaration must still be ACCEPTED by
// the same call, or this test would pass just as well against a function that
// refused everything.
func TestCryptoOperandTailMustMatchTheWholeType(t *testing.T) {
	byteListOf := func(t *testing.T, decl string) error {
		t.Helper()
		st := newStore(t)
		put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
		put(t, st, decl)
		h, ok := st.Resolve("List")
		if !ok {
			t.Fatalf("the store did not bind List, so this test never reached its subject")
		}
		e := &llvmEmitter{st: st, fname: map[string]string{}}
		_, _, err := e.byteListCtors(&Ty{K: "data", Hash: h, Args: []Ty{{K: "int"}}})
		return err
	}

	if err := byteListOf(t, `(data List [a] (Nil) (Cons a (List a)))`); err != nil {
		t.Fatalf("the ordinary List declaration was refused as a byte list, so the "+
			"check below cannot be read as discriminating: %v", err)
	}

	err := byteListOf(t, `(data List [a] (Nil) (Cons a (List Bool)))`)
	if err == nil {
		t.Fatal("a List whose cons tail is (List Bool) was accepted as a byte list. " +
			"Its head is an Int and its tail carries Bools, so the emitted runtime " +
			"would read a Bool as an octet and abort in o_bug — a shape this backend " +
			"must refuse by name at compile time, not discover at run time")
	}
	if !strings.Contains(err.Error(), "not (Int, itself)") {
		t.Errorf("the refusal does not name the shape that was wrong: %v", err)
	}
}
