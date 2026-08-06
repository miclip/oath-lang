package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Backend refusals carry a TYPED reason (#134).
//
// The defect this closes: a test keyed on `strings.Contains(err, "match on
// Str")`, and llvmUnsupported's help paragraph LISTS unsupported features — so
// the substring stayed true after match on Str was implemented and the test
// skipped forever while logging a message that read as deliberate. A witness
// that infers semantic state from implementation WORDING rather than exercising
// the capability.
//
// The repair is not a better substring. Reason is the contract; Detail and Help
// are presentation.

// TestRefusalReasonSurvivesRewording is the central claim: changing only the
// human text must not change what any gate measures.
func TestRefusalReasonSurvivesRewording(t *testing.T) {
	original := llvmUnsupported(reasonDynamicStr, "a Str built from non-constant parts")
	reworded := &backendRefusal{
		Reason:  reasonDynamicStr,
		Backend: "some-other-backend/9",
		Detail:  "completely different wording that shares no words with the original",
		Help:    "and different help",
	}
	a, okA := refusedFor(original)
	b, okB := refusedFor(reworded)
	if !okA || !okB {
		t.Fatal("both must be recognised as refusals")
	}
	if a != b {
		t.Errorf("rewording changed the reason: %q vs %q", a, b)
	}
	// ...and the messages really are unrelated, so the test above is not
	// passing because they happen to be similar.
	if strings.Contains(reworded.Error(), "Str") {
		t.Error("setup: the reworded message should share no vocabulary with the original")
	}
}

// TestUnrelatedErrorCannotSatisfyAReasonCheck. The old style could be satisfied
// by boilerplate; this one cannot be satisfied by anything but the refusal.
func TestUnrelatedErrorCannotSatisfyAReasonCheck(t *testing.T) {
	for _, err := range []error{
		errors.New("the llvm backend cannot lower a match on Str whose arms are not SNil and SCons"),
		fmt.Errorf("dynamic-str"),
		errors.New(""),
		nil,
	} {
		if _, ok := refusedFor(err); ok {
			t.Errorf("a plain error satisfied a refusal check: %v", err)
		}
	}
	// A refusal with a DIFFERENT reason must not satisfy a specific check.
	r, ok := refusedFor(llvmUnsupported(reasonIntRange, "the Int literal 2^70"))
	if !ok || r == reasonDynamicStr {
		t.Errorf("reason %q must not match reasonDynamicStr", r)
	}
}

// TestBothBackendsShareTheCapabilityReason: where two backends mean the same
// thing, they say it with the same reason. Previously these were two unrelated
// fmt.Errorf strings, so a caller could match one and silently miss the other.
func TestBothBackendsShareTheCapabilityReason(t *testing.T) {
	req := CapabilityRequirement{Field: "fetch", Kind: capabilityKind("http_request")}
	_, goErr := goProviderFor(req)
	_, llvmErr := llvmProviderFor(req)
	if goErr == nil && llvmErr == nil {
		t.Skip("both backends now provide http_request; pick another unprovided kind")
	}
	seen := map[refusalReason]int{}
	for name, err := range map[string]error{"go": goErr, "llvm": llvmErr} {
		if err == nil {
			continue
		}
		r, ok := refusedFor(err)
		if !ok {
			t.Errorf("%s backend returned an untyped capability refusal: %v", name, err)
			continue
		}
		if r != reasonCapability {
			t.Errorf("%s backend used reason %q for an unprovided capability", name, r)
		}
		seen[r]++
	}
	if len(seen) > 1 {
		t.Errorf("the same fact produced %d different reasons: %v", len(seen), seen)
	}
}

// TestEveryRefusalUsesADeclaredReason makes the vocabulary CLOSED.
//
// WHAT IT CATCHES, verified by injecting each into a production file:
//
//	Reason: "invented"                      a literal            -> fires
//	no Reason field at all                  omission             -> fires
//	llvmUnsupported(refusalReason("x"), …)  a conversion at a
//	                                        forwarder call site  -> fires
//
// AND ONE CASE THAT IS NOT REACHABLE, recorded so the absence is not mistaken
// for a hole: an "undeclared identifier" cannot exist. Any identifier of type
// refusalReason is declared BY that declaration, and adding
// `const reasonX refusalReason = "..."` is the legitimate, greppable way to
// extend the vocabulary. The check exists to stop ad-hoc strings entering it,
// not to stop it growing.
//
// The forwarder path is followed rather than exempted: llvmUnsupported takes a
// refusalReason parameter, so its Reason field is that parameter and the
// constant appears at its CALL sites, which are checked separately. Exempting
// it would have left nine of eleven reasons unverified.
//
// The issue asks for "which refusals a backend can emit at all" to be
// checkable, so a refusal added without a reason identifier is a build error
// rather than a new string nobody matches. Go will not enforce that — an
// untyped string constant is assignable to refusalReason — so it is enforced
// here: every construction site must name a declared constant.
func TestEveryRefusalUsesADeclaredReason(t *testing.T) {
	declared := map[string]bool{}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files found; this check did not run (%v)", err)
	}
	type site struct {
		file      string
		line      int
		reason    string // the identifier used, or "" for a literal
		absent    bool   // no Reason field at all
		forwarded bool   // a refusalReason parameter, checked at its call sites
	}
	var sites []site
	// forwarders take a refusalReason parameter and construct a refusal from
	// it, so the constant appears at THEIR call sites instead. Closing the
	// vocabulary means following them rather than exempting them.
	forwarders := map[string]bool{}
	forwardedParams := map[string]bool{}
	fset := gotoken.NewFileSet()
	parsed := map[string]*ast.File{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		parsed[f] = file
		// A function taking a refusalReason parameter is a forwarder: its
		// callers supply the constant.
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Type.Params == nil {
				continue
			}
			for _, pf := range fn.Type.Params.List {
				if id, ok := pf.Type.(*ast.Ident); ok && id.Name == "refusalReason" {
					forwarders[fn.Name.Name] = true
					for _, nm := range pf.Names {
						forwardedParams[nm.Name] = true
					}
				}
			}
		}
	}
	for f, file := range parsed {
		ast.Inspect(file, func(n ast.Node) bool {
			// Declarations: const reasonX refusalReason = "..."
			if vs, ok := n.(*ast.ValueSpec); ok {
				if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "refusalReason" {
					for _, nm := range vs.Names {
						declared[nm.Name] = true
					}
				}
			}
			// Constructions: &backendRefusal{Reason: ...}
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			id, ok := cl.Type.(*ast.Ident)
			if !ok || id.Name != "backendRefusal" {
				return true
			}
			// A construction with NO Reason field must be caught too: an
			// earlier version only recorded sites where a Reason key was
			// present, so omitting it entirely produced no site and passed.
			found := false
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "Reason" {
					continue
				}
				found = true
				name := ""
				if id, ok := kv.Value.(*ast.Ident); ok {
					name = id.Name
				}
				sites = append(sites, site{file: f, line: fset.Position(kv.Pos()).Line,
					reason: name, forwarded: forwardedParams[name]})
			}
			if !found {
				sites = append(sites, site{file: f, line: fset.Position(cl.Pos()).Line, absent: true})
			}
			return true
		})
	}
	// Every CALL to a forwarder must pass a declared constant, so the
	// vocabulary is closed through them rather than around them.
	callSites := 0
	for f, file := range parsed {
		ast.Inspect(file, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := c.Fun.(*ast.Ident)
			if !ok || !forwarders[id.Name] || len(c.Args) == 0 {
				return true
			}
			arg, isIdent := c.Args[0].(*ast.Ident)
			if !isIdent || !declared[arg.Name] {
				t.Errorf("%s:%d calls %s without a declared refusalReason constant",
					f, fset.Position(c.Pos()).Line, id.Name)
			}
			callSites++
			return true
		})
	}
	if callSites == 0 {
		t.Error("no forwarder call sites found; the vocabulary is not actually closed")
	}

	if len(declared) < 5 {
		t.Fatalf("only %d reason constants found; the scan did not work", len(declared))
	}
	if len(sites) == 0 {
		t.Fatal("no backendRefusal constructions found; the scan did not work")
	}
	for _, s := range sites {
		switch {
		case s.absent:
			t.Errorf("%s:%d constructs a backendRefusal with NO Reason — every refusal "+
				"must name one, or callers cannot branch on it", s.file, s.line)
		case s.reason == "":
			t.Errorf("%s:%d constructs a refusal with a literal Reason — use a declared "+
				"constant so the vocabulary stays closed and greppable", s.file, s.line)
		case s.forwarded:
			// checked at the forwarder's call sites below
		case !declared[s.reason]:
			// Being an identifier is not enough: it must be one of the DECLARED
			// reasons. An earlier version checked only the syntactic form, so a
			// newly invented identifier satisfied a test whose claim is that the
			// vocabulary is closed.
			t.Errorf("%s:%d uses reason %q, which is not a declared refusalReason constant",
				s.file, s.line, s.reason)
		}
	}
	t.Logf("%d reason constants, %d construction sites, all naming declared constants",
		len(declared), len(sites))
}

// skipIfRefused is the pattern #134 exists to make possible, and the reason it
// is a helper rather than an idiom: the failure mode it prevents is a test
// deciding for itself what "still blocked" means.
//
//	err == nil                 SUPPORT LANDED -> fail, so the skip retires
//	refusal with `want`        still blocked  -> skip, naming the reason
//	anything else              a real failure -> fail
//
// The middle case is the only skip, and it cannot be reached by prose. The
// FIRST case is the one that matters: a skip keyed on a substring stays
// satisfied after the feature ships, so the test never comes back. Keyed on a
// reason, the absence of the refusal is itself a failure.
func skipIfRefused(t *testing.T, err error, want refusalReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("this path is no longer refused for %q — the backend now supports it, "+
			"so this guard must be REMOVED and the assertion it was hiding enabled", want)
	}
	got, ok := refusedFor(err)
	if !ok {
		t.Fatalf("expected a typed refusal for %q, got a plain error: %v", want, err)
	}
	if got != want {
		t.Fatalf("expected refusal %q, got %q: %v", want, got, err)
	}
	t.Skipf("still refused for %q", want)
}

// TestSkipIfRefusedRetiresWhenSupportLands exercises all three branches of the
// helper, because a guard nobody has watched fail is a hypothesis.
func TestSkipIfRefusedRetiresWhenSupportLands(t *testing.T) {
	run := func(err error, want refusalReason) (skipped, failed bool, msg string) {
		var sub testing.T
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { recover() }()
			skipIfRefused(&sub, err, want)
		}()
		<-done
		return sub.Skipped(), sub.Failed(), ""
	}

	// SUPPORT LANDED: must FAIL, not skip. This is the case a substring-keyed
	// guard gets wrong, and it is why the old test skipped forever.
	if skipped, failed, _ := run(nil, reasonDynamicStr); skipped || !failed {
		t.Errorf("nil error (support landed) must FAIL the guard, got skipped=%v failed=%v",
			skipped, failed)
	}
	// STILL BLOCKED: must skip.
	if skipped, failed, _ := run(llvmUnsupported(reasonDynamicStr, "x"), reasonDynamicStr); !skipped || failed {
		t.Errorf("the expected refusal must SKIP, got skipped=%v failed=%v", skipped, failed)
	}
	// A DIFFERENT refusal: must fail, not skip past an unrelated problem.
	if skipped, failed, _ := run(llvmUnsupported(reasonIntRange, "x"), reasonDynamicStr); skipped || !failed {
		t.Errorf("a different refusal must FAIL, got skipped=%v failed=%v", skipped, failed)
	}
	// A plain error: must fail.
	if skipped, failed, _ := run(errors.New("cannot lower dynamic-str"), reasonDynamicStr); skipped || !failed {
		t.Errorf("a plain error must FAIL, got skipped=%v failed=%v", skipped, failed)
	}
}
