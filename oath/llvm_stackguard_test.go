package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ---------- the stack guard ----------
//
// Oath recursion becomes C recursion in this backend, so a deep enough
// structure exhausts the process stack. Before the guard that was a SIGSEGV:
// exit 139, zero bytes on stdout and stderr. These tests hold three separate
// claims, and they fail for different reasons on purpose:
//
//	EVERY BODY IS GUARDED     an IR-level claim about what the emitter writes.
//	                          Needs no clang, so it still runs where the
//	                          backend cannot be exercised.
//	A STANDALONE PROGRAM      exit 70 and a diagnostic, instead of 139 and
//	REFUSES                   silence.
//	A HANDLER SURVIVES        SPEC 14.2 — "a remote party must not be able to
//	                          halt a host". This is a CONFORMANCE claim, not an
//	                          ergonomics one, and it is why the guard routes
//	                          through o_refused rather than exiting where it is
//	                          detected.

// oathBodyDefine matches the header of an emitted OATH function body. Both
// shapes an Oath body can take — a named def and a hoisted lambda — and nothing
// else: @o_resolve_caps and @main are runtime plumbing, emitted once, cannot
// appear in a recursive cycle, and are deliberately outside the claim.
var oathBodyDefine = regexp.MustCompile(`(?m)^define ptr (@[a-zA-Z0-9_.]+)\(ptr %env, ptr %arg\) \{`)

// TestLLVMStackGuardIsOnEveryEmittedBody is the EXCLUSIVITY control.
//
// The claim is "every Oath function this backend emits checks the stack before
// descending", so its universe is emitted Oath BODIES — not a list of emitter
// call sites. A list is the proxy population this repo keeps getting caught by:
// correct when written, silently incomplete when a third shape is added. This
// derives the population from the emitted MODULE, so a new emission path is
// covered the day it appears rather than the day someone remembers it.
func TestLLVMStackGuardIsOnEveryEmittedBody(t *testing.T) {
	st := llvmStore(t)
	// Deliberately varied, so the module contains BOTH define shapes: a
	// program with no lambda would pass this test while leaving emitLam's own
	// emission path unguarded.
	put(t, st, `(defn sg-len [] [(xs (List Int))] Int
	  (match xs ((Nil) 0) ((Cons y ys) (+ 1 (sg-len ys))))
	  (prop nil-is-zero [] (== (sg-len (Nil [Int])) 0)))`)
	put(t, st, `(defn sg-map [] [(f (-> Int Int)) (xs (List Int))] (List Int)
	  (match xs ((Nil) (Nil [Int])) ((Cons y ys) (Cons [Int] (f y) (sg-map f ys))))
	  (prop nil-maps-to-nil [(f (-> Int Int))] (== (sg-map f (Nil [Int])) (Nil [Int]))))`)
	put(t, st, `(defn sg-main [] [(args (List Str))] Str
	  (if (== 1 (sg-len (sg-map (fn [(n Int)] (+ n 1)) (Cons [Int] 1 (Nil [Int])))))
	      "one" "other")
	  (prop answers-one [(args (List Str))] (== (sg-main args) "one")))`)

	prog, err := planProgram(st, "sg-main")
	if err != nil {
		t.Fatalf("planProgram: %v", err)
	}
	ir, err := emitLLVM(st, prog)
	if err != nil {
		t.Fatalf("emitLLVM: %v", err)
	}

	bodies := oathBodyDefine.FindAllStringSubmatchIndex(ir, -1)
	if len(bodies) < 4 {
		// THE CONTROL ON THE INSTRUMENT ITSELF. If the regexp stops matching —
		// the define line is reformatted, the calling convention changes — this
		// test would find zero bodies, guard none of them, and pass. A count
		// floor makes that a failure instead. Four: three defs plus the lambda.
		t.Fatalf("found only %d emitted Oath bodies; this test cannot be "+
			"measuring what it claims. Has the define line changed shape?\n%s",
			len(bodies), firstLines(ir, 40))
	}

	var lams int
	for i, m := range bodies {
		name := ir[m[2]:m[3]]
		if strings.HasPrefix(name, "@lam_") {
			lams++
		}
		// The guard must be in the ENTRY block, before anything else can
		// recurse. Slice from this define to the next so a neighbouring
		// function's guard cannot satisfy this one.
		end := len(ir)
		if i+1 < len(bodies) {
			end = bodies[i+1][0]
		}
		body := ir[m[0]:end]
		head, _, _ := strings.Cut(body, "\n  br i1 ")
		if !strings.Contains(head, "load i64, ptr @o_stack_floor") ||
			!strings.Contains(head, "icmp ult i64") {
			t.Errorf("emitted Oath body %s has no stack guard in its entry "+
				"block. Every body must check before descending, or a "+
				"recursive cycle through this one exhausts the stack "+
				"silently:\n%s", name, firstLines(body, 12))
		}
		if !strings.Contains(body, "call void @o_stack_exhausted()") {
			t.Errorf("body %s never calls @o_stack_exhausted; its guard "+
				"cannot refuse", name)
		}
	}
	if lams == 0 {
		t.Fatal("no @lam_ body in the module, so emitLam's own emission path " +
			"was never exercised: this run does not witness the claim")
	}
}

func firstLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls = ls[:n]
	}
	return strings.Join(ls, "\n")
}

// sgDeepStore branches on ARGV, so one binary witnesses both directions.
//
// THE DEPTH CANNOT BE A CONSTANT IN THE BODY. Checking any property of a def
// that always recursed 4,000,000 deep makes the EVALUATOR descend that far, and
// the evaluator's own depth guard refuses first — so the def would never be
// verified and never reach a backend at all. Branching on argv keeps every
// property cheap to check while leaving the compiled artifact able to go deep
// on demand.
func sgDeepStore(t *testing.T) *Store {
	t.Helper()
	st := llvmStore(t)
	put(t, st, `(defn sg-down [] [(n Int)] Int
	  (if (< n 1) 0 (+ 1 (sg-down (- n 1))))
	  (prop zero-is-zero [] (== (sg-down 0) 0))
	  (prop one-is-one [] (== (sg-down 1) 1)))`)
	put(t, st, `(defn sg-deep [] [(args (List Str))] Str
	  (match args
	    ((Nil) "shallow")
	    ((Cons a rest) (if (== 0 (sg-down 4000000)) "zero" "deep")))
	  (prop no-args-is-shallow [] (== (sg-deep (Nil [Str])) "shallow")))`)
	return st
}

// TestLLVMDeepRecursionRefusesInsteadOfCrashing is the standalone half.
//
// THE BUDGET IS ASSERTED, NOT JUST THE REFUSAL. The message reports the budget
// it derived, so the test pins it to the rlimit it imposed — which is what
// distinguishes a guard that READ THE HOST from one carrying a compiled-in
// constant that happens to be in range. A constant would refuse just as loudly
// and be wrong on every other machine, and that is precisely the defect the
// evaluator's depth guard was once found to have.
func TestLLVMDeepRecursionRefusesInsteadOfCrashing(t *testing.T) {
	bin := buildLLVM(t, sgDeepStore(t), "sg-deep")

	const stackKB = 2048
	out, code := sgRunWithStack(t, bin, stackKB, "go-deep")
	if code == 139 || code == -1 {
		t.Fatalf("the artifact crashed (exit %d) instead of refusing; the "+
			"guard did not fire:\n%s", code, out)
	}
	if code != 70 {
		t.Fatalf("expected exit 70, the runtime-refusal code this backend "+
			"already uses for a zero divisor; got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "exhausted its stack budget") {
		t.Fatalf("exit 70 with no stack diagnostic — 70 is also the "+
			"provisioning-failure code, so without the message this test "+
			"cannot tell the two apart:\n%s", out)
	}
	// The imposed limit minus O_STACK_MARGIN (128 KB).
	want := fmt.Sprintf("%d bytes", stackKB*1024-131072)
	if !strings.Contains(out, want) {
		t.Errorf("the guard reported a budget that is not the one imposed "+
			"(wanted %q). A guard using a compiled-in constant rather than "+
			"getrlimit would still refuse here, and would be wrong "+
			"everywhere else:\n%s", want, out)
	}

	// THE OTHER DIRECTION, FROM THE SAME BINARY. A guard that refused
	// unconditionally would pass every assertion above. Same artifact, same
	// budget, only the argument differs — so nothing but depth can explain the
	// difference.
	out2, code2 := sgRunWithStack(t, bin, stackKB)
	if code2 != 0 || !strings.Contains(out2, "shallow") {
		t.Fatalf("the same binary with no argument must run normally under "+
			"the same budget, or the guard is refusing unconditionally: "+
			"exit %d\n%s", code2, out2)
	}
}

// TestLLVMStackGuardOnASmallStackStillRefuses is the direction-of-error control.
//
// A guard is only useful if its floor sits INSIDE the real stack. An earlier
// draft replaced any measured limit at or below twice the margin with a 4 MB
// fallback — so on a host with a small stack the floor landed megabytes past the
// end, the fault arrived before the guard, and the silent crash was back. The
// defect is invisible at the default 8 MB and appears only where the limit is
// small, which is why this test imposes one.
//
// It asserts the SHAPE, not a number: whatever budget the guard derives, it must
// refuse rather than crash, and the budget it reports must fit inside the
// imposed limit. A guard that reported more than the host gave it would be
// describing a stack that does not exist.
func TestLLVMStackGuardOnASmallStackStillRefuses(t *testing.T) {
	bin := buildLLVM(t, sgDeepStore(t), "sg-deep")
	// 256 KB AND BELOW, CHOSEN BY MEASUREMENT RATHER THAN BY FEEL. The broken
	// draft substituted the fallback only when the limit was at or below twice
	// the 128 KB margin, so a 512 KB or 1 MB stack — the sizes this test tried
	// first — sailed past it and the test passed against the defect it was
	// written for. Verified by reverting: at these limits it fails, at those it
	// does not.
	for _, stackKB := range []int{256, 128} {
		out, code := sgRunWithStack(t, bin, stackKB, "go-deep")
		if code == 139 || code == -1 {
			t.Fatalf("a %d KB stack crashed (exit %d) instead of refusing: the "+
				"guard's floor is outside the real stack, so the fault arrives "+
				"first and the guard never runs\n%s", stackKB, code, out)
		}
		if code != 70 || !strings.Contains(out, "exhausted its stack budget") {
			t.Fatalf("a %d KB stack did not produce the refusal (exit %d):\n%s",
				stackKB, code, out)
		}
		var got int
		if _, err := fmt.Sscanf(out[strings.Index(out, "budget of "):],
			"budget of %d bytes", &got); err != nil {
			t.Fatalf("could not read the reported budget back: %v\n%s", err, out)
		}
		if got >= stackKB*1024 {
			t.Errorf("under a %d KB stack the guard claimed a %d-byte budget, "+
				"which does not fit inside it; the floor is past the end",
				stackKB, got)
		}
		if got <= 0 {
			t.Errorf("under a %d KB stack the guard derived a %d-byte budget, "+
				"which would refuse every call", stackKB, got)
		}
	}
}

// TestLLVMStackGuardSurvivesALargeEnvironment is the startup-consumption
// control, and the invocation it uses is legal rather than exotic.
//
// argv and the environment block are laid down at the stack TOP before user code
// runs, so a constructor's frame is already below it. Subtracting a whole
// RLIMIT_STACK from that frame puts the floor past the end of the stack, and the
// bigger the environment the further past. A process started with a megabyte of
// environment then faults before the guard can fire — the silent crash back
// again, on an ordinary invocation.
//
// The control is the SAME binary and the SAME stack limit with a small
// environment: if both refuse, the environment is not what decides it.
// sgStartupPad is the shell fragment that builds a large startup block.
//
// THE PAYLOAD IS BUILT INSIDE THE SHELL, NOT EMBEDDED IN ITS ARGUMENT, and that
// is a portability requirement rather than tidiness: Linux caps a SINGLE exec
// string at 128 KiB (MAX_ARG_STRLEN), so a script carrying an inline 512 KiB
// payload fails execve before the shell starts. Go then returns an error that is
// not an *exec.ExitError, the harness reads that as -1, and the test reports an
// artifact crash that never happened — a false failure on Linux only.
//
// FOUR STRINGS OF 60 KiB, for the same reason: each is under the per-string cap,
// and the total (~240 KiB) is under the total cap, which Linux derives as
// RLIMIT_STACK/4 — 512 KiB at the 2 MiB limit these tests impose. It also
// exceeds the guard's 128 KiB margin, which is what makes the control fire.
const sgStartupPad = `p=$(head -c 61440 /dev/zero | tr "\000" p) || exit 112; `

// TestLLVMStackGuardSurvivesALargeEnvironment is the startup-consumption
// control, and the invocation it uses is legal rather than exotic.
//
// argv and the environment block are laid down at the stack TOP before user code
// runs, so a constructor's frame is already below it. Subtracting a whole
// RLIMIT_STACK from that frame puts the floor past the end of the stack, and the
// bigger the block the further past. A process started with a few hundred
// kilobytes of environment then faults before the guard can fire.
//
// WHAT THIS WITNESSES IS THE SCAN, not every part of it. The refinement of
// taking each string's END rather than its start can only be witnessed
// independently by a single string longer than the 128 KiB margin, which Linux
// forbids outright — so it is exercised here on hosts that permit it and is
// otherwise a correction in the safe direction: it can only move the measured
// top UP, never down.
func TestLLVMStackGuardSurvivesALargeEnvironment(t *testing.T) {
	bin := buildLLVM(t, sgDeepStore(t), "sg-deep")
	const stackKB = 2048
	// `env -i` so the pad is the whole environment and is therefore what sits
	// at the top. Appending to the inherited environment left some other
	// variable highest, and the test then passed against the very defect it
	// was written for.
	script := fmt.Sprintf("ulimit -s %d 2>/dev/null || exit 111; %s"+
		"exec env -i A=\"$p\" B=\"$p\" C=\"$p\" D=\"$p\" %q go-deep",
		stackKB, sgStartupPad, bin)
	out, code := sgShellRun(t, script, stackKB)
	sgWantRefusal(t, "a ~240 KiB environment", out, code)
}

// TestLLVMStackGuardSurvivesALargeArgv is the other half of the startup block.
//
// An EMPTY environment with a large argument vector has the same shape, and a
// guard that scans only the environment misses it entirely.
func TestLLVMStackGuardSurvivesALargeArgv(t *testing.T) {
	bin := buildLLVM(t, sgDeepStore(t), "sg-deep")
	const stackKB = 2048
	script := fmt.Sprintf("ulimit -s %d 2>/dev/null || exit 111; %s"+
		"exec env -i %q go-deep \"$p\" \"$p\" \"$p\" \"$p\"",
		stackKB, sgStartupPad, bin)
	out, code := sgShellRun(t, script, stackKB)
	sgWantRefusal(t, "a ~240 KiB argument vector with an empty environment", out, code)
}

func sgShellRun(t *testing.T, script string, stackKB int) (string, int) {
	t.Helper()
	b, err := exec.Command("sh", "-c", script).CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			// NOT read as a crash. A failure to even start the shell is a
			// harness problem, and reporting it as an artifact segfault is the
			// exact confusion this repo keeps finding in failure-path tests.
			t.Fatalf("could not run the probe at all (not an exit status): "+
				"%v\n%s", err, string(b))
		}
	}
	switch code {
	case 111:
		t.Skipf("this host cannot impose a %d KB stack, so nothing is pinned", stackKB)
	case 112:
		t.Skip("this host could not build the startup padding")
	}
	if strings.Contains(string(b), "too long") {
		t.Skipf("this host refused the startup block: %s", firstLines(string(b), 2))
	}
	return string(b), code
}

func sgWantRefusal(t *testing.T, what, out string, code int) {
	t.Helper()
	if code == 139 || code == -1 {
		t.Fatalf("with %s the artifact crashed (exit %d) instead of refusing: "+
			"the startup block sits above the constructor's frame, so the "+
			"floor was derived below the real stack boundary\n%s",
			what, code, firstLines(out, 3))
	}
	if code != 70 || !strings.Contains(out, "exhausted its stack budget") {
		t.Fatalf("with %s: expected the refusal, got exit %d\n%s",
			what, code, firstLines(out, 3))
	}
}

// sgRunWithStack runs a binary under an imposed stack rlimit.
//
// Through `sh -c 'ulimit -s N; exec BIN'` because Go has no portable way to set
// a CHILD's rlimit, and setting the test process's own would apply to every
// later test in the package. `exec` matters: without it the shell survives its
// child and writes its own "Segmentation fault" notice into the captured
// stream, which reads exactly like artifact output.
func sgRunWithStack(t *testing.T, bin string, stackKB int, args ...string) (string, int) {
	t.Helper()
	script := fmt.Sprintf("ulimit -s %d 2>/dev/null || exit 111; exec %q %s",
		stackKB, bin, strings.Join(args, " "))
	b, err := exec.Command("sh", "-c", script).CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	if code == 111 {
		t.Skipf("this host cannot impose a %d KB stack, so the guard's budget "+
			"cannot be pinned and the test would assert nothing", stackKB)
	}
	return string(b), code
}

// TestLLVMStackExhaustionInAHandlerIsA500AndKeepsServing is the SPEC 14.2 half,
// and the reason this work is a correctness fix rather than a better message.
//
// 14.2 answers 400 for an unrepresentable request field "because a remote party
// must not be able to halt a host". A body deep enough to exhaust the stack
// halted it: measured before the guard, the process died with no response and
// every later connection was refused. The obligation was already normative;
// this backend could not keep it.
//
// THE CONTROLS ARE THE TEST. A dead server and a server answering 500 are told
// apart only by what happens NEXT, so the shape is: a good request must
// succeed, then the deep one, then a good request must succeed AGAIN.
func TestLLVMStackExhaustionInAHandlerIsA500AndKeepsServing(t *testing.T) {
	st := llvmHandlerStore(t)
	// Recurses on the BODY, so depth is the remote party's choice — which is
	// exactly the shape 14.2 is about.
	put(t, st, `(defn sg-body-count [] [(bs (List Int))] Int
	  (match bs ((Nil) 0) ((Cons b rest) (+ 1 (sg-body-count rest))))
	  (prop nil-is-zero [] (== (sg-body-count (Nil [Int])) 0)))`)
	// The count reaches the STATUS through a comparison rather than the body.
	// Putting it in the body would refuse on any body over 255 octets, because
	// a response body is octets — measured, and it made the control 500 as
	// loudly as the attack did, which is a control that measures nothing.
	put(t, st, `(defn sg-deep-handler [] [(r Request)] Response
	  (match r
	    ((Req m p hs body ts)
	      (Resp (if (< (sg-body-count body) 0) 500 200)
	            (Nil [(Pair Str Str)]) (Nil [Int]))))
	  (prop always-200 [(r Request)]
	    (== (match (sg-deep-handler r) ((Resp s h b) s)) 200)))`)

	bin := buildLLVM(t, st, "sg-deep-handler")
	// THE SERVED PROCESS'S STACK IS PINNED, and an earlier draft did not pin it.
	// llvmServe starts the artifact with whatever limit the harness inherited,
	// so how deep 400,000 body octets can go was a property of the SHELL that
	// happened to run the suite. It passed under `go test` and failed under
	// `make ci-local` on the same machine, answering 200 because the ambient
	// stack was large enough. A test whose independent variable is set by its
	// environment is not measuring what it claims.
	addr, errs := sgServeWithStack(t, bin, 2048)

	post := func(n int) string {
		return fmt.Sprintf("POST / HTTP/1.1\r\nHost: h\r\nContent-Length: %d\r\n\r\n%s",
			n, strings.Repeat("x", n))
	}

	if code, _ := llvmSend(t, addr, post(500)); code != 200 {
		t.Fatalf("the CONTROL request did not succeed (got %d), so nothing "+
			"after it measures the guard", code)
	}
	// 400,000 body octets drive ~400,000 frames, decisively past the 2 MB
	// imposed above.
	code, _ := llvmSend(t, addr, post(400000))
	if code != 500 {
		t.Errorf("a body deep enough to exhaust the stack answered %d; SPEC "+
			"14.2 requires the host to survive a remote party's input, and "+
			"the refusal door answers 500 for every other runtime refusal", code)
	}
	if code, _ := llvmSend(t, addr, post(500)); code != 200 {
		t.Fatalf("the handler stopped serving after the deep request (got "+
			"%d). That is the 14.2 violation itself: before the guard the "+
			"process died here and every later connection was refused", code)
	}
	if !strings.Contains(errs.String(), "exhausted its stack budget") {
		t.Errorf("no stack diagnostic on stderr; the operator cannot tell "+
			"this 500 from any other refusal:\n%s", errs.String())
	}
}

// sgServeWithStack is llvmServe with an imposed stack rlimit.
//
// Through `sh -c 'ulimit -s N; exec BIN'` for the reason sgRunWithStack gives:
// Go cannot set a child's rlimit portably, and setting the test process's own
// would apply to every later test in the package. `exec` is what makes the
// shell become the artifact, so Process.Kill reaches the server rather than a
// surviving parent shell — without it the cleanup leaves an orphan holding the
// port.
func sgServeWithStack(t *testing.T, bin string, stackKB int) (string, *syncBuf) {
	t.Helper()
	addr := llvmFreeAddr(t)
	errs := &syncBuf{}
	script := fmt.Sprintf("ulimit -s %d 2>/dev/null || exit 111; exec %q", stackKB, bin)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+addr)
	cmd.Stderr = errs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// WAIT IN A GOROUTINE, so the exit status is available WITHOUT blocking.
	// An earlier draft read cmd.ProcessState after the dial loop, which only
	// Wait populates — so it was always nil and the skip below could never fire.
	// The test would then spend fifteen seconds and FAIL on precisely the
	// unsupported host it claims to tolerate: a branch that cannot execute is
	// indistinguishable from one that is wrong.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
			c.Close()
			return addr, errs
		}
		select {
		case err := <-done:
			// It exited instead of listening. Put it back so Cleanup's receive
			// still completes, then classify.
			done <- err
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 111 {
				t.Skipf("this host cannot impose a %d KB stack on the handler", stackKB)
			}
			t.Fatalf("the handler exited instead of listening (%v):\n%s", err, errs.String())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the handler never accepted a connection on %s.\nstderr:\n%s", addr, errs.String())
	return "", nil
}

// TestLLVMStackGuardCompilesOnEveryPlatformBranch covers the three
// preprocessor paths the guard's initialisation has, only one of which any other
// test in this package can reach.
//
//	native            glibc/dyld: the constructor receives (argc, argv, envp)
//	no __GLIBC__      musl-shaped: void(void) constructor, environ only, plus
//	  no __APPLE__    the startup allowance
//	_WIN32            no POSIX at all; getrlimit must not be reached, and the
//	                  budget falls back to a compiled-in constant
//
// It witnesses that each branch COMPILES, not that each behaves — the other two
// cannot be run on this host at all. That is a real limit and is why the
// branches are kept as small as they are.
func TestLLVMStackGuardCompilesOnEveryPlatformBranch(t *testing.T) {
	requireClang(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		flags []string
	}{
		{"native (glibc/dyld constructor ABI)", nil},
		{"musl-shaped (no documented constructor ABI)", []string{"-U__APPLE__", "-U__GLIBC__"}},
		{"windows (no POSIX)", []string{"-D_WIN32"}},
	} {
		args := append([]string{"-fsyntax-only"}, c.flags...)
		cmd := exec.Command("clang", append(args, "rt.c")...)
		cmd.Dir = dir
		b, err := cmd.CombinedOutput()
		if err == nil {
			continue
		}
		out := string(b)
		if strings.Contains(out, "o_stack") || strings.Contains(out, "environ") ||
			strings.Contains(out, "getrlimit") || strings.Contains(out, "RLIMIT_STACK") {
			t.Errorf("the stack guard does not compile on the %s branch:\n%s",
				c.name, firstLines(out, 8))
			continue
		}
		// Host headers under a forced -D_WIN32 can fail for reasons that have
		// nothing to do with this guard. Skipping on THOSE while failing on any
		// diagnostic that names the guard is what keeps this from being a test
		// that cannot fail.
		t.Logf("%s: unrelated host-header diagnostics, guard clean:\n%s",
			c.name, firstLines(out, 3))
	}
}

// TestLLVMStackGuardRuntimeCompilesWithoutPOSIX is the Windows control.
//
// getrlimit is inside the POSIX block, because the CLI entry links this runtime
// on Windows too and this project publishes a Windows binary. The guard must
// fall back to a compiled-in budget there rather than failing to build — and
// nothing else in the suite would notice, since every other test runs on a
// POSIX host.
func TestLLVMStackGuardRuntimeCompilesWithoutPOSIX(t *testing.T) {
	requireClang(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rt.c"), []byte(llvmRuntimeC), 0o644); err != nil {
		t.Fatal(err)
	}
	// -D_WIN32 takes the same branch the Windows build takes, without needing a
	// Windows toolchain. Not a full cross-compile — headers still come from
	// this host — so this witnesses the PREPROCESSOR path, which is where an
	// unguarded getrlimit would break the build.
	cmd := exec.Command("clang", "-fsyntax-only", "-D_WIN32", "rt.c")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(b), "getrlimit") || strings.Contains(string(b), "RLIMIT_STACK") {
			t.Fatalf("the stack guard reaches getrlimit on a non-POSIX "+
				"target; it must fall back to the compiled-in budget "+
				"there:\n%s", string(b))
		}
		t.Skipf("this host's headers do not survive -D_WIN32 for unrelated "+
			"reasons, so the guard's own branch could not be isolated:\n%s",
			firstLines(string(b), 6))
	}
}
