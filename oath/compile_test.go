package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The compiler's honesty rests on the differential gate: a compiled program
// must produce the same output as `oath eval`. This exercises it end to end for
// the native-string representation (#13) — Str values compile to Go strings, so
// SCons/SNil construction and `match` on Str must go through native string ops
// and still agree with the structural interpreter.
func TestCompileNativeStrDifferential(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	// str-append: matches on Str (SNil/SCons) and constructs SCons — the exact
	// paths the native representation special-cases.
	put(t, st, `(defn str-append [] [(a Str) (b Str)] Str
		(match a ((SNil) b) ((SCons c t) (SCons c (str-append t b)))))`)
	// entry: (List Str) -> Str, using a string literal, a List match, and Str ops.
	put(t, st, `(defn greet2 [] [(args (List Str))] Str
		(str-append "hi " (match args ((Nil) "world") ((Cons h t) h))))`)

	h, ok := st.Resolve("greet2")
	if !ok {
		t.Fatal("greet2 not in store")
	}
	src, err := emitProgram(st, h, nil)
	if err != nil {
		t.Fatalf("emitProgram: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := runIn(dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("go build failed:\n%s", out)
	}

	// One name arg → "hi bob"; no args → the "world" default. Both compare
	// against the answer the interpreter gives for the same input.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"bob"}, "hi bob"},
		{nil, "hi world"},
	} {
		out, err := exec.Command(bin, tc.args...).Output()
		if err != nil {
			t.Fatalf("run %v: %v", tc.args, err)
		}
		got := strings.TrimRight(string(out), "\n")
		if got != tc.want {
			t.Fatalf("compiled greet2 %v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// Rat (#36) compiles to *big.Rat and arithmetic is exact — the same guarantee
// the interpreter and prover give. Rat can't be an entry type ((List Str)->Str),
// so this drives rat codegen through a Str-returning entry that computes with
// rationals internally: 0.1 + 0.2 == 3/10 EXACTLY (the float trap the structural
// model avoids). A wrong representation (asserting *big.Int on a *big.Rat) would
// panic; a lossy one would print "inexact". Both differ from what `oath eval`
// gives, which is the gate.
func TestCompileRatExactDifferential(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	// exact?: ignores args, returns "exact" iff 0.1 + 0.2 is exactly 3/10.
	put(t, st, `(defn exactp [] [(args (List Str))] Str
		(if (== (+ 0.1 0.2) 3/10) "exact" "inexact"))`)

	h, ok := st.Resolve("exactp")
	if !ok {
		t.Fatal("exactp not in store")
	}
	src, err := emitProgram(st, h, nil)
	if err != nil {
		t.Fatalf("emitProgram: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := runIn(dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("go build failed:\n%s", out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "exact" {
		t.Fatalf("compiled exactp = %q, want %q (rationals are not exact under compilation)", got, "exact")
	}
}

// Float (#36) compiles to native float64 with IEEE semantics — the mirror of
// the Rat test, and the point of the whole exhibit: the SAME expression that is
// "exact" for Rat is "inexact" for Float, because 0.1f + 0.2f is
// 0.30000000000000004, not 0.3f. If float codegen were wrong (e.g. asserting
// *big.Rat on a float64, or losing the canonical-NaN/bit-== semantics) this
// would panic or print "exact"; either differs from what `oath eval` gives.
func TestCompileFloatInexactDifferential(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	// Same shape as exactp, but over Float: structural == on the float result.
	put(t, st, `(defn finexactp [] [(args (List Str))] Str
		(if (== (+ 0.1f 0.2f) 0.3f) "exact" "inexact"))`)

	h, ok := st.Resolve("finexactp")
	if !ok {
		t.Fatal("finexactp not in store")
	}
	src, err := emitProgram(st, h, nil)
	if err != nil {
		t.Fatalf("emitProgram: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := runIn(dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("go build failed:\n%s", out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "inexact" {
		t.Fatalf("compiled finexactp = %q, want %q (float 0.1+0.2 must not equal 0.3)", got, "inexact")
	}
}

// Native Set (containers): a Set compiles to a native Go map (oset), with the
// recognized set-* operations lowered to O(1) map ops, and MkSet/match-Set as
// List boundaries. The differential gate is the whole point — the compiled
// oset-backed program must agree with the interpreter's sorted-list model. Here
// the entry builds a set out of order with a duplicate, then tests membership;
// a correct native representation must dedup, and answer yes/no exactly as the
// structural model does.
func TestCompileNativeSetDifferential(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	put(t, st, `(data Set [] (MkSet (List Int)))`)
	put(t, st, `(defn length [a] [(xs (List a))] Int
		(match xs ((Nil) 0) ((Cons h t) (+ 1 (length [a] t)))))`)
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
	put(t, st, `(defn si-union [] [(xs (List Int)) (ys (List Int))] (List Int)
		(match xs ((Nil) ys) ((Cons h t) (si-insert h (si-union t ys)))))`)
	put(t, st, `(defn set-union [] [(a Set) (b Set)] Set
		(match a ((MkSet xs) (match b ((MkSet ys) (MkSet (si-union xs ys)))))))`)
	put(t, st, `(defn set-size [] [(s Set)] Int (match s ((MkSet xs) (length [Int] xs))))`)
	put(t, st, `(defn set-elems [] [(s Set)] (List Int) (match s ((MkSet xs) xs)))`)
	// membership on a set built out of order with a duplicate: 2 is present, 5 is not.
	put(t, st, `(defn setq [] [(args (List Str))] Str
		(if (set-member 2 (set-add 3 (set-add 1 (set-add 2 (set-add 1 set-empty)))))
			(if (set-member 5 (set-add 3 (set-add 1 set-empty))) "both" "first-only")
			"neither"))`)
	// union {1,3} ∪ {2,3} has size 3 (dedup); and its smallest element (via the
	// set-elems boundary, which must sort) is 1. Both exercise osetElems.
	put(t, st, `(defn head-or [] [(d Int) (xs (List Int))] Int
		(match xs ((Nil) d) ((Cons h t) h)))`)
	put(t, st, `(defn setq2 [] [(args (List Str))] Str
		(if (== (set-size (set-union (set-add 3 (set-add 1 set-empty)) (set-add 3 (set-add 2 set-empty)))) 3)
			(if (== (head-or 0 (set-elems (set-union (set-add 3 (set-add 1 set-empty)) (set-add 2 set-empty)))) 1)
				"size3-min1" "min-wrong")
			"size-wrong"))`)

	h, ok := st.Resolve("setq")
	if !ok {
		t.Fatal("setq not in store")
	}
	src, err := emitProgram(st, h, nil)
	if err != nil {
		t.Fatalf("emitProgram: %v", err)
	}
	if !strings.Contains(src, "osetAdd(") || !strings.Contains(src, "osetMember(") {
		t.Fatalf("expected native oset ops in generated source, got none")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if out, err := runIn(dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("go build failed:\n%s", out)
	}
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// The interpreter's sorted-list model gives "first-only"; the native oset
	// must agree (2 present after dedup, 5 absent).
	if got := strings.TrimRight(string(out), "\n"); got != "first-only" {
		t.Fatalf("compiled setq = %q, want %q (native Set diverged from the structural model)", got, "first-only")
	}

	// Second entry: union dedup (size 3) and the sorted set-elems boundary (min 1).
	h2, _ := st.Resolve("setq2")
	src2, err := emitProgram(st, h2, nil)
	if err != nil {
		t.Fatalf("emitProgram setq2: %v", err)
	}
	if !strings.Contains(src2, "osetUnion(") || !strings.Contains(src2, "osetElems(") {
		t.Fatalf("expected osetUnion/osetElems in generated source")
	}
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "main.go"), []byte(src2), 0o644)
	os.WriteFile(filepath.Join(dir2, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644)
	bin2 := filepath.Join(dir2, "prog")
	if out, err := runIn(dir2, "go", "build", "-o", bin2, "."); err != nil {
		t.Fatalf("go build setq2 failed:\n%s", out)
	}
	out2, err := exec.Command(bin2).Output()
	if err != nil {
		t.Fatalf("run setq2: %v", err)
	}
	if got := strings.TrimRight(string(out2), "\n"); got != "size3-min1" {
		t.Fatalf("compiled setq2 = %q, want %q (union/size/elems diverged)", got, "size3-min1")
	}
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
