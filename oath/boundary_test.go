package main

// The #114 boundary, as a test rather than as a comment.
//
// program.go is the backend-neutral layer and compile.go is the Go backend. The
// dependency is meant to run one way:
//
//	Oath semantics -> neutral requirements -> backend provider
//
// A comment saying so is a hope. This checks it, because the failure mode is
// quiet: adding one helper to the wrong file compiles, passes every other test,
// and is only discovered by whoever writes the second backend — at which point
// the boundary has to be renegotiated rather than used.
//
// The question this answers is the one to ask of #115 before starting it: can a
// second backend be written against the neutral program description alone? If
// program.go reaches into the Go emitter for anything, the honest answer is no.
//
// Go's single-package layout makes this invisible to the compiler — there are no
// imports between files to constrain — so the check has to be explicit.
//
// IT RESOLVES BINDINGS RATHER THAN MATCHING NAMES, which matters in both
// directions and was worth the extra machinery:
//
//   - a local variable, parameter, or field in program.go that merely SPELLS the
//     same as a backend declaration is not a dependency, and failing on it would
//     make the guard something people learn to work around;
//   - a METHOD defined in compile.go on a shared type — say `func (p
//     *CompiledProgram) somethingGoish()` — IS a dependency, and name matching
//     would miss it entirely, because a receiver type does not say which file its
//     methods live in. That is a direct path for the exact thing this forbids.
//
// So each identifier is resolved to the object it binds to, and the object's
// declaring FILE is what the check reads.

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	gotoken "go/token"
	"go/types"
	"path/filepath"
	"testing"
)

const (
	neutralFile = "program.go"
	backendFile = "compile.go"
)

// resolvedUses type-checks the package as the Go build actually sees it (build
// constraints honoured, so the wasm and cloud-tagged files do not collide) and
// returns, for every identifier used in `file`, the file that declares it.
func resolvedUses(t *testing.T, file string) map[*ast.Ident]string {
	t.Helper()
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("reading package: %v", err)
	}
	fset := gotoken.NewFileSet()
	var files []*ast.File
	var target *ast.File
	for _, name := range pkg.GoFiles {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
		if name == file {
			target = f
		}
	}
	if target == nil {
		t.Fatalf("%s is not in the package's Go files", file)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{
		Importer: importer.Default(),
		// Type errors are tolerated rather than fatal: this test is about where
		// declarations live, and a build that genuinely does not compile is
		// already failing everywhere else.
		Error: func(error) {},
	}
	_, _ = conf.Check("oath", fset, files, info)

	out := map[*ast.Ident]string{}
	ast.Inspect(target, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := info.Uses[id]
		if obj == nil || !obj.Pos().IsValid() {
			return true
		}
		out[id] = filepath.Base(fset.Position(obj.Pos()).Filename)
		return true
	})
	if len(out) == 0 {
		t.Fatalf("resolved no identifiers in %s; the check is not reading anything", file)
	}
	return out
}

// The neutral layer must not reach into the Go backend.
//
// If this fails, the fix is almost never to add an exception: it is that
// something backend-neutral was written into compile.go and belongs in
// program.go, or that something genuinely Go-specific leaked into program.go and
// belongs in the backend. Deciding which is the whole discipline — ask whether
// the thing describes Oath or describes Go.
func TestNeutralLayerDoesNotDependOnTheGoBackend(t *testing.T) {
	uses := resolvedUses(t, neutralFile)

	// Sanity: a check that resolved nothing interesting would pass forever.
	sawNeutral := false
	for id, decl := range uses {
		if decl == neutralFile && id.Name == "CapabilityRequirement" {
			sawNeutral = true
		}
	}
	if !sawNeutral {
		t.Fatal("did not resolve program.go's own declarations; the check is not working")
	}

	for id, decl := range uses {
		if decl == backendFile {
			t.Errorf("%s references %q, which is declared in %s — "+
				"the neutral layer is reaching into the Go backend", neutralFile, id.Name, backendFile)
		}
	}
}

// And the reverse must hold, because a backend consuming nothing from the
// neutral layer would mean the boundary is decorative: compile.go is supposed to
// be a CONSUMER of the program description.
func TestGoBackendConsumesTheNeutralLayer(t *testing.T) {
	uses := resolvedUses(t, backendFile)

	want := map[string]bool{"CompiledProgram": false, "CapabilityRequirement": false, "capabilityKind": false}
	for id, decl := range uses {
		if decl == neutralFile {
			if _, tracked := want[id.Name]; tracked {
				want[id.Name] = true
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("%s does not use %q from %s — the backend is not consuming the neutral description",
				backendFile, name, neutralFile)
		}
	}
}
