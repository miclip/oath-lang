package main

import (
	"bytes"
	"os/exec"
	"testing"
)

// The exit-result protocol (#120 second-app friction): a compiled program's CLI
// entry may return (Ok Str) or (Fail Int Str) instead of Str, so a program that
// can COMPUTE a refusal can REPORT it to its caller — Ok prints to stdout and
// exits 0, Fail prints its message to stderr and exits its (clamped) code. This
// is the one real gap docs/experiments/second-app-friction.md names, and the fix
// is a new entry shape lowered on BOTH backends. The test pins the whole
// disposition — stdout, stderr, and exit code — and requires the two backends to
// agree, which is the differential gate that makes "both lower the same
// semantics" a checked claim rather than a hope.
//
// Constructor ORDER is deliberately (Ok Str) then (Fail Int Str) here; a
// companion case flips it to prove recognition is order-independent (the arms
// differ in shape, so which is declared first does not change their meaning).

func exitResultStore(t *testing.T, okFirst bool) *Store {
	t.Helper()
	st := llvmStore(t)
	if okFirst {
		put(t, st, `(data Res [] (Ok Str) (Fail Int Str))`)
	} else {
		put(t, st, `(data Res [] (Fail Int Str) (Ok Str))`)
	}
	// No args -> success on stdout, exit 0. Any arg -> a program-chosen refusal
	// on stderr, exit 3. The refusal is COMPUTED (it names why), which is exactly
	// what the old (-> (List Str) Str) protocol could describe but never signal.
	put(t, st, `(defn xr-main [] [(args (List Str))] Res
	  (match args
	    ((Nil) (Ok "all good"))
	    ((Cons a rest) (Fail 3 "refusing: this tool takes no arguments"))))`)
	markVerified(t, st, "xr-main")
	return st
}

type runResult struct {
	stdout, stderr string
	code           int
}

func runCaptured(t *testing.T, bin string, args ...string) runResult {
	t.Helper()
	var so, se bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &so
	cmd.Stderr = &se
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
	}
	return runResult{so.String(), se.String(), code}
}

// Large codes exercise the clamp that a same-int64 shortcut got wrong: a Fail
// code beyond a byte (or beyond int64, or multi-limb) must exit 255 on BOTH
// backends, not wrap to its low bits. Int is arbitrary precision, so this is a
// reachable value, and the two backends must agree.
// A List published with RENAMED constructors (Empty/More instead of Nil/Cons)
// has the SAME structural identity as the canonical List — names are metadata,
// not identity — so recognition accepts it. Both backends must then build argv
// from its SHAPE (nullary at 0, binary at 1), not from the names, or one plans
// and the other fails at emission. Exercised through the exit-result entry.
func TestExitResultRenamedListConstructors(t *testing.T) {
	requireClang(t)
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Empty) (More a (List a)))`)
	put(t, st, `(data Res [] (Ok Str) (Fail Int Str))`)
	put(t, st, `(defn xr-main [] [(args (List Str))] Res
	  (match args
	    ((Empty) (Ok "no args"))
	    ((More a rest) (Fail 2 "got args"))))`)
	markVerified(t, st, "xr-main")
	goBin, _ := buildProgram(t, st, "xr-main")
	llBin := buildLLVM(t, st, "xr-main")
	for _, tc := range []struct {
		args []string
		want runResult
	}{
		{nil, runResult{stdout: "no args\n", code: 0}},
		{[]string{"x"}, runResult{stderr: "got args\n", code: 2}},
	} {
		g := runCaptured(t, goBin, tc.args...)
		l := runCaptured(t, llBin, tc.args...)
		if g != tc.want || l != tc.want {
			t.Errorf("args=%v: go=%+v llvm=%+v want=%+v", tc.args, g, l, tc.want)
		}
	}
}

func TestExitResultClampsLargeCodes(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(data Res [] (Ok Str) (Fail Int Str))`)
	// 4294967296 = 2^32 (multi-limb) and 300 (single limb, > 255) both clamp to
	// 255; 254 passes through; a negative code clamps up to 1.
	put(t, st, `(defn xr-code [] [(args (List Str))] Res
	  (match args
	    ((Nil) (Ok "ok"))
	    ((Cons a rest)
	      (match rest
	        ((Nil) (Fail 4294967296 "huge"))
	        ((Cons b more)
	          (match more
	            ((Nil) (Fail 254 "just under"))
	            ((Cons c cs) (Fail 300 "over"))))))))`)
	markVerified(t, st, "xr-code")
	goBin, _ := buildProgram(t, st, "xr-code")
	llBin := buildLLVM(t, st, "xr-code")
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"a"}, 255},           // 2^32 -> 255
		{[]string{"a", "b"}, 254},      // 254 -> 254
		{[]string{"a", "b", "c"}, 255}, // 300 -> 255
	} {
		g := runCaptured(t, goBin, tc.args...)
		l := runCaptured(t, llBin, tc.args...)
		if g.code != tc.code || l.code != tc.code {
			t.Errorf("args=%v: go exit %d, llvm exit %d, want %d", tc.args, g.code, l.code, tc.code)
		}
		if g != l {
			t.Errorf("args=%v: backends disagree go=%+v llvm=%+v", tc.args, g, l)
		}
	}
}

func TestExitResultProtocolBothBackends(t *testing.T) {
	requireClang(t)
	for _, okFirst := range []bool{true, false} {
		okFirst := okFirst
		name := "ok-first"
		if !okFirst {
			name = "fail-first"
		}
		t.Run(name, func(t *testing.T) {
			st := exitResultStore(t, okFirst)
			goBin, _ := buildProgram(t, st, "xr-main")
			llBin := buildLLVM(t, st, "xr-main")

			cases := []struct {
				args []string
				want runResult
			}{
				{nil, runResult{stdout: "all good\n", stderr: "", code: 0}},
				{[]string{"x"}, runResult{stdout: "", stderr: "refusing: this tool takes no arguments\n", code: 3}},
			}
			for _, tc := range cases {
				for _, b := range []struct{ name, bin string }{{"go", goBin}, {"llvm", llBin}} {
					got := runCaptured(t, b.bin, tc.args...)
					if got != tc.want {
						t.Errorf("[%s] args=%v: got {out:%q err:%q code:%d}, want {out:%q err:%q code:%d}",
							b.name, tc.args, got.stdout, got.stderr, got.code,
							tc.want.stdout, tc.want.stderr, tc.want.code)
					}
				}
			}
		})
	}
}
