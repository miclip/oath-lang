package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLLVMNativeContainersAgree is the three-way differential (#178) for the
// LLVM backend's native Set/Map: the compiled artifact must produce the same
// answer as the Go backend, and both must match the structural answer `oath
// eval` gives (pinned as the expected literal — the Go backend's agreement with
// eval is separately established by TestCompileNative{Set,Map}Differential, so
// this adds the LLVM leg). It exercises every recognized operation: for Set,
// empty/add/member/union/inter/size/elems and a match; for Map, empty/insert/
// lookup/has/keys/values/size/merge and a match. Before this the LLVM backend
// REFUSED nothing and emitted the structural list walk, which overflowed the
// stack on a few thousand distinct elements (the reason #178 exists).
func TestLLVMNativeContainersAgree(t *testing.T) {
	requireClang(t)
	st := newStore(t)
	// Prelude.
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Option [a] (None) (Some a))`)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(defn length [a] [(xs (List a))] Int
		(match xs ((Nil) 0) ((Cons h t) (+ 1 (length [a] t)))))`)
	put(t, st, `(defn head-or [] [(d Int) (xs (List Int))] Int
		(match xs ((Nil) d) ((Cons h t) h)))`)

	// Set stdlib (the si-* recursion is total over List subterms; set-* are the
	// thin newtype wrappers the backend recognizes).
	put(t, st, `(data Set [] (MkSet (List Int)))`)
	put(t, st, `(defn si-member [] [(x Int) (xs (List Int))] Bool
		(match xs ((Nil) false)
			((Cons h t) (if (== x h) true (if (< x h) false (si-member x t))))))`)
	put(t, st, `(defn si-insert [] [(x Int) (xs (List Int))] (List Int)
		(match xs ((Nil) (Cons [Int] x (Nil [Int])))
			((Cons h t) (if (< x h) (Cons [Int] x xs)
				(if (== x h) xs (Cons [Int] h (si-insert x t)))))))`)
	put(t, st, `(defn si-union [] [(xs (List Int)) (ys (List Int))] (List Int)
		(match xs ((Nil) ys) ((Cons h t) (si-insert h (si-union t ys)))))`)
	put(t, st, `(defn si-inter [] [(xs (List Int)) (ys (List Int))] (List Int)
		(match xs ((Nil) (Nil [Int]))
			((Cons h t) (if (si-member h ys) (Cons [Int] h (si-inter t ys)) (si-inter t ys)))))`)
	put(t, st, `(defn set-empty [] [] Set (MkSet (Nil [Int])))`)
	put(t, st, `(defn set-member [] [(x Int) (s Set)] Bool (match s ((MkSet xs) (si-member x xs))))`)
	put(t, st, `(defn set-add [] [(x Int) (s Set)] Set (match s ((MkSet xs) (MkSet (si-insert x xs)))))`)
	put(t, st, `(defn set-union [] [(a Set) (b Set)] Set
		(match a ((MkSet xs) (match b ((MkSet ys) (MkSet (si-union xs ys)))))))`)
	put(t, st, `(defn set-inter [] [(a Set) (b Set)] Set
		(match a ((MkSet xs) (match b ((MkSet ys) (MkSet (si-inter xs ys)))))))`)
	put(t, st, `(defn set-size [] [(s Set)] Int (match s ((MkSet xs) (length [Int] xs))))`)
	put(t, st, `(defn set-elems [] [(s Set)] (List Int) (match s ((MkSet xs) xs)))`)

	// Map stdlib.
	put(t, st, `(data Map [] (MkMap (List (Pair Int Int))))`)
	put(t, st, `(defn mi-lookup [] [(k Int) (m (List (Pair Int Int)))] (Option Int)
		(match m ((Nil) (None [Int]))
			((Cons p t) (match p ((Pair pk pv)
				(if (== k pk) (Some [Int] pv) (if (< k pk) (None [Int]) (mi-lookup k t))))))))`)
	put(t, st, `(defn mi-insert [] [(k Int) (v Int) (m (List (Pair Int Int)))] (List (Pair Int Int))
		(match m ((Nil) (Cons [(Pair Int Int)] (Pair [Int Int] k v) (Nil [(Pair Int Int)])))
			((Cons p t) (match p ((Pair pk pv)
				(if (< k pk) (Cons [(Pair Int Int)] (Pair [Int Int] k v) m)
					(if (== k pk) (Cons [(Pair Int Int)] (Pair [Int Int] k v) t)
						(Cons [(Pair Int Int)] p (mi-insert k v t)))))))))`)
	put(t, st, `(defn mi-keys [] [(m (List (Pair Int Int)))] (List Int)
		(match m ((Nil) (Nil [Int])) ((Cons p t) (match p ((Pair pk pv) (Cons [Int] pk (mi-keys t)))))))`)
	put(t, st, `(defn mi-values [] [(m (List (Pair Int Int)))] (List Int)
		(match m ((Nil) (Nil [Int])) ((Cons p t) (match p ((Pair pk pv) (Cons [Int] pv (mi-values t)))))))`)
	put(t, st, `(defn mi-merge [] [(xs (List (Pair Int Int))) (ys (List (Pair Int Int)))] (List (Pair Int Int))
		(match xs ((Nil) ys) ((Cons p t) (match p ((Pair k v) (mi-insert k v (mi-merge t ys)))))))`)
	put(t, st, `(defn map-empty [] [] Map (MkMap (Nil [(Pair Int Int)])))`)
	put(t, st, `(defn map-insert [] [(k Int) (v Int) (m Map)] Map (match m ((MkMap ps) (MkMap (mi-insert k v ps)))))`)
	put(t, st, `(defn map-lookup [] [(k Int) (m Map)] (Option Int) (match m ((MkMap ps) (mi-lookup k ps))))`)
	put(t, st, `(defn map-has [] [(k Int) (m Map)] Bool
		(match m ((MkMap ps) (match (mi-lookup k ps) ((None) false) ((Some v) true)))))`)
	put(t, st, `(defn map-keys [] [(m Map)] (List Int) (match m ((MkMap ps) (mi-keys ps))))`)
	put(t, st, `(defn map-values [] [(m Map)] (List Int) (match m ((MkMap ps) (mi-values ps))))`)
	put(t, st, `(defn map-size [] [(m Map)] Int (match m ((MkMap ps) (length [(Pair Int Int)] ps))))`)
	put(t, st, `(defn map-merge [] [(a Map) (b Map)] Map
		(match a ((MkMap xs) (match b ((MkMap ys) (MkMap (mi-merge xs ys)))))))`)

	// Entries. set-inter {1,2,3}∩{2,3,4}={2,3}, min 2. Built out of order with dups.
	put(t, st, `(defn e-set [] [(args (List Str))] Str
		(if (set-member 2 (set-add 3 (set-add 1 (set-add 2 (set-add 1 set-empty)))))
			(if (== (set-size (set-union (set-add 1 (set-add 3 set-empty)) (set-add 2 (set-add 3 set-empty)))) 3)
				(if (== (head-or 0 (set-elems (set-inter (set-add 1 (set-add 2 (set-add 3 set-empty))) (set-add 2 (set-add 3 (set-add 4 set-empty)))))) 2)
					"set-ok" "inter-wrong")
				"size-wrong")
			"member-wrong"))`)
	// map-insert 2->20 overwrites an earlier 2->99; merge is LEFT-biased so a's
	// 1->100 wins over b's 1->999, size 2; keys sorted so smallest is 1.
	put(t, st, `(defn e-map [] [(args (List Str))] Str
		(match (map-lookup 2 (map-insert 2 20 (map-insert 1 10 (map-insert 2 99 map-empty))))
			((None) "lookup-miss")
			((Some v)
				(if (== v 20)
					(if (map-has 1 (map-insert 1 10 map-empty))
						(if (== (map-size (map-merge (map-insert 1 100 map-empty) (map-insert 1 999 (map-insert 2 2 map-empty)))) 2)
							(if (== (head-or 0 (map-keys (map-insert 3 3 (map-insert 1 1 map-empty)))) 1)
								(if (== (head-or 0 (map-values (map-insert 1 100 (map-insert 2 200 map-empty)))) 100)
									"map-ok" "values-wrong")
								"keys-wrong")
							"merge-size-wrong")
						"has-wrong")
					"lookup-val-wrong"))))`)

	for _, tc := range []struct{ entry, want string }{
		{"e-set", "set-ok"},
		{"e-map", "map-ok"},
	} {
		markVerified(t, st, tc.entry)
		goBin, _ := buildProgram(t, st, tc.entry)
		llBin := buildLLVM(t, st, tc.entry)
		goOut, err := exec.Command(goBin).Output()
		if err != nil {
			t.Fatalf("%s go run: %v", tc.entry, err)
		}
		llOut, err := exec.Command(llBin).Output()
		if err != nil {
			t.Fatalf("%s llvm run: %v", tc.entry, err)
		}
		g := strings.TrimRight(string(goOut), "\n")
		l := strings.TrimRight(string(llOut), "\n")
		if g != l {
			t.Errorf("%s: backends disagree: go=%q llvm=%q", tc.entry, g, l)
		}
		if l != tc.want {
			t.Errorf("%s: llvm=%q, want %q (native container diverged from the structural model)", tc.entry, l, tc.want)
		}
	}
}

// TestLLVMContainerOpPartialApplication witnesses that an operation used as a
// FIRST-CLASS value still compiles: prunableOps leaves set-add structural
// because it is applied partially, so defValue finds it, while its saturated
// call sites still lower natively. The structural set-add produces the same
// native Set (its MkSet/match are intercepted), so the two paths agree — pinned
// three-way against the Go backend and the expected answer.
func TestLLVMContainerOpPartialApplication(t *testing.T) {
	requireClang(t)
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Set [] (MkSet (List Int)))`)
	put(t, st, `(defn si-member [] [(x Int) (xs (List Int))] Bool
		(match xs ((Nil) false)
			((Cons h t) (if (== x h) true (if (< x h) false (si-member x t))))))`)
	put(t, st, `(defn si-insert [] [(x Int) (xs (List Int))] (List Int)
		(match xs ((Nil) (Cons [Int] x (Nil [Int])))
			((Cons h t) (if (< x h) (Cons [Int] x xs)
				(if (== x h) xs (Cons [Int] h (si-insert x t)))))))`)
	put(t, st, `(defn set-empty [] [] Set (MkSet (Nil [Int])))`)
	put(t, st, `(defn set-member [] [(x Int) (s Set)] Bool (match s ((MkSet xs) (si-member x xs))))`)
	put(t, st, `(defn set-add [] [(x Int) (s Set)] Set (match s ((MkSet xs) (MkSet (si-insert x xs)))))`)
	// set-add is passed as a first-class value, so it is NOT saturated at this
	// reference and must stay structural; set-member below is a saturated call and
	// lowers natively.
	put(t, st, `(defn apply-to [] [(f (-> Set Set)) (s Set)] Set (f s))`)
	put(t, st, `(defn e-partial [] [(args (List Str))] Str
		(if (set-member 5 (apply-to (set-add 5) set-empty)) "partial-ok" "partial-wrong"))`)

	markVerified(t, st, "e-partial")
	goBin, _ := buildProgram(t, st, "e-partial")
	llBin := buildLLVM(t, st, "e-partial")
	goOut, err := exec.Command(goBin).Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}
	llOut, err := exec.Command(llBin).Output()
	if err != nil {
		t.Fatalf("llvm run (partial application regressed to an unemitted reference?): %v", err)
	}
	g := strings.TrimRight(string(goOut), "\n")
	l := strings.TrimRight(string(llOut), "\n")
	if g != l {
		t.Errorf("backends disagree on partial application: go=%q llvm=%q", g, l)
	}
	if l != "partial-ok" {
		t.Errorf("partial application = %q, want %q", l, "partial-ok")
	}
}

// TestLLVMInactiveContainerDatatype witnesses that a store defining Set WITHOUT
// any recognized operation compiles the datatype structurally rather than
// erroring: no operation is recognized, so native lowering is inactive and the
// MkSet constructor and its match take the ordinary build path. Before the
// interception was gated on native lowering being active it rejected this valid
// program with a missing-Cons error.
func TestLLVMInactiveContainerDatatype(t *testing.T) {
	requireClang(t)
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Set [] (MkSet (List Int)))`)
	// No set-* operations exist, so nothing is recognized: MkSet and the match on
	// it must compile structurally.
	put(t, st, `(defn e-direct [] [(args (List Str))] Str
		(match (MkSet (Cons [Int] 7 (Nil [Int])))
			((MkSet xs) (match xs ((Nil) "empty") ((Cons h t) "has")))))`)
	markVerified(t, st, "e-direct")
	llBin := buildLLVM(t, st, "e-direct")
	out, err := exec.Command(llBin).Output()
	if err != nil {
		t.Fatalf("llvm run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "has" {
		t.Errorf("inactive-container program = %q, want %q", got, "has")
	}
}

// TestNativeContainerHashesAreCanonical witnesses that container recognition
// derives the datatype hash from the recognized operations' CANONICAL types, not
// from the mutable name "Set". After the name is repointed to a structurally
// different datatype, resolveNativeContainers must still bind SetHash to the
// datatype the existing operations were defined against — otherwise a backend
// would lower a call natively while treating that operation's own MkSet as an
// ordinary constructor (the #178 review's P1). This is the identity discipline:
// derive representation from the hash, never from a name.
func TestNativeContainerHashesAreCanonical(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Set [] (MkSet (List Int)))`)
	origSet, ok := st.Resolve("Set")
	if !ok {
		t.Fatal("Set did not resolve")
	}
	put(t, st, `(defn si-insert [] [(x Int) (xs (List Int))] (List Int)
		(match xs ((Nil) (Cons [Int] x (Nil [Int])))
			((Cons h t) (if (< x h) (Cons [Int] x xs)
				(if (== x h) xs (Cons [Int] h (si-insert x t)))))))`)
	put(t, st, `(defn set-empty [] [] Set (MkSet (Nil [Int])))`)
	put(t, st, `(defn set-add [] [(x Int) (s Set)] Set (match s ((MkSet xs) (MkSet (si-insert x xs)))))`)

	// Repoint the NAME "Set" to a structurally different datatype (an extra
	// constructor → a different hash). The existing set-* ops still reference the
	// ORIGINAL Set by hash; only the name moved.
	put(t, st, `(data Set [] (MkSet (List Int)) (Tombstone))`)
	drifted, _ := st.Resolve("Set")
	if drifted == origSet {
		t.Fatal("repointing Set did not change its hash; the test cannot discriminate")
	}

	nc := resolveNativeContainers(st, llvmSetHelperNames())
	if nc.SetHash != origSet {
		t.Errorf("SetHash = %s (the repointed name), want %s (the ops' canonical Set) — "+
			"recognition is resolving representation from a mutable name", nc.SetHash, origSet)
	}
}

// TestNativeContainerVocabularyIsValidated witnesses that recognition matches an
// operation's canonical SIGNATURE, not merely its name and arity: a store may
// define an unrelated `set-member : Int -> Int -> Bool`, and lowering its call
// to the native helper would misread the second Int as a Set. The vocabulary is
// admitted fail-closed per family, so a single non-conforming member drops the
// whole family to the structural path.
func TestNativeContainerVocabularyIsValidated(t *testing.T) {
	// An unrelated function under a recognized name must NOT be recognized.
	bad := newStore(t)
	put(t, bad, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, bad, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, bad, `(data Set [] (MkSet (List Int)))`)
	put(t, bad, `(defn set-member [] [(x Int) (y Int)] Bool (== x y))`) // Int->Int->Bool, not Int->Set->Bool
	if nc := resolveNativeContainers(bad, nativeOpNames()); len(nc.Ops) != 0 {
		t.Errorf("an unrelated set-member (Int->Int->Bool) was recognized as the native operation: %v", nc.Ops)
	}

	// Control: the CANONICAL set-member is recognized — so the check above is
	// rejecting on the signature, not failing to recognize anything at all.
	good := newStore(t)
	put(t, good, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, good, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, good, `(data Set [] (MkSet (List Int)))`)
	put(t, good, `(defn si-member [] [(x Int) (xs (List Int))] Bool
		(match xs ((Nil) false)
			((Cons h t) (if (== x h) true (if (< x h) false (si-member x t))))))`)
	put(t, good, `(defn set-member [] [(x Int) (s Set)] Bool (match s ((MkSet xs) (si-member x xs))))`)
	nc := resolveNativeContainers(good, nativeOpNames())
	found := false
	for _, op := range nc.Ops {
		if op.Name == "set-member" {
			found = true
		}
	}
	if !found {
		t.Error("the canonical set-member (Int->Set->Bool) was NOT recognized; the validator is over-rejecting")
	}
	if nc.SetHash == "" {
		t.Error("SetHash was not derived from the canonical set-member")
	}
}

// TestNativeContainerRejectsNonCanonicalDatatype witnesses that recognition
// checks constructor FIELD TYPES, not just names and arities: a List whose Cons
// head is Bool rather than the element type would make the runtime place Int
// values into a Bool binder. Such a family must drop to the structural path.
func TestNativeContainerRejectsNonCanonicalDatatype(t *testing.T) {
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	// A List whose Cons carries a Bool head, not the element `a`.
	put(t, st, `(data List [a] (Nil) (Cons Bool (List a)))`)
	put(t, st, `(data Set [] (MkSet (List Int)))`)
	put(t, st, `(defn set-empty [] [] Set (MkSet (Nil [Int])))`)
	put(t, st, `(defn set-elems [] [(s Set)] (List Int) (match s ((MkSet xs) xs)))`)
	if nc := resolveNativeContainers(st, nativeOpNames()); len(nc.Ops) != 0 || nc.SetHash != "" {
		t.Errorf("a Set over a non-canonical List (Cons Bool ...) was recognized natively: Ops=%v SetHash=%q", nc.Ops, nc.SetHash)
	}
}
