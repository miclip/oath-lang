package main

// BUILDING A Str AT RUNTIME (#164).
//
// The LLVM backend used to fold a Str constructor chain at compile time and
// REFUSE anything else, which bounded the whole backend to a class #158 measured
// and named: every result a compiled program can produce is a literal or a
// SUFFIX of an input. It could search and echo; it could not report.
//
// These four tests are the four halves of that change that can fail
// independently, which is why they are four and not one:
//
//	construction WORKS            — and agrees with the interpreter, three ways
//	a non-scalar element REFUSES  — at run time now, by class, exit 70
//	a LITERAL chain still folds   — the compile-time check did not move
//	a tail VIEW survives          — the new allocator did not invalidate one
//
// The third is the control for the first: without it, "construction is lowered"
// would be satisfied by lowering EVERYTHING dynamically, which would silently
// retire the compile-time refusal and make every literal a runtime allocation.

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// strConsStore is llvmStore plus the definitions that need runtime construction:
// append (a non-constant head AND a non-constant tail) and shift (a codepoint
// that appears in no literal and in no input).
func strConsStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(defn str-append [] [(a Str) (b Str)] Str
		(match a ((SNil) b) ((SCons c rest) (SCons c (str-append rest b)))))`)
	put(t, st, `(defn str-shift [] [(s Str)] Str
		(match s ((SNil) (SNil)) ((SCons c rest) (SCons (+ c 1) (str-shift rest)))))`)
	put(t, st, `(defn str-drop1 [] [(s Str)] Str
		(match s ((SNil) (SNil)) ((SCons c rest) rest)))`)
	return st
}

// threeWay runs one entry through the interpreter, the Go backend and the LLVM
// backend and reports any disagreement, with the interpreter as the reference —
// two identically wrong lowerings agree, so a backend-to-backend comparison is
// not evidence.
func threeWay(t *testing.T, st *Store, entry string, argv [][]string) {
	t.Helper()
	goBin, _ := buildProgram(t, st, entry)
	llBin := buildLLVM(t, st, entry)
	for _, args := range argv {
		lit, ok := oathList(args)
		if !ok {
			t.Fatalf("args %v cannot be written as an Oath literal, so the three paths "+
				"would not be receiving the same input", args)
		}
		want := evalDenotation(t, st, "("+entry+" "+lit+")")
		goOut, err := exec.Command(goBin, args...).Output()
		if err != nil {
			t.Fatalf("%s: go run %v: %v", entry, args, err)
		}
		llOut, err := exec.Command(llBin, args...).Output()
		if err != nil {
			t.Fatalf("%s: llvm run %v: %v", entry, args, err)
		}
		if v := threeWayVerdict(want, trimFrame(string(goOut)), trimFrame(string(llOut))); v != "" {
			t.Errorf("%s %v: %s", entry, args, v)
		}
	}
}

// A Str BUILT FROM VALUES THE COMPILER CANNOT SEE.
//
// `report` is #164's own example — `"missing key: " ++ k` — and it is the case
// the old subset could not express at all. `shifted` is the stronger one: every
// codepoint it emits is arithmetic on an input codepoint, so the result appears
// in no literal in the program and is not a suffix of any argument. If the
// backend were still folding, or had quietly started substituting, neither could
// agree with the interpreter.
func TestLLVMBuildsAStrFromRuntimeValues(t *testing.T) {
	requireClang(t)
	st := strConsStore(t)
	put(t, st, `(defn report [] [(args (List Str))] Str
		(match args
			((Nil) "missing key: <none>")
			((Cons h t) (str-append "missing key: " h))))`)
	markVerified(t, st, "report")
	put(t, st, `(defn shifted [] [(args (List Str))] Str
		(match args ((Nil) "") ((Cons h t) (str-shift h))))`)
	markVerified(t, st, "shifted")

	// THE GREP CONTROL for the fold test below: that test asserts this exact
	// string is ABSENT from a literal-only program, and a typo there would make
	// the absence vacuous. Assert it is PRESENT where construction is real.
	prog, err := planProgram(st, "report")
	if err != nil {
		t.Fatal(err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("emitLLVM(report): %v", err)
	}
	if !strings.Contains(ir, "call ptr @o_str_cons") {
		t.Error("a program that appends two Str values emitted no o_str_cons call, so the " +
			"absence asserted by the fold test is not evidence of folding")
	}

	threeWay(t, st, "report", [][]string{
		nil,
		{"host"},
		{""},
		{"a name with spaces and ü"},
		{"first", "second"},
	})
	// Shifted inputs stay well clear of the surrogate block and 0x10FFFF, since
	// c+1 landing on a non-scalar is the REFUSAL path and is tested below; here
	// the claim is that construction produces the right bytes.
	threeWay(t, st, "shifted", [][]string{
		nil,
		{""},
		{"abc"},
		{"HAL"},
		{"héllo"},
	})
}

// EVERY NON-SCALAR CLASS REFUSES AT RUN TIME, AND SAYS WHICH ONE.
//
// The head of each SCons here is computed from an input codepoint, so nothing is
// foldable and the compile-time scan cannot see it — this is the o_str_cons path
// and nothing else.
//
// Two claims, and the second is the one that decays quietly: the program must
// FAIL rather than substitute, and the four failures must stay DISTINGUISHABLE.
// Collapsing them would be the same defect as U+FFFD, relocated from the output
// into the diagnostic — three inputs, one message.
func TestLLVMRefusesANonScalarBuiltAtRuntime(t *testing.T) {
	requireClang(t)
	// Each entry maps the first input codepoint into one non-scalar class. With
	// argv[0] = "A" (65): -65, 0xD800, 0x110000, and 65 * 10^21 — the last one
	// past int64, which is the limb path through the decimal renderer.
	cases := []struct {
		name, expr, want string
	}{
		{"cons-negative", "(- 0 c)", "-65"},
		{"cons-surrogate", "(+ c 55231)", "55296"},
		{"cons-above-max", "(+ c 1114047)", "1114112"},
		// Exactly 2^32 — TWO limbs, and the low one is zero. A renderer that read
		// imag[0] alone, or that lost the high limb, says 0 here; the value also
		// sits one above the largest single-limb magnitude, which is where the
		// ilen>1 refusal and the >0x10FFFF refusal have to agree.
		{"cons-limb-boundary", "(+ c 4294967231)", "4294967296"},
		// 65 * 10^21: many limbs, and eighteen ZERO digits that only survive if
		// interior 9-digit chunks keep their leading zeros. Verified by mutation:
		// dropping that padding renders this value as 65000.
		{"cons-astronomical", "(* c (* 1000000000000 1000000000))", "65000000000000000000000"},
	}
	st := strConsStore(t)
	seen := map[string]string{}
	for _, c := range cases {
		put(t, st, fmt.Sprintf(`(defn %s [] [(args (List Str))] Str
			(match args
				((Nil) "")
				((Cons h t)
					(match h ((SNil) "") ((SCons c rest) (SCons %s (SNil)))))))`, c.name, c.expr))
		markVerified(t, st, c.name)

		// It must COMPILE — the whole point is that the refusal moved to run time.
		bin := buildLLVM(t, st, c.name)
		out, err := exec.Command(bin, "A").CombinedOutput()
		if err == nil {
			t.Errorf("%s: the artifact produced %q instead of refusing the non-scalar element",
				c.name, out)
			continue
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 70 {
			t.Errorf("%s: exited %v, want 70 — the exit code is what a supervisor reads, and "+
				"70 is what this runtime uses for every host-side refusal", c.name, err)
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("%s: refused without naming the element %s: %s", c.name, c.want, out)
		}
		if strings.Contains(string(out), "�") {
			t.Errorf("%s: substituted U+FFFD instead of refusing", c.name)
		}
		if prev, dup := seen[string(out)]; dup {
			t.Errorf("%s and %s produce the SAME message — distinct refused inputs must stay "+
				"distinguishable, which is the whole reason packing refuses", prev, c.name)
		}
		seen[string(out)] = c.name

		// A CONTROL ON THE PROGRAM ITSELF, not on the backend: the same artifact
		// must SUCCEED where the arithmetic lands on a scalar, or "it refuses" is
		// satisfied by an entry that fails for some unrelated reason.
		if _, err := exec.Command(bin).Output(); err != nil {
			t.Errorf("%s: refused the EMPTY argument list too, so the failure above is not "+
				"evidence about the element: %v", c.name, err)
		}

		// AND THE OTHER BACKEND MUST REFUSE THE SAME VALUE FOR THE SAME STATED
		// REASON. Both pack Str as UTF-8, so they exclude exactly the same set —
		// but "both refuse" is a weaker claim than "both say why", and it was the
		// weaker one that held: testing IsInt64 before the sign left the Go
		// backend naming the astronomical case with a bare number while this
		// backend named its class. Two artifacts compiled from ONE definition
		// telling different stories about why they stopped is the same defect
		// class as the div/mod diagnostic #166 found, and only a check that reads
		// the message sees either.
		goBin, _ := buildProgram(t, st, c.name)
		goOut, goErr := exec.Command(goBin, "A").CombinedOutput()
		if goErr == nil {
			t.Errorf("%s: the Go backend produced %q instead of refusing", c.name, goOut)
			continue
		}
		if !strings.Contains(string(goOut), c.want) {
			t.Errorf("%s: the Go backend refused without naming the element %s: %s",
				c.name, c.want, goOut)
		}
		for _, class := range []string{"negative", "above the maximum scalar", "a surrogate"} {
			if strings.Contains(string(out), class) != strings.Contains(string(goOut), class) {
				t.Errorf("%s: the backends disagree about the CLASS %q — llvm=%q go=%q",
					c.name, class, out, goOut)
			}
		}
	}
}

// THE COMPILE-TIME CHECK DID NOT MOVE.
//
// A chain of literal codepoints still folds to a constant, and a literal
// non-scalar is still refused BY THE COMPILER with reasonStrElementRange. This
// is the control for the test above: lowering everything through o_str_cons
// would pass every dynamic-construction assertion while silently retiring the
// build-time refusal and turning every string literal into a runtime allocation.
func TestLLVMStillFoldsAndRefusesLiteralStrChains(t *testing.T) {
	st := strConsStore(t)

	put(t, st, `(defn literal-only [] [(args (List Str))] Str "folded ü")`)
	markVerified(t, st, "literal-only")
	prog, err := planProgram(st, "literal-only")
	if err != nil {
		t.Fatal(err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("a literal Str no longer lowers: %v", err)
	}
	if strings.Contains(ir, "call ptr @o_str_cons") {
		t.Error("a fully literal Str chain now calls o_str_cons — it must fold to a " +
			"constant at compile time, which is what keeps codepoints out of the runtime " +
			"for the case the compiler can decide")
	}
	if !strings.Contains(ir, "@o_strn") {
		t.Error("the folded literal did not reach o_strn, so this test is not observing " +
			"the fold at all")
	}

	// Each literal class refused at BUILD time, named, with the typed reason —
	// matched on Reason rather than on prose, since the help paragraph lists
	// unsupported features and a substring test would survive the feature landing.
	for _, bad := range []struct{ name, cp string }{
		{"lit-neg", "-1"},
		{"lit-surrogate", "55296"},
		{"lit-above-max", "1114112"},
		// Beyond int64. This one used to reach the generic non-constant refusal,
		// because the scan could not hold a value it could not name.
		{"lit-astronomical", "99999999999999999999"},
	} {
		put(t, st, `(defn `+bad.name+` [] [(args (List Str))] Str (SCons `+bad.cp+` (SNil)))`)
		markVerified(t, st, bad.name)
		prog, err := planProgram(st, bad.name)
		if err != nil {
			t.Fatal(err)
		}
		_, err = emitLLVM(st, prog)
		if err == nil {
			t.Errorf("%s: the compiler lowered a literal non-scalar", bad.name)
			continue
		}
		if r, ok := refusedFor(err); !ok || r != reasonStrElementRange {
			t.Errorf("%s: refused with reason %q, want %q", bad.name, r, reasonStrElementRange)
		}
		if !strings.Contains(err.Error(), bad.cp) {
			t.Errorf("%s: refused without naming the element: %v", bad.name, err)
		}
	}
}

// A TAIL IS A VIEW, AND CONSTRUCTION MUST NOT INVALIDATE ONE.
//
// o_str_tail returns a pointer INTO its parent's buffer rather than a copy, which
// was sound while every buffer was an IR constant or a getenv result. Runtime
// construction is the first allocating producer of Str buffers, and an allocator
// that reused or freed one would break every view taken before it — silently, and
// only for programs that hold a tail across a later construction.
//
// So the entry takes a tail FIRST, then builds two more strings, then uses the
// tail it took. What mutation makes this fail: any o_str_cons that writes into,
// frees or reuses a buffer a view already spans. It does NOT witness the copy
// itself — a version that shared the tail's bytes and never mutated them would
// also pass, correctly, because the claim is about lifetime and not about
// layout.
func TestLLVMTailViewSurvivesLaterConstruction(t *testing.T) {
	requireClang(t)
	st := strConsStore(t)
	put(t, st, `(defn held-tail [] [(args (List Str))] Str
		(match args
			((Nil) "")
			((Cons h t)
				(match (str-drop1 (str-append "xy" h))
					((SNil) "empty")
					((SCons c rest)
						(str-append (SCons c rest)
							(str-append (str-shift h) (str-append "0123456789" h))))))))`)
	markVerified(t, st, "held-tail")
	threeWay(t, st, "held-tail", [][]string{
		nil,
		{""},
		{"a"},
		{"a longer argument, held across several later allocations"},
	})
}
