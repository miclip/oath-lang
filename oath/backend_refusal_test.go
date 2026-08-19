package main

import (
	"errors"
	"fmt"
	"go/ast"
	gobuild "go/build"
	"go/importer"
	"go/parser"
	goprinter "go/printer"
	gotoken "go/token"
	"go/types"
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

// TestEveryRefusalUsesADeclaredReason makes the vocabulary CLOSED, by tracing
// the BINDING RELATION rather than by matching shapes.
//
// THE CLAIM: every value assigned to backendRefusal.Reason resolves, through any
// chain of forwarding parameters, to a package-level `const` of type
// refusalReason.
//
// Three earlier versions of this check were each weaker than that sentence, and
// each was weaker in a way I had not pictured:
//
//	v1  "is it an identifier?"      an undeclared one passed; an omitted
//	                                Reason field produced no site at all
//	v2  name-matched declarations   `var r refusalReason = "x"` joined the
//	                                vocabulary, because the scan never checked
//	                                the declaration was const
//	v3  package-wide parameter      any local named `reason` in any function was
//	    names, argument 0           treated as forwarded; a forwarder whose
//	                                reason is not the first parameter had the
//	                                WRONG argument validated
//
// Every one encoded the examples in front of me — one forwarder, one parameter,
// first position, constants — instead of the relation. So this resolves
// identifiers through go/types, identifies forwarding by (function object,
// parameter index), and follows the actual argument at that index, recursively,
// failing on any chain that does not terminate in a declared constant.
func TestEveryRefusalUsesADeclaredReason(t *testing.T) {
	fset := gotoken.NewFileSet()
	var files []*ast.File
	// Files chosen by BUILD CONSTRAINTS, not by glob: eval_depth_native.go and
	// eval_depth_wasm.go both declare maxEvalDepth, so a glob type-checks a
	// package that cannot exist.
	bpkg, err := gobuild.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("resolving the package: %v", err)
	}
	names := bpkg.GoFiles
	if len(names) < 20 {
		t.Fatalf("only %d files in the package; this check did not run", len(names))
	}
	for _, n := range names {
		src, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("reading %s: %v", n, err)
		}
		f, err := parser.ParseFile(fset, n, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", n, err)
		}
		files = append(files, f)
	}

	got, err := auditReasonClosure(fset, files, "oath")
	if err != nil {
		t.Fatalf("auditing the package: %v", err)
	}
	// An excluded file can reach the vocabulary through an ALIAS declared in an
	// audited file, mentioning neither original spelling, so the alias names
	// are derived from the audited package rather than assumed.
	aliasNames := got.aliases

	// COVERAGE: the audit type-checks ONE build configuration, but the claim is
	// about the package however it is built — this repo builds with `cloud`,
	// `wasm` and `conformance_mutation` tags. A file excluded by the default
	// context is invisible to go/types here, so instead of silently narrowing
	// the claim, assert that no such file constructs a refusal. If one ever
	// does, this fails and says the universe stopped matching the claim, rather
	// than passing over a file it never read.
	audited := map[string]bool{}
	for _, n := range names {
		audited[n] = true
	}
	all, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package directory: %v", err)
	}
	checkedOut := 0
	for _, n := range all {
		if audited[n] || strings.HasSuffix(n, "_test.go") {
			continue
		}
		checkedOut++
		src, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("reading %s: %v", n, err)
		}
		ef, err := parser.ParseFile(fset, n, src, 0)
		if err != nil {
			t.Fatalf("parsing build-excluded %s: %v", n, err)
		}
		for _, f := range scanUnauditedFile(fset, ef, aliasNames, got.forwarders) {
			t.Errorf("%s: %s — the closure audit cannot type-check this file under the "+
				"default build context, so the vocabulary is not closed under every "+
				"build of this package", n, f)
		}
	}
	t.Logf("%d files audited; %d build-excluded files checked for refusals", len(names), checkedOut)

	for _, f := range got.findings {
		t.Error(f)
	}
	if got.declared < 5 {
		t.Fatalf("only %d declared refusalReason constants found; the scan did not work", got.declared)
	}
	if got.sites == 0 {
		t.Fatal("no backendRefusal constructions found; the scan did not work")
	}
	// NON-VACUITY of the forwarder closure: the derivation must find at least the
	// known forwarder, or the excluded-file scan's forwarder-name check is
	// silently checking nothing — the very forever-passing failure this whole
	// audit exists to prevent, one level up.
	if !got.forwarders["llvmUnsupported"] {
		t.Errorf("the forwarder derivation did not find llvmUnsupported (a function taking a "+
			"refusalReason); the excluded-file forwarder-name check is vacuous. got: %v", got.forwarders)
	}
	t.Logf("%d declared reasons; %d construction sites; forwarders %v; every Reason traced to a constant",
		got.declared, got.sites, got.forwarders)
}

// WHAT THIS AUDIT REFUSES CONSERVATIVELY — declared, so that a refusal on one
// of these shapes reads as a decision rather than as a bug.
//
// Every one FAILS CLOSED. None can admit an ad-hoc reason; each rejects a shape
// that is legal Go, does not occur in this package, and would make the gate
// fail loudly and be extended if it ever did.
//
//	shape                              why it is refused
//	----------------------------------------------------------------------
//	a function used as a VALUE         its calls cannot be enumerated, so the
//	                                   recorded sites are not the complete set
//	an EXPORTED forwarder              callers outside the package are not in
//	                                   the graph (vacuous today: package main)
//	a method whose name is also        dispatch reaches implementations the
//	dispatched through an INTERFACE    call graph never sees
//	a function-literal PARAMETER       a literal is a callable context this
//	                                   audit does not model (a CAPTURED
//	                                   variable is fine — it is the enclosing
//	                                   function's parameter)
//	a method RECEIVER carrying the     receivers are not in the forwarding
//	reason                             relation
//	a parameter that is reassigned,    it no longer necessarily carries what
//	has its address taken, or has a    callers passed
//	pointer-method taken on it
//	a pointer-method taken on Reason   the address is taken implicitly and the
//	                                   write need not mention Reason again
//	the refusal type named anywhere    only a composite literal can give a
//	except behind a pointer or as a    Reason, so every other appearance is
//	composite-literal type             treated as possible zero-value creation
//	                                   — this over-refuses a by-value return
//	                                   type and a nested `[]backendRefusal{{…}}`
//
// The former open boundary — a build-excluded file passing an untyped string
// constant to an audited forwarder, which converts implicitly and names no TYPE
// — is now closed: scanUnauditedFile also flags a mention of the FORWARDER's
// name (derived from the type-checked package), so calling or valuing one in an
// excluded file is caught. The only residue is a forwarder reached without ever
// naming it (reflection, or an interface method), which no parse-only scan sees;
// type-checking every tagged configuration is what would close that.

// reasonAudit is what one run of the closure audit observed.
type reasonAudit struct {
	findings []string        // one per Reason that does not trace to a declared constant
	sites    int             // backendRefusal constructions seen
	declared int             // package-scope refusalReason constants
	aliases  map[string]bool // package-scope aliases of either refusal type
	// forwarders are package-scope functions with a refusalReason parameter —
	// the constructions an excluded file could reach WITHOUT naming a refusal
	// type, by passing an untyped string constant that converts implicitly. Their
	// names let the parse-only scan flag such a call, closing the boundary the
	// scan otherwise leaves open.
	forwarders map[string]bool
}

// auditReasonClosure is the audit as a PURE FUNCTION over a parsed package, so
// that the controls below can feed it synthetic sources rather than mutating
// the real ones. Its predecessor was inlined in the test, which meant the only
// way to ask "would this reject X?" was to append X to llvm.go and run — an
// experiment that leaves no trace in the suite and is therefore not evidence
// anyone but its author ever had.
func auditReasonClosure(fset *gotoken.FileSet, files []*ast.File, pkgName string) (reasonAudit, error) {
	var out reasonAudit

	info := &types.Info{
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Types:      map[ast.Expr]types.TypeAndValue{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(pkgName, fset, files, info)
	if err != nil {
		return out, err
	}

	// DECLARED = package-scope const of type refusalReason. Constant-ness is
	// part of the claim: a var of that type is mutable and is not vocabulary.
	declared := map[types.Object]bool{}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if c, ok := scope.Lookup(name).(*types.Const); ok {
			// Unalias for the same reason the literal type is unaliased: a
			// constant declared through an alias of refusalReason has the
			// identical type and is identical vocabulary.
			if named, ok := types.Unalias(c.Type()).(*types.Named); ok && named.Obj().Name() == "refusalReason" {
				declared[c] = true
			}
		}
	}
	out.declared = len(declared)

	// Alias spellings, for callers that must recognise the vocabulary in files
	// this audit could not type-check.
	out.aliases = map[string]bool{}
	for _, name := range scope.Names() {
		if tn, ok := scope.Lookup(name).(*types.TypeName); ok && tn.IsAlias() {
			if named, ok := types.Unalias(tn.Type()).(*types.Named); ok {
				if n := named.Obj().Name(); n == "backendRefusal" || n == "refusalReason" {
					out.aliases[name] = true
				}
			}
		}
	}

	// Forwarder names: any package-scope function taking a refusalReason
	// parameter constructs (or forwards to a construction of) a refusal. An
	// excluded file calling one reaches the vocabulary even when it passes an
	// untyped string constant that names no type — so scanUnauditedFile flags a
	// mention of the name, which also catches the forwarder taken as a value
	// (`fwd := llvmUnsupported`). Derived, not hardcoded, so a new forwarder is
	// covered without anyone remembering to add it.
	out.forwarders = map[string]bool{}
	for _, name := range scope.Names() {
		fn, ok := scope.Lookup(name).(*types.Func)
		if !ok {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			continue
		}
		for i := 0; i < sig.Params().Len(); i++ {
			if named, ok := types.Unalias(sig.Params().At(i).Type()).(*types.Named); ok && named.Obj().Name() == "refusalReason" {
				out.forwarders[name] = true
				break
			}
		}
	}
	// METHOD forwarders too: the audit follows concrete method forwarders, and a
	// method taking a refusalReason is reachable from an excluded file as
	// `x.reject("invented")`. Its name appears as a selector's Sel identifier,
	// which the scan's Ident case visits, so recording the method name is enough.
	sigHasReason := func(sig *types.Signature) bool {
		for j := 0; j < sig.Params().Len(); j++ {
			if named, ok := types.Unalias(sig.Params().At(j).Type()).(*types.Named); ok && named.Obj().Name() == "refusalReason" {
				return true
			}
		}
		return false
	}
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		for i := 0; i < named.NumMethods(); i++ {
			m := named.Method(i)
			if sig, ok := m.Type().(*types.Signature); ok && sigHasReason(sig) {
				out.forwarders[m.Name()] = true
			}
		}
	}

	// Call graph: for each function object, every call to it with its enclosing
	// function, so a forwarded parameter can be followed to its arguments.
	type callSite struct {
		call *ast.CallExpr
		in   *ast.FuncDecl
		// argOffset is 1 for a method EXPRESSION (`T.m(recv, x)`), where the
		// receiver occupies argument 0 while paramIndex counts only declared
		// parameters. Without it, parameter i resolves to argument i and a
		// valid constant-rooted chain is rejected because the receiver was
		// inspected instead.
		argOffset int
	}
	calls := map[types.Object][]callSite{}
	funcOf := map[*ast.FuncDecl]types.Object{}
	// calleeIdents are the identifiers appearing in callee position. Any OTHER
	// mention of a function is that function used as a VALUE — `alias := fwd` —
	// after which it can be called through a name this audit cannot follow, so
	// its recorded call sites are no longer the complete set.
	calleeIdents := map[*ast.Ident]bool{}
	usedAsValue := map[types.Object]bool{}
	// Method names invoked through an INTERFACE value. Such a call names the
	// interface's method, not the concrete one, so it lands under a different
	// object and leaves the implementation looking fully enumerated by its
	// direct calls alone. Matching by NAME over-marks, which is the safe
	// direction: over-marking refuses, under-marking admits.
	ifaceDispatched := map[string]bool{}
	// EVERY declaration is scanned, not only function bodies. A call sited at
	// package level (`var _ = mk(reasonPrim)`) is a real link in the chain, and
	// a graph that cannot see it reports "nothing calls this" about a function
	// that is called — the check would then fail for a reason that has nothing
	// to do with the vocabulary it is guarding.
	// calleeIdent names the function being called. A bare *ast.Ident covers only
	// plain calls: a METHOD forwarder, a generic instantiation, or a
	// parenthesised callee all present differently, and omitting them made the
	// audit report "nothing calls this" about a function with call sites — a
	// false rejection produced by the graph, not by the vocabulary.
	var calleeIdent func(e ast.Expr) *ast.Ident
	calleeIdent = func(e ast.Expr) *ast.Ident {
		switch f := e.(type) {
		case *ast.Ident:
			return f
		case *ast.ParenExpr:
			return calleeIdent(f.X)
		case *ast.SelectorExpr:
			return f.Sel
		case *ast.IndexExpr: // generic instantiation, one type argument
			return calleeIdent(f.X)
		case *ast.IndexListExpr: // generic instantiation, several
			return calleeIdent(f.X)
		}
		return nil
	}
	collectCalls := func(root ast.Node, in *ast.FuncDecl) {
		ast.Inspect(root, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id := calleeIdent(c.Fun); id != nil {
				calleeIdents[id] = true
				if obj := info.Uses[id]; obj != nil {
					off := 0
					fun := c.Fun
					for {
						pe, ok := fun.(*ast.ParenExpr)
						if !ok {
							break
						}
						fun = pe.X
					}
					if sel, ok := fun.(*ast.SelectorExpr); ok {
						if s := info.Selections[sel]; s != nil && s.Kind() == types.MethodExpr {
							off = 1
						}
					}
					calls[obj] = append(calls[obj], callSite{c, in, off})
				}
			}
			return true
		})
	}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				collectCalls(d, nil)
				continue
			}
			if obj := info.Defs[fd.Name]; obj != nil {
				funcOf[fd] = obj
			}
			if fd.Body != nil {
				collectCalls(fd.Body, fd)
			}
		}
	}

	// assignedVars holds every object written by an assignment or ++/--. A
	// forwarding parameter that is reassigned no longer carries what its
	// callers passed, so tracing to the call sites would report a constant that
	// is not the value used.
	// Also holds objects whose ADDRESS is taken: `p := &reason; *p = x` writes
	// the parameter without ever naming it again. Same conservative closure the
	// Reason field gets.
	assignedVars := map[types.Object]bool{}
	noteWrite := func(e ast.Expr) {
		for {
			pe, ok := e.(*ast.ParenExpr)
			if !ok {
				break
			}
			e = pe.X // `(reason) = x` writes reason
		}
		if id, ok := e.(*ast.Ident); ok {
			if obj := info.Uses[id]; obj != nil {
				assignedVars[obj] = true
			}
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				if x.Tok != gotoken.DEFINE {
					for _, lhs := range x.Lhs {
						noteWrite(lhs)
					}
				}
			case *ast.IncDecStmt:
				noteWrite(x.X)
			case *ast.UnaryExpr:
				if x.Op == gotoken.AND {
					noteWrite(x.X)
				}
			case *ast.SelectorExpr:
				// `reason.setBad()` with a pointer receiver takes the address
				// implicitly and can replace the value — the same channel as
				// `&reason`, with no `&` written.
				//
				// Matched at the SELECTOR, not at the call: `m := reason.setBad`
				// takes the address without calling anything, and `(reason.setBad)()`
				// hides the selector behind parentheses. Looking only at
				// `CallExpr.Fun` missed both — the same correction interface
				// dispatch needed one screen up.
				if s := info.Selections[x]; s != nil && s.Kind() == types.MethodVal {
					if fn, ok := s.Obj().(*types.Func); ok {
						if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
							if _, isPtr := sig.Recv().Type().(*types.Pointer); isPtr {
								noteWrite(x.X)
							}
						}
					}
				}
			case *ast.RangeStmt:
				if x.Tok == gotoken.ASSIGN {
					noteWrite(x.Key)
					noteWrite(x.Value)
				}
			}
			return true
		})
	}

	// Interface dispatch is detected over EVERY selector, not only those in
	// callee position: `(i.make)(x)` parenthesises the callee and `f := i.make`
	// never calls it there at all, yet both reach the implementation.
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// BOTH selection kinds: `r.refuse(x)` is a MethodVal and
			// `Refuser.refuse(r, x)` a MethodExpr, and each dispatches
			// dynamically to the same implementations.
			if s := info.Selections[sel]; s != nil && (s.Kind() == types.MethodVal || s.Kind() == types.MethodExpr) {
				if _, isIface := s.Recv().Underlying().(*types.Interface); isIface {
					ifaceDispatched[sel.Sel.Name] = true
				}
			}
			return true
		})
	}

	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || calleeIdents[id] {
				return true
			}
			if fn, ok := info.Uses[id].(*types.Func); ok {
				usedAsValue[fn] = true
			}
			return true
		})
	}

	// paramIndex reports which parameter of fd `obj` is, or -1. Matching is by
	// OBJECT, so an unrelated local sharing a parameter's spelling resolves to a
	// different object and exempts nothing.
	paramIndex := func(fd *ast.FuncDecl, obj types.Object) int {
		if fd.Type.Params == nil {
			return -1
		}
		i := 0
		for _, field := range fd.Type.Params.List {
			for _, nm := range field.Names {
				if info.Defs[nm] == obj {
					return i
				}
				i++
			}
			if len(field.Names) == 0 {
				i++
			}
		}
		return -1
	}

	// resolve traces one expression to a declared constant. `seen` breaks
	// cycles: an unresolved or circular chain is a FAILURE, not an exemption.
	type step struct {
		fn  types.Object
		idx int
	}
	var resolve func(e ast.Expr, in *ast.FuncDecl, seen map[step]bool) error
	resolve = func(e ast.Expr, in *ast.FuncDecl, seen map[step]bool) error {
		id, ok := e.(*ast.Ident)
		if !ok {
			return fmt.Errorf("%s is not an identifier — literals, conversions and "+
				"computed expressions are not vocabulary", exprText(fset, e))
		}
		obj := info.Uses[id]
		if obj == nil {
			return fmt.Errorf("%s does not resolve to any object", id.Name)
		}
		if declared[obj] {
			return nil
		}
		v, isVar := obj.(*types.Var)
		if !isVar || in == nil {
			return fmt.Errorf("%s is not a declared refusalReason constant", id.Name)
		}
		if assignedVars[v] {
			return fmt.Errorf("%s is reassigned, or has its address taken, inside %s — it "+
				"no longer necessarily carries what callers passed", id.Name, in.Name.Name)
		}
		idx := paramIndex(in, v)
		if idx < 0 {
			return fmt.Errorf("%s is a local, not a parameter — it cannot be traced to a constant", id.Name)
		}
		fnObj := funcOf[in]
		if fnObj == nil {
			return fmt.Errorf("cannot identify the function declaring %s", id.Name)
		}
		st := step{fnObj, idx}
		if seen[st] {
			return errCyclicStep
		}
		seen[st] = true
		if fn, ok := fnObj.(*types.Func); ok {
			if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil && ifaceDispatched[fn.Name()] {
				return fmt.Errorf("%s is a method whose name is also dispatched through an "+
					"interface, so its calls cannot be enumerated and the chain cannot be "+
					"shown to reach a constant", fn.Name())
			}
		}
		// An EXPORTED forwarder can be called from outside the package, where
		// an untyped string constant converts implicitly to refusalReason. Such
		// callers are not in this graph at all.
		//
		// This package is `main` and cannot be imported, so the rule is
		// currently vacuous — it is here because it costs nothing while no
		// exported forwarder exists, and because the day one appears is exactly
		// the day nobody would think to add it.
		if fnObj.Exported() {
			return fmt.Errorf("%s is exported, so callers outside this package cannot be "+
				"enumerated and the chain cannot be shown to reach a constant", fnObj.Name())
		}
		if usedAsValue[fnObj] {
			return fmt.Errorf("%s is used as a function value, so its calls cannot be "+
				"enumerated and the chain cannot be shown to reach a constant", fnObj.Name())
		}
		sites := calls[fnObj]
		if len(sites) == 0 {
			// NARROWED DELIBERATELY: forwarding is followed through calls that
			// NAME their callee. A forwarder reached only through a function
			// value (`f := fwd; f(reasonAlpha)`) has no recorded call site and
			// lands here — reported, not exempted. Tracking function values
			// needs dataflow the rest of this audit does not have, and the
			// honest boundary is a claim that fails CLOSED at it: valid code in
			// that shape makes this gate fail loudly and be extended, while no
			// ad-hoc reason is admitted by the gap.
			return fmt.Errorf("%s forwards parameter %d, but no call to it by name was "+
				"found — the chain never reaches a constant", fnObj.Name(), idx)
		}
		// A RECURSIVE forwarder revisits its own step, and that is not the same
		// as an unrooted chain: `f(r, n-1)` called once with a constant is
		// rooted, while `cycA`/`cycB` calling only each other is not. So a
		// cyclic call site contributes no information and is skipped, and the
		// chain is refused only when EVERY site was cyclic — which is exactly
		// the unrooted case.
		rooted := 0
		for _, cs := range sites {
			argIdx := idx + cs.argOffset
			if argIdx >= len(cs.call.Args) {
				return fmt.Errorf("a call to %s passes %d arguments, fewer than parameter %d",
					fnObj.Name(), len(cs.call.Args), idx)
			}
			// Each call site is a SEPARATE path and gets its own cycle set.
			// Sharing one map let the first path mark a step and the second
			// legitimate path through the same forwarder be reported circular
			// — a false rejection that grows with how often a wrapper is used.
			branch := make(map[step]bool, len(seen)+1)
			for k := range seen {
				branch[k] = true
			}
			switch err := resolve(cs.call.Args[argIdx], cs.in, branch); {
			case err == nil:
				rooted++
			case errors.Is(err, errCyclicStep):
				// this path returned to a step already being resolved
			default:
				return fmt.Errorf("via %s at %s: %w", fnObj.Name(),
					fset.Position(cs.call.Pos()), err)
			}
		}
		if rooted == 0 {
			return fmt.Errorf("every call to %s parameter %d is part of a forwarding cycle — "+
				"the chain never reaches a constant", fnObj.Name(), idx)
		}
		return nil
	}

	// Each declaration is scanned in ITS OWN context. An earlier version fell
	// back to scanning the whole FILE whenever a declaration was not a
	// function, so every literal in that file lost its enclosing function and
	// no forwarding parameter could be resolved.
	// isRefusalType asks go/types, not the spelling. An ALIAS for
	// backendRefusal is the same type and must be audited; a local type that
	// merely shares the name is a different type and must not be.
	isRefusalType := func(t types.Type) bool {
		// Unalias first: with materialized aliases an alias for backendRefusal
		// is a *types.Alias, not the *types.Named it denotes.
		named, ok := types.Unalias(t).(*types.Named)
		return ok && named.Obj().Name() == "backendRefusal" && named.Obj().Pkg() == pkg
	}
	// isReasonField identifies `x.Reason` where the field really is
	// backendRefusal's. The claim is about every value ASSIGNED to Reason, and
	// a composite literal is only one way to assign one.
	// reasonField is backendRefusal's own Reason field, resolved once.
	var reasonField types.Object
	if obj := pkg.Scope().Lookup("backendRefusal"); obj != nil {
		if st, ok := obj.Type().Underlying().(*types.Struct); ok {
			for i := 0; i < st.NumFields(); i++ {
				if st.Field(i).Name() == "Reason" {
					reasonField = st.Field(i)
				}
			}
		}
	}
	// isReasonField compares the selected FIELD OBJECT, not the receiver type.
	// Matching on the receiver missed promotion: when backendRefusal is
	// embedded, `outer.Reason` selects backendRefusal's own field while the
	// receiver is the outer struct, so the assignment left the audit entirely.
	isReasonField := func(sel *ast.SelectorExpr) bool {
		s := info.Selections[sel]
		return s != nil && s.Kind() == types.FieldVal && reasonField != nil && s.Obj() == reasonField
	}
	// reasonTargetIn reports the Reason field being WRITTEN by an assignment
	// target, unwrapping only parentheses.
	//
	// It deliberately does NOT search the target for any Reason selector
	// anywhere: in `m[r.Reason] = true` the field is READ as a map key and
	// nothing is written to it, and treating that as a write made the audit
	// reject unrelated valid code.
	reasonTargetIn := func(e ast.Expr) *ast.SelectorExpr {
		for {
			if pe, ok := e.(*ast.ParenExpr); ok {
				e = pe.X
				continue
			}
			sel, ok := e.(*ast.SelectorExpr)
			if ok && isReasonField(sel) {
				return sel
			}
			return nil
		}
	}
	var checkIn func(root ast.Node, in *ast.FuncDecl)
	checkIn = func(root ast.Node, in *ast.FuncDecl) {
		ast.Inspect(root, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok {
				for i, lhs := range as.Lhs {
					sel := reasonTargetIn(lhs)
					if sel == nil {
						continue
					}
					// The target may be the selector itself or an expression
					// AROUND it — `*(&r.Reason) = x`. Either way the value
					// written is as.Rhs[i], so it is traced the same way. An
					// earlier version additionally refused the indirect form;
					// a mutation showed nothing witnessed that strictness, and
					// it was wrong on its own terms, since assigning a declared
					// constant through a pointer assigns a declared constant.
					out.sites++
					// ONLY `=` can be traced. `r.Reason += reasonAlpha` has a
					// declared constant on the right and still yields a value
					// depending on whatever Reason already held, so resolving
					// the RHS alone would report a constant that is not the
					// value assigned.
					if as.Tok != gotoken.ASSIGN {
						out.findings = append(out.findings, fmt.Sprintf(
							"%s: Reason updated with %s, whose result depends on the previous "+
								"value — only a plain assignment can trace to a declared constant",
							fset.Position(lhs.Pos()), as.Tok))
						continue
					}
					if len(as.Rhs) != len(as.Lhs) {
						out.findings = append(out.findings, fmt.Sprintf(
							"%s: Reason assigned from a multi-value expression, which "+
								"cannot be traced to a declared constant", fset.Position(lhs.Pos())))
						continue
					}
					if err := resolve(as.Rhs[i], in, map[step]bool{}); err != nil {
						out.findings = append(out.findings, fmt.Sprintf(
							"%s: Reason assigned a value that does not trace to a declared constant: %v",
							fset.Position(lhs.Pos()), err))
					}
				}
				return true
			}
			// Taking the ADDRESS of Reason opens a write channel this audit
			// cannot follow: `p := &r.Reason` and a later `*p = x` share no
			// syntax with the field at all. Chasing pointer aliases needs
			// dataflow analysis; refusing the address is the conservative
			// closure, and it fails closed — no real refusal needs one.
			// Go takes that address IMPLICITLY for a pointer-receiver method
			// call on an addressable field: `r.Reason.set()` mutates Reason
			// without an `&` anywhere. Same channel, different syntax.
			// Matched at the SELECTOR, for the same reason the parameter case
			// is: `m := r.Reason.setBad` takes the address without calling
			// anything, and parentheses hide the selector from `CallExpr.Fun`.
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if recvSel := reasonTargetIn(sel.X); recvSel != nil {
					// The receiver comes from the METHOD OBJECT: a MethodVal
					// selection's own Type() has the receiver removed, so
					// reading it there always reports none.
					if s := info.Selections[sel]; s != nil && s.Kind() == types.MethodVal {
						fn, _ := s.Obj().(*types.Func)
						sig, _ := fn.Type().(*types.Signature)
						if sig != nil && sig.Recv() != nil {
							if _, isPtr := sig.Recv().Type().(*types.Pointer); isPtr {
								out.sites++
								out.findings = append(out.findings, fmt.Sprintf(
									"%s: a pointer-receiver method is taken on Reason, which takes "+
										"its address implicitly and allows writes this audit cannot trace",
									fset.Position(sel.Pos())))
							}
						}
					}
				}
			}
			if ue, ok := n.(*ast.UnaryExpr); ok && ue.Op == gotoken.AND {
				if sel := reasonTargetIn(ue.X); sel != nil {
					out.sites++
					out.findings = append(out.findings, fmt.Sprintf(
						"%s: the address of Reason is taken, which allows writes this audit "+
							"cannot trace to a declared constant", fset.Position(ue.Pos())))
				}
				return true
			}
			// `for r.Reason = range xs` assigns each element in turn. No
			// element of a runtime collection is a declared constant, so this
			// is refused outright rather than traced.
			if rs, ok := n.(*ast.RangeStmt); ok && rs.Tok == gotoken.ASSIGN {
				for _, target := range []ast.Expr{rs.Key, rs.Value} {
					if target == nil || reasonTargetIn(target) == nil {
						continue
					}
					sel := reasonTargetIn(target)
					out.sites++
					out.findings = append(out.findings, fmt.Sprintf(
						"%s: Reason assigned by ranging over a collection, whose elements "+
							"are not declared constants", fset.Position(sel.Pos())))
				}
				return true
			}
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			tv, ok := info.Types[cl]
			if !ok || !isRefusalType(tv.Type) {
				return true
			}
			out.sites++
			// A POSITIONAL literal is legal Go and assigns Reason by position:
			// `backendRefusal{reasonPrim, "llvm", ...}`. Skipping non-keyed
			// elements reported it as having no Reason at all.
			if len(cl.Elts) > 0 {
				if _, keyed := cl.Elts[0].(*ast.KeyValueExpr); !keyed {
					idx := -1
					if st, ok := types.Unalias(tv.Type).Underlying().(*types.Struct); ok {
						for i := 0; i < st.NumFields(); i++ {
							if st.Field(i) == reasonField {
								idx = i
							}
						}
					}
					if idx >= 0 && idx < len(cl.Elts) {
						if err := resolve(cl.Elts[idx], in, map[step]bool{}); err != nil {
							out.findings = append(out.findings, fmt.Sprintf(
								"%s: Reason does not trace to a declared constant: %v",
								fset.Position(cl.Elts[idx].Pos()), err))
						}
						return true
					}
				}
			}
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Reason" {
					if err := resolve(kv.Value, in, map[step]bool{}); err != nil {
						out.findings = append(out.findings, fmt.Sprintf(
							"%s: Reason does not trace to a declared constant: %v",
							fset.Position(kv.Pos()), err))
					}
					return true
				}
			}
			out.findings = append(out.findings, fmt.Sprintf(
				"%s: backendRefusal constructed with NO Reason — every refusal "+
					"must name one, or callers cannot branch on it", fset.Position(cl.Pos())))
			return true
		})
	}
	// ZERO-VALUED REFUSALS, as ONE rule rather than a list of creation forms.
	// `var r backendRefusal`, `new(backendRefusal)`, a named result
	// `(r backendRefusal)`, `[1]backendRefusal` and `make([]backendRefusal, 1)`
	// all produce a refusal whose Reason is the empty string — undeclared, and
	// nothing a caller can branch on. Enumerating those spellings is the game
	// this audit already lost twice, so the rule is stated over the TYPE: a
	// VALUE of backendRefusal may only come from a composite literal. The type
	// may be named freely behind a pointer, which is how it is declared,
	// received, and type-asserted.
	exempt := map[ast.Expr]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.StarExpr:
				exempt[x.X] = true
			case *ast.CompositeLit:
				if x.Type != nil {
					exempt[x.Type] = true
				}
			case *ast.TypeSpec:
				// `type Alias = backendRefusal` NAMES the type; it creates no
				// value. Without this the declaration itself was reported as a
				// zero-valued refusal, which also made the alias control below
				// pass for a reason that had nothing to do with its Reason.
				exempt[x.Type] = true
			}
			return true
		})
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || exempt[ast.Expr(id)] {
				return true
			}
			tn, ok := info.Uses[id].(*types.TypeName)
			if !ok || !isRefusalType(tn.Type()) {
				return true
			}
			out.sites++
			out.findings = append(out.findings, fmt.Sprintf(
				"%s: a backendRefusal VALUE is named here; only a composite literal can "+
					"give it a Reason, so any other creation leaves the zero value",
				fset.Position(id.Pos())))
			return true
		})
	}

	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				checkIn(fd, fd)
				continue
			}
			checkIn(d, nil) // package-level: no enclosing function to forward through
		}
	}
	return out, nil
}

func exprText(fset *gotoken.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := goprinter.Fprint(&b, fset, e); err != nil {
		return "<expr>"
	}
	return b.String()
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

// A minimal package carrying the same relation as the real one: a refusalReason
// type, two package-scope constants, and the struct. Each control appends its
// own declarations to it. Nothing here imports anything, so the audit's type
// check needs no resolvable dependencies.
const reasonAuditPreamble = `package p

type refusalReason string

const (
	reasonAlpha refusalReason = "alpha"
	reasonBeta  refusalReason = "beta"
)

type backendRefusal struct {
	Reason  refusalReason
	Backend string
	Detail  string
	Help    string
}

func (e *backendRefusal) Error() string { return e.Backend }
`

func runReasonAudit(t *testing.T, extra string) reasonAudit {
	t.Helper()
	fset := gotoken.NewFileSet()
	f, err := parser.ParseFile(fset, "control.go", reasonAuditPreamble+extra, 0)
	if err != nil {
		t.Fatalf("the control itself does not parse: %v", err)
	}
	got, err := auditReasonClosure(fset, []*ast.File{f}, "p")
	if err != nil {
		t.Fatalf("the control itself does not type-check: %v", err)
	}
	// At LEAST the preamble's two, so a control that declares its own extra
	// vocabulary is not mistaken for a broken scan.
	if got.declared < 2 {
		t.Fatalf("preamble declares 2 reasons, audit saw %d — the control is not "+
			"exercising what it claims", got.declared)
	}
	return got
}

// TestReasonClosureAuditDiscriminates is the CONTROL SET for the audit above.
//
// The audit's whole value is that it accepts exactly the constant-rooted chains
// and rejects everything else, and a green run over the real package cannot
// show that: a check that accepted every expression would also be green. So
// each row states a shape and whether the audit must reject it, and the pairs
// are chosen to differ in ONE respect — the same forwarder shape with a
// constant argument and with a non-constant one, the same parameter name bound
// as a parameter and as an unrelated local.
//
// Three of these are the specific defects that got past earlier versions of
// this gate, which is why they are rows and not prose.
func TestReasonClosureAuditDiscriminates(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		reject bool
	}{
		// --- must be REJECTED ---
		{"mutable var of the reason type", `
var mutableReason refusalReason = "invented"
var _ = &backendRefusal{Reason: mutableReason, Backend: "x"}`, true},

		{"unrelated local sharing a forwarded parameter's name", `
func fwd(reason refusalReason) error { return &backendRefusal{Reason: reason, Backend: "x"} }
var _ = fwd(reasonAlpha)

func other() error {
	reason := refusalReason("invented")
	return &backendRefusal{Reason: reason, Backend: "x"}
}`, true},

		// The row above is not enough on its own, and a mutation showed it: its
		// `other` has NO parameters, so an audit matching parameters by NAME
		// instead of by object still rejects it, and the whole table stayed
		// green under that mutation. Only a local SHADOWING a parameter of the
		// same name separates the two — under name matching this traces to
		// `shadow`'s constant call site and is wrongly accepted.
		{"local shadowing a parameter of the same name", `
func shadow(reason refusalReason) error {
	if reason != reasonBeta {
		reason := refusalReason("invented")
		return &backendRefusal{Reason: reason, Backend: "x"}
	}
	return nil
}
var _ = shadow(reasonAlpha)`, true},

		{"forwarder at nonzero position, argument not constant", `
func fwdN(detail string, reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = fwdN("d", refusalReason("invented"))`, true},

		{"forwarding through the WRONG parameter", `
func two(a, b refusalReason) error { return &backendRefusal{Reason: b, Backend: "x"} }
var _ = two(reasonAlpha, refusalReason("invented"))`, true},

		{"string literal", `
var _ = &backendRefusal{Reason: "invented", Backend: "x"}`, true},

		{"conversion of a computed expression", `
func cast(s string) error { return &backendRefusal{Reason: refusalReason(s + "!"), Backend: "x"} }`, true},

		{"no Reason field at all", `
var _ = &backendRefusal{Backend: "x", Detail: "y"}`, true},

		{"forwarder nobody calls", `
func orphan(reason refusalReason) error { return &backendRefusal{Reason: reason, Backend: "x"} }`, true},

		{"mutually recursive forwarders, no constant root", `
func cycA(r refusalReason) error { return cycB(r) }
func cycB(r refusalReason) error {
	if r == "" {
		return cycA(r)
	}
	return &backendRefusal{Reason: r, Backend: "x"}
}`, true},

		// --- must be ACCEPTED ---
		{"direct declared constant", `
var _ = &backendRefusal{Reason: reasonAlpha, Backend: "x"}`, false},

		{"multi-hop forwarding, nonzero position, constant at the root", `
func hop2(d string, reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}
func hop1(reason refusalReason) error { return hop2("d", reason) }
var _ = hop1(reasonBeta)`, false},

		{"two-parameter forwarder where THAT parameter is constant", `
func twoOK(a, b refusalReason) error { return &backendRefusal{Reason: b, Backend: "x"} }
var _ = twoOK(refusalReason("invented"), reasonAlpha)`, false},

		{"every call site constant, several of them", `
func many(reason refusalReason) error { return &backendRefusal{Reason: reason, Backend: "x"} }
var _ = many(reasonAlpha)
var _ = many(reasonBeta)`, false},

		// --- shapes review found the first version of this audit mishandled ---

		{"one forwarder reached twice through the same wrapper", `
func inner(reason refusalReason) error { return &backendRefusal{Reason: reason, Backend: "x"} }
func wrapper(reason refusalReason) error {
	if reason == reasonBeta {
		return inner(reason)
	}
	return inner(reason)
}
var _ = wrapper(reasonAlpha)`, false},

		{"Reason assigned after construction", `
func mutateAfter() error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	r.Reason = refusalReason("invented")
	return r
}`, true},

		{"construction through a type alias", `
type refusalAlias = backendRefusal

var _ = &refusalAlias{Reason: "invented", Backend: "x"}`, true},

		// A method EXPRESSION passes the receiver as argument 0, so parameter i
		// sits at argument i+1. The method-value row below does not shift and
		// cannot detect a missing offset.
		{"method expression forwarder, receiver shifts the arguments", `
type mexpr struct{}

func (mexpr) build(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}

var _ = mexpr.build(mexpr{}, reasonAlpha)`, false},

		{"method forwarder called with a constant", `
type maker struct{}

func (maker) make(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}

var _ = maker{}.make(reasonAlpha)`, false},

		{"compound assignment to Reason", `
func compound() error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	r.Reason += reasonBeta
	return r
}`, true},

		{"promoted Reason through an embedded backendRefusal", `
type outer struct {
	backendRefusal
	extra string
}

func promote(o *outer) { o.Reason = refusalReason("invented") }`, true},

		{"Reason assigned by ranging over a map", `
func ranged(m map[refusalReason]bool) error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	for r.Reason = range m {
	}
	return r
}`, true},

		{"parenthesised method expression", `
type pexpr struct{}

func (pexpr) build(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}

var _ = (pexpr.build)(pexpr{}, reasonAlpha)`, false},

		{"indirect assignment of an ad-hoc value", `
func indirect() error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	*(&r.Reason) = refusalReason("invented")
	return r
}`, true},

		// Refused even though the value IS declared: what is refused is taking
		// the address, because the same address can be written again anywhere.
		{"the address of Reason taken at all", `
func addr() error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	*(&r.Reason) = reasonBeta
	return r
}`, true},

		{"pointer alias stored, then written through", `
func aliased() error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	p := &r.Reason
	*p = refusalReason("invented")
	return r
}`, true},

		{"forwarder that reassigns its own parameter", `
func reassign(reason refusalReason) error {
	reason = refusalReason("invented")
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = reassign(reasonAlpha)`, true},

		// Reason is READ here as a map key; nothing is written to the field.
		{"Reason read as a map key in an assignment target", `
func mapKey(m map[refusalReason]bool) error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	m[r.Reason] = true
	return r
}`, false},

		{"constant declared through an alias of the reason type", `
type reasonAliasTy = refusalReason

const reasonGamma reasonAliasTy = "gamma"

var _ = &backendRefusal{Reason: reasonGamma, Backend: "x"}`, false},

		{"recursive forwarder entered with a constant", `
func recur(reason refusalReason, n int) error {
	if n > 0 {
		return recur(reason, n-1)
	}
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = recur(reasonAlpha, 3)`, false},

		{"forwarder also called through a function value", `
func viaValue(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}

var _ = viaValue(reasonAlpha)

func callIndirectly() {
	alias := viaValue
	_ = alias(refusalReason("invented"))
}`, true},

		{"address of a forwarding parameter taken", `
func addrParam(reason refusalReason) error {
	p := &reason
	*p = refusalReason("invented")
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = addrParam(reasonAlpha)`, true},

		// A function literal is NOT modelled as a callable context, so a reason
		// drawn from its own PARAMETER resolves as a local and is refused —
		// the conservative side of a boundary this audit does not cross.
		//
		// A CAPTURED variable is different and must still resolve: it is the
		// enclosing function's parameter, and closing over it does not change
		// what the caller passed. An earlier attempt scanned literal bodies
		// with the enclosing context cleared, which refused captures too; it
		// changed no verdict the controls covered, and the capture row below is
		// what showed it was wrong rather than merely redundant.
		{"reason drawn from a function-literal parameter", `
func closureCtx() error {
	f := func(reason refusalReason) error {
		return &backendRefusal{Reason: reason, Backend: "x"}
	}
	return f(reasonAlpha)
}`, true},

		{"reason CAPTURED by a function literal", `
func captured(reason refusalReason) error {
	f := func() error {
		return &backendRefusal{Reason: reason, Backend: "x"}
	}
	return f()
}
var _ = captured(reasonAlpha)`, false},

		{"zero-valued refusal declared with var", `
func zeroVar() error {
	var r backendRefusal
	return &r
}`, true},

		{"refusal allocated with new", `
func zeroNew() error { return new(backendRefusal) }`, true},

		{"zero-valued refusal as a named result", `
func namedResult() (r backendRefusal) { return }`, true},

		{"zero-valued refusals in an array", `
var refusalTable [2]backendRefusal

func fromTable() error { return &refusalTable[0] }`, true},

		{"pointer-receiver method called on Reason", `
func (p *refusalReason) set() { *p = refusalReason("invented") }

func implicitAddr() error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	r.Reason.set()
	return r
}`, true},

		{"forwarding method also reached through an interface", `
type refuser interface{ refuse(refusalReason) error }

type concrete struct{}

func (concrete) refuse(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}

var _ = concrete{}.refuse(reasonAlpha)

func viaIface(r refuser) { _ = r.refuse(refusalReason("invented")) }`, true},

		{"forwarding parameter written through parentheses", `
func parenWrite(reason refusalReason) error {
	(reason) = refusalReason("invented")
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = parenWrite(reasonAlpha)`, true},

		{"forwarding parameter mutated by a pointer-receiver method", `
func (p *refusalReason) setBad() { *p = refusalReason("invented") }

func implicitParamWrite(reason refusalReason) error {
	reason.setBad()
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = implicitParamWrite(reasonAlpha)`, true},

		{"interface dispatch written as a method expression", `
type refuserME interface{ refuseME(refusalReason) error }

type concreteME struct{}

func (concreteME) refuseME(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}

var _ = concreteME{}.refuseME(reasonAlpha)

func viaMethodExpr(r refuserME) { _ = refuserME.refuseME(r, refusalReason("invented")) }`, true},

		{"positional literal with a declared constant", `
var _ = &backendRefusal{reasonAlpha, "x", "detail", "help"}`, false},

		{"positional literal with an ad-hoc value", `
var _ = &backendRefusal{refusalReason("invented"), "x", "detail", "help"}`, true},

		{"valid construction through a type alias", `
type refusalAliasOK = backendRefusal

var _ = &refusalAliasOK{Reason: reasonAlpha, Backend: "x"}`, false},

		{"interface method stored as a value, then called", `
type refuserStored interface{ refuseStored(refusalReason) error }

type concreteStored struct{}

func (concreteStored) refuseStored(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}

var _ = concreteStored{}.refuseStored(reasonAlpha)

func viaStored(r refuserStored) {
	f := r.refuseStored
	_ = f(refusalReason("invented"))
}`, true},

		{"exported forwarder", `
func Exported(reason refusalReason) error {
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = Exported(reasonAlpha)`, true},

		{"pointer-receiver method captured as a value", `
func (p *refusalReason) setViaValue() { *p = refusalReason("invented") }

func capturedMethod(reason refusalReason) error {
	m := reason.setViaValue
	m()
	return &backendRefusal{Reason: reason, Backend: "x"}
}
var _ = capturedMethod(reasonAlpha)`, true},

		{"pointer-receiver method captured from Reason", `
func (p *refusalReason) setFromField() { *p = refusalReason("invented") }

func capturedFromField() error {
	r := &backendRefusal{Reason: reasonAlpha, Backend: "x"}
	m := r.Reason.setFromField
	m()
	return r
}`, true},

		{"one call site of many is NOT constant", `
func mixed(reason refusalReason) error { return &backendRefusal{Reason: reason, Backend: "x"} }
var _ = mixed(reasonAlpha)
var _ = mixed(refusalReason("invented"))`, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runReasonAudit(t, c.src)
			if got.sites == 0 {
				t.Fatalf("the control constructed no backendRefusal, so it tested nothing")
			}
			if c.reject && len(got.findings) == 0 {
				t.Errorf("audit ACCEPTED a shape it must reject (%d sites scanned)", got.sites)
			}
			if !c.reject && len(got.findings) > 0 {
				t.Errorf("audit REJECTED a legitimate shape: %v", got.findings)
			}
		})
	}
}

// The preamble alone must be silent AND site-free. Without this, a bug that made
// the audit report a finding for the shared scaffolding would make every
// rejection row pass for the wrong reason.
func TestReasonAuditControlScaffoldingIsInert(t *testing.T) {
	got := runReasonAudit(t, "\n")
	if len(got.findings) != 0 || got.sites != 0 {
		t.Fatalf("the shared control preamble is not inert: %d sites, findings %v",
			got.sites, got.findings)
	}
}

// scanUnauditedFile looks for refusal traffic in a file the audit cannot
// type-check, and it is deliberately STRUCTURAL and conservative.
//
// Its predecessor searched for the substring "backendRefusal{", which is the
// contamination-check mistake this repo has made before: it proved a spelling
// was absent, not that the file was refusal-free. `r.Reason = ...` and a
// construction through an alias both evade it completely.
//
// Without type information it cannot decide whether a `Reason` field belongs to
// backendRefusal, so it does not try. It flags any composite literal carrying a
// `Reason:` key, any assignment to a `.Reason` selector, and any mention of
// backendRefusal at all. A false positive here costs one line — bring the file
// into a configuration the audit can type-check, or rename the field — while a
// false negative silently exempts a whole build.
func scanUnauditedFile(fset *gotoken.FileSet, f *ast.File, aliases, forwarders map[string]bool) []string {
	var found []string
	at := func(n ast.Node, what string) {
		found = append(found, fmt.Sprintf("%s: %s", fset.Position(n.Pos()), what))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			// refusalReason too: an excluded file calling an audited forwarder
			// mentions neither backendRefusal nor a Reason field, and
			// `refusalReason("invented")` is how it would mint an ad-hoc value.
			//
			// A forwarder passed an untyped string constant converts implicitly
			// and names no TYPE — but it still names the FORWARDER, which the
			// `forwarders` check above flags. The only residue left is a forwarder
			// reached without ever naming it (through reflection or an interface
			// method in an excluded file), which no parse-only scan can see; the
			// count of excluded files is logged rather than assumed zero for that
			// vanishing case.
			if x.Name == "backendRefusal" || x.Name == "refusalReason" || aliases[x.Name] {
				at(x, "mentions "+x.Name)
			}
			// A refusal forwarder (a function taking a refusalReason) named here —
			// called, or taken as a value — reaches the vocabulary even with an
			// untyped string argument that names no type. This is what closes the
			// residual boundary the comment below used to leave open.
			if forwarders[x.Name] {
				at(x, "names the refusal forwarder "+x.Name)
			}
		case *ast.CompositeLit:
			for _, el := range x.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Reason" {
						at(kv, "constructs a value with a Reason field")
					}
				}
			}
		case *ast.SelectorExpr:
			// ANY `.Reason` selector, wherever it appears. Enumerating write
			// FORMS was a losing game here — plain, parenthesised, ranged, and
			// `*(&x.Reason)` each needed their own case, and each was found
			// missing one at a time. Without type information this scan cannot
			// tell a write from a read anyway, so it flags the mention and lets
			// the author bring the file into an auditable configuration.
			if x.Sel.Name == "Reason" {
				at(x, "mentions a Reason field")
			}
		}
		return true
	})
	return found
}

// TestUnauditedFileScanSeesStructureNotSpelling is the control set for the scan
// above: each row is a shape that evaded the substring check it replaced.
func TestUnauditedFileScanSeesStructureNotSpelling(t *testing.T) {
	cases := []struct {
		name string
		src  string
		flag bool
	}{
		{"assignment to Reason", `package p
type r struct{ Reason string }
func f(x *r) { x.Reason = "invented" }`, true},
		{"compound assignment to Reason", `package p
type r struct{ Reason string }
func f(x *r) { x.Reason += "invented" }`, true},
		{"construction through an alias", `package p
type alias = backendRefusal
var _ = &alias{Reason: "invented"}`, true},
		{"construction with no recognisable spelling", `package p
func f() interface{} { return &someAlias{Reason: "invented"} }`, true},
		{"range assignment to Reason", `package p
type r struct{ Reason string }
func f(x *r, m map[string]bool) {
	for x.Reason = range m {
	}
}`, true},
		{"parenthesised assignment target", `package p
type r struct{ Reason string }
func f(x *r) { (x.Reason) = "invented" }`, true},
		{"mints a reason by conversion", `package p
func f() interface{} { return mk(refusalReason("invented")) }`, true},
		{"uses an alias declared in an audited file", `package p
var _ = myRefusalAlias{}`, true},
		{"a bare mention", `package p
func f() *backendRefusal { return nil }`, true},
		{"calls a forwarder with an untyped string", `package p
func f() interface{} { return llvmUnsupported("invented") }`, true},
		{"takes a forwarder as a value", `package p
var alias = llvmUnsupported`, true},
		{"calls a method forwarder", `package p
func f(x T) interface{} { return x.reject("invented") }`, true},
		{"an unrelated call by a different name", `package p
func f() { somethingElse("fine") }`, false},
		{"nothing refusal-related", `package p
type other struct{ Cause string }
func f(x *other) { x.Cause = "fine" }`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fset := gotoken.NewFileSet()
			// Parsed only — these are files the audit cannot type-check, which
			// is the whole reason this scan exists.
			f, err := parser.ParseFile(fset, "x.go", c.src, 0)
			if err != nil {
				t.Fatalf("the control does not parse: %v", err)
			}
			got := scanUnauditedFile(fset, f, map[string]bool{"myRefusalAlias": true}, map[string]bool{"llvmUnsupported": true, "reject": true})
			if c.flag && len(got) == 0 {
				t.Errorf("scan missed refusal traffic it must flag")
			}
			if !c.flag && len(got) > 0 {
				t.Errorf("scan flagged an unrelated file: %v", got)
			}
		})
	}
}

// errCyclicStep marks a resolution path that returned to a (function,
// parameter) step already being resolved. It is a PATH outcome, not a verdict:
// the caller decides, and a chain is refused only when no path escapes.
var errCyclicStep = errors.New("forwarding cycle")
