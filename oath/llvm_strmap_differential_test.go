package main

import "testing"

// #184: the Str-keyed map must agree across eval / Go / LLVM. The Go backend
// lowers it to a NATIVE string-keyed map; the LLVM backend has no str-map helper
// yet, so it runs the STRUCTURAL assoc-list definitions; the interpreter runs the
// same structural model as its reference. All three must produce byte-identical
// output. The keys are taken from the program's ARGUMENTS, so the compiler cannot
// fold the map at build time — the native map is exercised at run time, and a
// str-lt/o_str_cmp ordering disagreement or a lost/duplicated key would show as a
// three-way mismatch rather than a silent one.
func TestStrMapThreeWayDifferential(t *testing.T) {
	requireClang(t)
	st := newStore(t)
	strMapPrelude(t, st) // Str, List, Option, Pair, length, the Int-keyed family
	strMapModel(t, st)   // str-lt, str-eq, StrMap, smi-*, str-map-*

	// str-append / str-join render a (List Str) so the outputs are pure Str.
	put(t, st, `(defn str-append [] [(a Str) (b Str)] Str
		(match a ((SNil) b) ((SCons c r) (SCons c (str-append r b)))))`)
	put(t, st, `(defn str-join [] [(d Int) (xs (List Str))] Str
		(match xs
			((Nil) (SNil))
			((Cons h t) (match t
				((Nil) h)
				((Cons h2 t2) (str-append h (SCons d (str-join d t))))))))`)

	// Fold the argument list into a Str-keyed map (value is irrelevant to these
	// witnesses; the KEY is what the native map orders and dedups).
	put(t, st, `(defn sm-build [] [(xs (List Str)) (m StrMap)] StrMap
		(match xs ((Nil) m) ((Cons h t) (sm-build t (str-map-insert h 1 m)))))`)

	// The str-lt-sorted, de-duplicated keys, comma-joined — witnesses insert,
	// dedup, keys, and the ORDER (the whole reason the native comparison must
	// match str-lt).
	put(t, st, `(defn sm-keys [] [(args (List Str))] Str
		(str-join 44 (str-map-keys (sm-build args str-map-empty))))`)
	markVerified(t, st, "sm-keys")

	// Membership of the first argument in the map built from the rest.
	put(t, st, `(defn sm-has [] [(args (List Str))] Str
		(match args
			((Nil) "e")
			((Cons k rest) (if (str-map-has k (sm-build rest str-map-empty)) "y" "n"))))`)
	markVerified(t, st, "sm-has")

	// Scrambled, duplicate-heavy, order-adversarial key sets — including keys that
	// share prefixes ("a"/"ab"/"abc") where a shorter-is-less comparison and a
	// codepoint comparison must agree, and mixed-length/interleaved insert orders.
	threeWay(t, st, "sm-keys", [][]string{
		{},
		{"alice"},
		{"carol", "alice", "bob", "alice"},
		{"b", "a", "c", "a", "b"},
		{"abc", "ab", "a", "abc", "abcd"},
		{"zzz", "aaa", "mmm", "aaa", "zzz", "mmm"},
		{"delta", "alpha", "gamma", "beta", "alpha", "delta"},
	})
	threeWay(t, st, "sm-has", [][]string{
		{"a", "b", "a", "c"},        // present
		{"z", "a", "b", "c"},        // absent
		{"ab", "a", "abc"},          // prefix-adjacent, absent (ab not in {a,abc})
		{"only"},                    // present-in-empty-rest -> absent
	})
}
