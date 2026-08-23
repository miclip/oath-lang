package main

import (
	"bytes"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	goprinter "go/printer"
	gotoken "go/token"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// THE ARTIFACT'S REFUSAL CONTRACT (#167).
//
// A compiled artifact reports a host-side refusal as ONE stderr line and exit
// 70, whichever backend produced it. Before this, the Go backend reported the
// runtime half by panicking — status 2, plus a goroutine dump naming the temp
// directory the program had been emitted into — while the LLVM backend exited
// 70 for the same conditions from the same source definition. A supervisor
// branching on status got a different answer per backend.
//
// TWO CLAIMS, and they need different instruments, which is why this file has
// two kinds of test rather than a list of conditions:
//
//   - EVERY host-side refusal takes that path. The universe is the emitted
//     runtime's own refusal sites, so the witness READS THE EMITTED PROGRAM and
//     derives them, rather than checking the conditions I happened to think of.
//     A refusal added later as a panic fails TestEmittedRuntimeHasOneRefusalPath
//     without anyone remembering to extend a list.
//   - The path does what it says end to end: status, message, and nothing else
//     on the way out. That one has to run a binary.

// classifiedBugPanics is the COMPLEMENT of the refusal contract, and it is
// written down here because "which panics are allowed" is a judgement that must
// be stated somewhere a reader can argue with.
//
// Both are conditions the checker makes unreachable: equality is only emitted at
// types it admits for equality (none of which contain a function), and a match
// is only emitted with one arm per constructor. Reaching either means THIS
// COMPILER is wrong — not that the host declined something — and a bug wants a
// stack trace, which is precisely what a refusal must not print.
//
// Adding an entry here is the deliberate act. Adding a panic to the emitted
// runtime without adding one is what the test catches.
var classifiedBugPanics = map[string]string{
	"structEq on function value":      "the checker admits no equality type containing a function",
	"non-exhaustive":                  "the checker requires one match arm per constructor",
	"byte list element is not an Int": "the checker types the argument (List Int)",
}

// emittedSources returns emitted Go programs covering both emitted main shapes
// and both provisioning states, so the scan below sees the launch gate as well
// as the runtime helpers. It does NOT invoke the Go toolchain: the claim is
// about the source this backend emits, and reading it is the cheaper witness.
func emittedSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}

	plain := capStore(t)
	put(t, plain, `(defn echo1 [] [(args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, plain, "echo1")
	prog, err := planProgram(plain, "echo1")
	if err != nil {
		t.Fatal(err)
	}
	src, err := emitProgram(plain, prog)
	if err != nil {
		t.Fatal(err)
	}
	out["standalone"] = src

	// EVERY CAPABILITY KIND, DERIVED FROM THE VOCABULARY RATHER THAN LISTED.
	//
	// Each kind contributes its own provider snippet to the emitted program, so
	// a program requiring only `readfile` leaves three of them unscanned — and a
	// termination added inside one of those would never be parsed. Building the
	// record from capabilityVocabulary means a NEW kind joins this universe by
	// construction; the assertion below is what makes that true rather than
	// hoped for.
	names := make([]string, 0, len(capabilityVocabulary))
	for n := range capabilityVocabulary {
		names = append(names, n)
	}
	sort.Strings(names)
	fields, body := "", "h"
	for _, n := range names {
		fields += n + " (-> Str Str) "
		body = "((. w " + n + ") " + body + ")"
	}

	capd := capStore(t)
	put(t, capd, `(defn needs [] [(w {`+fields+`secret Str}) (args (List Str))] Str
		(match args ((Nil) (. w secret)) ((Cons h t) `+body+`)))`)
	markVerified(t, capd, "needs")
	prog2, err := planProgram(capd, "needs")
	if err != nil {
		t.Fatal(err)
	}
	// THE DERIVATION, ASSERTED. Without this the loop above is just a longer
	// remembered list: a kind whose field name changed, or one the entry shape
	// cannot express, would drop out silently and the scan would be narrower
	// than it claims.
	got := map[capabilityKind]bool{}
	for _, r := range prog2.Requirements {
		got[r.Kind] = true
	}
	for kind := range capabilityKinds() {
		if !got[kind] {
			t.Fatalf("the provisioned witness does not require capability kind %q, so its "+
				"provider snippet is never scanned — this universe is narrower than it claims", kind)
		}
	}
	src2, err := emitProgram(capd, prog2)
	if err != nil {
		t.Fatal(err)
	}
	out["provisioned"] = src2

	// BOTH HANDLER SHAPES. The handler main is a different main and holds the
	// listen-failure exit and the per-request disposition; the capability-first
	// handler is a THIRD main, combining the launch gate with that adapter, and
	// it is the one an actual deployed app uses. A scan that saw only the
	// standalone main would be blind to exactly the sites the allowlist has to
	// reason about.
	hand := capStore(t)
	put(t, hand, `(data Pair [a b] (Pair a b))`)
	put(t, hand, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, hand, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)
	put(t, hand, `(defn hentry [] [(r Request)] Response
		(Resp 200 (Nil [(Pair Str Str)]) (match r ((Req m p h b t) b))))`)
	put(t, hand, `(defn hcaps [] [(w {readfile (-> Str Str) secret Str}) (r Request)] Response
		(Resp 200 (Nil [(Pair Str Str)]) (match r ((Req m p h b t) b))))`)
	for _, n := range []string{"hentry", "hcaps"} {
		markVerified(t, hand, n)
		prog3, err := planProgram(hand, n)
		if err != nil {
			t.Fatal(err)
		}
		src3, err := emitProgram(hand, prog3)
		if err != nil {
			t.Fatal(err)
		}
		out[n] = src3
	}
	return out
}

// EVERY REFUSAL THROUGH ONE DOOR, established by reading the emitted program
// rather than by trusting the emitter's decomposition.
//
// This is a PARSE, not a grep: `panic(` and `os.Exit(` inside a comment or a
// string literal are not calls, and a scan that could not tell the difference
// would have been satisfied by the very comments this change added.
func TestEmittedRuntimeHasOneRefusalPath(t *testing.T) {
	seenPanics := map[string]bool{}
	for shape, src := range emittedSources(t) {
		fset := gotoken.NewFileSet()
		f, err := goparser.ParseFile(fset, "main.go", src, goparser.ParseComments)
		if err != nil {
			t.Fatalf("%s: the emitted program does not parse, so this test observes nothing: %v", shape, err)
		}

		// CONTROL FIRST. If a termination authority is absent, every assertion
		// below is about a program that never had the path — the scan would
		// report "no violations" over an empty claim. All three are emitted
		// unconditionally, so all three must be here in every shape.
		fns := map[string]*goast.FuncDecl{}
		for _, d := range f.Decls {
			if fd, ok := d.(*goast.FuncDecl); ok && fd.Recv == nil {
				fns[fd.Name.Name] = fd
			}
		}
		for _, want := range []string{"oathRefuse", "oathExitOnRefusal", "oathDone", "oathListenFailed"} {
			if fns[want] == nil {
				t.Fatalf("%s: the emitted runtime has no %s, so there is no termination "+
					"authority to check against", shape, want)
			}
		}
		raise := src[fset.Position(fns["oathRefuse"].Body.Pos()).Offset:fset.Position(fns["oathRefuse"].Body.End()).Offset]
		if !strings.Contains(raise, "panic(oathRefusal{") {
			t.Errorf("%s: oathRefuse does not raise a refusal value, so no boundary can "+
				"dispose of one: %s", shape, raise)
		}
		dispose := src[fset.Position(fns["oathExitOnRefusal"].Body.Pos()).Offset:fset.Position(fns["oathExitOnRefusal"].Body.End()).Offset]
		if !strings.Contains(dispose, "os.Stderr") {
			t.Errorf("%s: the standalone disposition does not write to stderr: %s", shape, dispose)
		}
		if !strings.Contains(dispose, "os.Exit(oathExitRefusal)") {
			t.Errorf("%s: the standalone disposition does not exit through the named status: %s", shape, dispose)
		}
		// AND IT MUST BE INSTALLED. A disposer nothing defers is a function
		// that would satisfy every assertion above while the artifact printed a
		// goroutine dump — the exact output this change removes.
		if !strings.Contains(src, "defer oathExitOnRefusal()") {
			t.Errorf("%s: main never defers oathExitOnRefusal, so no refusal is ever disposed of", shape)
		}

		// WHICH FUNCTION a termination is in, not which NUMBER it passes.
		//
		// The first version allowed os.Exit(0) and os.Exit(1) ANYWHERE IN main,
		// which is the same defect it was written to catch, one level up: a
		// population described by the VALUES its members pass rather than by the
		// SITES that own them. A host refusal added to main would exit 1, bypass
		// the refusal path, and pass.
		//
		// So the emitted runtime has named authorities and main has no
		// termination of its own. A call inside a FUNCTION LITERAL is never at
		// an authority — none of them contains one — and treating it as though
		// it were its enclosing declaration is how a termination hidden in a
		// closure would inherit that declaration's permission.
		var lits []*goast.FuncLit
		goast.Inspect(f, func(n goast.Node) bool {
			if fl, ok := n.(*goast.FuncLit); ok {
				lits = append(lits, fl)
			}
			return true
		})
		site := func(p gotoken.Pos) string {
			for _, fl := range lits {
				if p >= fl.Pos() && p <= fl.End() {
					return "a function literal"
				}
			}
			for _, d := range f.Decls {
				fd, ok := d.(*goast.FuncDecl)
				if ok && fd.Recv == nil && p >= fd.Pos() && p <= fd.End() {
					return fd.Name.Name
				}
			}
			return "<top level>"
		}

		// PARENTHESES ARE NOT A DIFFERENT OPERATION. `(panic)(x)` and
		// `(os.Exit)(1)` are valid Go and put a ParenExpr where the callee is
		// expected, so a switch on call.Fun classifies neither and every
		// allowlist below stays green over a termination it never saw. Same
		// mistake as a gate that knew Term but not []Term: the claim is about
		// the CALL, and the syntax has more than one spelling of it.
		unparen := func(e goast.Expr) goast.Expr {
			for {
				pe, ok := e.(*goast.ParenExpr)
				if !ok {
					return e
				}
				e = pe.X
			}
		}

		type callSite struct{ fn, arg string }
		render := func(e goast.Expr) string {
			var b bytes.Buffer
			if err := goprinter.Fprint(&b, fset, e); err != nil {
				return "<unrenderable>"
			}
			return b.String()
		}

		panics := map[callSite]int{}
		exits := map[callSite]int{}
		goast.Inspect(f, func(n goast.Node) bool {
			call, ok := n.(*goast.CallExpr)
			if !ok {
				return true
			}
			switch fn := unparen(call.Fun).(type) {
			case *goast.Ident:
				if fn.Name != "panic" || len(call.Args) != 1 {
					return true
				}
				arg := render(call.Args[0])
				if lit, ok := call.Args[0].(*goast.BasicLit); ok && lit.Kind == gotoken.STRING {
					if unq, err := strconv.Unquote(lit.Value); err == nil {
						arg = unq
					}
				}
				panics[callSite{site(call.Pos()), arg}]++
			case *goast.SelectorExpr:
				pkg, ok := fn.X.(*goast.Ident)
				if !ok || pkg.Name != "os" || fn.Sel.Name != "Exit" || len(call.Args) != 1 {
					return true
				}
				exits[callSite{site(call.Pos()), render(call.Args[0])}]++
			}
			return true
		})

		// THE SELECTOR SCAN IS ONLY SOUND IF `os` MEANS os AND NOTHING ELSE CAN
		// END THE PROCESS. Both are properties of the emitted IMPORT SET, which
		// this backend computes, so they are cheap to check exactly — cheaper
		// and more direct than running go/types over a generated file.
		//
		// Without this the claim is "no unauthorized os.Exit in today's
		// spelling", not "no unauthorized termination": `import host "os"`,
		// log.Fatal and runtime.Goexit would each walk straight past.
		terminators := map[string]string{
			"log":     "log.Fatal and friends exit without passing an authority",
			"runtime": "runtime.Goexit ends the goroutine outside the contract",
			"syscall": "syscall.Exit bypasses os.Exit entirely",
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Errorf("%s: unparsable import %s", shape, imp.Path.Value)
				continue
			}
			if path == "os" && imp.Name != nil {
				t.Errorf("%s: os is imported as %q, so a selector scan for os.Exit no longer "+
					"sees every direct exit", shape, imp.Name.Name)
			}
			if why, bad := terminators[path]; bad {
				t.Errorf("%s: the emitted program imports %q — %s; if it is genuinely needed, "+
					"this scan has to learn that spelling before the import lands", shape, path, why)
			}
		}
		// os.Exit TAKEN AS A VALUE is the other way past a call-site scan:
		// `exit := os.Exit` moves the termination to a name this scan does not
		// know. Any mention of os.Exit that is not the callee of a call is one.
		goast.Inspect(f, func(n goast.Node) bool {
			call, ok := n.(*goast.CallExpr)
			if ok {
				if sel, isSel := unparen(call.Fun).(*goast.SelectorExpr); isSel {
					if id, isID := sel.X.(*goast.Ident); isID && id.Name == "os" && sel.Sel.Name == "Exit" {
						// The callee itself: already counted above.
						return false
					}
				}
			}
			if sel, isSel := n.(*goast.SelectorExpr); isSel {
				if id, isID := sel.X.(*goast.Ident); isID && id.Name == "os" && sel.Sel.Name == "Exit" {
					t.Errorf("%s: os.Exit is used as a VALUE at %s, not called — a termination "+
						"reached through an alias is outside this scan's reach",
						shape, fset.Position(sel.Pos()))
				}
			}
			return true
		})

		// EXACT SITES. Each authority may pass exactly one status, and nothing
		// else in the program may exit at all — including main, which ends
		// through oathDone or oathListenFailed rather than by itself.
		//
		// oathListenFailed is 1 and NOT the refusal status deliberately: it
		// happens after provisioning succeeded, and apps/github-webhook's
		// acceptance suite uses that 70-vs-1 difference as its control that
		// capabilities resolve BEFORE the port is bound.
		allowedExits := map[callSite]bool{
			{"oathExitOnRefusal", "oathExitRefusal"}: true,
			{"oathDone", "0"}:                        true,
			{"oathListenFailed", "1"}:                true,
			// oathExitResult is the PROGRAM's own disposition (#120 exit-result
			// protocol), the one authority whose status is NOT a fixed literal:
			// a (Fail Int Str) entry chose the code, clamped to [1,255]. That is
			// the feature, so this site is a variable rather than a constant. Its
			// SUCCESS arm still leaves through oathDone(0), so the only exit here
			// is the program-chosen failure code. A supervisor's contract is
			// therefore: 0 clean, 70 runtime refusal, 1 handler listen-fail, and
			// a program-chosen 1..255 via oathExitResult; a program that picks 70
			// or 1 overlaps a runtime status, which is the program's own call.
			{"oathExitResult", "c"}: true,
		}
		// THE RAISE AND THE RE-RAISE are the only panics whose operand is not a
		// classified literal, and each has exactly one home. oathRefuse raises a
		// refusal; oathRefusalOf re-raises whatever is NOT one, which is what
		// keeps a compiler-bug panic's stack and status intact through a
		// recover() that cannot be selective.
		allowedRaises := map[callSite]bool{
			{"oathRefuse", "oathRefusal{string(b)}"}: true,
			{"oathRefusalOf", "r"}:                   true,
		}
		for s, n := range exits {
			if !allowedExits[s] {
				t.Errorf("%s: %s calls os.Exit(%s) %d time(s) — every termination must leave "+
					"through one of the named authorities, or a supervisor reads two contracts "+
					"from one backend", shape, s.fn, s.arg, n)
			}
		}
		for s, n := range panics {
			if allowedRaises[s] {
				continue
			}
			if _, ok := classifiedBugPanics[s.arg]; ok {
				continue
			}
			t.Errorf("%s: %s panics with %q %d time(s), which is neither the refusal raise nor a "+
				"classified compiler-bug condition. If it is a HOST refusal it must go through "+
				"oathRefuse (one line, exit %d); if it is unreachable unless this compiler is "+
				"wrong, add it to classifiedBugPanics with the reason",
				shape, s.fn, s.arg, n, exitHostRefusal)
		}
		// AND NEITHER ALLOWLIST MAY BE VACUOUS. Every one of these sites is in a
		// function the prelude always emits, so all of them are reachable by the
		// scan in every shape; an entry that stops appearing is a permission
		// left standing over a door that moved.
		for s := range allowedExits {
			if exits[s] == 0 {
				t.Errorf("%s: no os.Exit(%s) in %s — the allowlist entry is stale, and a stale "+
					"permission is how the next direct exit gets waved through", shape, s.arg, s.fn)
			}
		}
		for s := range allowedRaises {
			if panics[s] == 0 {
				t.Errorf("%s: no panic(%s) in %s — the refusal is no longer raised or no longer "+
					"re-raised where this test believes it is", shape, s.arg, s.fn)
			}
		}
		// And the number itself has ONE authority: the emitted constant is
		// formatted from exitHostRefusal, so a literal 70 anywhere is a second
		// copy that can drift.
		if strings.Contains(src, "os.Exit(70)") {
			t.Errorf("%s: the emitted program writes os.Exit(70) literally; the status must come "+
				"from oathExitRefusal, which is formatted from exitHostRefusal", shape)
		}
		// The refusal block is emitted through Fprintf so the status comes from
		// the constant, which puts a format string around PROSE — and a verb
		// with no argument fails silently into the artifact's own source rather
		// than at the call.
		if strings.Contains(src, "%!") {
			for _, line := range strings.Split(src, "\n") {
				if strings.Contains(line, "%!") {
					t.Errorf("%s: a format verb went unfilled in the emitted source: %s", shape, line)
				}
			}
		}
		if want := fmt.Sprintf("const oathExitRefusal = %d", exitHostRefusal); !strings.Contains(src, want) {
			t.Errorf("%s: the emitted runtime does not define %q, so its status is not derived "+
				"from this backend's constant", shape, want)
		}

		for s := range panics {
			seenPanics[s.arg] = true
		}
	}

	// STALENESS IS CHECKED ACROSS THE UNION, NOT PER SHAPE, and unconditionally.
	//
	// The first version guarded this with "if the message still appears in the
	// emitted text", which made it vacuous in exactly the case it exists for: a
	// panic removed outright takes its message with it, the guard goes false,
	// and the stale permission survives to wave through the next panic that
	// reuses the string. It is per-union because `non-exhaustive` is emitted per
	// `match` and a program with none legitimately lacks it — so the honest
	// claim is that each classification is earned SOMEWHERE in the shapes
	// scanned, not everywhere.
	for want := range classifiedBugPanics {
		if !seenPanics[want] {
			t.Errorf("no emitted shape panics with %q, so the classification is stale — remove "+
				"it, or the next panic reusing that message is silently called a compiler bug", want)
		}
	}
}

// AND THE PATH DOES WHAT IT SAYS, measured on a real binary.
//
// Each case names ONE condition, and each carries a CONTROL invocation of the
// SAME binary that must succeed — otherwise "it exited 70" is satisfied by an
// artifact that is broken for an unrelated reason.
//
// What is asserted is the whole shape of the refusal, because the panic it
// replaced satisfied "it failed" perfectly well:
//   - exit 70, not merely non-zero
//   - the condition NAMED on stderr
//   - stdout untouched
//   - ONE line, and no goroutine dump — the dump named the emitted program's
//     temp directory, which is build-time detail escaping into runtime output
//
// requireGoToolchain skips locally and FAILS in CI, following requireClang in
// llvm_test.go for the same reason it exists there.
//
// Every test below that exercises a COMPILED artifact shells out to `go build`,
// so an absent toolchain removes the whole disposition claim — refusals exiting
// 70, bug panics still escaping — while the run stays green. CI is a Go
// project's CI; the toolchain is never legitimately missing there, which is
// exactly why nobody would notice the day it went.
func requireGoToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("the go toolchain is absent, so no compiled artifact was built and " +
				"the refusal disposition is unwitnessed. In CI that is a failure, not a skip.")
		}
		t.Skip("go toolchain not available (local); this is a hard failure under CI=1")
	}
}

// requireFilename skips locally and FAILS in CI when a filesystem will not hold
// a name the test needs. The names here carry a newline on purpose — they are
// how the injectivity of the refusal message is tested — and a filesystem that
// refuses them removes that witness rather than failing it.
func requireFilename(t *testing.T, path string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("this filesystem will not hold %q, so the message-injectivity witness "+
			"did not run. In CI that is a failure, not a skip: %v", path, err)
	}
	t.Skipf("this filesystem does not accept the filename %q (local): %v", path, err)
}

func TestCompiledArtifactRefusesWithOneLineAndSeventy(t *testing.T) {
	requireGoToolchain(t)
	cases := []struct {
		name string
		def  string
		// trigger and control are argv for the SAME artifact.
		trigger []string
		control []string
		want    string // stderr must name the condition
		wantOut string // stdout of the control run
	}{{
		name: "division by zero",
		def: `(defn divz [] [(args (List Str))] Str
			(match args ((Nil) (if (== (/ 1 0) 0) "zero" "nonzero")) ((Cons h t) "given")))`,
		trigger: nil, control: []string{"x"}, want: "division by zero", wantOut: "given",
	}, {
		name: "modulo by zero",
		def: `(defn modz [] [(args (List Str))] Str
			(match args ((Nil) (if (== (% 1 0) 0) "zero" "nonzero")) ((Cons h t) "given")))`,
		// A SEPARATE CASE, not a variant: big.Int.Quo and big.Int.Rem both said
		// "division by zero", so the artifact once reported an operation the
		// program never performed (#166). The messages must stay two.
		trigger: nil, control: []string{"x"}, want: "modulo by zero", wantOut: "given",
	}, {
		name: "to-rat of non-finite",
		def: `(defn ratinf [] [(args (List Str))] Str
			(match args ((Nil) (if (== (to-rat (/ 1.0f 0.0f)) 1/1) "one" "other")) ((Cons h t) "given")))`,
		trigger: nil, control: []string{"x"}, want: "to-rat of non-finite float", wantOut: "given",
	}, {
		name: "floor of non-finite",
		def: `(defn floorinf [] [(args (List Str))] Str
			(match args ((Nil) (if (== (floor (/ 1.0f 0.0f)) 0) "zero" "other")) ((Cons h t) "given")))`,
		trigger: nil, control: []string{"x"}, want: "floor of non-finite float", wantOut: "given",
	}, {
		name: "Str element outside the scalars",
		// Built from an input codepoint so nothing folds at compile time: this
		// is the oathStrCons path and nothing else.
		def: `(defn negcp [] [(args (List Str))] Str
			(match args
				((Nil) "none")
				((Cons h t) (match h ((SNil) "empty") ((SCons c rest) (SCons (- 0 c) (SNil)))))))`,
		trigger: []string{"A"}, control: nil, want: "cannot encode Str element -65", wantOut: "none",
	}, {
		// THE MESSAGE CARRIES HOST-CHOSEN TEXT, and a newline in it would split
		// one refusal into two stderr lines — the contract broken by data
		// rather than by code, and a way to forge a log line. `what` here is
		// "the contents of <path>" and the path comes from argv, so the vector
		// is a filename. The control run reads a NORMAL file through the same
		// provider, so a refusal that merely failed to open anything would not
		// satisfy this case.
		name: "a refusal naming a path with a newline in it",
		def: `(defn readit [] [(w {readfile (-> Str Str)}) (args (List Str))] Str
			(match args ((Nil) "no path") ((Cons p t) ((. w readfile) p))))`,
		trigger: []string{"@@BADPATH@@"}, control: []string{"@@GOODPATH@@"},
		want: "not valid UTF-8", wantOut: "fine",
	}, {
		name: "octets that are not text",
		def: `(defn echo1 [] [(args (List Str))] Str
			(match args ((Nil) "none") ((Cons h t) h)))`,
		trigger: []string{"\xff\xfe"}, control: nil, want: "is not valid UTF-8", wantOut: "none",
	}}

	// The newline case needs real files, and their PATHS are the vector, so
	// they are substituted into argv rather than written into the table.
	fileDir := t.TempDir()
	badPath := filepath.Join(fileDir, "two\nlines")
	goodPath := filepath.Join(fileDir, "ordinary")
	requireFilename(t, badPath, os.WriteFile(badPath, []byte("\xff\xfe"), 0o644))
	if err := os.WriteFile(goodPath, []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}
	subst := strings.NewReplacer("@@BADPATH@@", badPath, "@@GOODPATH@@", goodPath)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := capStore(t)
			name := strings.SplitN(strings.TrimPrefix(tc.def, "(defn "), " ", 2)[0]
			put(t, st, tc.def)
			markVerified(t, st, name)
			for i, a := range tc.trigger {
				tc.trigger[i] = subst.Replace(a)
			}
			for i, a := range tc.control {
				tc.control[i] = subst.Replace(a)
			}
			bin, _ := buildProgram(t, st, name)

			// CONTROL FIRST.
			cmd := exec.Command(bin, tc.control...)
			var cout, cerr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &cout, &cerr
			if err := cmd.Run(); err != nil {
				t.Fatalf("control: the artifact failed on input that must succeed: %v — %s",
					err, cerr.String())
			}
			if got := strings.TrimRight(cout.String(), "\n"); got != tc.wantOut {
				t.Fatalf("control: printed %q, want %q — the refusal below would be evidence "+
					"about a broken program rather than about the boundary", got, tc.wantOut)
			}

			cmd = exec.Command(bin, tc.trigger...)
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			err := cmd.Run()
			if err == nil {
				t.Fatalf("the artifact did not refuse; it printed %q", out.String())
			}
			if code := cmd.ProcessState.ExitCode(); code != exitHostRefusal {
				t.Errorf("exit %d, want %d — the exit code is the machine-readable half of a "+
					"refusal, and both backends must give a supervisor the same answer",
					code, exitHostRefusal)
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("refused without naming the condition %q: %q", tc.want, errb.String())
			}
			if out.Len() != 0 {
				t.Errorf("a refusal wrote to stdout: %q", out.String())
			}
			if n := len(strings.Split(strings.TrimRight(errb.String(), "\n"), "\n")); n != 1 {
				t.Errorf("the refusal is %d lines, want 1:\n%s", n, errb.String())
			}
			// The specific leak #167 names: a panic printed a goroutine dump
			// carrying the temp directory the Go source was emitted into.
			for _, leak := range []string{"panic:", "goroutine ", "/main.go:", "runtime."} {
				if strings.Contains(errb.String(), leak) {
					t.Errorf("the refusal carries %q — build-time detail in an artifact's "+
						"runtime output: %q", leak, errb.String())
				}
			}
		})
	}
}

// A REFUSAL REACHED FROM REQUEST DATA MUST NOT END THE SERVER.
//
// This is the case that made the refusal a travelling value instead of an exit
// at the raise site. The handler divides by a byte the client chose; a zero
// byte is a refusal in any backend, and the first version of this change
// answered it with os.Exit(70) — a remote process-kill in a well-typed program,
// against SPEC 14.2's rule that a remote party must never be able to end a host.
//
// THE THIRD REQUEST IS THE WHOLE TEST. A 500 alone is satisfied by a server
// that dies immediately after writing it; only a later request proves the
// process survived. The first request is the control: without it, "500" could
// be a handler that refuses everything.
func TestHandlerRefusalAnswers500AndKeepsServing(t *testing.T) {
	requireGoToolchain(t)
	st := capStore(t)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)
	// status = 200 / first-body-byte. Byte 1 answers 200; byte 0 is a refusal.
	put(t, st, `(defn divhandler [] [(r Request)] Response
		(match r ((Req m p h b t)
			(match b
				((Nil) (Resp 204 (Nil [(Pair Str Str)]) (Nil [Int])))
				((Cons x xs) (Resp (/ 200 x) (Nil [(Pair Str Str)]) (Nil [Int])))))))`)
	markVerified(t, st, "divhandler")
	bin, _ := buildProgram(t, st, "divhandler")

	// Claim a free port, release it, hand it over. A race surfaces below as a
	// launch failure rather than as a wrong answer.
	//
	// A SANDBOX THAT FORBIDS BINDING IS NOT A FAILING CLAIM, and reporting it as
	// one is the mirror of the defect this file is full of: a check that cannot
	// tell its own setup from the thing it hunts. Under CI it stays fatal —
	// there, an unbindable socket means the environment changed and this witness
	// silently stopped running, which is worse than a red build.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("cannot bind a local socket, so the handler disposition is unwitnessed: %v", err)
		}
		t.Skipf("this environment does not permit binding a local socket: %v", err)
	}
	addr := probe.Addr().String()
	probe.Close()

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+addr)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	post := func(t *testing.T, body []byte) int {
		t.Helper()
		var resp *http.Response
		var perr error
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			resp, perr = http.Post("http://"+addr+"/", "application/octet-stream", bytes.NewReader(body))
			if perr == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if perr != nil {
			t.Fatalf("no response from %s within 15s: %v — stderr: %s", addr, perr, errb.String())
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// CONTROL: the same artifact serves a request whose byte is not zero.
	if code := post(t, []byte{1}); code != 200 {
		t.Fatalf("control: a divisor of 1 answered %d, want 200 — the refusal below would be "+
			"evidence about a broken handler rather than about the disposition", code)
	}

	if code := post(t, []byte{0}); code != 500 {
		t.Errorf("a zero divisor answered %d, want 500 — the handler was invoked and could not "+
			"complete, which is a 500 and not a dropped connection", code)
	}

	// THE CLAIM. If the refusal exited, this dials a dead process.
	if code := post(t, []byte{1}); code != 200 {
		t.Errorf("after refusing one request the server answered %d — a remote party must never "+
			"be able to end this process (SPEC 14.2), and exiting at the raise site is exactly "+
			"how it could", code)
	}
	if !strings.Contains(errb.String(), "division by zero") {
		t.Errorf("the refusal was not reported on stderr, so an operator sees only a 500: %q",
			errb.String())
	}
	// Still ONE line for the refusal — the property does not stop holding
	// because the disposition changed.
	n := 0
	for _, line := range strings.Split(strings.TrimRight(errb.String(), "\n"), "\n") {
		if strings.Contains(line, "division by zero") {
			n++
		}
		if strings.Contains(line, "goroutine ") || strings.Contains(line, "panic:") {
			t.Errorf("a goroutine dump reached the server's stderr: %q", errb.String())
		}
	}
	if n != 1 {
		t.Errorf("the refusal appears on %d lines, want 1:\n%s", n, errb.String())
	}
}

// THE ONE-LINE ENCODING IS INJECTIVE, which is a different claim from "one
// line" and fails differently.
//
// Escaping only the line breaks maps a real newline to the two characters
// backslash-n — so a file named "two\nlines" and one named "two" + backslash +
// "nlines" produce the SAME diagnostic. That is the U+FFFD defect this whole
// area exists to remove, relocated from the output into the message: two
// distinct inputs, one rendering, no way back.
func TestRefusalMessageEncodingIsInjective(t *testing.T) {
	requireGoToolchain(t)
	st := capStore(t)
	put(t, st, `(defn readit [] [(w {readfile (-> Str Str)}) (args (List Str))] Str
		(match args ((Nil) "no path") ((Cons p t) ((. w readfile) p))))`)
	markVerified(t, st, "readit")
	bin, _ := buildProgram(t, st, "readit")

	dir := t.TempDir()
	newline := filepath.Join(dir, "two\nlines")
	literal := filepath.Join(dir, "two\\nlines")
	for _, p := range []string{newline, literal} {
		requireFilename(t, p, os.WriteFile(p, []byte("\xff\xfe"), 0o644))
	}

	say := func(path string) string {
		t.Helper()
		cmd := exec.Command(bin, path)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err == nil {
			t.Fatalf("%q was not refused; it printed %q", path, out.String())
		}
		if code := cmd.ProcessState.ExitCode(); code != exitHostRefusal {
			t.Errorf("%q refused with exit %d, want %d", path, code, exitHostRefusal)
		}
		if n := len(strings.Split(strings.TrimRight(errb.String(), "\n"), "\n")); n != 1 {
			t.Errorf("%q refused across %d lines, want 1:\n%s", path, n, errb.String())
		}
		return errb.String()
	}

	if a, b := say(newline), say(literal); a == b {
		t.Errorf("a filename containing a real newline and one containing backslash-n produce "+
			"the SAME refusal %q — the escape is not injective, so the message no longer names "+
			"which file was refused", a)
	}
}

// A NON-REFUSAL PANIC STILL ESCAPES AS A PANIC — status 2, stack trace, no
// disposition. This is the half of the classification that only a running
// binary can witness.
//
// `oathExitOnRefusal` is a deferred recover(), and recover() cannot be
// selective: it catches a compiler-bug panic exactly as readily as a refusal.
// So the entire separation between the two classes rests on oathRefusalOf
// re-raising what is not a refusal, and nothing about a green suite would
// notice if it stopped. The failure it guards against is not a wrong exit code
// — it is a crashed program exiting 0, because a recovered panic lets main
// return normally.
//
// THE PANIC IS INJECTED INTO THE EMITTED SOURCE, and that is deliberate rather
// than a shortcut. Every classified bug panic is unreachable unless the checker
// is wrong, which is what makes it a bug panic; there is no Oath program that
// reaches one. What is under test is the DISPOSER's treatment of such a panic,
// not the condition, so injecting one at a point inside main's dynamic extent
// is the only way to put the disposer in front of it at all.
func TestBugPanicStillEscapesAsAPanic(t *testing.T) {
	requireGoToolchain(t)
	st := capStore(t)
	put(t, st, `(defn echo1 [] [(args (List Str))] Str
		(match args ((Nil) "none") ((Cons h t) h)))`)
	markVerified(t, st, "echo1")
	prog, err := planProgram(st, "echo1")
	if err != nil {
		t.Fatal(err)
	}
	src, err := emitProgram(st, prog)
	if err != nil {
		t.Fatal(err)
	}

	// After the disposer is installed, so the panic unwinds THROUGH it. Injected
	// before it would prove nothing: the runtime's default printer would handle
	// a panic no disposer had a chance to see.
	const anchor = "\tdefer oathExitOnRefusal()\n"
	if strings.Count(src, anchor) != 1 {
		t.Fatalf("the injection anchor %q appears %d times, so this test is not placing the "+
			"panic where it believes it is", anchor, strings.Count(src, anchor))
	}
	bug := `panic("structEq on function value")`
	src = strings.Replace(src, anchor,
		anchor+"\tif len(os.Args) > 1 {\n\t\t"+bug+"\n\t}\n", 1)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if o, err := runIn(dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("go build failed:\n%s", o)
	}

	// CONTROL: the injection did not break the program. Without it, "the other
	// invocation panicked" is evidence about a broken build.
	cmd := exec.Command(bin)
	var cout, cerr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &cout, &cerr
	if err := cmd.Run(); err != nil {
		t.Fatalf("control: the artifact failed with no argument: %v — %s", err, cerr.String())
	}
	if got := strings.TrimRight(cout.String(), "\n"); got != "none" {
		t.Fatalf("control: printed %q, want %q", got, "none")
	}

	cmd = exec.Command(bin, "trigger")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if err == nil {
		t.Fatalf("a compiler-bug panic was SWALLOWED: the artifact exited 0 and printed %q. "+
			"recover() is not selective, so a disposer that does not re-raise turns every "+
			"internal defect into a successful run", out.String())
	}
	code := cmd.ProcessState.ExitCode()
	if code == exitHostRefusal {
		t.Errorf("a compiler-bug panic was reported as a host refusal (exit %d) — the two "+
			"classes have collapsed, which is the defect this change exists to undo, one "+
			"layer down", code)
	}
	if code != 2 {
		t.Errorf("a compiler-bug panic exited %d, want 2 (Go's own panic status)", code)
	}
	// The stack trace is the whole value of a bug report, and it is exactly what
	// a refusal must not print — so this asserts the OPPOSITE of what the
	// refusal tests assert, over the same runtime.
	for _, want := range []string{"panic:", "structEq on function value", "goroutine "} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the bug report does not contain %q: %q", want, errb.String())
		}
	}
}

// A BUG PANIC INSIDE A HANDLER MUST NOT BE SWALLOWED, AND MUST NOT KILL THE
// PROCESS EITHER.
//
// The per-request disposer calls the same oathRefusalOf as the standalone one,
// and that function re-raises anything that is not an oathRefusal — so a
// compiler-bug panic keeps travelling and net/http recovers it per connection,
// exactly as before #167. Both halves matter and they fail in opposite
// directions:
//
//	swallowed   the disposer returns quietly, the request gets a clean answer,
//	            and a compiler defect is now invisible — the outcome the
//	            refusal/panic split exists to prevent
//	fatal       the panic ends the process, and a remote party who can reach a
//	            bug can stop the host — SPEC 14.2's rule, and the reason the
//	            standalone disposition was wrong for this boundary
//
// The structural assertion that both boundaries call one classifier is real but
// it is not this: it says the code is shaped correctly, not that a running
// artifact behaves correctly. The injection mirrors
// TestBugPanicStillEscapesAsAPanic — after the disposer, so the panic unwinds
// THROUGH it — and is gated on a header so ordinary requests still prove the
// server is alive.
// syncBuf is a bytes.Buffer safe to read while os/exec's stderr-copy goroutine
// is still writing to it. Reading a bare bytes.Buffer there is a data race, and
// the race is not theoretical for an assertion ABOUT stderr: the copy may not
// have drained the line the assertion is looking for, so the check passes by
// missing it rather than by the line being absent.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestHandlerBugPanicIsNotSwallowedAndDoesNotKillTheProcess(t *testing.T) {
	requireGoToolchain(t)
	st := capStore(t)
	put(t, st, `(data Pair [a b] (Pair a b))`)
	put(t, st, `(data Request [] (Req Str Str (List (Pair Str Str)) (List Int) Int))`)
	put(t, st, `(data Response [] (Resp Int (List (Pair Str Str)) (List Int)))`)
	put(t, st, `(defn okhandler [] [(r Request)] Response
		(match r ((Req m p h b t) (Resp 200 (Nil [(Pair Str Str)]) (Nil [Int])))))`)
	markVerified(t, st, "okhandler")
	prog, err := planProgram(st, "okhandler")
	if err != nil {
		t.Fatal(err)
	}
	src, err := emitProgram(st, prog)
	if err != nil {
		t.Fatal(err)
	}

	// AFTER the per-request disposer, so the panic unwinds through it. The
	// anchor is asserted unique for the same reason the standalone test asserts
	// its own: an injection that silently lands nowhere makes every assertion
	// below vacuous.
	const anchor = "\t\t}()\n"
	dispose := "if msg, ok := oathRefusalOf(recover()); ok {"
	i := strings.Index(src, dispose)
	if i < 0 {
		t.Fatal("the emitted handler has no per-request disposer, so this test is not " +
			"measuring what it believes it is")
	}
	j := strings.Index(src[i:], anchor)
	if j < 0 {
		t.Fatal("could not find the end of the per-request disposer")
	}
	at := i + j + len(anchor)
	bug := "\t\tif r.Header.Get(\"X-Oath-Bug\") != \"\" {\n" +
		"\t\t\tpanic(\"structEq on function value\")\n\t\t}\n"
	src = src[:at] + bug + src[at:]

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module oathprog\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	if o, err := runIn(dir, "go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("go build failed:\n%s", o)
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("cannot bind a local socket, so this witness did not run: %v", err)
		}
		t.Skipf("this environment does not permit binding a local socket: %v", err)
	}
	addr := probe.Addr().String()
	probe.Close()

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OATH_HTTP_ADDR="+addr)
	errb := &syncBuf{}
	cmd.Stderr = errb
	if err := cmd.Start(); err != nil {
		t.Fatalf("launch: %v", err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		// cmd.Wait, NOT cmd.Process.Wait: the latter waits for the process and
		// leaves os/exec's stderr-copy goroutine still running, so a read after
		// it can miss the very line this test asserts is absent. Only exec.Cmd's
		// Wait joins the copiers.
		_ = cmd.Wait()
	}
	defer stop()

	get := func(bug bool) (*http.Response, error) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
		if bug {
			req.Header.Set("X-Oath-Bug", "1")
		}
		return (&http.Client{Timeout: 5 * time.Second}).Do(req)
	}

	// The server must be serving before anything is concluded from a failure.
	deadline := time.Now().Add(15 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		if resp, err = get(false); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the handler never came up, so nothing below is evidence: %v\n%s", err, errb.String())
	}
	resp.Body.Close()

	// THE BUG REQUEST. net/http recovers a handler panic and closes the
	// connection without a response, so an error here is the CORRECT outcome —
	// what must not happen is a clean answer, which is what "swallowed" looks
	// like from outside.
	if resp, err := get(true); err == nil {
		defer resp.Body.Close()
		t.Errorf("a compiler-bug panic produced a clean %d response — it was swallowed by "+
			"the refusal disposer instead of being re-raised", resp.StatusCode)
	}

	// AND THE PROCESS SURVIVED, which is the half a remote party could exploit.
	// Asked by SERVING, not by signalling: os.Process.Signal supports only Kill on
	// Windows, so a liveness probe built on signal 0 fails there against a
	// perfectly healthy server. Answering a request is the stronger evidence
	// anyway — a process can be alive and no longer listening.
	if resp, err := get(false); err != nil {
		t.Fatalf("the server stopped answering after the panic: %v\n%s", err, errb.String())
	} else {
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("after the panic the handler answers %d, want 200", resp.StatusCode)
		}
	}

	// The refusal one-liner must NOT appear: this was not a refusal. Rejected
	// UNCONDITIONALLY rather than only when no panic report accompanies it — a
	// disposition that printed the refusal line AND re-raised would satisfy every
	// check above (the connection drops, the process lives) while still having
	// described a compiler defect as a semantic refusal, which is the confusion
	// this whole split exists to remove.
	// STOP FIRST, so the stderr copy has drained. Checking a live process's
	// captured stderr can pass by not having seen the line yet, which is the
	// same defect as a check that cannot tell absence from not-looking.
	stop()
	if strings.Contains(errb.String(), "oath: ") {
		t.Errorf("a bug panic was reported as a refusal:\n%s", errb.String())
	}
}
