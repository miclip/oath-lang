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

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
