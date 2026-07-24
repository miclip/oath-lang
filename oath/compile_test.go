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

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
