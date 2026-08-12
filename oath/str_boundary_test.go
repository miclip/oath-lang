package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// THE Str BOUNDARY (#133): bytes from outside the language become a Str only by
// being valid UTF-8, and every refusal here is a refusal to SUBSTITUTE.
//
// The rule these tests defend is one sentence — no path may invent a codepoint
// the input does not denote — but it has to hold at four different boundaries,
// and the reason it matters is different at each. At the parser it is an
// IDENTITY property; at a capability it is a runtime one.

// The parser is the boundary that reaches IDENTITY, so it is the one whose
// failure is permanent.
//
// []rune(s) yields U+FFFD per malformed byte, that codepoint enters the SCons
// chain, the chain is the canonical encoding, and the encoding is the hash. So
// distinct source files content-addressed to one object — measured before the
// fix, four different bytes all producing af78b48ff413. Names, journal entries
// and signatures reference that hash and publication is permanent, which is why
// this boundary cannot be left to a runtime check downstream.
func TestSourceMustBeValidUTF8(t *testing.T) {
	// CONTROL FIRST. If valid multibyte text does not lex, every rejection below
	// is uninterpretable — the vectors would be failing for whatever broke the
	// lexer rather than for the reason each declares.
	for _, ok := range []string{
		`(defn f [] [(x Int)] Str "plain ascii")`,
		`(defn f [] [(x Int)] Str "café")`,          // 2-byte
		`(defn f [] [(x Int)] Str "check ✓")`,       // 3-byte
		`(defn f [] [(x Int)] Str "lock 🔒")`,        // 4-byte, astral
		"(defn f [] [(x Int)] Str \"�\")",      // a VALIDLY ENCODED U+FFFD
		`; comment with é and 🔒` + "\n(defn f [] [(x Int)] Int 1)",
	} {
		if _, err := lex(ok); err != nil {
			t.Fatalf("valid UTF-8 source was refused, so this test proves nothing: %q: %v", ok, err)
		}
	}

	// Each vector is a DIFFERENT way to be invalid, because a check that only
	// catches bare 0xff would pass a suite built from bare 0xff.
	for _, bad := range []struct{ why, src string }{
		{"a continuation byte alone", "(defn f [] [(x Int)] Str \"A\x80B\")"},
		{"an invalid start byte", "(defn f [] [(x Int)] Str \"A\xffB\")"},
		{"a truncated 2-byte sequence", "(defn f [] [(x Int)] Str \"A\xc3\")"},
		{"a truncated 3-byte sequence", "(defn f [] [(x Int)] Str \"A\xe2\x9c\")"},
		{"an overlong encoding of '/'", "(defn f [] [(x Int)] Str \"A\xc0\xafB\")"},
		{"a surrogate half, CESU-8 style", "(defn f [] [(x Int)] Str \"A\xed\xa0\x80B\")"},
		{"malformed bytes outside a literal", "(defn f\xff [] [(x Int)] Int 1)"},
		{"malformed bytes in a comment", "; \xff\n(defn f [] [(x Int)] Int 1)"},
	} {
		if _, err := lex(bad.src); err == nil {
			t.Errorf("%s was accepted; it must be refused, because U+FFFD substitution "+
				"here would put a codepoint the source does not contain into the hash", bad.why)
		}
	}
}

// The claim is not merely "malformed sources are refused" but that the COLLAPSE
// is gone: before the fix these four differed only in one byte and produced one
// hash. Asserting refusal one vector at a time cannot see that, because it never
// compares two of them.
func TestDistinctMalformedSourcesNoLongerCollapseToOneDefinition(t *testing.T) {
	tmpl := "(defn collide [] [(x Int)] Str \"A%sB\" (prop p [] (== (collide 0) (collide 0))))"
	for _, b := range []string{"\xff", "\xfe", "\xc0", "\x80"} {
		if _, err := lex(strings.Replace(tmpl, "%s", b, 1)); err == nil {
			t.Fatalf("byte %q still parses; before #133 all four of these hashed to "+
				"af78b48ff413, so accepting any one of them reopens the collapse", b)
		}
	}
	// And the control: the same shape with a valid character is a real definition.
	if _, err := lex(strings.Replace(tmpl, "%s", "é", 1)); err != nil {
		t.Fatalf("the valid-character control was refused, so the rejections above "+
			"may be about the template rather than the bytes: %v", err)
	}
}

// READING A SOURCE FILE IS ITS OWN BOUNDARY, distinct from parsing one.
//
// `lex` covers every reader that ELABORATES locally. `put --remote` does not:
// it reads the file, hands the string to json.Marshal, and ships it — and
// json.Marshal substitutes U+FFFD on the way out, so the registry receives
// well-formed JSON and every check on the far side is looking at repaired bytes.
// The same is true of `find --spec` and `find --implies` against a remote.
//
// The lossy step is []byte -> string, common to all of them, which is why the
// guard is at the read. This test is written against readSourceFile rather than
// against any one command so that a NEW command reading a .oath file is covered
// by construction rather than by remembering.
func TestReadSourceFileRefusesMalformedBytes(t *testing.T) {
	dir := t.TempDir()

	// CONTROL: a file with real multibyte text must round-trip byte-for-byte.
	good := filepath.Join(dir, "good.oath")
	want := "(defn f [] [(x Int)] Str \"café ✓ 🔒\")\n"
	if err := os.WriteFile(good, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readSourceFile(good); got != want {
		t.Fatalf("control: read %q, want %q", got, want)
	}

	// The refusal calls fail(), which exits — so it is exercised in a subprocess,
	// the same shape the other exit-path tests in this package use.
	bad := filepath.Join(dir, "bad.oath")
	if err := os.WriteFile(bad, []byte("(defn f [] [(x Int)] Str \"A\xffB\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OATH_TEST_READ_SOURCE") != "" {
		readSourceFile(os.Getenv("OATH_TEST_READ_SOURCE"))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestReadSourceFileRefusesMalformedBytes")
	cmd.Env = append(os.Environ(), "OATH_TEST_READ_SOURCE="+bad)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("readSourceFile accepted malformed bytes; `put --remote` would then "+
			"marshal them to U+FFFD and publish: %s", out)
	}
	if !strings.Contains(string(out), "not valid UTF-8") {
		t.Errorf("refused, but not for the declared reason: %s", out)
	}
}

// ---------- the capability boundary, in both backends ----------

// A REQUIRED VALUE (#126) is the fifth ingestion site, and the one this change
// first missed — worth a comment because of HOW it was missed rather than that
// it was.
//
// The site list was derived from the goProviders map, which is the emitter's own
// decomposition of the problem; required values are provisioned by a different
// mechanism and so were invisible to that enumeration, while being no less a
// path from external octets to a Str. The universe had to come from the CLAIM —
// every way bytes from outside become a Str — and reading it off the
// implementation produced a check that was complete with respect to a list
// rather than with respect to the property.
//
// It refuses through the PROVISION channel, so the program never starts:
// missing, empty and malformed are three ways the host failed to supply the
// value, and an operator should see one framing for all three.
func TestMalformedRequiredValueRefusesLaunch(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn main-secret [] [(w {token Str}) (args (List Str))] Str
		(. w token))`)
	markVerified(t, st, "main-secret")

	bins := map[string]string{}
	goBin, _ := buildProgram(t, st, "main-secret")
	bins["go-emit"] = goBin
	if _, err := exec.LookPath("clang"); err == nil {
		bins["llvm-ir"] = buildLLVM(t, st, "main-secret")
	} else if os.Getenv("CI") != "" {
		t.Fatal("clang is absent, so only one backend was checked")
	}

	for backend, bin := range bins {
		// CONTROL: a well-formed value must still launch and reach the entry.
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), "OATH_VALUE_TOKEN=café")
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimRight(string(out), "\n") != "café" {
			t.Fatalf("%s: control failed, so the refusal below proves nothing: %q %v", backend, out, err)
		}

		cmd = exec.Command(bin)
		cmd.Env = append(os.Environ(), "OATH_VALUE_TOKEN=\xffbad")
		out, err = cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: launched with a malformed required value and printed %q", backend, out)
			continue
		}
		if code := cmd.ProcessState.ExitCode(); code != 70 {
			t.Errorf("%s: refused with exit %d, want 70", backend, code)
		}
		// The framing matters as much as the refusal: this is a provisioning
		// failure, not a mid-run abort.
		if !strings.Contains(string(out), "cannot provide required capability") {
			t.Errorf("%s: refused, but not through the provision channel: %q", backend, out)
		}
	}
}

// INGESTION, not decode — and only this test can tell them apart.
//
// The test below asserts exit 70 and a message mentioning UTF-8, which BOTH
// dispositions satisfy: llvm-ir/1 refused malformed storage at decode long
// before this change, with its own 70 and its own "not valid UTF-8". Deleting
// that backend's ingestion check left the whole suite green, because every
// program there happened to match on the value it read.
//
// The distinguishing observation is a program that READS the value and never
// matches on it. Validating at ingestion refuses it; refusing at decode runs it
// to completion and prints bytes that are not text — which is the substitution
// this issue exists to remove, merely relocated to stdout.
func TestMalformedCapabilityBytesAreRefusedEvenWhenNeverDecoded(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn envecho [] [(w {env (-> Str Str)}) (args (List Str))] Str
		((. w env) "OATH_STR_BOUNDARY_TEST"))`)
	markVerified(t, st, "envecho")

	bins := map[string]string{}
	goBin, _ := buildProgram(t, st, "envecho")
	bins["go-emit"] = goBin
	if _, err := exec.LookPath("clang"); err == nil {
		bins["llvm-ir"] = buildLLVM(t, st, "envecho")
	} else if os.Getenv("CI") != "" {
		t.Fatal("clang is absent, so the backend this test was written for did not run")
	}

	for backend, bin := range bins {
		// CONTROL: the same program must pass valid text straight through, or
		// the refusal below would only be evidence that the program is broken.
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), "OATH_STR_BOUNDARY_TEST=café")
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimRight(string(out), "\n") != "café" {
			t.Fatalf("%s: control failed, so this test proves nothing: %q %v", backend, out, err)
		}

		cmd = exec.Command(bin)
		cmd.Env = append(os.Environ(), "OATH_STR_BOUNDARY_TEST=\xff\xfeA")
		out, err = cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: a value that is never matched on reached the program and printed %q; "+
				"the boundary must refuse it on the way IN, not when something happens to decode it",
				backend, out)
			continue
		}
		if code := cmd.ProcessState.ExitCode(); code != 70 {
			t.Errorf("%s: refused with exit %d, want 70", backend, code)
		}
	}
}

// Both backends must refuse malformed bytes AT INGESTION, identically. The
// history this pins: go-emit/2 used to decode 0xff as U+FFFD, consume one byte
// and exit 0 while llvm-ir/1 exited 70 on the same program and input — two
// lowerings of one program disagreeing on whether it ran at all.
//
// The U+FFFD case is the discriminating one and is not decoration. A validly
// encoded U+FFFD is ordinary text; utf8.DecodeRuneInString reports it as
// (RuneError, 3) while malformed input is (RuneError, 1). A check written on the
// rune alone — the obvious way to write it — refuses legitimate text, and only
// this vector can tell the two implementations apart.
func TestBothBackendsRefuseMalformedCapabilityBytesAtIngestion(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn envtail [] [(w {env (-> Str Str)}) (args (List Str))] Str
		(match ((. w env) "OATH_STR_BOUNDARY_TEST")
			((SNil) "empty")
			((SCons c rest) rest)))`)
	markVerified(t, st, "envtail")

	goBin, _ := buildProgram(t, st, "envtail")
	bins := map[string]string{"go-emit": goBin}
	if _, err := exec.LookPath("clang"); err == nil {
		bins["llvm-ir"] = buildLLVM(t, st, "envtail")
	} else if os.Getenv("CI") != "" {
		t.Fatal("clang is absent, so only one backend was checked and the " +
			"cross-backend claim did not run")
	}

	cases := []struct {
		name    string
		value   string
		wantOut string // "" means: expect refusal
	}{
		{"malformed: invalid start byte", "\xff\xfeA", ""},
		{"malformed: lone continuation", "\x80abc", ""},
		{"malformed: truncated 3-byte", "\xe2\x9c", ""},
		{"valid ascii", "abc", "bc"},
		{"valid 2-byte", "café", "afé"},
		// The vector that discriminates sz<=1 from r==RuneError.
		{"valid ENCODED U+FFFD", "�tail", "tail"},
	}

	for backend, bin := range bins {
		for _, tc := range cases {
			cmd := exec.Command(bin)
			cmd.Env = append(os.Environ(), "OATH_STR_BOUNDARY_TEST="+tc.value)
			out, err := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()

			if tc.wantOut == "" {
				if err == nil {
					t.Errorf("%s/%s: accepted malformed bytes and printed %q; it must refuse "+
						"rather than substitute U+FFFD", backend, tc.name, out)
					continue
				}
				if code != 70 {
					t.Errorf("%s/%s: refused with exit %d, want 70 — a supervisor should not "+
						"need to know which backend compiled the artifact", backend, tc.name, code)
				}
				if !strings.Contains(string(out), "not valid UTF-8") {
					t.Errorf("%s/%s: refused, but not for the declared reason: %q",
						backend, tc.name, out)
				}
				continue
			}

			if err != nil {
				t.Errorf("%s/%s: refused VALID text (%q); the check is over-broad: %v — %s",
					backend, tc.name, tc.value, err, out)
				continue
			}
			if got := strings.TrimRight(string(out), "\n"); got != tc.wantOut {
				t.Errorf("%s/%s: printed %q, want %q", backend, tc.name, got, tc.wantOut)
			}
		}
	}
}

// ARGV IS AN INGESTION SITE AND IT WAS UNWITNESSED.
//
// Every test above reaches Str through a CAPABILITY — env, a required value —
// and argv reaches it through a different mechanism: the backend builds the
// (List Str) from the process's own arguments before the entry is applied.
// That is the same shape as the required-value miss recorded above: a site list
// derived from the emitter's capability decomposition cannot see a path that is
// not a capability.
//
// SCOPE, because this test names one backend where the tests above name two:
// what it witnesses is the llvm-ir door. Replacing o_str_host with o_str in
// o_argv — deleting that backend's argv guard outright — leaves the entire
// `oath` package suite GREEN, so nothing in this repository observed the
// crossing. go-emit builds argv through a DIFFERENT mechanism and is witnessed
// by the test below, not by this one; they are two doors, measured separately.
//
// The program deliberately NEVER DECODES the value it is handed — it returns
// the head of argv, and o_print writes a Str's bytes out without stepping
// through them. So a guard at decode is invisible to this test and a guard at
// ingestion is not, which is the distinction the env test above had to make for
// the same reason. Under the mutant these vectors print their malformed bytes
// to stdout and exit 0.
func TestLLVMRefusesMalformedArgvAtIngestion(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn argecho [] [(args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, st, "argecho")
	bin := buildLLVM(t, st, "argecho")

	// CONTROL FIRST: if valid text does not survive the crossing, every refusal
	// below is evidence about a broken program rather than about the boundary.
	for _, ok := range []struct{ name, value string }{
		{"ascii", "plain"},
		{"empty", ""},
		{"2-byte", "café"},
		{"4-byte astral", "lock 🔒"},
		// A VALIDLY ENCODED U+FFFD. The obvious spelling of this check — refuse
		// whatever decodes to RuneError — refuses this legitimate text, and only
		// this vector separates the two implementations.
		{"encoded U+FFFD", "�tail"},
	} {
		out, err := exec.Command(bin, ok.value).CombinedOutput()
		if err != nil {
			t.Fatalf("control %s: valid argv %q was refused: %v — %s", ok.name, ok.value, err, out)
		}
		if got := strings.TrimRight(string(out), "\n"); got != ok.value {
			t.Fatalf("control %s: argv %q came back as %q", ok.name, ok.value, got)
		}
	}

	// Each vector is a DIFFERENT way to be malformed, because a check written
	// against bare 0xff passes a suite built from bare 0xff.
	for _, bad := range []struct{ name, value string }{
		{"invalid start byte", "\xff\xfeA"},
		{"lone continuation", "\x80abc"},
		{"truncated 3-byte", "\xe2\x9c"},
		{"overlong '/'", "\xc0\xaf"},
		{"surrogate half, CESU-8 style", "\xed\xa0\x80"},
	} {
		cmd := exec.Command(bin, bad.value)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: malformed argv reached the program, which printed %q; argv must be "+
				"refused on the way IN, not when something happens to decode it", bad.name, out)
			continue
		}
		if code := cmd.ProcessState.ExitCode(); code != 70 {
			t.Errorf("%s: refused with exit %d, want 70", bad.name, code)
		}
		if !strings.Contains(string(out), "not valid UTF-8") {
			t.Errorf("%s: refused, but not for the declared reason: %q", bad.name, out)
		}
		// The diagnostic must name WHICH argument. Relocating the refusal from
		// decode to ingestion is what buys that provenance, and a message that
		// only says "a Str" would be the decode-time refusal wearing this
		// test's expectations.
		if !strings.Contains(string(out), "command-line argument 1") {
			t.Errorf("%s: refusal does not name the offending argument: %q", bad.name, out)
		}
	}

	// Not only argv[1]: the index carried into the diagnostic is computed per
	// argument, so a later one must be reported as itself.
	cmd := exec.Command(bin, "fine", "\xffbad")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("malformed argv[2] was accepted and printed %q", out)
	} else if !strings.Contains(string(out), "command-line argument 2") {
		t.Errorf("malformed argv[2] refused, but reported as: %q", out)
	}
}

// THE SECOND ARGV DOOR. go-emit's guard was measured unwitnessed BEFORE this
// test existed, and by a separate mutation: deleting the oathStrFromHost call
// from the main the Go backend emits left the whole package suite green, with
// the same before/after split on a real binary (exit 70 naming the argument,
// against exit 0 printing the malformed bytes). That measurement is on the
// issue; it is not re-derived here, and this test is what would now notice.
//
// TWO DOORS, NOT ONE TEST OVER TWO BACKENDS, which is why this is written out
// rather than folded into a loop with the LLVM case. The tests further up loop
// over backends because one capability mechanism is lowered two ways. argv is
// not that: each backend BUILDS the (List Str) itself, in its own emitted main,
// through a guard of its own — a C runtime call in one and a Go call in the
// other. A shared loop would suggest a shared crossing and quietly imply that
// mutating either one is covered by the pair.
//
// Same discriminator, and it is load-bearing here too: the program returns the
// head of argv and never steps through it, so go-emit's DECODE guard
// (oathStrHead) is never reached and only the INGESTION guard can account for a
// refusal. A program that matched on the value would pass against either
// disposition.
//
// WHICH HALF OF THE ASSERTION DISCRIMINATES CHANGED WITH THE #167 WORK, AND THE
// ASSERTION DID NOT. Decode used to panic and ingestion used to exit 70, so the
// STATUS alone told them apart; both now refuse through the one door, so the
// status no longer does and the MESSAGE does — decode says "a Str holds bytes",
// ingestion names the argument. That distinction was already asserted below
// (and had to be: a status shared with every other refusal was never enough on
// its own), so the pair still pins ingestion exactly as tightly as before.
func TestGoBackendRefusesMalformedArgvAtIngestion(t *testing.T) {
	st := llvmStore(t)
	put(t, st, `(defn argecho [] [(args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, st, "argecho")
	bin, _ := buildProgram(t, st, "argecho")

	// CONTROL FIRST: if valid text does not survive the crossing, every refusal
	// below is evidence about a broken program rather than about the boundary.
	for _, ok := range []struct{ name, value string }{
		{"ascii", "plain"},
		{"empty", ""},
		{"2-byte", "café"},
		{"4-byte astral", "lock 🔒"},
		// A VALIDLY ENCODED U+FFFD: utf8.ValidString admits it and the rune-only
		// spelling of this check would not, so it is the vector that tells a
		// correct implementation from the obvious wrong one.
		{"encoded U+FFFD", "�tail"},
	} {
		out, err := exec.Command(bin, ok.value).CombinedOutput()
		if err != nil {
			t.Fatalf("control %s: valid argv %q was refused: %v — %s", ok.name, ok.value, err, out)
		}
		if got := strings.TrimRight(string(out), "\n"); got != ok.value {
			t.Fatalf("control %s: argv %q came back as %q", ok.name, ok.value, got)
		}
	}

	// Each vector is a DIFFERENT way to be malformed, because a check written
	// against bare 0xff passes a suite built from bare 0xff.
	for _, bad := range []struct{ name, value string }{
		{"invalid start byte", "\xff\xfeA"},
		{"lone continuation", "\x80abc"},
		{"truncated 3-byte", "\xe2\x9c"},
		{"overlong '/'", "\xc0\xaf"},
		{"surrogate half, CESU-8 style", "\xed\xa0\x80"},
	} {
		cmd := exec.Command(bin, bad.value)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: malformed argv reached the program, which printed %q; argv must be "+
				"refused on the way IN, not when something happens to decode it", bad.name, out)
			continue
		}
		// 70 specifically, and not merely non-zero. Since the #167 work this no longer
		// separates ingestion from decode — both refuse with 70 — but it is
		// still the artifact's refusal contract, and "any failure" would admit a
		// crash. The naming assertions below are what place the refusal.
		if code := cmd.ProcessState.ExitCode(); code != 70 {
			t.Errorf("%s: refused with exit %d, want 70 — a supervisor should not need to "+
				"know which backend compiled the artifact", bad.name, code)
		}
		if !strings.Contains(string(out), "not valid UTF-8") {
			t.Errorf("%s: refused, but not for the declared reason: %q", bad.name, out)
		}
		if !strings.Contains(string(out), "command-line argument 1") {
			t.Errorf("%s: refusal does not name the offending argument: %q", bad.name, out)
		}
	}

	// Not only argv[1]: the index carried into the diagnostic is computed per
	// argument, so a later one must be reported as itself.
	cmd := exec.Command(bin, "fine", "\xffbad")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("malformed argv[2] was accepted and printed %q", out)
	} else if !strings.Contains(string(out), "command-line argument 2") {
		t.Errorf("malformed argv[2] refused, but reported as: %q", out)
	}
}

// readfile IS A THIRD MECHANISM, AND ITS OCTETS ARE THE LEAST CONSTRAINED OF
// ANY ADMIT SITE.
//
// argv and env arrive from a process launch; a FILE is arbitrary bytes chosen
// by whoever wrote it, so this is the crossing most likely to meet input that
// was never meant to be text. It was nonetheless unwitnessed: replacing the
// guarded result with `string(b)` in the Go backend's file_read provider —
// nothing else changed — leaves the entire `oath` package suite GREEN, while
// the same program goes from exit 70 naming the path to exit 0 printing the
// file's malformed bytes. Measured, not inferred.
//
// The program returns the read result WITHOUT DECODING IT, for the reason the
// two argv tests give: a guard at decode is invisible to a program that never
// steps through the value, so only a guard at ingestion can account for a
// refusal here.
//
// SCOPE: go-emit only. The LLVM backend has its own file_read provider with its
// own guard, on the same claim and a different mechanism; it is not witnessed
// by this test.
func TestGoBackendRefusesMalformedFileContentsAtIngestion(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn readit [] [(w {readfile (-> Str Str)}) (args (List Str))] Str
		(match args ((Nil) "no path") ((Cons p t) ((. w readfile) p))))`)
	markVerified(t, st, "readit")
	bin, _ := buildProgram(t, st, "readit")
	dir := t.TempDir()

	// The filename is generated rather than taken from the case label: a label
	// is prose ("overlong '/'") and a path is not, and deriving one from the
	// other made this test fail on its own fixture setup rather than on the
	// property. Distinct paths per case also give the diagnostic assertion
	// below something specific to match.
	n := 0
	write := func(content string) string {
		t.Helper()
		n++
		p := filepath.Join(dir, "case"+strconv.Itoa(n))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// CONTROL FIRST: if valid contents do not survive the crossing, every
	// refusal below is evidence about a broken program rather than a boundary.
	for _, ok := range []struct{ name, content string }{
		{"ascii", "plain"},
		{"empty", ""},
		{"2-byte", "café"},
		{"4-byte astral", "lock 🔒"},
		// A VALIDLY ENCODED U+FFFD, which the rune-only spelling of this check
		// would refuse as though it were malformed storage.
		{"encoded U+FFFD", "�tail"},
		// NUL IS U+0000, AN ORDINARY SCALAR. A file carrying one is text, and
		// this crossing is where that distinction is easiest to get wrong: the
		// value comes from os.ReadFile, so nothing has truncated it at a NUL,
		// and the validator must not treat it as a malformed byte.
		{"NUL is a scalar", "before\x00after"},
	} {
		out, err := exec.Command(bin, write(ok.content)).CombinedOutput()
		if err != nil {
			t.Fatalf("control %s: valid contents were refused: %v — %s", ok.name, err, out)
		}
		if got := strings.TrimRight(string(out), "\n"); got != ok.content {
			t.Fatalf("control %s: contents %q came back as %q", ok.name, ok.content, got)
		}
	}

	// A MISSING FILE IS NOT A TEXT PROBLEM. The provider answers the capability
	// failure sentinel and the program runs on; folding that into the UTF-8
	// refusal would report a read error as though the bytes were malformed.
	cmd := exec.Command(bin, filepath.Join(dir, "no-such-file"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("a missing file exited %d: %q — read failure and malformed text are "+
			"different answers and must not share an exit path", cmd.ProcessState.ExitCode(), out)
	}

	// Each vector is a DIFFERENT way to be malformed, because a check written
	// against bare 0xff passes a suite built from bare 0xff.
	for _, bad := range []struct{ name, content string }{
		{"invalid start byte", "A\xff\xfeB"},
		{"lone continuation", "\x80abc"},
		{"truncated 3-byte", "A\xe2\x9c"},
		{"overlong '/'", "\xc0\xaf"},
		{"surrogate half, CESU-8 style", "\xed\xa0\x80"},
	} {
		path := write(bad.content)
		cmd := exec.Command(bin, path)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: malformed file contents reached the program, which printed %q",
				bad.name, out)
			continue
		}
		// 70 specifically, and not merely non-zero. Since the #167 work the decode guard
		// exits 70 too, so what places this refusal at INGESTION is the
		// path-naming assertion below rather than the status.
		if code := cmd.ProcessState.ExitCode(); code != 70 {
			t.Errorf("%s: refused with exit %d, want 70", bad.name, code)
		}
		if !strings.Contains(string(out), "not valid UTF-8") {
			t.Errorf("%s: refused, but not for the declared reason: %q", bad.name, out)
		}
		// WHICH file. Relocating the refusal to ingestion is what buys the
		// provenance; a message naming only "a Str" would be the decode-time
		// refusal satisfying this test's other expectations.
		if !strings.Contains(string(out), "the contents of "+path) {
			t.Errorf("%s: refusal does not name the file it read: %q", bad.name, out)
		}
	}
}

// THE FOURTH DOOR: the same file_read claim, the other backend's mechanism.
//
// llvm-ir does not share go-emit's provider. It reads the file in its own C
// runtime and admits the bytes through o_strn_host, so the guard measured in
// the Go provider says nothing about this one — and this one was equally
// unwitnessed: replacing o_strn_host with the unguarded o_strn, nothing else
// changed, leaves the entire `oath` package suite GREEN while a program reading
// malformed bytes goes from exit 70 naming the path to exit 0 printing them.
//
// LENGTH-AWARE ON BOTH SIDES OF THE MUTATION, deliberately. o_strn takes an
// explicit length, so the mutant still preserves NUL and still returns the whole
// file; the ONLY thing it drops is validation. A mutation to o_str would have
// changed two things at once, and a test that then failed could not say which.
func TestLLVMRefusesMalformedFileContentsAtIngestion(t *testing.T) {
	requireClang(t)
	st := llvmStore(t)
	put(t, st, `(defn readit [] [(w {readfile (-> Str Str)}) (args (List Str))] Str
		(match args ((Nil) "no path") ((Cons p t) ((. w readfile) p))))`)
	markVerified(t, st, "readit")
	bin := buildLLVM(t, st, "readit")
	dir := t.TempDir()

	n := 0
	write := func(content string) string {
		t.Helper()
		n++
		p := filepath.Join(dir, "case"+strconv.Itoa(n))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// CONTROL FIRST: valid contents must survive the crossing byte for byte, or
	// every refusal below is evidence about a broken program.
	for _, ok := range []struct{ name, content string }{
		{"ascii", "plain"},
		{"empty", ""},
		{"2-byte", "café"},
		{"4-byte astral", "lock 🔒"},
		{"encoded U+FFFD", "�tail"},
		// NUL IS U+0000 AND THE FILE IS TEXT. This backend packs a Str as a
		// buffer plus a length precisely so a NUL is data rather than a
		// terminator, and the validator must agree: a C-string-shaped check
		// would stop at the NUL, validate "before", and admit whatever follows
		// it unread.
		{"NUL is a scalar", "before\x00after"},
	} {
		out, err := exec.Command(bin, write(ok.content)).CombinedOutput()
		if err != nil {
			t.Fatalf("control %s: valid contents were refused: %v — %q", ok.name, err, out)
		}
		if got := strings.TrimRight(string(out), "\n"); got != ok.content {
			t.Fatalf("control %s: contents %q came back as %q", ok.name, ok.content, got)
		}
	}

	// A MISSING FILE IS NOT A TEXT PROBLEM: the provider answers the capability
	// failure value and the program runs on. Asserting this also stops the test
	// from being satisfied by a backend that refuses everything.
	cmd := exec.Command(bin, filepath.Join(dir, "no-such-file"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("a missing file exited %d: %q — a read failure and malformed text are "+
			"different answers and must not share an exit path", cmd.ProcessState.ExitCode(), out)
	}

	for _, bad := range []struct{ name, content string }{
		{"invalid start byte", "A\xff\xfeB"},
		{"lone continuation", "\x80abc"},
		{"truncated 3-byte", "A\xe2\x9c"},
		{"overlong '/'", "\xc0\xaf"},
		{"surrogate half, CESU-8 style", "\xed\xa0\x80"},
		// MALFORMED AFTER A NUL. The NUL control above proves valid text with a
		// NUL is admitted; this proves the scan did not simply STOP there. A
		// C-string check passes both that control and every vector above it,
		// and only this one separates the two.
		{"malformed after a NUL", "ok\x00\xff\xfe"},
	} {
		path := write(bad.content)
		cmd := exec.Command(bin, path)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: malformed file contents reached the program, which printed %q",
				bad.name, out)
			continue
		}
		if code := cmd.ProcessState.ExitCode(); code != 70 {
			t.Errorf("%s: refused with exit %d, want 70 — a supervisor should not need to "+
				"know which backend compiled the artifact", bad.name, code)
		}
		if !strings.Contains(string(out), "not valid UTF-8") {
			t.Errorf("%s: refused, but not for the declared reason: %q", bad.name, out)
		}
		// WHICH file — and the expected text is TRUNCATED the way the runtime
		// truncates it. The C diagnostic formats the path with %.120s into a
		// fixed buffer, so asserting the raw path would pass here and fail
		// wherever the temp root is longer, which is a property of the machine
		// and not of the boundary.
		want := path
		if len(want) > 120 {
			want = want[:120]
		}
		if !strings.Contains(string(out), "the contents of "+want) {
			t.Errorf("%s: refusal does not name the file it read: %q", bad.name, out)
		}
	}
}

// THE FIFTH AND LAST ADMIT SITE, AND THE ONLY ONE WITH NO SECOND BACKEND.
//
// llvm-ir has no fetch crossing to witness: it REFUSES http_request by name at
// compile time, which is a supported-subset decision rather than a gap. Two
// backends may implement different subsets of one capability vocabulary, so the
// absence of a paired test here is the design and not an omission — and stating
// that is the difference between a claim about the language and a claim about
// one lowering.
//
// The bytes at this crossing are the least controlled of the five. argv and env
// come from whoever launched the process and a file from whoever wrote it; a
// RESPONSE BODY is chosen by a remote party the operator does not control, and
// may be chosen adversarially. It was nonetheless unwitnessed: replacing the
// guarded body with the raw string in the Go backend's http_request provider
// leaves the whole `oath` package suite GREEN, while a program reading a
// malformed body goes from exit 70 naming the URL to exit 0 printing the bytes.
//
// (SPEC §14.2 governs the OTHER direction — a Str built from an inbound request
// — and answers 400 without invoking the handler, because a remote party must
// not be able to end the process. Here the program is the client and the exit
// is its own, which is why these two dispositions differ without disagreeing.)
func TestGoBackendRefusesMalformedResponseBodyAtIngestion(t *testing.T) {
	st := capStore(t)
	put(t, st, `(defn fetchit [] [(w {fetch (-> Str Str)}) (args (List Str))] Str
		(match args ((Nil) "no url") ((Cons u t) ((. w fetch) u))))`)
	markVerified(t, st, "fetchit")
	bin, _ := buildProgram(t, st, "fetchit")

	// The body is keyed by path so one server answers every case and each
	// refusal has a DISTINCT url to name.
	bodies := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			t.Errorf("the program requested %q, which this test never registered", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		// Octet-stream, so nothing in the stack is entitled to transcode the
		// body on the way out and the bytes the test wrote are the bytes the
		// program reads.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	n := 0
	serve := func(content string) string {
		n++
		p := "/case" + strconv.Itoa(n)
		bodies[p] = content
		return srv.URL + p
	}

	// CONTROL FIRST: a valid body must reach the program byte for byte.
	for _, ok := range []struct{ name, content string }{
		{"ascii", "plain"},
		{"2-byte", "café"},
		{"4-byte astral", "lock 🔒"},
		{"encoded U+FFFD", "�tail"},
		{"NUL is a scalar", "before\x00after"},
		// An empty body is legitimate, and it is ALSO what the failure sentinel
		// looks like — the two are indistinguishable downstream by construction.
		// Asserted here so that equivalence is recorded rather than discovered.
		{"empty", ""},
	} {
		out, err := exec.Command(bin, serve(ok.content)).CombinedOutput()
		if err != nil {
			t.Fatalf("control %s: a valid response body was refused: %v — %q", ok.name, err, out)
		}
		if got := strings.TrimRight(string(out), "\n"); got != ok.content {
			t.Fatalf("control %s: body %q came back as %q", ok.name, ok.content, got)
		}
	}

	// A FAILED REQUEST IS NOT A TEXT PROBLEM. The provider answers the
	// capability failure value and the program runs on; folding that into the
	// UTF-8 refusal would report an unreachable host as malformed bytes.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	cmd := exec.Command(bin, deadURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("an unreachable server exited %d: %q — a request failure and a malformed "+
			"body are different answers and must not share an exit path",
			cmd.ProcessState.ExitCode(), out)
	}

	for _, bad := range []struct{ name, content string }{
		{"invalid start byte", "A\xff\xfeB"},
		{"lone continuation", "\x80abc"},
		{"truncated 3-byte", "A\xe2\x9c"},
		{"overlong '/'", "\xc0\xaf"},
		{"surrogate half, CESU-8 style", "\xed\xa0\x80"},
		{"malformed after a NUL", "ok\x00\xff\xfe"},
	} {
		url := serve(bad.content)
		cmd := exec.Command(bin, url)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: a malformed response body reached the program, which printed %q",
				bad.name, out)
			continue
		}
		if code := cmd.ProcessState.ExitCode(); code != 70 {
			t.Errorf("%s: refused with exit %d, want 70", bad.name, code)
		}
		if !strings.Contains(string(out), "not valid UTF-8") {
			t.Errorf("%s: refused, but not for the declared reason: %q", bad.name, out)
		}
		// WHICH REQUEST. A program may fetch many urls, so naming the one that
		// failed is worth more here than at any other crossing — and because
		// every case has its own path, a hardcoded or shared message cannot
		// satisfy this.
		if !strings.Contains(string(out), "the response body from "+url) {
			t.Errorf("%s: refusal does not name the request it read: %q", bad.name, out)
		}
	}
}
