package main

// #115 first slice: a second backend, tested the way the first one is.
//
// The differential gate is what makes a backend trustworthy — compiled output
// must equal what the interpreter gives — and it applies here unchanged. What is
// NEW is that the same neutral program description now feeds two lowerings, so
// the interesting assertion is not just "LLVM is right" but "both backends agree,
// and they agree because they are consuming the same description".

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// requireClang skips locally and FAILS in CI.
//
// Every test in this file that actually exercises the backend needs clang, so a
// plain skip means one absent tool silently removes the entire second backend —
// the differential gate included — while the run stays green. That is the
// failure shape this repo keeps finding: a check that cannot distinguish "my
// setup is broken" from "the thing I hunt is absent". `ubuntu-latest` ships
// clang today, which is exactly why nobody would notice the day it stopped.
//
// Skipping is still right for a contributor without clang; it is never right for
// the gate. CI is the only place that must be able to tell the difference, so CI
// is where the skip becomes a failure.
func requireClang(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("clang is absent, so the LLVM backend and the three-way " +
				"differential gate did not run. In CI that is a failure, not a " +
				"skip: a green run would otherwise mean this backend is unchecked.")
		}
		t.Skip("clang not available (local); this is a hard failure under CI=1")
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
// THE SUBSET BOUNDARY, BOTH SIDES OF IT.
//
// This checked that `<` was refused, back when the backend lowered exactly one
// primitive. #166 moved `+ - * == < <=` inside, so the contract it encoded
// deliberately changed — and by the rule this repo works to, a replacement must
// pin the NEW contract at least as tightly. A refusal on its own cannot: it
// witnesses that something is still outside the boundary without witnessing that
// the boundary moved, so a lowering silently deleted later would keep this green.
//
// Hence the table. Every operation named `accepted` must emit, every operation
// named `refused` must be refused BY NAME, and the two lists together are the
// claim. `neg` is unary and unlowered, so it is the remaining Int boundary.
//
// ACCEPTANCE IS THE WEAKER HALF OF EACH ROW AND IS NOT LEFT TO CARRY THE CLAIM.
// This asserts only that an operation LOWERS; it says nothing about the value
// produced, so a row moved from refused to accepted has surrendered a check
// unless something else pins the semantics. For `/` and `%` that is
// `docs/experiments/issue-158-llvm-subset/acceptance.sh`, which compares both
// against `oath eval` and holds all three paths to naming the by-zero condition
// they actually hit — strictly more than the refusal it replaced.
func TestLLVMPrimitiveBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		accepted   bool
	}{
		{"add", `(if (== (+ 1 2) 3) "yes" "no")`, true},
		{"sub", `(if (== (- 3 1) 2) "yes" "no")`, true},
		{"mul", `(if (== (* 2 3) 6) "yes" "no")`, true},
		{"eq", `(if (== 1 1) "yes" "no")`, true},
		{"lt", `(if (< 1 2) "yes" "no")`, true},
		{"le", `(if (<= 1 2) "yes" "no")`, true},
		{"div", `(if (== (/ 4 2) 2) "yes" "no")`, true},
		{"mod", `(if (== (% 4 2) 0) "yes" "no")`, true},
		{"neg", `(if (== (neg 1) -1) "yes" "no")`, false},
		// `==` is polymorphic and only its Int instance is lowered, so this is
		// not a stray case: it is the guard that keeps the six rows above from
		// being read as "the operator is supported".
		{"eq-on-str", `(if (== "a" "a") "yes" "no")`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := llvmStore(t)
			put(t, st, `(defn probe [] [(args (List Str))] Str `+tc.body+`)`)
			markVerified(t, st, "probe")
			prog, err := planProgram(st, "probe")
			if err != nil {
				t.Fatalf("planProgram: %v", err)
			}
			_, err = emitLLVM(st, prog)
			if tc.accepted {
				if err != nil {
					t.Fatalf("the LLVM backend refused %s, which it lowers: %v", tc.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the LLVM backend accepted %s, which it cannot lower", tc.name)
			}
			if !strings.Contains(err.Error(), "cannot lower") {
				t.Fatalf("unexpected error: %v", err)
			}
			// BY NAME, not merely refused. A refusal that did not say which
			// operation was missing would leave a user guessing, and would let a
			// refusal for an unrelated reason pass as this one.
			if !strings.Contains(err.Error(), "primitive operation") {
				t.Fatalf("%s was refused without naming a primitive: %v", tc.name, err)
			}
		})
	}
}

// An Int literal of any magnitude must reach the emitter. This is the retirement
// of `int-range` stated as a test: the old backend refused the literal below at
// compile time, and refusing it again would be a regression to a fixed-width
// representation rather than a stricter subset.
func TestLLVMAcceptsLargeIntLiterals(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn huge [] [(args (List Str))] Str
		(if (== (+ 9223372036854775807 1) 9223372036854775808) "yes" "no"))`)
	markVerified(t, st, "huge")
	prog, err := planProgram(st, "huge")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("the LLVM backend refused a literal outside int64: %v", err)
	}
	// The digits must appear in the emitted IR. Without this the test would pass
	// on a backend that accepted the literal and silently emitted something else,
	// which is the failure the old range check existed to prevent.
	if !strings.Contains(ir, "9223372036854775808") {
		t.Fatal("the emitted IR does not carry the literal's digits, so the value " +
			"reaching the runtime is not the one in the source")
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

// ---------- the three-way disagreement gate ----------
//
// `oath eval` is the semantic reference. Neither compiler is the other's oracle:
// comparing the two backends only tells you they agree, which two identically
// wrong lowerings also do. The interpreter is what either can be wrong ABOUT.
//
// The existing differential tests compare the backends to each other, and that
// was a real weakness in the first slice — it made the Go backend the reference
// by default. This runs all three.

// evalDenotation evaluates an expression through the interpreter and returns what
// its Str result DENOTES, so it can be compared with what a compiled program
// prints. The interpreter renders Str structurally — (SCons 104 (SCons ...)) —
// because it is an ordinary datatype there; a compiled program prints the string
// those codepoints spell.
func evalDenotation(t *testing.T, st *Store, expr string) string {
	t.Helper()
	out, err := apiEval(st, expr)
	if err != nil {
		t.Fatalf("eval %s: %v", expr, err)
	}
	if i := strings.LastIndex(out, " : "); i >= 0 {
		out = out[:i]
	}
	var sb strings.Builder
	rest := out
	for {
		i := strings.Index(rest, "(SCons ")
		if i < 0 {
			break
		}
		rest = rest[i+len("(SCons "):]
		j := strings.IndexByte(rest, ' ')
		if j < 0 {
			t.Fatalf("malformed Str in eval output: %s", out)
		}
		n, err := strconv.Atoi(rest[:j])
		if err != nil {
			t.Fatalf("malformed codepoint %q in %s", rest[:j], out)
		}
		sb.WriteRune(rune(n))
		rest = rest[j+1:]
	}
	return sb.String()
}

// oathQuote renders a string as an Oath literal using ONLY the escapes the Oath
// lexer accepts. strconv.Quote emits Go's set — \r, \b, \x01 — which the lexer
// does not read, so the interpreter would fail to parse an input both compiled
// binaries accept, and the three paths would no longer be receiving the same
// thing. Returns ok=false for a character with no Oath spelling.
func oathQuote(s string) (string, bool) {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if r < 0x20 || r == 0x7f {
				return "", false
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), true
}

// oathList renders arguments as the (List Str) literal the interpreter needs, so
// the SAME input reaches all three execution paths.
func oathList(args []string) (string, bool) {
	out := "(Nil [Str])"
	for i := len(args) - 1; i >= 0; i-- {
		q, ok := oathQuote(args[i])
		if !ok {
			return "", false
		}
		out = fmt.Sprintf("(Cons [Str] %s %s)", q, out)
	}
	return out, true
}

// trimFrame removes the ONE newline the CLI protocol appends. TrimRight would
// also eat newlines the result itself ends with, so a correct "x\n" would read as
// "x" and be reported as a disagreement that is not there.
func trimFrame(s string) string {
	return strings.TrimSuffix(s, "\n")
}

// threeWayVerdict compares the interpreter's answer with both backends' and
// reports what disagreed. Pure, so the comparison itself is testable — a gate
// whose logic is never exercised is a gate that could be inverted without anyone
// noticing.
func threeWayVerdict(want, gotGo, gotLL string) string {
	switch {
	case gotGo != want && gotLL != want:
		return fmt.Sprintf("BOTH backends disagree with the interpreter — eval=%q go=%q llvm=%q", want, gotGo, gotLL)
	case gotGo != want:
		return fmt.Sprintf("the Go backend disagrees with the interpreter — eval=%q go=%q", want, gotGo)
	case gotLL != want:
		return fmt.Sprintf("the LLVM backend disagrees with the interpreter — eval=%q llvm=%q", want, gotLL)
	}
	return ""
}

// For every supported pure program, the interpreter, the Go backend and the LLVM
// backend must all agree — with the interpreter as the reference, so a shared
// compiler bug cannot pass by consensus.
func TestThreeWayAgreement(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(data Box [a] (Box a))`)
	put(t, st, `(defn use [] [(f (-> Str Str)) (x Str)] Str (f x))`)
	put(t, st, `(defn triage [] [(args (List Str))] Str
		(match args
			((Nil) "empty")
			((Cons h t)
				(match t
					((Nil) (use (fn [(s Str)] s) h))
					((Cons h2 t2) (match (Box [{tag Str}] {tag h2}) ((Box r) (. r tag))))))))`)
	markVerified(t, st, "triage")

	goBin, _ := buildProgram(t, st, "triage")
	llBin := buildLLVM(t, st, "triage")

	for _, args := range [][]string{
		nil,
		{"only"},
		{"first", "second"},
		{"", "ü — unicode"},
		{"a", "b", "c"},
	} {
		lit, ok := oathList(args)
		if !ok {
			t.Fatalf("args %v cannot be written as an Oath literal, so the three paths "+
				"would not be receiving the same input", args)
		}
		want := evalDenotation(t, st, "(triage "+lit+")")

		goOut, err := exec.Command(goBin, args...).Output()
		if err != nil {
			t.Fatalf("go run %v: %v", args, err)
		}
		llOut, err := exec.Command(llBin, args...).Output()
		if err != nil {
			t.Fatalf("llvm run %v: %v", args, err)
		}
		if v := threeWayVerdict(want, trimFrame(string(goOut)), trimFrame(string(llOut))); v != "" {
			t.Errorf("args %v: %s", args, v)
		}
	}
}

// The gate has to be able to fail, or it is decoration. Feeding the comparison a
// deliberately wrong reference must be reported against the interpreter rather
// than quietly passing because the two backends agree with each other.
func TestThreeWayGateDetectsDisagreement(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn greet [] [(args (List Str))] Str "hello")`)

	want := evalDenotation(t, st, "(greet (Nil [Str]))")
	if want != "hello" {
		t.Fatalf("the interpreter reference decoded as %q, want %q — the decoder is wrong, "+
			"which would make every comparison against it meaningless", want, "hello")
	}
	// A non-empty Str must not decode to the empty string: that is the failure
	// shape that would make the gate pass for everything.
	if evalDenotation(t, st, `"x"`) != "x" {
		t.Fatal("the decoder collapses literals; the gate would accept any output")
	}

	// And the comparison itself must report each way it can fail. Without this the
	// switch could be inverted or deleted and every three-way test would still
	// pass, which is the exact shape of a check that reads as evidence and is not.
	for _, tc := range []struct {
		want, got, gotLL, expect string
	}{
		{"a", "a", "a", ""},
		{"a", "b", "a", "the Go backend disagrees"},
		{"a", "a", "b", "the LLVM backend disagrees"},
		{"a", "b", "b", "BOTH backends disagree"},
		{"", "", "x", "the LLVM backend disagrees"},
	} {
		v := threeWayVerdict(tc.want, tc.got, tc.gotLL)
		if tc.expect == "" && v != "" {
			t.Errorf("agreement reported as a failure: %s", v)
		}
		if tc.expect != "" && !strings.Contains(v, tc.expect) {
			t.Errorf("want=%q go=%q llvm=%q gave %q, want it to contain %q",
				tc.want, tc.got, tc.gotLL, v, tc.expect)
		}
	}
}

// Capability programs are compared on the four things that are observable
// without trusting either compiler: what they declare, how they refuse, what they
// print, and what they record.
func TestCapabilityParityAcrossBackends(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn readvar [] [(w {env (-> Str Str)}) (args (List Str))] Str
		((. w env) "OATH_PARITY"))`)
	markVerified(t, st, "readvar")

	goBin, _ := buildProgram(t, st, "readvar")
	llBin := buildLLVM(t, st, "readvar")

	// 1. the same declared requirements, and 4. the same record but for backend.
	goRaw, llRaw := readProvenance(t, goBin), readProvenance(t, llBin)
	norm := func(b []byte) string {
		return strings.ReplaceAll(strings.ReplaceAll(string(b), llvmBackendVersion, "X"), goBackendVersion, "X")
	}
	if norm(goRaw) != norm(llRaw) {
		t.Errorf("the backends describe the artifact differently:\n--- go ---\n%s\n--- llvm ---\n%s", goRaw, llRaw)
	}

	// 3. the same externally observable output.
	for _, val := range []string{"granted", "", "with spaces"} {
		var outs []string
		for _, bin := range []string{goBin, llBin} {
			cmd := exec.Command(bin)
			cmd.Env = append(os.Environ(), "OATH_PARITY="+val)
			o, err := cmd.Output()
			if err != nil {
				t.Fatalf("run %s: %v", bin, err)
			}
			outs = append(outs, string(o))
		}
		if outs[0] != outs[1] {
			t.Errorf("OATH_PARITY=%q: go=%q llvm=%q", val, outs[0], outs[1])
		}
	}
}

// ---------- #158's falsifier, run through the real CLI ----------
//
// Everything above exercises the backend IN PROCESS, which cannot witness the
// claim #158 actually asks about: that a person can write a useful program,
// `oath put` it, and `oath build --backend llvm` it. The acceptance script does
// that through the CLI into a scratch store, with `oath eval` as the reference
// for both backends and a control that must fire.
//
// It lives under docs/experiments/ because it is the experiment's record; it is
// invoked from HERE because an acceptance script nothing runs is a document
// pretending to be a gate. Two seconds, and it is the only check in this file
// that would notice `oath put`, argument passing or the build command breaking
// while the emitter stayed correct.
func TestLLVMSubsetAcceptanceScript(t *testing.T) {
	requireClang(t)
	script := filepath.Join("..", "docs", "experiments", "issue-158-llvm-subset", "acceptance.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the acceptance script is missing: %v", err)
	}
	// OATH_BIN IS STRIPPED, not merely left unset. The script honours it so a
	// person can point the gate at a particular binary; inheriting one here would
	// silently test something other than this checkout, which is the same shape as
	// an exported OATH_STORE disabling the publication guard — a variable that is
	// evidence of intent when typed per-command and a trap once it is exported.
	cmd := exec.Command("sh", script)
	cmd.Env = cmd.Env[:0]
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "OATH_BIN=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("acceptance failed: %v\n%s", err, out)
	}
	// ASSERT THE SUMMARY LINE AND THE COUNT, not the exit status. A harness that
	// dies during its own setup exits 0 on some shells and reads exactly like a
	// clean pass; and one whose checks were quietly narrowed to none would still
	// print "all 0 checks passed".
	var ran int
	if _, e := fmt.Sscanf(lastSummaryLine(string(out)), "show-from-marker acceptance: all %d checks passed", &ran); e != nil {
		t.Fatalf("the script did not reach its own verdict:\n%s", out)
	}
	got := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "  ok    ") {
			got++
		}
	}
	if got != ran {
		t.Errorf("the summary claims %d checks and %d were printed — the count and the "+
			"checks disagree, so one of them is not measuring the run", ran, got)
	}
	// A FLOOR, so a future edit cannot narrow the acceptance to nothing and stay
	// green. It is not the current count restated: it is the smallest number that
	// still covers the control, the fail-closed control and the three-way cases.
	if ran < 20 {
		t.Errorf("acceptance ran %d checks, fewer than the 20 that cover the controls", ran)
	}

	// AND A COUNT IS A PROXY FOR COVERAGE, WHICH IS THE DEFECT THE FLOOR ALONE
	// CARRIES. A floor set below the current count permits deleting a whole
	// FAMILY of checks and staying green — deleting every arbitrary-precision
	// case above left 20-odd others behind and would have passed. The claim is
	// that each family is exercised, and the structural owner of that claim is
	// the check LABELS, not their number: a family cannot be removed without its
	// label going with it.
	//
	// These name families, never individual cases, so adding or refining a check
	// inside one does not touch this list.
	for _, family := range []string{
		"the LLVM backend still refuses what it cannot lower", // the fail-closed control
		"CONTROL: a wrong reference IS reported",              // the comparator's own control
		"int-large-literals",                                  // beyond int64, parsed and carried
		"int-carry",                                           // limb boundaries
		"int-sign",                                            // sign-magnitude
		"int-multiplication",
		"int-ordering",
		"int-divmod-identity", // the law relating the two
		"int-divide-by-zero",  // the disposition, not just the value
		"int-modulo-by-zero",  // and that it is named DISTINCTLY from division
		"digit literal survives compilation",

		// #164, runtime Str construction. Five labels rather than one, because
		// the family has halves that fail independently: a result the old subset
		// could not express, a result no view of the input spells, a tail held
		// across a later allocation, and the two sides of the non-scalar
		// disposition — which are not one claim, since the interpreter ACCEPTING
		// and the packed backends REFUSING could each regress alone.
		"str-report",    // "missing key: " ++ k — the case the subset could not express
		"str-mirrored",  // order-reversing, so no view of the argument spells it
		"str-held-tail", // a tail view surviving later construction
		"the interpreter ACCEPTS the runtime non-scalar", // SPEC 3's kernel obligation
		"str-nonscalar-surrogate",                        // and the packed backends refusing
	} {
		if !strings.Contains(string(out), family) {
			t.Errorf("acceptance no longer exercises %q — a family was removed, "+
				"which the count floor alone cannot see", family)
		}
	}
}

// lastSummaryLine returns the final non-empty line of the script's output.
func lastSummaryLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}
