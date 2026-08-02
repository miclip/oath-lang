package main

// #115 first slice: a second backend, tested the way the first one is.
//
// The differential gate is what makes a backend trustworthy — compiled output
// must equal what the interpreter gives — and it applies here unchanged. What is
// NEW is that the same neutral program description now feeds two lowerings, so
// the interesting assertion is not just "LLVM is right" but "both backends agree,
// and they agree because they are consuming the same description".

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireClang(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
}

// buildLLVM compiles a name through the LLVM backend and returns the executable.
func buildLLVM(t *testing.T, st *Store, name string) string {
	t.Helper()
	requireClang(t)
	prog, err := planProgram(st, name)
	if err != nil {
		t.Fatalf("planProgram(%s): %v", name, err)
	}
	out := filepath.Join(t.TempDir(), "prog")
	if err := llvmBuild(st, prog, out); err != nil {
		t.Fatalf("llvmBuild(%s): %v", name, err)
	}
	return out
}

// llvmStore is the base store for these tests: Str and List, nothing else.
func llvmStore(t *testing.T) *Store {
	t.Helper()
	st := newStore(t)
	put(t, st, `(data Str [] (SNil) (SCons Int Str))`)
	put(t, st, `(data List [a] (Nil) (Cons a (List a)))`)
	return st
}

// ---------- the differential gate ----------

// Both backends must produce the same answer for the same program, because both
// are lowering the same verified definitions. A disagreement is a compiler bug in
// whichever one diverges from `oath eval`; agreement across two independent
// lowerings is much stronger evidence than either alone.
func TestLLVMAgreesWithTheGoBackend(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn pick [] [(args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t)
			(match t ((Nil) h) ((Cons h2 t2) h2)))))`)
	markVerified(t, st, "pick")

	goBin, _ := buildProgram(t, st, "pick")
	llBin := buildLLVM(t, st, "pick")

	for _, args := range [][]string{
		nil,
		{"first"},
		{"first", "second"},
		{"first", "second", "third"},
		{""},
		{"unicode ✓ and spaces"},
	} {
		goOut, err := exec.Command(goBin, args...).Output()
		if err != nil {
			t.Fatalf("go backend run %v: %v", args, err)
		}
		llOut, err := exec.Command(llBin, args...).Output()
		if err != nil {
			t.Fatalf("llvm backend run %v: %v", args, err)
		}
		if string(goOut) != string(llOut) {
			t.Errorf("backends disagree on %v: go=%q llvm=%q", args, goOut, llOut)
		}
	}
}

// Nested matching, closures and higher-order application — the constructs most
// likely to be lowered wrongly, and the ones where an SSA phi with the wrong
// predecessor block silently produces a plausible answer.
func TestLLVMHigherOrderAndNesting(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(data Opt [a] (No) (Yes a))`)
	// `use` takes a function and applies it — the closure path.
	put(t, st, `(defn use [] [(f (-> Str Str)) (x Str)] Str (f x))`)
	put(t, st, `(defn classify [] [(args (List Str))] Str
		(match args
			((Nil) "empty")
			((Cons h t)
				(match t
					((Nil) (use (fn [(s Str)] s) h))
					((Cons h2 t2) (use (fn [(s Str)] "many") h))))))`)
	markVerified(t, st, "classify")

	goBin, _ := buildProgram(t, st, "classify")
	llBin := buildLLVM(t, st, "classify")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "empty"},
		{[]string{"solo"}, "solo"},
		{[]string{"a", "b"}, "many"},
	} {
		llOut, err := exec.Command(llBin, tc.args...).Output()
		if err != nil {
			t.Fatalf("llvm run %v: %v", tc.args, err)
		}
		if got := strings.TrimRight(string(llOut), "\n"); got != tc.want {
			t.Errorf("llvm %v = %q, want %q", tc.args, got, tc.want)
		}
		goOut, err := exec.Command(goBin, tc.args...).Output()
		if err != nil {
			t.Fatalf("go run %v: %v", tc.args, err)
		}
		if string(goOut) != string(llOut) {
			t.Errorf("backends disagree on %v: go=%q llvm=%q", tc.args, goOut, llOut)
		}
	}
}

// ---------- the capability boundary, on a second backend ----------

// The #114 invariant is a property of the compiled artifact, not of the Go
// emitter: it has to hold for any backend that consumes the neutral description.
func TestLLVMResolvesCapabilitiesBeforeLaunch(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn readvar [] [(w {env (-> Str Str)}) (args (List Str))] Str
		((. w env) "OATH_LLVM_TEST"))`)
	markVerified(t, st, "readvar")
	bin := buildLLVM(t, st, "readvar")

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_LLVM_TEST=granted")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "granted" {
		t.Fatalf("printed %q, want %q", got, "granted")
	}
}

// An unprovidable capability stops the program before it runs, with the same exit
// status the Go backend uses — a supervisor must not have to know which backend
// compiled the artifact to understand why it refused to start.
func TestLLVMLaunchGate(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn emitrec [] [(w {emit (-> Str Str)}) (args (List Str))] Str
		((. w emit) "a record"))`)
	markVerified(t, st, "emitrec")
	bin := buildLLVM(t, st, "emitrec")

	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_EMIT_PATH="+filepath.Join(blocker, "sink.log"))
	stdout, err := cmd.Output()
	if err == nil {
		t.Fatalf("launched with an unprovidable capability and printed %q", stdout)
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected failure: %v", err)
	}
	if ee.ExitCode() != exitCapabilityUnavailable {
		t.Fatalf("exit %d, want %d — the two backends must refuse identically", ee.ExitCode(), exitCapabilityUnavailable)
	}
	if len(stdout) != 0 {
		t.Fatalf("the entry point produced output (%q) despite a failed resolution", stdout)
	}
	if msg := string(ee.Stderr); !strings.Contains(msg, "emit") || !strings.Contains(msg, string(capRecordSink)) {
		t.Fatalf("refusal does not name the capability or kind: %q", msg)
	}

	// And it writes when the host can supply the sink.
	sink := filepath.Join(dir, "sink.log")
	cmd = exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_EMIT_PATH="+sink)
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("run with a writable sink: %v", err)
	}
	b, err := os.ReadFile(sink)
	if err != nil || !strings.Contains(string(b), "a record") {
		t.Fatalf("sink holds %q (err %v), want the emitted record", b, err)
	}
}

// Two backends may support different subsets of the SAME vocabulary, and the
// neutral layer does not have to care. A kind this backend cannot lower is a
// COMPILE failure naming the kind — and the identical program builds on the Go
// backend, which is what makes this a property of the backend rather than of the
// program.
func TestLLVMRefusesAKindItCannotLower(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn grab [] [(w {fetch (-> Str Str)}) (args (List Str))] Str
		((. w fetch) "http://example.invalid/"))`)
	markVerified(t, st, "grab")

	prog, err := planProgram(st, "grab")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	_, err = emitLLVM(st, prog)
	if err == nil {
		t.Fatal("the LLVM backend accepted http_request, which it cannot provide")
	}
	if !strings.Contains(err.Error(), string(capHTTPRequest)) {
		t.Fatalf("refusal does not name the kind: %v", err)
	}

	// The same program, the same neutral description, the other backend.
	prog2, err := planProgram(st, "grab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emitProgram(st, prog2); err != nil {
		t.Fatalf("the Go backend refused a program only the LLVM backend cannot lower: %v", err)
	}
}

// Unsupported LANGUAGE constructs are refused rather than miscompiled. A backend
// that guessed at what it did not understand would make the differential gate the
// only thing standing between a wrong answer and a shipped executable.
func TestLLVMRefusesWhatItCannotLower(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn arith [] [(args (List Str))] Str
		(if (< 1 2) "yes" "no"))`)
	markVerified(t, st, "arith")

	prog, err := planProgram(st, "arith")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	if _, err := emitLLVM(st, prog); err == nil {
		t.Fatal("the LLVM backend accepted integer arithmetic it cannot lower")
	} else if !strings.Contains(err.Error(), "cannot lower") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- provenance ----------

// Provenance is a property of the artifact, so it must be readable from an LLVM
// binary exactly as from a Go one — same schema, same reader, same refusal to
// execute anything — and it must name THIS backend.
func TestLLVMArtifactCarriesProvenance(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn plain [] [(args (List Str))] Str "ok")`)
	markVerified(t, st, "plain")
	bin := buildLLVM(t, st, "plain")

	raw := readProvenance(t, bin)
	if !strings.Contains(string(raw), llvmBackendVersion) {
		t.Errorf("manifest does not name the backend that produced it:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"entry": "plain"`) {
		t.Errorf("manifest does not identify the entry:\n%s", raw)
	}
	// Backend identity is the ONLY thing that should differ from a Go build of the
	// same program: everything else describes the artifact, not the lowering.
	goBin, _ := buildProgram(t, st, "plain")
	goRaw := readProvenance(t, goBin)
	norm := func(b []byte) string {
		return strings.ReplaceAll(strings.ReplaceAll(string(b), llvmBackendVersion, "X"), goBackendVersion, "X")
	}
	if norm(raw) != norm(goRaw) {
		t.Errorf("the two backends disagree about the artifact:\n--- llvm ---\n%s\n--- go ---\n%s", raw, goRaw)
	}
}

// Every kind this backend claims to provide must have a runtime function, and it
// must not claim kinds Oath does not define — the same two directions the Go
// backend's table is held to.
func TestLLVMProviderTableIsConsistent(t *testing.T) {
	declared := map[capabilityKind]bool{}
	for _, decl := range capabilityVocabulary {
		declared[decl.Kind] = true
	}
	for kind, p := range llvmProviders {
		if !declared[kind] {
			t.Errorf("the LLVM backend provides %q, which no capability in Oath's vocabulary denotes", kind)
		}
		if p.Fn == "" {
			t.Errorf("kind %q has no runtime function", kind)
		}
		if !strings.Contains(llvmRuntimeC, p.Fn) {
			t.Errorf("kind %q names runtime function %q, which the runtime does not define", kind, p.Fn)
		}
		if !strings.Contains(llvmDeclarations, p.Fn) {
			t.Errorf("kind %q names runtime function %q, which the IR does not declare", kind, p.Fn)
		}
	}
}

// An entry typed (-> {} (-> (List Str) Str)) requires no authority and still
// TAKES a capability argument. Keying application on the requirement count rather
// than the record passes argv where the record belongs and prints a closure — the
// Go backend had this exact defect, and repeating it in a second backend is what
// makes it worth a test in both.
func TestLLVMEmptyCapabilityRecordIsStillApplied(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn nocaps [] [(w {}) (args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, st, "nocaps")

	llBin := buildLLVM(t, st, "nocaps")
	goBin, _ := buildProgram(t, st, "nocaps")
	for _, args := range [][]string{nil, {"hello"}} {
		llOut, err := exec.Command(llBin, args...).Output()
		if err != nil {
			t.Fatalf("llvm run %v: %v", args, err)
		}
		goOut, err := exec.Command(goBin, args...).Output()
		if err != nil {
			t.Fatalf("go run %v: %v", args, err)
		}
		if string(llOut) != string(goOut) {
			t.Errorf("backends disagree on %v: llvm=%q go=%q", args, llOut, goOut)
		}
	}
}

// A Str is a codepoint sequence and may contain NUL. Representing it as a
// C string truncates at the first one — silently, and only for inputs nobody
// inspects by eye, which is the worst shape a compiler bug can have.
func TestLLVMPreservesNulBytes(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn readit [] [(w {readfile (-> Str Str)}) (args (List Str))] Str
		(match args ((Nil) "no path") ((Cons p t) ((. w readfile) p))))`)
	markVerified(t, st, "readit")

	content := "before\x00after"
	path := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	llBin := buildLLVM(t, st, "readit")
	goBin, _ := buildProgram(t, st, "readit")
	llOut, err := exec.Command(llBin, path).Output()
	if err != nil {
		t.Fatalf("llvm run: %v", err)
	}
	goOut, err := exec.Command(goBin, path).Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}
	if string(llOut) != string(goOut) {
		t.Errorf("backends disagree on a file containing NUL: llvm=%q go=%q", llOut, goOut)
	}
	if !strings.Contains(string(llOut), "after") {
		t.Errorf("llvm truncated at the NUL byte: %q", llOut)
	}
}

// Matching an instantiated generic datatype must instantiate its constructor's
// field types. Pushing the DECLARED types leaves type variables in the checker
// context, so projecting a field of a bound record fails — a valid program
// refused, which is a different failure from an unsupported one and must not be
// reported as one.
func TestLLVMInstantiatesPolymorphicCtorFields(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(data Box [a] (Box a))`)
	put(t, st, `(defn unwrap [] [(args (List Str))] Str
		(match (Box [{label Str}] {label "boxed"})
			((Box r) (. r label))))`)
	markVerified(t, st, "unwrap")

	llBin := buildLLVM(t, st, "unwrap")
	out, err := exec.Command(llBin).Output()
	if err != nil {
		t.Fatalf("llvm run: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "boxed" {
		t.Fatalf("llvm printed %q, want %q", got, "boxed")
	}
	goBin, _ := buildProgram(t, st, "unwrap")
	goOut, _ := exec.Command(goBin).Output()
	if string(goOut) != string(out) {
		t.Errorf("backends disagree: llvm=%q go=%q", out, goOut)
	}
}
