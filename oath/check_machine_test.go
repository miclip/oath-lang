package main

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCtorRouteDerivesFromInferReady. The route decision must not become a
// second copy of `tyvars > 0 && len(tyargs) == 0`. A copy is correct exactly
// once, and when it drifts the machine and the recursive checker disagree about
// which PATH a constructor takes — which the differential would report as a
// moved hash with no hint that the route was the thing that diverged.
//
// Asserted over the whole matrix rather than a couple of examples, so the two
// cannot agree by coincidence on the cases someone happened to write down.
func TestCtorRouteDerivesFromInferReady(t *testing.T) {
	tyargs := func(n int) []Ty {
		out := make([]Ty, n)
		for i := range out {
			out[i] = Ty{K: "int"}
		}
		return out
	}
	checked := 0
	for tyvars := 0; tyvars <= 3; tyvars++ {
		for n := 0; n <= 3; n++ {
			d := &Def{K: "data", TyVars: tyvars}
			term := &Term{K: "ctor", TyArgs: tyargs(n)}
			got := ctorRouteFor(d, term)
			want := routeValidateOnly
			if inferReady(tyvars, term.TyArgs) {
				want = routeInferSolveValidate
			}
			if got != want {
				t.Errorf("tyvars=%d tyargs=%d: route %v, inferReady says %v",
					tyvars, n, got, want)
			}
			checked++
		}
	}
	if checked != 16 {
		t.Fatalf("matrix did not run: %d combinations", checked)
	}
}

// TestCtorRouteSplitsTheTwoWitnesses pins the split the port must not collapse,
// on the actual corpus datatypes the two #149 witnesses use.
//
//	Str  / SCons   tyvars = 0  -> validate-only, however long the literal
//	List / Cons    tyvars = 1  -> infer-solve-validate, unless written explicitly
//
// This is why the 5,000-rune string is a pure STACK-SAFETY witness with no
// inference involved, and cannot say anything about the memo — and why the
// polymorphic spine is needed separately.
func TestCtorRouteSplitsTheTwoWitnesses(t *testing.T) {
	st := canonicalStore(t)

	strHash, _, ok := st.FindCtor("SCons")
	if !ok {
		t.Fatal("corpus has no SCons; the monomorphic witness cannot be checked")
	}
	strDef, err := st.GetDef(strHash)
	if err != nil {
		t.Fatal(err)
	}
	if strDef.TyVars != 0 {
		t.Fatalf("setup: Str has %d type parameters, so it is no longer the monomorphic witness", strDef.TyVars)
	}
	if got := ctorRouteFor(strDef, &Term{K: "ctor", Hash: strHash}); got != routeValidateOnly {
		t.Errorf("SCons must take the validate-only route, got %v", got)
	}

	listHash, _, ok := st.FindCtor("Cons")
	if !ok {
		t.Fatal("corpus has no Cons; the polymorphic witness cannot be checked")
	}
	listDef, err := st.GetDef(listHash)
	if err != nil {
		t.Fatal(err)
	}
	if listDef.TyVars == 0 {
		t.Fatalf("setup: List has no type parameters, so it is no longer the polymorphic witness")
	}
	if got := ctorRouteFor(listDef, &Term{K: "ctor", Hash: listHash}); got != routeInferSolveValidate {
		t.Errorf("Cons with omitted type arguments must take the three-stage route, got %v", got)
	}
	// Written explicitly, the same constructor has nothing to infer.
	explicit := &Term{K: "ctor", Hash: listHash, TyArgs: []Ty{{K: "int"}}}
	if got := ctorRouteFor(listDef, explicit); got != routeValidateOnly {
		t.Errorf("Cons with explicit type arguments must take the validate-only route, got %v", got)
	}
}

// TestCheckerMachineIsNotRouted enforces step 2's whole point: the machine is
// DECLARED and unreachable. The port proceeds family by family against the
// frozen fixture, and a stray production reference would start mixing machines
// without anything saying so.
//
// Resolved through the AST rather than by grepping text, so a mention inside a
// comment or a string does not read as a use — the same distinction
// boundary_test.go draws between names and bindings.
func TestCheckerMachineIsNotRouted(t *testing.T) {
	machineDecls := map[string]bool{
		"checkerMachine": true, "checkerStep": true, "checkerFrame": true,
		"checkResult": true, "checkMode": true, "ctorRoute": true,
		"ctorRouteFor": true, "routeValidateOnly": true, "routeInferSolveValidate": true,
		"ctorFrame": true, "eqFrame": true, "lamBodyFrame": true, "fieldAccessFrame": true,
		"appArgFrame": true, "ifBranchFrame": true, "letBodyFrame": true,
		"matchArmFrame": true, "primArgFrame": true, "recordFieldFrame": true,
		"notPorted": true, "errFramePending": true,
	}

	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files found; this check did not run (%v)", err)
	}
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "check_machine.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		file, err := parser.ParseFile(gotoken.NewFileSet(), f, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && machineDecls[id.Name] {
				t.Errorf("%s references %s — the checker machine must stay unrouted until "+
					"its family is ported (#149 step 3). If this is the port, remove this "+
					"check's exemption deliberately rather than widening it.", f, id.Name)
			}
			return true
		})
		scanned++
	}
	if scanned < 20 {
		t.Fatalf("only %d production files scanned; the glob did not find the package", scanned)
	}
	t.Logf("machine is unrouted across %d production files", scanned)
}
